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

func TestCreateApp_ContainerRuntimeContract(t *testing.T) {
	for _, typ := range []string{"", "app"} {
		t.Run("type="+typ, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			for _, runtime := range []string{"go124", "node22", "invalid"} {
				rec := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "container-test", Type: typ, Runtime: runtime}, nil)
				apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
				if !strings.Contains(rec.Body.String(), "omit runtime") {
					t.Fatalf("missing recovery guidance: %s", rec.Body)
				}
			}
			// A rejected create must not consume the slug or quota.
			rec := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "container-test", Type: typ}, nil)
			if rec.Code != http.StatusCreated {
				t.Fatalf("container without runtime: %d %s", rec.Code, rec.Body)
			}
			var app api.AppResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil {
				t.Fatal(err)
			}
			if app.Type != "app" || app.Runtime != "" {
				t.Fatalf("container contract: type=%q runtime=%q", app.Type, app.Runtime)
			}
		})
	}
}

func TestCreateAppExplicitProject(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	p, err := (*e.Store()).CreateProject(t.Context(), state.Project{
		AccountID: e.Acct().ID, Slug: "my-project", ScanSource: state.ProjectScanSourceSingle,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]string{"slug": "my-api", "project_id": p.ID}
	rec := e.Do(t, http.MethodPost, "/v1/apps", request, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create under project: %d %s", rec.Code, rec.Body)
	}
	var app api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil || app.ProjectID != p.ID {
		t.Fatalf("response project = %q, error = %v", app.ProjectID, err)
	}
	workloads, err := (*e.Store()).AppsForProject(t.Context(), e.Acct().ID, p.ID)
	if err != nil || len(workloads) != 1 || workloads[0].WorkloadName != app.Slug {
		t.Fatalf("project workloads: %+v, error = %v", workloads, err)
	}
	linked, err := (*e.Store()).CreateProject(t.Context(), state.Project{
		AccountID: e.Acct().ID, Slug: "linked", RepoFullName: "demo/linked", ScanSource: state.ProjectScanSourceSingle,
	})
	if err != nil {
		t.Fatal(err)
	}
	request["project_id"] = linked.ID
	request["slug"] = "linked-api"
	rec = e.Do(t, http.MethodPost, "/v1/apps", request, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("repository-managed project: %d %s", rec.Code, rec.Body)
	}
	request["project_id"] = "11111111-1111-4111-8111-111111111111"
	request["slug"] = "orphan-api"
	rec = e.Do(t, http.MethodPost, "/v1/apps", request, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing project: %d %s", rec.Code, rec.Body)
	}
}
