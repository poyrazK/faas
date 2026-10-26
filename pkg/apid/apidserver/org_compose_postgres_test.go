package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261026151530: an organization-owned project provides a Compose
// postgres service as an organization database when the caller may create
// and bind one; a key without postgres:manage gets the permission reason.
func TestOrgProjectComposePostgresPermission(t *testing.T) {
	env := apidserver.NewSourceRefTestServer(t, api.PlanPro, "compose-org-db", 9002)
	ctx := t.Context()
	store := *env.Store()
	actor, err := store.AccountByID(ctx, *env.AcctID())
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "compose-database-company", Name: "Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, actor.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_compose_db"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_compose_db"); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: org.ID, OrgID: org.ID, Slug: "compose-project"})
	if err != nil {
		t.Fatal(err)
	}
	apidserver.AttachTestManagedPostgres(t, *env.Srv(), 3)
	_, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	deployOnly, err := store.CreateOrgAPIKey(ctx, org.ID, actor.ID, hash, "deploy only", []string{api.ScopeDeployWrite}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		key          *state.APIKey
		wantProvided bool
		wantReason   string
	}{
		{"owner session", nil, true, ""},
		{"deploy-only key", &deployOnly, false, "permission"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := func(w http.ResponseWriter, r *http.Request, owner state.Account) {
				provided, reason, err := apidserver.ComposePostgresPlanFor(*env.Srv(), r.Context(), owner)
				if err != nil || provided != tc.wantProvided || reason != tc.wantReason {
					t.Errorf("provided = %t, reason = %q, err = %v; want %t, %q", provided, reason, err, tc.wantProvided, tc.wantReason)
				}
				w.WriteHeader(http.StatusOK)
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+org.Slug+"/projects/"+project.Slug+"/deploy", nil)
			request.Pattern = "POST /v1/orgs/{organization}/projects/{slug}/deploy"
			request.SetPathValue("organization", org.Slug)
			request.SetPathValue("slug", project.Slug)
			request = request.WithContext(authmw.WithPrincipal(ctx, actor, tc.key, nil))
			response := httptest.NewRecorder()
			projectaccess.Wrap(store, nil, next)(response, request, actor)
			if response.Code != http.StatusOK {
				t.Fatalf("project access: %d %s", response.Code, response.Body)
			}
		})
	}
}
