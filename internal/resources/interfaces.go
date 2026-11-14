package resources

import (
	"context"
	"fmt"
	"strings"

	"kuberoot.in/kuberoot/internal/cluster"
)

// Connections is the part of the cluster manager that resource browsing
// needs. *cluster.Manager implements it.
type Connections interface {
	Session(contextID string) (cluster.Session, error)
}

// Sink receives the output of one view. Implementations must be safe for
// concurrent use; calls for one view are never concurrent with each other.
type Sink interface {
	Send(RowBatch)
	Status(ViewStatus)
}

// Catalog lists the resource types a cluster serves. When some API groups
// cannot be read, its methods return the types they could read together with
// a *PartialDiscoveryError.
type Catalog interface {
	ResourceTypes(ctx context.Context) ([]ResourceType, error)
	// Changed reports whether the server's resource types differ from the
	// cached set (e.g. a CRD was added). It never updates the cache.
	Changed(ctx context.Context) (bool, error)
	// Refresh drops the cached set and lists the types again.
	Refresh(ctx context.Context) ([]ResourceType, error)
}

// Navigator arranges resource types into the navigation tree.
type Navigator interface {
	Navigation(types []ResourceType) []NavGroup
}

// Describer produces human-readable describe output for one object, like
// `kubectl describe`.
type Describer interface {
	Describe(ctx context.Context, s cluster.Session, ref ObjectRef) (string, error)
}

// ObjectReader fetches one object and renders it for display.
type ObjectReader interface {
	YAML(ctx context.Context, s cluster.Session, ref ObjectRef, opts YAMLOptions) (string, error)
}

// PartialDiscoveryError reports API group versions whose resource types could
// not be read.
type PartialDiscoveryError struct {
	GroupVersions []string // sorted, e.g. "metrics.k8s.io/v1beta1"
}

func (e *PartialDiscoveryError) Error() string {
	return fmt.Sprintf("unable to read resource types of %s", strings.Join(e.GroupVersions, ", "))
}
