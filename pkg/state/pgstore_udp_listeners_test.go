package state_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreUDPListenerLifecycleAndRouteLookup(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-udp-listener")
	created, err := s.CreateUDPListener(ctx, state.UDPListener{
		AccountID: accountID, AppID: appID, ListenerName: "postgres",
		GuestPort: 5432, PublicPort: 40126, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.UDPListenerByPublicPort(ctx, created.PublicPort)
	if err != nil || got.ID != created.ID {
		t.Fatalf("lookup by public port = %+v, %v", got, err)
	}
	if _, err := s.CreateUDPListener(ctx, state.UDPListener{
		AccountID: accountID, AppID: appID, ListenerName: "other",
		GuestPort: 5433, PublicPort: created.PublicPort,
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("public collision = %v, want ErrConflict", err)
	}
	for _, port := range []int{-1, 0, 65536, int(int64(created.PublicPort) + (1 << 32))} {
		if _, err := s.UDPListenerByPublicPort(ctx, port); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("out-of-range port %d resolved or aliased a listener: %v", port, err)
		}
	}
	disabled, err := s.SetUDPListenerEnabled(ctx, created.ID, false)
	if err != nil || disabled.Enabled {
		t.Fatalf("disable = %+v, %v", disabled, err)
	}
	if _, err := s.UDPListenerByPublicPort(ctx, created.PublicPort); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("disabled lookup = %v, want ErrNotFound", err)
	}
	if err := s.DeleteUDPListener(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.UDPListenerByID(ctx, created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("read after delete = %v, want ErrNotFound", err)
	}
}

func TestPgStoreUDPListenerOwnershipNamespaceAndDeletedApp(t *testing.T) {
	s, ctx := pgStore(t)
	account, app, _ := seedLiveDeploy(t, s, ctx, "-udp-ownership")
	other, otherApp, _ := seedLiveDeploy(t, s, ctx, "-udp-other", "udp-other")
	base := state.UDPListener{AccountID: account, AppID: app, ListenerName: "DNS", GuestPort: 5353, PublicPort: 40127}
	wrong := base
	wrong.AccountID = other
	if _, err := s.CreateUDPListener(ctx, wrong); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("wrong owner: %v", err)
	}
	created, err := s.CreateUDPListener(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if created.Enabled || created.ListenerName != "dns" || created.Protocol != "udp" {
		t.Fatalf("default listener=%+v", created)
	}
	duplicate := base
	duplicate.PublicPort++
	if _, err := s.CreateUDPListener(ctx, duplicate); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("name collision: %v", err)
	}
	if _, err := s.CreateTCPListener(ctx, state.TCPListener{AppID: app, AccountID: account, ListenerName: "dns", GuestPort: 5353, PublicPort: base.PublicPort}); err != nil {
		t.Fatalf("independent TCP namespace: %v", err)
	}
	if _, err := s.SetUDPListenerEnabled(ctx, created.ID, true); err != nil {
		t.Fatal(err)
	}
	enabled, err := s.ListEnabledUDPListeners(ctx)
	if err != nil || len(enabled) != 1 || enabled[0].ID != created.ID {
		t.Fatalf("enabled set=%+v err=%v", enabled, err)
	}
	listed, err := s.ListUDPListenersForApp(ctx, app)
	if err != nil || len(listed) != 1 {
		t.Fatalf("app list=%+v err=%v", listed, err)
	}
	// Concurrent requests for independent apps contend on the database's
	// global UDP port reservation, rather than the same app row lock.
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, candidate := range []state.UDPListener{
		{AccountID: account, AppID: app, ListenerName: "race", GuestPort: 5353, PublicPort: 40128},
		{AccountID: other, AppID: otherApp, ListenerName: "race", GuestPort: 5353, PublicPort: 40128},
	} {
		go func(candidate state.UDPListener) {
			<-start
			_, err := s.CreateUDPListener(ctx, candidate)
			results <- err
		}(candidate)
	}
	close(start)
	var successes, conflicts int
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, state.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent reservation: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("reservation successes=%d conflicts=%d", successes, conflicts)
	}
	if err := s.DeleteApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UDPListenerByPublicPort(ctx, created.PublicPort); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted-app route: %v", err)
	}
	enabled, err = s.ListEnabledUDPListeners(ctx)
	if err != nil || len(enabled) != 0 {
		t.Fatalf("deleted-app bind set=%+v err=%v", enabled, err)
	}
	base.ListenerName = "another"
	base.PublicPort++
	if _, err := s.CreateUDPListener(ctx, base); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted-app create: %v", err)
	}
}

func TestPgStoreUDPListenerSQLConstraintsPreserveEnabledIntent(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app, _ := seedLiveDeploy(t, s, ctx, "-udp-constraints")
	listener, err := s.CreateUDPListener(ctx, state.UDPListener{AccountID: account, AppID: app, ListenerName: "echo", GuestPort: 5353, PublicPort: 40129, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, query string
		value       any
	}{
		{"guest-port", "UPDATE app_udp_listeners SET guest_port = $2 WHERE id = $1", 0},
		{"public-port", "UPDATE app_udp_listeners SET public_port = $2 WHERE id = $1", 39999},
		{"protocol", "UPDATE app_udp_listeners SET protocol = $2 WHERE id = $1", "tcp"},
		{"name", "UPDATE app_udp_listeners SET listener_name = $2 WHERE id = $1", "Bad.Name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, test.query, listener.ID, test.value); err == nil {
				t.Fatal("database accepted invalid UDP intent")
			}
			got, err := s.UDPListenerByID(ctx, listener.ID)
			if err != nil || got != listener {
				t.Fatalf("rejected SQL changed enabled intent: got=%+v err=%v", got, err)
			}
		})
	}
}

func TestPgStoreUDPListenerReservationConcurrency(t *testing.T) {
	store, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx, "-udp-quota")

	for i := 0; i < api.UDPListenerReservationsPerAppMax-1; i++ {
		if _, err := store.CreateUDPListener(ctx, state.UDPListener{AccountID: accountID, AppID: appID, ListenerName: fmt.Sprintf("slot-%d", i), GuestPort: 1000 + i, PublicPort: 40000 + i}); err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		listener state.UDPListener
		err      error
	}
	results := make(chan result, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			listener, err := store.CreateUDPListener(ctx, state.UDPListener{AccountID: accountID, AppID: appID, ListenerName: fmt.Sprintf("racing-%d", i), GuestPort: 2000 + i, PublicPort: 41000 + i})
			results <- result{listener, err}
		}(i)
	}
	wg.Wait()
	close(results)
	var winner state.UDPListener
	successes := 0
	for r := range results {
		if r.err == nil {
			successes++
			winner = r.listener
			continue
		}
		var quota *state.UDPListenerLimitError
		if !errors.Is(r.err, state.ErrUDPListenerLimit) || !errors.As(r.err, &quota) || quota.Limit != api.UDPListenerReservationsPerAppMax || quota.Observed != api.UDPListenerReservationsPerAppMax+1 {
			t.Fatalf("unexpected creation result: %v", r.err)
		}
	}
	if successes != 1 {
		t.Fatalf("last slot admitted %d creators", successes)
	}
	rows, err := store.ListUDPListenersForApp(ctx, appID)
	if err != nil || len(rows) != api.UDPListenerReservationsPerAppMax {
		t.Fatalf("reservation count=%d error=%v", len(rows), err)
	}
	if _, err := store.CreateUDPListener(ctx, state.UDPListener{AccountID: accountID, AppID: appID, ListenerName: "slot-0", GuestPort: 3000, PublicPort: 42000}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate name at quota=%v", err)
	}
	if _, err := store.SetUDPListenerEnabled(ctx, winner.ID, false); err != nil {
		t.Fatal(err)
	}
	candidate := state.UDPListener{AccountID: accountID, AppID: appID, ListenerName: "replacement", GuestPort: 3001, PublicPort: 42001}
	if _, err := store.CreateUDPListener(ctx, candidate); !errors.Is(err, state.ErrUDPListenerLimit) {
		t.Fatalf("disable freed a reservation: %v", err)
	}
	if err := store.DeleteUDPListener(ctx, winner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUDPListener(ctx, candidate); err != nil {
		t.Fatalf("delete did not free a reservation: %v", err)
	}
	other, err := store.CreateApp(ctx, state.App{AccountID: accountID, Slug: "udp-quota-other", Status: state.AppActive, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	candidate.AppID = other.ID
	candidate.PublicPort = 42002
	if _, err := store.CreateUDPListener(ctx, candidate); err != nil {
		t.Fatalf("another app inherited quota: %v", err)
	}
}
