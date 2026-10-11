//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func pgWorkloadRetirementFixture(t *testing.T, phase string) (*state.PgStore, *pgxpool.Pool, state.ProjectEnvironmentCloneLease) {
	t.Helper()
	s, pool, l := pgCaptureRetirementFixture(t, "adopted")
	if phase == "unmaterialized" {
		return s, pool, l
	}
	ctx := t.Context()
	views, err := s.ProjectEnvironmentCloneWorkloads(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil || len(views) != 1 {
		t.Fatal("frozen workloads", err)
	}
	view := views[0]
	var raw []byte
	if err = pool.QueryRow(ctx, "SELECT snapshot->'settings' FROM project_environment_clone_workloads WHERE operation_id=$1 AND app_id=$2", l.Operation.ID, view.AppID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var settings state.ProjectEnvironmentWorkloadSettings
	if err = json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	hash, err := state.WorkloadSettingsHash(settings)
	if err != nil {
		t.Fatal(err)
	}
	envID, specID := uuid.NewString(), uuid.NewString()
	// Reconstruct only original catalogue receipts. No VM or native data adapter
	// is dispatched by this fixture; unknown runtime outcomes remain owner-bound.
	if _, err = pool.Exec(ctx, "INSERT INTO project_environments(id,account_id,project_id,slug) VALUES($1,$2,$3,$4)", envID, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.TargetEnvironment); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "INSERT INTO project_environment_workload_specs(id,environment_id,app_id,revision,config_hash,settings) VALUES($1,$2,$3,1,$4,$5)", specID, envID, view.AppID, hash, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "INSERT INTO project_environment_workload_heads(environment_id,app_id,spec_id) VALUES($1,$2,$3)", envID, view.AppID, specID); err != nil {
		t.Fatal(err)
	}
	heads, err := json.Marshal(map[string]map[string]string{view.AppID: {"spec_id": specID, "hash": hash}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "INSERT INTO project_environment_clone_materializations(operation_id,environment_id,workload_settings) VALUES($1,$2,$3)", l.Operation.ID, envID, heads); err != nil {
		t.Fatal(err)
	}
	if phase == "deployed" {
		targetID := uuid.NewString()
		if _, err = pool.Exec(ctx, `INSERT INTO deployments SELECT (jsonb_populate_record(NULL::deployments,to_jsonb(d)||jsonb_build_object('id',$1::text,'scope',$2::text,'reason',$3::text,'status','pending','traffic_percent',0,'revision',d.revision+1))).* FROM deployments d WHERE d.id=$4`, targetID, l.Operation.TargetEnvironment, "environment-clone:"+l.Operation.ID, view.SourceDeploymentID); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, "INSERT INTO project_environment_workload_deployment_specs(deployment_id,spec_id) VALUES($1,$2)", targetID, specID); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, "UPDATE project_environment_clone_workloads SET target_deployment_id=$3,target_settings_hash=$4 WHERE operation_id=$1 AND app_id=$2", l.Operation.ID, view.AppID, targetID, hash); err != nil {
			t.Fatal(err)
		}
	}
	return s, pool, l
}

func pgWorkloadRetirementProof(p state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation {
	o := state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation{RequestID: state.ProjectEnvironmentCloneCheckpointWorkloadRetirementRequestID(p), PlanSHA256: state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(p), OwnerToken: p.OperationID, AllDeploymentRequestsPermanentlyFenced: true, AllSchedulerOperationsClosed: true, NoOwnedInstances: true, NoOwnedCgroups: true, NoOwnedNetworkResources: true, NoOwnedRoutes: true, NoNativePublication: true, SourceUnchanged: true, RetiredAt: time.Now().UTC().Truncate(time.Microsecond), Workloads: []state.ProjectEnvironmentCloneCheckpointRetiredWorkload{}}
	for _, w := range p.Workloads {
		o.Workloads = append(o.Workloads, state.ProjectEnvironmentCloneCheckpointRetiredWorkload{AppID: w.AppID, TargetDeploymentID: w.TargetDeploymentID, DeploymentPermanentlyFenced: w.TargetDeploymentID != "", AllRuntimeOperationsClosed: w.TargetDeploymentID != ""})
	}
	return o
}

func TestPgCheckpointWorkloadRetirementJournalRecoveryAndReplay(t *testing.T) {
	for _, phase := range []string{"unmaterialized", "materialized", "deployed"} {
		t.Run(phase, func(t *testing.T) {
			s, pool, l := pgWorkloadRetirementFixture(t, phase)
			ctx := t.Context()
			original, err := s.ProjectEnvironmentCloneOperationByID(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil || in.State != "planned" {
				t.Fatal("reserve original ownership", err)
			}
			if (in.Plan.EnvironmentID != "") != (phase != "unmaterialized") || (in.Plan.Workloads[0].TargetDeploymentID != "") != (phase == "deployed") {
				t.Fatal("wrong original plan", in.Plan)
			}
			replay, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil || !reflect.DeepEqual(in, replay) {
				t.Fatal("reservation changed", err)
			}
			in, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil || !dispatch {
				t.Fatal("first claim", err)
			}
			old := l
			if _, err = pool.Exec(ctx, "UPDATE project_environment_clone_operations SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", l.Operation.ID); err != nil {
				t.Fatal(err)
			}
			l, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, old); !errors.Is(err, state.ErrConflict) || dispatch {
				t.Fatal("stale worker gained authority", err)
			}
			in, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil || dispatch || in.State != "requested" {
				t.Fatal("lost claim was dispatched again", err)
			}
			o := pgWorkloadRetirementProof(in.Plan)
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, old, o); !errors.Is(err, state.ErrConflict) {
				t.Fatal("stale proof accepted", err)
			}
			bad := o
			bad.AllSchedulerOperationsClosed = false
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, bad); !errors.Is(err, state.ErrConflict) {
				t.Fatal("queued scheduler work ignored", err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(cancelled, l, o); err == nil {
				t.Fatal("cancelled finish accepted")
			}
			if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_workload_retirement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='retired' THEN RAISE EXCEPTION 'injected retirement failure' USING ERRCODE='40001'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_workload_retirement BEFORE UPDATE ON project_environment_clone_checkpoint_workload_retirements FOR EACH ROW EXECUTE FUNCTION reject_workload_retirement();`); err != nil {
				t.Fatal(err)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, o); err == nil {
				t.Fatal("failed mutation accepted")
			}
			replay, err = s.ProjectEnvironmentCloneCheckpointWorkloadRetirementForLease(ctx, l)
			if err != nil || replay.State != "requested" || replay.ObservationSHA256 != "" {
				t.Fatal("failed finish committed proof", err)
			}
			if _, err = pool.Exec(ctx, "DROP TRIGGER reject_workload_retirement ON project_environment_clone_checkpoint_workload_retirements; DROP FUNCTION reject_workload_retirement();"); err != nil {
				t.Fatal(err)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, o); err != nil {
				t.Fatal("finish", err)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, o); err != nil {
				t.Fatal("terminal replay", err)
			}
			replay, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil || dispatch || replay.State != "retired" {
				t.Fatal("terminal replay gained dispatch", err)
			}
			changed := o
			changed.RetiredAt = changed.RetiredAt.Add(time.Microsecond)
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, changed); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed replay accepted", err)
			}
			after, err := s.ProjectEnvironmentCloneOperationByID(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
			original.Revision = l.Operation.Revision // handoff intentionally advances revision
			original.UpdatedAt = after.UpdatedAt
			if err != nil || !reflect.DeepEqual(original, after) {
				t.Fatal("native proof changed operation or resources", err)
			}
			if phase != "unmaterialized" {
				var exists bool
				if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM project_environments WHERE id=$1)", in.Plan.EnvironmentID).Scan(&exists); err != nil || !exists {
					t.Fatal("native proof deleted configuration", err)
				}
			}
			if _, err = pool.Exec(ctx, "UPDATE project_environment_clone_checkpoint_workload_retirements SET observation=jsonb_set(observation,'{NoOwnedInstances}','false'::jsonb) WHERE operation_id=$1", l.Operation.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.ProjectEnvironmentCloneCheckpointWorkloadRetirementForLease(ctx, l); !errors.Is(err, state.ErrConflict) {
				t.Fatal("corrupted native proof accepted", err)
			}
		})
	}
}

func TestPgCheckpointWorkloadRetirementRejectsChangedOwnership(t *testing.T) {
	for _, fault := range []string{"settings", "deployment", "materialization", "source", "published", "orphan"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
			ctx := t.Context()
			in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "settings":
				_, err = pool.Exec(ctx, "UPDATE project_environment_workload_specs SET config_hash=$2 WHERE id=$1", in.Plan.Workloads[0].SpecID, strings.Repeat("a", 64))
			case "deployment":
				_, err = pool.Exec(ctx, "UPDATE deployments SET reason='foreign-owner' WHERE id=$1", in.Plan.Workloads[0].TargetDeploymentID)
			case "materialization":
				_, err = pool.Exec(ctx, "UPDATE project_environment_clone_materializations SET created_at=created_at+interval '1 second' WHERE operation_id=$1", l.Operation.ID)
			case "source":
				_, err = pool.Exec(ctx, "UPDATE project_environment_clone_checkpoint_source_compensations SET observation=jsonb_set(observation,'{CapturePermanentlyFenced}','false'::jsonb) WHERE operation_id=$1", l.Operation.ID)
			case "published":
				_, err = pool.Exec(ctx, "UPDATE project_environment_clone_operations SET target_release_set_id=$2 WHERE id=$1", l.Operation.ID, uuid.NewString())
			case "orphan":
				_, err = pool.Exec(ctx, "UPDATE project_environment_clone_workloads SET target_deployment_id=NULL,target_settings_hash='' WHERE operation_id=$1", l.Operation.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed reservation accepted", fault, err)
			}
			if _, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l); !errors.Is(err, state.ErrConflict) || dispatch {
				t.Fatal("changed ownership granted dispatch", fault, err)
			}
		})
	}
}

func TestPgCheckpointWorkloadRetirementMigrationOwnership(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "unmaterialized")
	raw, err := migrations.FS.ReadFile("20261011232006392_clone_checkpoint_workload_retirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	dropEmptyConfigurationCleanupForParentMigration(t, tx)
	for _, statement := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), statement); err != nil {
			t.Fatal("empty migration replay", err)
		}
	}
	_ = tx.Rollback(t.Context())
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"planned", "requested", "retired"} {
		if status != "planned" {
			in, _, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
			if err != nil {
				t.Fatal(err)
			}
			if status == "retired" {
				if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l, pgWorkloadRetirementProof(in.Plan)); err != nil {
					t.Fatal(err)
				}
			}
		}
		tx, err = pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(t.Context(), parts[1])
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_workload_retirements_down_no_ownership" {
			t.Fatal("owned migration downgrade", status, err)
		}
		_ = tx.Rollback(t.Context())
	}
}
