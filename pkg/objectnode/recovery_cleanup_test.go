package objectnode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
)

func cleanupTestOptions(n int) RecoveryCleanupOptions {
	return RecoveryCleanupOptions{MaxRecords: n, StepTime: api.ObjectNodeRecoveryCleanupMaxStepTime}
}

func cleanupStep(t *testing.T, s *Store, n int) RecoveryCleanupStatus {
	t.Helper()
	status, err := s.RecoveryCleanupStep(t.Context(), cleanupTestOptions(n))
	if err != nil || !status.Enabled || !status.Ready || status.StepExamined > n {
		t.Fatal(status, err)
	}
	return status
}

func TestRecoveryCleanupIndexesOnlyAcknowledgedBoundedProofs(t *testing.T) {
	s, now := recoveryTestStore(t)
	old := recoveryPutSettlement(t, s, "history", "old", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, old)
	putText(t, s, old.Key, "replacement")
	live := recoveryPutSettlement(t, s, "live", "keep", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, live)
	unknown := recoveryPutSettlement(t, s, "unknown", "keep", objectrecovery.NewID())
	legacy := recoveryPutSettlement(t, s, "legacy", "keep", uuid.NewString())
	acknowledgeTestRecovery(t, s, legacy)
	active := recoveryMultipart(t, s)
	status, err := s.RecoveryCleanupStatus()
	if err != nil || status.Candidates != 2 {
		t.Fatal(status, err)
	}
	if p := cleanupStep(t, s, 8); p.StepExamined != 0 {
		t.Fatal("retention window bypassed", p)
	}
	*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
	for range 3 {
		cleanupStep(t, s, 1)
	}
	if _, err = s.Receipt(old.Bucket, old.Key, old.ID); !errors.Is(err, ErrNoReceipt) {
		t.Fatal("historical acknowledged proof survived", err)
	}
	for _, a := range []objectrecovery.Settlement{live, unknown, legacy} {
		if _, err = s.Receipt(a.Bucket, a.Key, a.ID); err != nil {
			t.Fatal("protected proof lost", a.Key, err)
		}
	}
	if _, err = s.MultipartInfo(active.Bucket, active.Key, active.ID); err != nil {
		t.Fatal("active session lost", err)
	}
	if readText(t, s, old.Key) != "replacement" {
		t.Fatal("current bytes changed")
	}
	if p := cleanupStep(t, s, 8); p.StepExamined != 0 || p.Candidates != 1 || p.Protected != 1 || p.ReceiptsRemoved != 1 {
		t.Fatal("protected candidate was not deferred", p)
	}
}

func TestRecoveryCleanupBoundedSeekIgnoresUnrelatedDirectoryEntries(t *testing.T) {
	s, now := recoveryTestStore(t)
	for n := range 20 {
		a := recoveryPutSettlement(t, s, "history", fmt.Sprint(n), objectrecovery.NewID())
		acknowledgeTestRecovery(t, s, a)
	}
	putText(t, s, "history", "live")
	*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
	// A directory scan would reject this unrelated entry. An indexed step
	// reads only its selected proof and current pointer.
	sentinel := filepath.Join(s.root, "receipts", "unrelated-scan-sentinel")
	if err := os.WriteFile(sentinel, []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(sentinel) }()
	for n := range 20 {
		p := cleanupStep(t, s, 1)
		if p.StepExamined != 1 || p.Candidates != int64(19-n) {
			t.Fatal("unbounded or missed indexed work", p)
		}
	}
	if p := cleanupStep(t, s, 1); p.StepExamined != 0 {
		t.Fatal(p)
	}
}

func TestRecoveryCleanupRechecksForgedStaleHints(t *testing.T) {
	s, now := recoveryTestStore(t)
	unack := recoveryPutSettlement(t, s, "unknown", "retain", objectrecovery.NewID())
	live := recoveryPutSettlement(t, s, "live", "retain", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, live)
	missing := objectrecovery.NewID()
	*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
	for _, id := range []string{unack.ID, live.ID, missing} {
		if err := s.cleanupIndex.put(id, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		cleanupStep(t, s, 1)
	}
	p, _ := s.RecoveryCleanupStatus()
	if p.Stale != 1 || p.Protected != 2 || p.ReceiptsRemoved != 0 {
		t.Fatal(p)
	}
	for _, a := range []objectrecovery.Settlement{unack, live} {
		if _, err := s.Receipt(a.Bucket, a.Key, a.ID); err != nil {
			t.Fatal("derived index authorized deletion", err)
		}
	}
	if _, err := s.Receipt("test-bucket", "missing", missing); !errors.Is(err, ErrNoReceipt) {
		t.Fatal("missing hint created proof", err)
	}
}

func TestRecoveryCleanupPersistsProtectedDeferralAndReplaysSourceGap(t *testing.T) {
	s, now := recoveryTestStore(t)
	a := recoveryPutSettlement(t, s, "live", "retain", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, a)
	*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
	p := cleanupStep(t, s, 1)
	if p.Protected != 1 || !p.NextDueAt.Equal(now.Add(api.ObjectNodeRecoveryCleanupProtected).Truncate(time.Millisecond)) {
		t.Fatal(p)
	}
	// An unrelated mutation advances the source watermark without rewriting
	// this candidate. Emulate process exit without the optional close flush.
	putText(t, s, "other", "journal entry")
	if err := s.cleanupIndex.db.Close(); err != nil {
		t.Fatal(err)
	}
	s.cleanupIndex = nil
	_ = s.Close()
	next, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	next.recoveryClock = func() time.Time { return *now }
	p = cleanupStep(t, next, 1)
	if p.StepExamined != 0 || p.IndexReplayed == 0 || p.IndexRebuilds != 0 {
		t.Fatal("restart lost durable deferral", p)
	}
	if err = next.Delete(a.Bucket, a.Key); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(api.ObjectNodeRecoveryCleanupProtected)
	p = cleanupStep(t, next, 1)
	if p.ReceiptsRemoved != 1 {
		t.Fatal("previously live proof never became collectible", p)
	}
}

func TestRecoveryCleanupSourceCommitSurvivesIndexFailure(t *testing.T) {
	s, now := recoveryTestStore(t)
	a := recoveryPutSettlement(t, s, "old", "body", objectrecovery.NewID())
	s.cleanupIndexFault = func() error { return errors.New("index I/O failed") }
	acknowledgeTestRecovery(t, s, a)
	if _, err := s.Put(t.Context(), "test-bucket", a.Key, strings.NewReader("replacement"), 11, PutOptions{}); err != nil {
		t.Fatal("index failure changed customer write outcome", err)
	}
	*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
	p, err := s.RecoveryCleanupStep(t.Context(), cleanupTestOptions(1))
	if !errors.Is(err, ErrRecoveryIndex) || p.Ready || !p.Deferred {
		t.Fatal(p, err)
	}
	_ = s.Close()
	next, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	next.recoveryClock = func() time.Time { return *now }
	if p = cleanupStep(t, next, 1); p.ReceiptsRemoved != 1 {
		t.Fatal("source replay failed to restore missing projection", p)
	}
}

func TestRecoveryCleanupRebuildsMissingUnfinishedAndForeignCheckpoints(t *testing.T) {
	for _, mode := range []string{"missing", "building", "foreign", "ahead"} {
		t.Run(mode, func(t *testing.T) {
			s, now := recoveryTestStore(t)
			a := recoveryPutSettlement(t, s, "history", "old", objectrecovery.NewID())
			acknowledgeTestRecovery(t, s, a)
			putText(t, s, a.Key, "replacement")
			i := s.cleanupIndex
			switch mode {
			case "building":
				i.position.Building = true
			case "foreign":
				i.position.Stream = uuid.NewString()
			case "ahead":
				i.position.Sequence += 100
			}
			if err := i.savePosition(); err != nil {
				t.Fatal(err)
			}
			_ = s.Close()
			if mode == "missing" {
				if err := os.Remove(filepath.Join(s.root, recoveryIndexName)); err != nil {
					t.Fatal(err)
				}
			}
			next, err := Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
			next.recoveryClock = func() time.Time { return *now }
			if p := cleanupStep(t, next, 1); p.IndexRebuilds != 1 || p.ReceiptsRemoved != 1 {
				t.Fatal(p)
			}
		})
	}
}

func TestRecoveryCleanupCorruptIndexPausesWithoutBlockingCatalog(t *testing.T) {
	s, now := recoveryTestStore(t)
	a := recoveryPutSettlement(t, s, "live", "survives", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, a)
	_ = s.Close()
	path := filepath.Join(s.root, recoveryIndexName)
	if err := os.WriteFile(path, []byte("invalid index"), 0o600); err != nil {
		t.Fatal(err)
	}
	next, err := Open(s.root)
	if err != nil {
		t.Fatal("derived file prevented serving sealed catalog", err)
	}
	defer next.Close()
	next.recoveryClock = func() time.Time { return *now }
	if readText(t, next, a.Key) != "survives" {
		t.Fatal("catalog changed")
	}
	if p, err := next.RecoveryCleanupStep(t.Context(), cleanupTestOptions(1)); !errors.Is(err, ErrRecoveryIndex) || p.Ready || !p.Deferred {
		t.Fatal(p, err)
	}
}

func TestRecoveryCleanupMultipartAndNonemptyPartsProtection(t *testing.T) {
	for _, terminal := range []string{"committed", "rejected", "aborted", "expired"} {
		t.Run(terminal, func(t *testing.T) {
			s, now := recoveryTestStore(t)
			u := prepareMultipartRetirement(t, s, terminal)
			*now = now.Add(api.ObjectNodeRecoveryWindow + 48*time.Hour)
			p := cleanupStep(t, s, 1)
			if p.UploadsRemoved != 1 || p.Candidates != 0 {
				t.Fatal(p)
			}
			if _, err := s.MultipartInfo(u.Bucket, u.Key, u.ID); !errors.Is(err, ErrNoUpload) {
				t.Fatal(err)
			}
		})
	}
	t.Run("parts pinned", func(t *testing.T) {
		s, now := recoveryTestStore(t)
		u := prepareMultipartRetirement(t, s, "committed")
		parts := filepath.Join(s.multipartPath(u.ID), "parts")
		if err := os.MkdirAll(parts, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parts, "retained"), []byte("bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(api.ObjectNodeRecoveryWindow + 48*time.Hour)
		if p := cleanupStep(t, s, 1); p.Protected != 1 || p.UploadsRemoved != 0 || p.ReceiptsRemoved != 0 {
			t.Fatal(p)
		}
		if _, err := s.readReceipt(u.ReceiptID); err != nil {
			t.Fatal("paired receipt retired while parts remained", err)
		}
	})
}

func TestRecoveryCleanupCancellationDeadlineAndConcurrentPublication(t *testing.T) {
	s, now := recoveryTestStore(t)
	for range 3 {
		a := recoveryPutSettlement(t, s, "history", "old", objectrecovery.NewID())
		acknowledgeTestRecovery(t, s, a)
	}
	putText(t, s, "history", "current")
	*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	s.cleanupCheckpoint = func(phase string) error {
		if phase != "identity finished" {
			return nil
		}
		// Publication obtains mu between identities while a step is active.
		putText(t, s, "concurrent", "write succeeded")
		cancel()
		return nil
	}
	p, err := s.RecoveryCleanupStep(ctx, cleanupTestOptions(3))
	if !errors.Is(err, context.Canceled) || p.StepExamined != 1 || p.ReceiptsRemoved != 1 {
		t.Fatal(p, err)
	}
	s.cleanupCheckpoint = nil
	o := cleanupTestOptions(3)
	o.StepTime = time.Nanosecond
	if p, err = s.RecoveryCleanupStep(t.Context(), o); err != nil || p.StepExamined != 1 {
		t.Fatal("soft deadline was not checked", p, err)
	}
	var writers sync.WaitGroup
	for range 4 {
		writers.Go(func() {
			for range 5 {
				if _, err := s.Put(t.Context(), "test-bucket", "concurrent", strings.NewReader("fresh"), 5, PutOptions{}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	cleanupStep(t, s, 1)
	writers.Wait()
	if readText(t, s, "concurrent") != "fresh" {
		t.Fatal("concurrent bytes lost")
	}
}

func TestRecoveryCleanupRebuildBatchesAndChurnRemainBounded(t *testing.T) {
	s, now := recoveryTestStore(t)
	const count = api.ObjectNodeRecoveryIndexBuildBatch + 1
	for range count {
		a := recoveryPutSettlement(t, s, "history", "old", objectrecovery.NewID())
		acknowledgeTestRecovery(t, s, a)
	}
	putText(t, s, "history", "current")
	_ = s.Close()
	if err := os.Remove(filepath.Join(s.root, recoveryIndexName)); err != nil {
		t.Fatal(err)
	}
	next, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
	next.recoveryClock = func() time.Time { return *now }
	var p RecoveryCleanupStatus
	for range count {
		p = cleanupStep(t, next, api.ObjectNodeRecoveryCleanupBatch)
		if p.Candidates == 0 {
			break
		}
	}
	if p.Candidates != 0 || p.IndexRebuilds != 1 || p.ReceiptsRemoved != count || p.Examined != count || readText(t, next, "history") != "current" {
		t.Fatal("bounded cleanup did not drain acknowledged churn", p)
	}
}

func TestRecoveryCleanupDoesNotOptInAndRejectsInvalidOrCancelledWork(t *testing.T) {
	s := testStore(t)
	p, err := s.RecoveryCleanupStep(t.Context(), DefaultRecoveryCleanupOptions())
	if err != nil || p.Enabled || p.Ready {
		t.Fatal(p, err)
	}
	if _, err = os.Stat(filepath.Join(s.root, recoveryIndexName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cleanup implicitly enabled retention", err)
	}
	for _, o := range []RecoveryCleanupOptions{{}, {MaxRecords: api.ObjectNodeRecoveryCleanupMaxBatch + 1, StepTime: time.Millisecond}, {MaxRecords: 1, StepTime: api.ObjectNodeRecoveryCleanupMaxStepTime + time.Nanosecond}} {
		if _, err = s.RecoveryCleanupStep(t.Context(), o); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid budget accepted", o, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = s.RecoveryCleanupStep(ctx, DefaultRecoveryCleanupOptions()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRecoveryCleanupIndexRejectsAliasesWithoutChangingTheirTargets(t *testing.T) {
	for _, alias := range []string{"symlink", "hardlink"} {
		t.Run(alias, func(t *testing.T) {
			s, _ := recoveryTestStore(t)
			_ = s.Close()
			path := filepath.Join(s.root, recoveryIndexName)
			target := filepath.Join(t.TempDir(), "outside-index")
			if err := os.Rename(path, target); err != nil {
				t.Fatal(err)
			}
			link := os.Symlink
			if alias == "hardlink" {
				link = os.Link
			}
			if err := link(target, path); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			next, err := Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			p, err := next.RecoveryCleanupStatus()
			if err != nil || p.Ready || !p.Deferred {
				t.Fatal("unsafe index alias accepted", p, err)
			}
			_ = next.Close()
			after, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("index alias target changed", err)
			}
		})
	}
}

func TestRecoveryCleanupBackupRestoreOmitsIndexAndStaysFollower(t *testing.T) {
	s, _ := recoveryTestStore(t)
	a := recoveryPutSettlement(t, s, "live", "bytes", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, a)
	_ = s.Close()
	backup, restored := filepath.Join(t.TempDir(), "backup"), filepath.Join(t.TempDir(), "restored")
	if _, err := Backup(t.Context(), s.root, backup); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := VerifyBackup(t.Context(), backup); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(backup, "data", recoveryIndexName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("verify created a local index", err)
		}
	}
	if _, err := RestoreBackup(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	if next, err := Open(restored); !errors.Is(err, ErrReplica) {
		if next != nil {
			_ = next.Close()
		}
		t.Fatal("restore became a serving primary", err)
	}
	if _, err := os.Lstat(filepath.Join(restored, recoveryIndexName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("non-serving follower built a primary cleanup index", err)
	}
	// Even a regular local database is not accepted as an extra backup entry.
	if err := os.WriteFile(filepath.Join(backup, "data", recoveryIndexName), []byte("unlisted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackup(t.Context(), backup); err == nil {
		t.Fatal("unlisted local projection accepted into immutable backup")
	}
}

func TestRecoveryCleanupDefersPendingPayloadPublication(t *testing.T) {
	s, now := recoveryTestStore(t)
	a := recoveryPutSettlement(t, s, "history", "old", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, a)
	putText(t, s, a.Key, "replacement")
	injected := errors.New("catalog publication unavailable")
	attempts := 0
	s.persist = func(path string, value any) (bool, error) {
		if path == filepath.Join(s.objectPath("test-bucket", "pending-publication"), "current.json") {
			attempts++
			return false, injected
		}
		return writeJSON(path, value)
	}
	if _, err := s.Put(t.Context(), "test-bucket", "pending-publication", strings.NewReader("new"), 3, PutOptions{}); !errors.Is(err, injected) {
		t.Fatal("failed to leave a pending durable write", err)
	}
	if attempts != 1 || s.replication.ReceivedSequence == s.replication.AppliedSequence {
		t.Fatal("no pending source publication", attempts, s.replication)
	}
	*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
	p, err := s.RecoveryCleanupStep(t.Context(), cleanupTestOptions(1))
	if !errors.Is(err, ErrRecoveryBusy) || p.StepExamined != 0 || !p.Deferred || !p.Ready || attempts != 1 {
		t.Fatal("cleanup attempted unbounded pending source replay", p, attempts, err)
	}
	if _, err = s.readReceipt(a.ID); err != nil {
		t.Fatal("cleanup changed source before recovery", err)
	}
	s.persist = writeJSON
	if err = s.finishPending(false); err != nil {
		t.Fatal(err)
	}
	if p = cleanupStep(t, s, 1); p.ReceiptsRemoved != 1 || p.Deferred || readText(t, s, "pending-publication") != "new" {
		t.Fatal("ordinary recovery failed to unblock cleanup", p)
	}
}

func TestRecoveryCleanupWorkerCollectsAndStopsOnCancellation(t *testing.T) {
	s, now := recoveryTestStore(t)
	a := recoveryPutSettlement(t, s, "history", "old", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, a)
	putText(t, s, a.Key, "replacement")
	*now = now.Add(api.ObjectNodeRecoveryWindow + time.Hour)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type observation struct {
		status RecoveryCleanupStatus
		err    error
	}
	observed := make(chan observation, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.RunRecoveryCleanup(ctx, DefaultRecoveryCleanupOptions(), func(status RecoveryCleanupStatus, err error) {
			observed <- observation{status, err}
			cancel()
		})
	}()
	select {
	case p := <-observed:
		if p.err != nil || p.status.ReceiptsRemoved != 1 || p.status.Candidates != 0 {
			t.Fatal("background worker did not collect", p)
		}
	case <-ctx.Done():
		t.Fatal("background worker did not run", ctx.Err())
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled worker did not stop")
	}
	if readText(t, s, a.Key) != "replacement" {
		t.Fatal("background worker changed live bytes")
	}
}
