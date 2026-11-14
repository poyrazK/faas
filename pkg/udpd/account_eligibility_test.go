// adr: 20261104170000
package udpd_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tcpd"
	"github.com/onebox-faas/faas/pkg/udpd"
)

type rawOwnerFixture struct {
	store   *state.MemStore
	account state.Account
	app     state.App
	tcp     tcpd.Route
	udp     udpd.Route
}

func newRawOwnerFixture(t *testing.T, running bool, probed ...bool) rawOwnerFixture {
	t.Helper()
	s := state.NewMemStore()
	account, err := s.CreateAccount(t.Context(), "raw-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "raw-owner", Status: state.AppActive, RAMMB: 256,
		Manifest: state.AppManifest{Ports: []api.WorkloadPort{{Name: "tcp", Port: 9000}, {Name: "udp", Port: 5353, Protocol: api.WorkloadPortUDP}}}})
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := s.CreateTCPListener(t.Context(), state.TCPListener{AppID: app.ID, AccountID: account.ID, ListenerName: "tcp", GuestPort: 9000, PublicPort: 40101, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	udp, err := s.CreateUDPListener(t.Context(), state.UDPListener{AppID: app.ID, AccountID: account.ID, ListenerName: "udp", GuestPort: 5353, PublicPort: 40100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var probe json.RawMessage
	if len(probed) > 0 && probed[0] {
		probe = json.RawMessage(`{"path":"/readyz"}`)
	}
	if _, err := s.CreateDeployment(t.Context(), state.Deployment{ID: "deployment", AppID: app.ID, Status: state.DeployLive, TrafficPercent: 100, OverrideReadinessProbe: probe}); err != nil {
		t.Fatal(err)
	}
	if running {
		if _, err := s.CreateInstance(t.Context(), app.ID, "deployment", string(state.StateRunning), 256, "node", "wake"); err != nil {
			t.Fatal(err)
		}
	}
	return rawOwnerFixture{s, account, app,
		tcpd.Route{AppID: app.ID, AccountID: account.ID, ListenerID: tcp.ID, ListenerName: tcp.ListenerName, PublicPort: tcp.PublicPort, GuestPort: tcp.GuestPort, Protocol: "tcp"},
		udpd.Route{AppID: app.ID, AccountID: account.ID, ListenerID: udp.ID, ListenerName: udp.ListenerName, PublicPort: udp.PublicPort, GuestPort: udp.GuestPort}}
}

type rawOwnerAdmitter struct {
	calls   int
	onAdmit func()
}

func (a *rawOwnerAdmitter) AdmitInstance(_ context.Context, _, deployment, _, _ string) (string, string, string, string, int32, bool, int, error) {
	a.calls++
	if a.onAdmit != nil {
		a.onAdmit()
	}
	return "woken", "node", deployment, "wake", 0, false, 9000, nil
}

func TestRawIngressEnforcesCurrentAccountEligibility(t *testing.T) {
	for _, running := range []bool{false, true} {
		for _, mode := range []string{"active", "past-due", "suspended", "deleted-pending", "abuse-held"} {
			name := mode + "/cold"
			if running {
				name = mode + "/running"
			}
			t.Run(name, func(t *testing.T) {
				f := newRawOwnerFixture(t, running)
				admit := &rawOwnerAdmitter{}
				tcp := &tcpd.StoreTargetResolver{Instances: tcpd.NewCachedInstanceSource(f.store, time.Hour), Admitter: admit}
				udp := &udpd.StoreTargetResolver{Store: f.store, Admitter: admit}
				// Warm the public route cache before a policy change.
				if running {
					if _, err := tcp.ResolveTarget(t.Context(), f.tcp); err != nil {
						t.Fatal(err)
					}
					if _, err := udp.ResolveTarget(t.Context(), f.udp); err != nil {
						t.Fatal(err)
					}
				}
				status := state.AccountActive
				switch mode {
				case "past-due":
					status = state.AccountPastDue
				case "suspended":
					status = state.AccountSuspended
				case "deleted-pending":
					status = state.AccountDeletedPending
				case "abuse-held":
					if _, err := f.store.SetAccountAbuseHold(t.Context(), f.account.ID, state.AccountAbuseHoldOperator, time.Now()); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.store.UpdateAccountStatus(t.Context(), f.account.ID, status); err != nil {
					t.Fatal(err)
				}
				_, tcpErr := tcp.ResolveTarget(t.Context(), f.tcp)
				_, udpErr := udp.ResolveTarget(t.Context(), f.udp)
				allowed := mode == "active" || mode == "past-due"
				if (tcpErr == nil) != allowed || (udpErr == nil) != allowed {
					t.Fatalf("allowed=%v TCP=%v UDP=%v", allowed, tcpErr, udpErr)
				}
				wantCalls := 0
				if allowed && !running {
					wantCalls = 2
				}
				if admit.calls != wantCalls {
					t.Fatalf("scheduler calls=%d want=%d", admit.calls, wantCalls)
				}
				if !allowed {
					if err := f.store.UpdateAccountStatus(t.Context(), f.account.ID, state.AccountActive); err != nil {
						t.Fatal(err)
					}
					if _, err := f.store.ReleaseAccountAbuseHold(t.Context(), f.account.ID); err != nil {
						t.Fatal(err)
					}
					if _, err := tcp.ResolveTarget(t.Context(), f.tcp); err != nil {
						t.Fatal("TCP did not recover", err)
					}
					if _, err := udp.ResolveTarget(t.Context(), f.udp); err != nil {
						t.Fatal("UDP did not recover", err)
					}
				}
			})
		}
	}
}

func TestRawIngressRechecksAccountAfterWake(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		t.Run(protocol, func(t *testing.T) {
			f := newRawOwnerFixture(t, false)
			a := &rawOwnerAdmitter{onAdmit: func() {
				if _, err := f.store.SetAccountAbuseHold(t.Context(), f.account.ID, state.AccountAbuseHoldOperator, time.Now()); err != nil {
					t.Error(err)
				}
			}}
			var err error
			if protocol == "tcp" {
				_, err = (&tcpd.StoreTargetResolver{Instances: f.store, Admitter: a}).ResolveTarget(t.Context(), f.tcp)
			} else {
				_, err = (&udpd.StoreTargetResolver{Store: f.store, Admitter: a}).ResolveTarget(t.Context(), f.udp)
			}
			if err == nil || a.calls != 1 {
				t.Fatalf("late hold admitted %s: err=%v calls=%d", protocol, err, a.calls)
			}
		})
	}
}

// Embed only the narrow routing projection, with an interception at the slow
// instance read. The durable store remains the source of account policy.
type rawSelectionSource struct {
	tcpd.InstanceSource
	udp    udpd.TargetSource
	onList func()
}

func (s rawSelectionSource) ListInstancesForApp(ctx context.Context, app string) ([]state.Instance, error) {
	rows, err := s.InstanceSource.ListInstancesForApp(ctx, app)
	s.onList()
	return rows, err
}

func (s rawSelectionSource) UDPListenerByAppAndName(ctx context.Context, app, name string) (state.UDPListener, error) {
	return s.udp.UDPListenerByAppAndName(ctx, app, name)
}

func TestRawIngressRechecksAccountAfterRunningTargetSelection(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		t.Run(protocol, func(t *testing.T) {
			f := newRawOwnerFixture(t, true)
			source := rawSelectionSource{InstanceSource: f.store, udp: f.store, onList: func() {
				if _, err := f.store.SetAccountAbuseHold(t.Context(), f.account.ID, state.AccountAbuseHoldOperator, time.Now()); err != nil {
					t.Error(err)
				}
			}}
			var err error
			if protocol == "tcp" {
				_, err = (&tcpd.StoreTargetResolver{Instances: source}).ResolveTarget(t.Context(), f.tcp)
			} else {
				_, err = (&udpd.StoreTargetResolver{Store: source}).ResolveTarget(t.Context(), f.udp)
			}
			if err == nil {
				t.Fatal("account held during instance selection was admitted")
			}
		})
	}
}
