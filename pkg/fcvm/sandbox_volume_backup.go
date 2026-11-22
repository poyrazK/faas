package fcvm

// adr: 20261122132055 §4 — a volume's backup is taken from a point-in-time
// clone of it, so the VM may keep running: the clone is crash-consistent
// (ext4's journal recovers it as after a power cut) and a paused session's
// is exact. The node keeps the clone the last backup read; the next backup
// clones again and reads only the blocks that differ between the two
// (storage.ChangedRanges), so on the node's XFS a backup costs the blocks
// written since the last one. Where the filesystem cannot share blocks the
// clone is a sparse copy and every chunk is read; the chunks the store
// already holds are still not uploaded again.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/volbackup"
)

// The node's files beside a volume: the clone the last backup read, the
// clone a backup is reading, and the last backup's manifest.
const (
	volumeBackupBaseSuffix     = ".backup-base"
	volumeBackupNextSuffix     = ".backup-next"
	volumeBackupManifestSuffix = ".backup.json"
)

// SandboxVolumeBackup is one backup as the node reports it.
type SandboxVolumeBackup struct {
	Manifest volbackup.Manifest
	Stats    volbackup.Stats
	// Incremental reports a backup that read only the changed chunks.
	Incremental bool
}

// Backup stores backup id of the session's volume in store.
func (v *SandboxVolumes) Backup(ctx context.Context, store storage.StorageBackend, accountID, sandboxID, id string) (SandboxVolumeBackup, error) {
	if store == nil {
		return SandboxVolumeBackup{}, errors.New("fcvm: sandbox volume backup needs a store")
	}
	path, err := v.Path(accountID, sandboxID)
	if err != nil {
		return SandboxVolumeBackup{}, err
	}
	next := path + volumeBackupNextSuffix
	if _, err := storage.CloneFile(next, path); err != nil {
		return SandboxVolumeBackup{}, fmt.Errorf("fcvm: sandbox volume backup: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(next)
		}
	}()
	prev, changed := v.lastBackup(path, next)
	m, st, err := volbackup.Backup(ctx, store, accountID, sandboxID, id, next, prev, changed)
	if err != nil {
		return SandboxVolumeBackup{}, err
	}
	if err := v.keepBackupBase(path, next, m); err != nil {
		return SandboxVolumeBackup{}, err
	}
	keep = true
	return SandboxVolumeBackup{Manifest: m, Stats: st, Incremental: prev != nil && changed != nil}, nil
}

// lastBackup is the node's last backup of the volume and the ranges that
// changed since. Without one it is nil and the clone's data ranges, so a
// first backup skips the sparse volume's holes; nil ranges read it all.
func (v *SandboxVolumes) lastBackup(path, next string) (*volbackup.Manifest, []storage.Range) {
	firstBackup := func() (*volbackup.Manifest, []storage.Range) {
		data, err := storage.DataRanges(next)
		if err != nil {
			return nil, nil
		}
		if data == nil {
			data = []storage.Range{}
		}
		return nil, data
	}
	raw, err := os.ReadFile(path + volumeBackupManifestSuffix) //nolint:forbidigo // a node-owned file beside the volume.
	if err != nil {
		return firstBackup()
	}
	var m volbackup.Manifest
	if json.Unmarshal(raw, &m) != nil {
		return firstBackup()
	}
	changed, err := storage.ChangedRanges(path+volumeBackupBaseSuffix, next)
	if err != nil {
		// No clone to compare with, or a filesystem without extent maps:
		// the previous manifest still names the chunks the store holds.
		return &m, nil
	}
	if changed == nil {
		changed = []storage.Range{}
	}
	return &m, changed
}

// keepBackupBase makes next the clone the next backup compares with, and m
// its manifest.
func (v *SandboxVolumes) keepBackupBase(path, next string, m volbackup.Manifest) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := path + volumeBackupManifestSuffix + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("fcvm: keep sandbox volume backup: %w", err)
	}
	if err := os.Rename(next, path+volumeBackupBaseSuffix); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("fcvm: keep sandbox volume backup: %w", err)
	}
	if err := os.Rename(tmp, path+volumeBackupManifestSuffix); err != nil {
		return fmt.Errorf("fcvm: keep sandbox volume backup: %w", err)
	}
	return nil
}

// RestoreBackup replaces the session's volume on this node with backup id,
// for a session whose node copy is gone (ADR-20261122132055 §4). The next
// backup compares with the restored volume.
func (v *SandboxVolumes) RestoreBackup(ctx context.Context, store storage.StorageBackend, accountID, sandboxID, id string) (string, error) {
	if store == nil {
		return "", errors.New("fcvm: sandbox volume restore needs a store")
	}
	path, err := v.Path(accountID, sandboxID)
	if err != nil {
		return "", err
	}
	m, err := volbackup.ReadManifest(ctx, store, accountID, sandboxID, id)
	if err != nil {
		return "", err
	}
	v.dropBackupBase(path)
	if err := volbackup.Restore(ctx, store, accountID, m, path); err != nil {
		return "", err
	}
	next := path + volumeBackupNextSuffix
	if _, err := storage.CloneFile(next, path); err == nil {
		if err := v.keepBackupBase(path, next, m); err != nil {
			_ = os.Remove(next)
		}
	}
	return path, nil
}

// dropBackupBase forgets the node's last backup of the volume.
func (v *SandboxVolumes) dropBackupBase(path string) {
	for _, suffix := range []string{volumeBackupBaseSuffix, volumeBackupNextSuffix, volumeBackupManifestSuffix} {
		_ = os.Remove(path + suffix)
	}
}
