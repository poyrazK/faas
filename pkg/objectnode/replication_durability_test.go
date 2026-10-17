package objectnode

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (s localReplicationSource) Confirm(ctx context.Context, confirmation ReplicaConfirmation) (ReplicaDurability, error) {
	return s.store.ConfirmReplica(ctx, confirmation)
}

func durableReplicationPair(t *testing.T) (*Store, string) {
	t.Helper()
	primary, follower := replicationPair(t)
	local, err := ReadReplicationStatus(follower)
	if err != nil {
		t.Fatal(err)
	}
	if err = primary.Close(); err != nil {
		t.Fatal(err)
	}
	configured, err := ConfigureDurability(t.Context(), primary.root, DurabilityReplica, local.NodeID)
	if err != nil || configured.Version != 3 || configured.Durability == nil || configured.Durability.ConfirmedSequence != -1 {
		t.Fatal(configured, err)
	}
	reopened, err := Open(primary.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	return reopened, follower
}

func requireReplicaPending(t *testing.T, s *Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := s.WaitForReplica(ctx); !errors.Is(err, ErrReplicaPending) {
		t.Fatal("unconfirmed position did not remain pending", err)
	}
}

func confirmationAt(t *testing.T, s *Store, sequence int64) ReplicaConfirmation {
	t.Helper()
	head, err := s.ReplicationStatus()
	if err != nil {
		t.Fatal(err)
	}
	hash := head.BaseHash
	if sequence != head.BaseSequence {
		entry, err := s.ReplicationEntry(head.StreamID, sequence)
		if err != nil {
			t.Fatal(err)
		}
		hash = entry.Hash
	}
	return ReplicaConfirmation{Version: 1, StreamID: head.StreamID, PrimaryID: head.NodeID, Epoch: head.Epoch, PolicyID: head.Durability.PolicyID, ReplicaID: head.Durability.ReplicaID, Sequence: sequence, Hash: hash}
}

func TestReplicaDurabilityWaitsForAppliedTerminalPosition(t *testing.T) {
	primary, follower := durableReplicationPair(t)
	requireReplicaPending(t, primary)
	progress := catchUpTestReplica(t, primary, follower)
	if progress.ConfirmedSequence == nil || *progress.ConfirmedSequence != 0 {
		t.Fatal("baseline was not confirmed", progress)
	}
	id := uuid.NewString()
	if _, err := receiptPut(t, primary, "key", "payload", id, PutOptions{}); err != nil {
		t.Fatal(err)
	}
	requireReplicaPending(t, primary)
	partial, err := CatchUpReplica(t.Context(), follower, localReplicationSource{primary}, CatchUpOptions{MaxEntries: 1, MaxBytes: 1})
	if err != nil || partial.Local.ReceivedSequence <= partial.Local.AppliedSequence || partial.ConfirmedSequence == nil || *partial.ConfirmedSequence != 0 {
		t.Fatal("partial transfer confirmed received bytes", partial, err)
	}
	requireReplicaPending(t, primary)
	progress = catchUpTestReplica(t, primary, follower)
	if progress.ConfirmedSequence == nil || *progress.ConfirmedSequence != progress.Local.AppliedSequence {
		t.Fatal(progress)
	}
	if err = primary.WaitForReplica(t.Context()); err != nil {
		t.Fatal(err)
	}
	putText(t, primary, "key", "replacement")
	if err = primary.Delete("test-bucket", "key"); err != nil {
		t.Fatal(err)
	}
	requireReplicaPending(t, primary)
	catchUpTestReplica(t, primary, follower)
	if err = primary.WaitForReplica(t.Context()); err != nil {
		t.Fatal(err)
	}
	proof, err := primary.Receipt("test-bucket", "key", id)
	if err != nil || proof.Status != ReceiptCommitted {
		t.Fatal(proof, err)
	}
	if err = primary.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(primary.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.WaitForReplica(t.Context()); err != nil {
		t.Fatal("confirmed point was lost on restart", err)
	}
}

func TestReplicaConfirmationRejectsWrongAuthorityAndPosition(t *testing.T) {
	primary, _ := durableReplicationPair(t)
	putText(t, primary, "key", "payload")
	valid := confirmationAt(t, primary, 1)
	for _, field := range []string{"version", "stream", "primary", "epoch", "policy", "replica", "hash", "future", "negative"} {
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
		case "policy":
			bad.PolicyID = uuid.NewString()
		case "replica":
			bad.ReplicaID = uuid.NewString()
		case "hash":
			bad.Hash = nameHash("wrong")
		case "future":
			bad.Sequence++
		case "negative":
			bad.Sequence = -1
		}
		if _, err := primary.ConfirmReplica(t.Context(), bad); err == nil {
			t.Fatal("invalid confirmation accepted", field)
		}
	}
	if _, err := primary.ConfirmReplica(t.Context(), valid); err != nil {
		t.Fatal(err)
	}
	if repeated, err := primary.ConfirmReplica(t.Context(), valid); err != nil || repeated.ConfirmedSequence != 1 {
		t.Fatal(repeated, err)
	}
	if _, err := primary.ConfirmReplica(t.Context(), confirmationAt(t, primary, 0)); !errors.Is(err, ErrReplicationFork) {
		t.Fatal("confirmation regressed", err)
	}
}

func TestReplicaDurabilityCannotDowngradeUnconfirmedWrites(t *testing.T) {
	primary, follower := durableReplicationPair(t)
	putText(t, primary, "pending", "pending")
	head, err := primary.ReplicationStatus()
	if err != nil {
		t.Fatal(err)
	}
	if err = primary.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = ConfigureDurability(t.Context(), primary.root, DurabilityLocal, ""); !errors.Is(err, ErrReplicaPending) {
		t.Fatal("unconfirmed policy was weakened", err)
	}
	same, err := ConfigureDurability(t.Context(), primary.root, DurabilityReplica, head.Durability.ReplicaID)
	if err != nil || same.Durability.PolicyID != head.Durability.PolicyID {
		t.Fatal("retry changed policy", same, err)
	}
	reopened, err := Open(primary.root)
	if err != nil {
		t.Fatal(err)
	}
	catchUpTestReplica(t, reopened, follower)
	if err = reopened.Close(); err != nil {
		t.Fatal(err)
	}
	local, err := ConfigureDurability(t.Context(), primary.root, DurabilityLocal, "")
	if err != nil || local.Durability.Mode != DurabilityLocal {
		t.Fatal(local, err)
	}
	reopened, err = Open(primary.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	putText(t, reopened, "local", "available")
	if err = reopened.WaitForReplica(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestReplicaReplacementCannotReleaseUnconfirmedWrites(t *testing.T) {
	primary, original := durableReplicationPair(t)
	putText(t, primary, "key", "pending")
	old, err := primary.ReplicationStatus()
	if err != nil {
		t.Fatal(err)
	}
	stale := confirmationAt(t, primary, old.AppliedSequence)
	if err = primary.Close(); err != nil {
		t.Fatal(err)
	}
	backup, replacement := filepath.Join(t.TempDir(), "replacement-seed"), filepath.Join(t.TempDir(), "replacement")
	if _, err = Backup(t.Context(), primary.root, backup); err != nil {
		t.Fatal(err)
	}
	if _, err = BootstrapReplica(t.Context(), backup, replacement); err != nil {
		t.Fatal(err)
	}
	local, err := ReadReplicationStatus(replacement)
	if err != nil {
		t.Fatal(err)
	}
	rebound, err := ConfigureDurability(t.Context(), primary.root, DurabilityReplica, local.NodeID)
	if err != nil || rebound.Durability.PolicyID == old.Durability.PolicyID || rebound.Durability.ConfirmedSequence != -1 {
		t.Fatal(rebound, err)
	}
	primary, err = Open(primary.root)
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	if _, err = primary.ConfirmReplica(t.Context(), stale); !errors.Is(err, ErrReplicationFork) {
		t.Fatal("old policy report accepted", err)
	}
	catchUpTestReplica(t, primary, original)
	requireReplicaPending(t, primary)
	catchUpTestReplica(t, primary, replacement)
	if err = primary.WaitForReplica(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestReplicaConfirmationPersistenceFailureAndLostAck(t *testing.T) {
	for _, boundary := range []string{"before", "after"} {
		t.Run(boundary, func(t *testing.T) {
			primary, _ := durableReplicationPair(t)
			putText(t, primary, "key", "payload")
			confirmation := confirmationAt(t, primary, 1)
			injected, fired := errors.New("confirmation checkpoint failure"), false
			primary.replicationPersist = func(path string, value any) (bool, error) {
				state, ok := value.(replicationState)
				target := ok && state.Durability != nil && state.Durability.ConfirmedSequence == 1
				if target && !fired && boundary == "before" {
					fired = true
					return false, injected
				}
				published, err := writeReplicationJSON(path, value)
				if err == nil && target && !fired && boundary == "after" {
					fired = true
					return published, injected
				}
				return published, err
			}
			if _, err := primary.ConfirmReplica(t.Context(), confirmation); !errors.Is(err, injected) || !fired {
				t.Fatal(err)
			}
			requireReplicaPending(t, primary)
			if _, err := primary.ConfirmReplica(t.Context(), confirmation); err != nil {
				t.Fatal("confirmation retry failed", err)
			}
			if err := primary.WaitForReplica(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReplicaDurabilityPromotionPreservesModeAndRequiresNewFollower(t *testing.T) {
	primary, follower := durableReplicationPair(t)
	putText(t, primary, "key", "durable")
	catchUpTestReplica(t, primary, follower)
	wrong := fenceTestRequest(t, primary, follower)
	wrong.TargetID = uuid.NewString()
	if _, err := primary.FencePrimary(t.Context(), wrong); !errors.Is(err, ErrReplicationFork) {
		t.Fatal("another target received the replica handoff", err)
	}
	grant, err := FenceReplicaPrimary(t.Context(), follower, localReplicationSource{primary})
	if err != nil {
		t.Fatal(err)
	}
	catchUpTestReplica(t, primary, follower)
	status, err := PromoteReplica(t.Context(), follower, localReplicationSource{primary})
	if err != nil || status.Durability.Mode != DurabilityReplica || status.Durability.ReplicaID != "" || status.Durability.ConfirmedSequence != grant.Sequence {
		t.Fatal(status, err)
	}
	next, err := Open(follower)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(next.CanMutate(), ErrReplicaPending) {
		t.Fatal("unbound successor admitted mutations")
	}
	if err = next.WaitForReplica(t.Context()); err != nil {
		t.Fatal("handoff did not certify the old prefix", err)
	}
	if err = next.Close(); err != nil {
		t.Fatal(err)
	}
	backup, rejoined := filepath.Join(t.TempDir(), "seed"), filepath.Join(t.TempDir(), "rejoined")
	if _, err = Backup(t.Context(), follower, backup); err != nil {
		t.Fatal(err)
	}
	if _, err = BootstrapReplica(t.Context(), backup, rejoined); err != nil {
		t.Fatal(err)
	}
	standby, err := ReadReplicationStatus(rejoined)
	if err != nil || standby.Durability != nil {
		t.Fatal("restore cloned primary confirmation authority", standby, err)
	}
	if _, err = ConfigureDurability(t.Context(), follower, DurabilityReplica, standby.NodeID); err != nil {
		t.Fatal(err)
	}
	next, err = Open(follower)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err = next.CanMutate(); err != nil {
		t.Fatal(err)
	}
	putText(t, next, "new", "new epoch")
	requireReplicaPending(t, next)
	catchUpTestReplica(t, next, rejoined)
	if err = next.WaitForReplica(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestReplicaWaitReleasesLocksAndUnblocksOnClose(t *testing.T) {
	primary, follower := durableReplicationPair(t)
	putText(t, primary, "key", "pending")
	done := make(chan error, 1)
	go func() { done <- primary.WaitForReplica(t.Context()) }()
	catchUpTestReplica(t, primary, follower)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("confirmation could not wake a waiter")
	}
	if _, err := primary.Put(t.Context(), "test-bucket", "next", strings.NewReader("next"), 4, PutOptions{}); err != nil {
		t.Fatal(err)
	}
	go func() { done <- primary.WaitForReplica(t.Context()) }()
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown held a replica waiter")
	}
}
