package tcpd

import (
	"context"
	"errors"
	"testing"
)

func TestRouteTableUpsertMoveAndDelete(t *testing.T) {
	table := NewRouteTable()
	first := Route{
		PublicPort:   41001,
		AppID:        "app-1",
		ListenerName: "postgres",
		GuestPort:    5432,
		Protocol:     "tcp",
	}
	if err := table.Upsert(first); err != nil {
		t.Fatalf("Upsert(first): %v", err)
	}
	got, ok, err := table.Resolve(context.Background(), first.PublicPort)
	if err != nil || !ok || got != first {
		t.Fatalf("Resolve(first) = %#v, %v, %v", got, ok, err)
	}

	moved := first
	moved.PublicPort = 41002
	if err := table.Upsert(moved); err != nil {
		t.Fatalf("Upsert(moved): %v", err)
	}
	if _, ok, err := table.Resolve(context.Background(), first.PublicPort); err != nil || ok {
		t.Fatalf("old route still resolves: ok=%v err=%v", ok, err)
	}
	if got, ok, err := table.Resolve(context.Background(), moved.PublicPort); err != nil || !ok || got != moved {
		t.Fatalf("Resolve(moved) = %#v, %v, %v", got, ok, err)
	}
	if !table.Delete(moved.PublicPort) {
		t.Fatal("Delete(moved) = false")
	}
	if table.Delete(moved.PublicPort) {
		t.Fatal("second Delete(moved) = true")
	}
}

func TestRouteTableRejectsPortCollision(t *testing.T) {
	table := NewRouteTable()
	first := Route{PublicPort: 41001, AppID: "app-1", ListenerName: "one", GuestPort: 1001, Protocol: "tcp"}
	second := Route{PublicPort: 41001, AppID: "app-2", ListenerName: "two", GuestPort: 1002, Protocol: "tcp"}
	if err := table.Upsert(first); err != nil {
		t.Fatalf("Upsert(first): %v", err)
	}
	if err := table.Upsert(second); !errors.Is(err, ErrInvalidRoute) {
		t.Fatalf("Upsert(second) error = %v, want ErrInvalidRoute", err)
	}
}

func TestValidateRouteRejectsUDP(t *testing.T) {
	err := ValidateRoute(Route{PublicPort: 41001, AppID: "app", ListenerName: "dns", GuestPort: 53, Protocol: "udp"})
	if !errors.Is(err, ErrInvalidRoute) {
		t.Fatalf("ValidateRoute error = %v, want ErrInvalidRoute", err)
	}
}
