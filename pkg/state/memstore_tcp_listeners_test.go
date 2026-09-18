package state

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func tcpListenerFixture(t *testing.T) (*MemStore, context.Context, Account, App) {
	t.Helper()
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "tcp-listener-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, Slug: "tcp-" + uuid.NewString(), Status: AppActive, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, account, app
}

func TestMemStoreTCPListenerLifecycleAndRouteLookup(t *testing.T) {
	m, ctx, account, app := tcpListenerFixture(t)
	created, err := m.CreateTCPListener(ctx, TCPListener{
		AccountID: account.ID, AppID: app.ID, ListenerName: "Postgres",
		GuestPort: 5432, PublicPort: 40123, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Protocol != "tcp" || created.ListenerName != "postgres" {
		t.Fatalf("normalized listener = %+v", created)
	}
	byName, err := m.TCPListenerByAppAndName(ctx, app.ID, "POSTGRES")
	if err != nil || byName.ID != created.ID {
		t.Fatalf("lookup by name = %+v, %v", byName, err)
	}
	byPort, err := m.TCPListenerByPublicPort(ctx, created.PublicPort)
	if err != nil || byPort.ID != created.ID {
		t.Fatalf("lookup by port = %+v, %v", byPort, err)
	}
	disabled, err := m.SetTCPListenerEnabled(ctx, created.ID, false)
	if err != nil || disabled.Enabled {
		t.Fatalf("disable = %+v, %v", disabled, err)
	}
	if _, err := m.TCPListenerByPublicPort(ctx, created.PublicPort); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled public lookup = %v, want ErrNotFound", err)
	}
	rows, err := m.ListTCPListenersForApp(ctx, app.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != created.ID {
		t.Fatalf("list = %+v, %v", rows, err)
	}
	if err := m.DeleteTCPListener(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.TCPListenerByID(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read after delete = %v, want ErrNotFound", err)
	}
}

func TestMemStoreTCPListenerRejectsCollisionsAndInvalidPorts(t *testing.T) {
	m, ctx, account, app := tcpListenerFixture(t)
	first, err := m.CreateTCPListener(ctx, TCPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "one", GuestPort: 1001, PublicPort: 40124})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err = m.CreateTCPListener(ctx, TCPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "two", GuestPort: 1002, PublicPort: first.PublicPort})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("public collision = %v, want ErrConflict", err)
	}
	_, err = m.CreateTCPListener(ctx, TCPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "udp", GuestPort: 53, PublicPort: 40125, Protocol: "udp"})
	if !errors.Is(err, ErrInvalidTCPListener) {
		t.Fatalf("protocol error = %v, want ErrInvalidTCPListener", err)
	}
}
