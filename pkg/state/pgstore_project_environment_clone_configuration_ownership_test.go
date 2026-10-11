//go:build !no_pg

// adr: 590
package state_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func configurationOwnershipFixture(t *testing.T) (*state.PgStore, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, string) {
	t.Helper()
	s, ctx, pool := pgWithPool(t)
	a, err := s.CreateAccount(ctx, "ownership@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "ownership"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "service", WorkloadName: "service", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:ownership"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(ctx, d.ID, "/ownership.ext4", "layers/ownership.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertAppEnvInScope(ctx, a.ID, app.ID, "production", "VARIABLE", "private-original-value"); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertAppSecretWithKidAndValueHashInScope(ctx, a.ID, app.ID, "production", "TOKEN", "age1-test", "1111111111111111", []byte("private-sealed-value")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutProjectEnvironmentEdgePolicy(ctx, cloneWorkloadEdgePolicy(a.ID, p.ID, app.ID, "production", "private-policy-value")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateCapturedProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneCaptureRequest{AccountID: a.ID, ProjectID: p.ID, SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "ownership"}); err != nil {
		t.Fatal(err)
	}
	l, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// Phase setup only: this fixture exercises the actual original materialization
	// transaction, without dispatching any native capture, data copy or VM work.
	if _, err = pool.Exec(ctx, "UPDATE project_environment_clone_operations SET status='copying' WHERE id=$1", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	l.Operation, err = s.ProjectEnvironmentCloneOperationByID(ctx, a.ID, p.ID, l.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, pool, l, app.ID
}

func TestPgCloneConfigurationOwnershipAtomicReplayAndTampering(t *testing.T) {
	limits, _ := api.LimitsFor(api.PlanPro)
	for _, fault := range []string{"none", "variable", "secret", "policy", "receipt", "extra"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, appID := configurationOwnershipFixture(t)
			env, err := s.MaterializeProjectEnvironmentCloneForLease(t.Context(), l, nil, 0, limits)
			if err != nil {
				t.Fatal("original materialization", err)
			}
			var raw []byte
			var digest string
			if err = pool.QueryRow(t.Context(), "SELECT manifest,manifest_sha256 FROM project_environment_clone_configuration_ownership WHERE operation_id=$1", l.Operation.ID).Scan(&raw, &digest); err != nil {
				t.Fatal(err)
			}
			var manifest map[string]string
			if err = json.Unmarshal(raw, &manifest); err != nil || len(manifest) != 11 || len(digest) != 64 {
				t.Fatal("incomplete ownership receipt", err)
			}
			for _, value := range []string{"private-original-value", "private-sealed-value", "private-policy-value", "age1-test"} {
				if strings.Contains(string(raw), value) {
					t.Fatal("receipt exposed configuration")
				}
			}
			replay, err := s.MaterializeProjectEnvironmentCloneForLease(t.Context(), l, nil, 0, limits)
			if err != nil || replay.ID != env.ID {
				t.Fatal("lost materialization reply", err)
			}
			switch fault {
			case "variable":
				_, err = pool.Exec(t.Context(), "UPDATE app_envs SET value='changed' WHERE app_id=$1 AND scope='stage'", appID)
			case "secret":
				_, err = pool.Exec(t.Context(), "UPDATE app_secrets SET kid='changed' WHERE app_id=$1 AND scope='stage'", appID)
			case "policy":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_edge_policies SET updated_at=updated_at+interval '1 second' WHERE app_id=$1 AND environment_slug='stage'", appID)
			case "receipt":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_configuration_ownership SET manifest_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "extra":
				_, err = pool.Exec(t.Context(), "INSERT INTO app_envs(account_id,app_id,scope,key,value) VALUES($1,$2,'stage','EXTRA','foreign')", l.Operation.AccountID, appID)
			}
			if err != nil {
				t.Fatal(err)
			}
			replay, err = s.MaterializeProjectEnvironmentCloneForLease(t.Context(), l, nil, 0, limits)
			if fault == "none" {
				if err != nil || replay.ID != env.ID {
					t.Fatal(err)
				}
			} else if !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed original ownership accepted", err)
			}
			var after []byte
			if err = pool.QueryRow(t.Context(), "SELECT manifest FROM project_environment_clone_configuration_ownership WHERE operation_id=$1", l.Operation.ID).Scan(&after); err != nil || !reflect.DeepEqual(raw, after) {
				t.Fatal("replay adopted changed rows", err)
			}
		})
	}
}

func TestPgCloneConfigurationOwnershipWriteRollbackAndForeignScope(t *testing.T) {
	limits, _ := api.LimitsFor(api.PlanPro)
	for _, fault := range []string{"write", "foreign", "stale"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, appID := configurationOwnershipFixture(t)
			var err error
			if fault == "write" {
				_, err = pool.Exec(t.Context(), `CREATE FUNCTION reject_configuration_ownership() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected ownership failure' USING ERRCODE='40001'; END $$; CREATE TRIGGER reject_configuration_ownership BEFORE INSERT ON project_environment_clone_configuration_ownership FOR EACH ROW EXECUTE FUNCTION reject_configuration_ownership();`)
			}
			if fault == "foreign" {
				retired, createErr := s.CreateApp(t.Context(), state.App{AccountID: l.Operation.AccountID, ProjectID: l.Operation.ProjectID, Slug: "retired-service", WorkloadName: "retired-service", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
				if createErr != nil {
					t.Fatal(createErr)
				}
				appID = retired.ID
				if _, err = pool.Exec(t.Context(), "UPDATE apps SET status='deleted' WHERE id=$1", appID); err != nil {
					t.Fatal(err)
				}
				_, err = pool.Exec(t.Context(), "INSERT INTO app_envs(account_id,app_id,scope,key,value) VALUES($1,$2,'stage','FOREIGN','foreign-owned')", l.Operation.AccountID, appID)
			}
			if fault == "stale" {
				l.Token = uuid.NewString()
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.MaterializeProjectEnvironmentCloneForLease(t.Context(), l, nil, 0, limits); err == nil {
				t.Fatal("unsafe materialization committed")
			}
			var exists bool
			if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM project_environments WHERE project_id=$1 AND slug='stage') OR EXISTS(SELECT 1 FROM project_environment_clone_configuration_ownership WHERE operation_id=$2) OR EXISTS(SELECT 1 FROM project_environment_clone_materializations WHERE operation_id=$2)", l.Operation.ProjectID, l.Operation.ID).Scan(&exists); err != nil || exists {
				t.Fatal("partial materialization survived", err)
			}
			if fault == "foreign" {
				var value string
				if err = pool.QueryRow(t.Context(), "SELECT value FROM app_envs WHERE app_id=$1 AND scope='stage' AND key='FOREIGN'", appID).Scan(&value); err != nil || value != "foreign-owned" {
					t.Fatal("foreign configuration changed", err)
				}
			}
		})
	}
}

func TestPgCloneConfigurationOwnershipMigrationRefusesEvidence(t *testing.T) {
	limits, _ := api.LimitsFor(api.PlanPro)
	s, pool, l, _ := configurationOwnershipFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006394_clone_configuration_ownership_receipts.sql")
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
	for _, sql := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			t.Fatal("empty migration replay", err)
		}
	}
	_ = tx.Rollback(t.Context())
	if _, err = s.MaterializeProjectEnvironmentCloneForLease(t.Context(), l, nil, 0, limits); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_configuration_ownership_down_no_evidence" {
		t.Fatal("owned migration downgrade", err)
	}
	_ = tx.Rollback(t.Context())
}

func TestPgCloneConfigurationOwnershipPinsOriginalRetirement(t *testing.T) {
	for _, fault := range []string{"time", "missing", "configuration", "digest"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l := pgWorkloadRetirementFixture(t, "materialized")
			var envID string
			if err := pool.QueryRow(t.Context(), "SELECT environment_id FROM project_environment_clone_materializations WHERE operation_id=$1", l.Operation.ID).Scan(&envID); err != nil {
				t.Fatal(err)
			}
			raw, err := sqlc.New().ReadCloneConfigurationOwnershipSnapshot(t.Context(), pool, sqlc.ReadCloneConfigurationOwnershipSnapshotParams{ProjectID: pgtype.UUID{Bytes: uuid.MustParse(l.Operation.ProjectID), Valid: true}, Environment: l.Operation.TargetEnvironment, EnvironmentID: pgtype.UUID{Bytes: uuid.MustParse(envID), Valid: true}})
			if err != nil {
				t.Fatal(err)
			}
			var manifest map[string]string
			if err = json.Unmarshal(raw, &manifest); err != nil {
				t.Fatal(err)
			}
			// Seed original ownership as part of this metadata-only fixture, before
			// reserving any retirement authority. Production never backfills receipts.
			if _, err = pool.Exec(t.Context(), "INSERT INTO project_environment_clone_configuration_ownership(operation_id,environment_id,manifest,manifest_sha256) VALUES($1,$2,$3,$4)", l.Operation.ID, envID, raw, captureRetirementDigest(t, manifest)); err != nil {
				t.Fatal(err)
			}
			in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "time":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_configuration_ownership SET created_at=created_at-interval '1 second' WHERE operation_id=$1", l.Operation.ID)
			case "missing":
				_, err = pool.Exec(t.Context(), "DELETE FROM project_environment_clone_configuration_ownership WHERE operation_id=$1", l.Operation.ID)
			case "configuration":
				_, err = pool.Exec(t.Context(), "INSERT INTO app_envs(account_id,app_id,scope,key,value) VALUES($1,$2,$3,'FOREIGN','foreign')", l.Operation.AccountID, in.Plan.Workloads[0].AppID, l.Operation.TargetEnvironment)
			case "digest":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_configuration_ownership SET manifest_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); !errors.Is(err, state.ErrConflict) || dispatch {
				t.Fatal("changed ownership granted native authority", fault, err)
			}
		})
	}
}
