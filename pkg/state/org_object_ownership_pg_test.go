//go:build !no_pg

package state_test

// adr: 202610171907

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresOrgObjectOwnership(t *testing.T) {
	orgObjectOwnership(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPostgresOrgObjectQuota(t *testing.T) {
	orgObjectQuota(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPostgresOrgObjectAdoption(t *testing.T) {
	orgObjectAdoption(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPostgresOrgObjectAdoptionBillingConflict(t *testing.T) {
	orgObjectAdoptionBillingConflict(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPostgresOrgObjectAdoptionCreditsStorageCharges(t *testing.T) {
	orgObjectAdoptionCreditsStorageCharges(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPostgresOrgObjectOwnershipBackfill(t *testing.T) {
	for _, scenario := range []string{"retained roots and history", "mixed bucket root", "mixed app binding", "finalized current period"} {
		t.Run(scenario, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			ctx := t.Context()
			raw, err := migrations.FS.ReadFile("20261019190114551_org_object_storage_ownership_online.sql")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), "-- +goose Down") {
				t.Fatal("missing rollback")
			}
			if err := pgtest.ExecMigration(ctx, t, pool, string(raw), false); err != nil {
				t.Fatalf("restore legacy schema: %v", err)
			}
			store := state.NewPgStore(pool)
			f := setupOffboarding(t, store)
			adoptObjectCompany(t, f)
			bucket, human, managed := uuid.NewString(), uuid.NewString(), uuid.NewString()
			namespace := f.actor.ID
			if scenario == "mixed bucket root" {
				namespace = f.owner.ID
			}
			if _, err := pool.Exec(ctx, `INSERT INTO object_buckets(id,account_id,app_id,name,scope,region,backend_id,backend_fingerprint,physical_name,state)
			 VALUES($1,$2,$3,'assets','default','us-east-1','legacy',repeat('a',64),'physical-legacy','ready')`, bucket, namespace, f.app.ID); err != nil {
				t.Fatal(err)
			}
			boundApp := f.app.ID
			if scenario == "mixed app binding" {
				other, err := store.CreateApp(ctx, state.App{AccountID: f.actor.ID, Slug: "personal-binding-target"})
				if err != nil {
					t.Fatal(err)
				}
				boundApp = other.ID
			}
			access, _, err := api.GenerateObjectS3Credential()
			if err != nil {
				t.Fatal(err)
			}
			managedAccess, _, err := api.GenerateObjectS3Credential()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO object_storage_s3_credentials(id,account_id,bucket_id,access_key_id,secret_sealed,kid,label,permission)
			 VALUES($1,$2,$3,$4,'sealed','age1test','legacy human','read_write')`, human, namespace, bucket, access); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO object_storage_s3_credentials(id,account_id,bucket_id,access_key_id,secret_sealed,kid,label,permission,managed_app_id,managed_scope,managed_prefix)
			 VALUES($1,$2,$3,$4,'sealed','age1test','legacy application','read_write',$5,'default','GREGALE_S3_ASSETS')`, managed, namespace, bucket, managedAccess, boundApp); err != nil {
				t.Fatal(err)
			}
			for _, period := range []time.Time{state.ObjectStoragePeriod(time.Now()), state.ObjectStoragePeriod(time.Now()).AddDate(0, -1, 0)} {
				if _, err := pool.Exec(ctx, `INSERT INTO object_storage_usage_reports(account_id,backend_id,backend_fingerprint,source,period_start,observed_at,stored_byte_hours,request_count,egress_bytes,cost_millicents)
				 VALUES($1,'legacy',repeat('a',64),'legacy-provider',$2,$2::timestamptz+interval '1 second',1,2,3,4)`, namespace, period); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `INSERT INTO object_storage_usage_heads(account_id,backend_id,period_start,observed_at)
				 VALUES($1,'legacy',$2,$2::timestamptz+interval '1 second')`, namespace, period); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "finalized current period" {
				period := state.ObjectStoragePeriod(time.Now())
				if _, err := store.RecordObjectStorageBillingPeriod(ctx, state.ObjectStorageBillingRecord{AccountID: namespace, PeriodStart: period, PeriodEnd: period.AddDate(0, 1, 0), Currency: "EUR", FinalizedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			}
			err = pgtest.ExecMigration(ctx, t, pool, string(raw), true)
			if scenario != "retained roots and history" {
				if err == nil || !strings.Contains(err.Error(), "remediate") {
					t.Fatalf("unsafe migration accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("migrate legacy bucket: %v", err)
			}
			var org, physical, issuer string
			if err := pool.QueryRow(ctx, `SELECT org_id::text,physical_name FROM object_buckets WHERE id=$1`, bucket).Scan(&org, &physical); err != nil || org != f.org.ID || physical != "physical-legacy" {
				t.Fatalf("resource identity changed: %s %s %v", org, physical, err)
			}
			if err := pool.QueryRow(ctx, `SELECT issuer_account_id::text FROM object_storage_s3_credentials WHERE id=$1`, human).Scan(&issuer); err != nil || issuer != f.actor.ID {
				t.Fatalf("legacy issuer: %s %v", issuer, err)
			}
			var reports, heads int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_usage_reports`).Scan(&reports); err != nil || reports != 2 {
				t.Fatalf("historical evidence changed: %d %v", reports, err)
			}
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_usage_heads`).Scan(&heads); err != nil || heads != 1 {
				t.Fatalf("mixed current usage head retained: %d %v", heads, err)
			}
			if _, err := pool.Exec(ctx, `UPDATE object_buckets SET org_id=(SELECT id FROM orgs WHERE personal_owner_account_id=$2) WHERE id=$1`, bucket, namespace); err == nil {
				t.Fatal("bucket organization rewrite accepted")
			}
			if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.ResolveObjectS3Credential(ctx, access); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("backfilled user key survived departure")
			}
			if _, _, err := store.ResolveObjectS3Credential(ctx, managedAccess); err != nil {
				t.Fatal("backfilled app key revoked with creator:", err)
			}
		})
	}
}

func TestPostgresOrgObjectOffboardingOrdersCredentialMint(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	adoptObjectCompany(t, f)
	bucket := companyObjectBucket(t, store, f.app, "ordered")
	ctx := objectIssuerContext(t, store, f.org.ID, f.actor.ID)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), `UPDATE org_memberships SET removed_at=now() WHERE org_id=$1 AND account_id=$2`, f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	var pid int32
	if err := tx.QueryRow(t.Context(), "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	access, _, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.CreateObjectS3Credential(ctx, state.ObjectS3Credential{ID: uuid.NewString(), AccountID: bucket.AccountID,
			BucketID: bucket.ID, AccessKeyID: access, SecretSealed: []byte("sealed"), KID: "age1test", Label: "racing user", Permission: "read_write", Status: state.ObjectS3CredentialStatusActive}, 10)
		done <- err
	}()
	waitForOffboardingBlock(t, pool, pid)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, state.ErrOrgAccessRevoked) {
			t.Fatalf("credential committed after removal: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("credential mint did not finish")
	}
	if _, _, err := store.ResolveObjectS3Credential(t.Context(), access); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("racing key persisted: %v", err)
	}
}
