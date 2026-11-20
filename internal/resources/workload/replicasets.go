package workload

import (
	"fmt"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/redact"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/cache"
	"kuberoot.in/kuberoot/internal/resources/table"
)

// Subscriber is the part of the informer cache a Deployment's membership
// uses. *cache.Informers implements it.
type Subscriber interface {
	Subscribe(s cluster.Session, k cache.Key, h cache.Handler) (cache.Subscription, error)
}

var replicaSetsGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}

// replicaSets is a Deployment's Membership: the pods whose controller is one
// of the ReplicaSets the Deployment controls. The ReplicaSets are watched
// with the Deployment's selector, which every one of them carries.
type replicaSets struct {
	sel        labels.Selector
	deployment types.UID
	namespace  string
	changed    chan struct{}
	sub        cache.Subscription

	mu     sync.Mutex
	owned  map[types.UID]bool // ReplicaSets controlled by the Deployment
	synced bool
	err    error
}

func watchReplicaSets(s cluster.Session, subs Subscriber, namespace string, sel labels.Selector, deployment types.UID) (*replicaSets, error) {
	r := &replicaSets{
		sel:        sel,
		deployment: deployment,
		namespace:  namespace,
		changed:    make(chan struct{}, 1),
		owned:      map[types.UID]bool{},
	}
	k := cache.Key{ContextID: s.ContextID, GVR: replicaSetsGVR, Namespace: namespace, LabelSelector: sel.String()}
	sub, err := subs.Subscribe(s, k, r)
	if err != nil {
		return nil, fmt.Errorf("watch replicasets: %w", err)
	}
	r.sub = sub
	return r, nil
}

func (r *replicaSets) Selector() labels.Selector { return r.sel }

func (r *replicaSets) Owns(pod metav1.Object) bool {
	uid, kind := controllerUID(pod)
	if kind != "ReplicaSet" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.owned[uid]
}

func (r *replicaSets) Changed() <-chan struct{} { return r.changed }

func (r *replicaSets) State() (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.synced, r.err
}

func (r *replicaSets) Close() { r.sub.Close() }

// notify tells the reader that something changed, without blocking: one
// pending signal is enough, since the reader checks everything again.
func (r *replicaSets) notify() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

// Upsert implements cache.Handler.
func (r *replicaSets) Upsert(o *table.RowObject) {
	uid, _ := controllerUID(o)
	owned := uid == r.deployment
	r.mu.Lock()
	was := r.owned[o.UID]
	if owned {
		r.owned[o.UID] = true
	} else {
		delete(r.owned, o.UID) // e.g. orphaned
	}
	r.mu.Unlock()
	if owned != was {
		r.notify()
	}
}

// Delete implements cache.Handler.
func (r *replicaSets) Delete(o *table.RowObject) {
	r.mu.Lock()
	was := r.owned[o.UID]
	delete(r.owned, o.UID)
	r.mu.Unlock()
	if was {
		r.notify()
	}
}

// Synced implements cache.Handler.
func (r *replicaSets) Synced([]resources.Column) {
	r.mu.Lock()
	r.synced = true
	r.mu.Unlock()
	r.notify()
}

// Error implements cache.Handler.
func (r *replicaSets) Error(err error) {
	switch {
	case err == nil:
	case apierrors.IsForbidden(err):
		err = fmt.Errorf("no permission to list replicasets in %q, which is needed to find the deployment's pods", r.namespace)
	default:
		err = fmt.Errorf("could not list replicasets to find the deployment's pods: %s", redact.String(err.Error()))
	}
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
	r.notify()
}
