package fcvm

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/mod/semver"
)

// Diff captures for persistent sandbox sessions (ADR-20261122132055,
// amendment "diff capture merged at pause").
//
// A session restored from its last capture loads with KVM dirty page
// tracking on. Its next capture asks Firecracker for a Diff snapshot (only
// the pages written since the load or the last capture) and vmmd merges it
// locally: the parent image is cloned (FICLONE) to the capture's destination
// and the dirtied pages are written over it. What is published is an
// ordinary full memory image, so restore, upload, storage and retention see
// no chain and no new format. A session that was cold booted, or whose
// parent is unknown, takes a Full capture as before; any failure after a
// Diff falls back to a Full capture of the still-paused guest, which never
// depends on the dirty bitmap.
//
// The parent is the file the guest's clean pages come from: the restored
// image (staged in the jail as memSnapshotName) until the first capture,
// then the image that capture produced, held open so its blocks survive the
// publication's temp file and cache eviction.

// dirtyTrackingLoadKeyMinFirecracker is the first release whose
// /snapshot/load takes track_dirty_pages; earlier ones take
// enable_diff_snapshots (deprecated since, still accepted by v1.17).
const dirtyTrackingLoadKeyMinFirecracker = "v1.14.0"

// DirtyTrackingLoadKey is the /snapshot/load field that turns on dirty page
// tracking on a detected Firecracker version ("1.17.0").
func DirtyTrackingLoadKey(fcVersion string) string {
	v := "v" + fcVersion
	if semver.IsValid(v) && semver.Compare(v, dirtyTrackingLoadKeyMinFirecracker) >= 0 {
		return "track_dirty_pages"
	}
	return "enable_diff_snapshots"
}

// sandboxDiffSnapshots is the JailerVMM's diff capture state. The zero value
// is disabled.
type sandboxDiffSnapshots struct {
	mu sync.Mutex
	// loadKey is DirtyTrackingLoadKey's answer, or "" when disabled.
	loadKey string
	// tracked holds every instance loaded with dirty tracking. Its parent is
	// nil until a capture produces one (the restored image is used then);
	// an instance whose bitmap no longer matches any known image is removed.
	tracked map[string]*sandboxDiffParent
}

type sandboxDiffParent struct {
	file *os.File // nil: the restored image in the jail
}

// WithSandboxDiffSnapshots turns on diff captures for persistent sandbox
// sessions on the given Firecracker version. Off by default (built dark).
func (v *JailerVMM) WithSandboxDiffSnapshots(enabled bool, fcVersion string) *JailerVMM {
	v.sandboxDiff.mu.Lock()
	defer v.sandboxDiff.mu.Unlock()
	v.sandboxDiff.loadKey = ""
	if enabled {
		v.sandboxDiff.loadKey = DirtyTrackingLoadKey(fcVersion)
	}
	return v
}

// loadKeyFor is the dirty tracking field to add to a restore's
// /snapshot/load, or "" when this restore does not track. Only a persistent
// sandbox session restored from its own capture tracks (it will be captured
// again); native recovery keeps its own capture producer.
func (s *sandboxDiffSnapshots) loadKeyFor(spec RestoreSpec, nativeRecovery bool) string {
	if !spec.sandboxSessionCapture || nativeRecovery || spec.Storage != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadKey
}

// loaded records that instance was loaded with dirty tracking.
func (s *sandboxDiffSnapshots) loaded(instance string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tracked == nil {
		s.tracked = map[string]*sandboxDiffParent{}
	}
	s.dropLocked(instance)
	s.tracked[instance] = &sandboxDiffParent{}
}

// beginCapture returns the parent image for a Diff capture of instance, or
// nil for a Full one. The caller owns the returned file. root is the
// instance's jail root.
func (s *sandboxDiffSnapshots) beginCapture(instance, root string) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.tracked[instance]
	if !ok {
		return nil, nil
	}
	if p.file != nil {
		// The caller closes its own descriptor; the one kept here stays.
		// Readers use ReadAt and FICLONE, so the shared offset is unused.
		fd, err := syscall.Dup(int(p.file.Fd()))
		if err != nil {
			return nil, fmt.Errorf("dup diff parent: %w", err)
		}
		return os.NewFile(uintptr(fd), p.file.Name()), nil
	}
	// nolint:forbidigo // the restored image vmmd staged into this jail.
	f, err := os.Open(filepath.Join(root, memSnapshotName))
	if err != nil {
		return nil, fmt.Errorf("open restored image: %w", err)
	}
	return f, nil
}

// captured records the image a capture of instance produced as the parent of
// its next one. Firecracker reset the dirty bitmap at that capture, so
// whatever image it produced, Diff or Full, is the only valid parent. image
// nil (no file to keep) makes the next capture Full.
func (s *sandboxDiffSnapshots) captured(instance string, image *os.File) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tracked[instance]; !ok {
		if image != nil {
			_ = image.Close()
		}
		return
	}
	s.dropLocked(instance)
	if image == nil {
		return
	}
	s.tracked[instance] = &sandboxDiffParent{file: image}
}

// forget drops instance's state, closing a held parent.
func (s *sandboxDiffSnapshots) forget(instance string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropLocked(instance)
}

func (s *sandboxDiffSnapshots) dropLocked(instance string) {
	if p := s.tracked[instance]; p != nil && p.file != nil {
		_ = p.file.Close()
	}
	delete(s.tracked, instance)
}

// sandboxDiffParent opens the parent image for a Diff capture of instance,
// or returns nil for a Full one. Native captures (their own producer) are
// always Full.
func (v *JailerVMM) sandboxDiffParent(instance, root string, native bool) (*os.File, error) {
	if native || v.nativeRecovery != nil {
		return nil, nil
	}
	return v.sandboxDiff.beginCapture(instance, root)
}

// mergeDiffCapture merges the Diff Firecracker wrote at diffPath over parent
// into a vmmd temp file (TMPDIR, the node's /srv/fc filesystem, so parent
// clones into it) and returns that file's path. diffPath is removed on
// success.
func (v *JailerVMM) mergeDiffCapture(ctx context.Context, l Lease, parent *os.File, diffPath string) (string, error) {
	start := time.Now()
	tmp, err := os.CreateTemp("", snapshotTempPattern(l.Instance, ".mem"))
	if err != nil {
		return "", fmt.Errorf("alloc merged image: %w", err)
	}
	path := tmp.Name()
	_ = tmp.Close()
	diffBytes := allocatedBytesOrLogical(diffPath, -1)
	size, reflinked, err := mergeDiffSnapshot(ctx, parent, diffPath, path)
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	_ = os.Remove(diffPath)
	slog.Default().Info("vmm: diff capture merged", "instance", l.Instance, "mem_bytes", size,
		"diff_bytes", diffBytes, "reflink", reflinked, "merge_ms", time.Since(start).Milliseconds())
	return path, nil
}

// openDiffParent opens a capture's exported image to be the next capture's
// parent, or returns nil (next capture Full) when it cannot be held: an image
// on tmpfs would pin a guest's worth of RAM for as long as the session runs.
func openDiffParent(path string) *os.File {
	if path == "" {
		return nil
	}
	// nolint:forbidigo // vmmd's own capture output.
	f, err := os.Open(path)
	if err != nil {
		slog.Default().Warn("vmm: keep diff capture parent", "path", path, "err", err)
		return nil
	}
	if tmpfs, statErr := onTmpfs(path); statErr != nil || tmpfs {
		_ = f.Close()
		return nil
	}
	return f
}

// tracking reports whether instance was loaded with dirty tracking.
func (s *sandboxDiffSnapshots) tracking(instance string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.tracked[instance]
	return ok
}
