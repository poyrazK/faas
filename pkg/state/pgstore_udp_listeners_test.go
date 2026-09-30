package state_test

import (
	"errors"
	"testing"

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
