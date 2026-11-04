package accountexport_test

// adr: 202610171905

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"

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

// uuidStrictStore answers AppByID the way Postgres does: a reference that is
// not a UUID is a syntax error, not "not found" (Hunt 14 H14-120).
type uuidStrictStore struct {
	mem *state.MemStore
}

func (s uuidStrictStore) AppByID(ctx context.Context, id string) (state.App, error) {
	if uuid.Validate(id) != nil {
		return state.App{}, fmt.Errorf("ERROR: invalid input syntax for type uuid: %q (SQLSTATE 22P02)", id)
	}
	return s.mem.AppByID(ctx, id)
}

func (s uuidStrictStore) ProjectByID(ctx context.Context, id string) (state.Project, error) {
	return s.mem.ProjectByID(ctx, id)
}

func (s uuidStrictStore) ProjectBySlug(ctx context.Context, accountID, slug string) (state.Project, error) {
	return s.mem.ProjectBySlug(ctx, accountID, slug)
}

func (s uuidStrictStore) OrgByID(ctx context.Context, id string) (state.Org, error) {
	return s.mem.OrgByID(ctx, id)
}

func TestAuditExportsResolveAppSlugReferences(t *testing.T) {
	store := state.NewMemStore()
	identity, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: "export-slug@example.com", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	company, err := store.CreateOrg(t.Context(), state.Org{Slug: "export-slug-company", Name: "Company"})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := store.CreateApp(t.Context(), state.App{AccountID: identity.Account.ID, Slug: "web", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: identity.Account.ID, OrgID: company.ID, Slug: "shared", Type: state.AppTypeApp}); err != nil {
		t.Fatal(err)
	}
	_, scope := accountexport.WithScope(t.Context(), uuidStrictStore{mem: store}, identity.Account.ID)
	apps, err := store.ListApps(t.Context(), identity.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range apps {
		if _, err := scope.AppRow(t.Context(), app); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		data map[string]any
		want bool
	}{
		{"freeze on a personal app slug", map[string]any{"freeze": "f1", "app": "web", "kind": "deploy"}, true},
		{"approval policy on a company app slug", map[string]any{"app": "shared", "required_approvals": 1}, false},
		{"slug of an app outside the export", map[string]any{"app": "deleted-app"}, false},
		{"account-wide freeze", map[string]any{"freeze": "f2", "app": ""}, true},
		{"personal app by ID", map[string]any{"app": personal.ID}, true},
		{"app_id that is not a UUID", map[string]any{"app_id": "web"}, false},
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
