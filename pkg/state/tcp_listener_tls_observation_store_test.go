package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type tcpTLSObservationTestStore interface {
	state.TCPListenerStore
	state.TCPListenerTLSStore
	state.TCPListenerTLSObservationStore
}

func TestTCPListenerTLSObservationStores(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		store := state.NewMemStore()
		account, err := store.CreateAccount(t.Context(), "tls-observation@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "tls-observation", RAMMB: 256, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		tcpTLSObservationStoreContract(t, store, account.ID, app.ID)
	})
	t.Run("postgres", func(t *testing.T) {
		store, ctx := pgStore(t)
		account, app, _ := seedLiveDeploy(t, store, ctx, "-tls-observation")
		tcpTLSObservationStoreContract(t, store, account, app)
	})
}

func tcpTLSObservationStoreContract(t *testing.T, store tcpTLSObservationTestStore, accountID, appID string) {
	t.Helper()
	ctx := context.Background()
	listener, err := store.CreateTCPListener(ctx, state.TCPListener{AccountID: accountID, AppID: appID, ListenerName: "echo", GuestPort: 9000, PublicPort: 40148, Enabled: true, TLSMode: api.TCPListenerTLSTerminate, TLSHostname: "echo.example"})
	if err != nil {
		t.Fatal(err)
	}
	observation := state.TCPListenerTLSObservation{ListenerID: listener.ID, EdgeID: "edge-one", Hostname: listener.TLSHostname, IntentUpdatedAt: listener.UpdatedAt, ObservedAt: time.Now().UTC().Truncate(time.Microsecond).Add(time.Second), Ready: true, NotAfter: time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)}
	if err := store.PutTCPListenerTLSObservation(ctx, observation); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*state.TCPListenerTLSObservation){
		func(*state.TCPListenerTLSObservation) {},
		func(o *state.TCPListenerTLSObservation) { o.ObservedAt = o.ObservedAt.Add(-time.Millisecond) },
		func(o *state.TCPListenerTLSObservation) { o.Hostname = "other.example" },
		func(o *state.TCPListenerTLSObservation) { o.IntentUpdatedAt = o.IntentUpdatedAt.Add(-time.Second) },
	} {
		invalid := observation
		change(&invalid)
		if err := store.PutTCPListenerTLSObservation(ctx, invalid); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("accepted obsolete observation: err=%v", err)
		}
	}
	other := observation
	other.EdgeID = "edge-two"
	if err := store.PutTCPListenerTLSObservation(ctx, other); err != nil {
		t.Fatal(err)
	}
	latest := observation
	latest.ObservedAt = latest.ObservedAt.Add(time.Second)
	latest.Ready, latest.NotAfter = false, time.Time{}
	if err := store.PutTCPListenerTLSObservation(ctx, latest); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListTCPListenerTLSObservations(ctx, listener.ID)
	if err != nil || len(rows) != 2 || rows[0].EdgeID != "edge-one" || rows[0].Ready || !rows[0].NotAfter.IsZero() || !rows[0].ObservedAt.Equal(latest.ObservedAt) || rows[1].EdgeID != "edge-two" || !rows[1].Ready {
		t.Fatalf("latest per-edge rows=%+v err=%v", rows, err)
	}
	if count, err := store.PruneTCPListenerTLSObservations(ctx, observation.ObservedAt); err != nil || count != 1 {
		t.Fatalf("pruned=%d err=%v", count, err)
	}
	if _, err := store.SetTCPListenerTLS(ctx, listener.ID, api.TCPListenerTLSConfig{Mode: api.TCPListenerTLSTerminate, Hostname: "other.example"}); err != nil {
		t.Fatal(err)
	}
	latest.ObservedAt = latest.ObservedAt.Add(time.Second)
	if err := store.PutTCPListenerTLSObservation(ctx, latest); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("published after intent change: %v", err)
	}
	if err := store.DeleteTCPListener(ctx, listener.ID); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListTCPListenerTLSObservations(ctx, listener.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("listener deletion retained evidence: rows=%+v err=%v", rows, err)
	}
}
