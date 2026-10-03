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
func (m *Manager) MaterializeVerifiedParentExt4(ctx context.Context, expected imagechain.ParentMaterialization) (imagechain.ParentMaterialization, error) {
	if !expected.Valid() {
		return imagechain.ParentMaterialization{}, fmt.Errorf("invalid parent materialization")
	}
	mp, err := m.mountParentExt4(ctx, expected.Artifact.StorageKey, &expected.Artifact)
	if err != nil {
		return imagechain.ParentMaterialization{}, err
	}
	copyErr := vmmdmount.MaterializeParentExt4(ctx, mp, expected.TargetDir)
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
	defer cancel()
	cleanupErr := m.cleanupVerifiedParent(cleanupCtx, mp)
	if err := errors.Join(copyErr, cleanupErr, ctx.Err()); err != nil {
		return imagechain.ParentMaterialization{}, err
	}
	return expected, nil
}

func (m *Manager) cleanupVerifiedParent(ctx context.Context, mp string) error {
	entry, ok := m.parentMounts.Lookup(mp)
	if !ok {
		return fmt.Errorf("parent materialization mount ownership lost")
	}
	if err := m.UmountParentExt4(ctx, mp); err != nil {
		return err
	}
	for _, name := range []string{entry.SrcPath, mp} {
		if name == "" {
			return fmt.Errorf("parent materialization cleanup path missing")
		}
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("parent materialization cleanup: %w", err)
		}
	}
	return ctx.Err()
}
