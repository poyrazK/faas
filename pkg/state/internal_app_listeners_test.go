package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Making an app internal removed its public HTTP routes but left its raw
// TCP and UDP listeners open to the internet (bug hunt 2026-10-28 H52).
func TestMemStoreInternalAppListenersAreNotPublic(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	account, err := m.CreateAccount(ctx, "internal-listener@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "internal-db", Status: state.AppActive, RAMMB: 256, Visibility: api.AppVisibilityInternal})
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := m.CreateTCPListener(ctx, state.TCPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "pg", GuestPort: 5432, PublicPort: 40130, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.TCPListenerByPublicPort(ctx, tcp.PublicPort); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("internal app TCP listener by port = %v, want ErrNotFound", err)
	}
	if _, err := m.CreateUDPListener(ctx, state.UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "dns", GuestPort: 53, PublicPort: 40131, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if rows, err := m.ListEnabledUDPListeners(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("enabled UDP listeners = %+v, %v; want none for an internal app", rows, err)
	}
}
