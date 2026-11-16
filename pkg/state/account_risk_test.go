package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261115090000 — the risk view's store reads: strikes in a window,
// links to a held account, recent links with sign-up facts, and abuse
// events about an account filtered by kind and time.
func exerciseAccountRisk(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	tag := now.Format("150405.000000")
	held, err := store.CreateAccount(ctx, "risk-held-"+tag+"@risk-"+tag+".io", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	newcomer, err := store.CreateAccount(ctx, "risk-new-"+tag+"@risk-"+tag+".io", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []state.AccountSignupFacts{
		{AccountID: held.ID, Method: "password", SignupIP: "203.0.113.40", EmailDomain: "risk-" + tag + ".io", CreatedAt: now},
		{AccountID: newcomer.ID, Method: "password", SignupIP: "203.0.113.41", Country: "FR", EmailDomain: "risk-" + tag + ".io", CreatedAt: now},
	} {
		if err := store.RecordAccountSignupFacts(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.RecordAccountAbuseStrike(ctx, held.ID, state.AccountAbuseHoldEgressFlood, "", now.Add(-5*time.Minute), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAccountAbuseHold(ctx, held.ID, state.AccountAbuseHoldEgressFlood, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkAccountToHeld(ctx, newcomer.ID, true, now.Add(-time.Hour), now); err != nil {
		t.Fatal(err)
	}
	subject := held.ID
	if err := store.AppendEvent(ctx, "schedd", "accounts.abuse_hold", &subject, []byte(`{"reason":"egress_flood"}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "schedd", "wake.boot_started", &subject, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}

	strikes, err := store.AccountAbuseStrikes(ctx, held.ID, now.Add(-time.Hour))
	if err != nil || len(strikes) != 1 || strikes[0].Reason != state.AccountAbuseHoldEgressFlood {
		t.Fatalf("AccountAbuseStrikes = %+v, %v", strikes, err)
	}
	if strikes, err := store.AccountAbuseStrikes(ctx, held.ID, now); err != nil || len(strikes) != 0 {
		t.Fatalf("strikes after now = %+v, %v; want none", strikes, err)
	}
	linked, err := store.HeldLinksToAccount(ctx, held.ID)
	if err != nil || len(linked) != 2 {
		t.Fatalf("HeldLinksToAccount = %+v, %v; want network and email_domain links", linked, err)
	}
	for _, l := range linked {
		if l.AccountID != newcomer.ID || l.HeldAccountID != held.ID {
			t.Fatalf("unexpected link %+v", l)
		}
	}
	recent, err := store.RecentHeldLinks(ctx, now.Add(-time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, r := range recent {
		if r.AccountID == newcomer.ID {
			found++
			if r.Country != "FR" || r.EmailDomain != "risk-"+tag+".io" {
				t.Fatalf("recent link facts = %+v", r)
			}
		}
	}
	if found != 2 {
		t.Fatalf("RecentHeldLinks found %d links for the newcomer, want 2: %+v", found, recent)
	}
	events, err := store.AccountAbuseEvents(ctx, held.ID, []string{"accounts.abuse_hold", "instance.cpu_abuse"}, now.Add(-time.Hour), 10)
	if err != nil || len(events) != 1 || events[0].Kind != "accounts.abuse_hold" || events[0].Actor != "schedd" {
		t.Fatalf("AccountAbuseEvents = %+v, %v; want only the hold", events, err)
	}
}

func TestMemStoreAccountRisk(t *testing.T) {
	exerciseAccountRisk(t, state.NewMemStore())
}

func TestPgStoreAccountRisk(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAccountRisk(t, state.NewPgStore(pool))
}
