// adr: 20261019072523 — managed PostgreSQL credential receipts admit enforced candidates.
package pgintegration_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type candidatePostgresTestStore interface {
	candidateBindingTestStore
	state.EnvironmentQualificationPostgresStore
	PutManagedPostgresSecret(context.Context, state.AppSecret) error
	GetAppSecretInScope(context.Context, string, string, string, string) (*state.AppSecret, error)
}

// The PostgreSQL store needs real binding rows behind managed secrets, so
// these tests keep the raw pool alongside the store.
func postgresQualificationStores(t *testing.T, run func(*testing.T, gitOpsTestStore, *pgxpool.Pool)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { run(t, state.NewMemStore(), nil) })
	t.Run("postgres", func(t *testing.T) {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(context.Background(), pool); err != nil {
			t.Fatal(err)
		}
		run(t, state.NewPgStore(pool), pool)
	})
}

// seedCandidatePostgresBinding creates one ready managed binding and its
// sealed delivery in the candidate scope and returns its delivery version.
func seedCandidatePostgresBinding(t *testing.T, store candidatePostgresTestStore, pool *pgxpool.Pool, accountID, appID, key, access string) (string, int64) {
	t.Helper()
	bindingID := uuid.NewString()
	if pool != nil {
		databaseID := uuid.NewString()
		if _, err := pool.Exec(t.Context(), `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint) VALUES($1,$2,$3,'eu',17,'development','single_zone','test',$4)`,
			databaseID, accountID, "db-"+bindingID[:8], strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), `INSERT INTO managed_postgres_bindings(id,account_id,database_id,app_id,scope,environment_key,access) VALUES($1,$2,$3,$4,'production',$5,$6)`,
			bindingID, accountID, databaseID, appID, key, access); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), `UPDATE managed_postgres_bindings SET state='ready',provider_identity_id='identity',credential_ref='ref' WHERE id=$1`, bindingID); err != nil {
			t.Fatal(err)
		}
	}
	secret := state.AppSecret{AccountID: accountID, AppID: appID, Scope: "production", Key: key, ManagedPostgresBindingID: bindingID,
		ManagedPostgresAccess: access, ManagedCredentialRef: "ref-" + bindingID, ManagedCredentialGeneration: 1, Kid: "kid",
		Ciphertext: []byte("sealed"), ValueHash: "0123456789abcdef"}
	if err := store.PutManagedPostgresSecret(t.Context(), secret); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetAppSecretInScope(t.Context(), accountID, appID, "production", key)
	if err != nil || stored.DeliveryVersion < 1 {
		t.Fatal("managed delivery version", err)
	}
	return bindingID, stored.DeliveryVersion
}

func httpCandidate(t *testing.T, requests []state.EnvironmentWorkloadQualificationRequest) state.EnvironmentWorkloadQualificationRequest {
	t.Helper()
	for _, r := range requests {
		if r.Resource != "workload/worker" {
			return r
		}
	}
	t.Fatal("no HTTP candidate")
	return state.EnvironmentWorkloadQualificationRequest{}
}

func assertCandidatesHeld(t *testing.T, basic gitOpsTestStore, requests []state.EnvironmentWorkloadQualificationRequest) {
	t.Helper()
	for _, r := range requests {
		d, err := basic.(state.Store).DeploymentByID(t.Context(), r.DeploymentID)
		if err != nil || !d.EnvironmentWorkloadHeld() || d.Status != state.DeploySnapshotting {
			t.Fatal("failed activation unheld a candidate", err)
		}
	}
}

func TestCandidateBindingPostgresReceiptsQualifyRuntimeAndMigration(t *testing.T) {
	postgresQualificationStores(t, func(t *testing.T, basic gitOpsTestStore, pool *pgxpool.Pool) {
		lease, plan, requests, placement := candidateBindingFixture(t, basic, "", "")
		store := basic.(candidatePostgresTestStore)
		api := httpCandidate(t, requests)
		runtimeID, runtimeVersion := seedCandidatePostgresBinding(t, store, pool, lease.Source.AccountID, api.AppID, "DATABASE_URL", "read_write")
		migrationID, _ := seedCandidatePostgresBinding(t, store, pool, lease.Source.AccountID, api.AppID, "DATABASE_MIGRATION_URL", "migration")
		completeCandidateBindingCohortWithSecrets(t, store, requests, placement, true,
			map[string]map[string]int64{api.AppID: {"production/DATABASE_URL": runtimeVersion}})
		if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); active || !state.IsBindingReleaseRequired(err) {
			t.Fatalf("unverified PostgreSQL credentials activated: %v %v", active, err)
		}
		assertCandidatesHeld(t, basic, requests)
		checks, err := store.PendingEnvironmentQualificationPostgresChecks(t.Context(), api.GraphID)
		if err != nil || len(checks) != 2 {
			t.Fatalf("pending checks: %+v %v", checks, err)
		}
		accesses := map[string]string{}
		for _, c := range checks {
			if c.RequestID != api.ID || c.Attempt != api.Attempt || c.AppID != api.AppID || c.Scope != "production" || c.CredentialGeneration != 1 || len(c.Ciphertext) == 0 {
				t.Fatalf("check is not bound to the candidate delivery: %+v", c)
			}
			accesses[c.BindingID] = c.Access
		}
		if accesses[runtimeID] != "read_write" || accesses[migrationID] != "migration" {
			t.Fatalf("checks: %+v", accesses)
		}
		stale := checks[0]
		stale.DeliveryVersion++
		if err := store.RecordEnvironmentQualificationPostgresReceipt(t.Context(), stale); !errors.Is(err, state.ErrBindingPromotionChanged) {
			t.Fatal("receipt accepted a different delivery", err)
		}
		for _, c := range checks {
			if err := store.RecordEnvironmentQualificationPostgresReceipt(t.Context(), c); err != nil {
				t.Fatal(err)
			}
		}
		if again, err := store.PendingEnvironmentQualificationPostgresChecks(t.Context(), api.GraphID); err != nil || len(again) != 0 {
			t.Fatalf("verified deliveries still pending: %+v %v", again, err)
		}
		release, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
		if err != nil || !active || len(release.Members) != 2 {
			t.Fatalf("checked activation with PostgreSQL receipts: %+v %v %v", release, active, err)
		}
	})
}

// rotateCandidatePostgresBinding delivers a new credential generation, which
// is a different delivery than the one the receipt verified.
func rotateCandidatePostgresBinding(t *testing.T, store candidatePostgresTestStore, pool *pgxpool.Pool, accountID, appID, bindingID, key, access string) {
	t.Helper()
	if pool != nil {
		if _, err := pool.Exec(t.Context(), `UPDATE managed_postgres_bindings SET credential_generation=2 WHERE id=$1`, bindingID); err != nil {
			t.Fatal(err)
		}
	}
	rotated := state.AppSecret{AccountID: accountID, AppID: appID, Scope: "production", Key: key,
		ManagedPostgresBindingID: bindingID, ManagedPostgresAccess: access, ManagedCredentialRef: "ref2-" + bindingID,
		ManagedCredentialGeneration: 2, Kid: "kid", Ciphertext: []byte("sealed-2"), ValueHash: "fedcba9876543210"}
	if err := store.PutManagedPostgresSecret(t.Context(), rotated); err != nil {
		t.Fatal(err)
	}
}

func TestCandidateBindingPostgresRotationAfterReceiptStaysHeld(t *testing.T) {
	for _, rotate := range []string{"runtime", "migration"} {
		t.Run(rotate, func(t *testing.T) {
			postgresQualificationStores(t, func(t *testing.T, basic gitOpsTestStore, pool *pgxpool.Pool) {
				lease, plan, requests, placement := candidateBindingFixture(t, basic, "", "")
				store := basic.(candidatePostgresTestStore)
				api := httpCandidate(t, requests)
				runtimeID, version := seedCandidatePostgresBinding(t, store, pool, lease.Source.AccountID, api.AppID, "DATABASE_URL", "read_write")
				migrationID, _ := seedCandidatePostgresBinding(t, store, pool, lease.Source.AccountID, api.AppID, "DATABASE_MIGRATION_URL", "migration")
				completeCandidateBindingCohortWithSecrets(t, store, requests, placement, true,
					map[string]map[string]int64{api.AppID: {"production/DATABASE_URL": version}})
				checks, err := store.PendingEnvironmentQualificationPostgresChecks(t.Context(), api.GraphID)
				if err != nil || len(checks) != 2 {
					t.Fatalf("pending checks: %+v %v", checks, err)
				}
				for _, c := range checks {
					if err := store.RecordEnvironmentQualificationPostgresReceipt(t.Context(), c); err != nil {
						t.Fatal(err)
					}
				}
				// A delivered credential change also changes the app's runtime
				// configuration, so the candidate's qualification stops counting.
				if rotate == "runtime" {
					rotateCandidatePostgresBinding(t, store, pool, lease.Source.AccountID, api.AppID, runtimeID, "DATABASE_URL", "read_write")
				} else {
					rotateCandidatePostgresBinding(t, store, pool, lease.Source.AccountID, api.AppID, migrationID, "DATABASE_MIGRATION_URL", "migration")
				}
				if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); active {
					t.Fatalf("rotated %s credential kept qualification authority: %v", rotate, err)
				}
				assertCandidatesHeld(t, basic, requests)
			})
		})
	}
}

func TestCandidateBindingPostgresUnsupportedAccessStaysHeld(t *testing.T) {
	postgresQualificationStores(t, func(t *testing.T, basic gitOpsTestStore, pool *pgxpool.Pool) {
		lease, plan, requests, placement := candidateBindingFixture(t, basic, "", "")
		store := basic.(candidatePostgresTestStore)
		api := httpCandidate(t, requests)
		_, version := seedCandidatePostgresBinding(t, store, pool, lease.Source.AccountID, api.AppID, "GREGALE_DATA_API_URL", "data_api")
		completeCandidateBindingCohortWithSecrets(t, store, requests, placement, true,
			map[string]map[string]int64{api.AppID: {"production/GREGALE_DATA_API_URL": version}})
		if checks, err := store.PendingEnvironmentQualificationPostgresChecks(t.Context(), api.GraphID); err != nil || len(checks) != 0 {
			t.Fatalf("unsupported access was offered for verification: %+v %v", checks, err)
		}
		if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); active || !state.IsBindingReleaseRequired(err) {
			t.Fatalf("Data API binding activated under enforcement: %v %v", active, err)
		}
		assertCandidatesHeld(t, basic, requests)
	})
}

// Without a secret write the candidate stays qualified, so only the
// receipt's generation pin can catch a binding that moved on.
func TestPgCandidatePostgresBindingDriftBlocksActivation(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	lease, plan, requests, placement := candidateBindingFixture(t, store, "", "")
	api := httpCandidate(t, requests)
	_, version := seedCandidatePostgresBinding(t, store, pool, lease.Source.AccountID, api.AppID, "DATABASE_URL", "read_write")
	migrationID, _ := seedCandidatePostgresBinding(t, store, pool, lease.Source.AccountID, api.AppID, "DATABASE_MIGRATION_URL", "migration")
	completeCandidateBindingCohortWithSecrets(t, store, requests, placement, true,
		map[string]map[string]int64{api.AppID: {"production/DATABASE_URL": version}})
	checks, err := store.PendingEnvironmentQualificationPostgresChecks(t.Context(), api.GraphID)
	if err != nil || len(checks) != 2 {
		t.Fatalf("pending checks: %+v %v", checks, err)
	}
	for _, c := range checks {
		if err := store.RecordEnvironmentQualificationPostgresReceipt(t.Context(), c); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(t.Context(), `UPDATE managed_postgres_bindings SET credential_generation=2 WHERE id=$1`, migrationID); err != nil {
		t.Fatal(err)
	}
	if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); active || !state.IsBindingReleaseRequired(err) {
		t.Fatalf("binding generation drift kept qualification authority: %v %v", active, err)
	}
	assertCandidatesHeld(t, store, requests)
}

func TestPgCandidatePostgresReceiptsAreImmutable(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	lease, _, requests, placement := candidateBindingFixture(t, store, "", "")
	api := httpCandidate(t, requests)
	_, version := seedCandidatePostgresBinding(t, store, pool, lease.Source.AccountID, api.AppID, "DATABASE_URL", "read_write")
	completeCandidateBindingCohortWithSecrets(t, store, requests, placement, true,
		map[string]map[string]int64{api.AppID: {"production/DATABASE_URL": version}})
	checks, err := store.PendingEnvironmentQualificationPostgresChecks(t.Context(), api.GraphID)
	if err != nil || len(checks) != 1 {
		t.Fatalf("pending checks: %+v %v", checks, err)
	}
	if err := store.RecordEnvironmentQualificationPostgresReceipt(t.Context(), checks[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE environment_qualification_postgres_receipts SET recorded_at=clock_timestamp() WHERE request_id=$1", api.ID); err == nil {
		t.Fatal("credential receipt was mutable")
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM environment_qualification_postgres_receipts WHERE request_id=$1", api.ID); err == nil {
		t.Fatal("credential receipt was deletable")
	}
	source, err := os.ReadFile("../../../migrations/20261019073452862_candidate_postgres_binding_qualification.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing downgrade")
	}
	if _, err := pool.Exec(t.Context(), down); err == nil {
		t.Fatal("downgrade discarded credential receipt history")
	}
}
