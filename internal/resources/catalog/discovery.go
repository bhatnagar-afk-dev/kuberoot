// Package catalog lists the resource types a cluster serves, using API
// discovery.
package catalog

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
)

// Discovery is a Catalog backed by the session's cached discovery client.
// It is safe for concurrent use.
type Discovery struct {
	cached discovery.CachedDiscoveryInterface
	fresh  discovery.DiscoveryInterface // bypasses the cache, for Changed
	reset  func()                       // resets the session's REST mapper

	mu   sync.Mutex
	hash string // of the last set listed from the cache; "" before the first
}

var _ resources.Catalog = (*Discovery)(nil)

// New returns a catalog for one connected session.
func New(s cluster.Session) (*Discovery, error) {
	fresh, err := discovery.NewDiscoveryClientForConfig(s.Config)
	if err != nil {
		return nil, fmt.Errorf("discovery client: %w", err)
	}
	d := &Discovery{cached: s.Discovery, fresh: fresh, reset: func() {}}
	if s.Mapper != nil {
		d.reset = s.Mapper.Reset
	}
	return d, nil
}

// ResourceTypes returns the listable, watchable types the server prefers,
// one version per group, from the cache when possible.
func (d *Discovery) ResourceTypes(ctx context.Context) ([]resources.ResourceType, error) {
	types, failed, err := list(ctx, d.cached)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.hash = hash(types, failed)
	d.mu.Unlock()
	return types, partial(failed)
}

// Changed lists the types again without the cache and reports whether they
// differ from the last ResourceTypes or Refresh. It is false before either.
func (d *Discovery) Changed(ctx context.Context) (bool, error) {
	d.mu.Lock()
	cached := d.hash
	d.mu.Unlock()
	if cached == "" {
		return false, nil
	}
	types, failed, err := list(ctx, d.fresh)
	if err != nil {
		return false, err
	}
	return hash(types, failed) != cached, nil
}

// Refresh drops the cached discovery data and lists the types again.
func (d *Discovery) Refresh(ctx context.Context) ([]resources.ResourceType, error) {
	d.cached.Invalidate()
	d.reset()
	return d.ResourceTypes(ctx)
}

// list returns the usable types and the group versions that failed. A
// partial failure is not an error.
func list(ctx context.Context, c discovery.DiscoveryInterface) ([]resources.ResourceType, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	lists, err := c.ServerPreferredResources()
	var failed []string
	if err != nil {
		var gdf *discovery.ErrGroupDiscoveryFailed
		if !errors.As(err, &gdf) {
			return nil, nil, err
		}
		for gv := range gdf.Groups {
			failed = append(failed, gv.String())
		}
		slices.Sort(failed)
	}
	var types []resources.ResourceType
	for _, l := range lists {
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range l.APIResources {
			if usable(r) {
				types = append(types, resources.ResourceType{
					Group:      gv.Group,
					Version:    gv.Version,
					Resource:   r.Name,
					Kind:       r.Kind,
					Namespaced: r.Namespaced,
				})
			}
		}
	}
	slices.SortFunc(types, func(a, b resources.ResourceType) int {
		return cmp.Or(cmp.Compare(a.Group, b.Group), cmp.Compare(a.Resource, b.Resource))
	})
	return types, failed, nil
}

// usable reports whether r is a top-level type that can be listed and watched.
func usable(r metav1.APIResource) bool {
	return !strings.Contains(r.Name, "/") &&
		slices.Contains(r.Verbs, "list") && slices.Contains(r.Verbs, "watch")
}

func hash(types []resources.ResourceType, failed []string) string {
	h := sha256.New()
	for _, t := range types {
		_, _ = fmt.Fprintf(h, "%s/%s/%s/%s/%t\n", t.Group, t.Version, t.Resource, t.Kind, t.Namespaced)
	}
	for _, gv := range failed {
		_, _ = fmt.Fprintf(h, "failed %s\n", gv)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func partial(failed []string) error {
	if len(failed) == 0 {
		return nil
	}
	return &resources.PartialDiscoveryError{GroupVersions: failed}
}
