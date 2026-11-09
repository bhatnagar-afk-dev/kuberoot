package resources

import "kuberoot.in/kuberoot/internal/cluster"

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
