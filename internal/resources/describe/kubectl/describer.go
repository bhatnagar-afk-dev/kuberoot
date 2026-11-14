// Package kubectl implements resources.Describer with the describers of
// `kubectl describe`. It is the only package that imports k8s.io/kubectl, so
// replacing the describe output means replacing this package.
package kubectl

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kubectl/pkg/describe"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
)

// requestTimeout bounds each request a describer makes; kubectl's
// describers take no context.
const requestTimeout = 30 * time.Second

// Describer uses kubectl's describer for built-in kinds and its generic
// describer for everything else (e.g. custom resources).
type Describer struct{}

// Describe returns the `kubectl describe` output for ref, with its events.
func (Describer) Describe(ctx context.Context, s cluster.Session, ref resources.ObjectRef) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cfg := s.Config // a copy, per Session
	if cfg.Timeout == 0 || cfg.Timeout > requestTimeout {
		cfg.Timeout = requestTimeout
	}
	gk := schema.GroupKind{Group: ref.Type.Group, Kind: ref.Type.Kind}

	d, ok := describe.DescriberFor(gk, cfg)
	if !ok {
		mapping, err := s.Mapper.RESTMapping(gk, ref.Type.Version)
		if err != nil {
			return "", err
		}
		if d, ok = describe.GenericDescriberFor(mapping, cfg); !ok {
			return "", fmt.Errorf("no describer for %s", gk)
		}
	}
	return d.Describe(ref.Namespace, ref.Name, describe.DescriberSettings{ShowEvents: true, ChunkSize: 500})
}
