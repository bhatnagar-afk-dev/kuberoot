package version

import (
	"runtime"
	"testing"
)

func TestGet(t *testing.T) {
	info := Get()
	if info.Version != Version {
		t.Errorf("Version = %q, want %q", info.Version, Version)
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; info.Platform != want {
		t.Errorf("Platform = %q, want %q", info.Platform, want)
	}
	if info.GoVersion == "" {
		t.Error("GoVersion is empty")
	}
}
