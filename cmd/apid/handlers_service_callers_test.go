package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStandaloneAllowedServiceCallersCreateAndPatch(t *testing.T) {
	e := setup(t, api.PlanHobby)
	initial := []string{" Worker ", "FRONTEND", "frontend"}
	rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: "customer-billing", AllowedServiceCallers: &initial,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	assertCallers := func(want *[]string) {
		t.Helper()
		got, err := e.store.AppBySlug(t.Context(), "customer-billing")
		if err != nil {
			t.Fatal(err)
		}
		if (got.Manifest.AllowedServiceCallers == nil) != (want == nil) ||
			(got.Manifest.AllowedServiceCallers != nil && !reflect.DeepEqual(*got.Manifest.AllowedServiceCallers, *want)) {
			t.Fatalf("stored callers = %v, want %v", got.Manifest.AllowedServiceCallers, want)
		}
		read := e.do(t, http.MethodGet, "/v1/apps/customer-billing", nil, nil)
		if read.Code != http.StatusOK {
			t.Fatalf("read: %d %s", read.Code, read.Body)
		}
		var response api.AppResponse
		if err := json.Unmarshal(read.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if (response.AllowedServiceCallers == nil) != (want == nil) ||
			(response.AllowedServiceCallers != nil && !reflect.DeepEqual(*response.AllowedServiceCallers, *want)) {
			t.Fatalf("readback callers = %v, want %v", response.AllowedServiceCallers, want)
		}
	}
	normalized := []string{"frontend", "worker"}
	assertCallers(&normalized)
	denyAllAtCreate := []string{}
	rec = e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: "locked-service", AllowedServiceCallers: &denyAllAtCreate,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("deny-all create: %d %s", rec.Code, rec.Body)
	}
	locked, err := e.store.AppBySlug(t.Context(), "locked-service")
	if err != nil || locked.Manifest.AllowedServiceCallers == nil || len(*locked.Manifest.AllowedServiceCallers) != 0 {
		t.Fatalf("deny-all create policy = %v, %v", locked.Manifest.AllowedServiceCallers, err)
	}

	rec = e.do(t, http.MethodPatch, "/v1/apps/customer-billing", api.UpdateAppRequest{}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("omitted policy patch: %d %s", rec.Code, rec.Body)
	}
	assertCallers(&normalized)

	rec = e.do(t, http.MethodPatch, "/v1/apps/customer-billing", api.UpdateAppRequest{
		AllowedServiceCallers: json.RawMessage(`["identity"]`),
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace policy: %d %s", rec.Code, rec.Body)
	}
	identity := []string{"identity"}
	assertCallers(&identity)

	rec = e.do(t, http.MethodPatch, "/v1/apps/customer-billing", api.UpdateAppRequest{
		AllowedServiceCallers: json.RawMessage(`[]`),
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("deny all: %d %s", rec.Code, rec.Body)
	}
	empty := []string{}
	assertCallers(&empty)

	rec = e.do(t, http.MethodPatch, "/v1/apps/customer-billing", api.UpdateAppRequest{
		AllowedServiceCallers: json.RawMessage(`null`),
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore account policy: %d %s", rec.Code, rec.Body)
	}
	assertCallers(nil)
}

func TestStandaloneAllowedServiceCallersRejectsInvalidAndSourceOwned(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "standalone"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed app: %d %s", rec.Code, rec.Body)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`["../escape"]`), json.RawMessage(`123`)} {
		rec = e.do(t, http.MethodPatch, "/v1/apps/standalone", api.UpdateAppRequest{AllowedServiceCallers: raw}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid policy %s: %d %s", raw, rec.Code, rec.Body)
		}
	}
	tooMany := make([]string, api.AllowedServiceCallersMax+1)
	for i := range tooMany {
		tooMany[i] = "frontend"
	}
	tooManyJSON, err := json.Marshal(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodPatch, "/v1/apps/standalone", api.UpdateAppRequest{AllowedServiceCallers: tooManyJSON}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized policy: %d %s", rec.Code, rec.Body)
	}
	stored, err := e.store.AppBySlug(t.Context(), "standalone")
	if err != nil || stored.Manifest.AllowedServiceCallers != nil {
		t.Fatalf("invalid PATCH changed policy: %+v, %v", stored.Manifest, err)
	}
	invalidCreate := []string{"../escape"}
	rec = e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "invalid-callers", AllowedServiceCallers: &invalidCreate}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid create policy: %d %s", rec.Code, rec.Body)
	}

	project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "project"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.store.CreateApp(t.Context(), state.App{
		AccountID: e.acct.ID, ProjectID: project.ID, Slug: "project-billing", WorkloadName: "billing", Status: state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodPatch, "/v1/apps/project-billing", api.UpdateAppRequest{
		AllowedServiceCallers: json.RawMessage(`["frontend"]`),
	}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("project policy patch: %d %s", rec.Code, rec.Body)
	}
	projectApp, err := e.store.AppBySlug(t.Context(), "project-billing")
	if err != nil || projectApp.Manifest.AllowedServiceCallers != nil {
		t.Fatalf("project policy changed: %+v, %v", projectApp.Manifest, err)
	}
	_, err = e.store.CreateApp(t.Context(), state.App{
		AccountID: e.acct.ID, Slug: "pr-1-standalone", PreviewOfSlug: "standalone", Status: state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodPatch, "/v1/apps/pr-1-standalone", api.UpdateAppRequest{
		AllowedServiceCallers: json.RawMessage(`null`),
	}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("preview policy patch: %d %s", rec.Code, rec.Body)
	}
}

func TestStandaloneAllowedServiceCallersAuditsOldAndNew(t *testing.T) {
	e := setup(t, api.PlanPro)
	initial := []string{"frontend"}
	rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: "audit-billing", AllowedServiceCallers: &initial,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, http.MethodPatch, "/v1/apps/audit-billing", api.UpdateAppRequest{
		AllowedServiceCallers: json.RawMessage(`["identity"]`),
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	event := findEventByKind(rows, "app.updated")
	if event == nil {
		t.Fatal("app.updated event missing")
	}
	var data struct {
		Old map[string]json.RawMessage `json:"old"`
		New map[string]json.RawMessage `json:"new"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatal(err)
	}
	if string(data.Old["allowed_service_callers"]) != `["frontend"]` || string(data.New["allowed_service_callers"]) != `["identity"]` {
		t.Fatalf("audit policy old=%s new=%s", data.Old["allowed_service_callers"], data.New["allowed_service_callers"])
	}
}
