//go:build !no_pg

package state_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresOrgResourceLifecycle(t *testing.T) {
	orgResourceLifecycleSuite(t, func(t *testing.T) state.Store { t.Helper(); return state.NewPgStore(pgtest.OpenMigrated(t)) })
}

func TestPostgresOrgResourceBillingAdoption(t *testing.T) {
	orgResourceBillingAdoption(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPostgresOrgResourceUnreviewedCIBlocksErasure(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	ctx := t.Context()
	if err := store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_ci_guard"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_ci_guard"); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO ci_pipeline_revisions(id,account_id,name,digest,definition)
VALUES ($1,$2,'unreviewed','sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','{}'::jsonb)`, id, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, f.actor.ID); !errors.Is(err, state.ErrOrgAppOwnershipPending) {
		t.Fatalf("unreviewed CI allowed erasure: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, f.actor.ID); err == nil {
		t.Fatal("raw account deletion bypassed CI ownership guard")
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ci_pipeline_revisions WHERE id=$1)`, id).Scan(&exists); err != nil || !exists {
		t.Fatalf("CI ownership guard discarded the revision: %v", err)
	}
}

func TestPostgresOrgResourceUnreviewedProductControlBlocksErasure(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	ctx := t.Context()
	if err := store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_product_guard"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_product_guard"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_control_revisions(account_id,app_id,revision,spec)
VALUES ($1,$2,'unreviewed','{}'::jsonb)`, f.actor.ID, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, f.actor.ID); !errors.Is(err, state.ErrOrgAppOwnershipPending) {
		t.Fatalf("unreviewed Product Control allowed erasure: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, f.actor.ID); err == nil {
		t.Fatal("raw account deletion bypassed Product Control ownership guard")
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_control_revisions WHERE app_id=$1)`, f.app.ID).Scan(&exists); err != nil || !exists {
		t.Fatalf("Product Control ownership guard discarded the revision: %v", err)
	}
}

func TestPostgresOrgResourceUnreviewedRehearsalBlocksErasure(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	ctx := t.Context()
	if err := store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_rehearsal_guard"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_rehearsal_guard"); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	run := strings.ReplaceAll(id, "-", "")
	// A completed fixture can be purged by the rehearsal service. Identity
	// erasure must still retain its unreviewed company evidence.
	if _, err := pool.Exec(ctx, `INSERT INTO scenario_postgres_fixtures
(id,account_id,run_id,app_id,owner_id,database_name,role_name,status,spec,expires_at,backend_sha256)
VALUES ($1,$2,$3,$4,$1,$5,$5,'complete','{}'::jsonb,clock_timestamp()+interval '1 hour',$6)`,
		id, f.actor.ID, run, f.app.ID, "grpg_"+run, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, f.actor.ID); !errors.Is(err, state.ErrOrgAppOwnershipPending) {
		t.Fatalf("unreviewed rehearsal allowed erasure: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, f.actor.ID); err == nil {
		t.Fatal("raw account deletion bypassed rehearsal ownership guard")
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM scenario_postgres_fixtures WHERE id=$1)`, id).Scan(&exists); err != nil || !exists {
		t.Fatalf("rehearsal ownership guard discarded the fixture: %v", err)
	}
}

func TestPostgresOrgResourceUnsupportedBindingBlocksErasure(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	if err := store.UpdateAccountProviderCustomerID(t.Context(), f.org.ID, "cus_bound_company"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(t.Context(), f.org.ID, "si_bound_company"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO route_monitors(app_id,account_id,enabled,revision,routes) VALUES ($1,$2,false,1,'[]'::jsonb)`, f.app.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(t.Context(), f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(t.Context(), f.actor.ID); !errors.Is(err, state.ErrOrgAppOwnershipPending) {
		t.Fatalf("unsupported binding allowed erasure: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM accounts WHERE id=$1`, f.actor.ID); err == nil {
		t.Fatal("raw account deletion bypassed ownership guard")
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM route_monitors WHERE app_id=$1`, f.app.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("guard erased company binding: %d %v", count, err)
	}
}

func TestPostgresOrgResourceRuntimeAdmissionSurvivesErasure(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	ctx := t.Context()
	if err := store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_runtime_owned"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_runtime_owned"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(ctx, f.actor.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	var before, after []byte
	if err := pool.QueryRow(ctx, `SELECT application_standard_runtime_snapshot($1,NULL)`, f.app.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT application_standard_runtime_snapshot($1,NULL)`, f.app.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("creator erasure changed immutable runtime admission inputs")
	}
}

func TestPostgresOrgResourcePersonalTelemetryErasure(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	ctx := t.Context()
	if err := store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_telemetry_owned"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_telemetry_owned"); err != nil {
		t.Fatal(err)
	}
	personal, err := store.CreateApp(ctx, state.App{AccountID: f.actor.ID, Slug: "personal-telemetry"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO usage_daily(account_id,app_id,day,org_id) VALUES ($1,$2,current_date,$3)`, f.actor.ID, personal.ID, personal.OrgID); err != nil {
		t.Fatal(err)
	}
	for _, app := range []state.App{personal, f.app} {
		if err := store.AppendBuilderUsage(ctx, app.AccountID, app.ID, uuid.NewString(), time.Now(), "railpack", 5); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendSnapshotStorage(ctx, app.AccountID, app.ID, time.Now(), 1024, 512); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	var personalRows, companyRows int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM builder_usage WHERE app_id=$1)+(SELECT count(*) FROM usage_daily WHERE app_id=$1)+(SELECT count(*) FROM snapshot_storage_daily WHERE app_id=$1), (SELECT count(*) FROM builder_usage WHERE app_id=$2)+(SELECT count(*) FROM snapshot_storage_daily WHERE app_id=$2)`, personal.ID, f.app.ID).Scan(&personalRows, &companyRows); err != nil {
		t.Fatal(err)
	}
	if personalRows != 0 || companyRows != 2 {
		t.Fatalf("personal rows=%d company rows=%d", personalRows, companyRows)
	}
}
