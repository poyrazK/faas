package objectnode

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *Store) checkReplicaSource(ctx context.Context, source ReplicationSource, head ReplicationStatus) error {
	local := s.replication
	if local.Role != "follower" || !validReplicationStatus(head) || head.Version < 2 || head.Role != "primary" && head.Role != "fenced" || head.StreamID != local.StreamID || head.Epoch != local.Epoch || local.PrimaryID != "" && local.PrimaryID != head.PrimaryID || head.AppliedSequence < local.ReceivedSequence {
		return ErrReplicationFork
	}
	if head.Epoch > 1 && (local.Promotion == nil || head.Promotion == nil || local.Promotion.Seal != head.Promotion.Seal) {
		return ErrReplicationFork
	}
	if err := checkSourcePosition(ctx, source, head, local.AppliedSequence, local.AppliedHash); err != nil {
		return err
	}
	if local.PrimaryID == "" {
		state := *local
		state.PrimaryID = head.PrimaryID
		return s.saveReplicationState(state)
	}
	return nil
}

// FenceReplicaPrimary preflights a stopped catch-up worker's current copy before
// asking its authenticated, reachable primary for an irrevocable handoff. This
// does not promote the follower; catch-up to the returned position comes next.
func FenceReplicaPrimary(ctx context.Context, directory string, source FencingSource) (PrimaryHandoff, error) {
	if source == nil {
		return PrimaryHandoff{}, ErrInvalid
	}
	follower, err := OpenFollower(directory)
	if err != nil {
		return PrimaryHandoff{}, err
	}
	defer func() { _ = follower.Close() }()
	s := follower.store
	head, err := source.Head(ctx)
	if err != nil {
		return PrimaryHandoff{}, err
	}
	if err = s.checkReplicaSource(ctx, source, head); err != nil {
		return PrimaryHandoff{}, err
	}
	if head.Role == "fenced" && head.Handoff.TargetID != s.replication.NodeID {
		return PrimaryHandoff{}, ErrReplicationFork
	}
	if err = s.validatePromotionData(ctx); err != nil {
		return PrimaryHandoff{}, err
	}
	r := FenceRequest{StreamID: s.replication.StreamID, PrimaryID: s.replication.PrimaryID, TargetID: s.replication.NodeID, Epoch: s.replication.Epoch}
	h, err := source.Fence(ctx, r)
	if err == nil && (!validHandoff(h) || h.StreamID != r.StreamID || h.PrimaryID != r.PrimaryID || h.TargetID != r.TargetID || h.Epoch != r.Epoch || h.Sequence < head.AppliedSequence || h.Sequence == head.AppliedSequence && h.Hash != head.AppliedHash) {
		err = ErrReplicationFork
	}
	return h, err
}

// PromoteReplica requires a live authenticated confirmation of the old writer's
// durable fence and exact equality with its sealed checkpoint. There is no
// force/unreachable-primary mode. Publication changes only authority metadata;
// the existing hash chain, object identities and receipt history remain intact.
func PromoteReplica(ctx context.Context, directory string, source ReplicationSource) (ReplicationStatus, error) {
	if source == nil {
		return ReplicationStatus{}, ErrInvalid
	}
	s, err := openReplica(directory)
	if err != nil {
		return ReplicationStatus{}, err
	}
	defer func() { _ = s.Close() }()
	return s.promoteReplica(ctx, source)
}

func (s *Store) promoteReplica(ctx context.Context, source ReplicationSource) (ReplicationStatus, error) {
	if err := ctx.Err(); err != nil {
		return ReplicationStatus{}, err
	}
	if s.replication.Role == "primary" && s.replication.Promotion != nil {
		// A prior promotion may have succeeded without delivering its response.
		// The local durable grant suffices to acknowledge that same decision.
		if err := syncDirectory(s.replicationPath("")); err != nil {
			return ReplicationStatus{}, err
		}
		return replicationSnapshot(s.replication.ReplicationStatus), nil
	}
	head, err := source.Head(ctx)
	if err != nil {
		return ReplicationStatus{}, err
	}
	if err = s.checkReplicaSource(ctx, source, head); err != nil {
		return ReplicationStatus{}, err
	}
	if head.Role != "fenced" || head.Handoff.TargetID != s.replication.NodeID || s.replication.AppliedSequence != head.Handoff.Sequence || s.replication.AppliedHash != head.Handoff.Hash || s.replication.ReceivedSequence != s.replication.AppliedSequence {
		return ReplicationStatus{}, ErrReplicationFork
	}
	// Detect an entry published before a lost Received checkpoint as well.
	if err = s.adoptReplicationRecord(); err != nil {
		return ReplicationStatus{}, err
	}
	if s.replication.ReceivedSequence != head.Handoff.Sequence || s.replication.ReceivedHash != head.Handoff.Hash {
		return ReplicationStatus{}, ErrReplicationFork
	}
	if err = s.validatePromotionData(ctx); err != nil {
		return ReplicationStatus{}, err
	}
	if err = ctx.Err(); err != nil {
		return ReplicationStatus{}, err
	}
	state := *s.replication
	handoff := *head.Handoff
	state.Role, state.PrimaryID, state.Epoch, state.Promotion = "primary", state.NodeID, head.Epoch+1, &handoff
	if head.Durability != nil && head.Durability.Mode == DurabilityReplica {
		if head.Durability.ReplicaID != state.NodeID {
			return ReplicationStatus{}, ErrReplicationFork
		}
		state.Version, state.Durability = max(state.Version, 3), promotedDurability(handoff)
	}
	if err = s.saveReplicationState(state); err != nil {
		return ReplicationStatus{}, err
	}
	return replicationSnapshot(s.replication.ReplicationStatus), nil
}

// Bounded-memory verification covers terminal receipt proofs, every baseline/
// live object and part, and retained replay payloads. Only unpublished temporary
// files are reclaimed; no catalog decision or expiry is executed on the follower.
func (s *Store) validatePromotionData(ctx context.Context) error {
	// Vet types and paths before any catalog reader can follow a nested path.
	count := 0
	if err := walkBackupTree(ctx, s.root, "", 0, func(path string, info os.FileInfo) error {
		count++
		if count > api.ObjectNodeBackupMaxEntries+3 { // Process lock and both local derived indexes.
			return ErrCapacity
		}
		name := filepath.Base(path)
		if info.Mode().IsRegular() && strings.HasPrefix(name, ".tmp-") && validReceiptID(strings.TrimPrefix(name, ".tmp-")) && validBackupPath(filepath.ToSlash(filepath.Dir(path)), true) {
			if err := os.Remove(filepath.Join(s.root, path)); err != nil {
				return err
			}
			return syncDirectory(filepath.Join(s.root, filepath.Dir(path)))
		}
		if (path == recoveryIndexName || path == catalogIndexName) && info.Mode().IsRegular() {
			return nil // Local scheduling projection is never promotion authority.
		}
		if path != ".lock" && !validBackupPath(path, info.IsDir()) {
			return ErrCorrupt
		}
		return nil
	}); err != nil {
		return err
	}
	if err := s.validateCatalog(ctx, true); err != nil {
		return err
	}
	buffer := make([]byte, api.ObjectNodeBackupBufferBytes)
	count = 0
	return walkBackupTree(ctx, s.root, "", 0, func(path string, info os.FileInfo) error {
		count++
		if count > api.ObjectNodeBackupMaxEntries+3 {
			return ErrCapacity
		}
		if path == ".lock" || (path == recoveryIndexName || path == catalogIndexName) && info.Mode().IsRegular() {
			return nil
		}
		if !validBackupPath(path, info.IsDir()) {
			return ErrCorrupt
		}
		if info.IsDir() {
			return nil
		}
		digest, err := copyBackupFile(ctx, filepath.Join(s.root, path), "", info.Size(), buffer)
		if err != nil {
			return err
		}
		return s.verifyBackupDigest(path, info.Size(), digest)
	})
}
