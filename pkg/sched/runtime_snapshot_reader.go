package sched

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"sort"

	"github.com/onebox-faas/faas/pkg/storage"
)

// RuntimeSnapshotReader is a read-only view of vmmd's private native tree
// plus the canonical backend. Neither sched publication nor GC can use it to
// write or unlink root-owned snapshot objects.
type RuntimeSnapshotReader struct {
	native    *storage.LocalStorageBackend
	canonical storage.StorageBackend
}

func NewRuntimeSnapshotReader(canonical storage.StorageBackend, nativeRoot string) (*RuntimeSnapshotReader, error) {
	if canonical == nil || !filepath.IsAbs(nativeRoot) || filepath.Clean(nativeRoot) != nativeRoot || nativeRoot == "/" {
		return nil, ErrRuntimeSnapshotInvalid
	}
	native, err := storage.NewLocalStorageBackend(nativeRoot)
	if err != nil {
		return nil, err
	}
	return &RuntimeSnapshotReader{native: native, canonical: canonical}, nil
}

func (r *RuntimeSnapshotReader) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	reader, err := r.native.Get(ctx, key)
	if err == nil || !storage.IsNotFound(err) {
		return reader, err
	}
	return r.canonical.Get(ctx, key)
}
func (r *RuntimeSnapshotReader) Put(context.Context, string, io.Reader) error {
	return errors.New("sched: runtime snapshot view is read-only")
}
func (r *RuntimeSnapshotReader) Delete(context.Context, string) error {
	return storage.ErrDeleteUnsupported
}
func (r *RuntimeSnapshotReader) List(ctx context.Context, prefix string) ([]string, error) {
	a, err := r.native.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	lister, ok := r.canonical.(storage.LocalArtifactLister)
	if !ok {
		return nil, storage.ErrIncompleteEnumeration
	}
	b, err := lister.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, key := range append(a, b...) {
		seen[key] = true
	}
	result := make([]string, 0, len(seen))
	for key := range seen {
		result = append(result, key)
	}
	sort.Strings(result)
	return result, nil
}
