package accountexport_test

// adr: 202610171905

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/accountexport"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAuditExportsExcludeCompanyOrganizationReferences(t *testing.T) {
	store := state.NewMemStore()
	identity, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: "export-scope@example.com", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	company, err := store.CreateOrg(t.Context(), state.Org{Slug: "export-scope-company", Name: "Company"})
	if err != nil {
		t.Fatal(err)
	}
	companyProject, err := store.CreateProject(t.Context(), state.Project{AccountID: identity.Account.ID, OrgID: company.ID, Slug: "company-project"})
	if err != nil {
		t.Fatal(err)
	}
	personalProject, err := store.CreateProject(t.Context(), state.Project{AccountID: identity.Account.ID, Slug: "personal-project"})
	if err != nil {
		t.Fatal(err)
	}
	_, scope := accountexport.WithScope(t.Context(), store, identity.Account.ID)
	for _, tc := range []struct {
		name string
		data any
		want bool
	}{
		{"company project event without org ID", map[string]any{"project_id": companyProject.ID, "detail": "company environment"}, false},
		{"legacy company project slug", map[string]any{"project_slug": companyProject.Slug, "environment": "production"}, false},
		{"personal project event", map[string]any{"project_id": personalProject.ID}, true},
		{"personal project slug", map[string]any{"project_slug": personalProject.Slug}, true},
		{"malformed project reference", map[string]any{"project_id": []string{companyProject.ID}}, false},
		{"company event without app ID", map[string]any{"org_id": company.ID, "detail": "company configuration"}, false},
		{"personal organization event", map[string]any{"org_id": identity.PersonalOrg.ID, "detail": "personal configuration"}, true},
		{"unknown organization", map[string]any{"org_id": "00000000-0000-4000-8000-000000000001"}, false},
		{"malformed organization reference", map[string]any{"org_id": map[string]any{"id": company.ID}}, false},
		{"malformed app reference", map[string]any{"app_id": []string{"app"}}, false},
		{"historical scalar event", "personal legacy activity", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			allowed, err := scope.Audit(t.Context(), data)
			if err != nil || allowed != tc.want {
				t.Fatalf("audit export allowed = %v, %v; want %v", allowed, err, tc.want)
			}
		})
	}
}
