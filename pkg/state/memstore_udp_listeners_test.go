package state

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func udpListenerFixture(t *testing.T) (*MemStore, context.Context, Account, App) {
	t.Helper()
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "udp-listener-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, Slug: "udp-" + uuid.NewString(), Status: AppActive, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, account, app
}

func TestMemStoreUDPListenerLifecycleAndRouteLookup(t *testing.T) {
	m, ctx, account, app := udpListenerFixture(t)
	created, err := m.CreateUDPListener(ctx, UDPListener{
		AccountID: account.ID, AppID: app.ID, ListenerName: "Postgres",
		GuestPort: 5432, PublicPort: 40123, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Protocol != "udp" || created.ListenerName != "postgres" {
		t.Fatalf("normalized listener = %+v", created)
	}
	byName, err := m.UDPListenerByAppAndName(ctx, app.ID, "POSTGRES")
	if err != nil || byName.ID != created.ID {
		t.Fatalf("lookup by name = %+v, %v", byName, err)
	}
	byPort, err := m.UDPListenerByPublicPort(ctx, created.PublicPort)
	if err != nil || byPort.ID != created.ID {
		t.Fatalf("lookup by port = %+v, %v", byPort, err)
	}
	disabled, err := m.SetUDPListenerEnabled(ctx, created.ID, false)
	if err != nil || disabled.Enabled {
		t.Fatalf("disable = %+v, %v", disabled, err)
	}
	if _, err := m.UDPListenerByPublicPort(ctx, created.PublicPort); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled public lookup = %v, want ErrNotFound", err)
	}
	rows, err := m.ListUDPListenersForApp(ctx, app.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != created.ID {
		t.Fatalf("list = %+v, %v", rows, err)
	}
	if err := m.DeleteUDPListener(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.UDPListenerByID(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read after delete = %v, want ErrNotFound", err)
	}
}

func TestMemStoreUDPListenerRejectsCollisionsAndInvalidPorts(t *testing.T) {
	m, ctx, account, app := udpListenerFixture(t)
	first, err := m.CreateUDPListener(ctx, UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "one", GuestPort: 1001, PublicPort: 40124})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err = m.CreateUDPListener(ctx, UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "two", GuestPort: 1002, PublicPort: first.PublicPort})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("public collision = %v, want ErrConflict", err)
	}
	_, err = m.CreateUDPListener(ctx, UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "udp", GuestPort: 53, PublicPort: 40125, Protocol: "tcp"})
	if !errors.Is(err, ErrInvalidUDPListener) {
		t.Fatalf("protocol error = %v, want ErrInvalidUDPListener", err)
	}
}

func TestMemStoreUDPListenerSeparateNamespaceAndAppDeletion(t *testing.T) {
	m, ctx, account, app := udpListenerFixture(t)
	if _, err := m.CreateTCPListener(ctx, TCPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "echo", GuestPort: 5353, PublicPort: 40126, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	listener, err := m.CreateUDPListener(ctx, UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "echo", GuestPort: 5353, PublicPort: 40126})
	if err != nil {
		t.Fatalf("TCP port blocked independent UDP namespace: %v", err)
	}
	if listener.Enabled {
		t.Fatal("UDP endpoint enabled by default")
	}
	if _, err := m.SetUDPListenerEnabled(ctx, listener.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UDPListenerByPublicPort(ctx, listener.PublicPort); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateUDPListener(ctx, UDPListener{AccountID: "different-account", AppID: app.ID, ListenerName: "other", GuestPort: 5353, PublicPort: 40127}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account create: %v", err)
	}
	if err := m.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UDPListenerByPublicPort(ctx, listener.PublicPort); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted app remained public: %v", err)
	}
	enabled, err := m.ListEnabledUDPListeners(ctx)
	if err != nil || len(enabled) != 0 {
		t.Fatalf("deleted app remained in bind set: %+v %v", enabled, err)
	}
}
