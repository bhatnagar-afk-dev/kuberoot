package browser

import (
	"context"
	"errors"
	"time"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/redact"
	"kuberoot.in/kuberoot/internal/resources"
)

// objectTimeout bounds one Describe or YAML call.
const objectTimeout = 30 * time.Second

// Objects renders single objects for the detail panel. Errors it returns
// are redacted. It is safe for concurrent use.
type Objects struct {
	conns     resources.Connections
	describer resources.Describer
	reader    resources.ObjectReader
}

// NewObjects returns an Objects using the given describer and reader.
func NewObjects(conns resources.Connections, d resources.Describer, r resources.ObjectReader) *Objects {
	return &Objects{conns: conns, describer: d, reader: r}
}

// Describe returns describe output for ref.
func (o *Objects) Describe(ref resources.ObjectRef) (string, error) {
	return o.do(ref, func(ctx context.Context, s cluster.Session) (string, error) {
		return o.describer.Describe(ctx, s, ref)
	})
}

// YAML returns ref rendered as YAML.
func (o *Objects) YAML(ref resources.ObjectRef, opts resources.YAMLOptions) (string, error) {
	return o.do(ref, func(ctx context.Context, s cluster.Session) (string, error) {
		return o.reader.YAML(ctx, s, ref, opts)
	})
}

func (o *Objects) do(ref resources.ObjectRef, f func(context.Context, cluster.Session) (string, error)) (string, error) {
	s, err := o.conns.Session(ref.ContextID)
	if err != nil {
		return "", errors.New(redact.String(err.Error()))
	}
	// The session's context ends with the connection.
	ctx, cancel := context.WithTimeout(s.Ctx, objectTimeout)
	defer cancel()
	out, err := f(ctx, s)
	if err != nil {
		return "", errors.New(redact.String(err.Error()))
	}
	return out, nil
}
