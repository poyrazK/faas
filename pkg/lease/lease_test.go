package lease_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/dispatch"
	"github.com/onebox-faas/faas/pkg/lease"
)

// fixedClock returns the same instant for every call so tests are
// deterministic. Tests inject this via lease.New(...).
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// setupTestTable creates a table in the pgtest schema (the helper
// gives every test its own schema + drops it on test cleanup) and
// inserts one row in state='pending'. We use a fresh table rather
// than the production `instances` table so the test doesn't
// require the full account+app+deployment fixtures instances
// demands.
func setupTestTable(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		create table lease_test (
			id text primary key,
			state text not null,
			lease_token text,
			lease_expires_at timestamptz
		)`)
	if err != nil {
		t.Fatalf("create test table: %v", err)
	}
	rowID := "row-1"
	_, err = pool.Exec(ctx,
		`insert into lease_test (id, state) values ($1, 'pending')`, rowID)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}
	return rowID
}

func TestLease_AcquireStampsTokenAndExpiry(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	rowID := setupTestTable(t, pool)

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	mgr := lease.New(pool, "lease_test", "id", "state",
		"lease_token", "lease_expires_at", fixedClock(now))

	got, err := mgr.Acquire(ctx, rowID, "pending", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got.Token == "" {
		t.Fatal("Acquire returned empty token")
	}
	want := now.Add(time.Minute)
	if !got.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, want)
	}

	// Verify the row in PG.
	var token string
	var expires time.Time
	if err := pool.QueryRow(ctx,
		`select lease_token, lease_expires_at from lease_test where id = $1`,
		rowID).Scan(&token, &expires); err != nil {
		t.Fatalf("scan row: %v", err)
	}
	if token != got.Token {
		t.Errorf("DB token = %q, want %q", token, got.Token)
	}
	if !expires.Equal(want) {
		t.Errorf("DB expires_at = %v, want %v", expires, want)
	}
}

func TestLease_AcquireStateMismatch(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	rowID := setupTestTable(t, pool)

	mgr := lease.New(pool, "lease_test", "id", "state",
		"lease_token", "lease_expires_at", fixedClock(time.Now()))

	// expectedState="running" but row is in 'pending' → conflict.
	if _, err := mgr.Acquire(ctx, rowID, "running", time.Minute); !errors.Is(err, lease.ErrLeaseConflict) {
		t.Fatalf("Acquire(running) = %v, want ErrLeaseConflict", err)
	}
}

func TestLease_AcquireStealsExpiredLease(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	rowID := setupTestTable(t, pool)

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

	// Stamp a stale lease manually.
	_, err := pool.Exec(ctx,
		`update lease_test set lease_token = 'stale', lease_expires_at = $1 where id = $2`,
		now.Add(-time.Minute), rowID)
	if err != nil {
		t.Fatalf("seed stale lease: %v", err)
	}

	mgr := lease.New(pool, "lease_test", "id", "state",
		"lease_token", "lease_expires_at", fixedClock(now))

	got, err := mgr.Acquire(ctx, rowID, "pending", time.Minute)
	if err != nil {
		t.Fatalf("Acquire should steal expired lease: %v", err)
	}
	if got.Token == "" || got.Token == "stale" {
		t.Fatalf("expected new token, got %q", got.Token)
	}
}

func TestLease_AcquireRejectsLiveLease(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	rowID := setupTestTable(t, pool)

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	mgr := lease.New(pool, "lease_test", "id", "state",
		"lease_token", "lease_expires_at", fixedClock(now))

	// First claim succeeds.
	if _, err := mgr.Acquire(ctx, rowID, "pending", time.Hour); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	// Second claim must conflict — lease still live.
	if _, err := mgr.Acquire(ctx, rowID, "pending", time.Hour); !errors.Is(err, lease.ErrLeaseConflict) {
		t.Fatalf("second Acquire = %v, want ErrLeaseConflict", err)
	}
}

func TestLease_RenewExtendsTTL(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	rowID := setupTestTable(t, pool)

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	mgr := lease.New(pool, "lease_test", "id", "state",
		"lease_token", "lease_expires_at", fixedClock(now))

	first, err := mgr.Acquire(ctx, rowID, "pending", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Advance the clock by 30s and renew with a 2-minute TTL.
	mgr2 := lease.New(pool, "lease_test", "id", "state",
		"lease_token", "lease_expires_at", fixedClock(now.Add(30*time.Second)))
	renewed, err := mgr2.Renew(ctx, rowID, first.Token, 2*time.Minute)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	want := now.Add(30 * time.Second).Add(2 * time.Minute)
	if !renewed.ExpiresAt.Equal(want) {
		t.Errorf("renewed ExpiresAt = %v, want %v", renewed.ExpiresAt, want)
	}
}

func TestLease_RenewStrictToken(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	rowID := setupTestTable(t, pool)

	mgr := lease.New(pool, "lease_test", "id", "state",
		"lease_token", "lease_expires_at", fixedClock(time.Now()))

	first, err := mgr.Acquire(ctx, rowID, "pending", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// Renew with wrong token → conflict.
	if _, err := mgr.Renew(ctx, rowID, "wrong-token", time.Minute); !errors.Is(err, lease.ErrLeaseConflict) {
		t.Fatalf("Renew(wrong) = %v, want ErrLeaseConflict", err)
	}
	// Correct token still works.
	if _, err := mgr.Renew(ctx, rowID, first.Token, time.Minute); err != nil {
		t.Fatalf("Renew(correct): %v", err)
	}
}

func TestLease_ReleaseStrictToken(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	rowID := setupTestTable(t, pool)

	mgr := lease.New(pool, "lease_test", "id", "state",
		"lease_token", "lease_expires_at", fixedClock(time.Now()))

	first, err := mgr.Acquire(ctx, rowID, "pending", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// Release with wrong token → conflict.
	if err := mgr.Release(ctx, rowID, "wrong"); !errors.Is(err, lease.ErrLeaseConflict) {
		t.Fatalf("Release(wrong) = %v, want ErrLeaseConflict", err)
	}
	// Correct token releases.
	if err := mgr.Release(ctx, rowID, first.Token); err != nil {
		t.Fatalf("Release(correct): %v", err)
	}
	// Verify the row is back to a free state.
	var token *string
	var expires *time.Time
	if err := pool.QueryRow(ctx,
		`select lease_token, lease_expires_at from lease_test where id = $1`,
		rowID).Scan(&token, &expires); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if token != nil || expires != nil {
		t.Errorf("expected NULLs, got token=%v expires=%v", token, expires)
	}
}

func TestLease_AcquireOnMissingRow(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()

	mgr := lease.New(pool, "lease_test", "id", "state",
		"lease_token", "lease_expires_at", fixedClock(time.Now()))

	if _, err := mgr.Acquire(ctx, "nonexistent", "pending", time.Minute); !errors.Is(err, lease.ErrLeaseConflict) {
		t.Fatalf("Acquire(missing) = %v, want ErrLeaseConflict", err)
	}
}

func TestLease_NewRejectsUnsafeIdentifier(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New should panic on unsafe identifier")
		}
	}()
	lease.New(nil, "instances; DROP TABLE x", "id", "state",
		"lease_token", "lease_expires_at", time.Now)
}

func TestLease_Expired(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		l    dispatch.Lease
		want bool
	}{
		{"empty token", dispatch.Lease{}, true},
		{"future expires", dispatch.Lease{Token: "t", ExpiresAt: now.Add(time.Minute)}, false},
		{"past expires", dispatch.Lease{Token: "t", ExpiresAt: now.Add(-time.Second)}, true},
		{"zero expires + token", dispatch.Lease{Token: "t"}, false},
		{"exact now", dispatch.Lease{Token: "t", ExpiresAt: now}, true},
	}
	for _, c := range cases {
		if got := c.l.Expired(now); got != c.want {
			t.Errorf("%s: Expired()=%v want %v", c.name, got, c.want)
		}
	}
}
