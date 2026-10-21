package objectnode

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// RegisteredFollower pins history at its fsynced applied position. Retirement
// is explicit; inactivity never silently revokes a retention promise.
type RegisteredFollower struct {
	ReplicaID      string    `json:"replica_id"`
	RegistrationID string    `json:"registration_id"`
	Sequence       int64     `json:"sequence"`
	Hash           string    `json:"hash"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type FollowerReport struct {
	Version        int    `json:"version"`
	StreamID       string `json:"stream_id"`
	PrimaryID      string `json:"primary_id"`
	Epoch          int64  `json:"epoch"`
	RegistrationID string `json:"registration_id"`
	ReplicaID      string `json:"replica_id"`
	Sequence       int64  `json:"sequence"`
	Hash           string `json:"hash"`
}

type FollowerReporter interface {
	ReportFollower(context.Context, FollowerReport) (RegisteredFollower, error)
}

func validFollowerReport(r FollowerReport) bool {
	return r.Version == 1 && validReceiptID(r.StreamID) && validReceiptID(r.PrimaryID) && validReceiptID(r.ReplicaID) && r.ReplicaID != r.PrimaryID && validReceiptID(r.RegistrationID) && r.Epoch >= 1 && r.Epoch <= api.ObjectNodeReplicationMaxEpoch && r.Sequence >= 0 && r.Sequence <= api.ObjectNodeReplicationMaxSequence && validScrubFindingID(r.Hash)
}

// RegisterFollower uses an operator-supplied position from replica-status. It
// verifies the exact source hash; the operator must identify the intended disk.
// Repeating the same enrollment preserves its generation after a lost response.
func RegisterFollower(ctx context.Context, directory, replica string, sequence int64, hash string) (ReplicationStatus, error) {
	if !validReceiptID(replica) || sequence < 0 || sequence > api.ObjectNodeReplicationMaxSequence || !validScrubFindingID(hash) {
		return ReplicationStatus{}, ErrInvalid
	}
	s, err := openRetentionPrimary(ctx, directory)
	if err != nil {
		return ReplicationStatus{}, err
	}
	defer func() { _ = s.Close() }()
	if s.replication == nil {
		return ReplicationStatus{}, ErrReplicationOff
	}
	if replica == s.replication.NodeID {
		return ReplicationStatus{}, ErrInvalid
	}
	if sequence < s.replication.BaseSequence {
		return ReplicationStatus{}, ErrReplicaReseed
	}
	if err = s.checkDurablePosition(sequence, hash); err != nil {
		return ReplicationStatus{}, err
	}
	state := *s.replication
	for _, f := range state.Followers {
		if f.ReplicaID == replica {
			if f.Sequence != sequence || f.Hash != hash {
				return ReplicationStatus{}, ErrReplicationFork
			}
			return replicationSnapshot(state.ReplicationStatus), nil
		}
	}
	if len(state.Followers) >= api.ObjectNodeReplicationMaxFollowers {
		return ReplicationStatus{}, ErrCapacity
	}
	state.Version = 4
	state.Followers = append(append([]RegisteredFollower(nil), state.Followers...), RegisteredFollower{ReplicaID: replica, RegistrationID: uuid.NewString(), Sequence: sequence, Hash: hash, UpdatedAt: time.Now().UTC()})
	sort.Slice(state.Followers, func(i, j int) bool { return state.Followers[i].ReplicaID < state.Followers[j].ReplicaID })
	if err = s.saveReplicationState(state); err != nil {
		return ReplicationStatus{}, err
	}
	return replicationSnapshot(s.replication.ReplicationStatus), nil
}

// RetireFollower releases only its retention pin. A designated durability
// replica remains required by its separate acknowledgement policy.
func RetireFollower(ctx context.Context, directory, replica string) (ReplicationStatus, error) {
	if !validReceiptID(replica) {
		return ReplicationStatus{}, ErrInvalid
	}
	s, err := openRetentionPrimary(ctx, directory)
	if err != nil {
		return ReplicationStatus{}, err
	}
	defer func() { _ = s.Close() }()
	if s.replication == nil {
		return ReplicationStatus{}, ErrReplicationOff
	}
	state := *s.replication
	state.Followers = nil
	for _, f := range s.replication.Followers {
		if f.ReplicaID != replica {
			state.Followers = append(state.Followers, f)
		}
	}
	if err = s.saveReplicationState(state); err != nil {
		return ReplicationStatus{}, err
	}
	return replicationSnapshot(s.replication.ReplicationStatus), nil
}

func openRetentionPrimary(ctx context.Context, directory string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := openReplica(directory)
	if err != nil {
		return nil, err
	}
	if s.replication.Role != "primary" {
		_ = s.Close()
		return nil, ErrFenced
	}
	if err = s.finishReplicationPending(); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// ReportFollower persists an authenticated applied cursor before acknowledging.
// A generation prevents an old worker from reporting across retire/re-enroll.
func (s *Store) ReportFollower(ctx context.Context, r FollowerReport) (RegisteredFollower, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return RegisteredFollower{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return RegisteredFollower{}, err
	}
	if s.replication == nil {
		return RegisteredFollower{}, ErrReplicationOff
	}
	if s.replicationDirty || s.authorityDirty {
		return RegisteredFollower{}, ErrCorrupt
	}
	state, err := s.readReplicationState()
	if err != nil {
		return RegisteredFollower{}, err
	}
	if !validFollowerReport(r) || state.Role == "follower" || r.StreamID != state.StreamID || r.PrimaryID != state.NodeID || r.Epoch != state.Epoch {
		return RegisteredFollower{}, ErrReplicationFork
	}
	if r.Sequence < state.BaseSequence {
		return RegisteredFollower{}, ErrReplicaReseed
	}
	for i, f := range state.Followers {
		if r.ReplicaID != f.ReplicaID || r.RegistrationID != f.RegistrationID {
			continue
		}
		if r.Sequence < f.Sequence {
			return RegisteredFollower{}, ErrReplicationFork
		}
		if err = s.checkDurablePosition(r.Sequence, r.Hash); err != nil {
			return RegisteredFollower{}, err
		}
		if err = syncDirectory(s.replicationPath("")); err != nil {
			return RegisteredFollower{}, err
		}
		if r.Sequence == f.Sequence {
			s.replication = &state
			return f, nil
		}
		f.Sequence, f.Hash, f.UpdatedAt = r.Sequence, r.Hash, time.Now().UTC()
		state.Followers[i] = f
		if err = s.saveReplicationState(state); err != nil {
			return RegisteredFollower{}, err
		}
		return f, nil
	}
	return RegisteredFollower{}, ErrReplicationFork
}
