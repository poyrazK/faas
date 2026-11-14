package datapipelines

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestDurableSetupHTTPPlanRecoveryAndForeignIsolation(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	schema, err := os.ReadFile("../../datapipeline/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	account, foreign := uuid.NewString(), uuid.NewString()
	allocations := 0
	store := datapipeline.NewPostgresStore(pool)
	service := &datapipeline.Service{Store: store, Recipient: key.Recipient(), Allowed: func(context.Context, string) bool { return true }, PlanMirror: func(context.Context, string, string) (datapipeline.MirrorPlan, error) {
		return datapipeline.MirrorPlan{Region: "test", StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600}, nil
	}, EnsureMirror: func(context.Context, datapipeline.Setup) error { allocations++; return nil }, MirrorState: func(context.Context, string, string) (string, error) { return "ready", nil }}
	c, err := service.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "orders-source", ConnectionURL: "postgres://r:private-sentinel@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.RequestCheck(ctx, account, c.ID, c.Revision, ""); err != nil {
		t.Fatal(err)
	}
	check, err := store.ClaimCheck(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	capacity := &api.DataPipelineCapacity{State: "ready", Source: &api.DataEndpointCapacity{State: "ready", UsableConnections: 100, RequiredConnections: 5, ObservedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(api.DataPipelineCapacityMaxAge).Format(time.RFC3339Nano)}}
	if err = store.FinishCheck(ctx, check, api.DataConnectionCheck{State: "passed", Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}, Capacity: capacity}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Service: service}
	mux := http.NewServeMux()
	wrap := func(fn func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			owner := account
			if r.Header.Get("Authorization") == "Bearer foreign" {
				owner = foreign
			}
			fn(w, r, owner)
		}
	}
	mux.HandleFunc("POST /v1/data/pipeline-setups/plan", wrap(h.PlanSetup))
	mux.HandleFunc("POST /v1/data/pipeline-setups", wrap(h.StartSetup))
	mux.HandleFunc("GET /v1/data/pipeline-setups", wrap(h.Setups))
	mux.HandleFunc("GET /v1/data/pipeline-setups/{id}", wrap(h.GetSetup))
	mux.HandleFunc("POST /v1/data/pipeline-setups/{id}/control", wrap(h.ControlSetup))
	server := httptest.NewServer(mux)
	defer server.Close()
	client := api.NewClient(server.URL, "owner")
	outsider := api.NewClient(server.URL, "foreign")
	req := api.DataPipelineSetupRequest{Name: "customer-orders", SourceConnectionID: c.ID, Tables: []string{"orders"}, ReadSubjects: []string{"alice"}, FreshnessTargetSeconds: 30}
	plan, err := client.PlanDataPipelineSetup(ctx, req)
	if err != nil || !plan.CreatesMirror || allocations != 0 {
		t.Fatal(plan, allocations, err)
	}
	before, err := client.ListDataPipelineSetups(ctx)
	if err != nil || len(before.Items) != 0 {
		t.Fatal("plan saved resource intent", before, err)
	}
	job, err := client.CreateDataPipelineSetup(ctx, req)
	if err != nil || job.ID == "" || job.DatabaseID == "" || job.Request.FreshnessTargetSeconds != 30 {
		t.Fatal(job, err)
	}
	recovered, err := client.CreateDataPipelineSetup(ctx, req)
	if err != nil || recovered.ID != job.ID || recovered.DatabaseID != job.DatabaseID || allocations != 0 {
		t.Fatal("duplicate setup reallocated", recovered, err)
	}
	if _, err = outsider.GetDataPipelineSetup(ctx, job.ID); err == nil {
		t.Fatal("foreign account read setup")
	}
	list, err := outsider.ListDataPipelineSetups(ctx)
	if err != nil || len(list.Items) != 0 {
		t.Fatal("foreign setup list leaked", list, err)
	}
	raw, _ := json.Marshal(job)
	for _, secret := range []string{"private-sentinel", "Ciphertext", "LeaseToken", "DataAPIPlan", "MirrorPlan", "db.example"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private setup data escaped", secret)
		}
	}
	stopped, err := client.ControlDataPipelineSetup(ctx, job.ID, api.DataPipelineSetupControlRequest{ExpectedRevision: job.Revision, Action: "cancel"})
	if err != nil || stopped.State != "cancelled" || stopped.DatabaseID != job.DatabaseID {
		t.Fatal("cancel discarded recovery state", stopped, err)
	}
}
