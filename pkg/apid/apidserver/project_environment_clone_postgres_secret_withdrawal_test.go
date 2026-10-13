//go:build !no_pg

// adr: 590
package apidserver

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Seed only original materialization metadata before withdrawal. These tests
// dispatch no VMs; production saves ownership inside original materialization.
func checkpointPgWithdrawalOwnershipFixture(t *testing.T, f *cloneCoordinatorFixture) string {
	t.Helper()
	op := f.lease.Operation
	envID := uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO project_environments(id,account_id,project_id,slug) VALUES($1,$2,$3,$4)", envID, op.AccountID, op.ProjectID, op.TargetEnvironment); err != nil {
		t.Fatal(err)
	}
	views, err := f.store.ProjectEnvironmentCloneWorkloads(t.Context(), op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	heads := map[string]map[string]string{}
	for _, w := range views {
		var raw []byte
		if err = f.pool.QueryRow(t.Context(), "SELECT snapshot->'settings' FROM project_environment_clone_workloads WHERE operation_id=$1 AND app_id=$2", op.ID, w.AppID).Scan(&raw); err != nil {
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
		specID := uuid.NewString()
		if _, err = f.pool.Exec(t.Context(), "INSERT INTO project_environment_workload_specs(id,environment_id,app_id,revision,config_hash,settings) VALUES($1,$2,$3,1,$4,$5)", specID, envID, w.AppID, hash, raw); err != nil {
			t.Fatal(err)
		}
		if _, err = f.pool.Exec(t.Context(), "INSERT INTO project_environment_workload_heads(environment_id,app_id,spec_id) VALUES($1,$2,$3)", envID, w.AppID, specID); err != nil {
			t.Fatal(err)
		}
		heads[w.AppID] = map[string]string{"spec_id": specID, "hash": hash}
	}
	rawHeads, err := json.Marshal(heads)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), "INSERT INTO project_environment_clone_materializations(operation_id,environment_id,workload_settings) VALUES($1,$2,$3)", op.ID, envID, rawHeads); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), "INSERT INTO app_secrets(account_id,app_id,scope,key,ciphertext) VALUES($1,$2,$3,'TOKEN','sealed-original')", op.AccountID, f.apps[0].ID, op.TargetEnvironment); err != nil {
		t.Fatal(err)
	}
	raw, err := sqlc.New().ReadCloneConfigurationOwnershipSnapshot(t.Context(), f.pool, sqlc.ReadCloneConfigurationOwnershipSnapshotParams{ProjectID: pgtype.UUID{Bytes: uuid.MustParse(op.ProjectID), Valid: true}, Environment: op.TargetEnvironment, EnvironmentID: pgtype.UUID{Bytes: uuid.MustParse(envID), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]string
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), "INSERT INTO project_environment_clone_configuration_ownership(operation_id,environment_id,manifest,manifest_sha256) VALUES($1,$2,$3,$4)", op.ID, envID, raw, cloneCheckpointDigest(manifest)); err != nil {
		t.Fatal(err)
	}
	return envID
}

func TestPGClonePostgresSecretWithdrawalOwnershipAndRecovery(t *testing.T) {
	for _, fault := range []string{"none", "write", "changed", "external", "runtime", "stale", "legacy"} {
		t.Run(fault, func(t *testing.T) {
			f, s, d, _ := checkpointPgPublicationFixture(t)
			var err error
			f.lease, err = f.srv.publishCheckpointPgBindings(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal(err)
			}
			published := assertCheckpointPgPublication(t, f, s, "published")
			envID := ""
			var originalOwner sqlc.ProjectEnvironmentCloneConfigurationOwnership
			if fault != "legacy" {
				envID = checkpointPgWithdrawalOwnershipFixture(t, f)
				originalOwner, err = sqlc.New().ReadCloneConfigurationOwnership(t.Context(), f.pool, pgtype.UUID{Bytes: uuid.MustParse(f.lease.Operation.ID), Valid: true})
				if err != nil {
					t.Fatal(err)
				}
			}
			compensateCheckpointCredentialFixture(t, f, s.checkpointCredentialFailureStore)
			switch fault {
			case "write":
				_, err = f.pool.Exec(t.Context(), `CREATE FUNCTION reject_secret_withdrawal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure' USING ERRCODE='40001'; END $$; CREATE TRIGGER reject_secret_withdrawal BEFORE INSERT ON project_environment_clone_postgres_secret_withdrawals FOR EACH ROW EXECUTE FUNCTION reject_secret_withdrawal();`)
			case "changed":
				_, err = f.pool.Exec(t.Context(), "UPDATE app_secrets SET ciphertext='changed' WHERE app_id=$1 AND scope=$2 AND key='TOKEN'", f.apps[0].ID, f.lease.Operation.TargetEnvironment)
			case "external":
				_, err = f.pool.Exec(t.Context(), "INSERT INTO environment_external_field_owners(environment_id,resource,field_path,manager_id) VALUES($1,'environment','configuration/example','terraform')", envID)
			case "stale":
				f.lease.Token = uuid.NewString()
			case "runtime":
				node, nodeErr := s.PgStore.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
				if nodeErr != nil {
					t.Fatal(nodeErr)
				}
				var deploymentID string
				if err = f.pool.QueryRow(t.Context(), "SELECT source_deployment_id FROM project_environment_clone_workloads WHERE operation_id=$1 AND app_id=$2", f.lease.Operation.ID, published.Plan.Secrets[0].AppID).Scan(&deploymentID); err != nil {
					t.Fatal(err)
				}
				instance, createErr := s.PgStore.CreateInstance(t.Context(), published.Plan.Secrets[0].AppID, deploymentID, string(state.StateColdBooting), 256, node.ID, "")
				if createErr != nil {
					t.Fatal(createErr)
				}
				secret := published.Plan.Secrets[0]
				_, err = f.pool.Exec(t.Context(), "INSERT INTO app_secret_runtime_reload_observations(app_id,scope,key,instance_id,secret_version,projection,signal,observed_at) VALUES($1,$2,$3,$4,1,'unchanged','not_attempted',clock_timestamp())", secret.AppID, secret.Scope, secret.Key, instance.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease)
			if fault != "none" && fault != "legacy" {
				if err == nil || (fault != "write" && !errors.Is(err, state.ErrConflict)) {
					t.Fatal("unsafe withdrawal accepted", fault, err)
				}
				var status string
				if err = f.pool.QueryRow(t.Context(), "SELECT state FROM project_environment_clone_checkpoint_pg_publications WHERE operation_id=$1", f.lease.Operation.ID).Scan(&status); err != nil || status != "published" {
					t.Fatal("partial publication withdrawal", status, err)
				}
				for _, secret := range published.Plan.Secrets {
					var n int
					if err = f.pool.QueryRow(t.Context(), "SELECT count(*) FROM app_secrets WHERE managed_postgres_binding_id=$1", secret.ManagedPostgresBindingID).Scan(&n); err != nil || n != 1 {
						t.Fatal("partial envelope withdrawal", n, err)
					}
				}
				var n int
				if err = f.pool.QueryRow(t.Context(), "SELECT count(*) FROM project_environment_clone_postgres_secret_withdrawals WHERE operation_id=$1", f.lease.Operation.ID).Scan(&n); err != nil || n != 0 {
					t.Fatal("partial withdrawal receipt", n, err)
				}
				if fault != "write" {
					return
				}
				if _, err = f.pool.Exec(t.Context(), "DROP TRIGGER reject_secret_withdrawal ON project_environment_clone_postgres_secret_withdrawals; DROP FUNCTION reject_secret_withdrawal()"); err != nil {
					t.Fatal(err)
				}
				err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease)
			}
			if err != nil {
				t.Fatal("atomic withdrawal", err)
			}
			assertCheckpointPgPublication(t, f, s, "withdrawn")
			if fault == "legacy" {
				var n int
				if err = f.pool.QueryRow(t.Context(), "SELECT count(*) FROM project_environment_clone_postgres_secret_withdrawals WHERE operation_id=$1", f.lease.Operation.ID).Scan(&n); err != nil || n != 0 {
					t.Fatal("legacy ownership backfilled", n, err)
				}
				return
			}
			row, err := sqlc.New().ReadClonePostgresSecretWithdrawal(t.Context(), f.pool, pgtype.UUID{Bytes: uuid.MustParse(f.lease.Operation.ID), Valid: true})
			if err != nil || row.PublicationPlanSha256 != cloneCheckpointDigest(published.Plan) || len(row.OwnershipSha256) != 64 {
				t.Fatal("missing original witness", row, err)
			}
			owner, err := sqlc.New().ReadCloneConfigurationOwnership(t.Context(), f.pool, pgtype.UUID{Bytes: uuid.MustParse(f.lease.Operation.ID), Valid: true})
			if err != nil || !reflect.DeepEqual(originalOwner, owner) {
				t.Fatal("original ownership rewritten", err)
			}
			if strings.Contains(string(row.Manifest), "sealed-original") || strings.Contains(string(row.Manifest), "ciphertext") {
				t.Fatal("receipt exposed sealed values")
			}
			setSecretRecipient, mfaIdentities, hostHMACKey = nil, nil, nil
			reclaimCommonCheckpointWorker(t, f)
			if err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease); err != nil {
				t.Fatal("keyless replay", err)
			}
			replay, err := sqlc.New().ReadClonePostgresSecretWithdrawal(t.Context(), f.pool, pgtype.UUID{Bytes: uuid.MustParse(f.lease.Operation.ID), Valid: true})
			if err != nil || !reflect.DeepEqual(row, replay) {
				t.Fatal("receipt rewritten", err)
			}
			if _, err = f.pool.Exec(t.Context(), "UPDATE app_secrets SET ciphertext='changed' WHERE app_id=$1 AND scope=$2 AND key='TOKEN'", f.apps[0].ID, f.lease.Operation.TargetEnvironment); err != nil {
				t.Fatal(err)
			}
			if err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed post-state granted replay", err)
			}
		})
	}
}

func TestPGClonePostgresSecretWithdrawalRejectsChangedReceipt(t *testing.T) {
	for _, fault := range []string{"ownership", "publication", "manifest", "time", "missing"} {
		t.Run(fault, func(t *testing.T) {
			f, s, d, _ := checkpointPgPublicationFixture(t)
			var err error
			f.lease, err = f.srv.publishCheckpointPgBindings(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal(err)
			}
			checkpointPgWithdrawalOwnershipFixture(t, f)
			compensateCheckpointCredentialFixture(t, f, s.checkpointCredentialFailureStore)
			if err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "ownership":
				_, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_secret_withdrawals SET ownership_sha256=$2 WHERE operation_id=$1", f.lease.Operation.ID, strings.Repeat("a", 64))
			case "publication":
				_, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_secret_withdrawals SET publication_plan_sha256=$2 WHERE operation_id=$1", f.lease.Operation.ID, strings.Repeat("a", 64))
			case "manifest":
				_, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_secret_withdrawals SET manifest_sha256=$2 WHERE operation_id=$1", f.lease.Operation.ID, strings.Repeat("a", 64))
			case "time":
				_, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_secret_withdrawals SET publication_withdrawn_at=publication_withdrawn_at-interval '1 day' WHERE operation_id=$1", f.lease.Operation.ID)
			case "missing":
				_, err = f.pool.Exec(t.Context(), "DELETE FROM project_environment_clone_postgres_secret_withdrawals WHERE operation_id=$1", f.lease.Operation.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed evidence granted replay", fault, err)
			}
		})
	}
}

// An object-only stage still publishes the empty PostgreSQL activation journal.
// Build that journal through production coordinators rather than fabricating it.
func checkpointEmptyPgPublicationFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointObjectVerificationTestStore) {
	t.Helper()
	f, store, objects := checkpointObjectVerificationFixture(t, func(f *cloneCoordinatorFixture, _ state.ObjectBucket) {
		if _, err := f.pool.Exec(t.Context(), "DELETE FROM app_secrets WHERE app_id IN(SELECT id FROM apps WHERE project_id=$1) AND scope='production' AND managed_postgres_binding_id IS NOT NULL", f.lease.Operation.ProjectID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(t.Context(), "DELETE FROM managed_postgres_bindings WHERE app_id IN(SELECT id FROM apps WHERE project_id=$1) AND scope='production'", f.lease.Operation.ProjectID); err != nil {
			t.Fatal(err)
		}
	})
	var err error
	f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, objects)
	if err != nil {
		t.Fatal(err)
	}
	checkpointObjectValueIdentity(t)
	f.lease, _, err = f.srv.prepareCheckpointObjectCredentialValues(t.Context(), f.lease, objects)
	if err != nil {
		t.Fatal(err)
	}
	f.lease, _, err = f.srv.publishCheckpointObjects(t.Context(), f.lease, objects)
	if err != nil {
		t.Fatal(err)
	}
	restore := &checkpointPostgresRestoreTestDriver{checkpointReleaseTestDriver: objects.checkpointReleaseTestDriver}
	isolation := &checkpointIsolationTestDriver{checkpointPostgresRestoreTestDriver: restore, f: f}
	verification := &checkpointVerificationTestDriver{checkpointIsolationTestDriver: isolation}
	runtime := &checkpointRuntimeTestDriver{checkpointVerificationTestDriver: verification}
	credentials := &checkpointCredentialTestDriver{checkpointRuntimeTestDriver: runtime, credentials: make(map[string]state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation)}
	connections := &checkpointPgConnectionTestDriver{checkpointCredentialTestDriver: credentials, endpoints: make(map[string]state.ProjectEnvironmentCloneCheckpointPgEndpointObservation)}
	activation := &checkpointPgActivationTestDriver{checkpointPgConnectionTestDriver: connections}
	f.lease, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, activation)
	if err != nil {
		t.Fatal("empty activation", err)
	}
	f.lease, err = f.srv.publishCheckpointPgBindings(t.Context(), f.lease, activation)
	if err != nil {
		t.Fatal("empty publication", err)
	}
	inputs, err := store.PgStore.ProjectEnvironmentCloneCheckpointPgPublicationInputs(t.Context(), f.lease)
	if err != nil || inputs.Publication == nil || len(inputs.Publication.Plan.Secrets) != 0 {
		t.Fatal("fixture was not empty", err)
	}
	return f, store
}

func TestPGClonePostgresSecretWithdrawalEmptyPublication(t *testing.T) {
	for _, fault := range []string{"none", "write", "changed", "missing", "legacy"} {
		t.Run(fault, func(t *testing.T) {
			f, s := checkpointEmptyPgPublicationFixture(t)
			opID := pgtype.UUID{Bytes: uuid.MustParse(f.lease.Operation.ID), Valid: true}
			var original sqlc.ProjectEnvironmentCloneConfigurationOwnership
			var err error
			if fault != "legacy" {
				checkpointPgWithdrawalOwnershipFixture(t, f)
				original, err = sqlc.New().ReadCloneConfigurationOwnership(t.Context(), f.pool, opID)
				if err != nil {
					t.Fatal(err)
				}
			}
			compensateCheckpointObjectFixture(t, f)
			if fault == "write" {
				if _, err = f.pool.Exec(t.Context(), `CREATE FUNCTION reject_empty_withdrawal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure' USING ERRCODE='40001'; END $$; CREATE TRIGGER reject_empty_withdrawal BEFORE INSERT ON project_environment_clone_postgres_secret_withdrawals FOR EACH ROW EXECUTE FUNCTION reject_empty_withdrawal();`); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "changed" {
				if _, err = f.pool.Exec(t.Context(), "UPDATE app_secrets SET ciphertext='changed' WHERE app_id=$1 AND scope=$2 AND key='TOKEN'", f.apps[0].ID, f.lease.Operation.TargetEnvironment); err != nil {
					t.Fatal(err)
				}
			}
			err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease)
			if fault == "write" || fault == "changed" {
				if err == nil || (fault == "changed" && !errors.Is(err, state.ErrConflict)) {
					t.Fatal("unsafe empty withdrawal", err)
				}
				var status string
				if err = f.pool.QueryRow(t.Context(), "SELECT state FROM project_environment_clone_checkpoint_pg_publications WHERE operation_id=$1", f.lease.Operation.ID).Scan(&status); err != nil || status != "published" {
					t.Fatal("partial withdrawal", status, err)
				}
				if _, err = sqlc.New().ReadClonePostgresSecretWithdrawal(t.Context(), f.pool, opID); err == nil {
					t.Fatal("partial receipt")
				}
				if fault == "changed" {
					return
				}
				if _, err = f.pool.Exec(t.Context(), "DROP TRIGGER reject_empty_withdrawal ON project_environment_clone_postgres_secret_withdrawals; DROP FUNCTION reject_empty_withdrawal()"); err != nil {
					t.Fatal(err)
				}
				err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease)
			}
			if err != nil {
				t.Fatal("empty withdrawal", err)
			}
			if fault == "legacy" {
				if _, err = sqlc.New().ReadClonePostgresSecretWithdrawal(t.Context(), f.pool, opID); err == nil {
					t.Fatal("legacy receipt backfilled")
				}
				return
			}
			receipt, err := sqlc.New().ReadClonePostgresSecretWithdrawal(t.Context(), f.pool, opID)
			if err != nil || receipt.ManifestSha256 != original.ManifestSha256 {
				t.Fatal("empty transition changed manifest", err)
			}
			owner, err := sqlc.New().ReadCloneConfigurationOwnership(t.Context(), f.pool, opID)
			if err != nil || !reflect.DeepEqual(original, owner) {
				t.Fatal("original ownership changed", err)
			}
			if fault == "missing" {
				if _, err = f.pool.Exec(t.Context(), "DELETE FROM project_environment_clone_postgres_secret_withdrawals WHERE operation_id=$1", f.lease.Operation.ID); err != nil {
					t.Fatal(err)
				}
				if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
					t.Fatal("missing empty receipt accepted", err)
				}
				if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
					t.Fatal("object withdrawal accepted missing predecessor", err)
				}
				return
			}
			setSecretRecipient, mfaIdentities, hostHMACKey = nil, nil, nil
			reclaimCommonCheckpointWorker(t, f)
			if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
				t.Fatal("object-only chain", err)
			}
			if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease); err != nil {
				t.Fatal("keyless empty replay", err)
			}
			replay, err := sqlc.New().ReadClonePostgresSecretWithdrawal(t.Context(), f.pool, opID)
			if err != nil || !reflect.DeepEqual(receipt, replay) {
				t.Fatal("receipt rewritten", err)
			}
		})
	}
}

func TestPGClonePostgresSecretWithdrawalRejectsInventedEmptyRoster(t *testing.T) {
	f, s, d, _ := checkpointPgPublicationFixture(t)
	var err error
	f.lease, err = f.srv.publishCheckpointPgBindings(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	checkpointPgWithdrawalOwnershipFixture(t, f)
	in, err := s.PgStore.ProjectEnvironmentCloneCheckpointPgActivationForLease(t.Context(), f.lease)
	if err != nil || len(in.Plan.Bindings) == 0 {
		t.Fatal("fixture has no bindings", err)
	}
	in.Plan.Bindings = nil
	in.Observation.PlanSHA256 = state.ProjectEnvironmentCloneCheckpointPgActivationPlanSHA256(in.Plan)
	in.Observation.AllowedBindingsSHA256 = state.ProjectEnvironmentCloneCheckpointPgActivationBindingsSHA256(in.Plan)
	digestObservation := in.Observation
	digestObservation.HealthyThrough = time.Time{}
	observationSHA := cloneCheckpointDigest(digestObservation)
	rawPlan, err := json.Marshal(in.Plan)
	if err != nil {
		t.Fatal(err)
	}
	rawObservation, err := json.Marshal(in.Observation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_checkpoint_pg_activations SET plan=$2,observation=$3,observation_sha256=$4 WHERE operation_id=$1", f.lease.Operation.ID, rawPlan, rawObservation, observationSHA); err != nil {
		t.Fatal(err)
	}
	// Keep the forged publication internally consistent with the forged activation.
	var rawPublication []byte
	if err = f.pool.QueryRow(t.Context(), "SELECT plan FROM project_environment_clone_checkpoint_pg_publications WHERE operation_id=$1", f.lease.Operation.ID).Scan(&rawPublication); err != nil {
		t.Fatal(err)
	}
	var publication state.ProjectEnvironmentCloneCheckpointPgPublicationPlan
	if err = json.Unmarshal(rawPublication, &publication); err != nil {
		t.Fatal(err)
	}
	publication.Secrets = nil
	publication.ActivationPlanSHA256 = in.Observation.PlanSHA256
	publication.ActivationObservationSHA256 = observationSHA
	rawPublication, err = json.Marshal(publication)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_checkpoint_pg_publications SET plan=$2,plan_sha256=$3 WHERE operation_id=$1", f.lease.Operation.ID, rawPublication, cloneCheckpointDigest(publication)); err != nil {
		t.Fatal(err)
	}
	compensateCheckpointObjectFixture(t, f)
	if err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
		t.Fatal("invented empty roster accepted", err)
	}
	var status string
	if err = f.pool.QueryRow(t.Context(), "SELECT state FROM project_environment_clone_checkpoint_pg_publications WHERE operation_id=$1", f.lease.Operation.ID).Scan(&status); err != nil || status != "published" {
		t.Fatal("forged withdrawal committed", status, err)
	}
}
