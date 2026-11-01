package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/bhatnagar-afk-dev/kuberoot/internal/kubeconfig"
	"github.com/bhatnagar-afk-dev/kuberoot/internal/redact"
)

// ErrConnectionLimit is returned when MaxConnections contexts are already connected.
var ErrConnectionLimit = errors.New("connection limit reached")

// classify maps a connection error to a Reason and a short human message.
// server and execCmd are used to make messages specific.
func classify(err error, server, execCmd string) (Reason, string) {
	var legacy *kubeconfig.LegacyAuthProviderError
	var urlErr *url.Error
	var netErr net.Error
	var opErr *net.OpError
	var dnsErr *net.DNSError
	var unknownCA x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var certErr x509.CertificateInvalidError
	var verifyErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError

	msg := err.Error()
	switch {
	case errors.Is(err, ErrConnectionLimit):
		return ReasonConnectionLimit, fmt.Sprintf("At most %d clusters can be connected at once; disconnect one first", MaxConnections)
	case errors.As(err, &legacy):
		return ReasonInvalidConfig, legacy.Error()
	case errors.Is(err, kubeconfig.ErrContextNotFound):
		return ReasonInvalidConfig, "This context no longer exists in its kubeconfig"
	// client-go formats exec failures as text without wrapping the cause.
	case strings.Contains(msg, "exec: executable") && strings.Contains(msg, "not found"):
		return ReasonExecPluginNotFound, fmt.Sprintf("`%s` was not found on PATH", execCmd)
	case strings.Contains(msg, "exec: executable") || strings.Contains(msg, "getting credentials: exec"):
		return ReasonExecPluginFailed, fmt.Sprintf("`%s` failed to provide credentials", execCmd)
	case apierrors.IsUnauthorized(err):
		return ReasonUnauthorized, "Credentials were rejected; they may have expired"
	case apierrors.IsForbidden(err):
		return ReasonForbidden, "Access denied by the cluster"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return ReasonTimeout, fmt.Sprintf("Timed out connecting to %s", server)
	case errors.As(err, &unknownCA), errors.As(err, &hostErr), errors.As(err, &certErr),
		errors.As(err, &verifyErr), errors.As(err, &recordErr):
		return ReasonTLSError, "The cluster's TLS certificate could not be verified"
	case errors.As(err, &dnsErr), errors.As(err, &opErr), errors.As(err, &urlErr):
		return ReasonUnreachable, fmt.Sprintf("Cluster is unreachable at %s", server)
	default:
		return ReasonUnknown, "Could not connect to the cluster"
	}
}

// errorStatus builds an error Status for id from err.
func errorStatus(id string, err error, server, execCmd string) Status {
	reason, msg := classify(err, server, execCmd)
	return Status{
		ContextID: id,
		Phase:     PhaseError,
		Reason:    reason,
		Message:   msg,
		Detail:    redact.String(err.Error()),
	}
}
