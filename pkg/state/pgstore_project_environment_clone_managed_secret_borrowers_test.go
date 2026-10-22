//go:build !no_pg

// adr: 590
package state_test

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

func TestPgCloneManagedSecretBorrowersAuthenticationAndRecovery(t *testing.T) {
	for _, kind := range []string{"none", "managed", "legacy", "unretired", "stale", "changed", "proof", "cleaned"} {
		t.Run(kind, func(t *testing.T) {
			s, pool, l, appID := configurationCleanupFixture(t, kind != "legacy", kind == "managed")
			if _, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			if kind != "unretired" {
				retireConfigurationCleanupFixture(t, s, l)
			}
			var err error
			switch kind {
			case "stale":
				l.Token = uuid.NewString()
			case "changed":
				_, err = pool.Exec(t.Context(), "UPDATE app_secrets SET ciphertext='changed' WHERE app_id=$1 AND scope=$2 AND key='TOKEN'", appID, l.Operation.TargetEnvironment)
			case "proof":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_checkpoint_workload_retirements SET observation_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "cleaned":
				err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l)
				if err == nil {
					err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			report, err := s.ProjectEnvironmentCloneManagedSecretBorrowersForLease(t.Context(), l)
			if kind != "none" && kind != "managed" && kind != "cleaned" {
				if !errors.Is(err, state.ErrConflict) || report != (state.ProjectEnvironmentCloneManagedSecretBorrowers{}) {
					t.Fatal("unauthenticated inventory returned", report, err)
				}
				return
			}
			if err != nil || report.OperationID != l.Operation.ID || report.EnvironmentID == "" || len(report.OwnershipSHA256) != 64 || len(report.NativePlanSHA256) != 64 || len(report.NativeObservationSHA256) != 64 {
				t.Fatal("missing original evidence", report, err)
			}
			want := int64(0)
			if kind == "managed" {
				want = 1
				if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); !errors.Is(err, state.ErrConflict) {
					t.Fatal("inventory granted managed-secret deletion", err)
				}
			}
			if report.PostgresSecrets != want || report.CredentialReferences != want || report.ObjectStorageSecrets != 0 || report.RuntimeObservations != 0 {
				t.Fatal("incorrect target-only counts", report)
			}
			replay, err := s.ProjectEnvironmentCloneManagedSecretBorrowersForLease(t.Context(), l)
			if err != nil || !reflect.DeepEqual(report, replay) {
				t.Fatal("inventory changed evidence", replay, err)
			}
		})
	}
}

func TestPgCloneManagedSecretBorrowersRuntimeObservations(t *testing.T) {
	s, pool, l, appID := configurationCleanupFixture(t, true)
	native, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	retireConfigurationCleanupFixture(t, s, l)
	// An observation can borrow a target secret from an instance belonging to
	// another scope. Inventory follows secret scope, not deployment membership.
	instance, err := s.CreateInstance(t.Context(), appID, native.Plan.Workloads[0].SourceDeploymentID, string(state.StateColdBooting), 512, resolveDefaultLocal(t, t.Context(), s), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), "INSERT INTO app_secret_runtime_reload_observations(app_id,scope,key,instance_id,secret_version,projection,signal,observed_at) VALUES($1,$2,'TOKEN',$3,1,'unchanged','not_attempted',clock_timestamp())", appID, l.Operation.TargetEnvironment, instance.ID); err != nil {
		t.Fatal(err)
	}
	report, err := s.ProjectEnvironmentCloneManagedSecretBorrowersForLease(t.Context(), l)
	if err != nil || report.RuntimeObservations != 1 || report.PostgresSecrets != 0 || report.ObjectStorageSecrets != 0 || report.CredentialReferences != 0 {
		t.Fatal("cross-scope observation omitted", report, err)
	}
	if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); !errors.Is(err, state.ErrConflict) {
		t.Fatal("observation cascaded during cleanup", err)
	}
	var n int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM app_secret_runtime_reload_observations WHERE instance_id=$1", instance.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal("inventory or cleanup removed observation", n, err)
	}
}

func TestPgCloneManagedSecretBorrowersObjectEnvelopes(t *testing.T) {
	s, pool, l, appID := configurationCleanupFixture(t, true)
	if _, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	retireConfigurationCleanupFixture(t, s, l)
	bucket := cloneBindingBucket(t, s, state.Account{ID: l.Operation.AccountID}, state.App{ID: appID}, "inventory", l.Operation.TargetEnvironment, true)
	id := uuid.NewString()
	req := state.ObjectS3ComputeBindingCreateRequest{Credential: state.ObjectS3Credential{ID: id, AccountID: l.Operation.AccountID, BucketID: bucket.ID, AccessKeyID: "GRGABBBBBBBBBBBBBBBB", SecretSealed: []byte("sealed"), KID: "kid", Label: "inventory", Permission: "read", Status: "active", ManagedAppID: appID, ManagedScope: l.Operation.TargetEnvironment, ManagedPrefix: "ASSETS"}, MaxCredentialsPerBucket: 10, MaxSecretsPerApp: 20}
	for _, suffix := range []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"} {
		req.Secrets = append(req.Secrets, state.AppSecret{AccountID: l.Operation.AccountID, AppID: appID, Scope: l.Operation.TargetEnvironment, Key: "ASSETS" + suffix, Ciphertext: []byte("sealed"), Kid: "kid", ValueHash: "123456789abcdef0", ManagedObjectStorageCredentialID: id})
	}
	if _, err := s.CreateObjectS3ComputeBinding(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	// The raw query inventories all six envelopes; the authenticated entry point
	// must reject their late introduction instead of adopting new ownership.
	row, err := sqlc.New().ReadCloneManagedSecretConfigurationBorrowers(t.Context(), pool, sqlc.ReadCloneManagedSecretConfigurationBorrowersParams{ProjectID: pgtype.UUID{Bytes: uuid.MustParse(l.Operation.ProjectID), Valid: true}, Environment: l.Operation.TargetEnvironment})
	if err != nil || row.ObjectStorageSecrets != 6 || row.PostgresSecrets != 0 || row.CredentialReferences != 0 || row.RuntimeObservations != 0 {
		t.Fatal("object envelopes omitted", row, err)
	}
	if report, err := s.ProjectEnvironmentCloneManagedSecretBorrowersForLease(t.Context(), l); !errors.Is(err, state.ErrConflict) || report != (state.ProjectEnvironmentCloneManagedSecretBorrowers{}) {
		t.Fatal("adopted late object envelopes", report, err)
	}
}
