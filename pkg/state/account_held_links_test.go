package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/disposableemail"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261114174600 — a new account links to abuse-held accounts (and a
// held organization's owner) that share its sign-up network, by sign-up or a
// recent sign-in, or its private email domain. Accounts that are not held,
// stale sign-ins and unlinkable domains never link, and a repeat check adds
// nothing.
func exerciseAccountHeldLinks(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	since := now.Add(-api.SignupFactsPersonalRetention)
	tag := now.Format("150405.000000")
	account := func(email, ip string) state.Account {
		t.Helper()
		a, err := store.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount(%s): %v", email, err)
		}
		if ip != "" {
			if err := store.RecordAccountSignupFacts(ctx, state.AccountSignupFacts{
				AccountID: a.ID, Method: "password", SignupIP: ip, EmailDomain: disposableemail.EmailDomain(email), CreatedAt: now,
			}); err != nil {
				t.Fatalf("RecordAccountSignupFacts(%s): %v", email, err)
			}
		}
		return a
	}
	hold := func(id string) {
		t.Helper()
		if _, err := store.SetAccountAbuseHold(ctx, id, state.AccountAbuseHoldEgressFanout, now); err != nil {
			t.Fatalf("SetAccountAbuseHold(%s): %v", id, err)
		}
	}
	private := "acme-" + tag + ".io"

	// Held by its own sign-up address and a private domain.
	heldBySignup := account("held-signup-"+tag+"@"+private, "203.0.113.5")
	hold(heldBySignup.ID)
	// Held, signed up before sign-up records existed: only a sign-in address.
	heldBySession := account("held-session-"+tag+"@example.org", "")
	hold(heldBySession.ID)
	if _, err := store.CreateSession(ctx, uuid.NewString(), heldBySession.ID, "198.51.100.9", "ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Same network as the new accounts, but not held.
	account("innocent-"+tag+"@"+private, "203.0.113.6")

	first := account("new-1-"+tag+"@"+private, "203.0.113.77")
	links, err := store.LinkAccountToHeld(ctx, first.ID, true, since, now)
	if err != nil {
		t.Fatalf("LinkAccountToHeld(first): %v", err)
	}
	want := map[string]bool{
		heldBySignup.ID + "|" + state.HeldLinkSignalNetwork:     true,
		heldBySignup.ID + "|" + state.HeldLinkSignalEmailDomain: true,
	}
	if len(links) != len(want) {
		t.Fatalf("first links = %+v; want network and email_domain links to the held account only", links)
	}
	for _, l := range links {
		if !want[l.HeldAccountID+"|"+l.Signal] || l.HeldReason != state.AccountAbuseHoldEgressFanout || l.HeldOrgID != "" || l.AccountID != first.ID {
			t.Fatalf("unexpected link %+v", l)
		}
	}
	if again, err := store.LinkAccountToHeld(ctx, first.ID, true, since, now.Add(time.Minute)); err != nil || len(again) != 0 {
		t.Fatalf("repeat LinkAccountToHeld = %+v, %v; want nothing new", again, err)
	}
	stored, err := store.AccountHeldLinks(ctx, first.ID)
	if err != nil || len(stored) != 2 {
		t.Fatalf("AccountHeldLinks(first) = %+v, %v; want the two links", stored, err)
	}

	// Network from a held account's sign-in; the domain is not linkable.
	second := account("new-2-"+tag+"@"+private, "198.51.100.200")
	links, err = store.LinkAccountToHeld(ctx, second.ID, false, since, now)
	if err != nil {
		t.Fatalf("LinkAccountToHeld(second): %v", err)
	}
	if len(links) != 1 || links[0].HeldAccountID != heldBySession.ID || links[0].Signal != state.HeldLinkSignalNetwork {
		t.Fatalf("second links = %+v; want one network link to the account held by its sign-in", links)
	}
	// The same sign-in no longer counts once it is older than since.
	third := account("new-3-"+tag+"@example.net", "198.51.100.201")
	if links, err := store.LinkAccountToHeld(ctx, third.ID, false, now.Add(time.Hour), now); err != nil || len(links) != 0 {
		t.Fatalf("links with a stale sign-in = %+v, %v; want none", links, err)
	}

	// A held organization links to its owner.
	owner := account("org-owner-"+tag+"@example.com", "192.0.2.10")
	org, err := store.CreateOrg(ctx, state.Org{ID: uuid.NewString(), Slug: "held-" + uuid.NewString()[:8], Name: "Held", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatalf("AddOrgMember: %v", err)
	}
	hold(org.ID)
	fourth := account("new-4-"+tag+"@example.com", "192.0.2.99")
	links, err = store.LinkAccountToHeld(ctx, fourth.ID, false, since, now)
	if err != nil {
		t.Fatalf("LinkAccountToHeld(fourth): %v", err)
	}
	if len(links) != 1 || links[0].HeldAccountID != owner.ID || links[0].HeldOrgID != org.ID || links[0].Signal != state.HeldLinkSignalNetwork {
		t.Fatalf("fourth links = %+v; want a network link to the held organization's owner", links)
	}

	// No sign-up record: nothing to compare.
	bare := account("bare-"+tag+"@"+private, "")
	if links, err := store.LinkAccountToHeld(ctx, bare.ID, true, since, now); err != nil || len(links) != 0 {
		t.Fatalf("links without a sign-up record = %+v, %v; want none", links, err)
	}
}

func TestMemStoreAccountHeldLinks(t *testing.T) {
	exerciseAccountHeldLinks(t, state.NewMemStore())
}

func TestPgStoreAccountHeldLinks(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAccountHeldLinks(t, state.NewPgStore(pool))
}
