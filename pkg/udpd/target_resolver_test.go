package udpd

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type testUDPAdmitter struct {
	calls    int
	capacity bool
	err      error
}

func (a *testUDPAdmitter) AdmitInstance(_ context.Context, app, deployment, scope, trigger string) (string, string, string, string, int32, bool, int, error) {
	a.calls++
	if app == "" || deployment != "" || scope != "" || trigger != "gateway" {
		return "", "", "", "", 0, false, 0, errors.New("incorrect admission request")
	}
	return "woken", "node", "deployment", "wake", 0, a.capacity, 8080, a.err
}
func TestUDPTargetResolverValidatesIntentBeforeWake(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "udp-target@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	manifest := state.AppManifest{Ports: []api.WorkloadPort{{Name: "dns", Port: 5353, Protocol: api.WorkloadPortUDP}}}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "udp-target", Status: state.AppActive, RAMMB: 256, Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := store.CreateUDPListener(ctx, state.UDPListener{AppID: app.ID, AccountID: acct.ID, ListenerName: "dns", GuestPort: 5353, PublicPort: 40100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	admit := &testUDPAdmitter{}
	resolver := &StoreTargetResolver{Store: store, Admitter: admit}
	route := Route{ListenerID: intent.ID, PublicPort: intent.PublicPort, AppID: app.ID, AccountID: acct.ID, ListenerName: "dns", GuestPort: 5353}
	maintenance := true
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{MaintenanceMode: &maintenance}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveTarget(ctx, route); err == nil || admit.calls != 0 {
		t.Fatalf("maintenance app admitted a wake: err=%v calls=%d", err, admit.calls)
	}
	maintenance = false
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{MaintenanceMode: &maintenance}); err != nil {
		t.Fatal(err)
	}
	target, err := resolver.ResolveTarget(ctx, route)
	if err != nil {
		t.Fatal(err)
	}
	if target.InstanceID != "woken" || target.Port != 5353 || target.WakeID != "wake" || admit.calls != 1 {
		t.Fatalf("target=%+v calls=%d", target, admit.calls)
	}
	admit.capacity = true
	if _, err := resolver.ResolveTarget(ctx, route); err == nil {
		t.Fatal("capacity accepted")
	}
	admit.capacity = false
	before := admit.calls
	wrong := route
	wrong.AccountID = "other"
	if _, err := resolver.ResolveTarget(ctx, wrong); err == nil {
		t.Fatal("wrong owner accepted")
	}
	wrong = route
	wrong.GuestPort = 53
	if _, err := resolver.ResolveTarget(ctx, wrong); err == nil {
		t.Fatal("changed port accepted")
	}
	if _, err := store.SetUDPListenerEnabled(ctx, intent.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveTarget(ctx, route); err == nil {
		t.Fatal("disabled listener accepted")
	}
	if _, err := store.SetUDPListenerEnabled(ctx, intent.ID, true); err != nil {
		t.Fatal(err)
	}
	empty := state.AppManifest{}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &empty}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveTarget(ctx, route); err == nil {
		t.Fatal("removed UDP declaration accepted")
	}
	if admit.calls != before {
		t.Fatal("invalid route woke app")
	}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, "deployment", string(state.StateRunning), 256, "node", "wake")
	if err != nil {
		t.Fatal(err)
	}
	target, err = resolver.ResolveTarget(ctx, route)
	if err != nil {
		t.Fatal(err)
	}
	if target.InstanceID != instance.ID || target.Port != 5353 || admit.calls != before {
		t.Fatalf("running target=%+v calls=%d", target, admit.calls)
	}
	maintenance = true
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{MaintenanceMode: &maintenance}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveTarget(ctx, route); err == nil {
		t.Fatal("maintenance app accepted a new session to its running instance")
	}
	if admit.calls != before {
		t.Fatal("maintenance app caused admission")
	}
	maintenance = false
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{MaintenanceMode: &maintenance}); err != nil {
		t.Fatal(err)
	}
	if target, err := resolver.ResolveTarget(ctx, route); err != nil || target.InstanceID != instance.ID {
		t.Fatalf("maintenance exit did not restore routing: target=%+v err=%v", target, err)
	}
	if err := store.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveTarget(ctx, route); err == nil {
		t.Fatal("deleted app accepted")
	}
	if admit.calls != before {
		t.Fatal("deleted app woke instance")
	}

}

func TestUDPTargetResolverRejectsRecreatedListenerSocket(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "cold", true: "running"}[running], func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "udp-recreated@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "udp-recreated", Status: state.AppActive, RAMMB: 256, Manifest: state.AppManifest{Ports: []api.WorkloadPort{{Name: "dns", Port: 5353, Protocol: api.WorkloadPortUDP}}}})
			if err != nil {
				t.Fatal(err)
			}
			old, err := store.CreateUDPListener(ctx, state.UDPListener{AppID: app.ID, AccountID: acct.ID, ListenerName: "dns", GuestPort: 5353, PublicPort: 40100, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			route := Route{AppID: app.ID, AccountID: acct.ID, ListenerName: "dns", GuestPort: 5353, ListenerID: old.ID, PublicPort: old.PublicPort}
			if err := store.DeleteUDPListener(ctx, old.ID); err != nil {
				t.Fatal(err)
			}
			replacement, err := store.CreateUDPListener(ctx, state.UDPListener{AppID: app.ID, AccountID: acct.ID, ListenerName: "dns", GuestPort: 5353, PublicPort: 40101, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if running {
				if _, err := store.CreateInstance(ctx, app.ID, "deployment", string(state.StateRunning), 256, "node", "wake"); err != nil {
					t.Fatal(err)
				}
			}
			admit := &testUDPAdmitter{}
			resolver := &StoreTargetResolver{Store: store, Admitter: admit}
			for _, stale := range []Route{route, func() Route { r := route; r.ListenerID = replacement.ID; return r }(), func() Route { r := route; r.PublicPort = replacement.PublicPort; return r }()} {
				if target, err := resolver.ResolveTarget(ctx, stale); err == nil || admit.calls != 0 {
					t.Fatalf("stale socket routed: target=%+v err=%v wakes=%d", target, err, admit.calls)
				}
			}
			route.ListenerID, route.PublicPort = replacement.ID, replacement.PublicPort
			target, err := resolver.ResolveTarget(ctx, route)
			if err != nil || target.InstanceID == "" || target.Port != 5353 {
				t.Fatalf("replacement route: target=%+v err=%v", target, err)
			}
			if running && admit.calls != 0 || !running && admit.calls != 1 {
				t.Fatalf("replacement wakes=%d running=%v", admit.calls, running)
			}
		})
	}
}

func TestUDPTargetResolverNeverSelectsMirrorInstances(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "udp-mirror-routing@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "udp-mirror-routing", Status: state.AppActive, RAMMB: 256, Manifest: state.AppManifest{Ports: []api.WorkloadPort{{Name: "dns", Port: 5353, Protocol: api.WorkloadPortUDP}}}})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := store.CreateUDPListener(ctx, state.UDPListener{AppID: app.ID, AccountID: acct.ID, ListenerName: "dns", GuestPort: 5353, PublicPort: 40100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mirror, err := store.CreateInstanceWithMode(ctx, app.ID, "shadow-deployment", string(state.StateRunning), 256, "node", "shadow-wake", string(state.InstanceModeMirror))
	if err != nil {
		t.Fatal(err)
	}
	admit := &testUDPAdmitter{}
	resolver := &StoreTargetResolver{Store: store, Admitter: admit}
	route := Route{AppID: app.ID, AccountID: acct.ID, ListenerName: "dns", GuestPort: 5353, ListenerID: intent.ID, PublicPort: intent.PublicPort}
	target, err := resolver.ResolveTarget(ctx, route)
	if err != nil || target.InstanceID == mirror.ID || target.InstanceID != "woken" || admit.calls != 1 {
		t.Fatalf("mirror-only route: target=%+v err=%v admissions=%d", target, err, admit.calls)
	}
	customer, err := store.CreateInstanceWithMode(ctx, app.ID, "customer-deployment", string(state.StateRunning), 256, "node", "customer-wake", string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		target, err := resolver.ResolveTarget(ctx, route)
		if err != nil || target.InstanceID != customer.ID || admit.calls != 1 {
			t.Fatalf("target=%+v err=%v admissions=%d", target, err, admit.calls)
		}
	}
}
