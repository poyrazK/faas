package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrgOffboardingCreatorInventories(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "offboard-team", "Offboard Team", api.PlanPro)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, OrgID: org.ID, Slug: "company-api"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateBuild(t.Context(), dep.ID, state.DeploymentKindDockerfile, 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "personal-api"}); err != nil {
		t.Fatal(err)
	}
	newOwner, _ := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	if err := store.TransferOrgOwnership(t.Context(), org.ID, (*e.Acct()).ID, newOwner.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(t.Context(), org.ID, (*e.Acct()).ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/apps", "/v1/deployments", "/v1/builds"} {
		t.Run(path, func(t *testing.T) {
			rec := e.DoAdmin(t, "GET", path, nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("inventory: %d %s", rec.Code, rec.Body)
			}
			if strings.Contains(rec.Body.String(), app.ID) || strings.Contains(rec.Body.String(), dep.ID) || strings.Contains(rec.Body.String(), "company-api") {
				t.Fatalf("removed creator sees company resources: %s", rec.Body)
			}
			if path == "/v1/apps" {
				var apps []api.AppResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &apps); err != nil || len(apps) != 1 || apps[0].Slug != "personal-api" {
					t.Fatalf("personal inventory lost: %+v %v", apps, err)
				}
			}
		})
	}
}
