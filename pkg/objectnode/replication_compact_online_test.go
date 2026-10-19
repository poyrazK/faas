package objectnode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func onlineOptions() CompactionOptions {
	o := DefaultCompactionOptions()
	o.TriggerRecords = 1
	o.MaxRecords = 1
	o.MaxBlobs = 1
	return o
}

func drainOnline(t *testing.T, s *Store, o CompactionOptions) {
	t.Helper()
	for range 1000 {
		p, err := s.CompactStep(t.Context(), o)
		if err != nil {
			t.Fatal(p, err)
		}
		if p.StepRecords > o.MaxRecords || p.StepBlobs > o.MaxBlobs {
			t.Fatal("unbounded step", p)
		}
		if !p.Active {
			return
		}
	}
	t.Fatal("online compaction did not finish")
}

func TestOnlineCompactionPreservesSharedPayloadsAndFollowerPins(t *testing.T) {
	primary, follower := replicationPair(t)
	primary = registerTestFollower(t, primary, follower)
	first := putText(t, primary, "first", "shared")
	putText(t, primary, "second", "shared")
	putText(t, primary, "third", "other")
	p, err := CatchUpReplica(t.Context(), follower, localReplicationSource{primary}, CatchUpOptions{MaxEntries: 1, MaxBytes: 64 << 20})
	if err != nil || p.Local.AppliedSequence != 1 {
		t.Fatal(p, err)
	}
	o := onlineOptions()
	for range 10 {
		if _, err = primary.CompactStep(t.Context(), o); err != nil {
			t.Fatal(err)
		}
	}
	head, _ := primary.ReplicationStatus()
	if head.BaseSequence != 1 || primary.retentionIndex.refs[first.SHA256] != 1 {
		t.Fatal("pin/shared reference lost", head, primary.retentionIndex.refs)
	}
	file, err := primary.OpenReplicationBlob(head.StreamID, first.SHA256)
	if err != nil {
		t.Fatal("retained payload was removed", err)
	}
	_ = file.Close()
	catchUpTestReplica(t, primary, follower)
	drainOnline(t, primary, o)
	if primary.journalUsage.Records != 0 || primary.journalUsage.Blobs != 0 || readText(t, primary, "first") != "shared" {
		t.Fatal(primary.journalUsage)
	}
	putText(t, primary, "after", "continued")
	catchUpTestReplica(t, primary, follower)
	f, err := OpenFollower(follower)
	if err != nil {
		t.Fatal(err)
	}
	drainOnline(t, f.store, o)
	if f.store.journalUsage.Records != 0 || f.store.journalUsage.Blobs != 0 || followerText(t, f.store, "after") != "continued" {
		t.Fatal(f.store.journalUsage)
	}
	_ = f.Close()
}

func TestOnlineCompactionNeverBypassesReplicaConfirmation(t *testing.T) {
	primary, follower := durableReplicationPair(t)
	id := uuid.NewString()
	if _, err := receiptPut(t, primary, "key", "payload", id, PutOptions{}); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if _, err := primary.CompactStep(t.Context(), onlineOptions()); err != nil {
			t.Fatal(err)
		}
	}
	head, _ := primary.ReplicationStatus()
	if head.BaseSequence != 0 {
		t.Fatal("unconfirmed prefix pruned", head)
	}
	requireReplicaPending(t, primary)
	partial, err := CatchUpReplica(t.Context(), follower, localReplicationSource{primary}, CatchUpOptions{MaxEntries: 1, MaxBytes: 1})
	if err != nil || partial.Local.ReceivedSequence == partial.Local.AppliedSequence {
		t.Fatal(partial, err)
	}
	f, err := OpenFollower(follower)
	if err != nil {
		t.Fatal(err)
	}
	before := f.store.journalUsage
	status, err := f.store.CompactStep(t.Context(), onlineOptions())
	if err != nil || !status.Deferred || f.store.journalUsage != before {
		t.Fatal("partial receipt/transfer compacted", status, err)
	}
	_ = f.Close()
	catchUpTestReplica(t, primary, follower)
	drainOnline(t, primary, onlineOptions())
	if err = primary.WaitForReplica(t.Context()); err != nil {
		t.Fatal(err)
	}
	proof, err := primary.Receipt("test-bucket", "key", id)
	if err != nil || proof.Status != ReceiptCommitted {
		t.Fatal(proof, err)
	}
}

func TestOnlineCompactionKeepsPendingReceiptBelowFloorAndAfterRestart(t *testing.T) {
	primary, follower := replicationPair(t)
	basePersist := primary.replicationPersist
	injected := errors.New("terminal publication blocked")
	primary.replicationPersist = func(path string, value any) (bool, error) {
		if e, ok := value.(JournalEntry); ok && strings.HasPrefix(e.Path, "receipts/") {
			var r writeReceipt
			if decodeBackupRecord(e.Data, &r) == nil && r.Status == ReceiptCommitted {
				return false, injected
			}
		}
		return basePersist(path, value)
	}
	id := uuid.NewString()
	if _, err := receiptPut(t, primary, "key", "pending bytes", id, PutOptions{}); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	digest := primary.pending.Object.SHA256
	catchUpTestReplica(t, primary, follower)
	drainOnline(t, primary, onlineOptions())
	if primary.journalUsage.Records != 0 || primary.journalUsage.Blobs != 1 || primary.retentionIndex.pins[digest] != 1 {
		t.Fatal("pending replay was lost", primary.journalUsage)
	}
	f, err := OpenFollower(follower)
	if err != nil {
		t.Fatal(err)
	}
	drainOnline(t, f.store, onlineOptions())
	_ = f.Close()
	f, err = OpenFollower(follower)
	if err != nil {
		t.Fatal(err)
	}
	if f.store.retentionIndex.pins[digest] != 1 || f.store.journalUsage.Blobs != 1 {
		t.Fatal("pending pin was not rebuilt")
	}
	_ = f.Close()
	primary.replicationPersist = basePersist
	r, err := primary.Receipt("test-bucket", "key", id)
	if err != nil || r.Status != ReceiptCommitted {
		t.Fatal(r, err)
	}
	catchUpTestReplica(t, primary, follower)
	drainOnline(t, primary, onlineOptions())
	f, err = OpenFollower(follower)
	if err != nil {
		t.Fatal(err)
	}
	drainOnline(t, f.store, onlineOptions())
	_ = f.Close()
	if primary.journalUsage.Blobs != 0 || f.store.journalUsage.Blobs != 0 {
		t.Fatal("resolved pin leaked", primary.journalUsage, f.store.journalUsage)
	}
}

func TestOnlineCompactionDefersBlobDeletionDuringReplayPreparation(t *testing.T) {
	primary, _ := replicationPair(t)
	old := putText(t, primary, "old", "reused")
	if _, err := primary.CompactStep(t.Context(), onlineOptions()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	copyFile := primary.replayCopy
	var once sync.Once
	primary.replayCopy = func(ctx context.Context, source, destination string, size int64, buffer []byte) (string, error) {
		once.Do(func() { close(entered); <-release })
		return copyFile(ctx, source, destination, size, buffer)
	}
	done := make(chan error, 1)
	go func() {
		_, err := primary.Put(t.Context(), "test-bucket", "new", strings.NewReader("reused"), 6, PutOptions{})
		done <- err
	}()
	<-entered
	status, err := primary.CompactStep(t.Context(), onlineOptions())
	if err != nil || !status.Deferred {
		close(release)
		<-done
		t.Fatal(status, err)
	}
	if _, err = os.Stat(primary.replicationPath("blobs/" + old.SHA256 + ".data")); err != nil {
		close(release)
		<-done
		t.Fatal(err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	primary.replayCopy = copyFile
	drainOnline(t, primary, onlineOptions())
	if readText(t, primary, "new") != "reused" {
		t.Fatal("prepared bytes changed")
	}
}

func TestOnlineCompactionCreditsOnlySyncedUnlinks(t *testing.T) {
	for _, directory := range []string{"log", "blobs"} {
		t.Run(directory, func(t *testing.T) {
			primary, _ := replicationPair(t)
			putText(t, primary, "key", "reusable")
			if directory == "blobs" {
				if _, err := primary.CompactStep(t.Context(), onlineOptions()); err != nil {
					t.Fatal(err)
				}
			}
			before := primary.journalUsage
			injected := errors.New("directory sync acknowledgement lost")
			primary.compactionSync = func(path string) error {
				if filepath.Base(path) == directory {
					return injected
				}
				return syncDirectory(path)
			}
			if _, err := primary.CompactStep(t.Context(), onlineOptions()); !errors.Is(err, injected) {
				t.Fatal(err)
			}
			if primary.journalUsage != before {
				t.Fatal("unsynced unlink credited", before, primary.journalUsage)
			}
			putText(t, primary, "key", "reusable")
			primary.compactionSync = nil
			drainOnline(t, primary, onlineOptions())
			drainOnline(t, primary, onlineOptions())
			if primary.journalUsage.Records != 0 || primary.journalUsage.Blobs != 0 || readText(t, primary, "key") != "reusable" {
				t.Fatal(primary.journalUsage)
			}
		})
	}
}

func TestOnlineCompactionConcurrentWritesAndReads(t *testing.T) {
	primary, _ := replicationPair(t)
	putText(t, primary, "key", "value-0")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	errorsSeen := make(chan error, 8)
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			if _, err := primary.CompactStep(ctx, onlineOptions()); err != nil && ctx.Err() == nil {
				errorsSeen <- err
				return
			}
		}
	}()
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for ctx.Err() == nil {
			_, file, err := primary.Get("test-bucket", "key")
			if err != nil {
				errorsSeen <- err
				return
			}
			_ = file.Close()
		}
	}()
	var ids []string
	for n := 1; n <= 30; n++ {
		id := uuid.NewString()
		ids = append(ids, id)
		if _, err := receiptPut(t, primary, "key", fmt.Sprintf("value-%d", n), id, PutOptions{}); err != nil {
			cancel()
			<-done
			<-readerDone
			t.Fatal(err)
		}
	}
	cancel()
	<-done
	<-readerDone
	select {
	case err := <-errorsSeen:
		t.Fatal(err)
	default:
	}
	drainOnline(t, primary, onlineOptions())
	if readText(t, primary, "key") != "value-30" {
		t.Fatal("latest bytes lost")
	}
	for _, id := range ids {
		r, err := primary.Receipt("test-bucket", "key", id)
		if err != nil || r.Status != ReceiptCommitted {
			t.Fatal(r, err)
		}
	}
}

type advancingFloorSource struct {
	local   localReplicationSource
	advance func()
	mutate  func(*ReplicationStatus)
}

func (s *advancingFloorSource) Head(ctx context.Context) (ReplicationStatus, error) {
	h, e := s.local.Head(ctx)
	if s.mutate != nil {
		s.mutate(&h)
	}
	return h, e
}
func (s *advancingFloorSource) Entry(ctx context.Context, stream string, sequence int64) (JournalEntry, error) {
	if s.advance != nil {
		s.advance()
		s.advance = nil
	}
	return s.local.Entry(ctx, stream, sequence)
}
func (s *advancingFloorSource) Blob(ctx context.Context, stream, digest string, offset, length int64) (io.ReadCloser, error) {
	return s.local.Blob(ctx, stream, digest, offset, length)
}

func TestOnlineCompactionFollowerAcceptsOnlyExactAdvancedFloor(t *testing.T) {
	for _, scenario := range []string{"exact", "behind", "changed authority", "wrong hash"} {
		t.Run(scenario, func(t *testing.T) {
			primary, _ := replicationPair(t)
			putText(t, primary, "first", "one")
			putText(t, primary, "second", "two")
			head, _ := primary.ReplicationStatus()
			entry, _ := primary.ReplicationEntry(head.StreamID, 1)
			source := &advancingFloorSource{local: localReplicationSource{primary}}
			source.advance = func() {
				o := onlineOptions()
				if scenario == "behind" {
					o.MaxRecords = 2
				}
				if _, err := primary.CompactStep(t.Context(), o); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "changed authority" {
				source.mutate = func(h *ReplicationStatus) { h.NodeID = uuid.NewString(); h.PrimaryID = h.NodeID }
			}
			hash := entry.Hash
			if scenario == "wrong hash" {
				hash = strings.Repeat("0", 64)
			}
			err := checkSourcePosition(t.Context(), source, head, 1, hash)
			if scenario == "exact" && err != nil || scenario == "behind" && !errors.Is(err, ErrReplicaReseed) || scenario != "exact" && scenario != "behind" && !errors.Is(err, ErrReplicationFork) {
				t.Fatal(scenario, err)
			}
		})
	}
}

func TestOnlineCompactionWorkerCancelsAndRejectsAliases(t *testing.T) {
	primary, _ := replicationPair(t)
	object := putText(t, primary, "key", "payload")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); primary.RunCompactor(ctx, onlineOptions(), nil) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		status, _ := primary.CompactionStatus()
		if status.Cycles > 0 {
			break
		}
		select {
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("worker did not reclaim")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
	putText(t, primary, "key", "payload")
	outside := filepath.Join(t.TempDir(), "alias")
	if err := os.Link(primary.replicationPath("blobs/"+object.SHA256+".data"), outside); err != nil {
		t.Fatal(err)
	}
	if _, err := primary.CompactStep(t.Context(), onlineOptions()); err != nil {
		t.Fatal(err)
	}
	if _, err := primary.CompactStep(t.Context(), onlineOptions()); !errors.Is(err, ErrCorrupt) {
		t.Fatal("alias accepted", err)
	}
}

func TestOnlineCompactionUncertainCheckpointDoesNotLoseNewWrites(t *testing.T) {
	for _, pruning := range []bool{true, false} {
		t.Run(fmt.Sprint(pruning), func(t *testing.T) {
			primary, _ := replicationPair(t)
			putText(t, primary, "key", "before")
			persist := primary.replicationPersist
			injected := errors.New("checkpoint acknowledgement lost")
			primary.replicationPersist = func(path string, value any) (bool, error) {
				published, err := persist(path, value)
				if state, ok := value.(replicationState); ok && state.Checkpoint != nil && state.Checkpoint.Pruning == pruning && err == nil {
					return published, injected
				}
				return published, err
			}
			if _, err := primary.CompactStep(t.Context(), onlineOptions()); !errors.Is(err, injected) {
				t.Fatal(err)
			}
			primary.replicationPersist = persist
			if _, err := primary.CompactStep(t.Context(), onlineOptions()); err != nil {
				t.Fatal("checkpoint could not be reconciled", err)
			}
			putText(t, primary, "key", "after")
			drainOnline(t, primary, onlineOptions())
			drainOnline(t, primary, onlineOptions())
			if primary.journalUsage.Records != 0 || primary.journalUsage.Blobs != 0 || readText(t, primary, "key") != "after" {
				t.Fatal(primary.journalUsage)
			}
			_ = primary.Close()
			primary = reopenCompactionPrimary(t, primary.root)
			if readText(t, primary, "key") != "after" {
				t.Fatal("new publication was rolled back")
			}
		})
	}
}

func TestOnlineCompactionBoundsMetadataReadsAndOptions(t *testing.T) {
	primary, _ := replicationPair(t)
	options := PutOptions{Headers: http.Header{"X-Amz-Meta-large": {strings.Repeat("x", 600<<10)}}}
	for _, key := range []string{"first", "second"} {
		if _, err := primary.Put(t.Context(), "test-bucket", key, strings.NewReader("x"), 1, options); err != nil {
			t.Fatal(err)
		}
	}
	o := onlineOptions()
	o.MaxRecords = 128
	status, err := primary.CompactStep(t.Context(), o)
	if err != nil || status.StepRecords != 1 {
		t.Fatal("metadata byte cap ignored", status, err)
	}
	for _, change := range []func(*CompactionOptions){func(o *CompactionOptions) { o.MaxRecords = 0 }, func(o *CompactionOptions) { o.MaxBlobs = 100000 }, func(o *CompactionOptions) { o.TriggerRecords = 0 }, func(o *CompactionOptions) { o.TriggerBlobBytes = -1 }, func(o *CompactionOptions) { o.MinFreeBytes = -1 }} {
		bad := o
		change(&bad)
		if _, err = primary.CompactStep(t.Context(), bad); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid work bounds accepted", bad, err)
		}
	}
}

func TestOnlineCompactionRetryUsesCurrentStepLimit(t *testing.T) {
	primary, _ := replicationPair(t)
	for n := range 4 {
		putText(t, primary, fmt.Sprint(n), "shared")
	}
	injected := errors.New("pause after floor")
	primary.compactionCheckpoint = func(event string) error {
		if event == "after checkpoint" {
			return injected
		}
		return nil
	}
	o := onlineOptions()
	o.MaxRecords = 4
	if _, err := primary.CompactStep(t.Context(), o); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	primary.compactionCheckpoint = nil
	o.MaxRecords = 1
	for range 4 {
		status, err := primary.CompactStep(t.Context(), o)
		if err != nil || status.StepRecords != 1 {
			t.Fatal("retry exceeded work bound", status, err)
		}
	}
	drainOnline(t, primary, o)
	if primary.journalUsage.Records != 0 || primary.journalUsage.Blobs != 0 {
		t.Fatal(primary.journalUsage)
	}
}
