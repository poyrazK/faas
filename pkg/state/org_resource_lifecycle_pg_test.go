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

func TestPostgresOrgResourceUnreviewedObservabilityBlocksErasure(t *testing.T) {
	cases := []struct {
		name   string
		insert string
		exists string
	}{
		{"slo", `INSERT INTO app_slos(id,account_id,app_id,name,sli,objective_bp,window_days)
VALUES ($1,$2,$3,'unreviewed','availability',9900,7)`, `SELECT EXISTS(SELECT 1 FROM app_slos WHERE id=$1)`},
		{"synthetic", `INSERT INTO synthetic_checks(id,account_id,app_id,name,method,path,timeout_ms,interval_seconds)
VALUES ($1,$2,$3,'unreviewed','GET','/',1000,300)`, `SELECT EXISTS(SELECT 1 FROM synthetic_checks WHERE id=$1)`},
		{"channel", `INSERT INTO notification_channels(id,account_id,name,kind,email)
VALUES ($1,$2,'unreviewed','email','unreviewed@example.com')`, `SELECT EXISTS(SELECT 1 FROM notification_channels WHERE id=$1)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			store := state.NewPgStore(pool)
			f := setupOffboarding(t, store)
			ctx := t.Context()
			if err := store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_observability_guard"); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_observability_guard"); err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			args := []any{id, f.actor.ID, f.app.ID}
			if tc.name == "channel" {
				args = args[:2]
			}
			if _, err := pool.Exec(ctx, tc.insert, args...); err != nil {
				t.Fatal(err)
			}
			if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkAccountDeletionPending(ctx, f.actor.ID); !errors.Is(err, state.ErrOrgAppOwnershipPending) {
				t.Fatalf("unreviewed observability record allowed erasure: %v", err)
			}
			if _, err := pool.Exec(ctx, `UPDATE accounts SET status='deleted_pending',deletion_requested_at=now() WHERE id=$1`, f.actor.ID); err == nil {
				t.Fatal("transactional status writer bypassed observability ownership guard")
			}
			var status string
			if err := pool.QueryRow(ctx, `SELECT status FROM accounts WHERE id=$1`, f.actor.ID).Scan(&status); err != nil || status != string(state.AccountActive) {
				t.Fatalf("blocked deletion changed the account status: %s %v", status, err)
			}
			if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, f.actor.ID); err == nil {
				t.Fatal("raw account deletion bypassed observability ownership guard")
			}
			var exists bool
			if err := pool.QueryRow(ctx, tc.exists, id).Scan(&exists); err != nil || !exists {
				t.Fatalf("observability ownership guard discarded the record: %v", err)
			}
		})
	}
}

func TestPostgresOrgResourcePersonalMailHistoryDoesNotOwnCompanyResources(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	ctx := t.Context()
	if err := store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_mail_history"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_mail_history"); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	// A restored account can retain a cancelled personal deletion notice. Its
	// history must be erased with the person while company resources survive.
	if _, err := pool.Exec(ctx, `INSERT INTO platform_mail_producer_outbox
(id,account_id,namespace,producer_id,from_address,recipient_address,template,template_version,
deletion_requested_at,restore_until,expires_at,state,code)
VALUES ($1,$2,'platform','apid','notice@example.test','','account_deletion_pending',1,
now()-interval '1 day',now()+interval '29 days',now()+interval '1 day','cancelled','account_changed')`, id, f.actor.ID); err != nil {
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
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_mail_producer_outbox WHERE id=$1)`, id).Scan(&exists); err != nil || exists {
		t.Fatalf("personal notification history survived identity erasure: %v", err)
	}
	app, err := store.AppByID(ctx, f.app.ID)
	if err != nil || app.AccountID != f.app.AccountID || app.OrgID != f.org.ID {
		t.Fatalf("personal notification cleanup changed company resources: %+v %v", app, err)
	}
}

func TestPostgresOrgResourceUnreviewedMCPBlocksErasure(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	ctx := t.Context()
	if err := store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_mcp_guard"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_mcp_guard"); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO mcp_readiness_maintenance_windows
(id,account_id,app_id,scope,starts_at,ends_at,reason,created_by,created_at)
VALUES ($1,$2,$3,'production',now(),now()+interval '1 hour','unreviewed',$2,now())`, id, f.actor.ID, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, f.actor.ID); !errors.Is(err, state.ErrOrgAppOwnershipPending) {
		t.Fatalf("unreviewed MCP maintenance allowed erasure: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, f.actor.ID); err == nil {
		t.Fatal("raw account deletion bypassed MCP maintenance ownership guard")
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mcp_readiness_maintenance_windows WHERE id=$1)`, id).Scan(&exists); err != nil || !exists {
		t.Fatalf("MCP maintenance ownership guard discarded the record: %v", err)
	}
}

func TestPostgresOrgResourceUnreviewedConnectionCapacityBlocksErasure(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	ctx := t.Context()
	if err := store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_capacity_guard"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_capacity_guard"); err != nil {
		t.Fatal(err)
	}
	databaseID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases
(id,account_id,org_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint,state,deleted_at)
VALUES ($1,$2,$3,'capacity-guard','eu',17,'development','single_zone','test',repeat('a',64),'deleted',now())`, databaseID, f.actor.ID, f.org.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_connection_capacity
(database_id,account_id,backend_id,backend_fingerprint,provider_resource_id,data_resource_id,generation,checked_at,expires_at)
VALUES ($1,$2,'test',repeat('a',64),'provider-guard','data-guard',1,now(),now())`, databaseID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, f.actor.ID); !errors.Is(err, state.ErrOrgAppOwnershipPending) {
		t.Fatalf("unreviewed connection evidence allowed erasure: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, f.actor.ID); err == nil {
		t.Fatal("raw account deletion bypassed connection evidence ownership guard")
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM managed_postgres_connection_capacity WHERE database_id=$1)`, databaseID).Scan(&exists); err != nil || !exists {
		t.Fatalf("connection evidence ownership guard discarded the evidence: %v", err)
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

// Rehearsal evidence follows its run and its preview app, so it no longer
// blocks identity erasure; a fixture that still holds a database does, until
// its cleanup finishes (ADR-20261022155043, settling ADR-202610171904).
func TestPostgresOrgResourceRehearsalFixtureBlocksErasureOnlyWhileLive(t *testing.T) {
	for _, tc := range []struct {
		status  string
		blocked bool
	}{
		{"complete", false},
		{"provisioning", true},
	} {
		t.Run(tc.status, func(t *testing.T) {
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
			if _, err := pool.Exec(ctx, `INSERT INTO scenario_postgres_fixtures
(id,account_id,run_id,app_id,owner_id,database_name,role_name,status,spec,expires_at,backend_sha256)
VALUES ($1,$2,$3,$4,$1,$5,$5,$7,'{}'::jsonb,clock_timestamp()+interval '1 hour',$6)`,
				id, f.actor.ID, run, f.app.ID, "grpg_"+run, strings.Repeat("a", 64), tc.status); err != nil {
				t.Fatal(err)
			}
			if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
				t.Fatal(err)
			}
			err := store.MarkAccountDeletionPending(ctx, f.actor.ID)
			if !tc.blocked {
				if err != nil {
					t.Fatalf("completed rehearsal evidence blocked erasure: %v", err)
				}
				return
			}
			if !errors.Is(err, state.ErrOrgAppOwnershipPending) {
				t.Fatalf("a live fixture allowed erasure: %v", err)
			}
			if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, f.actor.ID); err == nil {
				t.Fatal("raw account deletion bypassed the live fixture guard")
			}
			var exists bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM scenario_postgres_fixtures WHERE id=$1)`, id).Scan(&exists); err != nil || !exists {
				t.Fatalf("erasure discarded a live fixture journal: %v", err)
			}
		})
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
	if _, err := pool.Exec(ctx, `INSERT INTO worker_recovery(app_id,revision,status,next_attempt_at,updated_at)
VALUES ($1,'company-runtime','ready',now(),now())`, f.app.ID); err != nil {
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
	var revision string
	if err := pool.QueryRow(ctx, `SELECT revision FROM worker_recovery WHERE app_id=$1`, f.app.ID).Scan(&revision); err != nil || revision != "company-runtime" {
		t.Fatalf("creator erasure changed durable worker recovery: %q %v", revision, err)
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
