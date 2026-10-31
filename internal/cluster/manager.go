package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	authnv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/bhatnagar-afk-dev/kuberoot/internal/kubeconfig"
)

// ConfigSource supplies contexts and client configs. *kubeconfig.Store implements it.
type ConfigSource interface {
	Context(id string) (kubeconfig.Context, bool)
	RESTConfig(id string) (*rest.Config, error)
}

// Options tunes a Manager. Zero values select the defaults.
type Options struct {
	MaxConnections int           // default MaxConnections
	ConnectTimeout time.Duration // per request; default 10s
	HealthInterval time.Duration // between checks while healthy; default 30s
	RetryBackoff   time.Duration // first retry after a failed check; default 1s
	MaxBackoff     time.Duration // cap between checks while failing; default 2m
	FailThreshold  int           // consecutive failures before PhaseError; default 3

	// OnStatus is called (from any goroutine) whenever a visible status changes.
	OnStatus func(Status)
	// LookPath resolves exec plugin commands; default exec.LookPath.
	LookPath func(string) (string, error)
}

func (o Options) withDefaults() Options {
	if o.MaxConnections <= 0 {
		o.MaxConnections = MaxConnections
	}
	if o.ConnectTimeout <= 0 {
		o.ConnectTimeout = 10 * time.Second
	}
	if o.HealthInterval <= 0 {
		o.HealthInterval = 30 * time.Second
	}
	if o.RetryBackoff <= 0 {
		o.RetryBackoff = time.Second
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 2 * time.Minute
	}
	if o.FailThreshold <= 0 {
		o.FailThreshold = 3
	}
	if o.OnStatus == nil {
		o.OnStatus = func(Status) {}
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	return o
}

// Manager owns connections to clusters. It is safe for concurrent use.
type Manager struct {
	src  ConfigSource
	opts Options

	mu    sync.Mutex
	conns map[string]*conn
	last  map[string]Status // final status of connections no longer held
}

type conn struct {
	id          string
	fingerprint string
	server      string
	execCmd     string
	cfg         *rest.Config
	client      kubernetes.Interface
	ctx         context.Context
	cancel      context.CancelFunc
	status      Status // guarded by Manager.mu
}

// NewManager returns a Manager reading configs from src.
func NewManager(src ConfigSource, opts Options) *Manager {
	return &Manager{
		src:   src,
		opts:  opts.withDefaults(),
		conns: map[string]*conn{},
		last:  map[string]Status{},
	}
}

// Status returns the current status of a context.
func (m *Manager) Status(id string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.conns[id]; ok {
		return c.status
	}
	if s, ok := m.last[id]; ok {
		return s
	}
	return Status{ContextID: id, Phase: PhaseDisconnected}
}

// Client returns the client for a connected context, for use by later features.
func (m *Manager) Client(id string) (kubernetes.Interface, *rest.Config, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[id]
	if !ok || c.status.Phase != PhaseConnected {
		return nil, nil, false
	}
	return c.client, rest.CopyConfig(c.cfg), true
}

// Connect connects to a context and blocks until the handshake succeeds or
// fails. Connecting an already connected (or connecting) context is a no-op.
func (m *Manager) Connect(ctx context.Context, id string) (Status, error) {
	m.mu.Lock()
	if c, ok := m.conns[id]; ok {
		if c.status.Phase == PhaseConnected || c.status.Phase == PhaseConnecting {
			s := c.status
			m.mu.Unlock()
			return s, nil
		}
		m.dropLocked(c, c.status) // retry a connection in error
	}
	if len(m.conns) >= m.opts.MaxConnections {
		s := errorStatus(id, ErrConnectionLimit, "", "")
		s.Message = fmt.Sprintf("At most %d clusters can be connected at once; disconnect one first", m.opts.MaxConnections)
		s.Detail = ""
		s.CheckedAt = time.Now()
		m.last[id] = s
		m.mu.Unlock()
		m.opts.OnStatus(s)
		return s, ErrConnectionLimit
	}
	c := m.newConnLocked(id, Reason(""))
	m.mu.Unlock()

	return m.handshake(ctx, c)
}

func (m *Manager) newConnLocked(id string, reason Reason) *conn {
	cctx, cancel := context.WithCancel(context.Background())
	c := &conn{id: id, ctx: cctx, cancel: cancel}
	if kc, ok := m.src.Context(id); ok {
		c.fingerprint, c.server, c.execCmd = kc.Fingerprint, kc.Server, kc.ExecCmd
	}
	c.status = Status{ContextID: id, Phase: PhaseConnecting, Reason: reason, CheckedAt: time.Now()}
	if reason == ReasonConfigChanged {
		c.status.Message = "Kubeconfig changed; reconnecting"
	}
	m.conns[id] = c
	delete(m.last, id)
	m.opts.OnStatus(c.status)
	return c
}

func (m *Manager) handshake(ctx context.Context, c *conn) (Status, error) {
	s, err := m.dial(ctx, c)
	s.ContextID, s.CheckedAt = c.id, time.Now()

	m.mu.Lock()
	if m.conns[c.id] != c { // disconnected or replaced meanwhile
		cur := m.statusLocked(c.id)
		m.mu.Unlock()
		return cur, context.Canceled
	}
	if err != nil {
		m.dropLocked(c, s)
		m.mu.Unlock()
		m.opts.OnStatus(s)
		return s, err
	}
	c.status = s
	m.mu.Unlock()
	m.opts.OnStatus(s)
	go m.health(c)
	return s, nil
}

func (m *Manager) statusLocked(id string) Status {
	if c, ok := m.conns[id]; ok {
		return c.status
	}
	if s, ok := m.last[id]; ok {
		return s
	}
	return Status{ContextID: id, Phase: PhaseDisconnected}
}

// dropLocked removes c and records its final status.
func (m *Manager) dropLocked(c *conn, final Status) {
	c.cancel()
	if m.conns[c.id] == c {
		delete(m.conns, c.id)
	}
	m.last[c.id] = final
}

// dial builds the client and verifies reachability and credentials.
func (m *Manager) dial(ctx context.Context, c *conn) (Status, error) {
	fail := func(err error) (Status, error) {
		return errorStatus(c.id, err, c.server, c.execCmd), err
	}

	cfg, err := m.src.RESTConfig(c.id)
	if err != nil {
		s, err := fail(err)
		if s.Reason == ReasonUnknown {
			s.Reason, s.Message = ReasonInvalidConfig, "The kubeconfig entry for this context is invalid"
		}
		return s, err
	}
	if ep := cfg.ExecProvider; ep != nil {
		if _, err := m.opts.LookPath(ep.Command); err != nil {
			s := Status{
				ContextID: c.id,
				Phase:     PhaseError,
				Reason:    ReasonExecPluginNotFound,
				Message:   fmt.Sprintf("`%s` was not found on PATH", ep.Command),
				Detail:    "PATH=" + os.Getenv("PATH"),
			}
			if ep.InstallHint != "" {
				s.Detail = ep.InstallHint + "\n\n" + s.Detail
			}
			return s, err
		}
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fail(err)
	}
	c.cfg, c.client = cfg, client

	ctx, cancel := context.WithTimeout(ctx, m.opts.ConnectTimeout)
	defer cancel()
	ver, err := serverVersion(ctx, client)
	if err != nil {
		s, err := fail(err)
		if s.Reason == ReasonExecPluginFailed && cfg.ExecProvider != nil {
			if stderr := diagnoseExec(context.Background(), cfg.ExecProvider); stderr != "" {
				s.Detail = stderr + "\n\n" + s.Detail
			}
		}
		return s, err
	}
	identity, err := whoAmI(ctx, client)
	if err != nil {
		return fail(err)
	}
	return Status{Phase: PhaseConnected, ServerVersion: ver, Identity: identity}, nil
}

func serverVersion(ctx context.Context, client kubernetes.Interface) (string, error) {
	raw, err := client.Discovery().RESTClient().Get().AbsPath("/version").Do(ctx).Raw()
	if err != nil {
		return "", err
	}
	var info version.Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return "", fmt.Errorf("decode /version: %w", err)
	}
	return info.GitVersion, nil
}

// whoAmI returns the authenticated username. Clusters that don't serve or
// allow SelfSubjectReview return "" without failing the connection.
func whoAmI(ctx context.Context, client kubernetes.Interface) (string, error) {
	res, err := client.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authnv1.SelfSubjectReview{}, metav1.CreateOptions{})
	switch {
	case err == nil:
		return res.Status.UserInfo.Username, nil
	case apierrors.IsNotFound(err), apierrors.IsForbidden(err), apierrors.IsMethodNotSupported(err):
		return "", nil
	default:
		return "", err
	}
}

// health periodically checks a connected cluster until its context is cancelled.
func (m *Manager) health(c *conn) {
	interval := m.opts.HealthInterval
	failures := 0
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-timer.C:
		}

		ctx, cancel := context.WithTimeout(c.ctx, m.opts.ConnectTimeout)
		ver, err := serverVersion(ctx, c.client)
		cancel()
		if c.ctx.Err() != nil {
			return
		}

		var next Status
		if err == nil {
			failures = 0
			interval = m.opts.HealthInterval
			next = m.Status(c.id)
			next.Phase, next.ServerVersion = PhaseConnected, ver
			next.Reason, next.Message, next.Detail = "", "", ""
		} else {
			failures++
			if failures < m.opts.FailThreshold {
				interval = m.opts.RetryBackoff << (failures - 1)
				timer.Reset(interval)
				continue
			}
			next = errorStatus(c.id, err, c.server, c.execCmd)
			cur := m.Status(c.id)
			next.ServerVersion, next.Identity = cur.ServerVersion, cur.Identity
			interval = min(m.opts.HealthInterval<<(failures-m.opts.FailThreshold), m.opts.MaxBackoff)
		}
		next.ContextID, next.CheckedAt = c.id, time.Now()
		m.update(c, next)
		timer.Reset(interval)
	}
}

// update sets c's status and notifies if it visibly changed.
func (m *Manager) update(c *conn, s Status) {
	m.mu.Lock()
	if m.conns[c.id] != c {
		m.mu.Unlock()
		return
	}
	changed := !sameVisible(c.status, s)
	c.status = s
	m.mu.Unlock()
	if changed {
		m.opts.OnStatus(s)
	}
}

// Disconnect closes the connection to a context.
func (m *Manager) Disconnect(id string) {
	m.mu.Lock()
	c, ok := m.conns[id]
	if !ok {
		delete(m.last, id)
		m.mu.Unlock()
		return
	}
	s := Status{ContextID: id, Phase: PhaseDisconnected, CheckedAt: time.Now()}
	m.dropLocked(c, s)
	delete(m.last, id)
	m.mu.Unlock()
	m.opts.OnStatus(s)
}

// SyncContexts reacts to kubeconfig changes: connections whose context was
// removed are closed, and those whose config changed are re-established.
func (m *Manager) SyncContexts(contexts []kubeconfig.Context) {
	byID := make(map[string]kubeconfig.Context, len(contexts))
	for _, c := range contexts {
		byID[c.ID] = c
	}

	var removed []Status
	var reconnect []*conn
	m.mu.Lock()
	for id, c := range m.conns {
		kc, ok := byID[id]
		switch {
		case !ok:
			s := Status{ContextID: id, Phase: PhaseDisconnected, Reason: ReasonInvalidConfig,
				Message: "This context was removed from its kubeconfig", CheckedAt: time.Now()}
			m.dropLocked(c, s)
			removed = append(removed, s)
		case kc.Fingerprint != c.fingerprint:
			m.dropLocked(c, c.status)
			reconnect = append(reconnect, m.newConnLocked(id, ReasonConfigChanged))
		}
	}
	m.mu.Unlock()

	for _, s := range removed {
		m.opts.OnStatus(s)
	}
	for _, c := range reconnect {
		go func() { _, _ = m.handshake(c.ctx, c) }()
	}
}

// Close disconnects everything.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.conns {
		c.cancel()
	}
	m.conns = map[string]*conn{}
}
