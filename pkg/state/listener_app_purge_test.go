package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type listenerRetirementStore interface {
	state.UDPListenerStore
	ListEnabledTCPListeners(context.Context) ([]state.TCPListener, error)
	ListEnabledUDPListeners(context.Context) ([]state.UDPListener, error)
	CreateTCPListener(context.Context, state.TCPListener) (state.TCPListener, error)
	TCPListenerByID(context.Context, string) (state.TCPListener, error)
	TCPListenerByPublicPort(context.Context, int) (state.TCPListener, error)
	CreateApp(context.Context, state.App) (state.App, error)
	ScheduleAppDeletion(context.Context, string, time.Time) (state.App, error)
	RestoreApp(context.Context, string, api.Limits) (state.App, error)
	ClaimAppDeletion(context.Context, string) error
	DeleteAppPermanently(context.Context, string) error
}

func TestMemStoreListenerAppRetirement(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "listener-retirement@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "listener-retirement", Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	checkListenerRetirement(t, store, ctx, account.ID, app.ID)
}

func checkListenerRetirement(t *testing.T, store listenerRetirementStore, ctx context.Context, accountID, appID string) {
	t.Helper()
	var tcpIDs, udpIDs []string
	for i := 0; i < 2; i++ {
		tcp, err := store.CreateTCPListener(ctx, state.TCPListener{AccountID: accountID, AppID: appID, ListenerName: []string{"enabled", "disabled"}[i], GuestPort: 5353 + i, PublicPort: 40190 + i, Enabled: i == 0, TLSMode: api.TCPListenerTLSTerminate, TLSHostname: "retirement.example"})
		if err != nil {
			t.Fatal(err)
		}
		tcpIDs = append(tcpIDs, tcp.ID)
		udp, err := store.CreateUDPListener(ctx, state.UDPListener{AccountID: accountID, AppID: appID, ListenerName: []string{"enabled", "disabled"}[i], GuestPort: 5353 + i, PublicPort: 40190 + i, Enabled: i == 0})
		if err != nil {
			t.Fatal(err)
		}
		udpIDs = append(udpIDs, udp.ID)
	}
	other, err := store.CreateApp(ctx, state.App{AccountID: accountID, Slug: "listener-retirement-other", Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	otherTCP, err := store.CreateTCPListener(ctx, state.TCPListener{AccountID: accountID, AppID: other.ID, ListenerName: "other", GuestPort: 5353, PublicPort: 40192, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	otherUDP, err := store.CreateUDPListener(ctx, state.UDPListener{AccountID: accountID, AppID: other.ID, ListenerName: "other", GuestPort: 5353, PublicPort: 40192, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ScheduleAppDeletion(ctx, appID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TCPListenerByPublicPort(ctx, 40190); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted TCP app remains routable: %v", err)
	}
	if _, err := store.UDPListenerByPublicPort(ctx, 40190); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted UDP app remains routable: %v", err)
	}
	if rows, err := store.ListEnabledTCPListeners(ctx); err != nil || len(rows) != 1 || rows[0].ID != otherTCP.ID {
		t.Fatalf("TCP feed exposes deleted app: %+v/%v", rows, err)
	}
	if rows, err := store.ListEnabledUDPListeners(ctx); err != nil || len(rows) != 1 || rows[0].ID != otherUDP.ID {
		t.Fatalf("UDP feed exposes deleted app: %+v/%v", rows, err)
	}
	if _, err := store.TCPListenerByPublicPort(ctx, (1<<32)+40190); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("oversized TCP port aliases a listener: %v", err)
	}
	if err := store.ClaimAppDeletion(ctx, appID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("premature purge claim=%v", err)
	}
	if err := store.DeleteAppPermanently(ctx, appID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("premature purge=%v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := store.TCPListenerByID(ctx, tcpIDs[i]); err != nil {
			t.Fatalf("TCP reservation lost during grace: %v", err)
		}
		if _, err := store.UDPListenerByID(ctx, udpIDs[i]); err != nil {
			t.Fatalf("UDP reservation lost during grace: %v", err)
		}
		if _, err := store.CreateTCPListener(ctx, state.TCPListener{AccountID: accountID, AppID: other.ID, ListenerName: "reuse", GuestPort: 6000, PublicPort: 40190 + i}); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("TCP grace reservation released: %v", err)
		}
		if _, err := store.CreateUDPListener(ctx, state.UDPListener{AccountID: accountID, AppID: other.ID, ListenerName: "reuse", GuestPort: 6000, PublicPort: 40190 + i}); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("UDP grace reservation released: %v", err)
		}
	}
	limits, _ := api.LimitsFor(api.PlanPro)
	if _, err := store.RestoreApp(ctx, appID, limits); err != nil {
		t.Fatal(err)
	}
	if got, err := store.TCPListenerByPublicPort(ctx, 40190); err != nil || got.ID != tcpIDs[0] || got.TLSMode != api.TCPListenerTLSTerminate || got.TLSHostname != "retirement.example" {
		t.Fatalf("TCP restoration=%+v/%v", got, err)
	}
	if got, err := store.UDPListenerByPublicPort(ctx, 40190); err != nil || got.ID != udpIDs[0] {
		t.Fatalf("UDP restoration=%+v/%v", got, err)
	}
	if _, err := store.ScheduleAppDeletion(ctx, appID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimAppDeletion(ctx, appID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAppPermanently(ctx, appID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := store.TCPListenerByID(ctx, tcpIDs[i]); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("TCP reservation survives purge: %v", err)
		}
		if _, err := store.UDPListenerByID(ctx, udpIDs[i]); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("UDP reservation survives purge: %v", err)
		}
		if _, err := store.CreateTCPListener(ctx, state.TCPListener{AccountID: accountID, AppID: other.ID, ListenerName: []string{"reuse-enabled", "reuse-disabled"}[i], GuestPort: 6000 + i, PublicPort: 40190 + i}); err != nil {
			t.Fatalf("TCP port not reclaimed: %v", err)
		}
		if _, err := store.CreateUDPListener(ctx, state.UDPListener{AccountID: accountID, AppID: other.ID, ListenerName: []string{"reuse-enabled", "reuse-disabled"}[i], GuestPort: 6000 + i, PublicPort: 40190 + i}); err != nil {
			t.Fatalf("UDP port not reclaimed: %v", err)
		}
	}
	if got, err := store.TCPListenerByPublicPort(ctx, 40192); err != nil || got.ID != otherTCP.ID {
		t.Fatalf("other TCP app disturbed: %+v/%v", got, err)
	}
	if got, err := store.UDPListenerByPublicPort(ctx, 40192); err != nil || got.ID != otherUDP.ID {
		t.Fatalf("other UDP app disturbed: %+v/%v", got, err)
	}
}
