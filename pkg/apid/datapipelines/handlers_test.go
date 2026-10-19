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
	if err = checkStore.FinishCheck(ctx, job, api.DataConnectionCheck{State: "passed", Checks: []api.DataConnectionIssue{}, Tables: []api.DataSourceTable{{Name: "orders", Supported: true, Columns: []api.DataSourceColumn{}, Issues: []api.DataConnectionIssue{}}}}); err != nil {
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
	request := httptest.NewRequest("POST", "/v1/data/connections", strings.NewReader(`{"name":"bad","connection_url":"postgres://u:p@db/db?sslmode=verify-full","account_id":"forged"}`))
	response := httptest.NewRecorder()
	h.CreateConnection(response, request, account)
	if response.Code != 400 || strings.Contains(response.Body.String(), "postgres:") {
		t.Fatal("unknown account field accepted or credential echoed")
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
