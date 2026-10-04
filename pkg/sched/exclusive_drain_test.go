package sched

// adr: 387

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

func TestExclusiveOperationDrainUnparksAcceptedDeployment(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		name := "current"
		if pinned {
			name = "pinned"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			drain, base, _, _, _ := newDrainHarness(t, api.PlanPro, false)
			store := base.(*state.MemStore)
			apps, err := store.ListAllApps(ctx)
			if err != nil || len(apps) != 1 {
				t.Fatalf("apps=%d err=%v", len(apps), err)
			}
			app := apps[0]
			deployment, err := store.LiveDeployment(ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			parked := state.AppEvictedCold
			manifest := app.Manifest
			manifest.RevisionPinTTLSeconds = 60
			if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &parked, Manifest: &manifest}); err != nil {
				t.Fatal(err)
			}
			owners := state.ExclusiveWorkStore(store)
			if _, err := owners.UpsertExclusiveWorkPolicy(ctx, app.AccountID, exclusivework.Policy{
				Name: "orders", Scope: "account", MemberAppIDs: []string{app.ID},
				Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 30,
			}); err != nil {
				t.Fatal(err)
			}
			request := api.InvokeRequest{Method: "POST", Path: "/sync"}
			if pinned {
				request.Headers, err = json.Marshal(map[string]string{api.RevisionHeader: deployment.ID})
				if err != nil {
					t.Fatal(err)
				}
			}
			payload, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			operation, _, err := owners.AdmitExclusiveOperation(ctx, state.ExclusiveAdmission{
				AccountID: app.AccountID, AppID: app.ID, PolicyName: "orders",
				Key: json.RawMessage(`"orders"`), Request: payload,
			})
			if err != nil {
				t.Fatal(err)
			}
			if pinned {
				// Cut over after acceptance. The old revision stays explicitly
				// reachable, and a wake must preserve the accepted pin.
				newer, err := store.CreateDeployment(ctx, state.Deployment{
					AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:newer",
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, newer.ID); err != nil {
					t.Fatal(err)
				}
				live, err := store.LiveDeployment(ctx, app.ID)
				if err != nil || live.ID != newer.ID {
					t.Fatalf("current deployment=%s want=%s err=%v", live.ID, newer.ID, err)
				}
				_, version, err := state.ResolveInvocationVersion(ctx, store, state.Invocation{
					AppID: app.ID, Headers: request.Headers,
				})
				if err != nil || version.DeploymentID != deployment.ID {
					t.Fatalf("accepted pin=%s want=%s err=%v", version.DeploymentID, deployment.ID, err)
				}
			}
			gateway := &exclusiveDrainGateway{}
			drain.gateway = gateway
			drain.Tick(ctx)
			ready, err := store.AppByID(ctx, app.ID)
			if err != nil || ready.Status != state.AppActive {
				t.Fatalf("managed wake left app parked: status=%s err=%v", ready.Status, err)
			}
			completed, err := owners.ExclusiveOperationByID(ctx, app.AccountID, operation.ID)
			if err != nil || completed.State != "completed" || gateway.calls != 1 || gateway.wake.DeploymentID != deployment.ID {
				t.Fatalf("operation=%+v wake=%+v calls=%d err=%v", completed, gateway.wake, gateway.calls, err)
			}
		})
	}
}

var _ prewokenGatewaySynth = (*exclusiveDrainGateway)(nil)
