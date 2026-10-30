package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

// An account's apps live on different nodes, but the per-account cron cap must
// hold across the whole fleet. AccountEligibleCronIDs ranks every app's crons
// together by age so each schedd computes the same eligible set.
func TestAccountEligibleCronIDsRanksGlobally(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	base := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	m.apps["A"] = App{ID: "A", AccountID: "acct", NodeID: "node-1", Status: AppActive}
	m.apps["B"] = App{ID: "B", AccountID: "acct", NodeID: "node-2", Status: AppActive}
	m.apps["other"] = App{ID: "other", AccountID: "someone-else", NodeID: "node-1", Status: AppActive}
	mk := func(id, app string, ageMin int, enabled bool, suspended string) {
		m.crons[id] = Cron{ID: id, AppID: app, CreatedAt: base.Add(time.Duration(ageMin) * time.Minute), Enabled: enabled, SuspendedReason: suspended}
	}
	mk("a1", "A", 0, true, "")
	mk("b1", "B", 1, true, "")
	mk("a2", "A", 2, true, "")
	mk("b2", "B", 3, true, "")
	mk("a3", "A", 4, true, "")
	mk("dis", "A", 5, false, "")          // disabled: excluded
	mk("susp", "B", 6, true, "no deploy") // suspended: excluded
	mk("x1", "other", 0, true, "")        // another account: excluded

	has := func(ids []string) map[string]bool {
		got := map[string]bool{}
		for _, id := range ids {
			got[id] = true
		}
		return got
	}

	// per-account cap 3, per-app cap not binding -> oldest 3 across both apps.
	ids, err := m.AccountEligibleCronIDs(ctx, "acct", 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := has(ids); len(ids) != 3 || !got["a1"] || !got["b1"] || !got["a2"] {
		t.Fatalf("eligible = %v; want oldest 3 across apps {a1,b1,a2}", ids)
	}

	// per-app cap 1 keeps only each app's oldest cron: a1 and b1.
	ids, _ = m.AccountEligibleCronIDs(ctx, "acct", 1, 3)
	if got := has(ids); len(ids) != 2 || !got["a1"] || !got["b1"] {
		t.Fatalf("eligible with per-app cap 1 = %v; want {a1,b1}", ids)
	}

	// Free-tier 0 caps -> nothing is eligible.
	if ids, _ := m.AccountEligibleCronIDs(ctx, "acct", 0, 0); len(ids) != 0 {
		t.Fatalf("zero caps returned %v; want none", ids)
	}

	// An organization that owns its resources owns every cron of its apps,
	// whoever created them; they are no longer counted against their
	// creators (bug hunt 2026-10-28 B37).
	m.orgs["org"] = Org{ID: "org", ResourceOwnershipEnabled: true}
	m.apps["C"] = App{ID: "C", AccountID: "acct", OrgID: "org", Status: AppActive}
	m.apps["D"] = App{ID: "D", AccountID: "someone-else", OrgID: "org", Status: AppActive}
	mk("c1", "C", 7, true, "")
	mk("d1", "D", 8, true, "")
	ids, _ = m.AccountEligibleCronIDs(ctx, "org", 10, 10)
	if got := has(ids); len(ids) != 2 || !got["c1"] || !got["d1"] {
		t.Fatalf("organization eligible = %v; want both members' crons {c1,d1}", ids)
	}
	if ids, _ := m.AccountEligibleCronIDs(ctx, "acct", 10, 10); has(ids)["c1"] {
		t.Fatalf("creator's eligible = %v; a company cron still counts against its creator", ids)
	}
}

func TestClaimCronFireCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	fired := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	m.crons["c"] = Cron{ID: "c", AppID: "A"} // never fired (zero LastFiredAt)

	ok, err := m.ClaimCronFire(ctx, "c", time.Time{}, fired)
	if err != nil || !ok {
		t.Fatalf("first claim from never-fired cursor ok=%v err=%v; want true", ok, err)
	}
	// A rival schedd with the same stale expected cursor loses.
	ok, err = m.ClaimCronFire(ctx, "c", time.Time{}, fired.Add(time.Minute))
	if err != nil || ok {
		t.Fatalf("stale claim ok=%v err=%v; want false", ok, err)
	}
	// The next occurrence claims from the current cursor and wins.
	next := fired.Add(time.Minute)
	if ok, _ := m.ClaimCronFire(ctx, "c", fired, next); !ok {
		t.Fatal("claim from current cursor lost")
	}
	// Rollback to a zero firedAt restores the never-fired (NULL) state.
	if ok, _ := m.ClaimCronFire(ctx, "c", next, time.Time{}); !ok {
		t.Fatal("rollback claim lost")
	}
	if got := m.crons["c"].LastFiredAt; !got.IsZero() {
		t.Fatalf("after rollback LastFiredAt=%v; want zero", got)
	}
	if _, err := m.ClaimCronFire(ctx, "missing", time.Time{}, fired); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing cron err=%v; want ErrNotFound", err)
	}
}
