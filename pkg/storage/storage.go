// Package storage is the artifact-manipulation surface introduced in
// issue #96 / ADR-025 axis 2 (slice 1). It abstracts local filesystem
// reads and writes for app/base layers, kernel images, and VM snapshot
// blobs behind a single interface so a future OCI or object-storage
// driver can serve the same logical keys without changing call sites
// in pkg/imaged, pkg/rootfs, pkg/fcvm, or pkg/sched.
//
// The local driver (LocalStorageBackend) is the reference implementation
// and preserves today's `/srv/fc` + `/var/lib/faas/apps` layout 1:1 —
// rolling the interface in does not require any ops change on a
// single-box deploy. Remote drivers are explicitly future per the
// issue; this slice ships the seam, not the remote backend.
//
// Key contract (see validateKey):
//
//   - slash-separated, non-empty, no leading slash, no backslashes,
//     no NUL bytes;
//   - no "." or ".." segments, no cleaned-to-empty / cleaned-to-dot;
//   - all components must be non-empty after split.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// StorageBackend is the artifact-manipulation surface introduced in
// issue #96. LocalStorageBackend is the reference implementation in
// pkg/storage. OCIRegistryStorageBackend (future slice) serves the same
// keys from a registry-v2 / OCI distribution backend.
//
// All operations are context-aware; a cancelled context must propagate
// to the underlying I/O and surface as ctx.Err. The local driver does
// this by aborting mid-copy on ctx.Done() via copyContext.
//
// Errors:
//
//   - Get returns ErrNotFound (wrapped) when the key is absent — call
//     sites use errors.Is(err, ErrNotFound) to drive cold-boot fallback
//     (ADR-005) and the recovery path for missing snapshots;
//   - Put/Get/Delete wrap the underlying error with %w + an operation
//     tag (operation context) so a failure carries both the storage
//     operation and the upstream cause;
//   - Put/Delete reject invalid keys (empty, absolute, "..", etc.) with
//     ErrInvalidKey before touching disk.
type StorageBackend interface {
	Put(ctx context.Context, key string, r io.Reader) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// LocalArtifactLister is an OPTIONAL capability LocalStorageBackend
// implements. Remote drivers (future) are NOT required to implement it.
// Callers that need list semantics (e.g. imaged's nightly GC) type-
// assert before calling; absence is not an error, it just means the
// remote driver does its own garbage collection.
//
// List returns every key under prefix, in arbitrary order. An empty
// prefix lists the entire backend. The returned slice is owned by the
// caller and may be sorted or filtered as needed.
type LocalArtifactLister interface {
	List(ctx context.Context, prefix string) ([]string, error)
}

// ErrNotFound is the canonical sentinel for a missing key. Callers
// across the imaged + vmmdgrpc packages used errors.Is(err, os.ErrNotExist)
// in single-box mode; the local backend still wraps os.ErrNotExist
// with %w so the existing idioms keep working, but new code MUST
// branch on errors.Is(err, ErrNotFound) to stay portable across the
// future remote driver.
var ErrNotFound = errors.New("storage: key not found")

// ErrInvalidKey is the canonical sentinel for a key that fails
// validation. Callers must not retry with the same key; correcting
// the key is the only fix. This is returned from every Put/Get/Delete
// before the operation touches disk or network, so a misuse never
// partially succeeds.
var ErrInvalidKey = errors.New("storage: invalid key")

// IsNotFound reports whether err is (or wraps) ErrNotFound. Use this
// in cold-boot-fallback code paths so callers stay agnostic of the
// underlying driver — the local backend's underlying os.ErrNotExist
// also satisfies the predicate via its %w chain.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// IsInvalidKey reports whether err is (or wraps) ErrInvalidKey.
func IsInvalidKey(err error) bool {
	return errors.Is(err, ErrInvalidKey)
}

// validateKey enforces the key contract documented at the package
// level. It returns ErrInvalidKey wrapped with the offending key when
// any rule fails. The check is split into two passes:
//
//  1. cheap rejections (empty, absolute, separator, NUL, "..");
//  2. structural clean (filepath.Clean) + per-segment rejection so
//     cleaned-to-empty / cleaned-to-dot keys are also caught.
//
// After both passes pass, the caller is still responsible for the
// defense-in-depth check that filepath.Join(root, key) starts with
// root+separator — LocalStorageBackend does that explicitly because
// the cost is dominated by disk anyway.
func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: empty", ErrInvalidKey)
	}
	if key[0] == '/' || key[0] == filepath.Separator {
		return fmt.Errorf("%w: %q starts with separator", ErrInvalidKey, key)
	}
	if strings.ContainsAny(key, "\\\x00") {
		return fmt.Errorf("%w: %q contains backslash or NUL", ErrInvalidKey, key)
	}
	if strings.Contains(key, "..") {
		return fmt.Errorf("%w: %q contains '..' segment", ErrInvalidKey, key)
	}
	cleaned := filepath.ToSlash(filepath.Clean(key))
	if cleaned == "" || cleaned == "." || cleaned == "/" {
		return fmt.Errorf("%w: %q cleans to %q", ErrInvalidKey, key, cleaned)
	}
	if cleaned != key {
		// filepath.Clean collapses "a//b" → "a/b" and "a/./b" → "a/b";
		// rejecting those keeps keys canonical so List + Delete agree
		// on the canonical form. The ".." check above catches the
		// only other transformation Clean applies.
		return fmt.Errorf("%w: %q is not canonical", ErrInvalidKey, key)
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == "" {
			return fmt.Errorf("%w: %q has empty segment", ErrInvalidKey, key)
		}
	}
	return nil
}
