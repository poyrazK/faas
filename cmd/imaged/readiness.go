// Package main — readiness.go constructs the imaged-side
// /readyz probe (issue #571 PR-A2). imaged is the layer-image
// cache daemon (snapshots + ext4 base images + pull-on-miss).
// /readyz checks the storage root + cache dir are present and
// writable.
//
// Why writability (vs. e.g. "cache DB up"): the spec names the
// storage paths as the single load-bearing resource imaged
// needs to serve traffic — the in-memory cache is a hot
// accelerator, but the on-disk root is the source of truth.
// A full disk or a permission-broken mount flips /readyz to
// 503 immediately so the LB stops routing traffic before the
// next pull-on-miss attempt wedges.
//
// Result cache: the probe caches each check's verdict for 5 s
// so a Prometheus scrape at 1 s doesn't turn into 4 syscalls
// per daemon per second. The cache key is the absolute path;
// the cache value is the (ready, reason) tuple.
package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// defaultStorageRoot is the canonical /srv/fc path imaged
// writes cache layers to. It is read from FAAS_STORAGE_ROOT
// at boot via envOr() in main.go; the constant here is the
// fallback so a test path can call BuildReadinessProbe
// without env wiring.
const defaultStorageRoot = "/srv/fc"

// BuildReadinessProbe constructs the imaged /readyz probe.
// storageRoot is the path imaged writes cache layers to
// (production: /srv/fc; tests: any tmpdir). The probe
// checks writability on a 5 s cadence with a 1 s timeout per
// stat call.
//
// Returns the probe + a stop func the daemon boot path
// defers. stop() halts the helper goroutines.
func BuildReadinessProbe(storageRoot string) *wire.ReadyzProbe {
	if storageRoot == "" {
		storageRoot = defaultStorageRoot
	}
	cacheDir := filepath.Join(storageRoot, "cache")

	return buildWritableReadinessProbe(storageRoot, cacheDir)
}

func buildWritableReadinessProbe(paths ...string) *wire.ReadyzProbe {
	p := &wire.ReadyzProbe{}
	for _, path := range paths {
		sig, stop := writableSignal(path, 5*time.Second)
		p.RegisterSignal(sig, stop)
	}
	return p
}

// writableSignal returns a (*ReadySignal, stopper). The signal
// reports whether path is openable for write; the check caches
// the verdict for `cacheFor` so a 1 s scrape cadence doesn't
// turn into 2 syscalls per scrape.
//
// Path semantics:
//
//   - missing path: signal flips false with "ENOENT" in the
//     reason.
//   - non-writable path (perm denied): signal flips false with
//     "EACCES" in the reason.
//   - timed-out check: signal flips false with "timeout" in
//     the reason.
//
// stopper is idempotent (sync.Once); callers may defer it
// without guarding against double-stop.
func writableSignal(path string, cacheFor time.Duration) (*wire.ReadySignal, func()) {
	s := &wire.ReadySignal{}
	s.Set(false, "not yet checked")
	stop := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once
	stopper := func() {
		stopOnce.Do(func() {
			close(stop)
			<-done
			s.Set(false, "imaged stopping")
		})
	}
	go func() {
		defer close(done)
		t := time.NewTicker(cacheFor)
		defer t.Stop()
		check := func() {
			// Bound the check so a wedged NFS mount can't stall
			// the readiness loop.
			doneCh := make(chan struct{})
			go func() {
				defer close(doneCh)
				if err := checkWritable(path); err != nil {
					s.Set(false, err.Error())
					return
				}
				s.Set(true, "")
			}()
			select {
			case <-doneCh:
				return
			case <-time.After(time.Second):
				s.Set(false, "timeout")
			}
		}
		check()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				check()
			}
		}
	}()
	return s, stopper
}

// checkWritable tests whether path is writeable. Three cases:
//
//   - path exists and is a directory: try to create a tempfile
//     inside it (mkstemp semantics) and immediately remove it.
//     This is the canonical "is this dir writable" test —
//     os.OpenFile on a directory fails with EISDIR.
//   - path exists and is a file: OpenFile with O_WRONLY (no
//     O_CREATE, no truncate).
//   - path does not exist: OpenFile with O_WRONLY|O_CREATE on
//     the path itself; success proves the parent dir is
//     writable AND the path's name is not in use (we then
//     remove the sentinel file).
//
// The test is non-destructive in every case: dir-test temp
// files are removed immediately; sentinel files created when
// the path is missing are also removed.
func checkWritable(path string) error {
	if path == "" {
		return errors.New("empty path")
	}
	info, err := os.Stat(path)
	switch {
	case err == nil:
		if info.IsDir() {
			return checkDirWritable(path)
		}
		return checkFileWritable(path)
	case errors.Is(err, os.ErrNotExist):
		// Try creating a sentinel file at the path.
		f, cerr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if cerr != nil {
			return cerr
		}
		_ = f.Close()
		_ = os.Remove(path) // best-effort cleanup
		return nil
	default:
		return err
	}
}

// checkDirWritable creates a tempfile inside dir via
// os.CreateTemp and immediately removes it. The temp file's
// create+remove cycle proves the dir is writable without
// leaving anything on disk.
func checkDirWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".imaged-readyz-*")
	if err != nil {
		return err
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(f.Name())
		return cerr
	}
	return os.Remove(f.Name())
}

// checkFileWritable tests whether the file at path is
// openable for write. Uses O_WRONLY (no O_CREATE, no truncate)
// so a successful open proves write permission without
// disturbing the file's contents.
func checkFileWritable(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func buildImageReadinessProbe(kind, storageRoot, cacheRoot string) *wire.ReadyzProbe {
	// OCI writes the configured local cache; the local-backend root can be read-only.
	if kind == "oci" && cacheRoot != "" {
		return buildWritableReadinessProbe(cacheRoot)
	}
	return BuildReadinessProbe(storageRoot)
}
