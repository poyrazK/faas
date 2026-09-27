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

func TestStandaloneOutboundBindingsCreateAndPatch(t *testing.T) {
	e := setup(t, api.PlanHobby)
	declared := api.ServiceBindingPolicyDeclared
	targets := []string{" Identity ", "BILLING", "billing"}
	rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: "frontend", ServiceBindingTargets: &targets, ServiceBindingPolicy: &declared,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	want := []api.AppServiceBinding{
		{Binding: "GREGALE_SERVICE_BILLING_URL", Service: "billing"},
		{Binding: "GREGALE_SERVICE_IDENTITY_URL", Service: "identity"},
	}
	assert := func(policy api.ServiceBindingPolicy, bindings []api.AppServiceBinding) {
		t.Helper()
		app, err := e.store.AppBySlug(t.Context(), "frontend")
		if err != nil {
			t.Fatal(err)
		}
		if got := app.Manifest.EffectiveServiceBindingPolicy(); got != policy {
			t.Fatalf("stored policy = %q, want %q", got, policy)
		}
		if !reflect.DeepEqual(app.Manifest.ServiceBindings, bindings) {
			t.Fatalf("stored bindings = %#v, want %#v", app.Manifest.ServiceBindings, bindings)
		}
		for _, binding := range bindings {
			if got := app.Manifest.Env[binding.Binding]; got != "http://"+binding.Service+".svc.gregale:10080" {
				t.Fatalf("%s = %q", binding.Binding, got)
			}
			if got := app.Manifest.Env[api.ServiceBindingHTTPSEnvKey(binding.Service)]; got != "https://"+binding.Service+".internal" {
				t.Fatalf("%s = %q", api.ServiceBindingHTTPSEnvKey(binding.Service), got)
			}
		}
		if len(app.Manifest.Env) != 2*len(bindings) {
			t.Fatalf("stale service env: %#v", app.Manifest.Env)
		}
		read := e.do(t, http.MethodGet, "/v1/apps/frontend", nil, nil)
		if read.Code != http.StatusOK {
			t.Fatalf("read: %d %s", read.Code, read.Body)
		}
		var response api.AppResponse
		if err := json.Unmarshal(read.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.ServiceBindingPolicy != policy || !reflect.DeepEqual(response.ServiceBindings, bindings) {
			t.Fatalf("readback policy/bindings = %q/%#v", response.ServiceBindingPolicy, response.ServiceBindings)
		}
		if response.ServiceBindingTransport != api.ServiceBindingTransportHTTP {
			t.Fatalf("readback transport = %q, want legacy http", response.ServiceBindingTransport)
		}
	}
	assert(declared, want)

	rec = e.do(t, http.MethodPatch, "/v1/apps/frontend", api.UpdateAppRequest{}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("omitted patch: %d %s", rec.Code, rec.Body)
	}
	assert(declared, want)

	replacement := []string{"email"}
	rec = e.do(t, http.MethodPatch, "/v1/apps/frontend", api.UpdateAppRequest{ServiceBindingTargets: &replacement}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace bindings: %d %s", rec.Code, rec.Body)
	}
	assert(declared, []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_EMAIL_URL", Service: "email"}})

	empty := []string{}
	rec = e.do(t, http.MethodPatch, "/v1/apps/frontend", api.UpdateAppRequest{ServiceBindingTargets: &empty}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear bindings: %d %s", rec.Code, rec.Body)
	}
	assert(declared, nil)

	account := api.ServiceBindingPolicyAccount
	rec = e.do(t, http.MethodPatch, "/v1/apps/frontend", api.UpdateAppRequest{ServiceBindingPolicy: &account}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore account policy: %d %s", rec.Code, rec.Body)
	}
	assert(account, nil)
}

func TestStandaloneServiceBindingHTTPSFirstTransportCanBeChanged(t *testing.T) {
	e := setup(t, api.PlanHobby)
	targets := []string{"billing"}
	https := api.ServiceBindingTransportHTTPS
	rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: "frontend", ServiceBindingTargets: &targets, ServiceBindingTransport: &https,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	assertTransport := func(want api.ServiceBindingTransport, wantURL string) {
		t.Helper()
		app, err := e.store.AppBySlug(t.Context(), "frontend")
		if err != nil {
			t.Fatal(err)
		}
		if app.Manifest.EffectiveServiceBindingTransport() != want {
			t.Fatalf("stored transport = %q, want %q", app.Manifest.EffectiveServiceBindingTransport(), want)
		}
		if got := app.Manifest.Env["GREGALE_SERVICE_BILLING_URL"]; got != wantURL {
			t.Fatalf("canonical URL = %q, want %q", got, wantURL)
		}
		if got := app.Manifest.Env["GREGALE_SERVICE_BILLING_HTTPS_URL"]; got != "https://billing.internal" {
			t.Fatalf("HTTPS alias = %q", got)
		}
		read := e.do(t, http.MethodGet, "/v1/apps/frontend", nil, nil)
		var response api.AppResponse
		if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &response) != nil || response.ServiceBindingTransport != want {
			t.Fatalf("readback transport = %q, status=%d body=%s", response.ServiceBindingTransport, read.Code, read.Body)
		}
	}
	assertTransport(api.ServiceBindingTransportHTTPS, "https://billing.internal")

	httpTransport := api.ServiceBindingTransportHTTP
	rec = e.do(t, http.MethodPatch, "/v1/apps/frontend", api.UpdateAppRequest{ServiceBindingTransport: &httpTransport}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("change transport: %d %s", rec.Code, rec.Body)
	}
	assertTransport(api.ServiceBindingTransportHTTP, "http://billing.svc.gregale:10080")

	bad := api.ServiceBindingTransport("opportunistic")
	rec = e.do(t, http.MethodPatch, "/v1/apps/frontend", api.UpdateAppRequest{ServiceBindingTransport: &bad}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid transport: %d %s", rec.Code, rec.Body)
	}
	assertTransport(api.ServiceBindingTransportHTTP, "http://billing.svc.gregale:10080")
}

func TestStandaloneOutboundBindingsAuditOldAndNew(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "audit-frontend"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	targets := []string{"billing"}
	declared := api.ServiceBindingPolicyDeclared
	rec = e.do(t, http.MethodPatch, "/v1/apps/audit-frontend", api.UpdateAppRequest{
		ServiceBindingTargets: &targets, ServiceBindingPolicy: &declared,
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
	if string(data.Old["service_binding_policy"]) != `"account"` || string(data.New["service_binding_policy"]) != `"declared"` {
		t.Fatalf("audit policy old=%s new=%s", data.Old["service_binding_policy"], data.New["service_binding_policy"])
	}
	var bindings []api.AppServiceBinding
	if err := json.Unmarshal(data.New["service_bindings"], &bindings); err != nil || !reflect.DeepEqual(bindings, []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_BILLING_URL", Service: "billing"}}) {
		t.Fatalf("audit bindings = %#v, %v", bindings, err)
	}
}

func TestStandaloneOutboundBindingsDefaultAndRejectInvalidOrSourceOwned(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "frontend"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed app: %d %s", rec.Code, rec.Body)
	}
	app, err := e.store.AppBySlug(t.Context(), "frontend")
	if err != nil || app.Manifest.EffectiveServiceBindingPolicy() != api.ServiceBindingPolicyAccount || len(app.Manifest.ServiceBindings) != 0 {
		t.Fatalf("default manifest = %+v, %v", app.Manifest, err)
	}
	for _, targets := range [][]string{{"../escape"}, {"frontend"}} {
		rec = e.do(t, http.MethodPatch, "/v1/apps/frontend", api.UpdateAppRequest{ServiceBindingTargets: &targets}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid targets %v: %d %s", targets, rec.Code, rec.Body)
		}
	}
	tooMany := make([]string, api.ServiceBindingTargetsMax+1)
	for i := range tooMany {
		tooMany[i] = "billing"
	}
	rec = e.do(t, http.MethodPatch, "/v1/apps/frontend", api.UpdateAppRequest{ServiceBindingTargets: &tooMany}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-limit targets: %d %s", rec.Code, rec.Body)
	}
	badPolicy := api.ServiceBindingPolicy("all")
	rec = e.do(t, http.MethodPatch, "/v1/apps/frontend", api.UpdateAppRequest{ServiceBindingPolicy: &badPolicy}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid policy: %d %s", rec.Code, rec.Body)
	}
	app, err = e.store.AppBySlug(t.Context(), "frontend")
	if err != nil || len(app.Manifest.ServiceBindings) != 0 || app.Manifest.ServiceBindingPolicy != "" {
		t.Fatalf("invalid patches changed manifest = %+v, %v", app.Manifest, err)
	}

	project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "project"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "project-frontend", WorkloadName: "frontend", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	targets := []string{"billing"}
	rec = e.do(t, http.MethodPatch, "/v1/apps/project-frontend", api.UpdateAppRequest{ServiceBindingTargets: &targets}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("project patch: %d %s", rec.Code, rec.Body)
	}
	https := api.ServiceBindingTransportHTTPS
	rec = e.do(t, http.MethodPatch, "/v1/apps/project-frontend", api.UpdateAppRequest{ServiceBindingTransport: &https}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("project transport patch: %d %s", rec.Code, rec.Body)
	}
	_, err = e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "pr-1-frontend", PreviewOfSlug: "frontend", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	declared := api.ServiceBindingPolicyDeclared
	rec = e.do(t, http.MethodPatch, "/v1/apps/pr-1-frontend", api.UpdateAppRequest{ServiceBindingPolicy: &declared}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("preview patch: %d %s", rec.Code, rec.Body)
	}
}
