package objectnode

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func followerReportAt(t *testing.T, primary *Store, sequence int64) FollowerReport {
	t.Helper()
	head, err := primary.ReplicationStatus()
	if err != nil {
		t.Fatal(err)
	}
	hash := head.BaseHash
	if sequence != head.BaseSequence {
		e, err := primary.ReplicationEntry(head.StreamID, sequence)
		if err != nil {
			t.Fatal(err)
		}
		hash = e.Hash
	}
	f := head.Followers[0]
	return FollowerReport{Version: 1, StreamID: head.StreamID, PrimaryID: head.NodeID, Epoch: head.Epoch, RegistrationID: f.RegistrationID, ReplicaID: f.ReplicaID, Sequence: sequence, Hash: hash}
}

func TestReplicationFollowerRegistrationGenerationsAndReports(t *testing.T) {
	primary, follower := replicationPair(t)
	primary = registerTestFollower(t, primary, follower)
	putText(t, primary, "key", "payload")
	valid := followerReportAt(t, primary, 1)
	for _, field := range []string{"version", "stream", "primary", "epoch", "registration", "replica", "hash", "future", "negative"} {
		bad := valid
		switch field {
		case "version":
			bad.Version++
		case "stream":
			bad.StreamID = uuid.NewString()
		case "primary":
			bad.PrimaryID = uuid.NewString()
		case "epoch":
			bad.Epoch++
		case "registration":
			bad.RegistrationID = uuid.NewString()
		case "replica":
			bad.ReplicaID = uuid.NewString()
		case "hash":
			bad.Hash = nameHash("wrong")
		case "future":
			bad.Sequence++
		case "negative":
			bad.Sequence = -1
		}
		if _, err := primary.ReportFollower(t.Context(), bad); err == nil {
			t.Fatal("invalid cursor was accepted", field)
		}
	}
	// A partial blob transfer must report only the previous applied position.
	progress, err := CatchUpReplica(t.Context(), follower, localReplicationSource{primary}, CatchUpOptions{MaxEntries: 1, MaxBytes: 1})
	if err != nil || progress.Local.AppliedSequence != 0 || progress.Local.ReceivedSequence != 1 {
		t.Fatal(progress, err)
	}
	head, _ := primary.ReplicationStatus()
	if head.Followers[0].Sequence != 0 {
		t.Fatal("received bytes advanced a retention pin", head)
	}
	if _, err = CompactJournal(t.Context(), follower, -1); !errors.Is(err, ErrReplicaPending) {
		t.Fatal("partial follower transfer was compacted", err)
	}
	catchUpTestReplica(t, primary, follower)
	if _, err = primary.ReportFollower(t.Context(), followerReportAt(t, primary, 0)); !errors.Is(err, ErrReplicationFork) {
		t.Fatal("regressing cursor was accepted", err)
	}
	_ = primary.Close()
	local, err := ReadReplicationStatus(follower)
	if err != nil {
		t.Fatal(err)
	}
	before, err := RegisterFollower(t.Context(), primary.root, local.NodeID, 1, local.AppliedHash)
	if err != nil || before.Followers[0].RegistrationID != valid.RegistrationID {
		t.Fatal("retry changed enrollment generation", before, err)
	}
	if _, err = RetireFollower(t.Context(), primary.root, local.NodeID); err != nil {
		t.Fatal(err)
	}
	after, err := RegisterFollower(t.Context(), primary.root, local.NodeID, 1, local.AppliedHash)
	if err != nil || after.Followers[0].RegistrationID == valid.RegistrationID {
		t.Fatal("retirement reused an old generation", after, err)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	if _, err = primary.ReportFollower(t.Context(), valid); !errors.Is(err, ErrReplicationFork) {
		t.Fatal("delayed report crossed re-enrollment", err)
	}
	catchUpTestReplica(t, primary, follower)
	putText(t, primary, "key", "behind")
	_ = primary.Close()
	if _, err = RetireFollower(t.Context(), primary.root, local.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err = CompactJournal(t.Context(), primary.root, -1); err != nil {
		t.Fatal(err)
	}
	if _, err = RegisterFollower(t.Context(), primary.root, local.NodeID, 1, local.AppliedHash); !errors.Is(err, ErrReplicaReseed) {
		t.Fatal("enrollment silently accepted retired history", err)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	if _, err = CatchUpReplica(t.Context(), follower, localReplicationSource{primary}, CatchUpOptions{MaxEntries: 32, MaxBytes: 64 << 20}); !errors.Is(err, ErrReplicaReseed) {
		t.Fatal("retired follower was not told to reseed", err)
	}
}

func TestReplicationFollowerReportUncertainPersistence(t *testing.T) {
	for _, boundary := range []string{"before", "after"} {
		t.Run(boundary, func(t *testing.T) {
			primary, follower := replicationPair(t)
			primary = registerTestFollower(t, primary, follower)
			putText(t, primary, "key", "payload")
			injected := errors.New("cursor persistence acknowledgement lost")
			primary.replicationPersist = func(path string, value any) (bool, error) {
				if state, ok := value.(replicationState); ok && state.Followers[0].Sequence == 1 {
					if boundary == "before" {
						return false, injected
					}
					published, err := writeReplicationJSON(path, value)
					if err != nil {
						return published, err
					}
					return published, injected
				}
				return writeReplicationJSON(path, value)
			}
			if _, err := primary.ReportFollower(t.Context(), followerReportAt(t, primary, 1)); !errors.Is(err, injected) {
				t.Fatal(err)
			}
			status, _ := primary.ReplicationStatus()
			if status.Followers[0].Sequence != 0 {
				t.Fatal("speculative report advanced the memory pin", status)
			}
			primary.replicationPersist = writeReplicationJSON
			if _, err := primary.ReportFollower(t.Context(), followerReportAt(t, primary, 1)); err != nil {
				t.Fatal("idempotent report could not resolve a lost acknowledgement", err)
			}
		})
	}
}

type lostCursorSource struct{ localReplicationSource }

func (s lostCursorSource) ReportFollower(ctx context.Context, report FollowerReport) (RegisteredFollower, error) {
	_, err := s.localReplicationSource.ReportFollower(ctx, report)
	if err != nil {
		return RegisteredFollower{}, err
	}
	return RegisteredFollower{}, io.ErrUnexpectedEOF
}

func TestReplicationFollowerLostReportResponseAfterDurableApply(t *testing.T) {
	primary, follower := replicationPair(t)
	primary = registerTestFollower(t, primary, follower)
	putText(t, primary, "key", "payload")
	progress, err := CatchUpReplica(t.Context(), follower, lostCursorSource{localReplicationSource{primary}}, CatchUpOptions{MaxEntries: 32, MaxBytes: 64 << 20})
	if !errors.Is(err, io.ErrUnexpectedEOF) || !progress.CaughtUp || progress.Local.AppliedSequence != 1 {
		t.Fatal(progress, err)
	}
	result := compactTestNode(t, primary, -1)
	if result.Status.BaseSequence != 1 {
		t.Fatal("lost HTTP response invalidated durable cursor", result)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	progress = catchUpTestReplica(t, primary, follower)
	if progress.EntriesApplied != 0 || progress.BytesTransferred != 0 {
		t.Fatal("report retry replayed a compacted entry", progress)
	}
}

func TestReplicationFollowerRegistrationBudget(t *testing.T) {
	primary, _ := replicationPair(t)
	head, _ := primary.ReplicationStatus()
	_ = primary.Close()
	for range api.ObjectNodeReplicationMaxFollowers {
		if _, err := RegisterFollower(t.Context(), primary.root, uuid.NewString(), head.AppliedSequence, head.AppliedHash); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := RegisterFollower(t.Context(), primary.root, uuid.NewString(), head.AppliedSequence, head.AppliedHash); !errors.Is(err, ErrCapacity) {
		t.Fatal("follower registration budget was ignored", err)
	}
	if _, err := RegisterFollower(t.Context(), primary.root, head.NodeID, head.AppliedSequence, head.AppliedHash); !errors.Is(err, ErrInvalid) {
		t.Fatal("primary registered itself", err)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	status, err := primary.ReplicationStatus()
	if err != nil || len(status.Followers) != api.ObjectNodeReplicationMaxFollowers {
		t.Fatal(status, err)
	}
}
