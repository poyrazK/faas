// adr: 644
package managedpostgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type receiptRestoreProvider struct {
	fakeProvider
	creates, recovers, ownedDeletes int
	cancel                          func()
}

func TestCreationReceiptReplayRetainsCleanupCheckpoint(t *testing.T) {
	store := NewMemoryStore()
	at := time.Now().UTC().Truncate(time.Microsecond)
	receipt := CreationReceipt{Kind: "snapshot", ResourceID: "snapshot-operation", AccountID: "account", BackendID: "primary-a",
		BackendFingerprint: strings.Repeat("a", 64), Generation: 1, PointInTime: at.Add(-time.Minute),
		Acknowledgement: CreationAcknowledgement{ProviderResourceID: "project/snapshots/accepted", SourceResourceID: "project/source", CreatedAt: at}}
	if err := store.RecordCreationReceipt(t.Context(), receipt, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCreationCleanup(t.Context(), receipt, ""); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := store.GetCreationReceipt(t.Context(), receipt.Kind, receipt.BackendID, receipt.ResourceID)
	if err != nil || checkpoint.CleanupStartedAt.IsZero() {
		t.Fatalf("checkpoint: %+v %v", checkpoint, err)
	}
	if err := store.RecordCreationReceipt(t.Context(), receipt, ""); err != nil {
		t.Fatal(err)
	}
	replayed, err := store.GetCreationReceipt(t.Context(), receipt.Kind, receipt.BackendID, receipt.ResourceID)
	if err != nil || !replayed.CleanupStartedAt.Equal(checkpoint.CleanupStartedAt) {
		t.Fatalf("replay reset cleanup authority: %+v %v", replayed, err)
	}
}

func (p *receiptRestoreProvider) RestoreWithCreationReceipt(ctx context.Context, r RestoreRequest, expected *CreationAcknowledgement, record CreationRecorder) (ObservedDatabase, error) {
	if expected == nil {
		p.creates++
		a := CreationAcknowledgement{ProviderResourceID: "created-target", SourceResourceID: r.SourceResourceID, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
		if p.cancel != nil {
			p.cancel()
		}
		if err := record(ctx, a); err != nil {
			return ObservedDatabase{}, err
		}
	} else {
		p.recovers++
		if expected.ProviderResourceID != "created-target" {
			return ObservedDatabase{}, ErrConflict
		}
	}
	return ObservedDatabase{}, ErrConflict // upstream accepted the fork but cannot prove its point
}
func (p *receiptRestoreProvider) DeleteRestoreCreation(ctx context.Context, r RestoreRequest, a CreationAcknowledgement, cleanup CreationCleanup) (DeleteResult, error) {
	if !cleanup.Started {
		if err := cleanup.RecordStarted(ctx); err != nil {
			return DeleteResult{}, err
		}
	}
	p.ownedDeletes++
	if a.ProviderResourceID != "created-target" || a.SourceResourceID != r.SourceResourceID {
		return DeleteResult{}, ErrConflict
	}
	return DeleteResult{Done: true}, nil
}

// Regression: correctness errors discarded an accepted physical identity.
func TestRestoreCreationJournalSurvivesCancellationAndVerificationFailure(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "lineage", true: "cancelled"}[cancelled], func(t *testing.T) {
			p := &receiptRestoreProvider{fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}}
			store := NewMemoryStore()
			s, err := NewService(testRegistry(t, p, nil), store, ServiceOptions{ProvisioningEnabled: func() bool { return true }})
			if err != nil {
				t.Fatal(err)
			}
			source, err := s.Create(t.Context(), CreateRequest{AccountID: "account", Name: "source", Spec: testSpec()})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelled {
				p.cancel = cancel
			}
			_, err = s.Restore(ctx, RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "target", PointInTime: time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("restore: %v", err)
			}
			target, err := store.FindByName(t.Context(), source.AccountID, "target")
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := store.GetCreationReceipt(t.Context(), "restore", target.BackendID, target.ID)
			if err != nil || receipt.Acknowledgement.ProviderResourceID != "created-target" || target.ProviderResourceID != "" || target.State == StateReady || !target.AccountingRequired {
				t.Fatalf("unverified target: %+v receipt=%+v err=%v", target, receipt, err)
			}
			p.cancel = nil
			s.now = func() time.Time { return target.RetryAt.Add(time.Second) }
			_, err = s.Reconcile(t.Context(), source.AccountID, target.ID)
			if !errors.Is(err, ErrConflict) || p.creates != 1 || p.recovers != 1 {
				t.Fatalf("recovery: %v creates=%d recovers=%d", err, p.creates, p.recovers)
			}
			s.provisioningEnabled = func() bool { return false }
			deleted, err := s.Delete(t.Context(), source.AccountID, target.ID)
			if err != nil || deleted.State != StateDeleted || deleted.ProviderResourceID != "created-target" || !deleted.AccountingRequired || p.ownedDeletes != 1 || p.deleteCalls != 0 {
				t.Fatalf("cleanup: %+v %v owned=%d generic=%d", deleted, err, p.ownedDeletes, p.deleteCalls)
			}
			unchanged, err := store.Get(t.Context(), source.AccountID, source.ID)
			if err != nil || unchanged.State != StateReady {
				t.Fatalf("source changed: %+v %v", unchanged, err)
			}
		})
	}
}

type receiptSnapshotProvider struct {
	serviceSnapshotProvider
	creates, recovers, ownedDeletes int
}

func (p *receiptSnapshotProvider) CaptureSnapshotWithCreationReceipt(ctx context.Context, r SnapshotCaptureRequest, a *CreationAcknowledgement, record CreationRecorder) (DatabaseSnapshot, error) {
	if a != nil {
		p.recovers++
		return DatabaseSnapshot{}, ErrUnavailable
	}
	p.creates++
	if err := record(ctx, CreationAcknowledgement{ProviderResourceID: "project/snapshots/accepted", SourceResourceID: r.SourceResourceID, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}); err != nil {
		return DatabaseSnapshot{}, err
	}
	return DatabaseSnapshot{}, ErrUnavailable
}
func (p *receiptSnapshotProvider) ObserveSnapshotCreation(context.Context, SnapshotCaptureRequest, CreationAcknowledgement) (DatabaseSnapshot, error) {
	p.recovers++
	return DatabaseSnapshot{}, ErrUnavailable
}
func (p *receiptSnapshotProvider) DeleteSnapshotCreation(ctx context.Context, r SnapshotCaptureRequest, a CreationAcknowledgement, cleanup CreationCleanup) (DeleteResult, error) {
	if !cleanup.Started {
		if err := cleanup.RecordStarted(ctx); err != nil {
			return DeleteResult{}, err
		}
	}
	p.ownedDeletes++
	if a.ProviderResourceID != "project/snapshots/accepted" || a.SourceResourceID != r.SourceResourceID {
		return DeleteResult{}, ErrConflict
	}
	return DeleteResult{Done: true}, nil
}

func TestSnapshotCreationJournalPinsRecoveryAndCleanup(t *testing.T) {
	p := &receiptSnapshotProvider{serviceSnapshotProvider: serviceSnapshotProvider{fakeProvider: fakeProvider{capabilities: testCapabilities()}}}
	s := testService(t, testRegistry(t, p, nil), NewMemoryStore())
	b, _ := s.registry.Default(testSpec().Region)
	d := RestoreSourceDefinition{Spec: testSpec(), BackendID: b.ID, BackendFingerprint: b.Fingerprint, DataResourceID: "project/branch"}
	r := SnapshotCaptureRequest{ResourceID: "operation-owner", SourceResourceID: d.DataResourceID, PointInTime: time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour), IdempotencyKey: "capture"}
	_, err := s.CaptureSnapshot(t.Context(), "account", d, r)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	_, err = s.CaptureSnapshot(t.Context(), "account", d, r)
	if !errors.Is(err, ErrUnavailable) || p.creates != 1 {
		t.Fatalf("repeat: %v creates=%d", err, p.creates)
	}
	_, err = s.FindSnapshot(t.Context(), d, r)
	if !errors.Is(err, ErrUnavailable) || p.finds != 0 {
		t.Fatalf("discovery replaced accepted identity: %v finds=%d", err, p.finds)
	}
	s.provisioningEnabled = func() bool { return false }
	result, err := s.DeleteSnapshot(t.Context(), d, r, "")
	if err != nil || !result.Done || p.ownedDeletes != 1 || p.deletes != 0 {
		t.Fatalf("cleanup: %+v %v", result, err)
	}
	if _, err := s.DeleteSnapshot(t.Context(), d, r, "project/snapshots/foreign"); !errors.Is(err, ErrConflict) || p.ownedDeletes != 1 {
		t.Fatalf("foreign ID: %v", err)
	}
	changed := r
	changed.PointInTime = changed.PointInTime.Add(time.Second)
	if _, err := s.DeleteSnapshot(t.Context(), d, changed, ""); !errors.Is(err, ErrConflict) || p.ownedDeletes != 1 {
		t.Fatalf("changed intent: %v", err)
	}
}

type lineageObservationProvider struct {
	fakeProvider
	fault string
}

func (p *lineageObservationProvider) Inspect(ctx context.Context, id string) (ObservedDatabase, error) {
	observed, err := p.fakeProvider.Inspect(ctx, id)
	if observed.RestoreLineage != nil {
		switch p.fault {
		case "missing":
			observed.RestoreLineage = nil
		case "source":
			observed.RestoreLineage.SourceResourceID = "foreign-source"
		case "point":
			observed.RestoreLineage.PointInTime = observed.RestoreLineage.PointInTime.Add(time.Second)
		}
	}
	return observed, err
}

// Regression: ordinary restores could become ready after Inspect returned
// absent or contradictory lineage, while only clone restores were fenced.
func TestCustomerRestoreRevalidatesLineageBeforeReadiness(t *testing.T) {
	for _, fault := range []string{"missing", "source", "point", "valid"} {
		t.Run(fault, func(t *testing.T) {
			p := &lineageObservationProvider{fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady, inspectStatus: ProviderStatusReady}, fault: fault}
			store := NewMemoryStore()
			s := testService(t, testRegistry(t, p, nil), store)
			source, err := s.Create(t.Context(), CreateRequest{AccountID: "account", Name: "source", Spec: testSpec()})
			if err != nil {
				t.Fatal(err)
			}
			p.provisionStatus = ProviderStatusPending
			target, err := s.Restore(t.Context(), RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "target", PointInTime: time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC)})
			if err != nil || target.State != StateProvisioning {
				t.Fatalf("pending restore: %+v %v", target, err)
			}
			s.now = func() time.Time { return target.RetryAt.Add(time.Second) }
			result, err := s.Reconcile(t.Context(), target.AccountID, target.ID)
			if fault == "valid" {
				if err != nil || result.State != StateReady {
					t.Fatalf("verified restore: %+v %v", result, err)
				}
			} else {
				wanted := ErrConflict
				if fault == "missing" {
					wanted = ErrUnavailable
				}
				if !errors.Is(err, wanted) {
					t.Fatalf("unverified readiness: %+v %v", result, err)
				}
				held, readErr := store.Get(t.Context(), target.AccountID, target.ID)
				if readErr != nil || held.State == StateReady || held.ObservedGeneration != 0 {
					t.Fatalf("unverified data published: %+v %v", held, readErr)
				}
			}
		})
	}
}
