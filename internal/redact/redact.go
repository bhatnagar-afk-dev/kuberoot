// Package redact removes credentials from strings before they are logged or
// sent to the UI.
package redact

import "regexp"

const mask = "[REDACTED]"

var rules = []struct {
	re   *regexp.Regexp
	repl string
}{
	// PEM private keys (client-key-data decoded, or inline in errors).
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), mask},
	// Authorization headers.
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9\-._~+/=]{8,}`), "$1 " + mask},
	// key: value / key=value / "key":"value" for common secret fields.
	{regexp.MustCompile(`(?i)("?\b(?:token|access[_-]?token|id[_-]?token|refresh[_-]?token|client[_-]?secret|password|client-key-data)"?\s*[:=]\s*"?)[^\s",}&]+`), "${1}" + mask},
	// URL userinfo: scheme://user:pass@host
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)[^/\s:@]+:[^/\s@]+@`), "${1}" + mask + "@"},
	// JWTs anywhere (three base64url segments starting with eyJ).
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+`), mask},
}

// String returns s with known credential patterns replaced by [REDACTED].
func String(s string) string {
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}
