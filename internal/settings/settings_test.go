package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestOpenMissingFile(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "nope", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); len(got.Sources) != 0 || got.ActiveContext != "" {
		t.Errorf("Get() = %+v, want empty", got)
	}
}

func TestUpdateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "KubeRoot", "settings.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *Settings) {
		s.Sources = append(s.Sources, "/home/me/kube")
		s.ActiveContext = "abc123"
	}); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.Get()
	if !slices.Equal(got.Sources, []string{"/home/me/kube"}) || got.ActiveContext != "abc123" {
		t.Errorf("reopened = %+v", got)
	}

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("perm = %o, want 600", perm)
		}
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1", len(entries))
	}
}

func TestGetReturnsCopy(t *testing.T) {
	st, _ := Open(filepath.Join(t.TempDir(), "s.json"))
	_ = st.Update(func(s *Settings) { s.Sources = []string{"a"} })
	got := st.Get()
	got.Sources[0] = "mutated"
	if st.Get().Sources[0] != "a" {
		t.Error("Get exposed internal slice")
	}
}

func TestUpdateFailureKeepsState(t *testing.T) {
	dir := t.TempDir()
	// Make the target path a directory so rename fails.
	path := filepath.Join(dir, "settings.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	st := &Store{path: path}
	if err := st.Update(func(s *Settings) { s.ActiveContext = "x" }); err == nil {
		t.Fatal("expected error")
	}
	if st.Get().ActiveContext != "" {
		t.Error("in-memory state changed despite failed write")
	}
}

func TestOpenCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(path, []byte("{not json"), 0o600)
	if _, err := Open(path); err == nil {
		t.Fatal("expected parse error")
	}
}
