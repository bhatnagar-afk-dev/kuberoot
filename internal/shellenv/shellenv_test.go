package shellenv

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	out := []byte("Welcome banner\n" + startMarker + "PATH=/usr/bin:/opt/homebrew/bin\x00KUBECONFIG=/a:/b\x00MULTI=line1\nline2\x00EQ=a=b\x00\x00" + endMarker + "trailing")
	env, err := parse(out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PATH":       "/usr/bin:/opt/homebrew/bin",
		"KUBECONFIG": "/a:/b",
		"MULTI":      "line1\nline2",
		"EQ":         "a=b",
	}
	if len(env) != len(want) {
		t.Fatalf("got %d vars %v, want %d", len(env), env, len(want))
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for name, out := range map[string]string{
		"no markers": "PATH=/bin",
		"empty":      startMarker + endMarker,
		"only start": startMarker + "PATH=/bin",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parse([]byte(out)); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestParseIgnoresMarkersInsideBanner(t *testing.T) {
	// A profile that echoes noise after the env dump must not confuse parsing.
	out := []byte(startMarker + "A=1\x00" + endMarker + "\nbye " + strings.Repeat("x", 10))
	env, err := parse(out)
	if err != nil || env["A"] != "1" {
		t.Fatalf("env=%v err=%v", env, err)
	}
}
