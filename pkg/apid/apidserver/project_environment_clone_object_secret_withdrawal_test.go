//go:build !no_pg

// adr: 590
package apidserver

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPGCloneObjectSecretWithdrawalOwnershipAndRecovery(t *testing.T) {
	for _, fault := range []string{"none", "write", "changed", "external", "runtime", "stale", "legacy"} {
		t.Run(fault, func(t *testing.T) {
			f, s, plan := checkpointObjectWithdrawalFixture(t)
			var err error
			var secret state.AppSecret
			for _, bucket := range plan.Buckets {
				for _, preparation := range bucket.Preparations {
					if len(preparation.Secrets) > 0 {
						secret = preparation.Secrets[0]
					}
				}
			}
			if secret.Key == "" {
				t.Fatal("fixture has no envelopes")
			}
			envID := ""
			var originalOwner sqlc.ProjectEnvironmentCloneConfigurationOwnership
			if fault != "legacy" {
				envID = checkpointPgWithdrawalOwnershipFixture(t, f)
				originalOwner, err = sqlc.New().ReadCloneConfigurationOwnership(t.Context(), f.pool, pgtype.UUID{Bytes: uuid.MustParse(f.lease.Operation.ID), Valid: true})
				if err != nil {
					t.Fatal(err)
				}
			}

			switch fault {
			case "write":
				_, err = f.pool.Exec(t.Context(), `CREATE FUNCTION reject_secret_withdrawal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure' USING ERRCODE='40001'; END $$; CREATE TRIGGER reject_secret_withdrawal BEFORE INSERT ON project_environment_clone_object_secret_withdrawals FOR EACH ROW EXECUTE FUNCTION reject_secret_withdrawal();`)
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
				if err = f.pool.QueryRow(t.Context(), "SELECT source_deployment_id FROM project_environment_clone_workloads WHERE operation_id=$1 AND app_id=$2", f.lease.Operation.ID, secret.AppID).Scan(&deploymentID); err != nil {
					t.Fatal(err)
				}
				instance, createErr := s.PgStore.CreateInstance(t.Context(), secret.AppID, deploymentID, string(state.StateColdBooting), 256, node.ID, "")
				if createErr != nil {
					t.Fatal(createErr)
				}
				_, err = f.pool.Exec(t.Context(), "INSERT INTO app_secret_runtime_reload_observations(app_id,scope,key,instance_id,secret_version,projection,signal,observed_at) VALUES($1,$2,$3,$4,1,'unchanged','not_attempted',clock_timestamp())", secret.AppID, secret.Scope, secret.Key, instance.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease)
			if fault != "none" && fault != "legacy" {
				if err == nil || (fault != "write" && !errors.Is(err, state.ErrConflict)) {
					t.Fatal("unsafe withdrawal accepted", fault, err)
				}
				var status string
				if err = f.pool.QueryRow(t.Context(), "SELECT state FROM project_environment_clone_checkpoint_object_publications WHERE operation_id=$1", f.lease.Operation.ID).Scan(&status); err != nil || status != "published" {
					t.Fatal("partial publication withdrawal", status, err)
				}
				assertCheckpointObjectWithdrawalState(t, f, plan, false)
				var n int
				if err = f.pool.QueryRow(t.Context(), "SELECT count(*) FROM project_environment_clone_object_secret_withdrawals WHERE operation_id=$1", f.lease.Operation.ID).Scan(&n); err != nil || n != 0 {
					t.Fatal("partial withdrawal receipt", n, err)
				}
				if fault != "write" {
					return
				}
				if _, err = f.pool.Exec(t.Context(), "DROP TRIGGER reject_secret_withdrawal ON project_environment_clone_object_secret_withdrawals; DROP FUNCTION reject_secret_withdrawal()"); err != nil {
					t.Fatal(err)
				}
				err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease)
			}
			if err != nil {
				t.Fatal("atomic withdrawal", err)
			}
			assertCheckpointObjectWithdrawalState(t, f, plan, true)
			if fault == "legacy" {
				var n int
				if err = f.pool.QueryRow(t.Context(), "SELECT count(*) FROM project_environment_clone_object_secret_withdrawals WHERE operation_id=$1", f.lease.Operation.ID).Scan(&n); err != nil || n != 0 {
					t.Fatal("legacy ownership backfilled", n, err)
				}
				return
			}
			row, err := sqlc.New().ReadCloneObjectSecretWithdrawal(t.Context(), f.pool, pgtype.UUID{Bytes: uuid.MustParse(f.lease.Operation.ID), Valid: true})
			if err != nil || row.PublicationPlanSha256 != plan.SHA256 || len(row.OwnershipSha256) != 64 {
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
			if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
				t.Fatal("keyless replay", err)
			}
			replay, err := sqlc.New().ReadCloneObjectSecretWithdrawal(t.Context(), f.pool, pgtype.UUID{Bytes: uuid.MustParse(f.lease.Operation.ID), Valid: true})
			if err != nil || !reflect.DeepEqual(row, replay) {
				t.Fatal("receipt rewritten", err)
			}
			if _, err = f.pool.Exec(t.Context(), "UPDATE app_secrets SET ciphertext='changed' WHERE app_id=$1 AND scope=$2 AND key='TOKEN'", f.apps[0].ID, f.lease.Operation.TargetEnvironment); err != nil {
				t.Fatal(err)
			}
			if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed post-state granted replay", err)
			}
		})
	}
}

func TestPGCloneObjectSecretWithdrawalRejectsChangedReceipt(t *testing.T) {
	for _, fault := range []string{"ownership", "publication", "manifest", "prior", "postgres", "time", "missing"} {
		t.Run(fault, func(t *testing.T) {
			f, s, _ := checkpointObjectWithdrawalFixture(t)
			var err error
			checkpointPgWithdrawalOwnershipFixture(t, f)

			if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "ownership":
				_, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_object_secret_withdrawals SET ownership_sha256=$2 WHERE operation_id=$1", f.lease.Operation.ID, strings.Repeat("a", 64))
			case "publication":
				_, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_object_secret_withdrawals SET publication_plan_sha256=$2 WHERE operation_id=$1", f.lease.Operation.ID, strings.Repeat("a", 64))
			case "manifest":
				_, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_object_secret_withdrawals SET manifest_sha256=$2 WHERE operation_id=$1", f.lease.Operation.ID, strings.Repeat("a", 64))
			case "prior":
				_, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_object_secret_withdrawals SET prior_manifest_sha256=$2 WHERE operation_id=$1", f.lease.Operation.ID, strings.Repeat("a", 64))
			case "postgres":
				_, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_object_secret_withdrawals SET postgres_withdrawal_sha256=$2 WHERE operation_id=$1", f.lease.Operation.ID, strings.Repeat("a", 64))
			case "time":
				_, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_object_secret_withdrawals SET publication_withdrawn_at=publication_withdrawn_at-interval '1 day' WHERE operation_id=$1", f.lease.Operation.ID)
			case "missing":
				_, err = f.pool.Exec(t.Context(), "DELETE FROM project_environment_clone_object_secret_withdrawals WHERE operation_id=$1", f.lease.Operation.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed evidence granted replay", fault, err)
			}
		})
	}
}

func TestPGCloneObjectSecretWithdrawalChainsPostgresReceipt(t *testing.T) {
	f, s, pg, _ := checkpointPgPublicationFixture(t)
	var err error
	f.lease, err = f.srv.publishCheckpointPgBindings(t.Context(), f.lease, pg)
	if err != nil {
		t.Fatal(err)
	}
	target := &checkpointTargetTestDriver{checkpointReleaseTestDriver: pg.checkpointReleaseTestDriver, f: f, native: make(map[string]state.ProjectEnvironmentCloneCheckpointObjectTargetObservation)}
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, target)
	if err != nil {
		t.Fatal(err)
	}
	copyDriver := &checkpointObjectCopyTestDriver{checkpointTargetTestDriver: target, proofs: make(map[string]state.ProjectEnvironmentCloneCheckpointObjectCopyObservation)}
	f.lease, err = f.srv.copyCheckpointObjects(t.Context(), f.lease, copyDriver)
	if err != nil {
		t.Fatal(err)
	}
	objects := &checkpointObjectVerificationTestDriver{checkpointTargetTestDriver: target, proofs: make(map[string]state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation)}
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
	checkpointPgWithdrawalOwnershipFixture(t, f)
	compensateCheckpointObjectFixture(t, f)
	if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
		t.Fatal("object withdrawal preceded PostgreSQL", err)
	}
	if err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease); err != nil {
		t.Fatal("PostgreSQL withdrawal", err)
	}
	if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
		t.Fatal("object withdrawal", err)
	}
	row, err := sqlc.New().ReadCloneObjectSecretWithdrawal(t.Context(), f.pool, pgtype.UUID{Bytes: uuid.MustParse(f.lease.Operation.ID), Valid: true})
	if err != nil || len(row.PostgresWithdrawalSha256) != 64 {
		t.Fatal("missing predecessor", err)
	}
	setSecretRecipient, mfaIdentities, hostHMACKey = nil, nil, nil
	reclaimCommonCheckpointWorker(t, f)
	if err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease); err != nil {
		t.Fatal("PostgreSQL chain replay", err)
	}
	if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
		t.Fatal("object chain replay", err)
	}
	if _, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_secret_withdrawals SET recorded_at=recorded_at+interval '1 microsecond' WHERE operation_id=$1", f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed predecessor replay", err)
	}
}

func TestPGCloneObjectSecretWithdrawalWithoutEnvelopes(t *testing.T) {
	f, s, objects := checkpointObjectVerificationFixture(t, func(f *cloneCoordinatorFixture, _ state.ObjectBucket) {
		if _, err := f.pool.Exec(t.Context(), "DELETE FROM app_secrets WHERE app_id IN(SELECT id FROM apps WHERE project_id=$1) AND scope='production' AND managed_object_storage_credential_id IS NOT NULL", f.lease.Operation.ProjectID); err != nil {
			t.Fatal(err)
		}
		// Convert the original managed credentials into standalone credentials,
		// so the captured catalogue truthfully has no app-secret bindings.
		// Credential ownership is immutable in production, so forge it with
		// triggers off.
		tx, err := f.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if _, err := tx.Exec(t.Context(), "SET LOCAL session_replication_role = replica"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), "UPDATE object_storage_s3_credentials SET managed_app_id=NULL,managed_scope=NULL,managed_prefix=NULL WHERE bucket_id IN(SELECT b.id FROM object_buckets b JOIN apps a ON a.id=b.app_id WHERE a.project_id=$1 AND b.scope='production')", f.lease.Operation.ProjectID); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil {
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
	var publication state.ProjectEnvironmentCloneCheckpointObjectPublication
	f.lease, publication, err = f.srv.publishCheckpointObjects(t.Context(), f.lease, objects)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range publication.Plan.Buckets {
		for _, preparation := range bucket.Preparations {
			if len(preparation.Secrets) != 0 {
				t.Fatal("fixture retained envelopes")
			}
		}
	}
	checkpointPgWithdrawalOwnershipFixture(t, f)
	compensateCheckpointObjectFixture(t, f)
	if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
		t.Fatal(err)
	}
	row, err := sqlc.New().ReadCloneObjectSecretWithdrawal(t.Context(), f.pool, pgtype.UUID{Bytes: uuid.MustParse(f.lease.Operation.ID), Valid: true})
	if err != nil || row.PriorManifestSha256 != row.ManifestSha256 {
		t.Fatal("unchanged configuration lost ownership", err)
	}
	if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
		t.Fatal("empty transition replay", err)
	}
	if _, err = f.pool.Exec(t.Context(), "DELETE FROM project_environment_clone_object_secret_withdrawals WHERE operation_id=$1", f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
		t.Fatal("missing empty transition receipt accepted", err)
	}
}
