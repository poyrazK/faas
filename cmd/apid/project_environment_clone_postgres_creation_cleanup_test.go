// adr: 638
package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneCreationReceiptProvider struct {
	*cloneSnapshotProvider
	accepted                               managedpostgres.CreationAcknowledgement
	ownedCreates, ownedDeletes, ownedReads int
	pending                                bool
}

func (p *cloneCreationReceiptProvider) CaptureSnapshotWithCreationReceipt(ctx context.Context, r managedpostgres.SnapshotCaptureRequest, expected *managedpostgres.CreationAcknowledgement, record managedpostgres.CreationRecorder) (managedpostgres.DatabaseSnapshot, error) {
	if expected != nil {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrConflict
	}
	p.ownedCreates++
	p.accepted = managedpostgres.CreationAcknowledgement{ProviderResourceID: "project/snapshots/accepted", SourceResourceID: r.SourceResourceID, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if err := record(ctx, p.accepted); err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrUnavailable
}
func (p *cloneCreationReceiptProvider) ObserveSnapshotCreation(_ context.Context, _ managedpostgres.SnapshotCaptureRequest, a managedpostgres.CreationAcknowledgement) (managedpostgres.DatabaseSnapshot, error) {
	p.ownedReads++
	if a != p.accepted {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrConflict
	}
	return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrUnavailable
}
func (p *cloneCreationReceiptProvider) DeleteSnapshotCreation(ctx context.Context, _ managedpostgres.SnapshotCaptureRequest, a managedpostgres.CreationAcknowledgement, cleanup managedpostgres.CreationCleanup) (managedpostgres.DeleteResult, error) {
	if !cleanup.Started {
		if err := cleanup.RecordStarted(ctx); err != nil {
			return managedpostgres.DeleteResult{}, err
		}
	}
	p.ownedDeletes++
	if a != p.accepted {
		return managedpostgres.DeleteResult{}, managedpostgres.ErrConflict
	}
	return managedpostgres.DeleteResult{Done: !p.pending}, nil
}

// Regression: missing capture metadata prevented cleanup despite a successful
// creation acknowledgement. Custody must survive independently of retention.
func TestPGCloneSnapshotCreationCustodyCompensatesUnverifiedCapture(t *testing.T) {
	for _, fault := range []string{"normal", "pending", "lost_cleanup_checkpoint"} {
		t.Run(fault, func(t *testing.T) {
			f, store, old, sourceID := cloneSnapshotWorkerFixture(t)
			p := &cloneCreationReceiptProvider{cloneSnapshotProvider: old, pending: fault == "pending"}
			registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, MaxDatabasesPerAccount: 3,
				Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "snapshot-worker"}}}, func(string) string { return "" },
				map[string]managedpostgres.Factory{"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
					return p, nil
				}})
			if err != nil {
				t.Fatal(err)
			}
			databases, err := managedpostgres.NewPostgresStore(f.pool)
			if err != nil {
				t.Fatal(err)
			}
			enabled := true
			f.srv.managedPostgres, err = managedpostgres.NewService(registry, databases, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return enabled }})
			if err != nil {
				t.Fatal(err)
			}
			f.lease, err = f.srv.captureProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
			if !errors.Is(err, managedpostgres.ErrUnavailable) || p.ownedCreates != 1 {
				t.Fatalf("capture: %v creates=%d", err, p.ownedCreates)
			}
			f.lease, err = f.srv.captureProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
			if !errors.Is(err, managedpostgres.ErrUnavailable) || p.ownedCreates != 1 || p.ownedReads != 1 || old.finds != 0 {
				t.Fatalf("recovery: %v creates=%d reads=%d finds=%d", err, p.ownedCreates, p.ownedReads, old.finds)
			}
			receipt, err := store.ProjectEnvironmentClonePostgresSnapshotForLease(t.Context(), f.lease, sourceID)
			if err != nil || receipt.State != "requested" || receipt.ProviderSnapshotID != "" {
				t.Fatalf("custody became retention: %+v %v", receipt, err)
			}
			op := f.lease.Operation
			f.lease.Operation, err = store.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
			if err != nil {
				t.Fatal(err)
			}
			enabled = false
			store.loseCleanup = fault == "lost_cleanup_checkpoint"
			var done bool
			f.lease, done, err = f.srv.cleanupProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
			if fault == "lost_cleanup_checkpoint" {
				if err == nil {
					t.Fatal("lost checkpoint error hidden")
				}
				f.lease, done, err = f.srv.cleanupProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
			}
			if err != nil || done == (fault == "pending") {
				t.Fatalf("cleanup: done=%v err=%v", done, err)
			}
			if fault == "pending" {
				p.pending = false
				f.lease, done, err = f.srv.cleanupProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
				if err != nil || !done {
					t.Fatalf("cleanup retry: %v %v", done, err)
				}
			}
			receipt, err = store.ProjectEnvironmentClonePostgresSnapshotForLease(t.Context(), f.lease, sourceID)
			if err != nil || receipt.State != "deleted" || receipt.ProviderSnapshotID != "" || old.deletes != 0 {
				t.Fatalf("cleanup fabricated retention: %+v %v deletes=%d", receipt, err, old.deletes)
			}
			if _, err := store.ProjectEnvironmentBySlug(t.Context(), op.AccountID, op.ProjectID, "snapshots"); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("unverified environment published: %v", err)
			}
		})
	}
}
