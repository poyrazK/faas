package fcvm

// adr: 435. Capacity is reserved before staging a native materialization source.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
)

func (m *Manager) stageParentSource(ctx context.Context, key string, expected *imagechain.BaseArtifact) (path string, err error) {
	rc, err := m.storage.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("%w: %w", vmmdmount.ErrNotFound, err)
	}
	if rc == nil {
		return "", fmt.Errorf("parent mount: missing storage reader")
	}
	closeReader := closeRuntimeSourceReaderOnCancel(ctx, rc)
	defer func() { err = errors.Join(err, closeReader()) }()
	src, err := os.CreateTemp(vmmdmount.MountRoot, parentSrcPrefix)
	if err != nil {
		return "", fmt.Errorf("parent mount: create source: %w", err)
	}
	defer func() {
		_ = src.Close()
		if err != nil {
			err = errors.Join(err, os.Remove(src.Name()))
		}
	}()
	if expected != nil {
		err = writeVerifiedParentSource(ctx, src, rc, *expected)
	} else {
		err = src.Close()
		if err == nil {
			err = streamToPath(rc, src.Name())
		}
	}
	if err := errors.Join(err, closeReader(), ctx.Err()); err != nil {
		return "", fmt.Errorf("parent mount: stage source: %w", err)
	}
	return src.Name(), nil
}
