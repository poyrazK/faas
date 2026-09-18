package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreTCPListenerLifecycleAndRouteLookup(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-tcp-listener")
	created, err := s.CreateTCPListener(ctx, state.TCPListener{
		AccountID: accountID, AppID: appID, ListenerName: "postgres",
		GuestPort: 5432, PublicPort: 40126, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.TCPListenerByPublicPort(ctx, created.PublicPort)
	if err != nil || got.ID != created.ID {
		t.Fatalf("lookup by public port = %+v, %v", got, err)
	}
	if _, err := s.CreateTCPListener(ctx, state.TCPListener{
		AccountID: accountID, AppID: appID, ListenerName: "other",
		GuestPort: 5433, PublicPort: created.PublicPort,
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("public collision = %v, want ErrConflict", err)
	}
	disabled, err := s.SetTCPListenerEnabled(ctx, created.ID, false)
	if err != nil || disabled.Enabled {
		t.Fatalf("disable = %+v, %v", disabled, err)
	}
	if _, err := s.TCPListenerByPublicPort(ctx, created.PublicPort); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("disabled lookup = %v, want ErrNotFound", err)
	}
	if err := s.DeleteTCPListener(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.TCPListenerByID(ctx, created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("read after delete = %v, want ErrNotFound", err)
	}
}
