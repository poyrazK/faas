package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObsCapacity_HappyPath_ReturnsBoundedSnapshot(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/capacity", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("capacity: got status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsCapacityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal capacity: %v", err)
	}
	if resp.GeneratedAt.IsZero() {
		t.Fatal("capacity: generated_at is zero")
	}
	if resp.Nodes == nil {
		t.Fatal("capacity: nodes must be a non-nil array")
	}
}

func TestObsTenant360_RejectsBadMonth(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/tenants/"+e.acct.ID+"/360?month=not-a-month", nil, nil)
	if rec.Code != 400 {
		t.Fatalf("tenant 360 bad month: got status %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestObsTenant360_HappyPath_ReturnsUsageAndBilling(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/tenants/"+e.acct.ID+"/360?month=2026-08", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("tenant 360: got status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsTenant360Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal tenant 360: %v", err)
	}
	if resp.Account.AccountID != e.acct.ID {
		t.Fatalf("tenant 360 account id: got %q, want %q", resp.Account.AccountID, e.acct.ID)
	}
	if resp.Usage.Month != "2026-08" {
		t.Fatalf("tenant 360 usage month: got %q", resp.Usage.Month)
	}
	if resp.Usage.Apps == nil || resp.Billing.Invoices == nil {
		t.Fatal("tenant 360: usage apps and billing invoices must be non-nil arrays")
	}
}

func TestObsTenant360_UnknownTenant(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/tenants/00000000-0000-0000-0000-000000000000/360", nil, nil)
	if rec.Code != 404 {
		t.Fatalf("tenant 360 unknown tenant: got status %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestObsTenantActivity_UnknownTenant(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/tenants/00000000-0000-0000-0000-000000000000/activity", nil, nil)
	if rec.Code != 404 {
		t.Fatalf("tenant activity: got status %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestObsAppDetail_RejectsBadID(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/apps/not-a-uuid", nil, nil)
	if rec.Code != 400 {
		t.Fatalf("app detail: got status %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestObsNodeDetail_UnknownNode(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/nodes/missing-node/detail", nil, nil)
	if rec.Code != 404 {
		t.Fatalf("node detail: got status %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestObsNodeMutation_RequiresConfirmation(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.doAdmin(t, "POST", "/v1/admin/ops/nodes/missing-node/drain", nil, nil)
	if rec.Code != 400 {
		t.Fatalf("node drain without confirmation: got status %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestObsNodeDrain_EnqueuesDurableIntentBeforeMutation(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	node, err := e.store.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name:               "node-intent-a",
		TargetURL:          "unix:///run/faas/vmmd-intent-a.sock",
		VPCPUs:             8,
		MemMB:              16384,
		MaxConcurrency:     32,
		AdmissionCeilingMB: 12000,
	})
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}

	rec := e.doAdmin(t, "POST", "/v1/admin/ops/nodes/node-intent-a/drain?confirm=true&reason=kernel_upgrade", nil, nil)
	if rec.Code != 202 {
		t.Fatalf("node drain: got status %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsNodeMutationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.IntentID == "" || resp.StatusURL == "" || resp.Kind != string(state.OperatorIntentKindNodeDrain) {
		t.Fatalf("missing durable intent receipt: %+v", resp)
	}
	if resp.PreviousLifecycle != string(state.NodeLifecycleActive) || resp.RequestedLifecycle != string(state.NodeLifecycleMaintenance) {
		t.Fatalf("unexpected lifecycle preflight: %+v", resp)
	}

	intent, err := e.store.GetOperatorIntent(context.Background(), resp.IntentID)
	if err != nil {
		t.Fatalf("read intent: %v", err)
	}
	if intent.Status != state.OperatorIntentPending || intent.ActorID != e.acct.ID || intent.Reason != "kernel_upgrade" {
		t.Fatalf("unexpected intent: %+v", intent)
	}
	if intent.TraceID == nil || *intent.TraceID == "" {
		t.Fatal("node intent did not retain request trace id")
	}

	// The HTTP handler records desired state only. schedd must claim the
	// durable row before any compute_nodes lifecycle mutation can land.
	fresh, err := e.store.NodeGet(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if fresh.Lifecycle != state.NodeLifecycleActive {
		t.Fatalf("handler mutated node before intent dispatch: lifecycle=%q", fresh.Lifecycle)
	}
}

func TestObsAccountMutation_RequiresConfirmation(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	target, err := e.store.CreateAccount(context.Background(), "tenant@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create target account: %v", err)
	}
	rec := e.doAdmin(t, "POST", "/v1/admin/ops/accounts/"+target.ID+"/suspend", nil, nil)
	if rec.Code != 400 {
		t.Fatalf("account suspend without confirmation: got status %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	current, err := e.store.AccountByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("reload target account: %v", err)
	}
	if current.Status != state.AccountActive {
		t.Fatalf("account status changed without confirmation: got %q", current.Status)
	}
}

func TestObsAccountMutation_RejectsSelf(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.doAdmin(t, "POST", "/v1/admin/ops/accounts/"+e.acct.ID+"/suspend?confirm=true", nil, nil)
	if rec.Code != 409 {
		t.Fatalf("self account suspend: got status %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	current, err := e.store.AccountByID(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("reload operator account: %v", err)
	}
	if current.Status != state.AccountActive {
		t.Fatalf("operator account status changed: got %q", current.Status)
	}
}

func TestObsAccountSuspend_UpdatesStatus(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	target, err := e.store.CreateAccount(context.Background(), "tenant-suspend@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create target account: %v", err)
	}
	rec := e.doAdmin(t, "POST", "/v1/admin/ops/accounts/"+target.ID+"/suspend?confirm=true&reason=customer_request", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("account suspend: got status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	current, err := e.store.AccountByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("reload target account: %v", err)
	}
	if current.Status != state.AccountSuspended {
		t.Fatalf("account status: got %q, want %q", current.Status, state.AccountSuspended)
	}
}

func TestProjectObsNodeApps_SeparatesLiveStates(t *testing.T) {
	now := time.Now().UTC()
	apps := []state.App{{ID: "app-1", AccountID: "acct-1", Slug: "orders", Status: state.AppActive}}
	instances := []state.Instance{
		{AppID: "app-1", State: "RUNNING", RAMMB: 128, LastRequestAt: now.Add(-time.Minute)},
		{AppID: "app-1", State: "COLD_BOOTING", RAMMB: 256, LastRequestAt: now},
		{AppID: "app-1", State: "PARKED", RAMMB: 512, LastRequestAt: now.Add(-time.Hour)},
	}
	rows := projectObsNodeApps(apps, instances)
	if len(rows) != 1 {
		t.Fatalf("node apps: got %d rows, want 1", len(rows))
	}
	if rows[0].InstancesLive != 2 || rows[0].InstancesRunning != 1 || rows[0].InstancesColdBooting != 1 {
		t.Fatalf("node app live counters: got %+v", rows[0])
	}
	if rows[0].RAMUsedMB != 400 {
		t.Fatalf("node app RAM: got %d, want 400", rows[0].RAMUsedMB)
	}
}

func TestProjectObsDrainStatus_TracksLiveStates(t *testing.T) {
	rows := []state.Instance{
		{State: "RUNNING"},
		{State: "WAKING"},
		{State: "COLD_BOOTING"},
		{State: "PARKED"},
	}
	status := projectObsDrainStatus(rows)
	if status.TotalInstances != 4 || status.LiveInstances != 3 {
		t.Fatalf("drain totals: got %+v", status)
	}
	if status.RunningInstances != 1 || status.WakingInstances != 1 || status.ColdBooting != 1 {
		t.Fatalf("drain live-state counters: got %+v", status)
	}
	if status.DrainSafe {
		t.Fatal("drain marked safe while live instances remain")
	}

	status = projectObsDrainStatus([]state.Instance{{State: "PARKED"}})
	if !status.DrainSafe || status.LiveInstances != 0 {
		t.Fatalf("parked-only node should be drain safe: %+v", status)
	}
}
