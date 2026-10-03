package tcpd

import (
	"context"
	"github.com/onebox-faas/faas/pkg/gateway"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestTCPBoundSocketRejectsReplacementBeforeTargetSelection(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	old := Route{ListenerID: "old", AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 9000, PublicPort: 40100, Protocol: "tcp"}
	replacement := old
	replacement.ListenerID = "replacement"
	routes := NewRouteTable()
	if err := routes.Upsert(replacement); err != nil {
		t.Fatal(err)
	}
	var targets atomic.Int32
	rejected := make(chan error, 1)
	server := &Server{Listener: &aliasedTCPListener{Listener: base, port: old.PublicPort}, BoundRoute: &old, Routes: routes, Targets: targetResolverFunc(func(context.Context, Route) (gateway.Target, error) {
		targets.Add(1)
		return gateway.Target{}, nil
	}), Forwarder: forwarderFunc(func(context.Context, net.Conn, gateway.Target) error { return nil }), OnError: func(err error) { rejected <- err }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("obsolete socket server did not stop")
		}
	}()
	conn, err := net.DialTimeout("tcp", base.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-rejected:
	case <-time.After(time.Second):
		t.Fatal("obsolete socket accepted replacement intent")
	}
	if targets.Load() != 0 {
		t.Fatal("obsolete socket reached target selection")
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("obsolete socket left connection open")
	}
}
