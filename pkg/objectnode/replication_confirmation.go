package objectnode

import "context"

func (s *Store) reportReplicaConfirmation(ctx context.Context, source ReplicationSource, head ReplicationStatus, progress *ReplicationProgress) error {
	p := head.Durability
	if p == nil || p.Mode != DurabilityReplica || p.ReplicaID != s.replication.NodeID {
		return nil
	}
	confirmer, ok := source.(ReplicaConfirmer)
	if !ok {
		return ErrReplicationOff
	}
	local := s.replication
	// Complete the durability boundary again on an idempotent report following
	// a lost HTTP acknowledgement. Received may still be one entry ahead.
	if err := syncDirectory(s.replicationPath("")); err != nil {
		return err
	}
	c := ReplicaConfirmation{Version: 1, StreamID: local.StreamID, PrimaryID: local.PrimaryID, Epoch: local.Epoch, PolicyID: p.PolicyID, ReplicaID: local.NodeID, Sequence: local.AppliedSequence, Hash: local.AppliedHash}
	confirmed, err := confirmer.Confirm(ctx, c)
	if err != nil {
		return err
	}
	if confirmed.Version != 1 || confirmed.Mode != DurabilityReplica || confirmed.PolicyID != c.PolicyID || confirmed.ReplicaID != c.ReplicaID || confirmed.ConfirmedSequence != c.Sequence || confirmed.ConfirmedHash != c.Hash || confirmed.ConfirmedAt.IsZero() {
		return ErrReplicationFork
	}
	progress.ConfirmedSequence = &confirmed.ConfirmedSequence
	return nil
}
