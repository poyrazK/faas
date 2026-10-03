package main

// adr: 281

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

type reconcileRealtimeRegistrar struct {
	registered []realtime.Endpoint
	removed    []string
	fail       map[string]error
	inventory  []string
}

func TestNodeChannelRouteSnapshotUsesCompactEndpoint(t *testing.T) {
	op := &fakeRealtimeNode{
		connections:   []realtime.ConnectionInfo{{EndpointID: "endpoint", Channels: []string{"connection-snapshot"}}},
		channelRoutes: []realtime.ChannelRoute{{EndpointID: "endpoint", Channel: "compact-snapshot"}},
	}

	routes, err := nodeChannelRouteSnapshot(context.Background(), op)
	if err != nil {
		t.Fatalf("nodeChannelRouteSnapshot: %v", err)
	}
	want := []realtime.ChannelRoute{{EndpointID: "endpoint", Channel: "compact-snapshot"}}
	if len(routes) != len(want) || routes[0] != want[0] {
		t.Fatalf("routes = %+v, want %+v", routes, want)
	}
	if op.routeReads != 1 || op.connReads != 0 {
		t.Fatalf("snapshot reads = (routes %d, connections %d), want (1, 0)", op.routeReads, op.connReads)
	}
}

func TestNodeChannelRouteSnapshotFallsBackForOlderNode(t *testing.T) {
	op := &fakeRealtimeNode{
		connections:      []realtime.ConnectionInfo{{EndpointID: "endpoint", Channels: []string{"updates"}}},
		channelRoutesErr: &realtime.ManagementError{StatusCode: http.StatusNotFound},
	}

	routes, err := nodeChannelRouteSnapshot(context.Background(), op)
	if err != nil {
		t.Fatalf("nodeChannelRouteSnapshot: %v", err)
	}
	want := []realtime.ChannelRoute{{EndpointID: "endpoint", Channel: "updates"}}
	if len(routes) != len(want) || routes[0] != want[0] {
		t.Fatalf("routes = %+v, want fallback %+v", routes, want)
	}
	if op.routeReads != 1 || op.connReads != 1 {
		t.Fatalf("snapshot reads = (routes %d, connections %d), want (1, 1)", op.routeReads, op.connReads)
	}
}

func (r *reconcileRealtimeRegistrar) ListEndpointInventory(context.Context) (realtime.EndpointInventory, error) {
	return realtime.EndpointInventory{IDs: append([]string(nil), r.inventory...), NodesQueried: 1}, nil
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

func TestReconcileManagedRealtimeEndpointsRemovesDeletedRegistration(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "deleted-realtime-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "deleted-realtime-" + uuid.NewString(), Status: state.AppActive, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		ID: "deleted-endpoint", AccountID: account.ID, AppID: app.ID,
		CallbackURL: "https://example.com/callback", Enabled: true,
	}, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteManagedRealtimeEndpoint(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	registrar := &reconcileRealtimeRegistrar{inventory: []string{row.ID}}
	srv := newServer(store, discardLogger(), "gregale.dev", noopNotifier{})
	srv.realtimeRegistrar = registrar
	if err := srv.reconcileManagedRealtimeEndpoints(ctx); err != nil {
		t.Fatal(err)
	}
	if len(registrar.removed) != 1 || registrar.removed[0] != row.ID {
		t.Fatalf("removed endpoints = %v, want %s", registrar.removed, row.ID)
	}
}

func TestReapManagedRealtimeConnectionOwnersRemovesOnlyExpiredRows(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	if _, err := store.ClaimManagedRealtimeConnectionOwner(ctx, "expired-connection", "endpoint", "node", time.Millisecond); err != nil {
		t.Fatalf("claim expired owner: %v", err)
	}
	if _, err := store.ClaimManagedRealtimeConnectionOwner(ctx, "live-connection", "endpoint", "node", time.Minute); err != nil {
		t.Fatalf("claim live owner: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	srv := newServer(store, discardLogger(), "gregale.dev", noopNotifier{})
	removed, err := srv.reapManagedRealtimeConnectionOwners(ctx)
	if err != nil {
		t.Fatalf("reap owners: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := store.GetManagedRealtimeConnectionOwner(ctx, "expired-connection", "endpoint"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired owner lookup = %v, want ErrNotFound", err)
	}
	if _, err := store.GetManagedRealtimeConnectionOwner(ctx, "live-connection", "endpoint"); err != nil {
		t.Fatalf("live owner lookup = %v, want present", err)
	}
}

// ADR-156 dispatches /__gregale/realtime/ before hostname lookup, so the
// gateway never applies app or account lifecycle to managed sockets. The
// reconciler must withdraw endpoints whose app is deleted or whose account
// is suspended, and keep past_due accounts (their apps keep running).
func TestReconcileManagedRealtimeEndpointsFollowsOwnerLifecycle(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	endpointFor := func(name string, status state.AccountStatus, deleteApp bool) string {
		t.Helper()
		account, err := store.CreateAccount(ctx, name+"-"+uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		if status != state.AccountActive {
			if err := store.UpdateAccountStatus(ctx, account.ID, status); err != nil {
				t.Fatal(err)
			}
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: name + "-" + uuid.NewString()[:8], Status: state.AppActive, RAMMB: 512})
		if err != nil {
			t.Fatal(err)
		}
		row, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
			ID: name + "-endpoint", AccountID: account.ID, AppID: app.ID,
			CallbackURL: "https://example.com/callback", Enabled: true,
		}, 10, 10)
		if err != nil {
			t.Fatal(err)
		}
		if deleteApp {
			if _, err := store.ScheduleAppDeletion(ctx, app.ID, time.Time{}); err != nil {
				t.Fatal(err)
			}
		}
		return row.ID
	}
	active := endpointFor("active", state.AccountActive, false)
	pastDue := endpointFor("pastdue", state.AccountPastDue, false)
	suspended := endpointFor("suspended", state.AccountSuspended, false)
	deletedApp := endpointFor("deletedapp", state.AccountActive, true)

	registrar := &reconcileRealtimeRegistrar{}
	srv := newServer(store, discardLogger(), "gregale.dev", noopNotifier{})
	srv.realtimeRegistrar = registrar
	if err := srv.reconcileManagedRealtimeEndpoints(ctx); err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, endpoint := range registrar.registered {
		registered[endpoint.ID] = true
	}
	removed := map[string]bool{}
	for _, id := range registrar.removed {
		removed[id] = true
	}
	for _, id := range []string{active, pastDue} {
		if !registered[id] || removed[id] {
			t.Errorf("endpoint %s: registered=%v removed=%v, want served", id, registered[id], removed[id])
		}
	}
	for _, id := range []string{suspended, deletedApp} {
		if registered[id] || !removed[id] {
			t.Errorf("endpoint %s: registered=%v removed=%v, want withdrawn", id, registered[id], removed[id])
		}
	}
}
