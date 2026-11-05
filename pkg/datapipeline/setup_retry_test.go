package datapipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestSetupRetryRefreshesFailedEvidenceAndKeepsCooldown(t *testing.T) {
	for _, recent := range []bool{false, true} {
		name := "after cooldown"
		if recent {
			name = "during cooldown"
		}
		t.Run(name, func(t *testing.T) {
			service, store, account, connection, request := setupFixture(t)
			ctx := t.Context()
			if _, err := store.pool.Exec(ctx, `UPDATE data_connection_checks SET state='blocked' WHERE connection_id=$1`, connection.ID); err != nil {
				t.Fatal(err)
			}
			job, err := service.StartSetup(ctx, account, request)
			if err != nil {
				t.Fatal(err)
			}
			job = setupTick(t, service, store, job.ID)
			if job.State != "blocked" || job.Reason != "connection_check_blocked" {
				t.Fatal("fixture did not fail its source check", job)
			}
			if !recent {
				if _, err = store.pool.Exec(ctx, `UPDATE data_connection_checks SET created_at=clock_timestamp()-interval '1 minute' WHERE connection_id=$1`, connection.ID); err != nil {
					t.Fatal(err)
				}
			}
			before, err := store.Check(ctx, account, connection.ID)
			if err != nil || !before.Fresh {
				t.Fatal(before, err)
			}
			for _, bad := range []struct {
				account  string
				revision int64
			}{{uuid.NewString(), job.Revision}, {account, job.Revision + 1}} {
				if _, err = store.ControlSetup(ctx, bad.account, job.ID, bad.revision, "retry"); !errors.Is(err, ErrConflict) {
					t.Fatal("foreign or stale retry accepted", err)
				}
				if check, err := store.Check(ctx, account, connection.ID); err != nil || !check.Fresh || check.ID != before.ID {
					t.Fatal("rejected retry changed source evidence", check, err)
				}
			}
			if _, err = store.ControlSetup(ctx, account, job.ID, job.Revision, "retry"); err != nil {
				t.Fatal(err)
			}
			job = setupTick(t, service, store, job.ID)
			check, err := store.Check(ctx, account, connection.ID)
			if err != nil || job.State != "working" || check.CreatedAt == "" {
				t.Fatal("retry became blocked on cached failure", job, check, err)
			}
			if recent {
				if check.ID != before.ID || check.CreatedAt != before.CreatedAt || check.Fresh {
					t.Fatal("retry bypassed cooldown or changed historical timestamps", before, check)
				}
				if _, err = store.pool.Exec(ctx, `UPDATE data_connection_checks SET created_at=clock_timestamp()-interval '1 minute' WHERE connection_id=$1`, connection.ID); err != nil {
					t.Fatal(err)
				}
				job = setupTick(t, service, store, job.ID)
				check, err = store.Check(ctx, account, connection.ID)
			}
			if err != nil || check.ID == before.ID || check.State != "pending" {
				t.Fatal("retry did not request a fresh check", check, err)
			}
			finishSetupCheck(t, store, account, connection, "")
			job = setupTick(t, service, store, job.ID)
			if job.State != "working" || job.Phase != "provisioning" {
				t.Fatal("fresh evidence did not resume the saved setup", job)
			}
		})
	}
}

func TestSetupRetryPreservesNewerPassedAndRunningChecks(t *testing.T) {
	for _, state := range []string{"passed", "running"} {
		t.Run(state, func(t *testing.T) {
			service, store, account, connection, request := setupFixture(t)
			ctx := t.Context()
			job, err := service.StartSetup(ctx, account, request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.pool.Exec(ctx, `UPDATE data_pipeline_setups SET state='blocked',reason='connection_check_blocked' WHERE id=$1`, job.ID); err != nil {
				t.Fatal(err)
			}
			if state == "running" {
				if _, err = store.pool.Exec(ctx, `UPDATE data_connection_checks SET state='pending',observed_at=NULL WHERE connection_id=$1`, connection.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = store.ClaimCheck(ctx, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			}
			before, err := store.Check(ctx, account, connection.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.ControlSetup(ctx, account, job.ID, job.Revision, "retry"); err != nil {
				t.Fatal(err)
			}
			after, err := store.Check(ctx, account, connection.ID)
			if err != nil || before.ID != after.ID || before.ObservedAt != after.ObservedAt || before.LeaseToken != after.LeaseToken || before.State != after.State {
				t.Fatal("retry discarded newer or active work", before, after, err)
			}
		})
	}
}

func TestSetupRetryRefreshesNegativeCapacityForExactDestination(t *testing.T) {
	for _, phase := range []string{"checking_source", "checking_mirror", "provisioning"} {
		t.Run(phase, func(t *testing.T) {
			service, store, account, connection, request := setupFixture(t)
			ctx := t.Context()
			job, err := service.StartSetup(ctx, account, request)
			if err != nil {
				t.Fatal(err)
			}
			database := ""
			if phase == "checking_mirror" {
				database = job.DatabaseID
				if _, err = store.pool.Exec(ctx, `UPDATE data_pipeline_setups SET phase='checking_mirror' WHERE id=$1`, job.ID); err != nil {
					t.Fatal(err)
				}
				finishSetupCheck(t, store, account, connection, database)
			}
			if _, err = store.pool.Exec(ctx, `UPDATE data_connection_checks SET result=jsonb_set(result,'{capacity,source,state}','"needs_qualification"') WHERE connection_id=$1`, connection.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = store.pool.Exec(ctx, `UPDATE data_pipeline_setups SET phase=$2,state='blocked',reason='capacity_needs_qualification' WHERE id=$1`, job.ID, phase); err != nil {
				t.Fatal(err)
			}
			if _, err = store.ControlSetup(ctx, account, job.ID, job.Revision, "retry"); err != nil {
				t.Fatal(err)
			}
			check, err := store.Check(ctx, account, connection.ID)
			if err != nil || check.Fresh || check.DatabaseID != database {
				t.Fatal("negative allocation was reused", check, err)
			}
			if _, err = time.Parse(time.RFC3339Nano, check.CreatedAt); err != nil || check.ConnectionRevision != connection.Revision || len(check.Tables) != 1 || check.Tables[0].Name != "orders" || check.Capacity == nil || check.Capacity.Source.State != "needs_qualification" {
				t.Fatal("retry altered immutable evidence instead of freshness", check, err)
			}
		})
	}
}

func TestSetupRejectsUnavailableExistingMirrorBeforeReservation(t *testing.T) {
	service, store, account, _, request := setupFixture(t)
	request.DatabaseID = uuid.NewString()
	service.MirrorState = func(_ context.Context, _, _ string) (string, error) { return "", ErrNotFound }
	if _, err := service.StartSetup(t.Context(), account, request); !errors.Is(err, ErrNotFound) {
		t.Fatal("unowned destination was reserved", err)
	}
	jobs, err := store.Setups(t.Context(), account)
	if err != nil || len(jobs) != 0 {
		t.Fatal("failed admission saved a reservation", jobs, err)
	}
}

func TestSetupProtectsCheckFromConflictingManualRequest(t *testing.T) {
	service, store, account, connection, request := setupFixture(t)
	ctx := t.Context()
	job, err := service.StartSetup(ctx, account, request)
	if err != nil {
		t.Fatal(err)
	}
	// An earlier source check may already be in flight when setup advances.
	if _, err = store.pool.Exec(ctx, `UPDATE data_connection_checks SET state='pending',observed_at=NULL WHERE connection_id=$1`, connection.ID); err != nil {
		t.Fatal(err)
	}
	old, err := store.ClaimCheck(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipeline_setups SET phase='checking_mirror' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	job = setupTick(t, service, store, job.ID)
	if job.State != "working" || job.Phase != "checking_mirror" {
		t.Fatal("setup failed rather than waiting for the in-flight check", job)
	}
	if _, err = service.RequestCheck(ctx, account, connection.ID, api.RequestDataConnectionCheck{ExpectedRevision: connection.Revision}); !errors.Is(err, ErrCheckBusy) {
		t.Fatalf("manual check should wait: %v", err)
	}
	if err = store.FinishCheck(ctx, old, api.DataConnectionCheck{State: "passed"}); err != nil {
		t.Fatalf("setup invalidated the old check: %v", err)
	}
	job = setupTick(t, service, store, job.ID)
	current, err := store.ClaimCheck(ctx, uuid.NewString())
	if err != nil || current.DatabaseID != job.DatabaseID {
		t.Fatal(current, err)
	}
	if _, err = service.RequestCheck(ctx, account, connection.ID, api.RequestDataConnectionCheck{ExpectedRevision: connection.Revision}); !errors.Is(err, ErrCheckBusy) {
		t.Fatalf("manual check displaced the setup: %v", err)
	}
	now := time.Now().UTC()
	endpoint := &api.DataEndpointCapacity{State: "ready", UsableConnections: 100, RequiredConnections: 5, ObservedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(api.DataPipelineCapacityMaxAge).Format(time.RFC3339Nano)}
	result := api.DataConnectionCheck{State: "passed", Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}, Capacity: &api.DataPipelineCapacity{State: "ready", Source: endpoint, Destination: endpoint}}
	if err = store.FinishCheck(ctx, current, result); err != nil {
		t.Fatalf("manual check invalidated setup's lease: %v", err)
	}
	if _, err = service.RequestCheck(ctx, account, connection.ID, api.RequestDataConnectionCheck{ExpectedRevision: connection.Revision}); !errors.Is(err, ErrCheckBusy) {
		t.Fatalf("completed evidence was displaced before setup used it: %v", err)
	}
	job = setupTick(t, service, store, job.ID)
	if job.State != "working" || job.Phase != "starting" {
		t.Fatal("setup did not consume its check", job)
	}
	if _, err = service.RequestCheck(ctx, account, connection.ID, api.RequestDataConnectionCheck{ExpectedRevision: connection.Revision}); !errors.Is(err, ErrCheckBusy) {
		t.Fatalf("evidence was displaced before pipeline creation: %v", err)
	}
	job = setupTick(t, service, store, job.ID)
	if job.State != "working" || job.Phase != "syncing" || job.PipelineID == "" {
		t.Fatal("setup did not create the pipeline", job)
	}
	if _, err = service.RequestCheck(ctx, account, connection.ID, api.RequestDataConnectionCheck{ExpectedRevision: connection.Revision}); err != nil {
		t.Fatalf("check slot stayed reserved after pipeline creation: %v", err)
	}
}
