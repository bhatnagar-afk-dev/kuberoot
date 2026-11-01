package kubeconfig

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// debounce coalesces bursts of events, e.g. an editor's write+rename or a
	// cloud CLI rewriting a file several times.
	debounce = 300 * time.Millisecond
	// rescanInterval picks up directories that did not exist at startup and
	// whose parents are not watched (e.g. /etc/rancher/k3s after installing k3s).
	rescanInterval = time.Minute
)

// watchTargets is the set of directories to watch and the paths within them
// that matter.
type watchTargets struct {
	dirs       map[string]bool // directories to add to the watcher
	files      map[string]bool // exact paths that matter
	folderDirs map[string]bool // any file directly inside these matters
}

func (t watchTargets) relevant(path string) bool {
	path = filepath.Clean(path)
	return t.files[path] || t.folderDirs[filepath.Dir(path)]
}

func (s *Store) watchTargets() watchTargets {
	t := watchTargets{dirs: map[string]bool{}, files: map[string]bool{}, folderDirs: map[string]bool{}}
	for _, spec := range s.Specs() {
		if spec.Hidden {
			continue
		}
		// Watch the parent: files are often replaced atomically, and a missing
		// file or folder may be created later.
		t.dirs[filepath.Dir(spec.Path)] = true
		t.files[spec.Path] = true
		if spec.Kind == KindFolder {
			for _, d := range ScanDirs(spec.Path) {
				t.dirs[d] = true
				t.folderDirs[d] = true
			}
		}
	}
	return t
}

// Watch reloads the store when kubeconfig files change on disk and calls
// onChange if the result differs. It blocks until ctx is cancelled.
func (s *Store) Watch(ctx context.Context, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()

	watched := map[string]bool{}
	var targets watchTargets
	// sync aligns the watcher with the current sources and reports whether a
	// new directory became watchable.
	sync := func() bool {
		targets = s.watchTargets()
		for d := range watched {
			if !targets.dirs[d] {
				_ = w.Remove(d)
				delete(watched, d)
			}
		}
		added := false
		for d := range targets.dirs {
			if watched[d] {
				continue
			}
			if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
				continue
			}
			if w.Add(d) == nil {
				watched[d] = true
				added = true
			}
		}
		return added
	}
	sync()

	timer := time.NewTimer(debounce)
	timer.Stop()
	ticker := time.NewTicker(rescanInterval)
	defer ticker.Stop()

	// Re-sync before notifying so directories created alongside the change
	// (e.g. a new subfolder) are already watched when onChange runs.
	reload := func() {
		changed := s.Reload()
		sync()
		if changed {
			onChange()
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if targets.relevant(ev.Name) || targets.dirs[filepath.Clean(ev.Name)] {
				timer.Reset(debounce)
			}
		case _, ok := <-w.Errors:
			if !ok {
				return nil
			}
		case <-timer.C:
			reload()
		case <-s.resync:
			sync()
		case <-ticker.C:
			if sync() {
				reload()
			}
		}
	}
}
