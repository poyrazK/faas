package objectnode

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ReplicationWaitCursor binds a change notification to the sampled source's
// authority and applied prefix. A notification is never an acknowledgement or
// permission to apply data without the normal hash-chain checks.
type ReplicationWaitCursor struct {
	StreamID  string `json:"stream_id"`
	PrimaryID string `json:"primary_id"`
	Epoch     int64  `json:"epoch"`
	Sequence  int64  `json:"sequence"`
	Hash      string `json:"hash"`
	Revision  string `json:"revision"`
}

type ReplicationWaiter interface {
	WaitForChange(context.Context, ReplicationWaitCursor) (ReplicationStatus, error)
}

func NewReplicationWaitCursor(status ReplicationStatus) (ReplicationWaitCursor, error) {
	if !validReplicationStatus(status) || status.Version < 2 || status.Role == "follower" {
		return ReplicationWaitCursor{}, ErrInvalid
	}
	return replicationWaitCursor(status), nil
}

func replicationWaitCursor(status ReplicationStatus) ReplicationWaitCursor {
	c := ReplicationWaitCursor{StreamID: status.StreamID, PrimaryID: status.PrimaryID, Epoch: status.Epoch, Sequence: status.AppliedSequence, Hash: status.AppliedHash}
	// Received checkpoints and reports can change without new applied data. In
	// particular, a worker must not wake itself by confirming its own cursor.
	status = replicationSnapshot(status)
	status.UpdatedAt = time.Time{}
	status.Checkpoint = nil // BaseSequence/BaseHash capture the floor; cleanup progress is not new data.
	status.ReceivedSequence, status.ReceivedHash = status.AppliedSequence, status.AppliedHash
	if status.Durability != nil {
		status.Durability.ConfirmedSequence = 0
		status.Durability.ConfirmedHash, status.Durability.ConfirmedAt = "", time.Time{}
	}
	for i := range status.Followers {
		status.Followers[i].Sequence = 0
		status.Followers[i].Hash, status.Followers[i].UpdatedAt = "", time.Time{}
	}
	c.Revision = nameHash("gregale-replication-wait-v1:" + scrubSeal(status))
	return c
}

func validReplicationWaitCursor(c ReplicationWaitCursor) bool {
	return validReceiptID(c.StreamID) && validReceiptID(c.PrimaryID) && c.Epoch >= 1 && c.Epoch <= api.ObjectNodeReplicationMaxEpoch && c.Sequence >= 0 && c.Sequence <= api.ObjectNodeReplicationMaxSequence && validScrubFindingID(c.Hash) && validScrubFindingID(c.Revision)
}

// WaitForReplicationChange releases all lifecycle/catalog locks before waiting.
// Unrelated report broadcasts are rechecked against the same revision and do not
// reset the fixed timeout. Timeout returns the current unchanged status normally.
func (s *Store) WaitForReplicationChange(ctx context.Context, cursor ReplicationWaitCursor) (ReplicationStatus, error) {
	if !validReplicationWaitCursor(cursor) {
		return ReplicationStatus{}, ErrInvalid
	}
	timer := time.NewTimer(api.ObjectNodeReplicationChangeWaitTimeout)
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return ReplicationStatus{}, err
		}
		status, changed, ready, err := s.replicationChange(cursor)
		if err != nil || ready {
			return status, err
		}
		select {
		case <-ctx.Done():
			return ReplicationStatus{}, ctx.Err()
		case <-timer.C:
			status, _, _, err = s.replicationChange(cursor)
			return status, err
		case <-changed:
		}
	}
}

func (s *Store) replicationChange(cursor ReplicationWaitCursor) (ReplicationStatus, <-chan struct{}, bool, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ReplicationStatus{}, nil, false, ErrClosed
	}
	if s.replication == nil || s.replication.Role == "follower" {
		return ReplicationStatus{}, nil, false, ErrReplicationOff
	}
	if s.replicationDirty || s.authorityDirty {
		return ReplicationStatus{}, nil, false, ErrCorrupt
	}
	state := s.replication
	if cursor.StreamID != state.StreamID || cursor.PrimaryID != state.PrimaryID || cursor.Epoch != state.Epoch {
		return ReplicationStatus{}, nil, false, ErrReplicationFork
	}
	if cursor.Sequence < state.BaseSequence {
		return ReplicationStatus{}, nil, false, ErrReplicaReseed
	}
	if err := s.checkDurablePosition(cursor.Sequence, cursor.Hash); err != nil {
		return ReplicationStatus{}, nil, false, err
	}
	status := replicationSnapshot(state.ReplicationStatus)
	return status, s.replicationChanged, replicationWaitCursor(status) != cursor, nil
}
