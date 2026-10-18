package objectnode

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
)

func recoveryTestStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	s := testStore(t)
	_ = s.Close()
	if _, err := EnableReplication(t.Context(), s.root); err != nil {
		t.Fatal(err)
	}
	s, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now().UTC()
	s.recoveryClock = func() time.Time { return now }
	if _, err = s.EnableRecoveryRetention(); err != nil {
		t.Fatal(err)
	}
	return s, &now
}

func recoveryPutSettlement(t *testing.T, s *Store, key, body, id string) objectrecovery.Settlement {
	t.Helper()
	o, err := receiptPut(t, s, key, body, id, PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return objectrecovery.Settlement{ID: id, Bucket: "test-bucket", Key: key, Kind: "put", Status: ReceiptCommitted, Size: o.Size, ETag: o.ETag}
}

func acknowledgeTestRecovery(t *testing.T, s *Store, a objectrecovery.Settlement) objectrecovery.Acknowledgement {
	t.Helper()
	ack, err := s.AcknowledgeRecovery(a)
	if err != nil || ack.Version != 1 || ack.ID != a.ID || ack.Status != a.Status || ack.SettledAt.IsZero() {
		t.Fatal(ack, err)
	}
	return ack
}

func TestRecoveryCollectionRequiresExactSettlementAndPreservesPins(t *testing.T) {
	s, now := recoveryTestStore(t)
	id, unknown, live, legacy, rejected := objectrecovery.NewID(), objectrecovery.NewID(), objectrecovery.NewID(), uuid.NewString(), objectrecovery.NewID()
	a := recoveryPutSettlement(t, s, "history", "original", id)
	for name, mutate := range map[string]func(*objectrecovery.Settlement){
		"key":      func(a *objectrecovery.Settlement) { a.Key = "other" },
		"size":     func(a *objectrecovery.Settlement) { a.Size++ },
		"etag":     func(a *objectrecovery.Settlement) { a.ETag = `"other"` },
		"status":   func(a *objectrecovery.Settlement) { a.Status = ReceiptRejected },
		"kind":     func(a *objectrecovery.Settlement) { a.Kind = "multipart" },
		"manifest": func(a *objectrecovery.Settlement) { a.ManifestHash = nameHash("other") },
	} {
		t.Run(name, func(t *testing.T) {
			bad := a
			mutate(&bad)
			if _, err := s.AcknowledgeRecovery(bad); !errors.Is(err, ErrReceiptReuse) {
				t.Fatal("mismatched settlement acknowledged", err)
			}
		})
	}
	if r, err := s.readReceipt(id); err != nil || r.SettledAt != nil {
		t.Fatal("invalid acknowledgement changed proof", r, err)
	}
	first := acknowledgeTestRecovery(t, s, a)
	*now = now.Add(24 * time.Hour)
	if again := acknowledgeTestRecovery(t, s, a); !again.SettledAt.Equal(first.SettledAt) {
		t.Fatal("lost reply retry extended retention", first, again)
	}
	putText(t, s, "history", "replacement")
	_ = recoveryPutSettlement(t, s, "unknown", "unknown", unknown)
	liveAck := recoveryPutSettlement(t, s, "live", "live", live)
	acknowledgeTestRecovery(t, s, liveAck)
	legacyAck := recoveryPutSettlement(t, s, "legacy", "legacy", legacy)
	acknowledgeTestRecovery(t, s, legacyAck)
	putText(t, s, "legacy", "later")
	if _, err := receiptPut(t, s, "history", "rejected", rejected, PutOptions{IfNoneMatch: "*"}); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
	negative := objectrecovery.Settlement{ID: rejected, Bucket: "test-bucket", Key: "history", Kind: "put", Status: ReceiptRejected, Size: 8}
	acknowledgeTestRecovery(t, s, negative)
	missing := a
	missing.ID = objectrecovery.NewID()
	if _, err := s.AcknowledgeRecovery(missing); !errors.Is(err, ErrNoReceipt) {
		t.Fatal("absence acknowledged", err)
	}
	*now = now.Add(api.ObjectNodeRecoveryWindow - time.Hour)
	page, err := s.CollectRecovery(t.Context(), "", api.ObjectNodeRecoveryBatch)
	if err != nil || page.ReceiptsRemoved != 1 { // Only the first acknowledgement is old enough.
		t.Fatal(page, err)
	}
	*now = now.Add(2 * time.Hour)
	page, err = s.CollectRecovery(t.Context(), "", api.ObjectNodeRecoveryBatch)
	if err != nil || page.ReceiptsRemoved != 1 || page.UploadsRemoved != 0 || !page.Done {
		t.Fatal(page, err)
	}
	for _, id := range []string{unknown, live, legacy} {
		if _, err := s.readReceipt(id); err != nil {
			t.Fatal("required proof collected", id, err)
		}
	}
	for _, id := range []string{id, rejected, missing.ID} {
		if _, err := s.Receipt("test-bucket", "history", id); !errors.Is(err, ErrNoReceipt) {
			t.Fatal("missing proof changed meaning", err)
		}
	}
	if _, err = receiptPut(t, s, "history", "original", a.ID, PutOptions{}); !errors.Is(err, ErrReceiptExpired) || readText(t, s, "history") != "replacement" {
		t.Fatal("collected identity changed current bytes", err)
	}
	if _, err = receiptPut(t, s, "live", "live", live, PutOptions{}); err != nil {
		t.Fatal("expired existing proof stopped replay", err)
	}
	if err = s.Delete("test-bucket", "history"); err != nil {
		t.Fatal(err)
	}
	*now = first.SettledAt // Simulate clock rollback after a persisted retirement.
	if _, err = receiptPut(t, s, "history", "original", a.ID, PutOptions{}); !errors.Is(err, ErrReceiptExpired) {
		t.Fatal("clock rollback reopened a retired identity", err)
	}
}

func recoveryMultipart(t *testing.T, s *Store) MultipartUpload {
	t.Helper()
	u, err := s.CreateMultipart("test-bucket", "multipart", MultipartOptions{Headers: http.Header{MultipartMetadataHeader: {objectrecovery.NewID()}}, Checksum: objectchecksum.Policy{}})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func prepareMultipartRetirement(t *testing.T, s *Store, status string) MultipartUpload {
	t.Helper()
	u := recoveryMultipart(t, s)
	a := objectrecovery.Settlement{ID: u.ReceiptID, Bucket: u.Bucket, Key: u.Key, Kind: "multipart"}
	if status == "aborted" || status == "expired" {
		multipartPart(t, s, u, 1, []byte("part"))
		if status == "aborted" {
			if err := s.AbortMultipart(u.Bucket, u.Key, u.ID); err != nil {
				t.Fatal(err)
			}
		} else if _, err := s.ExpireMultipart(t.Context(), u.ExpiresAt.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		a.Status = "aborted"
	} else {
		part := multipartPart(t, s, u, 1, []byte("part"))
		request := completion(part)
		if status == "rejected" {
			putText(t, s, u.Key, "replacement")
			request.IfNoneMatch = "*"
		}
		_, err := s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, request, nil)
		if status == "rejected" && !errors.Is(err, ErrPrecondition) || status != "rejected" && err != nil {
			t.Fatal(err)
		}
		r, err := s.readReceipt(u.ReceiptID)
		if err != nil {
			t.Fatal(err)
		}
		a.Status, a.Size, a.ManifestHash = r.Status, r.Object.Size, r.ManifestHash
		if r.Status == ReceiptCommitted {
			a.ETag = r.Object.ETag
			if err = s.Delete(u.Bucket, u.Key); err != nil {
				t.Fatal(err)
			}
		}
	}
	acknowledgeTestRecovery(t, s, a)
	return u
}

func TestRecoveryMultipartTerminalCleanupCannotResurrectSessions(t *testing.T) {
	for _, status := range []string{"committed", "rejected", "aborted", "expired"} {
		t.Run(status, func(t *testing.T) {
			s, now := recoveryTestStore(t)
			u := prepareMultipartRetirement(t, s, status)
			active := recoveryMultipart(t, s)
			*now = now.Add(api.ObjectNodeRecoveryWindow + 48*time.Hour)
			page, err := s.CollectRecovery(t.Context(), "", api.ObjectNodeRecoveryBatch)
			if err != nil || page.UploadsRemoved != 1 || page.ReceiptsRemoved != map[bool]int{true: 1}[status == "committed" || status == "rejected"] {
				t.Fatal(page, err)
			}
			if _, err = s.MultipartInfo(active.Bucket, active.Key, active.ID); err != nil {
				t.Fatal("unacknowledged active upload collected", err)
			}
			if _, err = s.MultipartInfo(u.Bucket, u.Key, u.ID); !errors.Is(err, ErrNoUpload) {
				t.Fatal(err)
			}
			if _, err = s.CreateMultipart(u.Bucket, u.Key, MultipartOptions{Headers: u.Headers, Checksum: u.Checksum, MaxBytes: u.MaxBytes}); !errors.Is(err, ErrReceiptExpired) {
				t.Fatal("retired session was recreated", err)
			}
		})
	}
}

func TestRecoveryCollectionPagingAndCancellation(t *testing.T) {
	s, now := recoveryTestStore(t)
	for range 7 {
		id := objectrecovery.NewID()
		a := recoveryPutSettlement(t, s, "key", "value", id)
		acknowledgeTestRecovery(t, s, a)
	}
	*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := s.replication.AppliedSequence
	if _, err := s.CollectRecovery(ctx, "", 2); !errors.Is(err, context.Canceled) || s.replication.AppliedSequence != before {
		t.Fatal("cancelled collection changed the journal", err)
	}
	cursor, examined, removed := "", 0, 0
	for range 5 {
		page, err := s.CollectRecovery(t.Context(), cursor, 2)
		if err != nil || page.Examined > 2 || !page.Done && page.NextCursor <= cursor {
			t.Fatal(page, err)
		}
		examined, removed = examined+page.Examined, removed+page.ReceiptsRemoved
		if page.Done {
			break
		}
		cursor = page.NextCursor
	}
	if examined != 7 || removed != 6 {
		t.Fatal("page skipped a record or deleted the live proof", examined, removed)
	}
	for _, limit := range []int{0, api.ObjectNodeRecoveryMaxBatch + 1} {
		if _, err := s.CollectRecovery(t.Context(), "", limit); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
}

func TestRecoveryRetentionBackupPreservesBarrierAndRequiredProofs(t *testing.T) {
	s, now := recoveryTestStore(t)
	a := recoveryPutSettlement(t, s, "gone", "value", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, a)
	if err := s.Delete(a.Bucket, a.Key); err != nil {
		t.Fatal(err)
	}
	live := recoveryPutSettlement(t, s, "live", "survives", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, live)
	*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
	if _, err := s.CollectRecovery(t.Context(), "", api.ObjectNodeRecoveryBatch); err != nil {
		t.Fatal(err)
	}
	want, err := s.RecoveryRetentionStatus()
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err = CompactJournal(t.Context(), s.root, -1); err != nil {
		t.Fatal(err)
	}
	backup, destination := filepath.Join(t.TempDir(), "backup"), filepath.Join(t.TempDir(), "replica")
	info, err := Backup(t.Context(), s.root, backup)
	if err != nil || info.FormatVersion != 5 {
		t.Fatal(info, err)
	}
	if _, err = VerifyBackup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	if _, err = RestoreBackup(t.Context(), backup, destination); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(destination); !errors.Is(err, ErrReplica) {
		t.Fatal("restored backup gained write authority", err)
	}
	f, err := OpenFollower(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := f.store.RecoveryRetentionStatus()
	if err != nil || !got.RetiredBefore.Equal(want.RetiredBefore) {
		t.Fatal("restored barrier lost", got, err)
	}
	if _, err = f.store.readReceipt(a.ID); !errors.Is(err, ErrNoReceipt) {
		t.Fatal("retired proof returned", err)
	}
	if proof, err := f.store.readReceipt(live.ID); err != nil || proof.SettledAt == nil {
		t.Fatal("required proof lost", proof, err)
	}
	if err = f.store.checkNewReceiptID(a.ID); !errors.Is(err, ErrReceiptExpired) {
		t.Fatal("restore reopened an expired ID", err)
	}
}

func TestRecoveryOptInAndCorruptBarrierFailClosed(t *testing.T) {
	s := testStore(t)
	if _, err := s.EnableRecoveryRetention(); !errors.Is(err, ErrReplicationOff) {
		t.Fatal(err)
	}
	s, now := recoveryTestStore(t)
	if err := s.checkNewReceiptID(objectrecovery.NewID()); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
	if err := s.checkNewReceiptID(objectrecovery.NewID()); !errors.Is(err, ErrReceiptExpired) {
		t.Fatal("absent ID first-use window extended", err)
	}
	_ = s.Close()
	if err := os.WriteFile(filepath.Join(s.root, "recovery.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(s.root); !errors.Is(err, ErrCorrupt) {
		t.Fatal("corrupt retention barrier ignored", err)
	}
}

func TestRecoveryMissingBarrierCannotReopenRetiredIdentities(t *testing.T) {
	s, now := recoveryTestStore(t)
	a := recoveryPutSettlement(t, s, "gone", "body", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, a)
	if err := s.Delete(a.Bucket, a.Key); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
	if _, err := s.CollectRecovery(t.Context(), "", api.ObjectNodeRecoveryBatch); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.root, "recovery.json")); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(-api.ObjectNodeRecoveryWindow)
	if err := s.checkNewReceiptID(a.ID); !errors.Is(err, ErrCorrupt) {
		t.Fatal("missing barrier reopened a collected identity", err)
	}
	_ = s.Close()
	if _, err := Open(s.root); !errors.Is(err, ErrCorrupt) {
		t.Fatal("startup ignored a lost barrier", err)
	}
}

func TestRecoveryInterruptedInitialUpgradeReplaysBeforeBarrierValidation(t *testing.T) {
	s := testStore(t)
	_ = s.Close()
	if _, err := EnableReplication(t.Context(), s.root); err != nil {
		t.Fatal(err)
	}
	s, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("interrupted after format publication")
	persist := s.persist
	s.persist = func(path string, value any) (bool, error) {
		if filepath.Base(path) == "recovery.json" {
			return false, injected
		}
		return persist(path, value)
	}
	if _, err = s.EnableRecoveryRetention(); !errors.Is(err, injected) {
		t.Fatal("upgrade missed interruption", err)
	}
	if version, e := nodeFormatVersion(s.root); e != nil || version != 5 {
		t.Fatal(version, e)
	}
	_ = s.Close()
	s, err = Open(s.root)
	if err != nil {
		t.Fatal("valid pending opt-in could not resume", err)
	}
	defer s.Close()
	if _, err = s.RecoveryRetentionStatus(); err != nil {
		t.Fatal("resumed upgrade omitted its barrier", err)
	}
}
