package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSynthAdapterPlatformTenantDurableAdmission(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "synth-tenant@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "synth-tenant", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 250)
	if err != nil {
		t.Fatal(err)
	}
	flagContext, err := flags.EncodePropagationHeader(flags.PropagationContext{
		Version: flags.PropagationContextVersion, CustomerID: tenant.ID,
		Decisions: []flags.PropagationDecision{{
			Decision: flags.Decision{Flag: "new-export", Value: true, ConfigVersion: 7, Reason: "default", Source: "configuration"},
			Origin:   flags.EvidenceOrigin{AppID: uuid.NewString(), EnvironmentID: uuid.NewString()},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	invocationHeaders, err := json.Marshal(map[string]string{
		"X-Faas-Platform-Tenant-Id": "forged",
		api.FlagContextHeader:       flagContext,
	})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, PlatformTenantID: tenant.ID,
		Source: state.InvocationQueue, Method: "POST", Path: "/documents", Payload: []byte(`{}`), Headers: invocationHeaders, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	inv, err = store.ClaimInvocation(ctx, inv.ID, "", 30)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	adapter := &synthAdapter{store: store, forward: func(gateway.Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Header.Get(api.PlatformTenantIDHeader) != tenant.ID {
				t.Fatalf("worker tenant=%q", r.Header.Get(api.PlatformTenantIDHeader))
			}
			if r.Header.Get(api.FlagContextHeader) != flagContext {
				t.Fatalf("worker flag context=%q, want persisted context", r.Header.Get(api.FlagContextHeader))
			}
			if r.URL.Path != "/documents" {
				t.Fatalf("wire path replaced persisted path: %s", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		})
	}}
	target := gateway.Target{InstanceID: "warm", NodeID: "node"}
	for _, tenantID := range []string{"", "forged"} {
		wire := inv
		wire.PlatformTenantID = tenantID
		if _, _, err := adapter.InvokeWithTargetStatus(ctx, app.ID, wire, target); err == nil || calls != 0 {
			t.Fatal("forged or omitted tenant reached worker")
		}
	}
	wire := inv
	wire.Path = "/forged"
	if _, status, err := adapter.InvokeWithTargetStatus(ctx, app.ID, wire, target); err != nil || status != http.StatusOK || calls != 1 {
		t.Fatalf("tenant delivery: %d %v calls=%d", status, err, calls)
	}
	if err := store.CancelInvocation(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := adapter.InvokeWithTargetStatus(ctx, app.ID, inv, target); err == nil || calls != 1 {
		t.Fatal("cancelled work reached worker")
	}
}
