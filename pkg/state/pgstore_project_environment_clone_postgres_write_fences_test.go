//go:build !no_pg

// adr: 583
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func clonePostgresWriteFenceFixture(t *testing.T) (*state.PgStore, context.Context, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, string) {
	t.Helper()
	s, ctx, pool := pgWithPool(t)
	a, p, app, op := cloneBindingFixture(t, s)
	secret := clonePostgresSecretFixture(t, pool, a, app, "production", "write-fence-source", 1)
	if err := s.PutManagedPostgresSecret(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision); err != nil {
		t.Fatal(err)
	}
	views, err := s.ProjectEnvironmentCloneBindings(ctx, a.ID, p.ID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return s, ctx, pool, lease, views[0].Postgres[0].DatabaseID
}

func clonePostgresFenceAbandonment(r state.ProjectEnvironmentClonePostgresWriteFence) state.ProjectEnvironmentClonePostgresFenceAbandonment {
	return state.ProjectEnvironmentClonePostgresFenceAbandonment{OwnerToken: r.OperationID, SourceDataResourceID: r.SourceDataResourceID,
		State: "abandoned", ReleasedAt: time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)}
}

func compensatePostgresWriteFence(t *testing.T, s *state.PgStore, ctx context.Context, lease state.ProjectEnvironmentCloneLease) state.ProjectEnvironmentCloneLease {
	t.Helper()
	op := lease.Operation
	var err error
	lease.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestPgClonePostgresWriteFenceOwnershipAndRecovery(t *testing.T) {
	s, ctx, pool, lease, sourceID := clonePostgresWriteFenceFixture(t)
	if len(lease.Operation.Resources) != 0 {
		t.Fatal("fixture has already selected a data point")
	}
	r, err := s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, lease, sourceID)
	if err != nil || r.State != "held" || r.OperationID != lease.Operation.ID || r.SourceDatabaseID != sourceID || len(r.SourceVersion) != 64 || r.CreatedAt.IsZero() {
		t.Fatalf("source hold before checkpoint: %+v %v", r, err)
	}
	if replay, err := s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, lease, sourceID); err != nil || replay != r {
		t.Fatalf("reservation recovery: %+v %v", replay, err)
	}
	if _, err := s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, lease, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("uncaptured source accepted: %v", err)
	}
	// Remove normal binding dependencies; the new receipt alone must hold
	// lifecycle deletion across unknown provider outcomes and lease turnover.
	if _, err := pool.Exec(ctx, "update managed_postgres_bindings set state='deleted',deleted_at=clock_timestamp() where database_id=$1", sourceID); err != nil {
		t.Fatal(err)
	}
	databases, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	assertHeld := func() {
		t.Helper()
		now := time.Now().UTC()
		if _, err := databases.ClaimDelete(ctx, lease.Operation.AccountID, sourceID, uuid.NewString(), now, now.Add(time.Minute)); !errors.Is(err, managedpostgres.ErrConflict) {
			t.Fatalf("closure recovery hold discarded: %v", err)
		}
	}
	assertHeld()
	for _, next := range []string{state.CloneOperationCopying, state.CloneOperationFailed} {
		op := lease.Operation
		if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, next, op.Revision, nil, ""); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("%s discarded recovery authority: %v", next, err)
		}
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("capture cleared a hold: %v", err)
	}
	stale := lease
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	lease, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []bool{false, true} {
		if mutate {
			_, err = s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, stale, sourceID)
		} else {
			_, err = s.ProjectEnvironmentClonePostgresWriteFencesForLease(ctx, stale)
		}
		if !errors.Is(err, state.ErrConflict) {
			t.Fatalf("old worker used source hold: %v", err)
		}
	}
	owned, err := s.ProjectEnvironmentClonePostgresWriteFencesForLease(ctx, lease)
	if err != nil || len(owned) != 1 || owned[0] != r {
		t.Fatalf("takeover rebased the hold: %+v %v", owned, err)
	}
	lease = compensatePostgresWriteFence(t, s, ctx, lease)
	op := lease.Operation
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensated, op.Revision, nil, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("terminal cleanup discarded the hold: %v", err)
	}
	observation := clonePostgresFenceAbandonment(r)
	if _, err := s.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID, observation); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("missing abandonment intent accepted: %v", err)
	}
	for range 2 {
		r, err = s.BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID)
		if err != nil || r.State != "abandoning" {
			t.Fatalf("abandonment intent recovery: %+v %v", r, err)
		}
	}
	assertHeld()
	for range 2 {
		r, err = s.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID, observation)
		if err != nil || r.State != "released" || r.ReleasedAt.IsZero() || r.RemoteTerminalState != observation.State || !r.RemoteReleasedAt.Equal(observation.ReleasedAt) {
			t.Fatalf("observed terminal record: %+v %v", r, err)
		}
	}
	now := time.Now().UTC()
	if _, err := databases.ClaimDelete(ctx, op.AccountID, sourceID, uuid.NewString(), now, now.Add(time.Minute)); err != nil {
		t.Fatalf("observed abandonment did not release deletion hold: %v", err)
	}
	// A lost control-plane commit reply remains recoverable after source
	// lifecycle has legitimately advanced. It cannot rewrite the receipt.
	replay, err := s.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID, observation)
	if err != nil || replay != r {
		t.Fatalf("release recovery after source deletion claim: %+v %v", replay, err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensated, op.Revision, nil, ""); err != nil {
		t.Fatalf("finished abandonment could not terminate: %v", err)
	}
}

func TestPgClonePostgresWriteFenceRejectsTerminalSubstitution(t *testing.T) {
	s, ctx, _, lease, sourceID := clonePostgresWriteFenceFixture(t)
	r, err := s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, lease, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	lease = compensatePostgresWriteFence(t, s, ctx, lease)
	if _, err := s.BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID); err != nil {
		t.Fatal(err)
	}
	good := clonePostgresFenceAbandonment(r)
	for _, fault := range []string{"owner", "source", "state", "missing_time", "future", "precision"} {
		bad := good
		switch fault {
		case "owner":
			bad.OwnerToken = uuid.NewString()
		case "source":
			bad.SourceDataResourceID += "-other"
		case "state":
			bad.State = "absent"
		case "missing_time":
			bad.ReleasedAt = time.Time{}
		case "future":
			bad.ReleasedAt = good.ReleasedAt.Add(time.Hour)
		case "precision":
			bad.ReleasedAt = good.ReleasedAt.Add(time.Nanosecond)
		}
		if _, err := s.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID, bad); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("%s terminal substitution: %v", fault, err)
		}
	}
	owned, err := s.ProjectEnvironmentClonePostgresWriteFencesForLease(ctx, lease)
	if err != nil || len(owned) != 1 || owned[0].State != "abandoning" || !owned[0].ReleasedAt.IsZero() {
		t.Fatalf("substitution changed the hold: %+v %v", owned, err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID, good); err != nil {
		t.Fatal(err)
	}
	good.ReleasedAt = good.ReleasedAt.Add(time.Second)
	if _, err := s.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID, good); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("terminal timestamp was rewritten: %v", err)
	}
}

func TestPgClonePostgresWriteFenceRejectsPlacementSubstitution(t *testing.T) {
	for _, phase := range []string{"reserve", "read", "begin", "finish"} {
		t.Run(phase, func(t *testing.T) {
			s, ctx, pool, lease, sourceID := clonePostgresWriteFenceFixture(t)
			var r state.ProjectEnvironmentClonePostgresWriteFence
			if phase != "reserve" {
				var err error
				r, err = s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, lease, sourceID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == "begin" || phase == "finish" {
				lease = compensatePostgresWriteFence(t, s, ctx, lease)
			}
			if phase == "finish" {
				var err error
				r, err = s.BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pool.Exec(ctx, "update managed_postgres_databases set data_resource_id=data_resource_id||'-other' where id=$1", sourceID); err != nil {
				t.Fatal(err)
			}
			if err := mutatePostgresWriteFence(ctx, s, lease, sourceID, phase, clonePostgresFenceAbandonment(r)); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("%s used substituted placement: %v", phase, err)
			}
		})
	}
}

func mutatePostgresWriteFence(ctx context.Context, s *state.PgStore, lease state.ProjectEnvironmentCloneLease, sourceID, phase string, observation state.ProjectEnvironmentClonePostgresFenceAbandonment) error {
	var err error
	switch phase {
	case "reserve":
		_, err = s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, lease, sourceID)
	case "read":
		_, err = s.ProjectEnvironmentClonePostgresWriteFencesForLease(ctx, lease)
	case "begin":
		_, err = s.BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID)
	case "finish":
		_, err = s.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID, observation)
	}
	return err
}

func TestPgClonePostgresWriteFenceLeaseExpiryAfterSourceLock(t *testing.T) {
	for _, phase := range []string{"reserve", "read", "begin", "finish"} {
		t.Run(phase, func(t *testing.T) {
			s, ctx, pool, lease, sourceID := clonePostgresWriteFenceFixture(t)
			var r state.ProjectEnvironmentClonePostgresWriteFence
			if phase != "reserve" {
				var err error
				r, err = s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, lease, sourceID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == "begin" || phase == "finish" {
				lease = compensatePostgresWriteFence(t, s, ctx, lease)
			}
			if phase == "finish" {
				var err error
				r, err = s.BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := pool.QueryRow(ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '500 milliseconds' where id=$1 returning lease_until", lease.Operation.ID).Scan(&lease.ExpiresAt); err != nil {
				t.Fatal(err)
			}
			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := blocker.Exec(ctx, "select id from managed_postgres_databases where id=$1 for update", sourceID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				done <- mutatePostgresWriteFence(ctx, s, lease, sourceID, phase, clonePostgresFenceAbandonment(r))
			}()
			deadline := time.Now().Add(2 * time.Second)
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, "select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and query like '%ReadProjectEnvironmentCloneDatabaseSource%')").Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("worker did not reach held source lock")
				}
				time.Sleep(5 * time.Millisecond)
			}
			time.Sleep(max(0, time.Until(lease.ExpiresAt)) + 30*time.Millisecond)
			if err := blocker.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, state.ErrConflict) {
				t.Fatalf("expired %s committed: %v", phase, err)
			}
			var count int
			want := 1
			if phase == "reserve" {
				want = 0
			}
			if err := pool.QueryRow(ctx, "select count(*) from project_environment_clone_postgres_write_fences where operation_id=$1 and state<>'released'", lease.Operation.ID).Scan(&count); err != nil || count != want {
				t.Fatalf("expired mutation changed holds: %d/%d %v", count, want, err)
			}
			if phase != "reserve" {
				var status string
				if err := pool.QueryRow(ctx, "select state from project_environment_clone_postgres_write_fences where operation_id=$1 and source_database_id=$2", lease.Operation.ID, sourceID).Scan(&status); err != nil || status != r.State {
					t.Fatalf("expired mutation changed hold phase: %q/%q %v", status, r.State, err)
				}
			}
		})
	}
}

func TestPgClonePostgresWriteFenceRejectsAnotherOperation(t *testing.T) {
	s, ctx, pool, lease, sourceID := clonePostgresWriteFenceFixture(t)
	r, err := s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, lease, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, time.Hour); err != nil {
		t.Fatal(err)
	}
	op := lease.Operation
	other, err := s.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: op.AccountID, ProjectID: op.ProjectID,
		SourceEnvironment: op.SourceEnvironment, TargetEnvironment: "another-stage", IdempotencyKey: uuid.NewString(), SourceRevisionHash: op.SourceRevisionHash})
	if err != nil {
		t.Fatal(err)
	}
	other, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, other.AccountID, other.ProjectID, other.ID, other.Status, state.CloneOperationCapturing, other.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, other.AccountID, other.ProjectID, other.ID, other.Revision); err != nil {
		t.Fatal(err)
	}
	second, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil || second.Operation.ID != other.ID {
		t.Fatalf("claim another operation: %+v %v", second, err)
	}
	if _, err := s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, second, sourceID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("another operation adopted the source: %v", err)
	}
	var owner string
	if err := pool.QueryRow(ctx, "select operation_id::text from project_environment_clone_postgres_write_fences where source_database_id=$1 and state<>'released'", sourceID).Scan(&owner); err != nil || owner != r.OperationID {
		t.Fatalf("reservation owner changed: %q %v", owner, err)
	}
	second = compensatePostgresWriteFence(t, s, ctx, second)
	if _, err := s.BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, second, sourceID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another operation retired the source: %v", err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, second, sourceID, clonePostgresFenceAbandonment(r)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another operation released the source: %v", err)
	}
}

func TestPgClonePostgresWriteFenceBlocksDirectPublication(t *testing.T) {
	s, ctx, pool, lease, sourceID := clonePostgresWriteFenceFixture(t)
	if _, err := s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, lease, sourceID); err != nil {
		t.Fatal(err)
	}
	// Exercise the final publication SQL guard independently of higher-level
	// graph verification. This fixture intentionally has no valid release.
	if _, err := pool.Exec(ctx, "update project_environment_clone_operations set status='publishing' where id=$1", lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	opID := pgtype.UUID{Bytes: uuid.MustParse(lease.Operation.ID), Valid: true}
	releaseID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	n, err := sqlc.New().CompleteProjectEnvironmentClonePublication(ctx, pool, sqlc.CompleteProjectEnvironmentClonePublicationParams{
		OperationID: opID, ReleaseID: releaseID, Revision: lease.Operation.Revision})
	if err != nil || n != 0 {
		t.Fatalf("publication discarded recovery authority: %d %v", n, err)
	}
}
