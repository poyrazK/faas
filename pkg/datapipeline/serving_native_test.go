package datapipeline

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
)

func approveTenantMembers(t *testing.T, f *nativeRecoveryFixture, members []api.DataPipelineTenantMembership) {
	t.Helper()
	p := f.pipeline
	if err := f.service.TenantReaders(t.Context(), p.AccountID, p.ID, p.Revision, "preview", "", members); err != nil {
		t.Fatal(err)
	}
	p, err := f.store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.TenantReaders(t.Context(), p.AccountID, p.ID, p.Revision, "approve", p.ReaderChange.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.pipeline, err = f.store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
}

func assertServingProjection(t *testing.T, f *nativeRecoveryFixture) {
	t.Helper()
	var columns []string
	if err := f.target.QueryRow(t.Context(), `SELECT array_agg(attname::text ORDER BY attnum) FROM pg_attribute WHERE attrelid='public.orders'::regclass AND attnum>0 AND NOT attisdropped`).Scan(&columns); err != nil || !slices.Equal(columns, []string{"id", "tenant_id", "status"}) {
		t.Fatal("mirror widened the reviewed projection", columns, err)
	}
	var published []string
	if err := f.source.QueryRow(t.Context(), `SELECT attnames FROM pg_publication_tables WHERE pubname=$1 AND tablename='orders'`, pipelineName(f.pipeline)).Scan(&published); err != nil || !slices.Equal(published, columns) {
		t.Fatal("publication widened the reviewed projection", published, err)
	}
}

func TestNativeServingTenantIsolationProjectionAndRecovery(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,tenant_id text NOT NULL,status text,secret integer[]);INSERT INTO public.orders VALUES(1,'a','alice-order','{1}'),(2,'b','bob-order','{2}')`, servingFixture())
	assertServingProjection(t, f)
	role := nativeReaderRole(t, f)
	if _, err := f.target.Exec(t.Context(), "ALTER ROLE "+ident(role)+" LOGIN NOINHERIT PASSWORD 'pipeline-test'"); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	httpAPI := startNativeDataAPI(t, t.Context(), f.target.Config().ConnString(), role)
	defer func() { httpAPI.close() }()
	f.engine.RefreshSchema = func(ctx context.Context, _ Pipeline, _ SchemaChange) ([]SchemaRefresh, bool, error) {
		httpAPI.close()
		httpAPI = startNativeDataAPI(t, context.WithoutCancel(ctx), f.target.Config().ConnString(), role)
		return nil, true, nil
	}
	for _, subject := range []string{"alice", "bob"} {
		status, body := httpAPI.request("GET", subject, "select=id,status", "")
		if status != 200 || !strings.Contains(body, subject+"-order") || strings.Contains(body, map[string]string{"alice": "bob-order", "bob": "alice-order"}[subject]) {
			t.Fatal("signed Data API crossed tenant boundary", subject, status, body)
		}
	}
	if status, _ := httpAPI.request("GET", "alice", "select=secret", ""); status == 200 {
		t.Fatal("excluded source column was served")
	}
	// A signed subject cannot choose another tenant through its claims.
	tx, err := f.target.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "SET LOCAL ROLE "+ident(role)+`;SELECT set_config('request.jwt.claims','{"sub":"alice","tenant_id":"b","tenant":"b"}',true)`); err != nil {
		t.Fatal(err)
	}
	var ids []int
	if err = tx.QueryRow(t.Context(), `SELECT array_agg(id ORDER BY id) FROM public.orders`).Scan(&ids); err != nil || !slices.Equal(ids, []int{1}) {
		t.Fatal("JWT tenant claim changed access", ids, err)
	}
	_ = tx.Rollback(t.Context())
	if nativeReadAs(t, f, role, "mallory") != 0 {
		t.Fatal("unregistered subject acquired tenant access")
	}
	if _, err = f.source.Exec(t.Context(), `UPDATE public.orders SET tenant_id='b',status='moved' WHERE id=1;INSERT INTO public.orders VALUES(3,'a','new-order','{3}');DELETE FROM public.orders WHERE id=2;ALTER TABLE public.orders ADD COLUMN later_secret text DEFAULT 'private'`); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(10 * time.Second); ; time.Sleep(25 * time.Millisecond) {
		var done bool
		if err = f.target.QueryRow(t.Context(), `SELECT count(*)=2 AND bool_and((id=1 AND tenant_id='b') OR (id=3 AND tenant_id='a')) FROM public.orders`).Scan(&done); err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		if time.Now().After(until) {
			t.Fatal("projected CDC did not apply insert/update/delete")
		}
	}
	f.tick(t)
	assertServingProjection(t, f)
	if nativeReadAs(t, f, role, "alice") != 1 || nativeReadAs(t, f, role, "bob") != 1 {
		t.Fatal("tenant-key CDC did not move row access")
	}
	f.control(t, "verify")
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Verification == "passed" && !p.VerifyRequested })
	if _, err = f.target.Exec(t.Context(), `UPDATE public.orders SET status='corrupted mirror' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	f.control(t, "verify")
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "live_table_mismatch" })
	if status, _ := httpAPI.request("GET", "alice", "select=id", ""); status != 503 {
		t.Fatal("tenant reader did not receive quarantined-table availability error", status)
	}
	if err = f.store.TableRecovery(t.Context(), f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "", "orders"); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "table_recovery_pending" })
	if err = f.store.TableRecovery(t.Context(), f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, f.pipeline.SchemaChange.Recovery.ID); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.SchemaChange.State == "complete" })
	assertServingProjection(t, f)
	// Freeze a new mapping, commit its target transaction and lose the catalog
	// acknowledgement. Reopening with stale catalog intent must use the receipt.
	stale := f.pipeline
	approveTenantMembers(t, f, []api.DataPipelineTenantMembership{{Subject: "alice", Tenant: "b"}})
	if _, err = applyReaders(t.Context(), f.pipeline, f.target, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err = updateTableReads(t.Context(), stale, f.target, true); err != nil {
		t.Fatal(err)
	}
	if nativeReadAs(t, f, role, "bob") != 0 {
		t.Fatal("stale read-gate reopen restored revoked membership")
	}
	identities := f.worker.Identities
	key, _ := age.GenerateX25519Identity()
	f.worker.Identities = []*age.X25519Identity{key}
	f.tick(t)
	if f.pipeline.ReaderChange.State != "applied" || len(tenantAccess(f.pipeline).Memberships) != 1 || tenantAccess(f.pipeline).Memberships[0].Tenant != "b" {
		t.Fatal("lost acknowledgement or unreadable source blocked target policy acknowledgement")
	}
	f.worker.Identities = identities
	f.await(t, "live")
	f.control(t, "resync")
	f.await(t, "live")
	assertServingProjection(t, f)
	if _, err = f.target.Exec(t.Context(), "GRANT SELECT ON public.orders TO "+ident(role)); err != nil {
		t.Fatal(err)
	}
	if nativeReadAs(t, f, role, "bob") != 0 || nativeReadAs(t, f, role, "alice") != 1 {
		t.Fatal("resync lost the last acknowledged tenant mapping")
	}
	// Same subject count is still a grant when a pair changes: grace must reject it.
	f.service.CanCreate = func(context.Context, string) bool { return false }
	if err = f.service.TenantReaders(t.Context(), f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "preview", "", []api.DataPipelineTenantMembership{{Subject: "alice", Tenant: "a"}}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("grace allowed a new tenant grant", err)
	}
	approveTenantMembers(t, f, []api.DataPipelineTenantMembership{})
	f.tick(t)
	if f.pipeline.ServingInvalid || len(tenantAccess(f.pipeline).Memberships) != 0 || len(f.pipeline.ReadSubjects) != 0 || nativeReadAs(t, f, role, "alice") != 0 {
		t.Fatal("full tenant revocation was not acknowledged")
	}
	if _, err = f.source.Exec(t.Context(), "ALTER PUBLICATION "+ident(pipelineName(f.pipeline))+" SET TABLE ONLY public.orders"); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if f.pipeline.Reason != "source_publication_changed" {
		t.Fatal("publication widening was not detected", f.pipeline.DataPipeline)
	}
}
