package workload

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/cache"
	"kuberoot.in/kuberoot/internal/resources/table"
)

var (
	deployments  = resources.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
	statefulSets = resources.ResourceType{Group: "apps", Version: "v1", Resource: "statefulsets", Kind: "StatefulSet", Namespaced: true}
	jobs         = resources.ResourceType{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespaced: true}
	services     = resources.ResourceType{Version: "v1", Resource: "services", Kind: "Service", Namespaced: true}
	configMaps   = resources.ResourceType{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true}
)

func object(t resources.ResourceType, name, uid string, spec map[string]any) *unstructured.Unstructured {
	gv := schema.GroupVersion{Group: t.Group, Version: t.Version}
	o := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gv.String(),
		"kind":       t.Kind,
		"metadata":   map[string]any{"name": name, "namespace": "web", "uid": uid},
	}}
	if spec != nil {
		o.Object["spec"] = spec
	}
	return o
}

func labelSelector(matchLabels map[string]any) map[string]any {
	return map[string]any{"selector": map[string]any{"matchLabels": matchLabels}}
}

// fakeSubscriber records the subscription a Deployment's membership makes.
type fakeSubscriber struct {
	key     cache.Key
	handler cache.Handler
	closed  bool
	err     error
}

func (f *fakeSubscriber) Subscribe(_ cluster.Session, k cache.Key, h cache.Handler) (cache.Subscription, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.key, f.handler = k, h
	return f, nil
}

func (f *fakeSubscriber) Close() { f.closed = true }

func match(t *testing.T, subs Subscriber, ref resources.ObjectRef, objs ...runtime.Object) (Membership, error) {
	t.Helper()
	gvrs := map[schema.GroupVersionResource]string{}
	for _, rt := range []resources.ResourceType{deployments, statefulSets, jobs, services, configMaps} {
		gvrs[rt.GVR()] = rt.Kind + "List"
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrs, objs...)
	s := cluster.Session{ContextID: "c1", Ctx: context.Background(), Dynamic: dyn}
	ref.ContextID, ref.Namespace = "c1", "web"
	return Matcher{Subscriber: subs}.Match(context.Background(), s, ref)
}

// pod returns pod metadata whose controller is (kind, uid); empty kind
// means no controller.
func pod(kind, uid string) metav1.Object {
	m := &metav1.ObjectMeta{Name: "p", Namespace: "web"}
	if kind != "" {
		yes := true
		m.OwnerReferences = []metav1.OwnerReference{{Kind: kind, Name: "owner", UID: types.UID(uid), Controller: &yes}}
	}
	return m
}

func TestSelectors(t *testing.T) {
	cases := []struct {
		name string
		typ  resources.ResourceType
		spec map[string]any
		want string
	}{
		{"matchLabels", statefulSets, labelSelector(map[string]any{"app": "web", "tier": "db"}), "app=web,tier=db"},
		{"matchExpressions", deployments, map[string]any{"selector": map[string]any{
			"matchLabels":      map[string]any{"app": "web"},
			"matchExpressions": []any{map[string]any{"key": "track", "operator": "In", "values": []any{"stable", "canary"}}},
		}}, "app=web,track in (canary,stable)"},
		{"job", jobs, labelSelector(map[string]any{"batch.kubernetes.io/controller-uid": "u1"}), "batch.kubernetes.io/controller-uid=u1"},
		{"service", services, map[string]any{"selector": map[string]any{"app": "web"}}, "app=web"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := match(t, &fakeSubscriber{}, resources.ObjectRef{Type: c.typ, Name: "x"}, object(c.typ, "x", "u1", c.spec))
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if got := m.Selector().String(); got != c.want {
				t.Errorf("selector = %q, want %q", got, c.want)
			}
		})
	}
}

func TestNoPods(t *testing.T) {
	cases := []struct {
		name string
		typ  resources.ResourceType
		spec map[string]any
		msg  string
	}{
		{"unsupported type", configMaps, nil, "this configmap does not select pods"},
		{"no selector", deployments, map[string]any{}, "this deployment does not select pods"},
		{"empty selector", deployments, map[string]any{"selector": map[string]any{}}, "this deployment does not select pods"},
		{"service without selector", services, map[string]any{"ports": []any{}}, "this service does not select pods"},
		{"ExternalName service", services, map[string]any{"type": "ExternalName", "selector": map[string]any{"app": "web"}}, "this service does not select pods"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := match(t, &fakeSubscriber{}, resources.ObjectRef{Type: c.typ, Name: "x"}, object(c.typ, "x", "u1", c.spec))
			if !errors.Is(err, ErrNoPods) || err.Error() != c.msg {
				t.Errorf("err = %v, want %q wrapping ErrNoPods", err, c.msg)
			}
		})
	}
}

func TestNotFound(t *testing.T) {
	_, err := match(t, &fakeSubscriber{}, resources.ObjectRef{Type: deployments, Name: "gone"})
	if !apierrors.IsNotFound(err) {
		t.Errorf("err = %v, want NotFound", err)
	}
}

func TestOwnsDirectly(t *testing.T) {
	m, err := match(t, &fakeSubscriber{}, resources.ObjectRef{Type: statefulSets, Name: "db"},
		object(statefulSets, "db", "sts-1", labelSelector(map[string]any{"app": "db"})))
	if err != nil {
		t.Fatal(err)
	}
	if synced, err := m.State(); !synced || err != nil {
		t.Errorf("state = %v, %v; want synced", synced, err)
	}
	notController := &metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "StatefulSet", UID: "sts-1"}}}
	cases := []struct {
		name string
		pod  metav1.Object
		want bool
	}{
		{"its pod", pod("StatefulSet", "sts-1"), true},
		{"another controller's pod", pod("StatefulSet", "sts-2"), false},
		{"a recreated statefulset's old pod", pod("StatefulSet", "sts-0"), false},
		{"a pod without controller", pod("", ""), false},
		{"an owner reference that is not the controller", notController, false},
	}
	for _, c := range cases {
		if got := m.Owns(c.pod); got != c.want {
			t.Errorf("%s: Owns = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestServiceOwnsEverySelectedPod(t *testing.T) {
	m, err := match(t, &fakeSubscriber{}, resources.ObjectRef{Type: services, Name: "web"},
		object(services, "web", "svc-1", map[string]any{"selector": map[string]any{"app": "web"}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []metav1.Object{pod("ReplicaSet", "rs-1"), pod("", "")} {
		if !m.Owns(p) {
			t.Errorf("Owns(%v) = false; a Service's pods are the ones its selector matches", p.GetOwnerReferences())
		}
	}
}

func replicaSet(uid, controller string) *table.RowObject {
	r := &table.RowObject{}
	r.Name, r.Namespace, r.UID = uid, "web", types.UID(uid)
	if controller != "" {
		yes := true
		r.OwnerReferences = []metav1.OwnerReference{{Kind: "Deployment", UID: types.UID(controller), Controller: &yes}}
	}
	return r
}

func changed(m Membership) bool {
	select {
	case <-m.Changed():
		return true
	case <-time.After(100 * time.Millisecond):
		return false
	}
}

func TestDeploymentOwnsThroughReplicaSets(t *testing.T) {
	subs := &fakeSubscriber{}
	m, err := match(t, subs, resources.ObjectRef{Type: deployments, Name: "web"},
		object(deployments, "web", "dep-1", labelSelector(map[string]any{"app": "web"})))
	if err != nil {
		t.Fatal(err)
	}
	want := cache.Key{ContextID: "c1", GVR: replicaSetsGVR, Namespace: "web", LabelSelector: "app=web"}
	if subs.key != want {
		t.Fatalf("subscribed to %+v, want %+v", subs.key, want)
	}
	if synced, _ := m.State(); synced {
		t.Error("synced before the ReplicaSets were listed")
	}

	h := subs.handler
	h.Upsert(replicaSet("rs-1", "dep-1"))
	h.Upsert(replicaSet("rs-canary", "dep-canary")) // matches the selector, owned by another Deployment
	h.Upsert(replicaSet("rs-orphan", ""))
	h.Synced(nil)
	if synced, err := m.State(); !synced || err != nil {
		t.Fatalf("state = %v, %v; want synced", synced, err)
	}
	if !changed(m) {
		t.Fatal("Changed did not fire")
	}
	for uid, want := range map[string]bool{"rs-1": true, "rs-canary": false, "rs-orphan": false, "rs-unknown": false} {
		if got := m.Owns(pod("ReplicaSet", uid)); got != want {
			t.Errorf("pod of %s: Owns = %v, want %v", uid, got, want)
		}
	}
	if m.Owns(pod("StatefulSet", "rs-1")) {
		t.Error("a pod whose controller is not a ReplicaSet was owned")
	}

	// A rollout: a second ReplicaSet appears, then the first goes.
	h.Upsert(replicaSet("rs-2", "dep-1"))
	if !changed(m) || !m.Owns(pod("ReplicaSet", "rs-2")) {
		t.Error("new ReplicaSet's pods are not owned, or Changed did not fire")
	}
	h.Delete(replicaSet("rs-1", "dep-1"))
	if !changed(m) || m.Owns(pod("ReplicaSet", "rs-1")) {
		t.Error("deleted ReplicaSet's pods are still owned, or Changed did not fire")
	}
	// Orphaned (e.g. kubectl delete --cascade=orphan on another path).
	h.Upsert(replicaSet("rs-2", ""))
	if !changed(m) || m.Owns(pod("ReplicaSet", "rs-2")) {
		t.Error("orphaned ReplicaSet's pods are still owned")
	}
	// An update that changes nothing does not signal.
	h.Upsert(replicaSet("rs-canary", "dep-canary"))
	if changed(m) {
		t.Error("Changed fired without a change")
	}

	m.Close()
	if !subs.closed {
		t.Error("Close did not end the ReplicaSet subscription")
	}
}

func TestDeploymentFailsClosed(t *testing.T) {
	subs := &fakeSubscriber{}
	m, err := match(t, subs, resources.ObjectRef{Type: deployments, Name: "web"},
		object(deployments, "web", "dep-1", labelSelector(map[string]any{"app": "web"})))
	if err != nil {
		t.Fatal(err)
	}
	subs.handler.Error(apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "replicasets"}, "", errors.New("denied")))
	_, err = m.State()
	if want := `no permission to list replicasets in "web", which is needed to find the deployment's pods`; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if !changed(m) {
		t.Error("Changed did not fire on error")
	}
	if m.Owns(pod("ReplicaSet", "rs-1")) {
		t.Error("owned a pod without knowing the ReplicaSets")
	}
	subs.handler.Error(nil)
	if _, err := m.State(); err != nil {
		t.Errorf("err = %v after recovery, want nil", err)
	}
}

func TestDeploymentSubscribeError(t *testing.T) {
	_, err := match(t, &fakeSubscriber{err: errors.New("boom")}, resources.ObjectRef{Type: deployments, Name: "web"},
		object(deployments, "web", "dep-1", labelSelector(map[string]any{"app": "web"})))
	if err == nil {
		t.Error("want an error")
	}
}

func TestSelectsPods(t *testing.T) {
	for typ, want := range map[resources.ResourceType]bool{deployments: true, jobs: true, services: true, configMaps: false, PodsType: false} {
		if got := SelectsPods(typ); got != want {
			t.Errorf("SelectsPods(%s) = %v, want %v", typ.Resource, got, want)
		}
	}
}
