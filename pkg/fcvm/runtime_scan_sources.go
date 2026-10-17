package fcvm

import (
	"context"
	"errors"
	"os"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
)

func (m *Manager) stageRuntimeScanSource(ctx context.Context, source runtimeadmission.ArtifactSource) (path string, err error) {
	reader, err := m.storage.Get(ctx, source.StorageKey)
	if err != nil {
		return "", err
	}
	if reader == nil {
		return "", runtimeadmission.ErrInvalid
	}
	closeReader := closeRuntimeSourceReaderOnCancel(ctx, reader)
	defer func() { err = errors.Join(err, closeReader()) }()
	file, err := os.CreateTemp(vmmdmount.MountRoot, parentSrcPrefix)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
		if err != nil {
			err = errors.Join(err, os.Remove(file.Name()))
		}
	}()
	err = writeSealedRuntimeSource(ctx, file, reader, source)
	err = errors.Join(err, closeReader(), file.Close(), ctx.Err())
	if err == nil {
		err = os.Chmod(file.Name(), 0o444)
	}
	if err != nil {
		return "", err
	}
	return file.Name(), nil
}

func (m *Manager) mountRuntimeScanSource(ctx context.Context, source runtimeadmission.ArtifactSource, lease *vmmdmount.MountLease) (string, error) {
	path, err := m.stageRuntimeScanSource(ctx, source)
	if err != nil {
		return "", err
	}
	mp, err := vmmdmount.MountExt4ReadOnly(ctx, path)
	if err != nil {
		return "", errors.Join(err, os.Remove(path))
	}
	if err := lease.Attach(mp, vmmdmount.MountKindParentExt4, source.StorageKey, path); err != nil {
		return "", errors.Join(err, cleanupUnregisteredParent(ctx, mp, path))
	}
	return mp, nil
}
