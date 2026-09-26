//go:build !no_pg

package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgUUID(t *testing.T, id string) pgtype.UUID {
	t.Helper()
	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatal(err)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

// TestPgStoreListAppErrorGroupsPagesEveryGroupOnce drives the ADR-096 error
// summary exactly as apid does: the first page carries no cursor, which the
// handler encodes as CursorCount 0 (the column is not nullable), and later
// pages carry the last row's (count, last_seen_at, fingerprint).
func TestPgStoreListAppErrorGroupsPagesEveryGroupOnce(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, "errors@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "errs", Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	// Four groups that tie on count and last_seen_at (one burst, same
	// millisecond), plus one older group.
	fingerprints := []string{strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64)}
	for _, fp := range fingerprints {
		if _, err := store.IncrementAppError(ctx, sqlc.IncrementAppErrorParams{
			ID: pgUUID(t, uuid.NewString()), AccountID: pgUUID(t, acct.ID), AppID: pgUUID(t, app.ID),
			Fingerprint: fp, Route: "/" + fp[:4], HttpStatus: 500, ErrorClass: "unhandled",
			FirstSeenAt: pgtype.Timestamptz{Time: seen, Valid: true},
		}); err != nil {
			t.Fatalf("increment %s: %v", fp, err)
		}
	}
	if _, err := store.IncrementAppError(ctx, sqlc.IncrementAppErrorParams{
		ID: pgUUID(t, uuid.NewString()), AccountID: pgUUID(t, acct.ID), AppID: pgUUID(t, app.ID),
		Fingerprint: strings.Repeat("e", 64), Route: "/old", HttpStatus: 502, ErrorClass: "unhandled",
		FirstSeenAt: pgtype.Timestamptz{Time: seen.Add(-time.Hour), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}

	params := sqlc.ListAppErrorGroupsParams{
		AccountID: pgUUID(t, acct.ID), AppID: pgUUID(t, app.ID),
		Since: pgtype.Timestamptz{Time: seen.Add(-24 * time.Hour), Valid: true},
		Until: pgtype.Timestamptz{Time: seen.Add(time.Hour), Valid: true},
		Limit: 2,
	}
	got := map[string]int{}
	for page := 0; page < 10; page++ {
		rows, err := store.ListAppErrorGroups(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			got[row.Fingerprint]++
		}
		if len(rows) < int(params.Limit) {
			break
		}
		last := rows[len(rows)-1]
		params.CursorCount = last.Count
		params.CursorLastSeen = pgtype.Timestamptz{Time: last.LastSeenAt, Valid: true}
		params.CursorFingerprint = last.Fingerprint
	}
	for _, fp := range append(fingerprints, strings.Repeat("e", 64)) {
		if got[fp] != 1 {
			t.Fatalf("group %s listed %d times across pages (all: %v), want exactly once", fp, got[fp], got)
		}
	}
}
