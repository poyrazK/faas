package tcpd

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestListenerStoreResolverUsesEnabledDurableRoute(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "tcpd-resolver-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "tcpd-" + uuid.NewString(), Status: state.AppActive, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := store.CreateTCPListener(ctx, state.TCPListener{
		AccountID: account.ID, AppID: app.ID, ListenerName: "postgres",
		GuestPort: 5432, PublicPort: 40127, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolver := ListenerStoreResolver{Store: store}
	route, ok, err := resolver.Resolve(ctx, listener.PublicPort)
	if err != nil || !ok {
		t.Fatalf("Resolve = %#v, %v, %v", route, ok, err)
	}
	if route.AppID != app.ID || route.ListenerName != "postgres" || route.GuestPort != 5432 || route.Protocol != "tcp" {
		t.Fatalf("route = %#v", route)
	}
	if _, ok, err := resolver.Resolve(ctx, 40128); err != nil || ok {
		t.Fatalf("missing route = ok:%v err:%v, want false,nil", ok, err)
	}
	if _, err := store.SetTCPListenerEnabled(ctx, listener.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := resolver.Resolve(ctx, listener.PublicPort); err != nil || ok {
		t.Fatalf("disabled route = ok:%v err:%v, want false,nil", ok, err)
	}
}

func TestListenerStoreResolverRequiresStore(t *testing.T) {
	_, _, err := ListenerStoreResolver{}.Resolve(context.Background(), 40129)
	if err == nil || errors.Is(err, state.ErrNotFound) {
		t.Fatalf("Resolve without store = %v, want configuration error", err)
	}
}
