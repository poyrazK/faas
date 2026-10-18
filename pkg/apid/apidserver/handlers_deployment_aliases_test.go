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

func TestDeploymentAliasesCRUD(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	first := apidserver.MustSeedDeployment(t, e, "alias-app")
	app, err := (*e.Store()).AppBySlug(t.Context(), "alias-app")
	if err != nil {
		t.Fatal(err)
	}
	second, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{
		AppID: app.ID, Status: state.DeployLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	other := apidserver.MustSeedDeployment(t, e, "other-alias-app")

	put := e.Do(t, http.MethodPut, "/v1/apps/alias-app/deployment-aliases/candidate",
		api.SetDeploymentAliasRequest{DeploymentID: first.ID}, nil)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT status=%d, want 200; body=%s", put.Code, put.Body.String())
	}
	var alias api.DeploymentAliasResponse
	if err := json.Unmarshal(put.Body.Bytes(), &alias); err != nil {
		t.Fatal(err)
	}
	expectedLabel, ok := api.DeploymentAliasHostLabel(app.ID, "candidate")
	if !ok {
		t.Fatal("host label rejected valid alias")
	}
	if alias.Name != "candidate" || alias.DeploymentID != first.ID || alias.Revision != first.Revision ||
		alias.Host != expectedLabel+".gregale.dev" || alias.URL != "https://"+alias.Host {
		t.Fatalf("PUT response = %+v", alias)
	}

	put = e.Do(t, http.MethodPut, "/v1/apps/alias-app/deployment-aliases/candidate",
		api.SetDeploymentAliasRequest{DeploymentID: second.ID}, nil)
	if put.Code != http.StatusOK {
		t.Fatalf("update status=%d, want 200; body=%s", put.Code, put.Body.String())
	}
	if err := json.Unmarshal(put.Body.Bytes(), &alias); err != nil {
		t.Fatal(err)
	}
	if alias.DeploymentID != second.ID || alias.Revision != second.Revision {
		t.Fatalf("updated alias = %+v", alias)
	}

	list := e.Do(t, http.MethodGet, "/v1/apps/alias-app/deployment-aliases", nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("GET status=%d, want 200; body=%s", list.Code, list.Body.String())
	}
	var got api.DeploymentAliasListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].DeploymentID != second.ID || got.Items[0].Host != alias.Host {
		t.Fatalf("GET aliases = %+v", got.Items)
	}

	wrongApp := e.Do(t, http.MethodPut, "/v1/apps/alias-app/deployment-aliases/candidate",
		api.SetDeploymentAliasRequest{DeploymentID: other.ID}, nil)
	if wrongApp.Code != http.StatusNotFound {
		t.Fatalf("cross-app target status=%d, want 404; body=%s", wrongApp.Code, wrongApp.Body.String())
	}
	badName := e.Do(t, http.MethodPut, "/v1/apps/alias-app/deployment-aliases/Bad_Name",
		api.SetDeploymentAliasRequest{DeploymentID: first.ID}, nil)
	if badName.Code != http.StatusBadRequest {
		t.Fatalf("invalid alias status=%d, want 400; body=%s", badName.Code, badName.Body.String())
	}

	deleted := e.Do(t, http.MethodDelete, "/v1/apps/alias-app/deployment-aliases/candidate", nil, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE status=%d, want 204; body=%s", deleted.Code, deleted.Body.String())
	}
	missing := e.Do(t, http.MethodDelete, "/v1/apps/alias-app/deployment-aliases/candidate", nil, nil)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "404") {
		t.Fatalf("missing DELETE status=%d, want 404; body=%s", missing.Code, missing.Body.String())
	}
}

// TestDeploymentAliasSupersededTargetSaysWhy covers H10-61: pointing an alias
// at a superseded revision answered only "no routable deployment for this
// app", as if the revision did not exist.
func TestDeploymentAliasSupersededTargetSaysWhy(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedDeployment(t, e, "alias-old")
	app, err := (*e.Store()).AppBySlug(t.Context(), "alias-old")
	if err != nil {
		t.Fatal(err)
	}
	old, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeploySuperseded})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodPut, "/v1/apps/alias-old/deployment-aliases/pinned",
		api.SetDeploymentAliasRequest{DeploymentID: old.ID}, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	var problem struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(problem.Detail, "is superseded") || !strings.Contains(problem.Detail, "only at a live or in-progress deployment") {
		t.Fatalf("detail = %q, want the target's status and the alias rule", problem.Detail)
	}
	unknown := e.Do(t, http.MethodPut, "/v1/apps/alias-old/deployment-aliases/pinned",
		api.SetDeploymentAliasRequest{DeploymentID: "00000000-0000-0000-0000-000000000000"}, nil)
	if unknown.Code != http.StatusNotFound || !strings.Contains(unknown.Body.String(), "no deployment") {
		t.Fatalf("unknown target = %d %s", unknown.Code, unknown.Body.String())
	}
}
