// Package kubeconfig discovers kubeconfig files and exposes their contexts.
//
// Unlike kubectl, each file is loaded independently so that contexts with the
// same name in different files remain distinct. A context is identified by
// its source file and name (see ContextID).
package kubeconfig

import (
	"crypto/sha256"
	"encoding/hex"
)

// SourceKind says whether a source is a single file or a folder to scan.
type SourceKind string

const (
	KindFile   SourceKind = "file"
	KindFolder SourceKind = "folder"
)

// SourceOrigin says where a source came from.
type SourceOrigin string

const (
	OriginEnv     SourceOrigin = "env"     // listed in $KUBECONFIG
	OriginDefault SourceOrigin = "default" // ~/.kube/config or the ~/.kube folder
	OriginUser    SourceOrigin = "user"    // added by the user
)

// SourceSpec describes a source to load.
type SourceSpec struct {
	Path   string       `json:"path"`
	Kind   SourceKind   `json:"kind"`
	Origin SourceOrigin `json:"origin"`
	Hidden bool         `json:"hidden"`
}

// Source is a loaded SourceSpec.
type Source struct {
	SourceSpec
	Contexts int    `json:"contexts"`        // number of contexts contributed
	Missing  bool   `json:"missing"`         // path does not exist
	Error    string `json:"error,omitempty"` // load error, if any
}

// AuthType classifies how a context authenticates.
type AuthType string

const (
	AuthToken        AuthType = "token"
	AuthCert         AuthType = "cert"
	AuthBasic        AuthType = "basic"
	AuthExec         AuthType = "exec"
	AuthAuthProvider AuthType = "authProvider"
	AuthNone         AuthType = "none"
)

// Context is a kubeconfig context as presented to the UI. It never contains
// credentials.
type Context struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Cluster   string   `json:"cluster"`
	User      string   `json:"user"`
	Namespace string   `json:"namespace"`
	Server    string   `json:"server"`
	Source    string   `json:"source"` // absolute path of the defining file
	AuthType  AuthType `json:"authType"`
	ExecCmd   string   `json:"execCmd,omitempty"`
	IsCurrent bool     `json:"isCurrent"`

	// Fingerprint changes whenever the context, its cluster or its user
	// change on disk. Used to detect config changes for live connections.
	Fingerprint string `json:"-"`
}

// ContextID returns the stable identifier for a context in a file.
func ContextID(absPath, contextName string) string {
	h := sha256.Sum256([]byte(absPath + "\x00" + contextName))
	return hex.EncodeToString(h[:8])
}
