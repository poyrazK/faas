package datapipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func previewNativeIndex(t *testing.T, f *nativeRecoveryFixture) {
	t.Helper()
	p := f.pipeline
	f.engine.IndexStorageLimit = func(context.Context, Pipeline) (int64, error) { return 1 << 30, nil }
	if err := f.service.Indexes(t.Context(), p.AccountID, p.ID, api.DataPipelineIndexRequest{ExpectedRevision: p.Revision, Action: "preview", Table: "orders", Filters: []string{"status"}}); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if c := f.pipeline.IndexAdvice; c == nil || c.State != "preview" || !c.CanApply {
		t.Fatal("no applicable advice", c)
	}
}
func approveNativeIndex(t *testing.T, f *nativeRecoveryFixture) {
	t.Helper()
	p := f.pipeline
	if err := f.service.Indexes(t.Context(), p.AccountID, p.ID, api.DataPipelineIndexRequest{ExpectedRevision: p.Revision, Action: "approve", AdviceID: p.IndexAdvice.ID}); err != nil {
		t.Fatal(err)
	}
	var err error
	f.pipeline, err = f.store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
}
func TestNativeIndexTenantExpressionLostAckAndSourceIndependent(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,tenant_id text NOT NULL,status text,secret integer[]);INSERT INTO public.orders VALUES(1,'a','alice-order','{1}'),(2,'b','bob-order','{2}')`, servingFixture())
	p := f.pipeline
	// Source credentials need not be decryptable for metadata advice or building.
	identities := f.worker.Identities
	f.worker.Identities = nil
	previewNativeIndex(t, f)
	if f.pipeline.ObservedAt != p.ObservedAt || f.pipeline.AppliedThroughAt != p.AppliedThroughAt || f.pipeline.ObservedRevision != p.ObservedRevision {
		t.Fatal("index advice refreshed replication evidence")
	}
	approveNativeIndex(t, f)
	tx, err := f.store.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `SELECT set_config('gregale.pipeline_worker_protocol','customer-serving-v1',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.q.PipelineClaim(t.Context(), tx, "previous-serving-owner"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("old owner acquired approved DDL", err)
	}
	_ = tx.Rollback(t.Context())
	claim, err := f.store.Claim(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	current := func(context.Context) error { return nil }
	ob := f.engine.reconcileUnavailable(t.Context(), claim, current)
	if !ob.IndexOnly || ob.IndexChange == nil || ob.IndexChange.State != "applied" {
		t.Fatal("source-independent build failed", ob.IndexChange, ob.Reason)
	}
	// Deliberately lose the control-catalog acknowledgement. The generation
	// receipt must acknowledge the exact object without executing DDL again.
	stale := claim
	stale.ObservedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	replay := f.engine.reconcileUnavailable(t.Context(), stale, current)
	if !replay.IndexOnly || replay.IndexChange == nil || replay.IndexChange.State != "applied" {
		t.Fatal("lost acknowledgement was not recovered", replay.IndexChange)
	}
	if err = f.store.Finish(t.Context(), claim, replay); err != nil {
		t.Fatal(err)
	}
	var definition string
	if err = f.target.QueryRow(t.Context(), `SELECT pg_get_indexdef(to_regclass($1))`, tableIdent(ob.IndexChange.IndexName)).Scan(&definition); err != nil || !strings.Contains(definition, `COLLATE "C"`) {
		t.Fatal("index omitted tenant policy expression", definition, err)
	}
	if _, err = f.target.Exec(t.Context(), `SET enable_seqscan=off`); err != nil {
		t.Fatal(err)
	}
	rows, err := f.target.Query(t.Context(), `EXPLAIN SELECT id FROM public.orders WHERE (tenant_id::text COLLATE "C")='a' AND status='alice-order'`)
	if err != nil {
		t.Fatal(err)
	}
	var explain string
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		explain += line
	}
	rows.Close()
	if !strings.Contains(explain, ob.IndexChange.IndexName) {
		t.Fatal("index did not support the proposed query shape", explain)
	}
	f.worker.Identities = identities
	f.await(t, "live")
	role := nativeReaderRole(t, f)
	if _, err = f.target.Exec(t.Context(), "ALTER ROLE "+ident(role)+" LOGIN NOINHERIT PASSWORD 'pipeline-test'"); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	httpAPI := startNativeDataAPI(t, t.Context(), f.target.Config().ConnString(), role)
	defer httpAPI.close()
	status, body := httpAPI.request("GET", "alice", "select=id,status", "")
	if status != 200 || !strings.Contains(body, "alice-order") || strings.Contains(body, "bob-order") {
		t.Fatal("index changed tenant access", status, body)
	}
	f.control(t, "resync")
	f.await(t, "live")
	if c := f.pipeline.IndexAdvice; c == nil || c.State != "blocked" || c.Reason != "index_mirror_replaced" {
		t.Fatal("replacement still reports the old index as applied", c)
	}
	var survives bool
	if err = f.target.QueryRow(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, tableIdent(ob.IndexChange.IndexName)).Scan(&survives); err != nil || survives {
		t.Fatal("resync retained an index from the old generation", survives, err)
	}
	var receipts int
	if err = f.target.QueryRow(t.Context(), `SELECT count(*) FROM gregale_pipeline.query_indexes WHERE pipeline_id=$1`, p.ID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("resync retained old generation index authority", receipts, err)
	}

}
func TestNativeIndexLockTimeoutRollsBack(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,status text);INSERT INTO public.orders VALUES(1,'open')`)
	previewNativeIndex(t, f)
	name := f.pipeline.IndexAdvice.IndexName
	approveNativeIndex(t, f)
	conn, err := pgx.Connect(t.Context(), f.target.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(t.Context(), `LOCK TABLE public.orders IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if c := f.pipeline.IndexAdvice; c == nil || c.State != "blocked" || c.Reason != "index_build_stopped" {
		t.Fatal("lock timeout was not actionable", c)
	}
	var exists bool
	if err = f.target.QueryRow(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, tableIdent(name)).Scan(&exists); err != nil || exists {
		t.Fatal("failed build left an index", exists, err)
	}
	_ = tx.Rollback(t.Context())
	f.await(t, "live")
}

func TestNativeIndexMissingTemporaryFileGuardIsAdviceOnly(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,status text);INSERT INTO public.orders VALUES(1,'open')`)
	p := f.pipeline
	f.engine.IndexStorageLimit = func(context.Context, Pipeline) (int64, error) { return 1 << 30, nil }
	var owner string
	if err := f.target.QueryRow(t.Context(), `SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='public.orders'::regclass`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"GRANT USAGE ON SCHEMA gregale_pipeline TO " + ident(owner), "GRANT SELECT ON gregale_pipeline.mirrors TO " + ident(owner), "SET ROLE " + ident(owner)} {
		if _, err := f.target.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = f.target.Exec(context.Background(), `RESET ROLE`) })
	if err := f.service.Indexes(t.Context(), p.AccountID, p.ID, api.DataPipelineIndexRequest{ExpectedRevision: p.Revision, Action: "preview", Table: "orders", Filters: []string{"status"}}); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	c := f.pipeline.IndexAdvice
	if c == nil || c.State != "preview" || c.CanApply || c.Reason != "index_guard_unavailable" || c.Statement == "" {
		t.Fatal("unguarded provider could apply advice", c)
	}
	if err := f.service.Indexes(t.Context(), p.AccountID, p.ID, api.DataPipelineIndexRequest{ExpectedRevision: f.pipeline.Revision, Action: "approve", AdviceID: c.ID}); !errors.Is(err, ErrConflict) {
		t.Fatal("advice-only provider accepted approval", err)
	}
}

func TestNativeIndexUUIDTenantPolicyExpression(t *testing.T) {
	profile := servingFixture()
	profile.TenantAccess.Memberships = []api.DataPipelineTenantMembership{{Subject: "alice", Tenant: "11111111-1111-4111-8111-111111111111"}}
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,tenant_id uuid NOT NULL,status text);INSERT INTO public.orders VALUES(1,'11111111-1111-4111-8111-111111111111','open')`, profile)
	previewNativeIndex(t, f)
	approveNativeIndex(t, f)
	f.tick(t)
	if c := f.pipeline.IndexAdvice; c == nil || c.State != "applied" {
		t.Fatal("UUID tenant policy expression was not indexable", c)
	}
}
