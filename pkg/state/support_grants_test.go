package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// supportGrantLifecycle is the store contract of customer-granted support
// access (ADR-20261116163508): a grant is usable while it is live and its
// creator is still an owner or admin, it fences support writes, and
// requests are capped and decided once.
func supportGrantLifecycle(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	f := setupOffboarding(t, store)
	grants, ok := store.(state.SupportGrantStore)
	if !ok {
		t.Fatal("store has no support grants")
	}
	operator, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "support-operator@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	fenced := func(grantID string) context.Context {
		ctx, cancel := state.WithOrgAccessFence(ctx, state.OrgAccessFence{OrgID: f.org.ID, AccountID: operator.Account.ID, SupportGrantID: grantID})
		t.Cleanup(func() { cancel(nil) })
		if mem, ok := store.(*state.MemStore); ok {
			ctx = mem.WithMemoryIntentFence(ctx)
		}
		return ctx
	}

	g, err := grants.CreateSupportGrant(ctx, state.NewSupportGrant{OrgID: f.org.ID, AppIDs: []string{f.app.ID}, CreatedBy: f.owner.ID, Reason: "deploy help", TTL: 4 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if !g.Covers(f.app.ID) || g.Covers("00000000-0000-0000-0000-000000000001") || g.OrgSlug != f.org.Slug {
		t.Fatalf("grant = %+v", g)
	}
	if _, err := grants.UsableSupportGrant(ctx, g.ID); err != nil {
		t.Fatalf("usable owner grant = %v", err)
	}
	if err := store.CheckOrgAccess(fenced(g.ID)); err != nil {
		t.Fatalf("fence on a live grant = %v", err)
	}
	if _, err := grants.CreateSupportGrant(ctx, state.NewSupportGrant{OrgID: f.org.ID, CreatedBy: f.owner.ID, Reason: "too long", TTL: 8 * 24 * time.Hour}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("eight-day grant = %v, want ErrInvalidArgument", err)
	}

	// A developer's grant is never usable; an admin's ends with the role.
	dev, err := grants.CreateSupportGrant(ctx, state.NewSupportGrant{OrgID: f.org.ID, CreatedBy: f.actor.ID, Reason: "developer", TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grants.UsableSupportGrant(ctx, dev.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("developer grant usable = %v, want ErrNotFound", err)
	}
	if err := store.UpdateOrgMemberRole(ctx, f.org.ID, f.actor.ID, state.OrgRoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := grants.UsableSupportGrant(ctx, dev.ID); err != nil {
		t.Fatalf("grant of a promoted admin = %v", err)
	}
	if err := store.UpdateOrgMemberRole(ctx, f.org.ID, f.actor.ID, state.OrgRoleDeveloper); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckOrgAccess(fenced(dev.ID)); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Fatalf("fence after the granter's demotion = %v, want ErrOrgAccessRevoked", err)
	}

	// Revoking ends the grant and its fence.
	if err := grants.RevokeSupportGrant(ctx, f.org.ID, g.ID, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := grants.RevokeSupportGrant(ctx, f.org.ID, g.ID, f.owner.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second revoke = %v, want ErrNotFound", err)
	}
	if err := store.CheckOrgAccess(fenced(g.ID)); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Fatalf("fence after revoke = %v, want ErrOrgAccessRevoked", err)
	}
	if got, err := grants.SupportGrantByID(ctx, g.ID); err != nil || got.RevokedAt == nil || got.RevokedBy != f.owner.ID {
		t.Fatalf("revoked grant = %+v, %v", got, err)
	}

	// An ended grant is claimed for its summary exactly once; a live one never.
	claimed, err := grants.ClaimEndedSupportGrants(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	gotRevoked := false
	for _, c := range claimed {
		gotRevoked = gotRevoked || c.ID == g.ID
		if c.ID == dev.ID {
			t.Fatal("a live grant was claimed for its summary")
		}
	}
	if !gotRevoked {
		t.Fatalf("the revoked grant was not claimed: %+v", claimed)
	}
	again, err := grants.ClaimEndedSupportGrants(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range again {
		if c.ID == g.ID {
			t.Fatal("a grant was claimed for its summary twice")
		}
	}

	// Requests: three open at most, decided once.
	var requests []state.SupportGrantRequest
	for range api.SupportGrantMaxOpenRequests {
		req, err := grants.CreateSupportGrantRequest(ctx, state.NewSupportGrantRequest{OrgID: f.org.ID, OperatorID: operator.Account.ID, Reason: "your build fails",
			AppIDs: []string{f.app.ID}, TTL: 2 * time.Hour, ExtendsGrantID: dev.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(req.AppIDs) != 1 || req.AppIDs[0] != f.app.ID || req.TTL != 2*time.Hour || req.ExtendsGrantID != dev.ID {
			t.Fatalf("request = %+v, want its apps, time and extended grant", req)
		}
		requests = append(requests, req)
	}
	if _, err := grants.CreateSupportGrantRequest(ctx, state.NewSupportGrantRequest{OrgID: f.org.ID, OperatorID: operator.Account.ID, Reason: "too short", TTL: time.Minute}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("one-minute request = %v, want ErrInvalidArgument", err)
	}
	if _, err := grants.CreateSupportGrantRequest(ctx, state.NewSupportGrantRequest{OrgID: f.org.ID, OperatorID: operator.Account.ID, Reason: "one more"}); !errors.Is(err, state.ErrSupportRequestLimit) {
		t.Fatalf("fourth open request = %v, want ErrSupportRequestLimit", err)
	}
	approved, err := grants.ApproveSupportGrantRequest(ctx, requests[0].ID, state.NewSupportGrant{OrgID: f.org.ID, CreatedBy: f.owner.ID, Reason: "your build fails", TTL: time.Hour})
	if err != nil || approved.RequestID != requests[0].ID {
		t.Fatalf("approve = %+v, %v", approved, err)
	}
	if _, err := grants.ApproveSupportGrantRequest(ctx, requests[0].ID, state.NewSupportGrant{OrgID: f.org.ID, CreatedBy: f.owner.ID, Reason: "again", TTL: time.Hour}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second approval = %v, want ErrNotFound", err)
	}
	if err := grants.DeclineSupportGrantRequest(ctx, f.org.ID, requests[1].ID, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	listed, err := grants.ListSupportGrantRequests(ctx, f.org.ID)
	if err != nil || len(listed) != 3 {
		t.Fatalf("requests = %d, %v", len(listed), err)
	}
	for _, l := range listed {
		if l.TTL != 2*time.Hour || len(l.AppIDs) != 1 || l.ExtendsGrantID != dev.ID {
			t.Fatalf("listed request = %+v, want its apps, time and extended grant", l)
		}
	}
	live, err := grants.ListLiveSupportGrants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range live {
		found = found || l.ID == approved.ID
		if l.ID == g.ID {
			t.Fatal("a revoked grant is listed as live")
		}
	}
	if !found {
		t.Fatal("the approved grant is not listed as live")
	}

	// Concurrent requests never pass the open-request cap.
	busy, err := store.CreateOrg(ctx, state.Org{Slug: "support-busy", Name: "Busy", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	created := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := grants.CreateSupportGrantRequest(ctx, state.NewSupportGrantRequest{OrgID: busy.ID, OperatorID: operator.Account.ID, Reason: "concurrent ask"}); err == nil {
				mu.Lock()
				created++
				mu.Unlock()
			} else if !errors.Is(err, state.ErrSupportRequestLimit) {
				t.Errorf("concurrent request = %v", err)
			}
		}()
	}
	wg.Wait()
	if created != api.SupportGrantMaxOpenRequests {
		t.Fatalf("concurrent requests created %d, want %d", created, api.SupportGrantMaxOpenRequests)
	}
}

// Reasons are measured in characters, like the tables' char_length CHECKs.
func TestValidSupportReasonCountsCharacters(t *testing.T) {
	for reason, want := range map[string]bool{
		"ab":                     false,
		"修复":                     false, // 6 bytes, 2 characters
		"修复了":                    true,
		strings.Repeat("修", 400): true, // 1200 bytes
		strings.Repeat("a", 401): false,
	} {
		if got := state.ValidSupportReason(reason); got != want {
			t.Errorf("ValidSupportReason(%d chars) = %v, want %v", len([]rune(reason)), got, want)
		}
	}
}

func TestMemSupportGrantLifecycle(t *testing.T) {
	supportGrantLifecycle(t, state.NewMemStore())
}
