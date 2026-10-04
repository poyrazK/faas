//go:build !no_pg

// adr: 583
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

func clonePostgresMaintenanceFixture(t *testing.T) (*state.PgStore, context.Context, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentClonePostgresMaintenance) {
	t.Helper()
	s, ctx, pool, lease, sourceID := clonePostgresWriteFenceFixture(t)
	if _, err := s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, lease, sourceID); err != nil {
		t.Fatal(err)
	}
	r, err := s.ReserveProjectEnvironmentClonePostgresMaintenance(ctx, lease, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	return s, ctx, pool, lease, sourceID, r
}

func cloneMaintenanceObservation(r state.ProjectEnvironmentClonePostgresMaintenance, phase string) state.ProjectEnvironmentClonePostgresMaintenanceObservation {
	o := state.ProjectEnvironmentClonePostgresMaintenanceObservation{OwnerToken: r.ID, SourceDataResourceID: r.SourceDataResourceID, State: "reserved", OwnerOID: 10101}
	if phase != "role" {
		o.DatabaseOID = 20202
	}
	if phase == "activation" {
		o.State = "ready"
	}
	return o
}

func finishCloneMaintenance(t *testing.T, s *state.PgStore, ctx context.Context, lease state.ProjectEnvironmentCloneLease, sourceID string, r state.ProjectEnvironmentClonePostgresMaintenance) state.ProjectEnvironmentClonePostgresMaintenance {
	t.Helper()
	for _, phase := range []string{"role", "database", "activation"} {
		var err error
		r, _, err = s.ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(ctx, lease, sourceID, phase)
		if err != nil {
			t.Fatal(err)
		}
		r, err = s.RecordProjectEnvironmentClonePostgresMaintenance(ctx, lease, sourceID, phase, cloneMaintenanceObservation(r, phase))
		if err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestPgClonePostgresMaintenanceRequiresSourceHold(t *testing.T) {
	s, ctx, pool, lease, sourceID := clonePostgresWriteFenceFixture(t)
	if _, err := s.ReserveProjectEnvironmentClonePostgresMaintenance(ctx, lease, sourceID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("maintenance preceded source recovery authority: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "select count(*) from managed_postgres_checkpoint_maintenance").Scan(&count); err != nil || count != 0 {
		t.Fatalf("unowned maintenance reservation: %d %v", count, err)
	}
}

func TestPgClonePostgresMaintenanceOwnershipAndRecovery(t *testing.T) {
	s, ctx, pool, lease, sourceID, r := clonePostgresMaintenanceFixture(t)
	id, err := uuid.Parse(r.ID)
	if err != nil || id == uuid.Nil || r.ID == lease.Operation.ID || r.State != "reserved" || r.OwnerOID != 0 || r.DatabaseOID != 0 ||
		r.ReservedByOperationID != lease.Operation.ID || r.SourceDatabaseID != sourceID || r.CreatedAt.IsZero() || !r.ReadyAt.IsZero() {
		t.Fatalf("private reservation before IO: %+v %v", r, err)
	}
	if again, err := s.ReserveProjectEnvironmentClonePostgresMaintenance(ctx, lease, sourceID); err != nil || again != r {
		t.Fatalf("reservation rebase: %+v %v", again, err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresMaintenance(ctx, lease, sourceID, "role", cloneMaintenanceObservation(r, "role")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unrequested role observation: %v", err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(ctx, lease, sourceID, "database"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("database dispatch preceded role identity: %v", err)
	}
	for _, phase := range []string{"role", "database", "activation"} {
		requested, claimed, err := s.ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(ctx, lease, sourceID, phase)
		if err != nil || !claimed || requested.ID != r.ID || requested.State == r.State {
			t.Fatalf("first %s dispatch: %+v %v %v", phase, requested, claimed, err)
		}
		for range 2 {
			again, claimed, err := s.ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(ctx, lease, sourceID, phase)
			if err != nil || claimed || again != requested {
				t.Fatalf("%s replay redispatched or changed pins: %+v %v %v", phase, again, claimed, err)
			}
		}
		// A replacement worker recovers requested IO and the same private
		// UUID rather than beginning a new provider attempt under a new owner.
		stale := lease
		if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
			t.Fatal(err)
		}
		lease, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"reserve", "read", "claim", "record"} {
			if err := mutateCloneMaintenance(ctx, s, stale, sourceID, action, phase, cloneMaintenanceObservation(requested, phase)); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("stale worker %s maintenance: %v", action, err)
			}
		}
		observed, claimed, err := s.ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(ctx, lease, sourceID, phase)
		if err != nil || claimed || observed != requested {
			t.Fatalf("replacement %s attempt: %+v %v %v", phase, observed, claimed, err)
		}
		r, err = s.RecordProjectEnvironmentClonePostgresMaintenance(ctx, lease, sourceID, phase, cloneMaintenanceObservation(requested, phase))
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			again, err := s.RecordProjectEnvironmentClonePostgresMaintenance(ctx, lease, sourceID, phase, cloneMaintenanceObservation(requested, phase))
			if err != nil || again != r {
				t.Fatalf("%s acknowledgement recovery: %+v %v", phase, again, err)
			}
		}
	}
	if r.State != "ready" || r.OwnerOID != 10101 || r.DatabaseOID != 20202 || r.ReadyAt.IsZero() || r.RoleRequestedAt.IsZero() ||
		r.DatabaseRequestedAt.IsZero() || r.ActivationRequestedAt.IsZero() || len(lease.Operation.Resources) != 0 {
		t.Fatalf("maintenance readiness selected a point or lost pins: %+v", r)
	}
	op := lease.Operation
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, nil, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("maintenance readiness became checkpoint readiness: %v", err)
	}
	var hold string
	if err := pool.QueryRow(ctx, "select state from project_environment_clone_postgres_write_fences where operation_id=$1 and source_database_id=$2", op.ID, sourceID).Scan(&hold); err != nil || hold != "held" {
		t.Fatalf("maintenance discarded the source hold: %q %v", hold, err)
	}
}

func TestPgClonePostgresMaintenanceRejectsObservationSubstitution(t *testing.T) {
	for _, phase := range []string{"role", "database", "activation"} {
		t.Run(phase, func(t *testing.T) {
			s, ctx, _, lease, sourceID, r := clonePostgresMaintenanceFixture(t)
			for _, prior := range []string{"role", "database", "activation"} {
				if prior == phase {
					break
				}
				var err error
				if _, _, err := s.ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(ctx, lease, sourceID, prior); err != nil {
					t.Fatal(err)
				}
				r, err = s.RecordProjectEnvironmentClonePostgresMaintenance(ctx, lease, sourceID, prior, cloneMaintenanceObservation(r, prior))
				if err != nil {
					t.Fatal(err)
				}
			}
			var err error
			r, _, err = s.ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(ctx, lease, sourceID, phase)
			if err != nil {
				t.Fatal(err)
			}
			for _, fault := range []string{"owner_token", "source", "state", "missing_owner_oid", "owner_oid", "database_oid"} {
				bad := cloneMaintenanceObservation(r, phase)
				switch fault {
				case "owner_token":
					bad.OwnerToken = lease.Operation.ID
				case "source":
					bad.SourceDataResourceID += "-other"
				case "state":
					bad.State = "absent"
				case "missing_owner_oid":
					bad.OwnerOID = 0
				case "owner_oid":
					if phase == "role" {
						continue // First owner OID comes from the trusted observation.
					}
					bad.OwnerOID++
				case "database_oid":
					if phase == "role" {
						bad.DatabaseOID = 20202
					} else if phase == "database" {
						bad.DatabaseOID = 0
					} else {
						bad.DatabaseOID++
					}
				}
				if _, err := s.RecordProjectEnvironmentClonePostgresMaintenance(ctx, lease, sourceID, phase, bad); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("%s %s observation accepted: %v", phase, fault, err)
				}
			}
			if again, err := s.ProjectEnvironmentClonePostgresMaintenanceForLease(ctx, lease, sourceID); err != nil || again != r {
				t.Fatalf("substitution changed requested state: %+v %v", again, err)
			}
		})
	}
}

func TestPgClonePostgresMaintenanceCompensationRetainsUnknownIO(t *testing.T) {
	s, ctx, _, lease, sourceID, r := clonePostgresMaintenanceFixture(t)
	lease = compensatePostgresWriteFence(t, s, ctx, lease)
	if _, err := s.ProjectEnvironmentClonePostgresMaintenanceForLease(ctx, lease, sourceID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("compensation without source abandonment intent: %v", err)
	}
	fence, err := s.BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID, clonePostgresFenceAbandonment(fence)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unknown maintenance IO discarded recovery authority: %v", err)
	}
	// Compensation may finish the already-owned maintenance bootstrap in
	// order to record a remote operation abandonment tombstone. That never
	// authorizes a checkpoint or source hold release on maintenance alone.
	r = finishCloneMaintenance(t, s, ctx, lease, sourceID, r)
	if r.State != "ready" {
		t.Fatalf("owned compensation recovery: %+v", r)
	}
	fences, err := s.ProjectEnvironmentClonePostgresWriteFencesForLease(ctx, lease)
	if err != nil || len(fences) != 1 || fences[0].State != "abandoning" {
		t.Fatalf("bootstrap released source without remote terminal evidence: %+v %v", fences, err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID, clonePostgresFenceAbandonment(fence)); err != nil {
		t.Fatal(err)
	}
}

func TestPgClonePostgresMaintenanceReusesReadyOwnerAcrossOperations(t *testing.T) {
	s, ctx, _, lease, sourceID, r := clonePostgresMaintenanceFixture(t)
	r = finishCloneMaintenance(t, s, ctx, lease, sourceID, r)
	lease = compensatePostgresWriteFence(t, s, ctx, lease)
	fence, err := s.BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, sourceID, clonePostgresFenceAbandonment(fence)); err != nil {
		t.Fatal(err)
	}
	op := lease.Operation
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensated, op.Revision, nil, ""); err != nil {
		t.Fatal(err)
	}
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
		t.Fatalf("claim new operation: %+v %v", second, err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresMaintenanceForLease(ctx, second, sourceID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("new operation read maintenance without its source hold: %v", err)
	}
	if _, err := s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, second, sourceID); err != nil {
		t.Fatal(err)
	}
	if reused, err := s.ReserveProjectEnvironmentClonePostgresMaintenance(ctx, second, sourceID); err != nil || reused != r {
		t.Fatalf("another operation rebased private ownership: %+v %v", reused, err)
	}
	for _, phase := range []string{"role", "database", "activation"} {
		observed, claimed, err := s.ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(ctx, second, sourceID, phase)
		if err != nil || claimed || observed != r {
			t.Fatalf("ready %s ownership redispatched: %+v %v %v", phase, observed, claimed, err)
		}
		observed, err = s.RecordProjectEnvironmentClonePostgresMaintenance(ctx, second, sourceID, phase, cloneMaintenanceObservation(r, phase))
		if err != nil || observed != r {
			t.Fatalf("replay rewrote first owner/ready time: %+v %v", observed, err)
		}
	}
}

func mutateCloneMaintenance(ctx context.Context, s *state.PgStore, lease state.ProjectEnvironmentCloneLease, sourceID, action, phase string, observation state.ProjectEnvironmentClonePostgresMaintenanceObservation) error {
	var err error
	switch action {
	case "reserve":
		_, err = s.ReserveProjectEnvironmentClonePostgresMaintenance(ctx, lease, sourceID)
	case "read":
		_, err = s.ProjectEnvironmentClonePostgresMaintenanceForLease(ctx, lease, sourceID)
	case "claim":
		_, _, err = s.ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(ctx, lease, sourceID, phase)
	case "record":
		_, err = s.RecordProjectEnvironmentClonePostgresMaintenance(ctx, lease, sourceID, phase, observation)
	}
	return err
}

func TestPgClonePostgresMaintenanceLeaseExpiryAfterSourceLock(t *testing.T) {
	for _, action := range []string{"reserve", "read", "claim", "record"} {
		t.Run(action, func(t *testing.T) {
			s, ctx, pool, lease, sourceID := clonePostgresWriteFenceFixture(t)
			if _, err := s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, lease, sourceID); err != nil {
				t.Fatal(err)
			}
			var r state.ProjectEnvironmentClonePostgresMaintenance
			if action != "reserve" {
				var err error
				r, err = s.ReserveProjectEnvironmentClonePostgresMaintenance(ctx, lease, sourceID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if action == "record" {
				var err error
				r, _, err = s.ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(ctx, lease, sourceID, "role")
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
				done <- mutateCloneMaintenance(ctx, s, lease, sourceID, action, "role", cloneMaintenanceObservation(r, "role"))
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
					t.Fatal("worker did not reach the confirmed source lock wait")
				}
				time.Sleep(5 * time.Millisecond)
			}
			time.Sleep(max(0, time.Until(lease.ExpiresAt)) + 30*time.Millisecond)
			if err := blocker.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, state.ErrConflict) {
				t.Fatalf("expired %s committed: %v", action, err)
			}
			var count int
			if err := pool.QueryRow(ctx, "select count(*) from managed_postgres_checkpoint_maintenance").Scan(&count); err != nil || count != map[bool]int{true: 0, false: 1}[action == "reserve"] {
				t.Fatalf("expired %s changed reservations: %d %v", action, count, err)
			}
			if action != "reserve" {
				var state string
				if err := pool.QueryRow(ctx, "select state from managed_postgres_checkpoint_maintenance where id=$1", r.ID).Scan(&state); err != nil || state != r.State {
					t.Fatalf("expired %s changed phase: %q %v", action, state, err)
				}
			}
		})
	}
}

func TestPgClonePostgresMaintenanceRejectsPlacementSubstitution(t *testing.T) {
	for _, table := range []string{"source", "receipt"} {
		t.Run(table, func(t *testing.T) {
			s, ctx, pool, lease, sourceID, r := clonePostgresMaintenanceFixture(t)
			if table == "source" {
				if _, err := pool.Exec(ctx, "update managed_postgres_databases set data_resource_id=data_resource_id||'-other' where id=$1", sourceID); err != nil {
					t.Fatal(err)
				}
			} else if _, err := pool.Exec(ctx, "update managed_postgres_checkpoint_maintenance set source_data_resource_id=source_data_resource_id||'-other' where id=$1", r.ID); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"reserve", "read", "claim", "record"} {
				if err := mutateCloneMaintenance(ctx, s, lease, sourceID, action, "role", cloneMaintenanceObservation(r, "role")); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("%s %s adopted a changed dataset: %v", table, action, err)
				}
			}
		})
	}
}

func TestPgClonePostgresMaintenanceLeaseExpiryAfterReceiptLock(t *testing.T) {
	for _, action := range []string{"reserve", "read", "claim", "record"} {
		t.Run(action, func(t *testing.T) {
			s, ctx, pool, lease, sourceID, r := clonePostgresMaintenanceFixture(t)
			if action == "record" {
				var err error
				r, _, err = s.ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(ctx, lease, sourceID, "role")
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
			if _, err := blocker.Exec(ctx, "select id from managed_postgres_checkpoint_maintenance where id=$1 for update", r.ID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				done <- mutateCloneMaintenance(ctx, s, lease, sourceID, action, "role", cloneMaintenanceObservation(r, "role"))
			}()
			deadline := time.Now().Add(2 * time.Second)
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, "select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and query like '%ReadClonePostgresMaintenance%')").Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("worker did not reach the confirmed maintenance receipt lock wait")
				}
				time.Sleep(5 * time.Millisecond)
			}
			time.Sleep(max(0, time.Until(lease.ExpiresAt)) + 30*time.Millisecond)
			if err := blocker.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, state.ErrConflict) {
				t.Fatalf("expired %s committed after receipt wait: %v", action, err)
			}
			var status string
			if err := pool.QueryRow(ctx, "select state from managed_postgres_checkpoint_maintenance where id=$1", r.ID).Scan(&status); err != nil || status != r.State {
				t.Fatalf("expired %s changed phase: %q %v", action, status, err)
			}
		})
	}
}
