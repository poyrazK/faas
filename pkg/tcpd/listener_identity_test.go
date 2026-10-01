package tcpd

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type listenerIdentitySource struct {
	*customerTargetSource
	app    state.App
	intent state.TCPListener
}

func (s *listenerIdentitySource) AppByID(context.Context, string) (state.App, error) {
	return s.app, nil
}
func (s *listenerIdentitySource) TCPListenerByAppAndName(context.Context, string, string) (state.TCPListener, error) {
	return s.intent, nil
}

func TestTCPResolverRejectsObsoleteIntentBeforeAdmission(t *testing.T) {
	for _, change := range []struct {
		name   string
		mutate func(*listenerIdentitySource)
	}{
		{"replacement", func(s *listenerIdentitySource) { s.intent.ID = "new" }},
		{"disabled", func(s *listenerIdentitySource) { s.intent.Enabled = false }},
		{"guest-port", func(s *listenerIdentitySource) { s.intent.GuestPort++ }},
		{"public-port", func(s *listenerIdentitySource) { s.intent.PublicPort++ }},
		{"account", func(s *listenerIdentitySource) { s.intent.AccountID = "foreign" }},
		{"app", func(s *listenerIdentitySource) { s.intent.AppID = "foreign" }},
		{"protocol", func(s *listenerIdentitySource) { s.intent.Protocol = "udp" }},
		{"deleted-app", func(s *listenerIdentitySource) { s.app.Status = state.AppDeleted }},
		{"maintenance", func(s *listenerIdentitySource) { s.app.MaintenanceMode = true }},
	} {
		t.Run(change.name, func(t *testing.T) {
			for _, warm := range []bool{false, true} {
				source := &listenerIdentitySource{customerTargetSource: &customerTargetSource{}, app: state.App{ID: "app", AccountID: "account", Status: state.AppActive}, intent: state.TCPListener{ID: "listener", AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 9000, PublicPort: 40100, Protocol: "tcp", Enabled: true}}
				if warm {
					source.instances = []state.Instance{{ID: "running", AppID: "app", DeploymentID: "deployment", NodeID: "node", State: string(state.StateRunning)}}
				}
				change.mutate(source)
				admit := &customerTargetAdmitter{}
				resolver := &StoreTargetResolver{Instances: source, Admitter: admit}
				route := Route{ListenerID: "listener", AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 9000, PublicPort: 40100, Protocol: "tcp"}
				if _, err := resolver.ResolveTarget(context.Background(), route); err == nil || admit.calls != 0 {
					t.Fatalf("obsolete intent routed: warm=%v err=%v admissions=%d", warm, err, admit.calls)
				}
			}
		})
	}
}

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
