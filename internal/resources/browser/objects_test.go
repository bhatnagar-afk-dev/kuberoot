package browser

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
)

type fakeDescriber struct {
	out string
	err error
	ctx context.Context
}

func (f *fakeDescriber) Describe(ctx context.Context, s cluster.Session, ref resources.ObjectRef) (string, error) {
	f.ctx = ctx
	return f.out + " " + s.ContextID + "/" + ref.Name, f.err
}

type fakeReader struct{ opts resources.YAMLOptions }

func (f *fakeReader) YAML(_ context.Context, _ cluster.Session, ref resources.ObjectRef, opts resources.YAMLOptions) (string, error) {
	f.opts = opts
	return "name: " + ref.Name, nil
}

func TestObjects(t *testing.T) {
	d, r := &fakeDescriber{out: "described"}, &fakeReader{}
	o := NewObjects(fakeConns{ctx: t.Context()}, d, r)
	ref := resources.ObjectRef{ContextID: "c1", Type: pods, Namespace: "web", Name: "p1"}

	out, err := o.Describe(ref)
	if err != nil || out != "described c1/p1" {
		t.Fatalf("Describe = %q, %v", out, err)
	}
	if dl, ok := d.ctx.Deadline(); !ok || time.Until(dl) > objectTimeout {
		t.Errorf("describe context deadline = %v, %v", dl, ok)
	}

	opts := resources.YAMLOptions{RevealSecrets: true}
	out, err = o.YAML(ref, opts)
	if err != nil || out != "name: p1" || r.opts != opts {
		t.Fatalf("YAML = %q, %v (opts %+v)", out, err, r.opts)
	}
}

func TestObjectsErrors(t *testing.T) {
	ref := resources.ObjectRef{ContextID: "c1", Type: pods, Name: "p1"}

	o := NewObjects(fakeConns{err: cluster.ErrNotConnected}, &fakeDescriber{}, &fakeReader{})
	if _, err := o.Describe(ref); err == nil || err.Error() != cluster.ErrNotConnected.Error() {
		t.Errorf("not connected: err = %v", err)
	}

	d := &fakeDescriber{err: errors.New("bad token=abcdef123456")}
	o = NewObjects(fakeConns{ctx: t.Context()}, d, &fakeReader{})
	if _, err := o.Describe(ref); err == nil || strings.Contains(err.Error(), "abcdef123456") {
		t.Errorf("err = %v, want a redacted error", err)
	}
}

func TestObjectsStopWithConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := &fakeDescriber{}
	o := NewObjects(fakeConns{ctx: ctx}, d, &fakeReader{})
	if _, err := o.Describe(resources.ObjectRef{ContextID: "c1", Name: "p1"}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if d.ctx.Err() == nil {
		t.Error("describe context outlives the connection")
	}
}
