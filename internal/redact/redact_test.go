package redact

import "testing"

func TestString(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"bearer header", "Authorization: Bearer abcdefgh12345678", "Authorization: Bearer [REDACTED]"},
		{"basic header", "basic dXNlcjpwYXNzd29yZA==", "basic [REDACTED]"},
		{"yaml token", "token: s3cr3t-value", "token: [REDACTED]"},
		{"json token", `{"token":"s3cr3t","expirationTimestamp":"x"}`, `{"token":"[REDACTED]","expirationTimestamp":"x"}`},
		{"query style", "access_token=abc123&x=1", "access_token=[REDACTED]&x=1"},
		{"client secret", "client-secret=hunter2", "client-secret=[REDACTED]"},
		{"password", "password: hunter2", "password: [REDACTED]"},
		{"url userinfo", "Get https://admin:hunter2@10.0.0.1:6443/version", "Get https://[REDACTED]@10.0.0.1:6443/version"},
		{"jwt", "token eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4In0.c2lnbmF0dXJl rejected", "token [REDACTED] rejected"},
		{
			"pem key",
			"key:\n-----BEGIN RSA PRIVATE KEY-----\nMIIEow\nIBAAK\n-----END RSA PRIVATE KEY-----\ndone",
			"key:\n[REDACTED]\ndone",
		},
		{"plain error untouched", `Get "https://10.0.0.1:6443/version": dial tcp: connection refused`, `Get "https://10.0.0.1:6443/version": dial tcp: connection refused`},
		{"short bearer word untouched", "the bearer of news", "the bearer of news"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := String(tt.in); got != tt.want {
				t.Errorf("String(%q)\n got  %q\n want %q", tt.in, got, tt.want)
			}
		})
	}
}
