package datapipelines

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPipelineHTTPJourneyAndSecretRedaction(t *testing.T) {
	pool := pgtest.Open(t)
	schema, err := os.ReadFile("../../datapipeline/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(context.Background(), string(schema)); err != nil {
		t.Fatal(err)
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	account, foreign := uuid.NewString(), uuid.NewString()
	h := &Handler{Service: &datapipeline.Service{Store: datapipeline.NewPostgresStore(pool), Recipient: key.Recipient(), Allowed: func(context.Context, string) bool { return true }, ValidateTarget: func(context.Context, string, string) error { return nil }}}
	mux := http.NewServeMux()
	wrap := func(fn func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			a := account
			if r.Header.Get("Authorization") == "Bearer foreign" {
				a = foreign
			}
			fn(w, r, a)
		}
	}
	mux.HandleFunc("POST /v1/data/connections", wrap(h.CreateConnection))
	mux.HandleFunc("GET /v1/data/connections", wrap(h.Connections))
	mux.HandleFunc("POST /v1/data/pipelines", wrap(h.Create))
	mux.HandleFunc("POST /v1/data/connections/{id}/check", wrap(h.RequestCheck))
	mux.HandleFunc("GET /v1/data/connections/{id}/check", wrap(h.GetCheck))
	mux.HandleFunc("GET /v1/data/pipelines/{id}", wrap(h.Get))
	mux.HandleFunc("POST /v1/data/pipelines/{id}/control", wrap(h.Control))
	mux.HandleFunc("POST /v1/data/pipelines/{id}/tenant-readers", wrap(h.TenantReaders))
	mux.HandleFunc("POST /v1/data/pipelines/{id}/evolution", wrap(h.Evolve))
	mux.HandleFunc("POST /v1/data/pipelines/{id}/operations", wrap(h.Operations))
	mux.HandleFunc("POST /v1/data/pipelines/{id}/schema-approval", wrap(h.ApproveSchema))
	server := httptest.NewServer(mux)
	defer server.Close()
	client := api.NewClient(server.URL, "owner")
	ctx := context.Background()
	c, err := client.CreateDataConnection(ctx, api.CreateDataConnectionRequest{Name: "orders-source", ConnectionURL: "postgres://reader:credential-sentinel@db.example/orders?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := client.ListDataConnections(ctx)
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("connection list: %v", err)
	}
	serialized, _ := json.Marshal(list)
	if strings.Contains(string(serialized), "credential-sentinel") || strings.Contains(string(serialized), "ciphertext") || strings.Contains(string(serialized), "db.example") {
		t.Fatal("connection response disclosed private material")
	}
	if _, err = client.RequestDataConnectionCheck(ctx, c.ID, api.RequestDataConnectionCheck{ExpectedRevision: c.Revision}); err != nil {
		t.Fatal(err)
	}
	checkStore := h.Service.Store
	job, err := checkStore.ClaimCheck(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = checkStore.FinishCheck(ctx, job, api.DataConnectionCheck{State: "passed", Checks: []api.DataConnectionIssue{}, Tables: []api.DataSourceTable{{Name: "orders", Supported: true, Columns: []api.DataSourceColumn{{Name: "id", Type: "integer", PrimaryKey: true}, {Name: "tenant_id", Type: "text"}}, Issues: []api.DataConnectionIssue{}}}}); err != nil {
		t.Fatal(err)
	}
	check, err := client.GetDataConnectionCheck(ctx, c.ID)
	if err != nil || !check.Fresh || check.State != "passed" {
		t.Fatalf("check HTTP read: %+v, %v", check, err)
	}
	foreignClient := api.NewClient(server.URL, "foreign")
	if _, err = foreignClient.GetDataConnectionCheck(ctx, c.ID); err == nil {
		t.Fatal("foreign account read source metadata")
	}
	p, err := client.CreateDataPipeline(ctx, api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: []string{"orders"}, ReadSubjects: []string{"alice"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.State != "validating" || p.Revision != 1 {
		t.Fatalf("unexpected accepted pipeline: %+v", p)
	}
	other := api.NewClient(server.URL, "foreign")
	if _, err = other.GetDataPipeline(ctx, p.ID); err == nil {
		t.Fatal("foreign account read pipeline")
	}
	paused, err := client.ControlDataPipeline(ctx, p.ID, api.ControlDataPipelineRequest{ExpectedRevision: 1, Action: "pause"})
	if err != nil || paused.DesiredState != "paused" {
		t.Fatalf("pause failed: %v", err)
	}
	if _, err = client.ControlDataPipeline(ctx, p.ID, api.ControlDataPipelineRequest{ExpectedRevision: 1, Action: "resume"}); err == nil {
		t.Fatal("stale HTTP control overwrote new intent")
	}
	work := api.DataPipelineOperationsRequest{ExpectedRevision: paused.Revision, Action: "configure", Config: &api.DataPipelineOperations{Version: 1, Workload: &api.DataPipelineWorkload{QueryTimeoutMillis: 500, BatchesPerTick: 1}}}
	if _, err = other.ConfigureDataPipelineOperations(ctx, p.ID, work); err == nil {
		t.Fatal("foreign account configured work")
	}
	configured, err := client.ConfigureDataPipelineOperations(ctx, p.ID, work)
	if err != nil || configured.Operations.Workload.QueryTimeoutMillis != 500 {
		t.Fatal("work policy HTTP round trip", configured, err)
	}
	if _, err = client.ConfigureDataPipelineOperations(ctx, p.ID, work); err == nil {
		t.Fatal("stale work policy replaced current revision")
	}
	request := httptest.NewRequest("POST", "/v1/data/connections", strings.NewReader(`{"name":"bad","connection_url":"postgres://u:p@db/db?sslmode=verify-full","account_id":"forged"}`))
	response := httptest.NewRecorder()
	h.CreateConnection(response, request, account)
	if response.Code != 400 || strings.Contains(response.Body.String(), "postgres:") {
		t.Fatal("unknown account field accepted or credential echoed")
	}
	tenant, err := client.CreateDataPipeline(ctx, api.CreateDataPipelineRequest{Name: "tenant-orders", SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: []string{"orders"}, Serving: &api.DataPipelineServing{Version: 1, TenantAccess: &api.DataPipelineTenantAccess{Column: "tenant_id", Memberships: []api.DataPipelineTenantMembership{{Subject: "alice", Tenant: "a"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET initialized=true,state='paused' WHERE id=$1`, tenant.ID); err != nil {
		t.Fatal(err)
	}
	previewReq := api.DataPipelineTenantReadersRequest{ExpectedRevision: tenant.Revision, Action: "preview", Memberships: []api.DataPipelineTenantMembership{}}
	if _, err = other.ChangeDataPipelineTenantReaders(ctx, tenant.ID, previewReq); err == nil {
		t.Fatal("foreign account changed tenant memberships")
	}
	preview, err := client.ChangeDataPipelineTenantReaders(ctx, tenant.ID, previewReq)
	if err != nil || preview.TenantChange == nil || preview.TenantChange.State != "preview" || len(preview.Serving.TenantAccess.Memberships) != 1 {
		t.Fatal("preview applied or lost tenant access", preview, err)
	}
	approved, err := client.ChangeDataPipelineTenantReaders(ctx, tenant.ID, api.DataPipelineTenantReadersRequest{ExpectedRevision: preview.Revision, Action: "approve", ChangeID: preview.TenantChange.ID})
	if err != nil || approved.TenantChange.State != "approved" {
		t.Fatal("HTTP approval failed", approved, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET initialized=true,state='live',desired_state='running',observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	current, err := client.GetDataPipeline(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	evolution := api.DataPipelineEvolutionRequest{ExpectedRevision: current.Revision, Action: "preview", Kind: "relationships", Relationships: []api.DataPipelineRelationship{{Name: "self_order", SourceTable: "orders", TargetTable: "orders", SourceColumns: []string{"id"}, TargetColumns: []string{"id"}}}}
	if _, err = other.EvolveDataPipeline(ctx, p.ID, evolution); err == nil {
		t.Fatal("foreign account changed serving contract")
	}
	previewEvolution, err := client.EvolveDataPipeline(ctx, p.ID, evolution)
	if err != nil || previewEvolution.Evolution == nil || previewEvolution.Serving != nil {
		t.Fatal("HTTP preview mutated serving", previewEvolution, err)
	}
	approvedEvolution, err := client.EvolveDataPipeline(ctx, p.ID, api.DataPipelineEvolutionRequest{ExpectedRevision: previewEvolution.Revision, Action: "approve", ChangeID: previewEvolution.Evolution.ID})
	if err != nil || approvedEvolution.Serving.Version != 2 {
		t.Fatal("frozen evolution HTTP approval", approvedEvolution, err)
	}
	change := strings.Repeat("a", 64)
	metadata, _ := json.Marshal(map[string]any{"ID": change, "State": "pending", "Supported": true, "Changes": []map[string]any{{"Table": "orders", "Column": "total", "Kind": "added", "AfterType": "integer", "Reason": "Backfill actual values", "Safe": true}}})
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET state='blocked',reason='schema_change_pending',schema_change=$2,observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, p.ID, metadata); err != nil {
		t.Fatal(err)
	}
	current, err = client.GetDataPipeline(ctx, p.ID)
	if err != nil || current.SchemaReview == nil || len(current.SchemaReview.Changes) != 1 || current.SchemaReview.Changes[0].AfterType != "integer" {
		t.Fatal("schema review metadata missing", current, err)
	}
	schemaRequest := api.DataPipelineSchemaApprovalRequest{ExpectedRevision: current.Revision, ChangeID: change}
	if _, err = other.ApproveDataPipelineSchema(ctx, p.ID, schemaRequest); err == nil {
		t.Fatal("foreign schema approval accepted")
	}
	if _, err = client.ApproveDataPipelineSchema(ctx, p.ID, schemaRequest); err != nil {
		t.Fatal("exact schema HTTP approval", err)
	}
}

func TestUnconfiguredPipelineRoutesFailClosed(t *testing.T) {
	var h *Handler
	response := httptest.NewRecorder()
	h.Create(response, httptest.NewRequest("POST", "/", strings.NewReader(`{}`)), uuid.NewString())
	if response.Code != 503 {
		t.Fatalf("closed route returned %d", response.Code)
	}
}

func TestCheckBusyReturnsActionableConflict(t *testing.T) {
	response := httptest.NewRecorder()
	WriteProblem(response, datapipeline.ErrCheckBusy)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "data_connection_check_busy") {
		t.Fatalf("busy check did not return an actionable conflict: %d %s", response.Code, response.Body.String())
	}
}
