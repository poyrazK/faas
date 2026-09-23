package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentAliasesCRUD(t *testing.T) {
	e := setup(t, api.PlanPro)
	first := mustSeedDeployment(t, e, "alias-app")
	app, err := e.store.AppBySlug(t.Context(), "alias-app")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.store.CreateDeployment(t.Context(), state.Deployment{
		AppID: app.ID, Status: state.DeployLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	other := mustSeedDeployment(t, e, "other-alias-app")

	put := e.do(t, http.MethodPut, "/v1/apps/alias-app/deployment-aliases/candidate",
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

	put = e.do(t, http.MethodPut, "/v1/apps/alias-app/deployment-aliases/candidate",
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

	list := e.do(t, http.MethodGet, "/v1/apps/alias-app/deployment-aliases", nil, nil)
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

	wrongApp := e.do(t, http.MethodPut, "/v1/apps/alias-app/deployment-aliases/candidate",
		api.SetDeploymentAliasRequest{DeploymentID: other.ID}, nil)
	if wrongApp.Code != http.StatusNotFound {
		t.Fatalf("cross-app target status=%d, want 404; body=%s", wrongApp.Code, wrongApp.Body.String())
	}
	badName := e.do(t, http.MethodPut, "/v1/apps/alias-app/deployment-aliases/Bad_Name",
		api.SetDeploymentAliasRequest{DeploymentID: first.ID}, nil)
	if badName.Code != http.StatusBadRequest {
		t.Fatalf("invalid alias status=%d, want 400; body=%s", badName.Code, badName.Body.String())
	}

	deleted := e.do(t, http.MethodDelete, "/v1/apps/alias-app/deployment-aliases/candidate", nil, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE status=%d, want 204; body=%s", deleted.Code, deleted.Body.String())
	}
	missing := e.do(t, http.MethodDelete, "/v1/apps/alias-app/deployment-aliases/candidate", nil, nil)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "404") {
		t.Fatalf("missing DELETE status=%d, want 404; body=%s", missing.Code, missing.Body.String())
	}
}
