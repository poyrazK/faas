package apidserver_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"testing"
)

func TestVerificationRunListAPIReadGatesAndValidation(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, acct := *e.Store(), *e.Acct()
	ctx := t.Context()
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "list-receipts", Type: state.AppTypeFunction})
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "list-receipts"})
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"/v1/apps/" + app.Slug + "/bindings/verification-runs", "/v1/projects/" + project.Slug + "/environments/production/release-sets/verification-runs"}
	for _, path := range paths {
		r := e.Do(t, http.MethodGet, path, nil, nil)
		if r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("read %d %s", r.Code, r.Body.String())
		}
		var page api.BindingVerificationRunList
		if err = json.Unmarshal(r.Body.Bytes(), &page); err != nil || page.Runs == nil || len(page.Runs) != 0 || page.ObservedAt.IsZero() {
			t.Fatalf("empty page %+v %v", page, err)
		}
		for _, query := range []string{"?limit=0", "?limit=101", "?limit=1&limit=2", "?cursor=bad", "?deployment_id=bad", "?status=blocked", "?unknown=true"} {
			r = e.Do(t, http.MethodGet, path+query, nil, nil)
			if r.Code != 400 {
				t.Fatalf("invalid query accepted %s %d %s", query, r.Code, r.Body.String())
			}
		}
	}
	r := e.Do(t, http.MethodGet, paths[1]+"?include_members=true", nil, nil)
	if r.Code != 400 {
		t.Fatal("graph member flag accepted")
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateAPIKey(ctx, acct.ID, hash, "write-only", []string{api.ScopeDeployWrite})
	if err != nil {
		t.Fatal(err)
	}
	*e.Key() = plain
	for _, path := range paths {
		r = e.Do(t, http.MethodGet, path, nil, nil)
		if r.Code != 403 {
			t.Fatalf("write-only listed receipts: %d", r.Code)
		}
	}
}
