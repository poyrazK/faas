package objectnode

import (
	"context"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

func (s *Store) validateReplicationCatalog(ctx context.Context, allowFollower bool) error {
	state, err := s.readReplicationState()
	if err != nil {
		return err
	}
	if state.Role != "primary" && (!allowFollower || state.Role != "follower") || state.ReceivedSequence != state.AppliedSequence {
		return ErrCorrupt
	}
	s.replication = &state
	for _, dir := range []string{"log", "blobs", "transfer"} {
		info, err := os.Lstat(s.replicationPath(dir))
		if err != nil || !info.IsDir() {
			return ErrCorrupt
		}
	}
	var records int64
	if err = visitDirectory(s.replicationPath("log"), func(_ os.DirEntry) error { records++; return ctx.Err() }); err != nil {
		return err
	}
	if records != state.AppliedSequence-state.BaseSequence || state.Checkpoint != nil && state.Checkpoint.Pruning {
		return ErrCorrupt
	}
	previous, epoch := state.BaseHash, int64(1)
	for sequence := state.BaseSequence + 1; sequence <= state.AppliedSequence; sequence++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		e, err := s.readJournal(sequence)
		if err != nil || e.Previous != previous || journalEpoch(e) < epoch {
			return ErrCorrupt
		}
		previous, epoch = e.Hash, journalEpoch(e)
		if e.Blob != nil {
			info, err := os.Lstat(s.replicationPath("blobs/" + e.Blob.SHA256 + ".data"))
			if err != nil || !info.Mode().IsRegular() || info.Size() != e.Blob.Size {
				return ErrCorrupt
			}
		}
	}
	if previous != state.AppliedHash {
		return ErrCorrupt
	}
	if err = s.validatePromotionPosition(); err != nil {
		return err
	}
	if err = s.validateDurabilityPosition(); err != nil {
		return err
	}
	return s.validateFollowerPositions()
}

// BootstrapReplica binds a verified snapshot to its exact primary stream and
// position. Format-4 restores are always fenced followers, including via restore.
func BootstrapReplica(ctx context.Context, backup, destination string) (BackupInfo, error) {
	info, err := readBackupInfo(backup)
	if err != nil {
		return BackupInfo{}, err
	}
	if info.FormatVersion < 4 {
		return BackupInfo{}, ErrReplicationOff
	}
	return RestoreBackup(ctx, backup, destination)
}

func prepareReplicaBootstrap(stage string) (*Store, error) {
	s, err := lockStore(stage)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Store, error) { _ = s.Close(); return nil, err }
	state, err := s.readReplicationState()
	if err != nil {
		return fail(err)
	}
	if state.Role != "primary" || state.ReceivedSequence != state.AppliedSequence {
		return fail(ErrCorrupt)
	}
	// The baseline already contains all applied catalog changes. Old journal
	// payloads are unnecessary on this non-serving follower and cannot establish
	// authority for promotion or downstream replication.
	for _, dir := range []string{"log", "blobs", "transfer"} {
		if err = os.RemoveAll(s.replicationPath(dir)); err != nil {
			return fail(err)
		}
		if err = os.Mkdir(s.replicationPath(dir), 0o700); err != nil {
			return fail(err)
		}
	}
	state.Role, state.BaseSequence, state.BaseHash = "follower", state.AppliedSequence, state.AppliedHash
	state.Version, state.Epoch, state.PrimaryID, state.NodeID = 2, replicationEpoch(state.ReplicationStatus), state.NodeID, uuid.NewString()
	state.Checkpoint, state.Followers = nil, nil
	state.Durability = nil // The snapshot never clones the primary's acknowledgement authority.
	if err = s.saveReplicationState(state); err != nil {
		return fail(err)
	}
	if err = syncDirectory(filepath.Join(stage, "replication")); err != nil {
		return fail(err)
	}
	return s, nil
}
