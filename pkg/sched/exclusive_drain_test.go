package sched

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

type exclusiveDrainGateway struct {
	invocation state.Invocation
	wake       WakeResult
	calls      int
}

func (g *exclusiveDrainGateway) SynthesizeRequest(context.Context, string, string, string) error {
	return nil
}

func (g *exclusiveDrainGateway) Invoke(_ context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	return inv, nil
}

func (g *exclusiveDrainGateway) InvokeWithWake(_ context.Context, appID string, inv state.Invocation, wake WakeResult) (state.Invocation, error) {
	g.calls++
	g.invocation = inv
	g.wake = wake
	if appID != inv.AppID || inv.ExclusiveClaim == nil {
		return inv, state.ErrInvalidArgument
	}
	inv.Result = json.RawMessage(`{"synced":true}`)
	return inv, nil
}

func TestExclusiveOperationDrainDispatchesAndCommitsUnderClaim(t *testing.T) {
	ctx := context.Background()
	drain, base, _, _, _ := newDrainHarness(t, api.PlanPro, false)
	store := base.(*state.MemStore)
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) != 1 {
		t.Fatalf("ListAllApps() = %d rows, %v", len(apps), err)
	}
	app := apps[0]
	ownerStore := state.ExclusiveWorkStore(store)
	if _, err := ownerStore.UpsertExclusiveWorkPolicy(ctx, app.AccountID, exclusivework.Policy{
		Name: "crm-sync", Scope: "account", MemberAppIDs: []string{app.ID},
		Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 30,
	}); err != nil {
		t.Fatalf("UpsertExclusiveWorkPolicy(): %v", err)
	}
	operation, _, err := ownerStore.AdmitExclusiveOperation(ctx, state.ExclusiveAdmission{
		AccountID: app.AccountID, AppID: app.ID, PolicyName: "crm-sync",
		Key:     json.RawMessage(`"customer:acme:crm-sync"`),
		Request: json.RawMessage(`{"method":"POST","path":"/sync","payload":{"mode":"incremental"}}`),
	})
	if err != nil {
		t.Fatalf("AdmitExclusiveOperation(): %v", err)
	}

	gateway := &exclusiveDrainGateway{}
	drain.gateway = gateway
	drain.log = slog.New(slog.NewTextHandler(testWriter{t}, nil))
	drain.Tick(ctx)

	if gateway.calls != 1 {
		t.Fatalf("gateway calls = %d, want one dispatch", gateway.calls)
	}
	if gateway.invocation.Source != state.InvocationExclusiveOperation || gateway.invocation.ExclusiveClaim == nil {
		t.Fatalf("gateway did not receive an exclusive claim: %+v", gateway.invocation)
	}
	if gateway.invocation.Path != "/sync" || string(gateway.invocation.Payload) != `{"mode":"incremental"}` {
		t.Fatalf("gateway request = path %q payload %s", gateway.invocation.Path, gateway.invocation.Payload)
	}
	if gateway.wake.InstanceID == "" || gateway.wake.WakeID == "" || gateway.wake.NodeID == "" {
		t.Fatalf("dispatch had no host-owned VM incarnation: %+v", gateway.wake)
	}
	committed, err := ownerStore.ExclusiveOperationByID(ctx, app.AccountID, operation.ID)
	if err != nil || committed.State != "completed" || string(committed.Result) != `{"synced":true}` {
		t.Fatalf("operation after drain = state %q result %s, %v", committed.State, committed.Result, err)
	}
	if committed.Generation != gateway.invocation.ExclusiveClaim.Generation {
		t.Fatalf("committed generation %d differs from dispatched claim %d", committed.Generation, gateway.invocation.ExclusiveClaim.Generation)
	}
}

var _ prewokenGatewaySynth = (*exclusiveDrainGateway)(nil)
