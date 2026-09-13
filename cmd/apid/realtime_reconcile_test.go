package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

type reconcileRealtimeRegistrar struct {
	registered []realtime.Endpoint
	removed    []string
	fail       map[string]error
}

func (r *reconcileRealtimeRegistrar) RegisterEndpoint(_ context.Context, endpoint realtime.Endpoint) error {
	if err := r.fail[endpoint.ID]; err != nil {
		return err
	}
	r.registered = append(r.registered, endpoint)
	return nil
}

func (r *reconcileRealtimeRegistrar) RemoveEndpoint(_ context.Context, endpointID string) error {
	r.removed = append(r.removed, endpointID)
	return nil
}

func TestReconcileManagedRealtimeEndpointsRepairsAllRows(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "reconcile-realtime-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "reconcile-realtime-" + uuid.NewString(), Status: state.AppActive, RAMMB: 512})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if _, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		ID: "enabled-endpoint", AccountID: account.ID, AppID: app.ID,
		CallbackURL: "https://example.com/callback", ConnectPath: "/connect",
		MessagePath: "/message", DisconnectPath: "/disconnect", Enabled: true,
	}, 10, 10); err != nil {
		t.Fatalf("create enabled endpoint: %v", err)
	}
	if _, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		ID: "disabled-endpoint", AccountID: account.ID, AppID: app.ID, Enabled: false,
	}, 10, 10); err != nil {
		t.Fatalf("create disabled endpoint: %v", err)
	}
	if _, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		ID: "healthy-endpoint", AccountID: account.ID, AppID: app.ID,
		CallbackURL: "https://example.com/healthy", Enabled: true,
	}, 10, 10); err != nil {
		t.Fatalf("create healthy endpoint: %v", err)
	}

	registrarErr := errors.New("realtime node unavailable")
	registrar := &reconcileRealtimeRegistrar{fail: map[string]error{"enabled-endpoint": registrarErr}}
	srv := newServer(store, discardLogger(), "gregale.dev", noopNotifier{})
	srv.realtimeRegistrar = registrar

	err = srv.reconcileManagedRealtimeEndpoints(ctx)
	if !errors.Is(err, registrarErr) || !strings.Contains(err.Error(), "enabled-endpoint") {
		t.Fatalf("reconcile error = %v, want endpoint error", err)
	}
	if len(registrar.registered) != 1 || registrar.registered[0].ID != "healthy-endpoint" {
		t.Fatalf("registered = %+v, want healthy endpoint despite failed sibling", registrar.registered)
	}
	if len(registrar.removed) != 1 || registrar.removed[0] != "disabled-endpoint" {
		t.Fatalf("removed = %v, want disabled endpoint", registrar.removed)
	}
}

func TestReconcileManagedRealtimeEndpointsNoRegistrarIsNoop(t *testing.T) {
	srv := newServer(state.NewMemStore(), discardLogger(), "gregale.dev", noopNotifier{})
	if err := srv.reconcileManagedRealtimeEndpoints(context.Background()); err != nil {
		t.Fatalf("reconcile without registrar = %v, want nil", err)
	}
}
