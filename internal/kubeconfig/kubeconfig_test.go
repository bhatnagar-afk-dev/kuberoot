package kubeconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	api "k8s.io/client-go/tools/clientcmd/api"
)

// kubeconfigYAML returns a kubeconfig with one context per name, each with
// its own cluster and token user.
func kubeconfigYAML(current string, names ...string) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: Config\ncurrent-context: " + current + "\nclusters:\n")
	for _, n := range names {
		fmt.Fprintf(&b, "- name: %s\n  cluster:\n    server: https://%s.example:6443\n", n, n)
	}
	b.WriteString("users:\n")
	for _, n := range names {
		fmt.Fprintf(&b, "- name: %s\n  user:\n    token: tok-%s\n", n, n)
	}
	b.WriteString("contexts:\n")
	for _, n := range names {
		fmt.Fprintf(&b, "- name: %s\n  context:\n    cluster: %s\n    user: %s\n    namespace: ns-%s\n", n, n, n, n)
	}
	return b.String()
}

func write(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func names(cs []Context) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = filepath.Base(c.Source) + "/" + c.Name
	}
	return out
}

func TestDefaultSources(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "home", "me")
	kube := filepath.Join(home, ".kube")

	t.Run("unset KUBECONFIG on linux", func(t *testing.T) {
		got := DefaultSources("", home, "linux")
		want := []SourceSpec{
			{Path: filepath.Join(kube, "config"), Kind: KindFile, Origin: OriginDefault},
			{Path: kube, Kind: KindFolder, Origin: OriginDefault},
			{Path: "/etc/rancher/k3s/k3s.yaml", Kind: KindFile, Origin: OriginDefault},
			{Path: "/etc/rancher/rke2/rke2.yaml", Kind: KindFile, Origin: OriginDefault},
			{Path: "/var/snap/microk8s/current/credentials/client.config", Kind: KindFile, Origin: OriginDefault},
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("got  %v\nwant %v", got, want)
		}
	})

	t.Run("darwin adds OrbStack", func(t *testing.T) {
		got := DefaultSources("", home, "darwin")
		last := got[len(got)-1]
		if last.Path != filepath.Join(home, ".orbstack", "k8s", "config.yml") {
			t.Errorf("last = %v", last)
		}
	})

	t.Run("windows has no distro files", func(t *testing.T) {
		if got := DefaultSources("", home, "windows"); len(got) != 2 {
			t.Errorf("got %v", got)
		}
	})

	t.Run("KUBECONFIG list replaces ~/.kube/config", func(t *testing.T) {
		env := strings.Join([]string{"~/a.yaml", "", "/abs/b.yaml", "~/a.yaml"}, string(os.PathListSeparator))
		got := DefaultSources(env, home, "windows")
		if len(got) != 3 {
			t.Fatalf("got %v", got)
		}
		if got[0].Path != filepath.Join(home, "a.yaml") || got[0].Origin != OriginEnv {
			t.Errorf("got[0] = %v", got[0])
		}
		if got[2].Kind != KindFolder {
			t.Errorf("want ~/.kube folder last, got %v", got[2])
		}
	})
}

func TestLoadDuplicateNamesStayDistinct(t *testing.T) {
	dir := t.TempDir()
	a := write(t, filepath.Join(dir, "a.yaml"), kubeconfigYAML("kind", "kind"))
	b := write(t, filepath.Join(dir, "b.yaml"), kubeconfigYAML("kind", "kind"))

	st := NewStore("test")
	st.SetSources([]SourceSpec{
		{Path: a, Kind: KindFile, Origin: OriginUser},
		{Path: b, Kind: KindFile, Origin: OriginUser},
	})
	cs := st.Contexts()
	if len(cs) != 2 || cs[0].ID == cs[1].ID {
		t.Fatalf("contexts = %+v", cs)
	}
	if cs[0].ID != ContextID(a, "kind") {
		t.Error("ID is not derived from path and name")
	}
	if !cs[0].IsCurrent || cs[0].Namespace != "ns-kind" || cs[0].AuthType != AuthToken {
		t.Errorf("context fields = %+v", cs[0])
	}
}

func TestLoadSourcesAndErrors(t *testing.T) {
	dir := t.TempDir()
	kube := filepath.Join(dir, ".kube")
	write(t, filepath.Join(kube, "config"), kubeconfigYAML("main", "main"))
	write(t, filepath.Join(kube, "prod.yaml"), kubeconfigYAML("", "prod-eu", "prod-us"))
	write(t, filepath.Join(kube, "configs", "staging"), kubeconfigYAML("", "staging")) // subfolder, no extension
	write(t, filepath.Join(kube, "cache", "x.yaml"), kubeconfigYAML("", "cached"))     // skipped dir
	write(t, filepath.Join(kube, ".hidden.yaml"), kubeconfigYAML("", "hidden"))        // hidden file
	write(t, filepath.Join(kube, "notes.txt"), "just some notes\n")                    // not a kubeconfig
	write(t, filepath.Join(kube, "broken.yaml"), "contexts: [[[")                      // invalid YAML
	write(t, filepath.Join(kube, "deep", "nested", "x.yaml"), kubeconfigYAML("", "deep"))
	broken := write(t, filepath.Join(dir, "user-broken.yaml"), "contexts: [[[")

	st := NewStore("test")
	st.SetSources([]SourceSpec{
		{Path: filepath.Join(kube, "config"), Kind: KindFile, Origin: OriginDefault},
		{Path: kube, Kind: KindFolder, Origin: OriginDefault},
		{Path: "/definitely/missing/k3s.yaml", Kind: KindFile, Origin: OriginDefault},
		{Path: filepath.Join(dir, "gone.yaml"), Kind: KindFile, Origin: OriginUser},
		{Path: broken, Kind: KindFile, Origin: OriginUser},
	})

	got := strings.Join(names(st.Contexts()), ",")
	want := "config/main,prod.yaml/prod-eu,prod.yaml/prod-us,staging/staging"
	if got != want {
		t.Errorf("contexts = %s\nwant       %s", got, want)
	}

	srcs := st.Sources()
	if srcs[0].Contexts != 1 || srcs[1].Contexts != 3 {
		t.Errorf("config listed once under the file source; counts = %d, %d", srcs[0].Contexts, srcs[1].Contexts)
	}
	if !srcs[2].Missing || srcs[2].Error != "" {
		t.Errorf("missing default source = %+v, want missing without error", srcs[2])
	}
	if !srcs[3].Missing || srcs[3].Error == "" {
		t.Errorf("missing user source = %+v, want error", srcs[3])
	}
	if !strings.Contains(srcs[4].Error, "invalid kubeconfig") {
		t.Errorf("broken user file error = %q", srcs[4].Error)
	}
}

func TestLoadHiddenSource(t *testing.T) {
	dir := t.TempDir()
	p := write(t, filepath.Join(dir, "a.yaml"), kubeconfigYAML("a", "a"))
	st := NewStore("test")
	st.SetSources([]SourceSpec{{Path: p, Kind: KindFile, Origin: OriginDefault, Hidden: true}})
	if len(st.Contexts()) != 0 {
		t.Error("hidden source contributed contexts")
	}
}

func TestLoadEnvCrossFileReferences(t *testing.T) {
	dir := t.TempDir()
	// Context in one file, cluster and user in another: valid for kubectl.
	ctxFile := write(t, filepath.Join(dir, "contexts.yaml"), `apiVersion: v1
kind: Config
contexts:
- name: split
  context: {cluster: shared, user: shared}
`)
	credFile := write(t, filepath.Join(dir, "creds.yaml"), `apiVersion: v1
kind: Config
clusters:
- name: shared
  cluster: {server: "https://shared.example:6443"}
users:
- name: shared
  user: {token: abc}
`)
	st := NewStore("test")
	st.SetSources([]SourceSpec{
		{Path: ctxFile, Kind: KindFile, Origin: OriginEnv},
		{Path: credFile, Kind: KindFile, Origin: OriginEnv},
	})
	cs := st.Contexts()
	if len(cs) != 1 || cs[0].Server != "https://shared.example:6443" {
		t.Fatalf("contexts = %+v", cs)
	}
	rc, err := st.RESTConfig(cs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if rc.BearerToken != "abc" || rc.QPS != defaultQPS || rc.UserAgent != "test" {
		t.Errorf("rest config = host %s token %q qps %v ua %q", rc.Host, rc.BearerToken, rc.QPS, rc.UserAgent)
	}
}

func TestRelativePathsResolved(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "certs", "ca.crt"), "not really a cert")
	p := write(t, filepath.Join(dir, "rel.yaml"), `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster: {server: "https://c:6443", certificate-authority: certs/ca.crt}
users:
- name: u
  user: {client-certificate: certs/client.crt, client-key: certs/client.key}
contexts:
- name: rel
  context: {cluster: c, user: u}
`)
	st := NewStore("test")
	st.SetSources([]SourceSpec{{Path: p, Kind: KindFile, Origin: OriginUser}})
	cs := st.Contexts()
	if len(cs) != 1 || cs[0].AuthType != AuthCert {
		t.Fatalf("contexts = %+v", cs)
	}
	e := st.snap.entries[cs[0].ID]
	if got := e.cfg.Clusters["c"].CertificateAuthority; got != filepath.Join(dir, "certs", "ca.crt") {
		t.Errorf("certificate-authority = %q, want absolute", got)
	}
}

func TestClassifyAuth(t *testing.T) {
	tests := []struct {
		auth     *api.AuthInfo
		want     AuthType
		wantExec string
	}{
		{nil, AuthNone, ""},
		{&api.AuthInfo{Exec: &api.ExecConfig{Command: "/usr/local/bin/aws"}}, AuthExec, "aws"},
		{&api.AuthInfo{Exec: &api.ExecConfig{Command: "kubelogin"}}, AuthExec, "kubelogin"},
		{&api.AuthInfo{AuthProvider: &api.AuthProviderConfig{Name: "oidc"}}, AuthAuthProvider, ""},
		{&api.AuthInfo{ClientCertificateData: []byte("x")}, AuthCert, ""},
		{&api.AuthInfo{TokenFile: "/var/run/token"}, AuthToken, ""},
		{&api.AuthInfo{Username: "admin"}, AuthBasic, ""},
		{&api.AuthInfo{}, AuthNone, ""},
	}
	for _, tt := range tests {
		got, exec := classifyAuth(tt.auth)
		if got != tt.want || exec != tt.wantExec {
			t.Errorf("classifyAuth(%+v) = %s, %q; want %s, %q", tt.auth, got, exec, tt.want, tt.wantExec)
		}
	}
}

func TestRESTConfig(t *testing.T) {
	dir := t.TempDir()
	p := write(t, filepath.Join(dir, "k.yaml"), `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster: {server: "https://c:6443"}
users:
- name: exec
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: some-plugin
      interactiveMode: Always
- name: gcp
  user:
    auth-provider: {name: gcp}
contexts:
- name: exec
  context: {cluster: c, user: exec}
- name: gcp
  context: {cluster: c, user: gcp}
- name: orphan
  context: {cluster: missing, user: exec}
`)
	st := NewStore("test")
	st.SetSources([]SourceSpec{{Path: p, Kind: KindFile, Origin: OriginUser}})

	rc, err := st.RESTConfig(ContextID(p, "exec"))
	if err != nil {
		t.Fatal(err)
	}
	if rc.ExecProvider == nil || rc.ExecProvider.InteractiveMode != api.NeverExecInteractiveMode || !rc.ExecProvider.StdinUnavailable {
		t.Errorf("exec provider = %+v, want non-interactive", rc.ExecProvider)
	}

	var legacy *LegacyAuthProviderError
	if _, err := st.RESTConfig(ContextID(p, "gcp")); !errors.As(err, &legacy) || legacy.Provider != "gcp" {
		t.Errorf("gcp err = %v, want LegacyAuthProviderError", err)
	}
	if _, err := st.RESTConfig(ContextID(p, "orphan")); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Errorf("orphan err = %v", err)
	}
	if _, err := st.RESTConfig("nope"); !errors.Is(err, ErrContextNotFound) {
		t.Errorf("unknown err = %v", err)
	}
	// The stored config must not be mutated by RESTConfig.
	if st.snap.entries[ContextID(p, "exec")].cfg.AuthInfos["exec"].Exec.InteractiveMode != api.AlwaysExecInteractiveMode {
		t.Error("RESTConfig mutated stored config")
	}
}

func TestReloadDetectsChanges(t *testing.T) {
	dir := t.TempDir()
	p := write(t, filepath.Join(dir, "k.yaml"), kubeconfigYAML("a", "a"))
	st := NewStore("test")
	st.SetSources([]SourceSpec{{Path: p, Kind: KindFile, Origin: OriginUser}})
	before := st.Contexts()[0].Fingerprint

	if st.Reload() {
		t.Error("Reload reported a change with nothing changed")
	}
	write(t, p, strings.Replace(kubeconfigYAML("a", "a"), "tok-a", "tok-rotated", 1))
	if !st.Reload() {
		t.Error("Reload missed a credential change")
	}
	if st.Contexts()[0].Fingerprint == before {
		t.Error("fingerprint did not change")
	}
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestWatch(t *testing.T) {
	dir := t.TempDir()
	kube := filepath.Join(dir, ".kube")
	cfg := write(t, filepath.Join(kube, "config"), kubeconfigYAML("a", "a"))

	st := NewStore("test")
	st.SetSources([]SourceSpec{
		{Path: cfg, Kind: KindFile, Origin: OriginDefault},
		{Path: kube, Kind: KindFolder, Origin: OriginDefault},
	})
	changed := make(chan struct{}, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = st.Watch(ctx, func() { changed <- struct{}{} }) }()
	time.Sleep(200 * time.Millisecond) // let the watcher register

	// Atomic replace, as editors and cloud CLIs do.
	tmp := write(t, filepath.Join(kube, ".config.tmp"), kubeconfigYAML("a", "a", "b"))
	if err := os.Rename(tmp, cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, changed, "atomic replace")
	if n := len(st.Contexts()); n != 2 {
		t.Fatalf("after replace: %d contexts", n)
	}

	// New file in a new subfolder of the scanned folder.
	write(t, filepath.Join(kube, "clusters", "c.yaml"), kubeconfigYAML("", "c"))
	waitFor(t, changed, "new subfolder file")
	// The subfolder is now watched too.
	write(t, filepath.Join(kube, "clusters", "d.yaml"), kubeconfigYAML("", "d"))
	waitFor(t, changed, "file in newly watched subfolder")
	if n := len(st.Contexts()); n != 4 {
		t.Fatalf("after additions: %d contexts: %v", n, names(st.Contexts()))
	}

	// Irrelevant churn must not trigger a change.
	write(t, filepath.Join(kube, "cache", "discovery.json"), "{}")
	select {
	case <-changed:
		t.Error("cache write triggered a change")
	case <-time.After(700 * time.Millisecond):
	}
}
