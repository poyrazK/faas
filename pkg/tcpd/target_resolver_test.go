package tcpd

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type targetSourceFixture struct {
	app       state.App
	instances []state.Instance
	intent    state.TCPListener
	domain    state.CustomDomain
}

func (s *targetSourceFixture) AppByID(context.Context, string) (state.App, error) { return s.app, nil }
func (s *targetSourceFixture) DomainByName(context.Context, string) (state.CustomDomain, error) {
	return s.domain, nil
}
func (s *targetSourceFixture) TCPListenerByAppAndName(context.Context, string, string) (state.TCPListener, error) {
	return s.intent, nil
}

func TestTCPResolverTLSRequiresCurrentDomainOwnership(t *testing.T) {
	source := &targetSourceFixture{app: state.App{ID: "app", AccountID: "account", Status: state.AppActive}, intent: state.TCPListener{ID: "listener", AppID: "app", AccountID: "account", PublicPort: 40100, GuestPort: 9000, Protocol: "tcp", Enabled: true, TLSMode: api.TCPListenerTLSTerminate, TLSHostname: "echo.example"}}
	admit := &targetAdmitterFixture{}
	resolver := &StoreTargetResolver{Instances: source, Admitter: admit}
	route := Route{ListenerID: "listener", AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 9000, PublicPort: 40100, Protocol: "tcp", TLSHostname: "echo.example"}
	for _, domain := range []state.CustomDomain{
		{},
		{Domain: "echo.example", AppID: "app"},
		{Domain: "echo.example", AppID: "other", VerifiedAt: time.Now()},
		{Domain: "other.example", AppID: "app", VerifiedAt: time.Now()},
		{Domain: "echo.example", AppID: "app", EnvironmentID: "staging", VerifiedAt: time.Now()},
	} {
		source.domain = domain
		if _, err := resolver.ResolveTarget(context.Background(), route); err == nil || admit.calls != 0 {
			t.Fatalf("invalid domain admitted: domain=%+v err=%v calls=%d", domain, err, admit.calls)
		}
	}
	source.domain = state.CustomDomain{Domain: "echo.example", AppID: "app", VerifiedAt: time.Now()}
	if target, err := resolver.ResolveTarget(context.Background(), route); err != nil || target.InstanceID != "new" || admit.calls != 1 {
		t.Fatalf("verified domain target=%+v err=%v calls=%d", target, err, admit.calls)
	}
}
func (s *targetSourceFixture) ListInstancesForApp(context.Context, string) ([]state.Instance, error) {
	return s.instances, nil
}

type targetAdmitterFixture struct{ calls int }

func (a *targetAdmitterFixture) AdmitInstance(context.Context, string, string, string, string) (string, string, string, string, int32, bool, int, error) {
	a.calls++
	return "new", "node", "deployment", "wake", 0, false, 8080, nil
}

func TestTCPResolverMaintenanceAndOwnership(t *testing.T) {
	ctx := context.Background()
	source := &targetSourceFixture{app: state.App{ID: "app", AccountID: "account", Status: state.AppActive}}
	admit := &targetAdmitterFixture{}
	resolver := &StoreTargetResolver{Instances: source, Admitter: admit}
	route := Route{ListenerID: "listener", AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 9000, PublicPort: 40100, Protocol: "tcp"}
	source.intent = state.TCPListener{ID: "listener", AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 9000, PublicPort: 40100, Protocol: "tcp", Enabled: true}
	for _, running := range []bool{false, true} {
		if running {
			source.instances = []state.Instance{{ID: "live", AppID: "app", NodeID: "node", State: string(state.StateRunning)}}
		}
		source.app.MaintenanceMode = true
		if _, err := resolver.ResolveTarget(ctx, route); err == nil || admit.calls != 0 {
			t.Fatalf("maintenance routed or woke app: running=%v err=%v calls=%d", running, err, admit.calls)
		}
	}
	source.app.MaintenanceMode = false
	target, err := resolver.ResolveTarget(ctx, route)
	if err != nil || target.InstanceID != "live" || target.Port != 9000 {
		t.Fatalf("maintenance exit: target=%+v err=%v", target, err)
	}
	source.app.AccountID = "other"
	if _, err := resolver.ResolveTarget(ctx, route); err == nil {
		t.Fatal("changed account owner accepted")
	}
	source.app.AccountID = "account"
	source.app.Status = state.AppDeleted
	if _, err := resolver.ResolveTarget(ctx, route); err == nil {
		t.Fatal("deleted app accepted")
	}
	source.app.Status = state.AppActive
	source.instances[0].AppID = "other"
	target, err = resolver.ResolveTarget(ctx, route)
	if err != nil || target.InstanceID != "new" || admit.calls != 1 {
		t.Fatalf("foreign instance selected: target=%+v err=%v calls=%d", target, err, admit.calls)
	}
}

func TestTCPResolverRejectsStaleListenerBeforeWake(t *testing.T) {
	for _, change := range []struct {
		name   string
		mutate func(*state.TCPListener)
	}{
		{"listener-id", func(l *state.TCPListener) { l.ID = "replacement" }},
		{"disabled", func(l *state.TCPListener) { l.Enabled = false }},
		{"guest-port", func(l *state.TCPListener) { l.GuestPort++ }},
		{"public-port", func(l *state.TCPListener) { l.PublicPort++ }},
		{"account", func(l *state.TCPListener) { l.AccountID = "other" }},
		{"app", func(l *state.TCPListener) { l.AppID = "other" }},
		{"protocol", func(l *state.TCPListener) { l.Protocol = "udp" }},
		{"tls-mode", func(l *state.TCPListener) { l.TLSMode = api.TCPListenerTLSTerminate; l.TLSHostname = "echo.example" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			source := &targetSourceFixture{app: state.App{ID: "app", AccountID: "account", Status: state.AppActive}, intent: state.TCPListener{ID: "listener", AppID: "app", AccountID: "account", PublicPort: 40100, GuestPort: 9000, Protocol: "tcp", Enabled: true}}
			change.mutate(&source.intent)
			admit := &targetAdmitterFixture{}
			resolver := &StoreTargetResolver{Instances: source, Admitter: admit}
			_, err := resolver.ResolveTarget(context.Background(), Route{ListenerID: "listener", AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 9000, PublicPort: 40100, Protocol: "tcp"})
			if err == nil || admit.calls != 0 {
				t.Fatalf("stale listener admitted: err=%v calls=%d", err, admit.calls)
			}
		})
	}
}
