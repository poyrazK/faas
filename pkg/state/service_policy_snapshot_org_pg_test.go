//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 23 H23-1: the pinned service-policy view read apps without org_id, so
// SameAppResourceOwner compared only creators there. Every service call is
// authorized from this view, and apps one person created in two orgs, or in
// an org and their personal account, authorized each other.
func TestPgServicePolicySnapshotCarriesTheOrg(t *testing.T) {
	s, ctx, _ := pgWithPool(t)
	member, err := s.CreateAccount(ctx, "policy-org-member@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	colleague, err := s.CreateAccount(ctx, "policy-org-colleague@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	orgA, err := s.CreateOrg(ctx, state.Org{Slug: "policy-org-a", Name: "Policy org A", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	orgB, err := s.CreateOrg(ctx, state.Org{Slug: "policy-org-b", Name: "Policy org B", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	apps := map[string]state.App{}
	for name, app := range map[string]state.App{
		"personal":       {AccountID: member.ID, Slug: "policy-personal"},
		"org-a":          {AccountID: member.ID, OrgID: orgA.ID, Slug: "policy-org-a-api"},
		"org-a-coworker": {AccountID: colleague.ID, OrgID: orgA.ID, Slug: "policy-org-a-worker"},
		"org-b":          {AccountID: member.ID, OrgID: orgB.ID, Slug: "policy-org-b-api"},
	} {
		app.RAMMB = 128
		if apps[name], err = s.CreateApp(ctx, app); err != nil {
			t.Fatal(name, err)
		}
	}
	err = s.WithServicePolicySnapshot(ctx, func(reader state.ServicePolicyReader) error {
		pinned := map[string]state.App{}
		for name, app := range apps {
			full, err := s.AppByID(ctx, app.ID)
			if err != nil {
				return err
			}
			byID, err := reader.AppByID(ctx, app.ID)
			if err != nil {
				return err
			}
			bySlug, err := reader.AppBySlug(ctx, app.Slug)
			if err != nil {
				return err
			}
			if full.OrgID == "" || byID.OrgID != full.OrgID || bySlug.OrgID != full.OrgID {
				t.Errorf("%s: pinned org by id %q, by slug %q, want %q", name, byID.OrgID, bySlug.OrgID, full.OrgID)
			}
			pinned[name] = byID
		}
		for _, tc := range []struct {
			caller, target string
			same           bool
		}{
			{"personal", "org-a", false},
			{"org-a", "personal", false},
			{"org-a", "org-b", false},
			{"org-a", "org-a-coworker", true},
		} {
			if got := state.SameAppResourceOwner(pinned[tc.caller], pinned[tc.target]); got != tc.same {
				t.Errorf("pinned SameAppResourceOwner(%s, %s) = %v, want %v", tc.caller, tc.target, got, tc.same)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
