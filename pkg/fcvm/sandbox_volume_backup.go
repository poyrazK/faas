package fcvm

// adr: 20261122132055 §4 — a volume's backup is taken from a point-in-time
// clone of it, so the VM may keep running: the clone is crash-consistent
// (ext4's journal recovers it as after a power cut) and a paused session's
// is exact. The node keeps the clone the newest backup read; the next backup
// reads only the blocks that differ between that clone and its own
// (storage.ChangedRanges), so on the node's XFS a backup costs the blocks
// written since the last one. Where the filesystem cannot share blocks the
// clone is a sparse copy and every chunk is read; the chunks the store
// already holds are still not uploaded again.
//
// A backup is prepared (cloned) and uploaded in two steps, so a pause clones
// the volume as its capture left it and uploads it in the background; a
// pending clone survives a vmmd restart and is uploaded then.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/volbackup"
)

// The node's files beside a volume <v>: <v>.backup-pending-<id> is a clone a
// backup has yet to upload, <v>.backup-base-<id> the clone the newest backup
// read, and <v>.backup.json that backup's manifest. The manifest names its
// base, so the two always match: a crash between keeping a base and writing
// the manifest leaves the older pair in force.
const (
	volumeBackupPendingInfix   = ".backup-pending-"
	volumeBackupBaseInfix      = ".backup-base-"
	volumeBackupManifestSuffix = ".backup.json"
	// volumeBackupTakenSuffix names the newest backup this node cloned the
	// volume for, uploaded or not: until the volume changes, the node's
	// copy is that backup.
	volumeBackupTakenSuffix = ".backup-taken"
)

// SandboxVolumeBackup is one backup as the node reports it.
type SandboxVolumeBackup struct {
	Manifest volbackup.Manifest
	Stats    volbackup.Stats
	// Incremental reports a backup that read only the changed chunks.
	Incremental bool
}

// PendingVolumeBackup is a prepared clone of a volume, not yet uploaded.
type PendingVolumeBackup struct {
	v                    *SandboxVolumes
	AccountID, SandboxID string
	ID                   string
	// TakenAt is when the clone was made: the backup's time.
	TakenAt time.Time
	volume  string
	clone   string
}

// lockVolume serializes uploads, restores and removals of one volume.
func (v *SandboxVolumes) lockVolume(path string) func() {
	v.mu.Lock()
	if v.locks == nil {
		v.locks = map[string]*sync.Mutex{}
	}
	l := v.locks[path]
	if l == nil {
		l = &sync.Mutex{}
		v.locks[path] = l
	}
	v.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// PrepareBackup clones the session's volume as it is now, for backup id.
func (v *SandboxVolumes) PrepareBackup(accountID, sandboxID, id string) (*PendingVolumeBackup, error) {
	path, err := v.Path(accountID, sandboxID)
	if err != nil {
		return nil, err
	}
	if !safeVolumeSegment(id) {
		return nil, fmt.Errorf("fcvm: sandbox volume backup id %q is invalid", id)
	}
	clone := path + volumeBackupPendingInfix + id
	taken := time.Now().UTC()
	if _, err := storage.CloneFile(clone, path); err != nil {
		return nil, fmt.Errorf("fcvm: sandbox volume backup: %w", err)
	}
	// Best effort: without the mark a resume here restores the backup from
	// the store once it is uploaded, as before.
	tmp := path + volumeBackupTakenSuffix + ".tmp"
	if os.WriteFile(tmp, []byte(id), 0o600) == nil {
		_ = os.Rename(tmp, path+volumeBackupTakenSuffix)
	}
	return &PendingVolumeBackup{v: v, AccountID: accountID, SandboxID: sandboxID, ID: id, TakenAt: taken, volume: path, clone: clone}, nil
}

// Upload stores the backup. It may be retried after an error; Discard
// drops a backup that will never be uploaded.
func (p *PendingVolumeBackup) Upload(ctx context.Context, store storage.StorageBackend) (SandboxVolumeBackup, error) {
	if store == nil {
		return SandboxVolumeBackup{}, errors.New("fcvm: sandbox volume backup needs a store")
	}
	unlock := p.v.lockVolume(p.volume)
	defer unlock()
	prev, changed := p.v.lastBackup(p.volume, p.clone)
	m, st, err := volbackup.Backup(ctx, store, p.AccountID, p.SandboxID, p.ID, p.clone, prev, changed, p.TakenAt)
	if err != nil {
		return SandboxVolumeBackup{}, err
	}
	if err := p.v.keepBackupBase(p.volume, p.clone, m); err != nil {
		return SandboxVolumeBackup{}, err
	}
	return SandboxVolumeBackup{Manifest: m, Stats: st, Incremental: prev != nil && changed != nil}, nil
}

// Discard removes the pending clone.
func (p *PendingVolumeBackup) Discard() { _ = os.Remove(p.clone) }

// Backup prepares and uploads backup id of the session's volume.
func (v *SandboxVolumes) Backup(ctx context.Context, store storage.StorageBackend, accountID, sandboxID, id string) (SandboxVolumeBackup, error) {
	p, err := v.PrepareBackup(accountID, sandboxID, id)
	if err != nil {
		return SandboxVolumeBackup{}, err
	}
	b, err := p.Upload(ctx, store)
	if err != nil {
		p.Discard()
	}
	return b, err
}

// PendingBackups lists the clones a restart left unuploaded.
func (v *SandboxVolumes) PendingBackups() []*PendingVolumeBackup {
	if v == nil || v.Root == "" {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(v.Root, "*", "*.ext4"+volumeBackupPendingInfix+"*"))
	var out []*PendingVolumeBackup
	for _, clone := range matches {
		volume, id, ok := strings.Cut(clone, volumeBackupPendingInfix)
		if !ok || !safeVolumeSegment(id) {
			continue
		}
		info, err := os.Stat(clone)
		if err != nil {
			continue
		}
		out = append(out, &PendingVolumeBackup{v: v, AccountID: filepath.Base(filepath.Dir(volume)),
			SandboxID: strings.TrimSuffix(filepath.Base(volume), ".ext4"), ID: id, TakenAt: info.ModTime().UTC(),
			volume: volume, clone: clone})
	}
	return out
}

// localManifest is the node's newest backup of the volume at path.
func localManifest(path string) (volbackup.Manifest, bool) {
	raw, err := os.ReadFile(path + volumeBackupManifestSuffix) //nolint:forbidigo // a node-owned file beside the volume.
	if err != nil {
		return volbackup.Manifest{}, false
	}
	var m volbackup.Manifest
	if json.Unmarshal(raw, &m) != nil || m.ID == "" {
		return volbackup.Manifest{}, false
	}
	return m, true
}

// LastBackup is the newest backup this node took or restored of the
// session's volume.
func (v *SandboxVolumes) LastBackup(accountID, sandboxID string) (volbackup.Manifest, bool) {
	path, err := v.Path(accountID, sandboxID)
	if err != nil {
		return volbackup.Manifest{}, false
	}
	return localManifest(path)
}

// TookBackup reports whether backup id is the newest this node cloned the
// session's volume for: the node's copy is that backup, whether or not its
// upload finished (ADR-20261122132055 §4).
func (v *SandboxVolumes) TookBackup(accountID, sandboxID, id string) bool {
	path, err := v.Path(accountID, sandboxID)
	if err != nil || id == "" {
		return false
	}
	raw, err := os.ReadFile(path + volumeBackupTakenSuffix) //nolint:forbidigo // a node-owned file beside the volume.
	return err == nil && string(raw) == id
}

// Exists reports whether this node holds the session's volume.
func (v *SandboxVolumes) Exists(accountID, sandboxID string) bool {
	path, err := v.Path(accountID, sandboxID)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// lastBackup is the node's newest backup of the volume and the ranges of
// clone that changed since. Without one it is nil and the clone's data
// ranges, so a first backup skips the sparse volume's holes; nil ranges
// read it all.
func (v *SandboxVolumes) lastBackup(path, clone string) (*volbackup.Manifest, []storage.Range) {
	m, ok := localManifest(path)
	if !ok {
		data, err := storage.DataRanges(clone)
		if err != nil {
			return nil, nil
		}
		if data == nil {
			data = []storage.Range{}
		}
		return nil, data
	}
	changed, err := storage.ChangedRanges(path+volumeBackupBaseInfix+m.ID, clone)
	if err != nil {
		// No clone to compare with, or a filesystem without extent maps:
		// the manifest still names the chunks the store holds.
		return &m, nil
	}
	if changed == nil {
		changed = []storage.Range{}
	}
	return &m, changed
}

// keepBackupBase makes clone the base the next backup compares with and m
// its manifest, unless the node already keeps a newer backup (a pause's
// upload finishing after a later one), in which case the clone goes.
func (v *SandboxVolumes) keepBackupBase(path, clone string, m volbackup.Manifest) error {
	old, hadOld := localManifest(path)
	if hadOld && old.CreatedAt.After(m.CreatedAt) {
		_ = os.Remove(clone)
		return nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	base := path + volumeBackupBaseInfix + m.ID
	if err := os.Rename(clone, base); err != nil {
		return fmt.Errorf("fcvm: keep sandbox volume backup: %w", err)
	}
	tmp := path + volumeBackupManifestSuffix + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("fcvm: keep sandbox volume backup: %w", err)
	}
	if err := os.Rename(tmp, path+volumeBackupManifestSuffix); err != nil {
		return fmt.Errorf("fcvm: keep sandbox volume backup: %w", err)
	}
	if hadOld && old.ID != m.ID {
		_ = os.Remove(path + volumeBackupBaseInfix + old.ID)
	}
	return nil
}

// RestoreBackup replaces the session's volume on this node with backup id,
// for a session whose node copy is gone or older (ADR-20261122132055 §4).
// The next backup compares with the restored volume.
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
	unlock := v.lockVolume(path)
	defer unlock()
	v.dropBackupBase(path)
	if err := volbackup.Restore(ctx, store, accountID, m, path); err != nil {
		return "", err
	}
	clone := path + volumeBackupPendingInfix + id
	if _, err := storage.CloneFile(clone, path); err == nil {
		if err := v.keepBackupBase(path, clone, m); err != nil {
			_ = os.Remove(clone)
		}
	}
	return path, nil
}

// dropBackupBase forgets the node's backups of the volume.
func (v *SandboxVolumes) dropBackupBase(path string) {
	matches, _ := filepath.Glob(path + ".backup*")
	for _, f := range matches {
		_ = os.Remove(f)
	}
}
