package projectaccess

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// ValidateScan binds an uploaded plan to the addressed organization/project.
// Legacy collection uploads remain personal; company uploads use explicit paths.
func ValidateScan(r *http.Request, store Store, account state.Account, slug string) *api.Problem {
	if grant, ok := From(r.Context()); ok {
		if grant.Project.ID != "" && slug != grant.Project.Slug {
			return api.ErrValidation("project_slug must match the project in the request path")
		}
		project, err := store.ProjectByOrgSlug(r.Context(), grant.Org.ID, slug)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return lookupProblem(err)
		}
		if err == nil && project.AccountID != account.ID {
			return api.NewProblem(http.StatusConflict, api.CodeConflict, "Project path required", "use the organization's project-specific scan or deploy URL for this existing project")
		}
		return nil
	}
	project, err := store.ProjectBySlug(r.Context(), account.ID, slug)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return lookupProblem(err)
	}
	if project.OrgID == "" {
		return nil
	}
	org, err := store.OrgByID(r.Context(), project.OrgID)
	if err != nil {
		return lookupProblem(err)
	}
	if !org.Personal {
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Organization project path required", "scan and deploy company projects through /v1/orgs/{organization}/projects/{slug}/scan or /deploy")
	}
	return nil
}

// Visible filters creator-scoped inventories, including dashboard lists, after
// offboarding. Membership and key binding are re-read on each request.
func Visible(ctx context.Context, store Store, actor state.Account, key *state.APIKey, project state.Project) (bool, error) {
	if project.OrgID == "" {
		if project.AccountID != actor.ID || key != nil && key.AppID != "" {
			return false, nil
		}
		if key != nil && key.OrgID != "" {
			org, err := store.OrgByID(ctx, key.OrgID)
			if err != nil {
				return false, err
			}
			return org.Personal && org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == actor.ID, nil
		}
		return true, nil
	}
	org, err := store.OrgByID(ctx, project.OrgID)
	if err != nil {
		return false, err
	}
	if org.Personal {
		return project.AccountID == actor.ID && (key == nil || key.AppID == "" && (key.OrgID == "" || key.OrgID == org.ID)), nil
	}
	if org.DeletedPending || key != nil && (key.AppID != "" || key.OrgID != "" && key.OrgID != org.ID || key.OrgID == "" && actor.ID != project.AccountID) {
		return false, nil
	}
	member, err := store.OrgMemberByAccount(ctx, org.ID, actor.ID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return member.RemovedAt == nil && authz.Can(member.Role, authz.OrgActionReadProject), nil
}

// FilterScanApps keeps the scan's blast-radius preview within the granted org.
// Unscoped scans cannot reveal company resources in their Unaffected output.
func FilterScanApps(ctx context.Context, apps []state.App) []state.App {
	grant, shared := From(ctx)
	result := make([]state.App, 0, len(apps))
	for _, app := range apps {
		if shared && app.OrgID == grant.Org.ID {
			result = append(result, app)
		}
	}
	return result
}

func Credential(r *http.Request) *state.APIKey {
	_, key, _ := authmw.AccountFromContext(r)
	return key
}

func OrganizationID(ctx context.Context) string {
	grant, _ := From(ctx)
	return grant.Org.ID
}

// Apps resolves the tenant inventory for admission and preview. A namespace may
// contain legacy apps from different companies; it is never an access boundary.
func Apps(ctx context.Context, store interface {
	ListApps(context.Context, string) ([]state.App, error)
}, accountID string) ([]state.App, error) {
	if grant, shared := From(ctx); shared {
		reader, ok := store.(interface {
			ListAppsByOrg(context.Context, string) ([]state.App, error)
		})
		if !ok {
			return nil, errors.New("organization app inventory unavailable")
		}
		return reader.ListAppsByOrg(ctx, grant.Org.ID)
	}
	return store.ListApps(ctx, accountID)
}

func ValidateEnvironmentCreation(r *http.Request, req api.CreateProjectEnvironmentRequest) *api.Problem {
	if _, shared := From(r.Context()); !shared {
		return nil
	}
	if req.Full || req.FromEnvironment != "" {
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Organization environment cloning unavailable", "create an empty environment and configure its resources separately")
	}
	_, _, member, ok := authmw.PrincipalFrom(r)
	if req.Protected != nil && *req.Protected && (!ok || member == nil || !authz.Can(member.Role, authz.OrgActionManageProjectSecurity)) {
		return api.ErrOrgRoleForbidden("create a protected project environment")
	}
	return nil
}
