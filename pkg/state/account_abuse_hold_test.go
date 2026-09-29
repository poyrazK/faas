package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 361 — the account abuse hold makes the account inactive without
// touching its billing status, keeps the first reason, and releases cleanly.
func exerciseAccountAbuseHold(t *testing.T, store interface {
	state.Store
	state.AccountAbuseHoldStore
}) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "abuse-hold-"+time.Now().Format("150405.000000")+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	placed, err := store.SetAccountAbuseHold(ctx, acct.ID, state.AccountAbuseHoldEgressFanout, at)
	if err != nil || !placed {
		t.Fatalf("SetAccountAbuseHold = %v, %v; want placed", placed, err)
	}
	if placed, err := store.SetAccountAbuseHold(ctx, acct.ID, state.AccountAbuseHoldOperator, at.Add(time.Hour)); err != nil || placed {
		t.Fatalf("second SetAccountAbuseHold = %v, %v; want kept", placed, err)
	}
	got, err := store.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if !got.AbuseHeld() || got.AbuseHoldReason != state.AccountAbuseHoldEgressFanout || !got.AbuseHoldAt.Equal(at) {
		t.Fatalf("held account = at %v reason %q, want %v egress_fanout", got.AbuseHoldAt, got.AbuseHoldReason, at)
	}
	if got.Active() || got.MayDeploy() || got.Status != state.AccountActive {
		t.Fatalf("held account Active=%v MayDeploy=%v Status=%s; want inactive, billing status untouched", got.Active(), got.MayDeploy(), got.Status)
	}
	if code := got.InactiveProblem().Code; code != api.CodeAccountAbuseHold {
		t.Fatalf("InactiveProblem code = %s, want %s", code, api.CodeAccountAbuseHold)
	}
	released, err := store.ReleaseAccountAbuseHold(ctx, acct.ID)
	if err != nil || !released {
		t.Fatalf("ReleaseAccountAbuseHold = %v, %v; want released", released, err)
	}
	if released, err := store.ReleaseAccountAbuseHold(ctx, acct.ID); err != nil || released {
		t.Fatalf("second release = %v, %v; want no-op", released, err)
	}
	if got, _ := store.AccountByID(ctx, acct.ID); !got.Active() || got.AbuseHoldReason != "" {
		t.Fatalf("released account Active=%v reason=%q", got.Active(), got.AbuseHoldReason)
	}
	if _, err := store.SetAccountAbuseHold(ctx, "00000000-0000-0000-0000-000000000000", state.AccountAbuseHoldOperator, at); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("hold on a missing account = %v, want state.ErrNotFound", err)
	}
}

func TestMemStoreAccountAbuseHold(t *testing.T) {
	exerciseAccountAbuseHold(t, state.NewMemStore())
}

func TestPgStoreAccountAbuseHold(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAccountAbuseHold(t, state.NewPgStore(pool))
}
