//go:build !no_pg

package db_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestNotifyPayloadMaxBytes_MatchesPostgres pins the constant to the server's
// actual behaviour rather than to the documentation's "~8 KB". The boundary is
// exact: 7999 is accepted, 8000 is not.
func TestNotifyPayloadMaxBytes_MatchesPostgres(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()

	atLimit := strings.Repeat("a", db.NotifyPayloadMaxBytes)
	if _, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", "limit_probe", atLimit); err != nil {
		t.Fatalf("Postgres rejected %d bytes; NotifyPayloadMaxBytes is too high: %v", len(atLimit), err)
	}

	overLimit := strings.Repeat("a", db.NotifyPayloadMaxBytes+1)
	if _, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", "limit_probe", overLimit); err == nil {
		t.Fatalf("Postgres accepted %d bytes; NotifyPayloadMaxBytes is too low", len(overLimit))
	}
}

// TestNotify_RejectsOversizePayloadBeforeTheRoundTrip pins the typed error.
//
// The previous contract delegated the limit to callers and enforced nothing,
// so an oversize payload surfaced as an opaque SQLSTATE from inside pgx — on
// a call whose error most producers deliberately discard.
func TestNotify_RejectsOversizePayloadBeforeTheRoundTrip(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()

	err := db.Notify(ctx, pool, db.NotifyCachePurge, strings.Repeat("a", db.NotifyPayloadMaxBytes+1))
	if err == nil {
		t.Fatal("oversize payload was accepted")
	}
	if !errors.Is(err, db.ErrNotifyPayloadTooLarge) {
		t.Fatalf("error is not matchable with errors.Is(ErrNotifyPayloadTooLarge): %v", err)
	}
	if !strings.Contains(err.Error(), db.NotifyCachePurge) {
		t.Fatalf("error does not name the channel: %v", err)
	}
}

// TestNotify_AcceptsPayloadAtTheLimit guards against an off-by-one that would
// reject a payload Postgres would have taken.
func TestNotify_AcceptsPayloadAtTheLimit(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()

	if err := db.Notify(ctx, pool, db.NotifyCachePurge,
		strings.Repeat("a", db.NotifyPayloadMaxBytes)); err != nil {
		t.Fatalf("payload of exactly the limit was rejected: %v", err)
	}
}
