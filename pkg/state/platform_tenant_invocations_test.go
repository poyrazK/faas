package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type tenantInvocationStore interface {
	state.Store
	state.PlatformTenantStore
}

func TestMemPlatformTenantInvocationLifecycle(t *testing.T) {
	testPlatformTenantInvocationLifecycle(t, state.NewMemStore(), context.Background())
}

func testPlatformTenantInvocationLifecycle(t *testing.T, store tenantInvocationStore, ctx context.Context) {
	t.Helper()
	account, err := store.CreateAccount(ctx, "jobs-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "jobs-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	testLegacySyntheticInvocationAdmission(t, store, ctx, app.ID)
	alice, _, err := store.CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 250)
	if err != nil {
		t.Fatal(err)
	}
	bob, _, err := store.CreatePlatformTenant(ctx, account.ID, "bob", "Bob", 250)
	if err != nil {
		t.Fatal(err)
	}
	enqueue := func(tenant string) state.Invocation {
		t.Helper()
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID,
			PlatformTenantID: tenant, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/documents",
			Payload: []byte(`{"title":"queued"}`), DueAt: time.Now().Add(-time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	a, b := enqueue(alice.ID), enqueue(bob.ID)
	if _, err := store.SetPlatformTenantStatus(ctx, account.ID, alice.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, PlatformTenantID: alice.ID,
		Source: state.InvocationReplay, Method: "POST", Path: "/documents", DueAt: time.Now()}); !errors.Is(err, state.ErrPlatformTenantSuspended) {
		t.Fatalf("suspended enqueue: %v", err)
	}
	for _, claim := range []func() (state.Invocation, error){
		func() (state.Invocation, error) { return store.ClaimInvocation(ctx, a.ID, "", 30) },
		func() (state.Invocation, error) { return store.ClaimInvocationWithCap(ctx, a.ID, "", 30, 1) },
	} {
		if _, err := claim(); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("suspended claim: %v", err)
		}
	}
	held, _ := store.InvocationByID(ctx, a.ID)
	if held.State != state.InvocationPending || held.Attempts != 0 || held.QuotaReserved {
		t.Fatalf("suspended work changed: %+v", held)
	}
	// A suspended customer's attempted claim did not reserve the account's slot.
	claimed, err := store.ClaimInvocationWithCap(ctx, b.ID, "", 30, 1)
	if err != nil || claimed.PlatformTenantID != bob.ID {
		t.Fatalf("other customer claim: %+v %v", claimed, err)
	}
	if err := store.CompleteInvocation(ctx, b.ID, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPlatformTenantStatus(ctx, account.ID, alice.ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimInvocationWithCap(ctx, a.ID, "", 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	// A durable retry keeps the tenant and increments only real dispatch attempts.
	if err := store.FailInvocation(ctx, a.ID, "temporary", time.Millisecond, 3, state.WithClaimAttempt(claimed.Attempts)); err != nil {
		t.Fatal(err)
	}
	retry, err := store.ClaimInvocationWithCap(ctx, a.ID, "", 30, 1)
	if err != nil || retry.PlatformTenantID != alice.ID || retry.Attempts != 2 {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	// Payload/header/wire identity cannot alter the persisted customer or request.
	forged := retry
	forged.PlatformTenantID = bob.ID
	if _, err := state.AdmitPlatformTenantInvocation(ctx, store, app.ID, forged); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("forged identity: %v", err)
	}
	forged.PlatformTenantID = ""
	if _, err := state.AdmitPlatformTenantInvocation(ctx, store, app.ID, forged); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("omitted identity: %v", err)
	}
	forged.PlatformTenantID = alice.ID
	forged.Path = "/forged"
	admitted, err := state.AdmitPlatformTenantInvocation(ctx, store, app.ID, forged)
	if err != nil || admitted.Path != "/documents" {
		t.Fatalf("persisted request admission: %+v %v", admitted, err)
	}
	if err := store.CancelInvocation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	cancelled, _ := store.InvocationByID(ctx, a.ID)
	if cancelled.PlatformTenantID != alice.ID || cancelled.State != state.InvocationCancelled {
		t.Fatalf("cancel: %+v", cancelled)
	}
	if _, err := state.AdmitPlatformTenantInvocation(ctx, store, app.ID, retry); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cancelled dispatch admission: %v", err)
	}
	for _, source := range []state.InvocationSource{state.InvocationQueue, state.InvocationCron} {
		_, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, PlatformTenantID: alice.ID, Source: source, Method: "POST", Path: "/", DueAt: time.Now()})
		if err == nil {
			t.Fatalf("unsupported bound source %s accepted", source)
		}
	}
}

// adr: 376
// Legacy cron and queue-trigger envelopes have synthetic IDs rather than
// durable invocation UUIDs. They cannot carry a platform tenant identity.
func testLegacySyntheticInvocationAdmission(t *testing.T, store tenantInvocationStore, ctx context.Context, appID string) {
	t.Helper()
	for _, tc := range []struct {
		name, id string
		source   state.InvocationSource
	}{
		{"cron", "cron-" + uuid.NewString(), state.InvocationCron},
		{"queue-trigger", "trigger-" + uuid.NewString() + "-" + uuid.NewString(), "esm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := state.Invocation{ID: tc.id, AppID: appID, Source: tc.source, Method: "POST", Path: "/_triggers/" + string(tc.source)}
			admitted, err := state.AdmitPlatformTenantInvocation(ctx, store, appID, inv)
			if err != nil || admitted.ID != inv.ID || admitted.Path != inv.Path {
				t.Fatalf("legacy envelope admission: %+v %v", admitted, err)
			}
			inv.PlatformTenantID = uuid.NewString()
			if _, err := state.AdmitPlatformTenantInvocation(ctx, store, appID, inv); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("tenant identity on synthetic ID: %v", err)
			}
		})
	}
}
