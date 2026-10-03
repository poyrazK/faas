package fcvm

// adr: 435

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
)

// writeVerifiedParentSource hashes the same complete stream copied into the
// root-owned loopback source. No caller-writable pathname is reopened.
func writeVerifiedParentSource(ctx context.Context, dst *os.File, src io.Reader, expected imagechain.BaseArtifact) error {
	if !expected.Valid() {
		return fmt.Errorf("parent source: invalid artifact identity")
	}
	actual, err := rootfs.ReadArtifactIdentity(ctx, io.TeeReader(io.LimitReader(src, expected.Bytes+1), dst))
	if err != nil {
		return fmt.Errorf("parent source: read complete artifact: %w", err)
	}
	if actual.Digest != expected.Digest || actual.Bytes != expected.Bytes {
		return fmt.Errorf("parent source: artifact identity mismatch")
	}
	if err := dst.Sync(); err != nil {
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return ctx.Err()
}

// MaterializeVerifiedParentExt4 reports the source identity only after the
// verified protected file was mounted, copied and released by this owner.
func (m *Manager) MaterializeVerifiedParentExt4(ctx context.Context, expected imagechain.ParentMaterialization) (actual imagechain.ParentMaterialization, err error) {
	if !expected.Valid() {
		return actual, fmt.Errorf("invalid parent materialization")
	}
	if m.parentMounts == nil {
		return actual, vmmdmount.ErrNotFound
	}
	lease, err := m.parentMounts.ReserveMount(ctx)
	if err != nil {
		return actual, err
	}
	defer func() {
		releaseParentMount(ctx, lease, &err)
		err = errors.Join(err, ctx.Err())
		if err != nil {
			actual = imagechain.ParentMaterialization{}
		}
	}()
	mp, err := m.mountParentExt4(ctx, expected.Artifact.StorageKey, &expected.Artifact, lease)
	if err != nil {
		return actual, err
	}
	if err := vmmdmount.MaterializeParentExt4(ctx, mp, expected.TargetDir); err != nil {
		return actual, err
	}
	return expected, nil
}

// Cleanup uses an independent bound even when the imaging request is canceled.
func releaseParentMount(ctx context.Context, lease *vmmdmount.MountLease, result *error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
	defer cancel()
	*result = errors.Join(*result, lease.Release(cleanupCtx))
}

func cleanupUnregisteredParent(ctx context.Context, mountpoint, source string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
	defer cancel()
	if err := vmmdmount.UmountExt4(cleanupCtx, mountpoint); err != nil && !errors.Is(err, vmmdmount.ErrUnknownMountpoint) {
		return err
	}
	if source == "" {
		return nil
	}
	return os.Remove(source)
}
