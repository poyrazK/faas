package datapipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNativeEvolutionRelationshipsTenantEqualityForgedRPCAndDependencyGate(t *testing.T) {
	serving := servingFixture()
	serving.Columns["orders"] = append(serving.Columns["orders"], "customer_id")
	serving.TenantAccess.Memberships = append(serving.TenantAccess.Memberships, api.DataPipelineTenantMembership{Subject: "alice", Tenant: "b"})
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,tenant_id text NOT NULL,status text,customer_id integer);INSERT INTO public.orders VALUES(1,'a','a order',1),(2,'b','b order',2),(3,'a','cross tenant',2);CREATE TABLE public.customers(id integer PRIMARY KEY,tenant_id text NOT NULL,status text);INSERT INTO public.customers VALUES(1,'a','a customer'),(2,'b','b customer')`, serving)
	_, orders, err := inspectSourceTable(t.Context(), f.source, "orders")
	if err != nil {
		t.Fatal(err)
	}
	_, customers, err := inspectSourceTable(t.Context(), f.source, "customers")
	if err != nil {
		t.Fatal(err)
	}
	refreshNativeEvolutionCheck(t, f, orders, customers)
	approveNativeEvolution(t, f, api.DataPipelineEvolutionRequest{Kind: "projection", Columns: map[string][]string{"orders": {"id", "tenant_id", "status", "customer_id"}, "customers": {"id", "tenant_id", "status"}}})
	f.tick(t)
	if err = f.store.ApproveSchema(t.Context(), f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, f.pipeline.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	refreshReady := true
	refreshFailed := false
	f.engine.RefreshSchema = func(context.Context, Pipeline, SchemaChange) ([]SchemaRefresh, bool, error) {
		if refreshFailed {
			return nil, false, ErrUnavailable
		}
		return nil, refreshReady, nil
	}
	awaitNativeEvolution(t, f, func(p Pipeline) bool { return p.State == "live" && p.Evolution.State == "applied" })
	role := nativeReaderRole(t, f)
	if _, err = f.target.Exec(t.Context(), "ALTER ROLE "+ident(role)+" LOGIN NOINHERIT PASSWORD 'pipeline-test';GRANT SELECT ON public.customers TO "+ident(role)); err != nil {
		t.Fatal(err)
	}
	refreshReady = false
	r := api.DataPipelineRelationship{Name: "customer", SourceTable: "orders", TargetTable: "customers", SourceColumns: []string{"customer_id"}, TargetColumns: []string{"id"}}
	approveNativeEvolution(t, f, api.DataPipelineEvolutionRequest{Kind: "relationships", Relationships: []api.DataPipelineRelationship{r}})
	awaitNativeEvolution(t, f, func(p Pipeline) bool { return p.Reason == "relationship_api_refresh_pending" })
	if f.pipeline.Evolution.State != "approved" {
		t.Fatal("relationship applied before cache refresh")
	}
	refreshReady = true
	awaitNativeEvolution(t, f, func(p Pipeline) bool { return p.State == "live" && p.Evolution.State == "applied" })
	httpAPI := startNativeDataAPI(t, t.Context(), f.target.Config().ConnString(), role, false)
	defer httpAPI.close()
	status, body := httpAPI.request("GET", "alice", "select=id,customer(id,status)&order=id", "")
	if status != 200 {
		t.Fatal(status, body)
	}
	var rows []struct {
		ID       int `json:"id"`
		Customer *struct {
			ID int `json:"id"`
		} `json:"customer"`
	}
	if err = json.Unmarshal([]byte(body), &rows); err != nil || len(rows) != 3 || rows[0].Customer == nil || rows[0].Customer.ID != 1 || rows[2].Customer != nil {
		t.Fatal("tenant equality failed for a subject in both tenants", body, err)
	}
	// A newly connected app needs function grants and another acknowledged
	// cache refresh even though the original owner change is already applied.
	newRole := nativeReaderRole(t, f)
	if _, err = f.target.Exec(t.Context(), "ALTER ROLE "+ident(newRole)+" LOGIN NOINHERIT;GRANT SELECT ON public.customers TO "+ident(newRole)); err != nil {
		t.Fatal(err)
	}
	refreshFailed = true
	awaitNativeEvolution(t, f, func(p Pipeline) bool { return p.Reason == "relationship_api_refresh_failed" })
	if f.pipeline.Evolution.State != "applied" {
		t.Fatal("new app lost historical owner approval")
	}
	if err = f.service.Evolve(t.Context(), f.pipeline.AccountID, f.pipeline.ID, api.DataPipelineEvolutionRequest{ExpectedRevision: f.pipeline.Revision, Action: "retry", ChangeID: f.pipeline.Evolution.ID}); err != nil {
		t.Fatal("applied relationship refresh cannot be recovered", err)
	}
	refreshFailed = false
	awaitNativeEvolution(t, f, func(p Pipeline) bool { return p.State == "live" })
	if f.pipeline.Evolution.Retry != 1 {
		t.Fatal("refresh retry did not retain its bounded attempt")
	}
	var granted bool
	if err = f.target.QueryRow(t.Context(), `SELECT has_function_privilege($1,'public.customer(public.orders)','EXECUTE')`, newRole).Scan(&granted); err != nil || !granted {
		t.Fatal("new binding lacks approved relationship", granted, err)
	}
	// A forged RPC row may name an existing source PK but cannot supply its
	// customer ID or tenant. Only source row 3's real cross-tenant link is used.
	tx, err := f.target.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "SET LOCAL ROLE "+ident(role)+`;SELECT set_config('request.jwt.claims','{"sub":"alice"}',true)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = tx.QueryRow(t.Context(), `SELECT count(*) FROM public.customer(ROW(3,'b','forged',1)::public.orders)`).Scan(&n); err != nil || n != 0 {
		t.Fatal("forged composite granted a joined row", n, err)
	}
	_ = tx.Rollback(t.Context())
	if _, err = f.target.Exec(t.Context(), `ALTER POLICY gregale_pipeline_read ON public.customers USING(false)`); err != nil {
		t.Fatal(err)
	}
	status, body = httpAPI.request("GET", "alice", "select=id,customer(id)", "")
	if status != 503 || !strings.Contains(body, "mirror_unavailable") {
		t.Fatal("joined read silently omitted quarantined dependency", status, body)
	}
	status, body = httpAPI.requestTable("POST", "alice", "rpc/customer", "", `{"source_row":{"id":1}}`)
	if status != 503 {
		t.Fatal("relationship RPC bypassed dependency availability", status, body)
	}
	// Restore the owned gate, then a resync must retire function row-type
	// dependencies before replacing the generation's tables.
	f.tick(t)
	f.control(t, "resync")
	awaitNativeEvolution(t, f, func(p Pipeline) bool { return p.State == "live" })
	var count int
	if err = f.target.QueryRow(t.Context(), `SELECT count(*) FROM gregale_pipeline.relationships WHERE pipeline_id=$1 AND generation=$2`, f.pipeline.ID, f.pipeline.Generation).Scan(&count); err != nil || count != 1 {
		t.Fatal("resync lost declared relationship", count, err)
	}
}
