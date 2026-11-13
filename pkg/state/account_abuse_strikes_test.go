package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261113133500 — strikes count per resource owner inside the window,
// across reasons; strikes outside it and other owners' strikes never count.
func exerciseAccountAbuseStrikes(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	owner, other, app := uuid.NewString(), uuid.NewString(), uuid.NewString()
	window := time.Hour
	start := time.Now().UTC().Truncate(time.Microsecond)

	record := func(account, reason, appID string, at time.Time) int {
		t.Helper()
		n, err := store.RecordAccountAbuseStrike(ctx, account, reason, appID, at, window)
		if err != nil {
			t.Fatalf("RecordAccountAbuseStrike(%s, %s): %v", account, reason, err)
		}
		return n
	}
	if n := record(owner, state.AccountAbuseHoldEgressFanout, app, start); n != 1 {
		t.Fatalf("first strike count = %d, want 1", n)
	}
	if n := record(other, state.AccountAbuseHoldCPUSustained, "", start); n != 1 {
		t.Fatalf("other owner's first strike count = %d, want 1 (strikes leaked across owners)", n)
	}
	if n := record(owner, state.AccountAbuseHoldCPUSustained, "", start.Add(30*time.Minute)); n != 2 {
		t.Fatalf("second strike in window = %d, want 2 (reasons share one count)", n)
	}
	// The first strike is a window old: it no longer counts.
	if n := record(owner, state.AccountAbuseHoldEgressFlood, app, start.Add(window)); n != 2 {
		t.Fatalf("strike a window after the first = %d, want 2", n)
	}
	if n := record(owner, state.AccountAbuseHoldEgressFlood, app, start.Add(3*window)); n != 1 {
		t.Fatalf("strike after a quiet window = %d, want 1", n)
	}

	for _, bad := range []struct{ account, reason, app string }{
		{owner, state.AccountAbuseHoldOperator, ""},
		{owner, "mining", ""},
		{"", state.AccountAbuseHoldEgressFanout, ""},
	} {
		if _, err := store.RecordAccountAbuseStrike(ctx, bad.account, bad.reason, bad.app, start, window); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("RecordAccountAbuseStrike(%q, %q) = %v, want ErrInvalidArgument", bad.account, bad.reason, err)
		}
	}
}

func TestMemStoreAccountAbuseStrikes(t *testing.T) {
	exerciseAccountAbuseStrikes(t, state.NewMemStore())
}

func TestPgStoreAccountAbuseStrikes(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAccountAbuseStrikes(t, state.NewPgStore(pool))
}
