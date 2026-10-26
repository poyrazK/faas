package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Instance rows carry the wake that started them, so `gregalectl obs node`
// and `obs app` can hand an operator straight to `obs wake <wake_id>`.
func TestObsNodeDetailInstancesCarryTheirWakeID(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	ctx := context.Background()
	store := *e.Store()
	node, err := store.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		Name: "node-wake-a", TargetURL: "unix:///run/faas/vmmd-wake-a.sock",
		VPCPUs: 8, MemMB: 16384, MaxConcurrency: 32, AdmissionCeilingMB: 12000,
	})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "node-wake-app", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	wakeID := uuid.NewString()
	if _, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 512, node.ID, wakeID); err != nil {
		t.Fatal(err)
	}
	rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/nodes/node-wake-a/detail", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var out api.ObsNodeDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Instances) != 1 || out.Instances[0].WakeID != wakeID || out.Instances[0].AppSlug != "node-wake-app" {
		t.Fatalf("instances = %+v", out.Instances)
	}
}
