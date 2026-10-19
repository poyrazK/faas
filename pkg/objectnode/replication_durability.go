package objectnode

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	DurabilityLocal   = "local"
	DurabilityReplica = "replica"
)

// ReplicaDurability is persisted with the primary's authority and cursors.
// ConfirmedSequence -1 means no follower confirmation has been received.
// A promoted primary preserves replica mode with an unbound ReplicaID until a
// new follower is enrolled. Its sealed handoff certifies only the old prefix.
type ReplicaDurability struct {
	Version           int       `json:"version"`
	Mode              string    `json:"mode"`
	PolicyID          string    `json:"policy_id"`
	ReplicaID         string    `json:"replica_id"`
	ConfirmedSequence int64     `json:"confirmed_sequence"`
	ConfirmedHash     string    `json:"confirmed_hash"`
	ConfirmedAt       time.Time `json:"confirmed_at"`
}

// ReplicaConfirmation is an authenticated report of an already fsynced applied
// checkpoint, not a claim about received records or partially downloaded bytes.
type ReplicaConfirmation struct {
	Version   int    `json:"version"`
	StreamID  string `json:"stream_id"`
	PrimaryID string `json:"primary_id"`
	Epoch     int64  `json:"epoch"`
	PolicyID  string `json:"policy_id"`
	ReplicaID string `json:"replica_id"`
	Sequence  int64  `json:"sequence"`
	Hash      string `json:"hash"`
}

type ReplicaConfirmer interface {
	Confirm(context.Context, ReplicaConfirmation) (ReplicaDurability, error)
}

func validReplicaConfirmation(c ReplicaConfirmation) bool {
	return c.Version == 1 && validReceiptID(c.StreamID) && validReceiptID(c.PrimaryID) && validReceiptID(c.PolicyID) && validReceiptID(c.ReplicaID) && c.PrimaryID != c.ReplicaID && c.Epoch >= 1 && c.Epoch <= api.ObjectNodeReplicationMaxEpoch && c.Sequence >= 0 && c.Sequence <= api.ObjectNodeReplicationMaxSequence && validScrubFindingID(c.Hash)
}

func validReplicaDurability(s ReplicationStatus) bool {
	p := s.Durability
	if s.Version < 3 {
		return p == nil
	}
	if p == nil {
		return s.Version >= 4
	}
	if s.Role == "follower" || p.Version != 1 || p.Mode != DurabilityLocal && p.Mode != DurabilityReplica || !validReceiptID(p.PolicyID) || p.ReplicaID != "" && (!validReceiptID(p.ReplicaID) || p.ReplicaID == s.NodeID) || p.ConfirmedSequence < -1 || p.ConfirmedSequence > s.AppliedSequence {
		return false
	}
	if p.ConfirmedSequence == -1 {
		if p.ConfirmedHash != "" || !p.ConfirmedAt.IsZero() || p.Mode != DurabilityReplica || p.ReplicaID == "" {
			return false
		}
	} else if p.ConfirmedSequence < s.BaseSequence || !validScrubFindingID(p.ConfirmedHash) || p.ConfirmedAt.IsZero() {
		return false
	}
	if p.ConfirmedSequence >= 0 && (p.ConfirmedSequence == s.BaseSequence && p.ConfirmedHash != s.BaseHash || p.ConfirmedSequence == s.AppliedSequence && p.ConfirmedHash != s.AppliedHash) {
		return false
	}
	if p.Mode == DurabilityReplica && p.ReplicaID == "" {
		return s.Promotion != nil && p.ConfirmedSequence == s.Promotion.Sequence && p.ConfirmedHash == s.Promotion.Hash
	}
	return true
}

func (s *Store) validateDurabilityPosition() error {
	p := s.replication.Durability
	if p == nil || p.ConfirmedSequence < 0 {
		return nil
	}
	return s.checkDurablePosition(p.ConfirmedSequence, p.ConfirmedHash)
}

func (s *Store) checkDurablePosition(sequence int64, hash string) error {
	if sequence == s.replication.BaseSequence {
		if hash != s.replication.BaseHash {
			return ErrReplicationFork
		}
		return nil
	}
	if sequence < s.replication.BaseSequence || sequence > s.replication.AppliedSequence {
		return ErrReplicationFork
	}
	e, err := s.readJournal(sequence)
	if err != nil {
		return err
	}
	if e.Hash != hash {
		return ErrReplicationFork
	}
	return nil
}

func (s *Store) signalReplicationChange() {
	if s.replicationChanged != nil {
		close(s.replicationChanged)
	}
	s.replicationChanged = make(chan struct{})
}

// ConfigureDurability is offline and cannot weaken an unconfirmed replica
// promise. Replacing a follower keeps replica mode and resets confirmation under
// a fresh policy identity; the replacement must confirm the full captured tail.
func ConfigureDurability(ctx context.Context, directory, mode, replica string) (ReplicationStatus, error) {
	if mode != DurabilityLocal && mode != DurabilityReplica || mode == DurabilityReplica && !validReceiptID(replica) || mode == DurabilityLocal && replica != "" {
		return ReplicationStatus{}, ErrInvalid
	}
	directory, err := backupDirectory(directory)
	if err != nil {
		return ReplicationStatus{}, err
	}
	var s *Store
	if mode == DurabilityReplica {
		s, err = openRetentionPrimary(ctx, directory)
	} else {
		// A downgrade must first settle every multi-record receipt decision.
		// Replacement in replica mode can still unblock a full journal safely.
		s, err = openBackupSource(ctx, directory)
	}
	if err != nil {
		return ReplicationStatus{}, err
	}
	defer func() { _ = s.Close() }()
	if s.replication == nil {
		return ReplicationStatus{}, ErrReplicationOff
	}
	if s.replication.Role != "primary" {
		return ReplicationStatus{}, ErrFenced
	}
	if replica == s.replication.NodeID {
		return ReplicationStatus{}, ErrInvalid
	}
	old := s.replication.Durability
	if old == nil && mode == DurabilityLocal || old != nil && old.Mode == mode && (mode == DurabilityLocal || old.ReplicaID == replica) {
		return replicationSnapshot(s.replication.ReplicationStatus), nil
	}
	if old != nil && old.Mode == DurabilityReplica && mode == DurabilityLocal && old.ConfirmedSequence < s.replication.AppliedSequence {
		return ReplicationStatus{}, ErrReplicaPending
	}
	policy := &ReplicaDurability{Version: 1, Mode: mode, PolicyID: uuid.NewString(), ReplicaID: replica, ConfirmedSequence: -1}
	if mode == DurabilityLocal || old != nil && old.Mode == DurabilityReplica && old.ReplicaID == "" {
		policy.ConfirmedSequence, policy.ConfirmedHash, policy.ConfirmedAt = old.ConfirmedSequence, old.ConfirmedHash, old.ConfirmedAt
	}
	state := *s.replication
	state.Version, state.Durability = max(state.Version, 3), policy
	if err = s.saveReplicationState(state); err != nil {
		return ReplicationStatus{}, err
	}
	return replicationSnapshot(s.replication.ReplicationStatus), nil
}

func promotedDurability(handoff PrimaryHandoff) *ReplicaDurability {
	return &ReplicaDurability{Version: 1, Mode: DurabilityReplica, PolicyID: uuid.NewString(), ConfirmedSequence: handoff.Sequence, ConfirmedHash: handoff.Hash, ConfirmedAt: time.Now().UTC()}
}

// ConfirmReplica validates the exact authority, policy and designated follower,
// checks the reported position against the primary's applied hash chain, and
// persists it before acknowledging or waking waiters. A fenced source can still
// accept confirmations of its sealed stream during planned handoff.
func (s *Store) ConfirmReplica(ctx context.Context, c ReplicaConfirmation) (ReplicaDurability, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ReplicaDurability{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return ReplicaDurability{}, err
	}
	if s.replication == nil {
		return ReplicaDurability{}, ErrReplicationOff
	}
	if s.replicationDirty || s.authorityDirty {
		return ReplicaDurability{}, ErrCorrupt
	}
	// Resolve a checkpoint whose durable publication acknowledgement was lost.
	state, err := s.readReplicationState()
	if err != nil {
		return ReplicaDurability{}, err
	}
	p := state.Durability
	if state.Role == "follower" || p == nil || p.Mode != DurabilityReplica || !validReplicaConfirmation(c) || c.StreamID != state.StreamID || c.PrimaryID != state.NodeID || c.Epoch != state.Epoch || c.PolicyID != p.PolicyID || c.ReplicaID != p.ReplicaID || c.Sequence < p.ConfirmedSequence || c.Sequence > state.AppliedSequence {
		return ReplicaDurability{}, ErrReplicationFork
	}
	if err = s.checkDurablePosition(c.Sequence, c.Hash); err != nil {
		return ReplicaDurability{}, err
	}
	if err = syncDirectory(s.replicationPath("")); err != nil {
		return ReplicaDurability{}, err
	}
	policy := *p
	if c.Sequence == p.ConfirmedSequence {
		s.replication = &state
		s.signalReplicationChange()
		return policy, nil
	}
	policy.ConfirmedSequence, policy.ConfirmedHash, policy.ConfirmedAt = c.Sequence, c.Hash, time.Now().UTC()
	state.Durability = &policy
	if err = s.saveReplicationState(state); err != nil {
		return ReplicaDurability{}, err
	}
	return policy, nil
}

// DurabilityStatus is a memory-only operational sample; nil replication/policy
// preserves the existing local service mode.
func (s *Store) DurabilityStatus() (ReplicaDurability, int64, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ReplicaDurability{}, 0, ErrClosed
	}
	if s.replication == nil || s.replication.Durability == nil {
		return ReplicaDurability{Version: 1, Mode: DurabilityLocal, ConfirmedSequence: -1}, 0, nil
	}
	if s.replicationDirty || s.authorityDirty {
		return ReplicaDurability{}, 0, ErrCorrupt
	}
	return *s.replication.Durability, s.replication.AppliedSequence, nil
}

// CanMutate prevents a promoted primary from accepting new S3 mutations before
// another follower is bound. Engine recovery can still finish local decisions.
func (s *Store) CanMutate() error {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.servingError(); err != nil {
		return err
	}
	if s.replication != nil && s.replication.Durability != nil && s.replication.Durability.Mode == DurabilityReplica && s.replication.Durability.ReplicaID == "" {
		return ErrReplicaPending
	}
	return nil
}

// WaitForReplica gates a service response after all local publication locks are
// released. It snapshots the current final record, which conservatively includes
// concurrent earlier publications. It never holds lifecycle/catalog locks while
// waiting, so export, confirmations, fencing and shutdown can progress.
func (s *Store) WaitForReplica(ctx context.Context) error {
	s.life.RLock()
	s.mu.RLock()
	if err := s.servingError(); err != nil {
		s.mu.RUnlock()
		s.life.RUnlock()
		return err
	}
	if s.replication == nil || s.replication.Durability == nil || s.replication.Durability.Mode == DurabilityLocal {
		s.mu.RUnlock()
		s.life.RUnlock()
		return nil
	}
	initial := replicationSnapshot(s.replication.ReplicationStatus)
	s.mu.RUnlock()
	s.life.RUnlock()
	for {
		ready, changed, err := s.replicaReady(initial)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ErrReplicaPending
		case <-changed:
		}
	}
}

func (s *Store) replicaReady(target ReplicationStatus) (bool, <-chan struct{}, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.servingError(); err != nil {
		return false, nil, err
	}
	if s.replicationDirty || s.authorityDirty {
		return false, nil, ErrCorrupt
	}
	state := s.replication
	if state == nil || state.Durability == nil || state.StreamID != target.StreamID || state.NodeID != target.NodeID || state.Epoch != target.Epoch || state.Durability.PolicyID != target.Durability.PolicyID {
		return false, nil, ErrReplicationFork
	}
	return state.Durability.ConfirmedSequence >= target.AppliedSequence, s.replicationChanged, nil
}
