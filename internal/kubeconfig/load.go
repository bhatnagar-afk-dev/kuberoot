package kubeconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
)

// Folder scan limits. Kubeconfigs are small; these bound the cost of scanning
// a folder that turns out to hold something else.
const (
	maxFileSize       = 1 << 20
	maxFilesPerFolder = 200
)

// skipDirs are subfolders of a scanned folder that never hold kubeconfigs.
var skipDirs = map[string]bool{"cache": true, "http-cache": true}

// distroFiles are kubeconfigs written by local Kubernetes distributions
// outside ~/.kube, keyed by GOOS. Paths starting with "~" are relative to home.
var distroFiles = map[string][]string{
	"darwin": {
		"~/.orbstack/k8s/config.yml", // OrbStack
	},
	"linux": {
		"/etc/rancher/k3s/k3s.yaml",                            // k3s
		"/etc/rancher/rke2/rke2.yaml",                          // RKE2
		"/var/snap/microk8s/current/credentials/client.config", // MicroK8s
	},
}

// DefaultSources returns the sources KubeRoot uses out of the box, in
// priority order: the files in $KUBECONFIG (or ~/.kube/config when unset),
// a scan of ~/.kube, and kubeconfigs written by local distributions on goos.
func DefaultSources(kubeconfigEnv, home, goos string) []SourceSpec {
	var specs []SourceSpec
	seen := map[string]bool{}
	for _, p := range filepath.SplitList(kubeconfigEnv) {
		if p == "" {
			continue
		}
		p = absPath(p, home)
		if seen[p] {
			continue
		}
		seen[p] = true
		specs = append(specs, SourceSpec{Path: p, Kind: KindFile, Origin: OriginEnv})
	}
	if home == "" {
		return specs
	}
	kubeDir := filepath.Join(home, ".kube")
	if len(specs) == 0 {
		specs = append(specs, SourceSpec{Path: filepath.Join(kubeDir, "config"), Kind: KindFile, Origin: OriginDefault})
	}
	specs = append(specs, SourceSpec{Path: kubeDir, Kind: KindFolder, Origin: OriginDefault})
	for _, p := range distroFiles[goos] {
		specs = append(specs, SourceSpec{Path: absPath(p, home), Kind: KindFile, Origin: OriginDefault})
	}
	return specs
}

func absPath(p, home string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		p = filepath.Join(home, p[1:])
	}
	if a, err := filepath.Abs(p); err == nil {
		return filepath.Clean(a)
	}
	return filepath.Clean(p)
}

// ScanDirs returns dir and the immediate subfolders a folder scan visits.
func ScanDirs(dir string) []string {
	dirs := []string{dir}
	des, err := os.ReadDir(dir)
	if err != nil {
		return dirs
	}
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, ".") || skipDirs[name] {
			continue
		}
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() { // follows symlinks
			dirs = append(dirs, p)
		}
	}
	return dirs
}

// entry is everything needed to build a client for one context.
type entry struct {
	name string
	cfg  *api.Config // minimal config containing only this context
}

// snapshot is the result of loading all sources.
type snapshot struct {
	sources  []Source
	contexts []Context
	entries  map[string]*entry
}

func load(specs []SourceSpec) snapshot {
	snap := snapshot{entries: map[string]*entry{}}
	seenFiles := map[string]bool{}
	// A hidden file stays hidden even if a folder scan would find it.
	for _, s := range specs {
		if s.Hidden && s.Kind == KindFile {
			seenFiles[s.Path] = true
		}
	}

	// Files listed in $KUBECONFIG may reference clusters/users defined in other
	// files of the list, as kubectl allows. Merge them once for lookups.
	var envFiles []string
	for _, s := range specs {
		if s.Origin == OriginEnv && s.Kind == KindFile {
			envFiles = append(envFiles, s.Path)
		}
	}
	var envMerged *api.Config
	if len(envFiles) > 1 {
		rules := &clientcmd.ClientConfigLoadingRules{Precedence: envFiles}
		if merged, err := rules.Load(); err == nil {
			envMerged = merged
		}
	}

	for _, spec := range specs {
		src := Source{SourceSpec: spec}
		if spec.Hidden {
			snap.sources = append(snap.sources, src)
			continue
		}
		fi, err := os.Stat(spec.Path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			src.Missing = true
			if spec.Origin == OriginUser {
				src.Error = "path not found"
			}
		case err != nil:
			src.Error = err.Error()
		case spec.Kind == KindFile && fi.IsDir():
			src.Error = "expected a file but found a folder"
		case spec.Kind == KindFolder && !fi.IsDir():
			src.Error = "expected a folder but found a file"
		case spec.Kind == KindFile:
			var fallback *api.Config
			if spec.Origin == OriginEnv {
				fallback = envMerged
			}
			n, err := snap.addFile(spec.Path, fallback, seenFiles)
			src.Contexts = n
			if err != nil {
				src.Error = err.Error()
			}
		default:
			src.Contexts, src.Error = snap.addFolder(spec.Path, seenFiles)
		}
		snap.sources = append(snap.sources, src)
	}

	return snap
}

// addFolder scans dir and its immediate subfolders. Files that fail to parse
// are not kubeconfigs and are skipped silently.
func (snap *snapshot) addFolder(dir string, seen map[string]bool) (int, string) {
	if _, err := os.ReadDir(dir); err != nil {
		return 0, err.Error()
	}
	total := 0
	for _, d := range ScanDirs(dir) {
		total += snap.scanDir(d, seen)
	}
	return total, ""
}

func (snap *snapshot) scanDir(dir string, seen map[string]bool) int {
	des, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	total, scanned := 0, 0
	for _, de := range des {
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, de.Name())
		fi, err := os.Stat(p) // follows symlinks
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFileSize || fi.Size() == 0 {
			continue
		}
		if scanned++; scanned > maxFilesPerFolder {
			break
		}
		n, _ := snap.addFile(p, nil, seen)
		total += n
	}
	return total
}

// addFile loads one kubeconfig file and records its contexts. fallback, if
// non-nil, supplies clusters and users missing from the file.
func (snap *snapshot) addFile(path string, fallback *api.Config, seen map[string]bool) (int, error) {
	if seen[path] {
		return 0, nil
	}
	cfg, err := clientcmd.LoadFromFile(path)
	switch {
	case errors.Is(err, fs.ErrPermission):
		return 0, errors.New("permission denied: copy the file into ~/.kube or make it readable by your user")
	case err != nil:
		return 0, fmt.Errorf("invalid kubeconfig: %w", err)
	}
	if len(cfg.Contexts) == 0 {
		return 0, nil
	}
	seen[path] = true
	if err := clientcmd.ResolveLocalPaths(cfg); err != nil {
		return 0, fmt.Errorf("resolve paths: %w", err)
	}

	names := make([]string, 0, len(cfg.Contexts))
	for name := range cfg.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		kctx := cfg.Contexts[name]
		cluster := cfg.Clusters[kctx.Cluster]
		auth := cfg.AuthInfos[kctx.AuthInfo]
		if fallback != nil {
			if cluster == nil {
				cluster = fallback.Clusters[kctx.Cluster]
			}
			if auth == nil {
				auth = fallback.AuthInfos[kctx.AuthInfo]
			}
		}

		minimal := api.NewConfig()
		minimal.CurrentContext = name
		minimal.Contexts[name] = kctx.DeepCopy()
		if cluster != nil {
			minimal.Clusters[kctx.Cluster] = cluster.DeepCopy()
		}
		if auth != nil {
			minimal.AuthInfos[kctx.AuthInfo] = auth.DeepCopy()
		}

		c := Context{
			ID:          ContextID(path, name),
			Name:        name,
			Cluster:     kctx.Cluster,
			User:        kctx.AuthInfo,
			Namespace:   kctx.Namespace,
			Source:      path,
			IsCurrent:   cfg.CurrentContext == name,
			Fingerprint: fingerprint(minimal),
		}
		if cluster != nil {
			c.Server = cluster.Server
		}
		c.AuthType, c.ExecCmd = classifyAuth(auth)

		snap.contexts = append(snap.contexts, c)
		snap.entries[c.ID] = &entry{name: name, cfg: minimal}
	}
	return len(names), nil
}

func classifyAuth(a *api.AuthInfo) (AuthType, string) {
	switch {
	case a == nil:
		return AuthNone, ""
	case a.Exec != nil:
		return AuthExec, filepath.Base(a.Exec.Command)
	case a.AuthProvider != nil:
		return AuthAuthProvider, ""
	case len(a.ClientCertificateData) > 0 || a.ClientCertificate != "":
		return AuthCert, ""
	case a.Token != "" || a.TokenFile != "":
		return AuthToken, ""
	case a.Username != "":
		return AuthBasic, ""
	default:
		return AuthNone, ""
	}
}

func fingerprint(cfg *api.Config) string {
	b, err := clientcmd.Write(*cfg)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
