package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type internalPortListenerStore interface {
	CreateTCPListener(context.Context, state.TCPListener) (state.TCPListener, error)
	CreateUDPListener(context.Context, state.UDPListener) (state.UDPListener, error)
	TCPListenerByPublicPort(context.Context, int) (state.TCPListener, error)
	UDPListenerByPublicPort(context.Context, int) (state.UDPListener, error)
	ListEnabledTCPListeners(context.Context) ([]state.TCPListener, error)
	ListEnabledUDPListeners(context.Context) ([]state.UDPListener, error)
	UpdateApp(context.Context, string, state.UpdateAppParams) (state.App, error)
}

func TestMemStoreInternalPortListenerPolicy(t *testing.T) {
	ctx := t.Context()
	s := state.NewMemStore()
	account, err := s.CreateAccount(ctx, "internal-port@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "internal-port", Status: state.AppActive, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	checkInternalPortListenerPolicy(t, s, account.ID, app.ID)
}

func TestPgStoreInternalPortListenerPolicy(t *testing.T) {
	s, ctx := pgStore(t)
	account, app, _ := seedLiveDeploy(t, s, ctx, "-internal-port")
	checkInternalPortListenerPolicy(t, s, account, app)
}

func checkInternalPortListenerPolicy(t *testing.T, s internalPortListenerStore, account, app string) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.CreateTCPListener(ctx, state.TCPListener{AccountID: account, AppID: app, ListenerName: "tcp-old-name", GuestPort: 5432, PublicPort: 40130, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUDPListener(ctx, state.UDPListener{AccountID: account, AppID: app, ListenerName: "udp-old-name", GuestPort: 5432, PublicPort: 40130, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                   string
		tcpPrivate, udpPrivate bool
	}{
		{"tcp-private", true, false},
		{"udp-private", false, true},
		{"both-private", true, true},
		{"public-again", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := state.AppManifest{Ports: []api.WorkloadPort{
				{Name: "renamed-tcp", Port: 5432, Protocol: api.WorkloadPortTCP, Internal: tc.tcpPrivate},
				{Name: "renamed-udp", Port: 5432, Protocol: api.WorkloadPortUDP, Internal: tc.udpPrivate},
			}}
			if _, err := s.UpdateApp(ctx, app, state.UpdateAppParams{Manifest: &manifest}); err != nil {
				t.Fatal(err)
			}
			tcp, err := s.ListEnabledTCPListeners(ctx)
			if err != nil || (len(tcp) == 0) != tc.tcpPrivate {
				t.Fatalf("TCP inventory: %v, %v", tcp, err)
			}
			udp, err := s.ListEnabledUDPListeners(ctx)
			if err != nil || (len(udp) == 0) != tc.udpPrivate {
				t.Fatalf("UDP inventory: %v, %v", udp, err)
			}
			if _, err := s.TCPListenerByPublicPort(ctx, 40130); (errors.Is(err, state.ErrNotFound) != tc.tcpPrivate) || (err != nil && !errors.Is(err, state.ErrNotFound)) {
				t.Fatalf("TCP public route: %v", err)
			}
			if _, err := s.UDPListenerByPublicPort(ctx, 40130); (errors.Is(err, state.ErrNotFound) != tc.udpPrivate) || (err != nil && !errors.Is(err, state.ErrNotFound)) {
				t.Fatalf("UDP public route: %v", err)
			}
		})
	}
}
