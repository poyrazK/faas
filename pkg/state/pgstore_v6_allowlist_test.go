package state_test

// ADR-032: round-trip the v6 mirror through the real PgStore so the
// DB trigger `apps_egress_allowlist_cidr` (migration 00030) is
// exercised end-to-end. The MemStore hermetic suite covers the
// parse / Set semantics; this file pins the SQL surface.
//
// pgtest.Open skips when Postgres is unreachable, so the file stays
// green on a plain `make test` run in environments without a
// running cluster (no_pg default build).

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedAppForAllowlist provisions a fresh account + app under a
// unique slug so UpdateApp's row-write side-effects don't collide
// between parallel subtests. Returns the app id.
func seedAppForAllowlist(t *testing.T, ctx context.Context, s *state.PgStore, label string) string {
	t.Helper()
	email := label + "@example.com"
	acct, err := s.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           label,
		Type:           state.AppTypeApp,
		RAMMB:          512,
		MaxConcurrency: 5,
		IdleTimeoutS:   300,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return app.ID
}

// TestPgStore_UpdateApp_V6RoundTrip pins that a v6-only allowlist
// survives UpdateApp + AppByID under the new trigger contract.
func TestPgStore_UpdateApp_V6RoundTrip(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	s := state.NewPgStore(pool)
	appID := seedAppForAllowlist(t, ctx, s, "v6-roundtrip")

	v6 := []netip.Prefix{
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	updated, err := s.UpdateApp(ctx, appID, state.UpdateAppParams{
		EgressAllowlist:    &v6,
		SetEgressAllowlist: true,
	})
	if err != nil {
		t.Fatalf("UpdateApp v6: %v", err)
	}
	if len(updated.EgressAllowlist) != 2 {
		t.Fatalf("after update v6: len = %d, want 2 (%+v)", len(updated.EgressAllowlist), updated.EgressAllowlist)
	}
	if updated.EgressAllowlist[0].String() != "fe80::/10" ||
		updated.EgressAllowlist[1].String() != "2001:db8::/32" {
		t.Errorf("after update v6: got %+v, want [fe80::/10 2001:db8::/32]", updated.EgressAllowlist)
	}

	// Re-read via AppByID to confirm the column deserialises
	// back to []netip.Prefix identically.
	readBack, err := s.AppByID(ctx, appID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if len(readBack.EgressAllowlist) != 2 {
		t.Fatalf("AppByID v6: len = %d, want 2", len(readBack.EgressAllowlist))
	}
	if readBack.EgressAllowlist[0].String() != "fe80::/10" ||
		readBack.EgressAllowlist[1].String() != "2001:db8::/32" {
		t.Errorf("AppByID v6: got %+v, want [fe80::/10 2001:db8::/32]", readBack.EgressAllowlist)
	}
}

// TestPgStore_UpdateApp_MixedRoundTrip pins that a v4 + v6 mixed
// allowlist survives UpdateApp + AppByID. The DB trigger doesn't
// partition by family — it only enforces v4-or-v6 + non-/0.
func TestPgStore_UpdateApp_MixedRoundTrip(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	s := state.NewPgStore(pool)
	appID := seedAppForAllowlist(t, ctx, s, "mix-roundtrip")

	mixed := []netip.Prefix{
		netip.MustParsePrefix("1.2.3.0/24"),
		netip.MustParsePrefix("fe80::/10"),
	}
	updated, err := s.UpdateApp(ctx, appID, state.UpdateAppParams{
		EgressAllowlist:    &mixed,
		SetEgressAllowlist: true,
	})
	if err != nil {
		t.Fatalf("UpdateApp mixed: %v", err)
	}
	if len(updated.EgressAllowlist) != 2 {
		t.Fatalf("after update mixed: len = %d, want 2", len(updated.EgressAllowlist))
	}
	readBack, err := s.AppByID(ctx, appID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if len(readBack.EgressAllowlist) != 2 {
		t.Fatalf("AppByID mixed: len = %d, want 2", len(readBack.EgressAllowlist))
	}
	if readBack.EgressAllowlist[0].String() != "1.2.3.0/24" ||
		readBack.EgressAllowlist[1].String() != "fe80::/10" {
		t.Errorf("AppByID mixed: got %+v, want [1.2.3.0/24 fe80::/10]", readBack.EgressAllowlist)
	}
}

// TestPgStore_UpdateApp_SlashZeroRejected pins the ADR-032 non-/0
// contract: `0.0.0.0/0` and `::/0` both raise the same SQLSTATE
// 23514 with constraint `apps_egress_allowlist_cidr` so the caller's
// error surface stays uniform regardless of family.
func TestPgStore_UpdateApp_SlashZeroRejected(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	s := state.NewPgStore(pool)

	for _, tc := range []struct {
		label string
		slug  string
		cidr  string
	}{
		{"v4-zero", "v4-slashzero", "0.0.0.0/0"},
		{"v6-zero", "v6-slashzero", "::/0"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			appID := seedAppForAllowlist(t, ctx, s, tc.slug)
			bad := []netip.Prefix{netip.MustParsePrefix(tc.cidr)}
			_, err := s.UpdateApp(ctx, appID, state.UpdateAppParams{
				EgressAllowlist:    &bad,
				SetEgressAllowlist: true,
			})
			if err == nil {
				t.Fatalf("UpdateApp(%q): expected rejection, got nil", tc.cidr)
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				t.Fatalf("UpdateApp(%q): expected *pgconn.PgError, got %T: %v", tc.cidr, err, err)
			}
			if pgErr.Code != "23514" {
				t.Errorf("UpdateApp(%q): SQLSTATE = %q, want 23514", tc.cidr, pgErr.Code)
			}
			if pgErr.ConstraintName != "apps_egress_allowlist_cidr" {
				t.Errorf("UpdateApp(%q): constraint = %q, want apps_egress_allowlist_cidr", tc.cidr, pgErr.ConstraintName)
			}
			// Sanity: the message should name the rejected entry
			// so an operator can grep the logs and see the
			// offender. Either the slash form or the word
			// "masklen" is acceptable — the trigger says
			// "masklen /0; ADR-032 non-/0 contract".
			if !strings.Contains(pgErr.Message, tc.cidr) {
				t.Errorf("UpdateApp(%q): message %q does not name the rejected entry", tc.cidr, pgErr.Message)
			}
			if !strings.Contains(pgErr.Message, "/0") && !strings.Contains(pgErr.Message, "masklen") {
				t.Errorf("UpdateApp(%q): message %q does not name /0 or masklen", tc.cidr, pgErr.Message)
			}
		})
	}
}
