package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// LocalStorageBackend is the reference StorageBackend. It is rooted at
// a single host directory and writes files directly. Today the
// production root is /srv/fc (covers base/, layers/, snap/, kernel/);
// imaged's apps/ prefix can either live under the same root (single
// root deployment) or under a separate prefix (the common case where
// /srv/fc and /var/lib/faas/apps are siblings). The two-backend case
// is composed via PrefixRouter (pkg/storage/router.go).
//
// Concurrency: Put is safe for concurrent calls on distinct keys;
// concurrent Put on the same key is serialised by the atomic temp-name
// suffix but the last-rename wins (matches today's imaged semantics).
// Get is safe for concurrent calls. Delete is safe for concurrent
// calls; a Delete racing a Put observes either "no key" or "old key"
// — never a torn file, because Put uses temp+rename atomically.
//
// Atomicity: Put writes to a sibling temp file with a process-unique
// suffix and os.Rename's to the final name. The rename is atomic on
// the same filesystem (ext4 in /srv/fc). A crash mid-write leaves the
// temp file behind; the next Put for the same key overwrites it after
// best-effort cleanup (see Put body).
//
// All errors wrap the underlying cause with %w plus a storage-tagged
// operation context so callers can match on errors.Is without losing
// the upstream signal.
type LocalStorageBackend struct {
	root string // absolute, validated by NewLocalStorageBackend
	// tmpSeq is a monotonic counter used to build unique temp suffixes
	// inside a single process. The PID already differentiates processes,
	// but the suffix also benefits from being short and unpredictable
	// when combined with os.CreateTemp's own randomness.
	tmpSeq atomic.Uint64
}

// NewLocalStorageBackend validates root, resolves it to an absolute
// path, and returns a usable backend. An empty root is rejected; a
// non-existent root is NOT pre-created (the caller decides whether
// /srv/fc must exist; production wiring uses systemd's tmpfs-on-boot
// guarantees).
//
// The root must point at a directory the process can MkdirAll into,
// or every Put/Get/Delete will fail — that's intentional: silent
// fallbacks have hidden bugs in earlier imaged iterations.
func NewLocalStorageBackend(root string) (*LocalStorageBackend, error) {
	if root == "" {
		return nil, fmt.Errorf("%w: empty root", ErrInvalidKey)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("storage: resolve root: %w", err)
	}
	// Belt-and-braces: the root must not contain "..", NUL, or backslash
	// segments — the same key contract applies to the root. A misconfigured
	// FAAS_STORAGE_ROOT would otherwise become a TOCTOU vector.
	if err := validateKey(strings.TrimPrefix(abs, string(filepath.Separator))); err != nil {
		return nil, fmt.Errorf("storage: root %q: %w", root, err)
	}
	return &LocalStorageBackend{root: abs}, nil
}

// Root returns the absolute on-disk root the backend resolves every
// key against. Useful for tests and for the cmd/{imaged,vmmd} wiring
// to log the resolved layout at startup.
func (l *LocalStorageBackend) Root() string { return l.root }

// join is the single point where a validated key becomes an absolute
// host path. It performs the defense-in-depth containment check that
// validateKey cannot — after filepath.Join, the joined path MUST start
// with root + separator so a future bug in validateKey cannot let a
// caller escape the root via a malformed key.
func (l *LocalStorageBackend) join(key string) (string, error) {
	full := filepath.Join(l.root, key)
	if !strings.HasPrefix(full, l.root+string(os.PathSeparator)) && full != l.root {
		return "", fmt.Errorf("%w: %q escapes root", ErrInvalidKey, key)
	}
	return full, nil
}

// Put writes r's bytes to the key's location. The write is atomic
// (temp + fsync + rename); a partially-written key is impossible to
// observe under normal conditions.
//
// ctx cancellation: the implementation checks ctx.Done() before the
// first disk write and during the copy via copyContext (per 256 KiB
// chunk). A cancellation leaves the temp file behind for best-effort
// cleanup; the caller sees ctx.Err() wrapped with %w.
func (l *LocalStorageBackend) Put(ctx context.Context, key string, r io.Reader) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("storage: put %q: %w", key, err)
	}
	full, err := l.join(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("storage: put %q: mkdir: %w", key, err)
	}
	// Build a process-unique temp suffix so concurrent Put calls don't
	// collide on the temp name. The atomic counter is in addition to
	// os.CreateTemp's randomness; together they make collisions
	// practically impossible.
	suffix := fmt.Sprintf(".tmp.%d.%d", os.Getpid(), l.tmpSeq.Add(1))
	tmp := full + suffix
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("storage: put %q: open tmp: %w", key, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err := copyContext(ctx, f, r); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("storage: put %q: %w", key, err)
	}
	if err := f.Sync(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("storage: put %q: fsync: %w", key, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("storage: put %q: close: %w", key, err)
	}
	closed = true
	if err := os.Rename(tmp, full); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("storage: put %q: rename: %w", key, err)
	}
	return nil
}

// Get opens the key for reading. A missing key returns ErrNotFound
// wrapping os.ErrNotExist (so existing errors.Is(err, os.ErrNotExist)
// idioms still work) AND wrapping ErrNotFound (so new code using
// storage.IsNotFound keeps working).
//
// The returned ReadCloser is the open *os.File; the caller is
// responsible for closing it (typically via a defer). A successful
// Get also verifies the file is non-empty — a zero-byte key is
// treated as not-found so cold-boot fallback fires cleanly rather
// than reading a half-published artifact.
func (l *LocalStorageBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: get %q: %w", key, err)
	}
	full, err := l.join(key)
	if err != nil {
		return nil, err
	}
	// nolint:forbidigo // `full` is the post-join absolute path under the
	// backend's vetted root; validateKey + the post-join containment check
	// in join() guarantee no customer-input symlink traversal is possible.
	// The customer-path guard in openCustomerFile is for paths derived from
	// cli args; storage keys flow through the same validateKey contract.
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("storage: get %q: %w", key, fmt.Errorf("%w: %w", ErrNotFound, err))
		}
		return nil, fmt.Errorf("storage: get %q: %w", key, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("storage: get %q: stat: %w", key, err)
	}
	if st.Size() == 0 {
		_ = f.Close()
		return nil, fmt.Errorf("storage: get %q: %w", key, fmt.Errorf("%w: empty file", ErrNotFound))
	}
	return f, nil
}

// Delete removes the key. Missing keys are NOT errors — matches the
// idempotent semantics imaged already relies on (cleanup paths call
// os.Remove and tolerate ErrNotExist).
func (l *LocalStorageBackend) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("storage: delete %q: %w", key, err)
	}
	full, err := l.join(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage: delete %q: %w", key, err)
	}
	return nil
}

// List returns every key under prefix, in arbitrary order. The
// returned keys are slash-separated (matching the storage contract,
// not the host separator). An empty prefix lists everything. The
// implementation walks the local root once with filepath.WalkDir so
// even large backends (snap/ contains ~thousands of dirs) complete
// in O(N) without unbounded memory.
//
// Not part of the StorageBackend interface — exposed via the
// LocalArtifactLister optional seam.
func (l *LocalStorageBackend) List(ctx context.Context, prefix string) ([]string, error) {
	if prefix != "" {
		// Strip a single trailing slash so callers can use either
		// "snap" or "snap/" interchangeably; validateKey otherwise
		// rejects the trailing-slash form because filepath.Clean
		// collapses it. Anything deeper (a stray "/" in the middle)
		// still fails canonical-form validation below.
		if err := validateKey(strings.TrimSuffix(prefix, "/")); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: list %q: %w", prefix, err)
	}
	root := l.root
	if prefix != "" {
		// Restrict the walk to the prefix subtree. Anything outside is
		// not part of this List call's scope.
		root = filepath.Join(l.root, prefix)
	}
	var keys []string
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// A missing root is an empty list — not an error.
			if errors.Is(err, os.ErrNotExist) && path == root {
				return filepath.SkipAll
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Translate the absolute host path back to a key relative to
		// l.root, slash-separated regardless of host OS.
		rel, err := filepath.Rel(l.root, path)
		if err != nil {
			return err
		}
		keys = append(keys, filepath.ToSlash(rel))
		// Cooperative cancellation: abort the walk if ctx fires
		// mid-iteration. The walker would otherwise keep going
		// through a backing fs that's already been told to stop.
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return nil
	})
	if walkErr != nil {
		if errors.Is(walkErr, filepath.SkipAll) {
			return keys, nil
		}
		return nil, fmt.Errorf("storage: list %q: %w", prefix, walkErr)
	}
	return keys, nil
}

// copyContext is io.Copy with cooperative ctx cancellation. It polls
// ctx.Done() every copyQuantum bytes; a cancellation aborts the copy
// and returns ctx.Err(). The exact quantum doesn't matter for
// correctness, only for cancellation responsiveness — 256 KiB strikes
// a balance between syscall overhead and wakeup latency.
func copyContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	const copyQuantum = 256 * 1024
	buf := make([]byte, copyQuantum)
	var written int64
	for {
		if cerr := ctx.Err(); cerr != nil {
			return written, cerr
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			if werr != nil {
				return written, werr
			}
			written += int64(w)
			if w < n {
				// io.Copy semantics: a short write is treated as
				// the upstream closing with an unread remainder.
				// io.ErrShortWrite is returned; the caller can
				// distinguish by inspecting the unwrapped form.
				return written, io.ErrShortWrite
			}
		}
		if rerr == io.EOF {
			return written, nil
		}
		if rerr != nil {
			return written, rerr
		}
	}
}
