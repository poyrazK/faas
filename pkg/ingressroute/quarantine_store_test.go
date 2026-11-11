// adr: 20261105120000
package ingressroute_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type rawQuarantineStore interface {
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreateTCPListener(context.Context, state.TCPListener) (state.TCPListener, error)
	CreateUDPListener(context.Context, state.UDPListener) (state.UDPListener, error)
	TCPListenerByPublicPort(context.Context, int) (state.TCPListener, error)
	UDPListenerByPublicPort(context.Context, int) (state.UDPListener, error)
	ListEnabledTCPListeners(context.Context) ([]state.TCPListener, error)
	ListEnabledUDPListeners(context.Context) ([]state.UDPListener, error)
	TCPListenerByAppAndName(context.Context, string, string) (state.TCPListener, error)
	UDPListenerByAppAndName(context.Context, string, string) (state.UDPListener, error)
	UpdateApp(context.Context, string, state.UpdateAppParams) (state.App, error)
	CreateDeployment(context.Context, state.Deployment) (state.Deployment, error)
	MarkDeploymentLive(context.Context, string) error
	SetDeploymentParked(context.Context, string, string, time.Time) error
}

func rawQuarantineCases(t *testing.T, s rawQuarantineStore) {
	t.Helper()
	ctx := t.Context()
	acct, err := s.CreateAccount(ctx, "quarantine-listeners@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "quarantine-listeners", Status: state.AppActive, RAMMB: 256, Manifest: state.AppManifest{Ports: []api.WorkloadPort{{Name: "tcp", Port: 9000}, {Name: "udp", Port: 5353, Protocol: api.WorkloadPortUDP}}}})
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := s.CreateTCPListener(ctx, state.TCPListener{AppID: app.ID, AccountID: acct.ID, ListenerName: "tcp", GuestPort: 9000, PublicPort: 40400, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	udp, err := s.CreateUDPListener(ctx, state.UDPListener{AppID: app.ID, AccountID: acct.ID, ListenerName: "udp", GuestPort: 5353, PublicPort: 40400, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:listener-quarantine", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	check := func(allowed bool) {
		t.Helper()
		_, te := s.TCPListenerByPublicPort(ctx, tcp.PublicPort)
		_, ue := s.UDPListenerByPublicPort(ctx, udp.PublicPort)
		if allowed && (te != nil || ue != nil) || !allowed && (!errors.Is(te, state.ErrNotFound) || !errors.Is(ue, state.ErrNotFound)) {
			t.Fatalf("allowed=%v TCP=%v UDP=%v", allowed, te, ue)
		}
		ts, err := s.ListEnabledTCPListeners(ctx)
		if err != nil {
			t.Fatal(err)
		}
		us, err := s.ListEnabledUDPListeners(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if (len(ts) == 1) != allowed || (len(us) == 1) != allowed {
			t.Fatalf("allowed=%v inventory TCP=%d UDP=%d", allowed, len(ts), len(us))
		}
	}
	check(true)
	stage, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stage", Scope: "staging", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, stage.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentParked(ctx, stage.ID, string(state.ParkReasonSecurityScanRegressed), time.Now()); err != nil {
		t.Fatal(err)
	}
	check(true) // A quarantined stage has no effect on public production listeners.
	if err := s.SetDeploymentParked(ctx, dep.ID, string(state.ParkReasonSecurityScanRegressed), time.Now()); err != nil {
		t.Fatal(err)
	}
	// The durable marker is authoritative even before the app/VM parks.
	check(false)
	parked := state.AppEvictedCold
	if _, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}
	check(false)
	if row, err := s.TCPListenerByAppAndName(ctx, app.ID, "tcp"); err != nil || !row.Enabled {
		t.Fatalf("lost TCP intent: %+v %v", row, err)
	}
	if row, err := s.UDPListenerByAppAndName(ctx, app.ID, "udp"); err != nil || !row.Enabled {
		t.Fatalf("lost UDP intent: %+v %v", row, err)
	}
	clean, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:listener-quarantine", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, clean.ID); err != nil {
		t.Fatal(err)
	}
	check(true)
	production, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:production", Scope: "production", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, production.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentParked(ctx, clean.ID, string(state.ParkReasonSecurityScanRegressed), time.Now()); err != nil {
		t.Fatal(err)
	}
	check(true) // Named production displaces the still-positive legacy lane.
	if err := s.SetDeploymentParked(ctx, production.ID, string(state.ParkReasonSecurityScanRegressed), time.Now()); err != nil {
		t.Fatal(err)
	}
	check(false)
}
func TestMemRawListenerQuarantine(t *testing.T) { rawQuarantineCases(t, state.NewMemStore()) }
func TestPgRawListenerQuarantine(t *testing.T) {
	s := state.NewPgStore(pgtest.OpenMigrated(t))
	rawQuarantineCases(t, s)
}
