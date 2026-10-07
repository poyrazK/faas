package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestApplicationStandardVersionAPI(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "standards-org", "Standards", api.PlanPro)
	base := "/v1/orgs/" + org.Slug + "/application-standards"
	request := api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"require_signed":{"mode":"mandatory","value":true}}`)}
	publish := e.do(t, http.MethodPost, base+"/production-baseline/versions", request, nil)
	if publish.Code != http.StatusCreated {
		t.Fatalf("publish: %d %s", publish.Code, publish.Body)
	}
	var version api.ApplicationStandardVersion
	if err := json.Unmarshal(publish.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	if version.OrgID != org.ID || version.Version != 1 {
		t.Fatalf("version %+v", version)
	}
	stale := e.do(t, http.MethodPost, base+"/production-baseline/versions", request, nil)
	assertProblem(t, stale, http.StatusConflict, "application_standard_version_stale")
	get := e.do(t, http.MethodGet, base+"/production-baseline?version=1", nil, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get: %d %s", get.Code, get.Body)
	}
	list := e.do(t, http.MethodGet, base+"?limit=1", nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body)
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?after=bad%2Fslug"} {
		assertProblem(t, e.do(t, http.MethodGet, base+query, nil, nil), http.StatusBadRequest, api.CodeValidation)
	}
	other := seedSharedOrgWithOwner(t, e, "other-standards", "Other standards", api.PlanPro)
	assertProblem(t, e.do(t, http.MethodGet, "/v1/orgs/"+other.Slug+"/application-standards/production-baseline", nil, nil), http.StatusNotFound, api.CodeNotFound)
}

func TestApplicationStandardDeveloperCanInspectButCannotPublish(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "standards-roles", "Standards roles", api.PlanPro)
	developer, err := e.store.CreateAccount(context.Background(), "standard-dev@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.AddOrgMember(context.Background(), org.ID, developer.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(context.Background(), developer.ID, hash, "developer", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	dev := e
	dev.acct, dev.key = developer, plain
	base := "/v1/orgs/" + org.Slug + "/application-standards"
	get := dev.do(t, http.MethodGet, base, nil, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("developer inspect %d %s", get.Code, get.Body)
	}
	request := api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"require_signed":{"mode":"mandatory","value":true}}`)}
	assertProblem(t, dev.do(t, http.MethodPost, base+"/production-baseline/versions", request, nil), http.StatusForbidden, api.CodeOrgRoleForbidden)
}
