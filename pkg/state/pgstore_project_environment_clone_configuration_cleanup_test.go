//go:build !no_pg

// adr: 590
package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func configurationCleanupFixture(t *testing.T, owned bool, managed ...bool) (*state.PgStore, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, string) {
	t.Helper()
	return configurationCleanupFixtureForPhase(t, owned, "deployed", managed...)
}

func configurationCleanupFixtureForPhase(t *testing.T, owned bool, phase string, managed ...bool) (*state.PgStore, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, string) {
	t.Helper()
	s, pool, l := pgWorkloadRetirementFixture(t, phase)
	var envID, appID string
	if err := pool.QueryRow(t.Context(), "SELECT m.environment_id,w.app_id FROM project_environment_clone_materializations m JOIN project_environment_clone_workloads w USING(operation_id) WHERE m.operation_id=$1", l.Operation.ID).Scan(&envID, &appID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "INSERT INTO app_envs(account_id,app_id,scope,key,value) VALUES($1,$2,$3,'ORIGINAL','copied')", l.Operation.AccountID, appID, l.Operation.TargetEnvironment); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "INSERT INTO app_secrets(account_id,app_id,scope,key,ciphertext) VALUES($1,$2,$3,'TOKEN','sealed')", l.Operation.AccountID, appID, l.Operation.TargetEnvironment); err != nil {
		t.Fatal(err)
	}
	if len(managed) > 0 && managed[0] {
		secret := clonePostgresSecretFixture(t, pool, state.Account{ID: l.Operation.AccountID}, state.App{ID: appID}, l.Operation.TargetEnvironment, "retained-managed", 1)
		if err := s.PutManagedPostgresSecret(t.Context(), secret); err != nil {
			t.Fatal(err)
		}
	}
	if owned {
		raw, err := sqlc.New().ReadCloneConfigurationOwnershipSnapshot(t.Context(), pool, sqlc.ReadCloneConfigurationOwnershipSnapshotParams{ProjectID: pgtype.UUID{Bytes: uuid.MustParse(l.Operation.ProjectID), Valid: true}, Environment: l.Operation.TargetEnvironment, EnvironmentID: pgtype.UUID{Bytes: uuid.MustParse(envID), Valid: true}})
		if err != nil {
			t.Fatal(err)
		}
		var manifest map[string]string
		if err = json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		// Original metadata-only fixture setup; production has no backfill API.
		if _, err = pool.Exec(t.Context(), "INSERT INTO project_environment_clone_configuration_ownership(operation_id,environment_id,manifest,manifest_sha256) VALUES($1,$2,$3,$4)", l.Operation.ID, envID, raw, captureRetirementDigest(t, manifest)); err != nil {
			t.Fatal(err)
		}
	}
	return s, pool, l, appID
}

func retireConfigurationCleanupFixture(t *testing.T, s *state.PgStore, l state.ProjectEnvironmentCloneLease) {
	t.Helper()
	in, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
	if err != nil || !dispatch {
		t.Fatal("native claim", err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l, pgWorkloadRetirementProof(in.Plan)); err != nil {
		t.Fatal("native closure", err)
	}
}

func TestPgCloneConfigurationCleanupAtomicRecovery(t *testing.T) {
	for _, fault := range []string{"none", "write", "foreign", "stale", "legacy", "unretired", "external"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, appID := configurationCleanupFixture(t, fault != "legacy")
			if _, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			if fault != "unretired" {
				retireConfigurationCleanupFixture(t, s, l)
			}
			var err error
			switch fault {
			case "write":
				_, err = pool.Exec(t.Context(), `CREATE FUNCTION reject_configuration_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected cleanup receipt failure' USING ERRCODE='40001'; END $$; CREATE TRIGGER reject_configuration_cleanup BEFORE INSERT ON project_environment_clone_configuration_cleanups FOR EACH ROW EXECUTE FUNCTION reject_configuration_cleanup();`)
			case "foreign":
				_, err = pool.Exec(t.Context(), "INSERT INTO app_envs(account_id,app_id,scope,key,value) VALUES($1,$2,$3,'FOREIGN','unowned')", l.Operation.AccountID, appID, l.Operation.TargetEnvironment)
			case "external":
				_, err = pool.Exec(t.Context(), "INSERT INTO environment_external_field_owners(environment_id,resource,field_path,manager_id) SELECT environment_id,'environment','configuration/example','terraform' FROM project_environment_clone_materializations WHERE operation_id=$1", l.Operation.ID)
			case "stale":
				l.Token = uuid.NewString()
			}
			if err != nil {
				t.Fatal(err)
			}
			err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l)
			if fault == "none" && err != nil {
				t.Fatal("cleanup", err)
			}
			if fault != "none" && err == nil {
				t.Fatal("unsafe deletion accepted")
			}
			var original, secret, receipt bool
			if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM app_envs WHERE app_id=$1 AND scope=$2 AND key='ORIGINAL'),EXISTS(SELECT 1 FROM app_secrets WHERE app_id=$1 AND scope=$2),EXISTS(SELECT 1 FROM project_environment_clone_configuration_cleanups WHERE operation_id=$3)", appID, l.Operation.TargetEnvironment, l.Operation.ID).Scan(&original, &secret, &receipt); err != nil {
				t.Fatal(err)
			}
			if fault != "none" {
				if !original || !secret || receipt {
					t.Fatal("partial deletion survived")
				}
				return
			}
			if original || secret || !receipt {
				t.Fatal("catalogue deletion incomplete")
			}
			if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
				t.Fatal("lost commit reply", err)
			}
			if _, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); err != nil || dispatch {
				t.Fatal("native replay dispatched again", err)
			}
			var retained bool
			if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM project_environment_workload_heads h JOIN project_environment_clone_materializations m ON m.environment_id=h.environment_id WHERE m.operation_id=$1)", l.Operation.ID).Scan(&retained); err != nil || !retained {
				t.Fatal("workload ownership removed", err)
			}
		})
	}
}

func TestPgCloneConfigurationCleanupPostStateAndProofTampering(t *testing.T) {
	for _, fault := range []string{"configuration", "ownership", "plan", "observation", "manifest", "time", "missing", "environment"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, appID := configurationCleanupFixture(t, true)
			if _, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			retireConfigurationCleanupFixture(t, s, l)
			if err := s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			var err error
			switch fault {
			case "configuration":
				_, err = pool.Exec(t.Context(), "INSERT INTO app_envs(account_id,app_id,scope,key,value) VALUES($1,$2,$3,'FOREIGN','unowned')", l.Operation.AccountID, appID, l.Operation.TargetEnvironment)
			case "ownership", "plan", "observation", "manifest":
				column := map[string]string{"ownership": "ownership_sha256", "plan": "native_plan_sha256", "observation": "native_observation_sha256", "manifest": "manifest_sha256"}[fault]
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_configuration_cleanups SET "+column+"=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "time":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_configuration_cleanups SET cleaned_at=cleaned_at-interval '1 day' WHERE operation_id=$1", l.Operation.ID)
			case "missing":
				_, err = pool.Exec(t.Context(), "DELETE FROM project_environment_clone_configuration_cleanups WHERE operation_id=$1", l.Operation.ID)
			case "environment":
				_, err = pool.Exec(t.Context(), "UPDATE project_environments SET protected=true WHERE project_id=$1 AND slug=$2", l.Operation.ProjectID, l.Operation.TargetEnvironment)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed evidence accepted", fault, err)
			}
			if _, err = s.ProjectEnvironmentCloneCheckpointWorkloadRetirementForLease(t.Context(), l); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed cleanup granted native replay", fault, err)
			}
		})
	}
}

func TestPgCloneConfigurationCleanupMigrationRefusesEvidence(t *testing.T) {
	s, pool, l, _ := configurationCleanupFixture(t, true)
	raw, err := migrations.FS.ReadFile("20261011232006397_clone_configuration_cleanup_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	dropEmptyCloneWorkloadCleanupForParentMigration(t, tx)
	for _, sql := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	_ = tx.Rollback(t.Context())
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	retireConfigurationCleanupFixture(t, s, l)
	if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_configuration_cleanup_down_no_evidence" {
		t.Fatal("cleanup evidence downgrade", err)
	}
	_ = tx.Rollback(t.Context())
}

func TestPgCloneConfigurationCleanupRetainsManagedSecretOwnership(t *testing.T) {
	s, pool, l, _ := configurationCleanupFixture(t, true, true)
	if _, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	retireConfigurationCleanupFixture(t, s, l)
	if err := s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); !errors.Is(err, state.ErrConflict) {
		t.Fatal("managed ownership deleted", err)
	}
	var retained bool
	if err := pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM app_secrets s JOIN apps a ON a.id=s.app_id WHERE a.project_id=$1 AND s.scope=$2 AND s.managed_postgres_binding_id IS NOT NULL)", l.Operation.ProjectID, l.Operation.TargetEnvironment).Scan(&retained); err != nil || !retained {
		t.Fatal("managed secret removed", err)
	}
}

// Follow reverse migration order before exercising an older parent table's
// empty rollback/replay; the cleanup FK intentionally preserves its evidence.
func dropEmptyConfigurationCleanupForParentMigration(t *testing.T, tx pgx.Tx) {
	t.Helper()
	rawObject, err := migrations.FS.ReadFile("20261011232006406_clone_object_secret_withdrawal_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(rawObject), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal("dependent object withdrawal downgrade", err)
	}
	rawWithdrawal, err := migrations.FS.ReadFile("20261011232006402_clone_postgres_secret_withdrawal_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(rawWithdrawal), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal("dependent secret withdrawal downgrade", err)
	}
	dropEmptyCloneWorkloadCleanupForParentMigration(t, tx)
	raw, err := migrations.FS.ReadFile("20261011232006397_clone_configuration_cleanup_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := strings.SplitN(string(raw), "-- +goose Down", 2)[1]
	if _, err = tx.Exec(t.Context(), down); err != nil {
		t.Fatal("dependent cleanup downgrade", err)
	}
}
