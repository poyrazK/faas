package apidserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrgAppQuotaUsesWorkspacePlanAndAllCreators(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "workspace-quota", "Workspace Quota", api.PlanHobby)
	developer, headers := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	limits := api.MustLimitsFor(api.PlanHobby)
	for i := range limits.DeployedApps {
		accountID := developer.ID
		if i%2 == 1 {
			accountID = (*e.Acct()).ID
		}
		if _, err := store.CreateApp(t.Context(), state.App{AccountID: accountID, OrgID: org.ID, Slug: fmt.Sprintf("shared-quota-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	// The owner has a Pro personal plan; the workspace has a Hobby plan.
	response := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "company-quota-excess"}, nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("company cap: %d %s", response.Code, response.Body.String())
	}
	var problem struct {
		Code     string `json:"code"`
		Detail   string `json:"detail"`
		Limit    int    `json:"limit"`
		Observed int    `json:"observed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != api.CodePlanLimitApps || problem.Limit != limits.DeployedApps || problem.Observed != limits.DeployedApps {
		t.Fatalf("quota response uses creator's personal limits: %+v", problem)
	}
	accountResponse := e.Do(t, http.MethodGet, "/v1/account", nil, nil)
	var counts struct {
		AppCount *int `json:"app_count"`
	}
	if err := json.Unmarshal(accountResponse.Body.Bytes(), &counts); err != nil || accountResponse.Code != http.StatusOK || counts.AppCount == nil || *counts.AppCount != 0 {
		t.Fatalf("account quota display includes company apps: %s %v", accountResponse.Body.String(), err)
	}
	// A personal app still has its own pool, despite creating company apps.
	response = e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "separate-personal-quota"}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("personal pool: %d %s", response.Code, response.Body.String())
	}
	// Restoring an app created by another member also uses the company pool.
	app, err := store.AppBySlug(t.Context(), "shared-quota-0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: developer.ID, OrgID: org.ID, Slug: "fill-restoration-slot"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOrgMemberRole(t.Context(), org.ID, developer.ID, state.OrgRoleAdmin); err != nil {
		t.Fatal(err)
	}
	response = e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/restore", nil, headers)
	if response.Code != http.StatusForbidden {
		t.Fatalf("restore company pool: %d %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || problem.Code != api.CodePlanLimitApps || problem.Limit != limits.DeployedApps {
		t.Fatalf("restore did not enforce the organization budget: %+v %v", problem, err)
	}
}

func TestOrgAppQuotaDeletionRequiresStorageMigration(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "workspace-delete", "Workspace Delete", api.PlanPro)
	developer, headers := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: developer.ID, OrgID: org.ID, Slug: "company-survives-personal-delete"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(t.Context(), org.ID, developer.ID); err != nil {
		t.Fatal(err)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), developer.ID, hash, "personal deletion", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	headers["Authorization"] = "Bearer " + plain
	response := e.Do(t, http.MethodDelete, "/v1/account", nil, headers)
	if response.Code != http.StatusConflict {
		t.Fatalf("personal deletion erased company resources: %d %s", response.Code, response.Body.String())
	}
	if _, err := store.AppByID(t.Context(), app.ID); err != nil {
		t.Fatalf("company app erased: %v", err)
	}
	account, err := store.AccountByID(t.Context(), developer.ID)
	if err != nil || account.Status != state.AccountActive {
		t.Fatalf("blocked deletion changed account: %+v %v", account, err)
	}
}
