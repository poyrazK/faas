package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreUDPListenerReadDeadlineAndRecovery(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app, _ := seedLiveDeploy(t, s, ctx, "-udp-read-recovery")
	listener, err := s.CreateUDPListener(ctx, state.UDPListener{AccountID: account, AppID: app, ListenerName: "echo", GuestPort: 5353, PublicPort: 40129, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "LOCK TABLE app_udp_listeners IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	rows, err := s.ListEnabledUDPListeners(readCtx)
	if !errors.Is(err, context.DeadlineExceeded) || len(rows) != 0 {
		t.Fatalf("blocked read rows=%v err=%v", rows, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	recoveryCtx, cancelRecovery := context.WithTimeout(ctx, time.Second)
	defer cancelRecovery()
	rows, err = s.ListEnabledUDPListeners(recoveryCtx)
	if err != nil || len(rows) != 1 || rows[0].ID != listener.ID {
		t.Fatalf("recovered read rows=%v err=%v", rows, err)
	}
	if _, err := s.SetUDPListenerEnabled(recoveryCtx, listener.ID, false); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListEnabledUDPListeners(recoveryCtx)
	if err != nil || len(rows) != 0 {
		t.Fatalf("disable after recovery rows=%v err=%v", rows, err)
	}
}
