package apidsource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/storage"
)

var ErrRetrySourceUnavailable = errors.New("original source archive is unavailable")

func publishRetrySource(ctx context.Context, be storage.StorageBackend, originalKey, destinationKey string, p EnqueueParams) error {
	var source io.ReadCloser
	if be != nil && originalKey != "" {
		var err error
		source, err = be.Get(ctx, originalKey)
		if err != nil && !errors.Is(err, storage.ErrNotFound) && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read retained source: %w", err)
		}
	}
	if source == nil {
		//nolint:forbidigo // SourcePath is a persisted, server-created spool path.
		f, err := os.Open(p.SourcePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("%w: %w", ErrRetrySourceUnavailable, err)
			}
			return fmt.Errorf("read original source archive: %w", err)
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() != p.SourceBytes {
			_ = f.Close()
			return fmt.Errorf("%w: retained archive is not a regular file of the original size", ErrRetrySourceUnavailable)
		}
		source = f
	}
	defer func() { _ = source.Close() }()
	if be == nil {
		return nil
	}
	return be.Put(ctx, destinationKey, source)
}
