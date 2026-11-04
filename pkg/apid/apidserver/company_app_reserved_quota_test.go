package apidserver_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A company app keeps its creator's account_id, but the reserved-tier cap
// belongs to the app's billing subject (ADR-202610171904). Once its company
// adopted billing, a company's reserved apps count across all of its apps,
// whoever created them, and never spend the creator's personal allowance.
// Until then the company keeps its existing policy: its creator's company
// apps share the creator's allowance.
func TestCompanyReservedAppsShareTheBillingSubjectsCap(t *testing.T) {
	for _, adopted := range []bool{true, false} {
		t.Run(fmt.Sprintf("adopted=%v", adopted), func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanHobby)
			ctx, store, creator := t.Context(), *e.Store(), *e.Acct()
			// A Free organization starts with billing adopted, and keeps it on
			// an upgrade; a paid one awaits adoption.
			plan := api.PlanHobby
			if adopted {
				plan = api.PlanFree
			}
			org, err := store.CreateOrg(ctx, state.Org{Slug: "reserved-co", Name: "Reserved Co", Plan: plan})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateOrgPlan(ctx, org.ID, api.PlanHobby); err != nil {
				t.Fatal(err)
			}
			member, err := store.CreateAccount(ctx, "member@reserved.example", api.PlanHobby)
			if err != nil {
				t.Fatal(err)
			}
			for id, role := range map[string]state.OrgRole{creator.ID: state.OrgRoleOwner, member.ID: state.OrgRoleDeveloper} {
				if err := store.AddOrgMember(ctx, org.ID, id, role, nil); err != nil {
					t.Fatal(err)
				}
			}
			plain, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateOrgAPIKey(ctx, org.ID, creator.ID, hash, "owner", api.ScopesAdminOnly, nil); err != nil {
				t.Fatal(err)
			}
			asOwner := map[string]string{"Authorization": "Bearer " + plain}
			for _, app := range []state.App{
				{AccountID: creator.ID, Slug: "personal-app"},
				{AccountID: creator.ID, OrgID: org.ID, Slug: "company-app"},
				{AccountID: member.ID, OrgID: org.ID, Slug: "member-app"},
			} {
				app.Type, app.Status, app.RAMMB, app.MaxConcurrency, app.IdleTimeoutS = state.AppTypeApp, state.AppActive, 256, 1, 60
				if _, err := store.CreateApp(ctx, app); err != nil {
					t.Fatal(err)
				}
			}

			// Hobby reserves one app per billing subject.
			reserved := string(api.EvictionPriorityReserved)
			for _, step := range []struct {
				slug    string
				headers map[string]string
				refused bool
			}{
				{"company-app", asOwner, false},
				// Adopted: the company holds its reservation and the
				// creator's is free. Unadopted: the creator holds theirs and
				// the member's is free.
				{"personal-app", nil, !adopted},
				{"member-app", asOwner, adopted},
			} {
				res := e.Do(t, http.MethodPatch, "/v1/apps/"+step.slug, api.UpdateAppRequest{EvictionPriority: &reserved}, step.headers)
				refused := res.Code == http.StatusUnprocessableEntity && strings.Contains(res.Body.String(), api.CodePlanEvictionPriorityReservedQuota)
				if refused != step.refused || !step.refused && res.Code != http.StatusOK {
					t.Fatalf("reserve %s: %d %s, want refused=%v", step.slug, res.Code, res.Body, step.refused)
				}
			}
		})
	}
}
