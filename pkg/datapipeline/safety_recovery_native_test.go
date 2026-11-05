package datapipeline

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func approveNativeReaders(t *testing.T, f *nativeRecoveryFixture, subjects []string) {
	t.Helper()
	ctx := t.Context()
	if err := f.service.Readers(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "preview", "", subjects); err != nil {
		t.Fatal(err)
	}
	p, err := f.store.Get(ctx, f.pipeline.AccountID, f.pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Readers(ctx, p.AccountID, p.ID, p.Revision, "approve", p.ReaderChange.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.pipeline, err = f.store.Get(ctx, p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
}

func nativeReadAs(t *testing.T, f *nativeRecoveryFixture, role, subject string) int {
	t.Helper()
	tx, err := f.target.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err = tx.Exec(t.Context(), "SET LOCAL ROLE "+ident(role)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `SELECT set_config('request.jwt.claims',jsonb_build_object('sub',$1::text)::text,true)`, subject); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = tx.QueryRow(t.Context(), `SELECT count(*) FROM public.orders`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func nativeReaderRole(t *testing.T, f *nativeRecoveryFixture) string {
	t.Helper()
	role := "review_reader_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := f.admin.Exec(t.Context(), "CREATE ROLE "+ident(role)+" NOLOGIN NOSUPERUSER NOBYPASSRLS"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = f.target.Exec(ctx, "DROP OWNED BY "+ident(role))
		_, _ = f.admin.Exec(ctx, "DROP ROLE "+ident(role))
	})
	if _, err := f.target.Exec(t.Context(), "GRANT USAGE ON SCHEMA public TO "+ident(role)+";GRANT SELECT ON public.orders TO "+ident(role)); err != nil {
		t.Fatal(err)
	}
	return role
}

func TestUnreadableSourceSecretAllowsRevocationPauseAndDisconnect(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'private')`)
	role := nativeReaderRole(t, f)
	approveNativeReaders(t, f, []string{"alice", "bob"})
	f.tick(t)
	f.tick(t)
	if nativeReadAs(t, f, role, "alice") != 1 {
		t.Fatal("reader fixture was not initially accessible")
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	f.worker.Identities, f.service.Recipient = []*age.X25519Identity{key}, key.Recipient()
	approveNativeReaders(t, f, []string{"bob"})
	f.tick(t)
	if f.pipeline.ReaderChange.State != "applied" || nativeReadAs(t, f, role, "alice") != 0 || nativeReadAs(t, f, role, "bob") != 1 {
		t.Fatal("source credential prevented reader revocation", f.pipeline.Reason)
	}
	f.tick(t)
	if f.pipeline.Reason != "credential_unavailable" {
		t.Fatal("missing source credential was not reported", f.pipeline.Reason)
	}
	f.control(t, "pause")
	f.tick(t)
	var enabled bool
	if err := f.target.QueryRow(t.Context(), `SELECT subenabled FROM pg_subscription WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())`, pipelineName(f.pipeline)).Scan(&enabled); err != nil || enabled {
		t.Fatal("missing source credential prevented destination-only pause", enabled, err)
	}
	if f.pipeline.State != "paused" {
		t.Fatal("pause was not acknowledged", f.pipeline.State)
	}
	f.control(t, "delete")
	f.tick(t)
	if f.pipeline.State != "deleting" || f.pipeline.Reason != "credential_unavailable" {
		t.Fatal("disconnect released resources before publisher cleanup", f.pipeline.State, f.pipeline.Reason)
	}
	// Restore the old identity only after observing the safe destination state.
	if _, err := f.service.RotateConnection(t.Context(), f.pipeline.AccountID, f.connection.ID, api.RotateDataConnectionRequest{ExpectedRevision: f.connection.Revision, ConnectionURL: f.raw}); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "deleted" })
	f.assertRetired(t, f.pipeline)
}

func TestPastDueGracePeriodAllowsPureReaderRevocation(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'private')`)
	role := nativeReaderRole(t, f)
	approveNativeReaders(t, f, []string{"alice", "bob"})
	f.tick(t)
	f.tick(t)
	account := state.Account{ID: f.pipeline.AccountID, Plan: api.PlanPro, Status: state.AccountPastDue}
	if !account.Active() || account.MayDeploy() {
		t.Fatal("billing grace semantics changed")
	}
	f.service.Allowed = func(context.Context, string) bool { return account.Active() }
	f.service.CanCreate = func(context.Context, string) bool { return account.MayDeploy() }
	approveNativeReaders(t, f, []string{"bob"})
	f.tick(t)
	if f.pipeline.ReaderChange.State != "applied" || nativeReadAs(t, f, role, "alice") != 0 || nativeReadAs(t, f, role, "bob") != 1 {
		t.Fatal("past-due grace prevented pure reader revocation")
	}

}

func TestReadOnlySourceFailsCompatibility(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE public.review_readonly(id integer PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	baseline, err := inspectSourceSettings(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	config := pool.Config()
	config.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	source, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	checks, err := inspectSourceSettings(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != len(baseline) {
		t.Fatal("read-only session changed the inspected requirements")
	}
	found := false
	for i, check := range checks {
		if check.Code == "source_writable_required" {
			found = baseline[i].State == "passed" && check.State == "blocked" && check.Remediation != ""
		} else if check != baseline[i] {
			// Ordinary CI databases need not enable logical replication or
			// bounded WAL retention. Read-only mode must preserve their result.
			t.Fatal("read-only session changed an unrelated requirement", baseline[i], check)
		}
	}
	if !found {
		t.Fatal("read-only source passed compatibility")
	}
	p := Pipeline{DataPipeline: api.DataPipeline{Tables: []string{"review_readonly"}}}
	if _, _, err := inspectSource(ctx, p, source); !isBlockedReason(err, "source_writable_required") {
		t.Fatal("worker did not share preflight blocker", err)
	}
	if checks, err = inspectSourceSettings(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(checks, baseline) {
		t.Fatal("read-only session changed the writable primary's settings", checks, baseline)
	}

}

func awaitNativeWorkflow(t *testing.T, f *nativeRecoveryFixture, ready func(Pipeline) bool) {
	t.Helper()
	for until := time.Now().Add(20 * time.Second); ; {
		f.tick(t)
		if ready(f.pipeline) {
			return
		}
		if time.Now().After(until) || f.pipeline.State == "resync_required" {
			t.Fatal("fixture did not advance", f.pipeline.State, f.pipeline.Reason)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func nativeRefreshCatalog(t *testing.T, f *nativeRecoveryFixture) {
	t.Helper()
	schema, err := os.ReadFile("catalog_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.pool.Exec(t.Context(), string(schema)); err != nil {
		t.Fatal(err)
	}
	app := uuid.NewString()
	if _, err = f.store.pool.Exec(t.Context(), `INSERT INTO apps VALUES($1,$2,'review-api','active')`, app, f.pipeline.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.pool.Exec(t.Context(), `INSERT INTO deployments VALUES($1,$2,'live','default')`, uuid.NewString(), app); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.pool.Exec(t.Context(), `INSERT INTO managed_postgres_bindings VALUES($1,$2,$3,$4,'default','read_only','ready')`, uuid.NewString(), f.pipeline.AccountID, f.pipeline.DatabaseID, app); err != nil {
		t.Fatal(err)
	}
}

func TestTableRetirementNativeRefreshRetry(t *testing.T) {
	for _, kind := range []string{"onboarding", "live"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'healthy');CREATE TABLE public.customers(id integer PRIMARY KEY);INSERT INTO public.customers VALUES(1)`)
			ctx := t.Context()
			if _, err := f.store.RequestCheck(ctx, f.pipeline.AccountID, f.connection.ID, f.connection.Revision, f.pipeline.DatabaseID); err != nil {
				t.Fatal(err)
			}
			check, err := f.store.ClaimCheck(ctx, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.FinishCheck(ctx, check, api.DataConnectionCheck{State: "passed", Tables: []api.DataSourceTable{{Name: "customers", Supported: true}, {Name: "orders", Supported: true}}}); err != nil {
				t.Fatal(err)
			}
			f.engine.RefreshSchema = func(context.Context, Pipeline, SchemaChange) ([]SchemaRefresh, bool, error) { return nil, true, nil }
			if err := f.store.RequestTables(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, []string{"customers"}); err != nil {
				t.Fatal(err)
			}
			awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "table_addition_pending" })
			if err := f.store.ApproveSchema(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, f.pipeline.SchemaChange.ID, false); err != nil {
				t.Fatal(err)
			}
			retire := f.service.TableCancellation
			if kind == "live" {
				awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.SchemaChange.State == "complete" })
				retire = f.service.TableRemoval
			} else {
				f.tick(t)
			}
			nativeRefreshCatalog(t, f)
			f.engine.RefreshSchema = f.store.RefreshSchema
			if err := retire(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "customers", ""); err != nil {
				t.Fatal(err)
			}
			awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.SchemaChange.Cancellation.State == "pending" })
			if err := retire(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "", f.pipeline.SchemaChange.Cancellation.ID); err != nil {
				t.Fatal(err)
			}
			awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "schema_api_refresh_pending" })
			if _, err := f.store.pool.Exec(ctx, `UPDATE notification_outbox SET state='dead_letter',last_error='runtime failed'`); err != nil {
				t.Fatal(err)
			}
			f.tick(t)
			if f.pipeline.Reason != "schema_api_refresh_failed" {
				t.Fatal("fixture did not reach failed removal refresh", f.pipeline.Reason)
			}
			if err := f.service.ApproveSchema(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, f.pipeline.SchemaChange.ID, true); !errors.Is(err, ErrConflict) {
				t.Fatal("schema retry result changed", err)
			}
			if err := retire(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "", ""); !errors.Is(err, ErrConflict) {
				t.Fatal("discard result changed", err)
			}
			originalID := f.pipeline.SchemaChange.Cancellation.ID
			generation := f.pipeline.Generation
			if err := retire(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "", originalID); err != nil {
				t.Fatal("refresh retry rejected", err)
			}
			f.tick(t)
			if f.pipeline.Reason != "schema_api_refresh_pending" || f.pipeline.SchemaChange.Cancellation.ID != originalID || f.pipeline.SchemaChange.Cancellation.RefreshRetry != 1 || f.pipeline.Generation != generation {
				t.Fatal("retry changed approved retirement scope", f.pipeline.Reason)
			}
			f.tick(t) // Worker replay must reuse this attempt's wake ID.
			var attempts int
			if err := f.store.pool.QueryRow(ctx, `SELECT count(*) FROM notification_outbox`).Scan(&attempts); err != nil || attempts != 2 {
				t.Fatal("refresh did not create exactly one new attempt", attempts, err)
			}
			if _, err := f.store.pool.Exec(ctx, `UPDATE notification_outbox SET state='delivered',delivered_at=clock_timestamp() WHERE state='pending'`); err != nil {
				t.Fatal(err)
			}
			awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.SchemaChange.Cancellation.State == "complete" })
			if len(f.pipeline.Tables) != 1 || f.pipeline.Tables[0] != "orders" {
				t.Fatal("retry changed remaining tables", f.pipeline.Tables)
			}
			var value string
			if err := f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&value); err != nil || value != "healthy" {
				t.Fatal("healthy table changed", value, err)
			}
		})
	}
}

func TestUnavailableSourceSafetyControlsKeepOwnershipAndLeaseFences(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY);INSERT INTO public.orders VALUES(1)`)
	ctx := t.Context()
	paused := f.pipeline
	paused.DesiredState = "paused"
	ob := f.engine.reconcileUnavailable(ctx, paused, func(context.Context) error { return ErrConflict })
	if ob.State == "paused" {
		t.Fatal("stale worker acknowledged suspension")
	}
	enabled := func() bool {
		t.Helper()
		var value bool
		if err := f.target.QueryRow(ctx, `SELECT subenabled FROM pg_subscription WHERE subname=$1`, pipelineName(f.pipeline)).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if !enabled() {
		t.Fatal("stale worker disabled subscription")
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	f.worker.Identities = []*age.X25519Identity{key}
	if _, err = f.target.Exec(ctx, "COMMENT ON SUBSCRIPTION "+ident(pipelineName(f.pipeline))+" IS 'foreign'"); err != nil {
		t.Fatal(err)
	}
	f.control(t, "pause")
	f.tick(t)
	if f.pipeline.Reason != "destination_subscription_conflict" || !enabled() {
		t.Fatal("missing source key bypassed subscription ownership", f.pipeline.Reason)
	}
	if _, err = f.target.Exec(ctx, "COMMENT ON SUBSCRIPTION "+ident(pipelineName(f.pipeline))+" IS "+literal(marker(f.pipeline))); err != nil {
		t.Fatal(err)
	}
	f.control(t, "resume")
	f.worker.Allowed = func(context.Context, string) bool { return false }
	f.tick(t)
	if f.pipeline.Reason != "admission_closed" || enabled() {
		t.Fatal("missing source key prevented account suspension", f.pipeline.Reason)
	}
	paused = f.pipeline
	paused.DesiredState, paused.State, paused.Reason = "paused", "resync_required", "replication_state_lost"
	ob = f.engine.reconcileUnavailable(ctx, paused, func(context.Context) error { return nil })
	if ob.State != "resync_required" || ob.Reason != "replication_state_lost" {
		t.Fatal("safety pause erased required resync", ob)
	}
}
