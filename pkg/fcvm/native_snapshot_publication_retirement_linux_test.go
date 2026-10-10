//go:build linux

// adr: 568 — retirement is exact, idempotent and permanently closes restore.
package fcvm

import (
	"context"
	"errors"
	"io"
	"sort"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

type nativeArtifactRetirementStore struct {
	retiredKeys map[string]bool
	calls       int
	failAt      int
}

func (*nativeArtifactRetirementStore) Put(context.Context, string, io.Reader) error {
	return errors.New("unexpected put")
}
func (*nativeArtifactRetirementStore) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("unexpected get")
}
func (*nativeArtifactRetirementStore) Delete(context.Context, string) error {
	return errors.New("unexpected delete")
}
func (s *nativeArtifactRetirementStore) RetireExclusiveArtifact(ctx context.Context, receipt storage.ExclusiveArtifactReceipt) error {
	s.calls++
	if err := errors.Join(receipt.Validate(), ctx.Err()); err != nil {
		return err
	}
	if s.failAt != 0 && s.calls == s.failAt {
		return errors.New("retirement acknowledgement lost")
	}
	if s.retiredKeys == nil {
		s.retiredKeys = map[string]bool{}
	}
	s.retiredKeys[receipt.Key] = true
	return ctx.Err()
}

func TestNativeSnapshotArtifactRetirementIsConditionalAndClosesRestore(t *testing.T) {
	_, journal, intent := nativePublicationDiskFixture(t)
	intent, err := journal.Begin(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range [...]string{"mem", "vmstate", "drive", "backing"} {
		body := []byte("original-" + kind)
		if _, err := journal.RecordObject(t.Context(), intent, kind, nativeModeledArtifactReceipt(nativePublicationObjectKey(intent, kind), body)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := journal.ReadRestoreCohort(t.Context(), intent.Capture.CaptureID); err != nil {
		t.Fatal("published capture was not readable before retirement", err)
	}
	backend := &nativeArtifactRetirementStore{failAt: 3}
	if err := journal.RetireRestoreCohort(t.Context(), intent.Capture.CaptureID, backend); err == nil {
		t.Fatal("partial backend retirement was acknowledged")
	}
	if len(backend.retiredKeys) != 2 {
		t.Fatalf("partial retirement effects differ: %+v", backend.retiredKeys)
	}
	if _, err := journal.ReadRestoreCohort(t.Context(), intent.Capture.CaptureID); err == nil {
		t.Fatal("partially retired capture remained eligible for restore")
	}
	backend.failAt = 0
	if err := journal.RetireRestoreCohort(t.Context(), intent.Capture.CaptureID, backend); err != nil {
		t.Fatal("receipt-conditional retry did not complete", err)
	}
	if len(backend.retiredKeys) != 4 {
		t.Fatalf("not all capture objects retired: %+v", backend.retiredKeys)
	}
	if _, err := journal.ReadRestoreCohort(t.Context(), intent.Capture.CaptureID); err == nil {
		t.Fatal("retired capture remained eligible for restore")
	}
	calls := backend.calls
	if err := journal.RetireRestoreCohort(t.Context(), intent.Capture.CaptureID, backend); err != nil || backend.calls != calls {
		t.Fatalf("retirement tombstone did not make retries idempotent: calls=%d err=%v", backend.calls, err)
	}
	if err := journal.owner.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := newNativeSnapshotPublicationJournal(journal.root, journal.base, "").(*linuxNativeSnapshotPublicationJournal)
	if err := restarted.Acquire(t.Context()); err != nil {
		t.Fatal("retirement tombstone did not survive restart", err)
	}
	defer func() { _ = restarted.owner.Close() }()
	if _, err := restarted.ReadRestoreCohort(t.Context(), intent.Capture.CaptureID); err == nil {
		t.Fatal("restart inventory resurrected retired capture")
	}
	if err := restarted.RetireRestoreCohort(t.Context(), intent.Capture.CaptureID, backend); err != nil {
		t.Fatal("restarted retirement is not idempotent", err)
	}
}

func TestNativeSnapshotArtifactRetirementRequiresCompleteOriginalReceipts(t *testing.T) {
	_, journal, intent := nativePublicationDiskFixture(t)
	intent, err := journal.Begin(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.RecordObject(t.Context(), intent, "mem", nativeModeledArtifactReceipt(intent.Keys.Memory, []byte("original"))); err != nil {
		t.Fatal(err)
	}
	backend := &nativeArtifactRetirementStore{}
	if err := journal.RetireRestoreCohort(t.Context(), intent.Capture.CaptureID, backend); err == nil || backend.calls != 0 {
		t.Fatalf("incomplete receipt cohort authorized deletion: calls=%d err=%v", backend.calls, err)
	}
	journal.mu.Lock()
	_, receiptErr := journal.readObjectLocked(t.Context(), intent, "mem")
	journal.mu.Unlock()
	if receiptErr != nil {
		t.Fatalf("failed retirement removed the original receipt: %v", receiptErr)
	}
}

func TestNativeSnapshotArtifactRetirementPreflightsBackendBeforeMarker(t *testing.T) {
	_, journal, intent := nativePublicationDiskFixture(t)
	intent, err := journal.Begin(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range [...]string{"mem", "vmstate", "drive", "backing"} {
		body := []byte("original-" + kind)
		if _, err := journal.RecordObject(t.Context(), intent, kind, nativeModeledArtifactReceipt(nativePublicationObjectKey(intent, kind), body)); err != nil {
			t.Fatal(err)
		}
	}
	backend := &memStorage{blobs: make(map[string][]byte)}
	if err := journal.RetireRestoreCohort(t.Context(), intent.Capture.CaptureID, backend); !errors.Is(err, storage.ErrExclusiveRetireUnsupported) {
		t.Fatalf("unsupported backend passed retirement preflight: %v", err)
	}
	if _, err := journal.ReadRestoreCohort(t.Context(), intent.Capture.CaptureID); err != nil {
		t.Fatalf("unsupported backend left a retirement marker or blocked restore: %v", err)
	}
}

func TestNativeSnapshotArtifactRetirementRecoversAfterJournalRestart(t *testing.T) {
	_, journal, intent := nativePublicationDiskFixture(t)
	intent, err := journal.Begin(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range [...]string{"mem", "vmstate", "drive", "backing"} {
		body := []byte("original-" + kind)
		if _, err := journal.RecordObject(t.Context(), intent, kind, nativeModeledArtifactReceipt(nativePublicationObjectKey(intent, kind), body)); err != nil {
			t.Fatal(err)
		}
	}
	backend := &nativeArtifactRetirementStore{failAt: 2}
	if err := journal.RetireRestoreCohort(t.Context(), intent.Capture.CaptureID, backend); err == nil {
		t.Fatal("partial retirement unexpectedly succeeded")
	}
	if err := journal.owner.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := newNativeSnapshotPublicationJournal(journal.root, journal.base, "").(*linuxNativeSnapshotPublicationJournal)
	if err := restarted.Acquire(t.Context()); err != nil {
		t.Fatal("restart did not validate the in-progress retirement record", err)
	}
	defer func() { _ = restarted.owner.Close() }()
	backend.failAt = 0
	if err := restarted.RecoverPendingRetirements(t.Context(), backend); err != nil {
		t.Fatal("restart did not finish the receipt-bound deletes", err)
	}
	if len(backend.retiredKeys) != 4 {
		t.Fatalf("restart recovery missed original artifacts: %+v", backend.retiredKeys)
	}
	if _, err := restarted.ReadRestoreCohort(t.Context(), intent.Capture.CaptureID); err == nil {
		t.Fatal("restart recovery left the capture restorable")
	}
	calls := backend.calls
	if err := restarted.RecoverPendingRetirements(t.Context(), backend); err != nil || backend.calls != calls {
		t.Fatalf("completed retirement was rediscovered: calls=%d err=%v", backend.calls, err)
	}
}

func TestNativeSnapshotArtifactRetirementPageAdvancesPastFailureAndWraps(t *testing.T) {
	_, journal, intent := nativePublicationDiskFixture(t)
	second := nativeCaptureOutputsFixture(t)
	intents := []nativeSnapshotPublicationIntent{intent, {
		Version: 1, JailBase: journal.base, Incoming: second.incoming, Capture: second.capture,
		Physical: second.owner, Keys: nativeSnapshotIntentKeys(second.incoming),
	}}
	backend := &nativeArtifactRetirementStore{}
	var captureIDs []string
	for i, candidate := range intents {
		stored, err := journal.Begin(t.Context(), candidate)
		if err != nil {
			t.Fatalf("begin capture %d: %v", i, err)
		}
		for _, kind := range [...]string{"mem", "vmstate", "drive", "backing"} {
			body := []byte("original-" + kind + "-" + stored.Capture.CaptureID)
			if _, err := journal.RecordObject(t.Context(), stored, kind,
				nativeModeledArtifactReceipt(nativePublicationObjectKey(stored, kind), body)); err != nil {
				t.Fatalf("record capture %d object %s: %v", i, kind, err)
			}
		}
		captureIDs = append(captureIDs, stored.Capture.CaptureID)
		backend.failAt = backend.calls + 1
		if err := journal.RetireRestoreCohort(t.Context(), stored.Capture.CaptureID, backend); err == nil {
			t.Fatalf("capture %s did not retain a pending marker after the simulated delete failure", stored.Capture.CaptureID)
		}
	}
	sort.Strings(captureIDs)
	backend.failAt = backend.calls + 1

	first, err := journal.RecoverPendingRetirementsPage(t.Context(), backend, "", 1)
	if err == nil || first.Examined != 1 || first.NextCursor != captureIDs[0] || !first.More {
		t.Fatalf("first page did not advance after a failed retirement: page=%+v err=%v", first, err)
	}
	secondPage, err := journal.RecoverPendingRetirementsPage(t.Context(), backend, first.NextCursor, 1)
	if err != nil || secondPage.Examined != 1 || secondPage.NextCursor != captureIDs[1] || secondPage.More {
		t.Fatalf("later pending capture was starved by the earlier failure: page=%+v err=%v", secondPage, err)
	}
	backend.failAt = 0
	wrapped, err := journal.RecoverPendingRetirementsPage(t.Context(), backend, "", 1)
	if err != nil || wrapped.Examined != 1 || wrapped.NextCursor != captureIDs[0] || wrapped.More {
		t.Fatalf("wrapped retry did not revisit the earlier failed record: page=%+v err=%v", wrapped, err)
	}
	if len(backend.retiredKeys) != 8 {
		t.Fatalf("wrapped recovery retired %d object keys, want 8", len(backend.retiredKeys))
	}
}
