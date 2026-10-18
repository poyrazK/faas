package objectnode

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

func (s localReplicationSource) ReportFollower(ctx context.Context, r FollowerReport) (RegisteredFollower, error) {
	return s.store.ReportFollower(ctx, r)
}

func registerTestFollower(t *testing.T, primary *Store, follower string) *Store {
	t.Helper()
	local, err := ReadReplicationStatus(follower)
	if err != nil {
		t.Fatal(err)
	}
	if err = primary.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = RegisterFollower(t.Context(), primary.root, local.NodeID, local.AppliedSequence, local.AppliedHash); err != nil {
		t.Fatal(err)
	}
	return reopenCompactionPrimary(t, primary.root)
}

func reopenCompactionPrimary(t *testing.T, directory string) *Store {
	t.Helper()
	s, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func compactTestNode(t *testing.T, s *Store, through int64) CompactionResult {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := CompactJournal(t.Context(), s.root, through)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestReplicationCompactionPinsRegisteredAppliedCursors(t *testing.T) {
	primary, follower := replicationPair(t)
	primary = registerTestFollower(t, primary, follower)
	first := putText(t, primary, "key", "shared")
	putText(t, primary, "key", "shared") // Same replay digest must survive pruning record 1.
	putText(t, primary, "later", "later")
	partial, err := CatchUpReplica(t.Context(), follower, localReplicationSource{primary}, CatchUpOptions{MaxEntries: 1, MaxBytes: 64 << 20})
	if err != nil || partial.Local.AppliedSequence != 1 {
		t.Fatal(partial, err)
	}
	head, _ := primary.ReplicationStatus()
	if len(head.Followers) != 1 || head.Followers[0].Sequence != 1 {
		t.Fatal("applied cursor was not reported", head)
	}
	if err = primary.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = CompactJournal(t.Context(), primary.root, 2); !errors.Is(err, ErrRetentionPinned) {
		t.Fatal("explicit cut bypassed a follower", err)
	}
	result, err := CompactJournal(t.Context(), primary.root, -1)
	if err != nil || result.Status.BaseSequence != 1 || result.RecordsPruned != 1 || result.After.Records != 2 || result.After.Blobs != 2 || result.Status.Checkpoint.Pruning {
		t.Fatal(result, err)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	retained, err := primary.OpenReplicationBlob(result.Status.StreamID, first.SHA256)
	if err != nil {
		t.Fatal("shared payload was reclaimed", err)
	}
	_ = retained.Close()
	catchUpTestReplica(t, primary, follower)
	result = compactTestNode(t, primary, -1)
	if result.Status.BaseSequence != 3 || result.After.Records != 0 || result.After.Blobs != 0 {
		t.Fatal("fully confirmed history was retained", result)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	if readText(t, primary, "key") != "shared" || readText(t, primary, "later") != "later" {
		t.Fatal("compaction changed current bytes")
	}
	if _, err = primary.ReplicationEntry(result.Status.StreamID, 1); !errors.Is(err, ErrReplicaReseed) {
		t.Fatal("old prefix was not explicitly retired", err)
	}
	putText(t, primary, "later", "continued")
	catchUpTestReplica(t, primary, follower)
	local, err := CompactJournal(t.Context(), follower, -1)
	if err != nil || local.Status.BaseSequence != 4 || local.After.Records != 0 || local.Status.Role != "follower" {
		t.Fatal("follower disk could not reclaim its own history", local, err)
	}
	putText(t, primary, "key", "after follower compaction")
	catchUpTestReplica(t, primary, follower)
	copy, err := openReplica(follower)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	if followerText(t, copy, "key") != "after follower compaction" {
		t.Fatal("follower lost its new baseline")
	}
}

func TestReplicationCompactionPreservesReceiptsMultipartBackupAndPromotion(t *testing.T) {
	primary, stale := replicationPair(t)
	committed, rejected := uuid.NewString(), uuid.NewString()
	original, err := receiptPut(t, primary, "history", "original", committed, PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	putText(t, primary, "history", "replacement")
	if _, err = receiptPut(t, primary, "history", "refused", rejected, PutOptions{IfNoneMatch: "*"}); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
	if err = primary.Delete("test-bucket", "history"); err != nil {
		t.Fatal(err)
	}
	complete := newMultipart(t, primary, objectchecksum.Policy{})
	part := multipartPart(t, primary, complete, 1, []byte("completed"))
	if _, err = primary.CompleteMultipart(t.Context(), complete.Bucket, complete.Key, complete.ID, completion(part), nil); err != nil {
		t.Fatal(err)
	}
	active := newMultipart(t, primary, objectchecksum.Policy{})
	selected := multipartPart(t, primary, active, 1, []byte("selected"))
	aborted := newMultipart(t, primary, objectchecksum.Policy{})
	if err = primary.AbortMultipart(aborted.Bucket, aborted.Key, aborted.ID); err != nil {
		t.Fatal(err)
	}
	usage, err := primary.Usage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result := compactTestNode(t, primary, -1)
	if result.After.Records != 0 || result.After.Blobs != 0 || result.BytesPruned <= 0 {
		t.Fatal(result)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	for id, status := range map[string]string{committed: ReceiptCommitted, rejected: ReceiptRejected} {
		r, err := primary.Receipt("test-bucket", "history", id)
		if err != nil || r.Status != status || id == committed && r.Object.ID != original.ID {
			t.Fatal("historical proof was altered", r, err)
		}
	}
	for id, status := range map[string]string{complete.ID: multipartCompleted, active.ID: multipartActive, aborted.ID: multipartAborted} {
		u, err := primary.readMultipart(id)
		if err != nil || u.Status != status {
			t.Fatal("multipart outcome was altered", u, err)
		}
	}
	after, err := primary.Usage(t.Context())
	if err != nil || !after.CatalogHealthy || usage.CommittedBytes != after.CommittedBytes || usage.MultipartBytes != after.MultipartBytes || usage.Receipts != after.Receipts || usage.Objects != after.Objects {
		t.Fatal("logical storage changed during compaction", usage, after, err)
	}
	if _, err = CatchUpReplica(t.Context(), stale, localReplicationSource{primary}, CatchUpOptions{MaxEntries: 32, MaxBytes: 64 << 20}); !errors.Is(err, ErrReplicaReseed) {
		t.Fatal("behind-floor follower was not told to reseed", err)
	}
	_ = primary.Close()
	backup, follower := filepath.Join(t.TempDir(), "backup"), filepath.Join(t.TempDir(), "fresh")
	if _, err = Backup(t.Context(), primary.root, backup); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyBackup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	if _, err = RestoreBackup(t.Context(), backup, follower); err != nil {
		t.Fatal(err)
	}
	local, err := ReadReplicationStatus(follower)
	if err != nil || local.BaseSequence != result.Status.AppliedSequence || local.Checkpoint != nil || len(local.Followers) != 0 {
		t.Fatal("restore cloned retention authority", local, err)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	putText(t, primary, "new", "new epoch will follow")
	catchUpTestReplica(t, primary, follower)
	if _, err = FenceReplicaPrimary(t.Context(), follower, localReplicationSource{primary}); err != nil {
		t.Fatal(err)
	}
	catchUpTestReplica(t, primary, follower)
	if _, err = PromoteReplica(t.Context(), follower, localReplicationSource{primary}); err != nil {
		t.Fatal(err)
	}
	next := reopenCompactionPrimary(t, follower)
	if _, err = next.CompleteMultipart(t.Context(), active.Bucket, active.Key, active.ID, completion(selected), nil); err != nil {
		t.Fatal("selected parts could not resume after promotion", err)
	}
	cut := compactTestNode(t, next, -1)
	if cut.Status.Epoch != 2 || cut.Status.BaseSequence <= cut.Status.Promotion.Sequence {
		t.Fatal("successor could not compact beyond its authority boundary", cut)
	}
	next = reopenCompactionPrimary(t, follower)
	putText(t, next, "new", "successor")
	_ = next.Close()
	second := filepath.Join(t.TempDir(), "successor-backup")
	if _, err = Backup(t.Context(), follower, second); err != nil {
		t.Fatal("post-promotion compacted backup failed", err)
	}
	if _, err = VerifyBackup(t.Context(), second); err != nil {
		t.Fatal(err)
	}
}

func TestReplicationCompactionPreservesPendingReceiptRecovery(t *testing.T) {
	primary, _ := replicationPair(t)
	id := uuid.NewString()
	injected := errors.New("retention reached after intent")
	primary.replicationPersist = func(path string, value any) (bool, error) {
		if e, ok := value.(JournalEntry); ok && e.Sequence == 2 {
			return false, injected
		}
		return writeReplicationJSON(path, value)
	}
	if _, err := receiptPut(t, primary, "pending", "pending bytes", id, PutOptions{}); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	result := compactTestNode(t, primary, -1)
	if result.Status.BaseSequence != 1 || result.After.Records != 0 || result.After.Blobs != 1 {
		t.Fatal("pending intent did not pin its recovery payload", result)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	proof, err := primary.Receipt("test-bucket", "pending", id)
	if err != nil || proof.Status != ReceiptCommitted || readText(t, primary, "pending") != "pending bytes" {
		t.Fatal("recovery lost a compacted intent", proof, err)
	}
	result = compactTestNode(t, primary, -1)
	if result.Status.AppliedSequence != 3 || result.After.Blobs != 0 {
		t.Fatal("settled payload was never reclaimed", result)
	}
}

func TestReplicationCompactionPreservesMultipartRecoveryBoundaries(t *testing.T) {
	for _, boundary := range []int64{4, 5, 7, 8} {
		t.Run(fmtSequence(boundary), func(t *testing.T) {
			primary, _ := replicationPair(t)
			u := newMultipart(t, primary, objectchecksum.Policy{})
			part := multipartPart(t, primary, u, 1, []byte("multipart recovery"))
			injected := errors.New("multipart publication interrupted")
			primary.replicationPersist = func(path string, value any) (bool, error) {
				if e, ok := value.(JournalEntry); ok && e.Sequence == boundary {
					return false, injected
				}
				return writeReplicationJSON(path, value)
			}
			if _, err := primary.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(part), nil); !errors.Is(err, injected) {
				t.Fatal("fault was not reached", boundary, err)
			}
			result := compactTestNode(t, primary, -1)
			if result.After.Records != 0 || result.Status.AppliedSequence != boundary-1 {
				t.Fatal(result)
			}
			primary = reopenCompactionPrimary(t, primary.root)
			completed, err := primary.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(part), nil)
			if err != nil || readText(t, primary, u.Key) != "multipart recovery" {
				t.Fatal("compaction lost a completion decision or selected part", completed, err)
			}
			proof, err := primary.MultipartReceipt(u.Bucket, u.Key, u.ReceiptID)
			if err != nil || proof.Status != ReceiptCommitted || proof.Object.ID != completed.ID {
				t.Fatal("completion did not retain its exact proof", proof, err)
			}
		})
	}
}

func TestReplicationCompactionDurabilityPinAndLocalHistory(t *testing.T) {
	primary, follower := durableReplicationPair(t)
	putText(t, primary, "key", "unconfirmed")
	result := compactTestNode(t, primary, -1)
	if result.Status.BaseSequence != 0 || result.Status.Durability.ConfirmedSequence != -1 || result.After.Records != 1 {
		t.Fatal("compaction weakened an unconfirmed promise", result)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	catchUpTestReplica(t, primary, follower)
	confirmation := confirmationAt(t, primary, 1)
	result = compactTestNode(t, primary, -1)
	primary = reopenCompactionPrimary(t, primary.root)
	if _, err := primary.ConfirmReplica(t.Context(), confirmation); err != nil {
		t.Fatal("duplicate confirmation lost its floor proof", err)
	}
	if err := primary.WaitForReplica(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = primary.Close()
	if _, err := ConfigureDurability(t.Context(), primary.root, DurabilityLocal, ""); err != nil {
		t.Fatal(err)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	putText(t, primary, "key", "local continuation")
	result = compactTestNode(t, primary, -1)
	if result.Status.Durability != nil || result.Status.BaseSequence != 2 || !validReplicationStatus(result.Status) {
		t.Fatal("old local-mode confirmation made the new floor invalid", result)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	if err := primary.WaitForReplica(t.Context()); err != nil {
		t.Fatal("local mode changed across compaction", err)
	}
}

func TestReplicationCompactionRetirementAndReplacementKeepDurabilityPromise(t *testing.T) {
	primary, follower := durableReplicationPair(t)
	primary = registerTestFollower(t, primary, follower)
	putText(t, primary, "key", "unconfirmed")
	old := confirmationAt(t, primary, 1)
	_ = primary.Close()
	if _, err := RetireFollower(t.Context(), primary.root, old.ReplicaID); err != nil {
		t.Fatal(err)
	}
	result, err := CompactJournal(t.Context(), primary.root, -1)
	if err != nil || result.Status.BaseSequence != 0 || result.Status.Durability.Mode != DurabilityReplica || result.After.Records != 1 {
		t.Fatal("retirement weakened replica acknowledgements", result, err)
	}
	if _, err = ConfigureDurability(t.Context(), primary.root, DurabilityReplica, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	result, err = CompactJournal(t.Context(), primary.root, -1)
	if err != nil || result.Status.BaseSequence != 0 || result.Status.Durability.ConfirmedSequence != -1 || result.Status.Durability.PolicyID == old.PolicyID {
		t.Fatal("replacement released an unconfirmed tail", result, err)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	if _, err = primary.ConfirmReplica(t.Context(), old); !errors.Is(err, ErrReplicationFork) {
		t.Fatal("delayed old confirmation released a replacement pin", err)
	}
}

func TestReplicationCompactionSequencesOutliveRetentionBudget(t *testing.T) {
	primary, _ := replicationPair(t)
	state := *primary.replication
	base := int64(api.ObjectNodeReplicationMaxEntries) + 123
	state.Version, state.BaseSequence, state.ReceivedSequence, state.AppliedSequence = 4, base, base, base
	state.BaseHash, state.ReceivedHash, state.AppliedHash = nameHash("verified earlier prefix"), nameHash("verified earlier prefix"), nameHash("verified earlier prefix")
	state.Checkpoint = &JournalCheckpoint{Version: 1, Sequence: base, Hash: state.BaseHash, CreatedAt: time.Now().UTC()}
	if err := primary.saveReplicationState(state); err != nil {
		t.Fatal(err)
	}
	_ = primary.Close()
	primary = reopenCompactionPrimary(t, primary.root)
	putText(t, primary, "key", "beyond old ceiling")
	result := compactTestNode(t, primary, -1)
	if result.Status.AppliedSequence != base+1 || result.After.Records != 0 {
		t.Fatal("compaction recycled or capped a lifetime sequence", result)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	putText(t, primary, "key", "keeps increasing")
	head, _ := primary.ReplicationStatus()
	entry, err := primary.ReplicationEntry(head.StreamID, base+2)
	if err != nil || entry.Sequence != base+2 || journalName(entry.Sequence) != "00000000000001000125.json" {
		t.Fatal(entry, err)
	}
	primary.journalUsage.Records = api.ObjectNodeReplicationMaxEntries
	if _, err = primary.Put(t.Context(), "test-bucket", "key", strings.NewReader("refused"), 7, PutOptions{}); !errors.Is(err, ErrCapacity) {
		t.Fatal("retained record ceiling was ignored", err)
	}
	if readText(t, primary, "key") != "keeps increasing" {
		t.Fatal("rejected write replaced the current object")
	}
	primary.journalUsage.Records = 1
	primary.journalUsage.JournalBytes = api.ObjectNodeReplicationJournalBytes
	if _, err = primary.Put(t.Context(), "test-bucket", "key", strings.NewReader("refused"), 7, PutOptions{}); !errors.Is(err, ErrCapacity) {
		t.Fatal("journal byte ceiling was ignored", err)
	}
	primary.journalUsage.BlobBytes = api.ObjectNodeReplicationBlobBytes
	if _, err = primary.Put(t.Context(), "test-bucket", "key", strings.NewReader("fresh blob"), 10, PutOptions{}); !errors.Is(err, ErrCapacity) {
		t.Fatal("payload byte ceiling was ignored", err)
	}
}

func TestReplicationCompactionSequenceLimitRemainsExactAndDoesNotWrap(t *testing.T) {
	primary, _ := replicationPair(t)
	state := *primary.replication
	base := api.ObjectNodeReplicationMaxSequence - 1
	hash := nameHash("verified large prefix")
	state.Version, state.BaseSequence, state.ReceivedSequence, state.AppliedSequence = 4, base, base, base
	state.BaseHash, state.ReceivedHash, state.AppliedHash = hash, hash, hash
	state.Checkpoint = &JournalCheckpoint{Version: 1, Sequence: base, Hash: hash, CreatedAt: time.Now().UTC()}
	if err := primary.saveReplicationState(state); err != nil {
		t.Fatal(err)
	}
	_ = primary.Close()
	primary = reopenCompactionPrimary(t, primary.root)
	putText(t, primary, "key", "last sequence")
	if _, err := primary.Put(t.Context(), "test-bucket", "key", strings.NewReader("overflow"), 8, PutOptions{}); !errors.Is(err, ErrCapacity) {
		t.Fatal("sequence ceiling did not refuse publication", err)
	}
	result := compactTestNode(t, primary, -1)
	if result.Status.BaseSequence != api.ObjectNodeReplicationMaxSequence || result.Status.AppliedSequence != api.ObjectNodeReplicationMaxSequence || !validReplicationStatus(result.Status) {
		t.Fatal("large sequence wrapped or lost its hash anchor", result)
	}
	primary = reopenCompactionPrimary(t, primary.root)
	if readText(t, primary, "key") != "last sequence" {
		t.Fatal("large sequence checkpoint lost current bytes")
	}
}
