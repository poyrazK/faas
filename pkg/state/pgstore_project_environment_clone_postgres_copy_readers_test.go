//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneCopyReaderFixture(t *testing.T) (*state.PgStore, context.Context, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, string) {
	t.Helper()
	s, ctx, pool, lease, snapshot := cloneNativeAdoptionFixture(t)
	if _, _, err := s.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	return s, ctx, pool, lease, snapshot.SourceDatabaseID
}

func claimCloneCopyReader(t *testing.T, s *state.PgStore, ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) state.ProjectEnvironmentClonePostgresCopyReader {
	t.Helper()
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresCopyReader(ctx, l, id, 2); err != nil {
		t.Fatal(err)
	}
	r, dispatch, err := s.ClaimProjectEnvironmentClonePostgresCopyReaderRequest(ctx, l, id)
	if err != nil || !dispatch {
		t.Fatalf("first dispatch: %v %v", dispatch, err)
	}
	return r
}

func copyReaderObservation(r state.ProjectEnvironmentClonePostgresCopyReader) state.ProjectEnvironmentClonePostgresCopyReaderObservation {
	return state.ProjectEnvironmentClonePostgresCopyReaderObservation{EndpointID: "reader-" + r.OwnerID, CreatedAt: r.RequestStartedAt, Available: true}
}

func TestPgClonePostgresSnapshotReaderSingleDispatchAndWorkerHandoff(t *testing.T) {
	s, ctx, pool, l, id := cloneCopyReaderFixture(t)
	var wg sync.WaitGroup
	type result struct {
		r       state.ProjectEnvironmentClonePostgresCopyReader
		created bool
		err     error
	}
	results := make(chan result, 6)
	for range 6 {
		wg.Go(func() {
			r, created, err := s.ReserveProjectEnvironmentClonePostgresCopyReader(ctx, l, id, 1)
			results <- result{r, created, err}
		})
	}
	wg.Wait()
	close(results)
	var original state.ProjectEnvironmentClonePostgresCopyReader
	creations := 0
	for got := range results {
		if got.err != nil || got.r.State != "reserved" || got.r.OwnerID == "" || got.r.Available || got.r.EndpointID != "" {
			t.Fatalf("reservation: %+v %v", got.r, got.err)
		}
		if original.OwnerID == "" {
			original = got.r
		}
		if original.OwnerID != got.r.OwnerID || !original.Scope.Equal(got.r.Scope) {
			t.Fatal("concurrent reservation replaced ownership")
		}
		if got.created {
			creations++
		}
	}
	if creations != 1 {
		t.Fatalf("created %d owners", creations)
	}
	r, dispatch, err := s.ClaimProjectEnvironmentClonePostgresCopyReaderRequest(ctx, l, id)
	if err != nil || !dispatch || r.State != "requested" || r.RequestStartedAt.IsZero() {
		t.Fatalf("first dispatch: %+v %v %v", r, dispatch, err)
	}
	old := l
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, l, 0); err != nil {
		t.Fatal(err)
	}
	l, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if recovered, dispatch, err := s.ClaimProjectEnvironmentClonePostgresCopyReaderRequest(ctx, l, id); err != nil || dispatch || recovered.OwnerID != r.OwnerID || !recovered.RequestStartedAt.Equal(r.RequestStartedAt) {
		t.Fatalf("handoff repeated uncertain creation: %v %v", dispatch, err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, old, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker read: %v", err)
	}
	o := copyReaderObservation(r)
	for _, available := range []bool{false, true, false} {
		o.Available = available
		observed, err := s.RecordProjectEnvironmentClonePostgresCopyReader(ctx, l, id, o)
		if err != nil || observed.Available != available || observed.State != "observed" || !observed.EndpointCreatedAt.Equal(o.CreatedAt) {
			t.Fatalf("availability observation: %+v %v", observed, err)
		}
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set storage_limit_bytes=123456,restore_window_seconds=0 where id=$1", id); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, l, id)
	if err != nil || !recovered.Scope.Equal(r.Scope) || recovered.EndpointID != o.EndpointID {
		t.Fatalf("live edit rebased reader: %v", err)
	}
	if resources := l.Operation.Resources; len(resources) != 1 || resources[0].TargetID != "" || resources[0].Status != "captured" {
		t.Fatal("compute observation supplied data readiness")
	}
	var holds int
	if err := pool.QueryRow(ctx, "select count(*) from project_environment_clone_postgres_copy_readers where account_id=$1 and state<>'retired'", l.Operation.AccountID).Scan(&holds); err != nil || holds != 1 {
		t.Fatalf("ownership quota hold: %d %v", holds, err)
	}
}

func TestPgClonePostgresSnapshotReaderRejectsReplacementAndBadScope(t *testing.T) {
	s, ctx, pool, l, id := cloneCopyReaderFixture(t)
	r := claimCloneCopyReader(t, s, ctx, l, id)
	o := copyReaderObservation(r)
	if _, err := s.RecordProjectEnvironmentClonePostgresCopyReader(ctx, l, id, o); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"endpoint", "time", "source", "capture", "old", "future", "precision"} {
		bad := o
		switch fault {
		case "endpoint":
			bad.EndpointID = "replacement"
		case "time":
			bad.CreatedAt = bad.CreatedAt.Add(time.Second)
		case "source":
			bad.EndpointID = r.Scope.SourceDataResourceID
		case "capture":
			bad.EndpointID = r.Scope.CaptureProviderResourceID
		case "old":
			bad.CreatedAt = r.RequestStartedAt.Add(-time.Second)
		case "future":
			bad.CreatedAt = time.Now().Add(time.Hour)
		case "precision":
			bad.CreatedAt = bad.CreatedAt.Add(time.Nanosecond)
		}
		if _, err := s.RecordProjectEnvironmentClonePostgresCopyReader(ctx, l, id, bad); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("accepted %s replacement: %v", fault, err)
		}
	}
	for _, limit := range []int{0, api.PostgresCopyReadersPerAccountMax + 1} {
		if _, _, err := s.ReserveProjectEnvironmentClonePostgresCopyReader(ctx, l, id, limit); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("unbounded reader quota: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, "update project_environment_clone_postgres_copy_readers set scope=jsonb_set(scope,'{CaptureDatabaseID}',to_jsonb($2::text)) where operation_id=$1", l.Operation.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, l, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("substituted scope adopted: %v", err)
	}
	raw, _ := json.Marshal(r.Scope)
	if _, err := pool.Exec(ctx, "update project_environment_clone_postgres_copy_readers set scope=$2 where operation_id=$1", l.Operation.ID, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, l, id); err != nil {
		t.Fatal(err)
	}
}

func TestPgClonePostgresSnapshotReaderUndispatchedRetirementAndDowngradeHold(t *testing.T) {
	s, ctx, pool, l, id := cloneCopyReaderFixture(t)
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresCopyReader(ctx, l, id, 1); err != nil {
		t.Fatal(err)
	}
	l = cloneForkCompensation(t, s, ctx, l)
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, l, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unretired reservation released native capture: %v", err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id); err != nil {
		t.Fatal(err)
	}
	retired, err := s.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id, state.ProjectEnvironmentClonePostgresCopyReaderDeletion{Done: true})
	if err != nil || retired.State != "retired" || retired.RetiredAt.IsZero() || !retired.RequestStartedAt.IsZero() {
		t.Fatalf("undispatched retirement: %+v %v", retired, err)
	}
	if replay, err := s.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id, state.ProjectEnvironmentClonePostgresCopyReaderDeletion{Done: true}); err != nil || replay != retired {
		t.Fatalf("retirement replay: %v", err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, l, id); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "alter table project_environment_clone_postgres_copy_readers add constraint postgres_copy_readers_down_no_ownership check(false)"); err == nil {
		t.Fatal("downgrade discarded retired ownership")
	}
}

func TestPgClonePostgresSnapshotReaderCleanupPinsProofAndHoldsNativeRetirement(t *testing.T) {
	s, ctx, pool, l, id := cloneCopyReaderFixture(t)
	r := claimCloneCopyReader(t, s, ctx, l, id)
	l = cloneForkCompensation(t, s, ctx, l)
	if _, err := s.BeginProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, l, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unknown create discarded native discovery input: %v", err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id, state.ProjectEnvironmentClonePostgresCopyReaderDeletion{Done: true}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unknown create retired by absence: %v", err)
	}
	o := copyReaderObservation(r)
	r, err := s.RecordProjectEnvironmentClonePostgresCopyReader(ctx, l, id, o)
	if err != nil || r.Available || r.State != "deleting" {
		t.Fatalf("cleanup identity discovery: %+v %v", r, err)
	}
	r, dispatch, err := s.ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id)
	if err != nil || !dispatch || r.CleanupDispatchedAt.IsZero() {
		t.Fatalf("first delete: %v %v", dispatch, err)
	}
	if replay, dispatch, err := s.ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id); err != nil || dispatch || !replay.CleanupDispatchedAt.Equal(r.CleanupDispatchedAt) {
		t.Fatalf("delete reply loss repeated dispatch: %v %v", dispatch, err)
	}
	capture, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, l, id)
	if err != nil {
		t.Fatal(err)
	}
	forkProof := state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{TargetProviderResourceID: capture.TargetProviderResourceID, OperationIDs: []string{"capture-delete"}, Done: true}
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(ctx, l, id, forkProof); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, l, id, forkProof); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("capture quota released before endpoint retirement: %v", err)
	}
	proof := state.ProjectEnvironmentClonePostgresCopyReaderDeletion{EndpointID: r.EndpointID, CreatedAt: r.EndpointCreatedAt, OperationIDs: []string{"reader-delete"}, Done: true}
	if _, err := s.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unstored operation proof retired reader: %v", err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(ctx, l, id, proof); err != nil {
		t.Fatal(err)
	}
	bad := proof
	bad.OperationIDs = []string{"other-delete"}
	if _, err := s.RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(ctx, l, id, bad); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("operation chain replaced: %v", err)
	}
	proof.Done = false
	if _, err := s.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("pending operations retired reader: %v", err)
	}
	proof.Done = true
	retired, err := s.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id, proof)
	if err != nil || retired.State != "retired" {
		t.Fatalf("reader retirement: %v", err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, l, id, forkProof); err != nil {
		t.Fatalf("reader retirement held capture quota: %v", err)
	}
	if replay, err := s.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, l, id); err != nil || replay != retired {
		t.Fatalf("native retirement discarded reader receipt: %v", err)
	}
	var active int
	if err := pool.QueryRow(ctx, "select count(*) from project_environment_clone_postgres_copy_readers where account_id=$1 and state<>'retired'", l.Operation.AccountID).Scan(&active); err != nil || active != 0 {
		t.Fatalf("retired reader kept compute ceiling: %d %v", active, err)
	}
}

func TestPgClonePostgresSnapshotReaderCaptureFallbackRequiresPinnedNativeChain(t *testing.T) {
	s, ctx, _, l, id := cloneCopyReaderFixture(t)
	r := claimCloneCopyReader(t, s, ctx, l, id)
	r, err := s.RecordProjectEnvironmentClonePostgresCopyReader(ctx, l, id, copyReaderObservation(r))
	if err != nil {
		t.Fatal(err)
	}
	l = cloneForkCompensation(t, s, ctx, l)
	if _, err := s.BeginProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id); err != nil {
		t.Fatal(err)
	}
	capture, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, l, id)
	if err != nil {
		t.Fatal(err)
	}
	proof := state.ProjectEnvironmentClonePostgresCopyReaderDeletion{EndpointID: r.EndpointID, CreatedAt: r.EndpointCreatedAt, CaptureOperationIDs: []string{"capture-delete"}, Done: true}
	if _, err := s.RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(ctx, l, id, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unretained native chain granted reader retirement: %v", err)
	}
	fork := state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{TargetProviderResourceID: capture.TargetProviderResourceID, OperationIDs: proof.CaptureOperationIDs, Done: true}
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(ctx, l, id, fork); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(ctx, l, id, proof); err != nil {
		t.Fatal(err)
	}
	r, err = s.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id, proof)
	if err != nil || r.State != "retired" || !r.CleanupDispatchedAt.IsZero() {
		t.Fatalf("qualified native/endpoint absence required DELETE replay: %v", err)
	}
}

func TestPgClonePostgresSnapshotReaderRechecksLeaseAfterReceiptLock(t *testing.T) {
	s, ctx, pool, l, id := cloneCopyReaderFixture(t)
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresCopyReader(ctx, l, id, 1); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '300 milliseconds' where id=$1 returning lease_until", l.Operation.ID).Scan(&l.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "select operation_id from project_environment_clone_postgres_copy_readers where operation_id=$1 for update", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, _, err := s.ClaimProjectEnvironmentClonePostgresCopyReaderRequest(ctx, l, id); done <- err }()
	time.Sleep(500 * time.Millisecond)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired worker dispatched after lock wait: %v", err)
	}
	var dispatched bool
	if err := pool.QueryRow(ctx, "select request_started_at is not null from project_environment_clone_postgres_copy_readers where operation_id=$1", l.Operation.ID).Scan(&dispatched); err != nil || dispatched {
		t.Fatalf("expired dispatch changed row: %v %v", dispatched, err)
	}
}

func TestPgClonePostgresSnapshotReaderAccountQuotaSerializesDistinctSources(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	a, project, firstApp, op := cloneBindingFixture(t, s)
	secondApp, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: project.ID, Slug: "reader-second", WorkloadName: "reader-second", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := s.CreateDeployment(ctx, state.Deployment{AppID: secondApp.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:bindings"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, deployment.ID, "/bindings.ext4", "layers/bindings-"+deployment.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	for i, app := range []state.App{firstApp, secondApp} {
		secret := clonePostgresSecretFixture(t, pool, a, app, "production", []string{"reader-first", "reader-second"}[i], 1)
		if err := s.PutManagedPostgresSecret(ctx, secret); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, project.ID, op.ID, op.Revision); err != nil {
		t.Fatal(err)
	}
	views, err := s.ProjectEnvironmentCloneBindings(ctx, a.ID, project.ID, op.ID)
	if err != nil || len(views) != 2 {
		t.Fatalf("two-source capture: %v", err)
	}
	l, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	point := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
	resources := []state.ProjectEnvironmentCloneResource{}
	for _, view := range views {
		source := view.Postgres[0]
		hash, err := state.ProjectEnvironmentCloneDatabaseSourceHash(source)
		if err != nil {
			t.Fatal(err)
		}
		resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: "managed_postgres", Name: source.DatabaseID, SourceID: source.DatabaseID,
			SourceVersion: hash, CapturePoint: point.Format(time.RFC3339Nano), Status: "captured"})
	}
	l.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, project.ID, op.ID, op.Status, op.Status, l.Operation.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, resource := range resources {
		id := resource.SourceID
		snapshot, err := s.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, l, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ClaimProjectEnvironmentClonePostgresSnapshotRequest(ctx, l, id); err != nil {
			t.Fatal(err)
		}
		retained := clonePostgresSnapshotObservation(snapshot)
		retained.ProviderSnapshotID = "provider-snapshot/" + id
		snapshot, err = s.RecordProjectEnvironmentClonePostgresSnapshot(ctx, l, id, retained)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, l, id, 4); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, l, id); err != nil {
			t.Fatal(err)
		}
		observed := cloneSnapshotRestoreObservation(snapshot)
		observed.TargetProviderResourceID = "provider/target/" + id
		observed.Restored = true
		if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, l, id, observed); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, l, id); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 2)
	for _, resource := range resources {
		go func(id string) {
			_, _, err := s.ReserveProjectEnvironmentClonePostgresCopyReader(ctx, l, id, 1)
			results <- err
		}(resource.SourceID)
	}
	success, quota := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, state.ErrQuotaExceeded) {
			quota++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || quota != 1 {
		t.Fatalf("account ceiling raced: success=%d quota=%d", success, quota)
	}
	for _, resource := range resources {
		if _, _, err := s.ReserveProjectEnvironmentClonePostgresCopyReader(ctx, l, resource.SourceID, 2); err != nil {
			t.Fatal(err)
		}
	}
}
