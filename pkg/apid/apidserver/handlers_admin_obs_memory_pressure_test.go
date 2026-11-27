package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The memory pressure report grades each node against its ceiling and
// counts the window's evictions and OOM kills by node and app
// (ADR-20261123190507).
func TestObsMemoryPressureReport(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	ctx := context.Background()
	store := *e.Store()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "fsn-mp", TargetURL: "unix:///run/faas/vmmd.sock"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodeMemoryReport(ctx, state.NodeMemoryReport{NodeID: node.ID, MeasuredMB: 999_999, FrozenInstances: 2, MeasuredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "mp-api", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateStopped), 512, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "schedd", state.MemoryPressureKindEviction, &ins.ID, []byte(`{"from":"running","to":"stopped","reason":"measured_memory"}`)); err != nil {
		t.Fatal(err)
	}
	oom := fmt.Sprintf(`{"instance_id":%q,"app_id":%q,"peak_mb":601,"plan_mb":512}`, ins.ID, app.ID)
	if err := store.AppendEvent(ctx, "schedd", state.MemoryPressureKindOOMKill, nil, []byte(oom)); err != nil {
		t.Fatal(err)
	}

	rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/memory-pressure?window=1h", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var r api.ObsMemoryPressureReport
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.WindowSeconds != 3600 || r.Totals.Evictions != 1 || r.Totals.OOMKills != 1 || r.Totals.AppsAffected != 1 {
		t.Fatalf("report = %+v", r.Totals)
	}
	var mp *api.ObsMemoryNode
	for i := range r.Nodes {
		if r.Nodes[i].NodeID == node.ID {
			mp = &r.Nodes[i]
		}
	}
	if mp == nil || mp.State != "over" || mp.Frozen != 2 || mp.Evictions != 1 || mp.OOMKills != 1 {
		t.Fatalf("node = %+v", mp)
	}
	if len(r.Apps) != 1 || r.Apps[0].AppSlug != "mp-api" || r.Apps[0].MaxPeakMB != 601 || len(r.Events) != 2 {
		t.Fatalf("apps=%+v events=%+v", r.Apps, r.Events)
	}

	if rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/memory-pressure?window=30d", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad window: status %d", rec.Code)
	}
}
