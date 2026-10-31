package appaccess

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Account views that name an app outside its URL (dashboard widgets,
// incidents) read the principal from the request's context and apply the
// inventories' rule: a creator who left the company, or a key bound to
// another company, no longer sees the company's app.
func TestVisibleAppsContextFollowsOrganizationAccess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		removed bool
		key     func(org, other state.Org) *state.APIKey
		want    bool
	}{
		{name: "current member's session", want: true},
		{name: "key bound to the company", key: func(org, _ state.Org) *state.APIKey { return &state.APIKey{ID: "k1", OrgID: org.ID} }, want: true},
		{name: "creator who left the company", removed: true},
		{name: "key bound to another company", key: func(_, other state.Org) *state.APIKey { return &state.APIKey{ID: "k2", OrgID: other.ID} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, creator, org := creatorFixture(t, state.OrgRoleDeveloper)
			other, err := store.CreateOrg(t.Context(), state.Org{Slug: "other-co", Name: "Other", Plan: api.PlanPro})
			if err != nil {
				t.Fatal(err)
			}
			if tc.removed {
				if err := store.RemoveOrgMember(t.Context(), org.ID, creator.ID); err != nil {
					t.Fatal(err)
				}
			}
			app, err := store.AppBySlug(t.Context(), "team-api")
			if err != nil {
				t.Fatal(err)
			}
			var key *state.APIKey
			if tc.key != nil {
				key = tc.key(org, other)
			}
			ctx := authmw.WithPrincipal(context.Background(), creator, key, nil)
			visible, err := VisibleAppsContext(ctx, store, creator, []state.App{app})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(visible) == 1; got != tc.want {
				t.Fatalf("visible = %v, want %v", got, tc.want)
			}
		})
	}
}
