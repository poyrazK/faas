package apidserver_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostingActivityFlagsRoutesAndCICredentialDenial(t *testing.T) {
	// adr: 202610171147 — operator listing grants no CI credential inventory authority.
	for _, value := range []string{"", "false", "true", "1"} {
		m := apidserver.NewRuntimeConfigManager(func(key string) string {
			if key == "FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED" {
				return value
			}
			return ""
		})
		if m.Bool("frontend_hosting_activity_enabled", false) != (value == "true" || value == "1") {
			t.Fatal(value)
		}
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "true")
	env := apidserver.Setup(t, api.PlanHobby)
	(*env.S()).WithAdminAllowlist(env.Acct().Email)
	values := map[string]string{"FAAS_FRONTEND_HOSTING_ENABLED": "true", "FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED": "true", "FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED": "true", "FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED": "true", "FAAS_FRONTEND_HOSTING_DOMAIN": "hosting.example", "FAAS_FRONTEND_HOSTING_R2_ENDPOINT": "https://example.r2.cloudflarestorage.com", "FAAS_FRONTEND_HOSTING_R2_BUCKET": "hosting", "FAAS_FRONTEND_HOSTING_R2_ACCESS_KEY": "test-key", "FAAS_FRONTEND_HOSTING_R2_SECRET_KEY": "test-secret", "FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID": strings.Repeat("a", 32), "FAAS_FRONTEND_HOSTING_KV_NAMESPACE_ID": strings.Repeat("b", 32), "FAAS_FRONTEND_HOSTING_CF_TOKEN": "test-token"}
	getenv := func(key string) string { return values[key] }
	if err := (*env.S()).ConfigureHostingForTest(getenv); err != nil {
		t.Fatal(err)
	}
	manager := apidserver.NewRuntimeConfigManager(getenv)
	(*env.S()).WithRuntimeConfigManager(manager)
	if _, err := (*env.Store()).CreateProject(t.Context(), state.Project{AccountID: env.Acct().ID, Slug: "activity"}); err != nil {
		t.Fatal(err)
	}
	created := env.Do(t, "POST", "/v1/internal/hosting/sites", api.CreateHostingSiteRequest{Project: "activity", Environment: "production", Name: "web"}, nil)
	var site api.HostingSite
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &site) != nil {
		t.Fatal(created.Code, created.Body)
	}
	root := "/v1/internal/hosting/sites/" + site.ID
	if got := env.Do(t, "GET", root+"/preview-capacity", nil, nil); got.Code != 503 {
		t.Fatal("capacity default off", got.Code, got.Body)
	}
	if err := manager.Apply("frontend_hosting_preview_reservations_enabled", []byte("true")); err != nil {
		t.Fatal(err)
	}
	if got := env.Do(t, "GET", root+"/preview-capacity", nil, nil); got.Code != 200 {
		t.Fatal("capacity route", got.Code, got.Body)
	}
	if got := env.Do(t, "GET", root+"/runs", nil, nil); got.Code != 503 {
		t.Fatal("default off", got.Code, got.Body)
	}
	if err := manager.Apply("frontend_hosting_activity_enabled", []byte("true")); err != nil {
		t.Fatal(err)
	}
	caps := env.Do(t, "GET", "/v1/internal/hosting/capabilities", nil, nil)
	var capabilities api.HostingCapabilities
	if caps.Code != 200 || json.Unmarshal(caps.Body.Bytes(), &capabilities) != nil || !capabilities.ActivityEnabled || capabilities.ActivityPageSize != api.HostingActivityPageSize {
		t.Fatal(caps.Code, caps.Body)
	}
	run, err := (*env.Store()).CreateHostingCIRun(t.Context(), env.Acct().ID, site.ID, uuid.NewString(), api.HostingCIRunRequest{Manifest: api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 1, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}}, PreviewTTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	got := env.Do(t, "GET", root+"/runs", nil, nil)
	var page api.HostingActivityPage
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &page) != nil || page.SiteID != site.ID || len(page.Entries) != 1 || page.Entries[0].RunID != run.ID || page.ServingHealth != "not_checked" || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(got.Code, got.Body)
	}
	if got := env.Do(t, "POST", root+"/ci/"+run.ID+"/cancel", nil, nil); got.Code != 503 {
		t.Fatal("cancellation default off", got.Code, got.Body)
	}
	if err := manager.Apply("frontend_hosting_cancellation_enabled", []byte("true")); err != nil {
		t.Fatal(err)
	}
	for _, ops := range [][]string{{"stage"}, {"preview"}, {"stage", "preview"}} {
		minted := env.Do(t, "POST", root+"/automation-credentials", api.CreateHostingAutomationCredentialRequest{Label: "activity", Operations: ops}, nil)
		var credential api.HostingAutomationCredentialResponse
		if minted.Code != 201 || json.Unmarshal(minted.Body.Bytes(), &credential) != nil {
			t.Fatal(minted.Code, minted.Body)
		}
		if got := env.Do(t, "GET", root+"/preview-capacity", nil, map[string]string{"Authorization": "Bearer " + credential.Token}); got.Code != 403 {
			t.Fatal("CI gained capacity inventory authority", got.Code, got.Body)
		}
		if got := env.Do(t, "POST", root+"/ci/"+run.ID+"/cancel", nil, map[string]string{"Authorization": "Bearer " + credential.Token}); got.Code != 403 {
			t.Fatal("CI gained cancellation authority", got.Code, got.Body)
		}
		if got := env.Do(t, "GET", root+"/runs", nil, map[string]string{"Authorization": "Bearer " + credential.Token}); got.Code != 403 {
			t.Fatal("CI gained listing authority", got.Code, got.Body)
		}
	}
	got = env.Do(t, "POST", root+"/ci/"+run.ID+"/cancel", nil, nil)
	var canceled api.HostingCIRun
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &canceled) != nil || !canceled.Canceled {
		t.Fatal("cancel route", got.Code, got.Body)
	}
	if err := manager.Apply("frontend_hosting_cancellation_enabled", []byte("false")); err != nil {
		t.Fatal(err)
	}
	if got := env.Do(t, "POST", root+"/ci/"+run.ID+"/cancel", nil, nil); got.Code != 503 {
		t.Fatal("hot-off cancellation replay", got.Code, got.Body)
	}
	if err := manager.Apply("frontend_hosting_activity_enabled", []byte("false")); err != nil {
		t.Fatal(err)
	}
	if got := env.Do(t, "GET", root+"/runs", nil, nil); got.Code != 503 {
		t.Fatal("hot disable", got.Code, got.Body)
	}
}

func TestHostingCancellationRuntimeSeed(t *testing.T) {
	for _, v := range []string{"", "false", "1", "true"} {
		m := apidserver.NewRuntimeConfigManager(func(k string) string {
			if k == "FAAS_FRONTEND_HOSTING_CANCELLATION_ENABLED" {
				return v
			}
			return ""
		})
		if m.Bool("frontend_hosting_cancellation_enabled", false) != (v == "1" || v == "true") {
			t.Fatal(v)
		}
	}
}
