package browser

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	clocktesting "k8s.io/utils/clock/testing"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/cache"
	"kuberoot.in/kuberoot/internal/resources/view"
	"kuberoot.in/kuberoot/internal/resources/workload"
)

var deployments = resources.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}

type fakeMatcher struct {
	err error
	m   *fakeMembership
	ref resources.ObjectRef
}

func (f *fakeMatcher) Match(_ context.Context, _ cluster.Session, ref resources.ObjectRef) (workload.Membership, error) {
	f.ref = ref
	if f.err != nil {
		return nil, f.err
	}
	return f.m, nil
}

type fakeMembership struct{ closed bool }

func (*fakeMembership) Selector() labels.Selector {
	return labels.SelectorFromSet(labels.Set{"app": "web"})
}
func (*fakeMembership) Owns(metav1.Object) bool  { return true }
func (*fakeMembership) Changed() <-chan struct{} { return nil }
func (*fakeMembership) State() (bool, error)     { return true, nil }
func (m *fakeMembership) Close()                 { m.closed = true }

func podsBrowser(t *testing.T, subs *fakeSubscriber, pods workload.PodMatcher) *Browser {
	t.Helper()
	b := New(fakeConns{ctx: t.Context()}, subs, pods, func(resources.ViewRequest) resources.Sink { return nopSink{} },
		view.Options{Clock: clocktesting.NewFakeClock(time.Now())})
	t.Cleanup(b.CloseAll)
	return b
}

func TestOpenPods(t *testing.T) {
	subs := &fakeSubscriber{}
	m := &fakeMatcher{m: &fakeMembership{}}
	b := podsBrowser(t, subs, m)

	ref := resources.ObjectRef{ContextID: "c1", Type: deployments, Namespace: "web", Name: "api"}
	if err := b.OpenPods("p1", ref); err != nil {
		t.Fatal(err)
	}
	if m.ref != ref {
		t.Errorf("matched %+v, want %+v", m.ref, ref)
	}
	want := cache.Key{ContextID: "c1", GVR: workload.PodsType.GVR(), Namespace: "web", LabelSelector: "app=web"}
	if len(subs.keys) != 1 || subs.keys[0] != want {
		t.Errorf("subscribed = %+v, want %+v", subs.keys, want)
	}
	b.Close("p1")
	if subs.count() != 0 || !m.m.closed {
		t.Errorf("after Close: subscriptions = %d, membership closed = %v", subs.count(), m.m.closed)
	}
}

func TestOpenPodsErrors(t *testing.T) {
	ref := resources.ObjectRef{ContextID: "c1", Type: deployments, Namespace: "web", Name: "api"}

	noPods := fmt.Errorf("this service %w", workload.ErrNoPods)
	b := podsBrowser(t, &fakeSubscriber{}, &fakeMatcher{err: noPods})
	if err := b.OpenPods("p1", ref); err == nil || err.Error() != "this service does not select pods" {
		t.Errorf("err = %v, want the ErrNoPods message as is", err)
	}

	// A view that fails to open releases its membership.
	subs := &fakeSubscriber{failOn: "web"}
	m := &fakeMatcher{m: &fakeMembership{}}
	b = podsBrowser(t, subs, m)
	if err := b.OpenPods("p1", ref); err == nil {
		t.Fatal("want an error")
	}
	if !m.m.closed {
		t.Error("membership not closed after a failed open")
	}

	b = podsBrowser(t, &fakeSubscriber{}, &fakeMatcher{err: errors.New("boom token=abcdef123456")})
	if err := b.OpenPods("p1", ref); err == nil || err.Error() != "open pods: boom token=[REDACTED]" {
		t.Errorf("err = %v, want a redacted error", err)
	}
}
