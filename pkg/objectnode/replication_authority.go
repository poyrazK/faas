package objectnode

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// PrimaryHandoff is a durable, irrevocable grant from a reachable primary to
// exactly one follower. Its checksum detects damage, not an untrusted operator.
// The authenticated source must confirm this record before the first promotion.
type PrimaryHandoff struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	StreamID  string    `json:"stream_id"`
	PrimaryID string    `json:"primary_id"`
	TargetID  string    `json:"target_id"`
	Epoch     int64     `json:"epoch"`
	Sequence  int64     `json:"sequence"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	Seal      string    `json:"seal"`
}

type FenceRequest struct {
	StreamID  string
	PrimaryID string
	TargetID  string
	Epoch     int64
}

type FencingSource interface {
	ReplicationSource
	Fence(context.Context, FenceRequest) (PrimaryHandoff, error)
}

func sealHandoff(h PrimaryHandoff) PrimaryHandoff {
	h.Seal = ""
	h.Seal = scrubSeal(h)
	return h
}

func validHandoff(h PrimaryHandoff) bool {
	return h.Version == 1 && validReceiptID(h.ID) && validReceiptID(h.StreamID) && validReceiptID(h.PrimaryID) && validReceiptID(h.TargetID) && h.PrimaryID != h.TargetID && h.Epoch >= 1 && h.Epoch < api.ObjectNodeReplicationMaxEpoch && h.Sequence >= 0 && h.Sequence <= api.ObjectNodeReplicationMaxSequence && validScrubFindingID(h.Hash) && (h.Sequence != 0 || h.Hash == replicationGenesis(h.StreamID)) && !h.CreatedAt.IsZero() && h.Seal == sealHandoff(h).Seal
}

func validReplicationAuthority(s ReplicationStatus) bool {
	if s.Version == 1 {
		return s.Role != "fenced" && s.NodeID == "" && s.PrimaryID == "" && s.Epoch == 0 && s.Promotion == nil && s.Handoff == nil && (s.Role != "primary" || s.BaseSequence == 0)
	}
	if !validReceiptID(s.NodeID) || s.Epoch < 1 || s.Epoch > api.ObjectNodeReplicationMaxEpoch || !validReceiptID(s.PrimaryID) && (s.Role != "follower" || s.PrimaryID != "" || s.Epoch != 1) {
		return false
	}
	if s.Role != "follower" && s.PrimaryID != s.NodeID || s.Role == "follower" && s.PrimaryID == s.NodeID {
		return false
	}
	if s.Promotion != nil {
		h := *s.Promotion
		if !validHandoff(h) || h.StreamID != s.StreamID || h.TargetID != s.PrimaryID || h.Epoch+1 != s.Epoch || h.Sequence > s.AppliedSequence || h.Sequence == s.AppliedSequence && h.Hash != s.AppliedHash || s.Role != "follower" && h.Sequence < s.BaseSequence && s.Checkpoint == nil {
			return false
		}
	} else if s.Epoch != 1 || s.Role != "follower" && s.BaseSequence != 0 && s.Checkpoint == nil {
		return false
	}
	if s.Role == "fenced" {
		h := s.Handoff
		return h != nil && validHandoff(*h) && h.StreamID == s.StreamID && h.PrimaryID == s.NodeID && h.Epoch == s.Epoch && h.Sequence == s.AppliedSequence && h.Hash == s.AppliedHash && s.ReceivedSequence == s.AppliedSequence
	}
	return s.Handoff == nil
}

func replicationEpoch(s ReplicationStatus) int64 {
	if s.Version == 1 {
		return 1
	}
	return s.Epoch
}

func journalEpoch(e JournalEntry) int64 {
	if e.Version == 1 {
		return 1
	}
	return e.Epoch
}

func journalAuthority(e JournalEntry, s ReplicationStatus) bool {
	epoch := journalEpoch(e)
	if s.Promotion == nil {
		return epoch == 1
	}
	if e.Sequence <= s.Promotion.Sequence {
		return epoch >= 1 && epoch < s.Epoch
	}
	return epoch == s.Epoch
}

func (s *Store) validatePromotionPosition() error {
	h := s.replication.Promotion
	if h == nil || h.Sequence < s.replication.BaseSequence {
		return nil // A verified snapshot can start beyond the authority boundary.
	}
	if h.Sequence == s.replication.BaseSequence {
		if h.Hash != s.replication.BaseHash {
			return ErrCorrupt
		}
		return nil
	}
	e, err := s.readJournal(h.Sequence)
	if err != nil || e.Hash != h.Hash || journalEpoch(e) != h.Epoch {
		return ErrCorrupt
	}
	return nil
}

func replicationSnapshot(s ReplicationStatus) ReplicationStatus {
	s.Followers = append([]RegisteredFollower(nil), s.Followers...)
	if s.Checkpoint != nil {
		value := *s.Checkpoint
		s.Checkpoint = &value
	}
	if s.Handoff != nil {
		value := *s.Handoff
		s.Handoff = &value
	}
	if s.Promotion != nil {
		value := *s.Promotion
		s.Promotion = &value
	}
	if s.Durability != nil {
		value := *s.Durability
		s.Durability = &value
	}
	return s
}

// A v1 follower's original primary identity is established only after checking
// the authenticated source against its saved hash-chain position.
func (s *Store) upgradeReplicationAuthority() error {
	if s.replication.Version >= 2 {
		return nil
	}
	state := *s.replication
	state.Version, state.NodeID, state.Epoch = 2, uuid.NewString(), 1
	if state.Role == "primary" {
		state.PrimaryID = state.NodeID
	}
	return s.saveReplicationState(state)
}

func (s *Store) servingError() error {
	if s.closed {
		return ErrClosed
	}
	if s.authorityDirty || s.replication != nil && s.replication.Role == "fenced" {
		return ErrFenced
	}
	if s.replication != nil && s.replication.Role == "follower" {
		return ErrReplica
	}
	return nil
}

// CanServe gates ordinary S3 traffic and background maintenance. The sealed
// replication export remains available after fencing, including after restart.
func (s *Store) CanServe() error {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.servingError()
}

// FencePrimary drains every operation holding the lifecycle lock, including
// uploads streaming outside the catalog lock. No queued mutation can publish
// after the final position is sealed. There is deliberately no unfence API.
func (s *Store) FencePrimary(ctx context.Context, request FenceRequest) (PrimaryHandoff, error) {
	s.life.Lock()
	defer s.life.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return PrimaryHandoff{}, ErrClosed
	}
	if s.replication == nil {
		return PrimaryHandoff{}, ErrReplicationOff
	}
	if !validReceiptID(request.TargetID) || request.TargetID == request.PrimaryID {
		return PrimaryHandoff{}, ErrInvalid
	}
	// Re-read an uncertain publication before deciding whether to grant again.
	state, err := s.readReplicationState()
	if err != nil {
		return PrimaryHandoff{}, err
	}
	if state.Version < 2 || state.Role == "follower" || request.StreamID != state.StreamID || request.PrimaryID != state.NodeID || request.Epoch != state.Epoch {
		return PrimaryHandoff{}, ErrReplicationFork
	}
	if state.Durability != nil && state.Durability.Mode == DurabilityReplica && request.TargetID != state.Durability.ReplicaID {
		return PrimaryHandoff{}, ErrReplicationFork
	}
	if state.Role == "fenced" {
		if state.Handoff.TargetID != request.TargetID {
			return PrimaryHandoff{}, ErrReplicationFork
		}
		if err = syncDirectory(s.replicationPath("")); err != nil {
			return PrimaryHandoff{}, err
		}
		s.replication, s.authorityDirty = &state, false
		return *state.Handoff, nil
	}
	if err = ctx.Err(); err != nil {
		return PrimaryHandoff{}, err
	}
	if state.Epoch >= api.ObjectNodeReplicationMaxEpoch {
		return PrimaryHandoff{}, ErrCapacity
	}
	s.replication, s.authorityDirty = &state, false
	if err = s.finishPending(true); err != nil {
		return PrimaryHandoff{}, err
	}
	if err = s.recoverReceipts(); err != nil {
		return PrimaryHandoff{}, err
	}
	if err = s.recoverMultipart(); err != nil {
		return PrimaryHandoff{}, err
	}
	if err = ctx.Err(); err != nil {
		return PrimaryHandoff{}, err
	}
	state = *s.replication
	handoff := sealHandoff(PrimaryHandoff{Version: 1, ID: uuid.NewString(), StreamID: state.StreamID, PrimaryID: state.NodeID, TargetID: request.TargetID, Epoch: state.Epoch, Sequence: state.AppliedSequence, Hash: state.AppliedHash, CreatedAt: time.Now().UTC()})
	state.Role, state.Handoff = "fenced", &handoff
	s.authorityDirty = true
	if err = s.saveReplicationState(state); err != nil {
		return PrimaryHandoff{}, err
	}
	s.authorityDirty = false
	s.invalidateInventory()
	return handoff, nil
}
