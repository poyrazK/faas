// adr: 20261105100000 — the jailer and firecracker executables stay resident on
// the jail tmpfs so the first launch after an idle spell reads no disk.

package fcvm

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
)

// residentExecDir is where vmmd keeps its staged executables, under the jail
// base (a root-only tmpfs in production). The jailer's own children of the base
// are named for the firecracker executable, so a dot name never collides with
// one, and native recovery scans only those.
const residentExecDir = ".faas-resident"

// residentExecTempPrefix names a copy in progress. Staging renames it into
// place, so a leftover one is a crashed vmmd's and startup removes it.
const residentExecTempPrefix = ".stage-"

// residentExecKinds are the executables vmmd launches as root on every VM
// start: the jailer, and the firecracker binary the jailer copies into each
// chroot. Both are read from the node's disk on every launch today.
var residentExecKinds = []string{"jailer", FirecrackerBin}

// residentExec is one verified copy of an installed executable.
type residentExec struct {
	source     string      // resolved installed path the copy was made from
	sourceInfo os.FileInfo // the installed file as it was when copied
	path       string      // the copy; same basename as source
	stagedInfo os.FileInfo // the copy as it was when published
}

// residentExecs holds the staged copies by kind. The zero value is ready.
type residentExecs struct {
	mu     sync.Mutex
	execs  map[string]*residentExec
	warned map[string]string // kind -> the last fallback reason logged
}

// resolveExecutable resolves name on PATH to the real installed file, the way
// the launch always has: jailer names its chroot directory after the real
// binary's basename, so a version symlink must not survive.
func resolveExecutable(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	return path, nil
}

// residentExecutable returns the path to launch for kind: a verified copy on
// the jail tmpfs, or source itself when staging fails. Staging is a cache of
// the installed binary, never a requirement (ADR-005): a failure costs the
// disk read this exists to avoid and nothing else. The copy keeps source's
// basename, which jailer turns into the chroot directory name and native
// recovery recognises a launch by.
func (v *JailerVMM) residentExecutable(kind, source string) string {
	if v == nil || v.chrootBase == "" {
		return source
	}
	path, staged, err := v.resident.resolve(filepath.Join(v.chrootBase, residentExecDir), kind, source)
	if err != nil {
		v.wakePhaseMetrics.ObserveResidentExec(kind, "fallback")
		if v.resident.firstWarning(kind, err) {
			slog.Default().Warn("resident executable unavailable; launching the installed binary",
				"binary", kind, "source", source, "error", err)
		}
		return source
	}
	if staged {
		v.wakePhaseMetrics.ObserveResidentExec(kind, "staged")
		slog.Default().Info("resident executable staged", "binary", kind, "source", source, "path", path)
	}
	return path
}

// PrepareResidentBinaries stages the jailer and firecracker executables on the
// jail tmpfs before vmmd serves wakes, so the first launch after a restart is
// as warm as the rest, and removes copies a previous run left that nothing
// names any more. Best effort and silent on success: a launch stages on demand
// and falls back to the installed binaries.
func (v *JailerVMM) PrepareResidentBinaries() {
	if v == nil || v.chrootBase == "" {
		return
	}
	keep := make([]string, 0, len(residentExecKinds))
	for _, kind := range residentExecKinds {
		source, err := resolveExecutable(kind)
		if err != nil {
			slog.Default().Warn("resident executable not installed", "binary", kind, "error", err)
			continue
		}
		if path := v.residentExecutable(kind, source); path != source {
			keep = append(keep, filepath.Base(path))
		}
	}
	pruneResidentDir(filepath.Join(v.chrootBase, residentExecDir), keep)
}

// resolve returns the copy for kind, making it when none exists or the
// installed file has changed since. staged reports that it was made now.
func (r *residentExecs) resolve(dir, kind, source string) (path string, staged bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur := r.execs[kind]; cur != nil && cur.current(source) {
		return cur.path, false, nil
	}
	next, err := stageResidentExec(dir, source)
	if err != nil {
		return "", false, err
	}
	if r.execs == nil {
		r.execs = make(map[string]*residentExec)
	}
	// A superseded copy is left in place: a launch that was handed its path
	// may not have exec'd it yet. Startup prunes it.
	r.execs[kind] = next
	return next.path, true, nil
}

// firstWarning reports whether err is new for kind, so a launch that fails the
// same way on every wake logs once, not once per wake.
func (r *residentExecs) firstWarning(kind string, err error) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.warned == nil {
		r.warned = make(map[string]string)
	}
	if r.warned[kind] == err.Error() {
		return false
	}
	r.warned[kind] = err.Error()
	return true
}

// current reports whether the copy still stands for source: the installed file
// is the file, size, mode and modification time it was copied at, and the copy
// is the file vmmd published. Two stats on the launch path, both metadata.
func (e *residentExec) current(source string) bool {
	if e.source != source {
		return false
	}
	info, err := os.Stat(source)
	if err != nil || !sameResidentFile(info, e.sourceInfo) {
		return false
	}
	staged, err := os.Lstat(e.path)
	return err == nil && staged.Mode().IsRegular() && sameResidentFile(staged, e.stagedInfo)
}

func sameResidentFile(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}

// stageResidentExec copies source into dir and verifies the copy. The bytes
// are hashed as they are read from the open installed file and again as they
// sit in the copy; the installed file must also be the same file, size and
// modification time before and after, so a binary replaced during the copy
// (an Ansible run swapping the symlink) is refused rather than half-copied.
// What this trusts is what vmmd already trusts when it execs the installed
// path as root: the bytes Ansible put there. The copy is root-owned in a
// root-only directory, which the installed file need not be.
func stageResidentExec(dir, source string) (*residentExec, error) {
	if err := ensureResidentDir(dir); err != nil {
		return nil, err
	}
	src, err := os.Open(source)
	if err != nil {
		return nil, fmt.Errorf("open installed executable: %w", err)
	}
	defer func() { _ = src.Close() }() // read-only
	before, err := src.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat installed executable: %w", err)
	}
	if err := checkResidentSource(before); err != nil {
		return nil, fmt.Errorf("installed executable %s: %w", source, err)
	}
	if byPath, err := os.Stat(source); err != nil || !os.SameFile(byPath, before) {
		return nil, fmt.Errorf("installed executable %s was replaced while opening", source)
	}
	tmp, err := os.CreateTemp(dir, residentExecTempPrefix+"*")
	if err != nil {
		return nil, fmt.Errorf("create resident executable: %w", err)
	}
	tmpPath := tmp.Name()
	published := false
	defer func() {
		if !published {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), src)
	if err != nil {
		return nil, fmt.Errorf("copy installed executable: %w", err)
	}
	after, err := src.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat installed executable after copy: %w", err)
	}
	if n != before.Size() || !sameResidentFile(before, after) {
		return nil, fmt.Errorf("installed executable %s changed while it was copied", source)
	}
	if err := tmp.Chmod(before.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("chmod resident executable: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close resident executable: %w", err)
	}
	var want [sha256.Size]byte
	copy(want[:], hash.Sum(nil))
	if got, err := hashResidentFile(tmpPath); err != nil || got != want {
		return nil, errors.Join(errors.New("resident executable differs from the installed one"), err)
	}
	final := filepath.Join(dir, filepath.Base(source))
	if err := os.Rename(tmpPath, final); err != nil {
		return nil, fmt.Errorf("publish resident executable: %w", err)
	}
	published = true
	staged, err := os.Lstat(final)
	if err != nil {
		return nil, fmt.Errorf("stat resident executable: %w", err)
	}
	return &residentExec{source: source, sourceInfo: before, path: final, stagedInfo: staged}, nil
}

// checkResidentSource refuses what vmmd must not bless by copying: anything but
// a non-empty executable regular file that only its owner can change.
func checkResidentSource(info os.FileInfo) error {
	switch {
	case !info.Mode().IsRegular():
		return errors.New("is not a regular file")
	case info.Size() == 0:
		return errors.New("is empty")
	case info.Mode().Perm()&0o111 == 0:
		return errors.New("is not executable")
	case info.Mode().Perm()&0o022 != 0:
		return fmt.Errorf("is writable by its group or others (mode %04o)", info.Mode().Perm())
	}
	return nil
}

func hashResidentFile(path string) (sum [sha256.Size]byte, err error) {
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer func() { _ = f.Close() }() // read-only
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// ensureResidentDir makes dir a directory only this user can enter, creating it
// if needed. The jail base is already root-only; this refuses a symlink or a
// directory someone else owns rather than copying executables into it.
func ensureResidentDir(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return fmt.Errorf("create resident executable root: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create resident executable directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("stat resident executable directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("resident executable directory %s is not a directory", dir)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("resident executable directory %s is owned by uid %d", dir, stat.Uid)
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("restrict resident executable directory: %w", err)
		}
	}
	return nil
}

// pruneResidentDir removes everything in dir except the named files: executables
// of a version that was replaced, and copies a crashed run left half made.
func pruneResidentDir(dir string, keep []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || slices.Contains(keep, name) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			slog.Default().Warn("resident executable prune failed", "path", filepath.Join(dir, name), "error", err)
		}
	}
}
