// Package cluster manages live connections to Kubernetes clusters.
package cluster

import "time"

// MaxConnections is the default cap on simultaneously connected contexts.
const MaxConnections = 5

// Phase is the lifecycle state of a connection.
type Phase string

// Connection phases.
const (
	PhaseDisconnected Phase = "disconnected"
	PhaseConnecting   Phase = "connecting"
	PhaseConnected    Phase = "connected"
	PhaseError        Phase = "error"
)

// Reason is a machine-readable cause for an error or transition.
type Reason string

// Reasons for errors and transitions.
const (
	ReasonUnreachable        Reason = "Unreachable"
	ReasonTimeout            Reason = "Timeout"
	ReasonTLSError           Reason = "TLSError"
	ReasonUnauthorized       Reason = "Unauthorized"
	ReasonForbidden          Reason = "Forbidden"
	ReasonExecPluginNotFound Reason = "ExecPluginNotFound"
	ReasonExecPluginFailed   Reason = "ExecPluginFailed"
	ReasonInvalidConfig      Reason = "InvalidConfig"
	ReasonConfigChanged      Reason = "ConfigChanged"
	ReasonConnectionLimit    Reason = "ConnectionLimit"
	ReasonUnknown            Reason = "Unknown"
)

// Status describes a connection. It never contains credentials.
type Status struct {
	ContextID     string    `json:"contextId"`
	Phase         Phase     `json:"phase"`
	ServerVersion string    `json:"serverVersion,omitempty"`
	Identity      string    `json:"identity,omitempty"`
	Reason        Reason    `json:"reason,omitempty"`
	Message       string    `json:"message,omitempty"`
	Detail        string    `json:"detail,omitempty"`
	CheckedAt     time.Time `json:"checkedAt"`
}

// sameVisible reports whether two statuses would look the same in the UI,
// ignoring the check timestamp.
func sameVisible(a, b Status) bool {
	a.CheckedAt, b.CheckedAt = time.Time{}, time.Time{}
	return a == b
}
