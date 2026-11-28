// Package workload finds the pods of a workload (Deployment, StatefulSet,
// DaemonSet, ReplicaSet, Job) or Service.
//
// A workload's pods are the pods it owns. The API server can filter by label
// but not by owner, so a Membership has two parts: the workload's label
// selector, which narrows LIST and WATCH on the server and never misses an
// owned pod (controllers only adopt pods that match), and an ownership check
// on the client, which drops pods that match the selector but belong to
// something else. A Service owns nothing: its pods are the pods its selector
// matches, so its Membership is the selector alone.
package workload

import (
	"context"
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
)

// ErrNoPods is returned for objects that do not select pods: unsupported
// types, empty selectors and Services without a selector.
var ErrNoPods = errors.New("does not select pods")

// PodsType is core/v1 pods.
var PodsType = resources.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}

// PodMatcher finds the pods of a workload or Service. It returns an error
// wrapping ErrNoPods if ref's type does not manage pods or its selector is
// empty.
type PodMatcher interface {
	Match(ctx context.Context, s cluster.Session, ref resources.ObjectRef) (Membership, error)
}

// Membership is the live set of pods that belong to one target. It is safe
// for concurrent use.
type Membership interface {
	// Selector selects a superset of the target's pods, for LIST and WATCH.
	Selector() labels.Selector
	// Owns reports whether pod belongs to the target. Always true for a
	// Service.
	Owns(pod metav1.Object) bool
	// Changed receives a value when Owns' answers or State may have changed
	// (a Deployment's ReplicaSets changed). It has one reader.
	Changed() <-chan struct{}
	// State reports whether Owns can decide yet, and why it cannot.
	State() (synced bool, err error)
	// Close releases what the membership watches.
	Close()
}

// ownership says how a type's pods are matched.
type ownership int

const (
	ownsDirectly     ownership = iota // the pod's controller is the object
	ownsReplicaSets                   // the pod's controller is one of the object's ReplicaSets
	selectsByService                  // the Service's selector alone
)

// kinds are the supported types, by group and resource.
var kinds = map[groupResource]ownership{
	{"apps", "deployments"}:  ownsReplicaSets,
	{"apps", "statefulsets"}: ownsDirectly,
	{"apps", "daemonsets"}:   ownsDirectly,
	{"apps", "replicasets"}:  ownsDirectly,
	{"batch", "jobs"}:        ownsDirectly,
	{"", "services"}:         selectsByService,
}

type groupResource struct{ group, resource string }

// SelectsPods reports whether objects of type t can have pods.
func SelectsPods(t resources.ResourceType) bool {
	_, ok := kinds[groupResource{t.Group, t.Resource}]
	return ok
}

// Matcher is the PodMatcher for the supported types. It reads the object
// with the session's dynamic client; a Deployment's ReplicaSets are watched
// through Subscriber.
type Matcher struct {
	Subscriber Subscriber
}

// Match implements PodMatcher.
func (m Matcher) Match(ctx context.Context, s cluster.Session, ref resources.ObjectRef) (Membership, error) {
	how, ok := kinds[groupResource{ref.Type.Group, ref.Type.Resource}]
	if !ok {
		return nil, noPods(ref.Type.Kind)
	}
	obj, err := s.Dynamic.Resource(ref.Type.GVR()).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	sel, err := selector(obj, how)
	if err != nil {
		return nil, err
	}
	switch how {
	case ownsReplicaSets:
		return watchReplicaSets(s, m.Subscriber, ref.Namespace, sel, obj.GetUID())
	case ownsDirectly:
		return newFixed(sel, obj.GetUID()), nil
	default:
		return newFixed(sel, ""), nil
	}
}

// selector returns obj's pod selector, or ErrNoPods if it selects nothing.
func selector(obj *unstructured.Unstructured, how ownership) (labels.Selector, error) {
	if how == selectsByService {
		// An ExternalName Service has no endpoints, whatever its selector.
		if t, _, _ := unstructured.NestedString(obj.Object, "spec", "type"); t == "ExternalName" {
			return nil, noPods(obj.GetKind())
		}
		set, _, err := unstructured.NestedStringMap(obj.Object, "spec", "selector")
		if err != nil {
			return nil, fmt.Errorf("read spec.selector: %w", err)
		}
		if len(set) == 0 {
			return nil, noPods(obj.GetKind())
		}
		return labels.SelectorFromSet(set), nil
	}
	raw, found, err := unstructured.NestedMap(obj.Object, "spec", "selector")
	if err != nil {
		return nil, fmt.Errorf("read spec.selector: %w", err)
	}
	if !found {
		return nil, noPods(obj.GetKind())
	}
	var ls metav1.LabelSelector
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &ls); err != nil {
		return nil, fmt.Errorf("read spec.selector: %w", err)
	}
	sel, err := metav1.LabelSelectorAsSelector(&ls)
	if err != nil {
		return nil, fmt.Errorf("read spec.selector: %w", err)
	}
	// An empty selector would match every pod in the namespace.
	if sel.Empty() {
		return nil, noPods(obj.GetKind())
	}
	return sel, nil
}

func noPods(kind string) error {
	if kind == "" {
		return ErrNoPods
	}
	return fmt.Errorf("this %s %w", strings.ToLower(kind), ErrNoPods)
}

// controllerUID returns the UID of pod's controller, or "".
func controllerUID(pod metav1.Object) (types.UID, string) {
	if c := metav1.GetControllerOfNoCopy(pod); c != nil {
		return c.UID, c.Kind
	}
	return "", ""
}

// fixed is a Membership whose owner never changes.
type fixed struct {
	sel     labels.Selector
	owner   types.UID // "" accepts every pod (a Service)
	changed chan struct{}
}

func newFixed(sel labels.Selector, owner types.UID) *fixed {
	return &fixed{sel: sel, owner: owner, changed: make(chan struct{})}
}

func (f *fixed) Selector() labels.Selector { return f.sel }

func (f *fixed) Owns(pod metav1.Object) bool {
	if f.owner == "" {
		return true
	}
	uid, _ := controllerUID(pod)
	return uid == f.owner
}

func (f *fixed) Changed() <-chan struct{} { return f.changed } // never fires
func (f *fixed) State() (bool, error)     { return true, nil }
func (f *fixed) Close()                   {}
