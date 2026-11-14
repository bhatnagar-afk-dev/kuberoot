// Package resources defines the types and interfaces for browsing cluster
// resources. Implementations live in subpackages (table, cache, view, ...),
// which depend on this package and never on each other's internals.
package resources

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ResourceType identifies a listable kind of resource on a cluster.
type ResourceType struct {
	Group      string `json:"group"`
	Version    string `json:"version"`
	Resource   string `json:"resource"` // plural, e.g. "deployments"
	Kind       string `json:"kind"`
	Namespaced bool   `json:"namespaced"`
}

// GVR returns the type's GroupVersionResource.
func (t ResourceType) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: t.Group, Version: t.Version, Resource: t.Resource}
}

// ViewRequest asks for a live table of one resource type. The UI chooses
// ViewID, so it can route events for the view before Open returns.
type ViewRequest struct {
	ViewID     string       `json:"viewId"`
	ContextID  string       `json:"contextId"`
	Type       ResourceType `json:"type"`
	Namespaces []string     `json:"namespaces"` // empty means all namespaces
}

// ObjectRef identifies one object for the detail panel.
type ObjectRef struct {
	ContextID string       `json:"contextId"`
	Type      ResourceType `json:"type"`
	Namespace string       `json:"namespace,omitempty"`
	Name      string       `json:"name"`
	// UID selects the object's events (OpenEvents); Describe and YAML
	// ignore it.
	UID string `json:"uid,omitempty"`
}

// YAMLOptions controls how an object is rendered as YAML.
type YAMLOptions struct {
	ManagedFields bool `json:"managedFields"` // keep metadata.managedFields
	RevealSecrets bool `json:"revealSecrets"` // show Secret values instead of their sizes
}

// Column describes one table column, as reported by the server (the same
// columns `kubectl get` prints).
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`             // integer | number | string | boolean | date
	Format   string `json:"format,omitempty"` // e.g. "name"
	Priority int    `json:"priority"`         // >0 is shown only in wide mode
}

// Row is one object in a table.
type Row struct {
	UID             string    `json:"uid"`
	Namespace       string    `json:"namespace,omitempty"`
	Name            string    `json:"name"`
	Created         time.Time `json:"created"`
	ResourceVersion string    `json:"resourceVersion"`
	Cells           []any     `json:"cells"`
}

// RowBatch is a set of changes to one view. A batch with Reset replaces all
// rows the UI holds for the view; the batches that follow it without Reset
// complete a reset that was split into chunks.
type RowBatch struct {
	ViewID  string   `json:"viewId"`
	Reset   bool     `json:"reset"`
	Columns []Column `json:"columns,omitempty"` // set on Reset batches
	Upserts []Row    `json:"upserts"`
	Deletes []string `json:"deletes"` // UIDs
}

// ViewPhase is the lifecycle state of a view.
type ViewPhase string

// View phases.
const (
	ViewLoading ViewPhase = "loading" // waiting for the first list
	ViewReady   ViewPhase = "ready"   // rows are live (possibly partial, see Forbidden)
	ViewStale   ViewPhase = "stale"   // connection lost; rows are the last known
	ViewError   ViewPhase = "error"   // nothing could be listed
)

// ViewStatus describes a view's state.
type ViewStatus struct {
	ViewID string    `json:"viewId"`
	Phase  ViewPhase `json:"phase"`
	// Forbidden lists the namespaces the user may not list in. "" means the
	// cluster-wide (all namespaces or cluster-scoped) list was forbidden.
	Forbidden []string `json:"forbidden,omitempty"`
	// Reason is ReasonNotFound when the server no longer serves the type.
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// ReasonNotFound is the ViewStatus.Reason of a view whose type is gone.
const ReasonNotFound = "NotFound"

// NavItem is an entry in the navigation tree: a resource type, or a folder
// of resource types (one API group of custom resources).
type NavItem struct {
	Title string        `json:"title"`
	Type  *ResourceType `json:"type,omitempty"`  // nil for a folder
	Items []NavItem     `json:"items,omitempty"` // a folder's entries
}

// NavGroup is a top-level section of the navigation tree.
type NavGroup struct {
	Title string    `json:"title"`
	Items []NavItem `json:"items"`
}

// Navigation is the navigation tree of one cluster.
type Navigation struct {
	Groups []NavGroup `json:"groups"`
	// Failed lists the API group versions whose resource types could not be
	// read (e.g. an aggregated API that is down); they are missing from Groups.
	Failed []string `json:"failed,omitempty"`
	// Stale is true when the server's resource types have changed since the
	// tree was built; RefreshNavigation rebuilds it.
	Stale bool `json:"stale,omitempty"`
}

// NavStale is sent when a cluster's resource types change.
type NavStale struct {
	ContextID string `json:"contextId"`
}
