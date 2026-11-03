package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A webhook target can carry the receiver's credential in its path (a
// Zapier catch hook), so only callers who may change the webhook see it in
// full, as for log drains (bug hunt 36).
func TestWebhookTargetHiddenFromReadOnlyCallers(t *testing.T) {
	const target = "https://hooks.zapier.example/hooks/catch/123/s3cr3t/"
	const masked = "https://hooks.zapier.example/***"
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	creator := *e.Acct()
	webhook := func(appID string) state.AppWebhook {
		t.Helper()
		row, err := store.CreateAppWebhook(t.Context(), state.AppWebhook{AccountID: creator.ID, AppID: appID, TargetURL: target,
			SecretSealed: []byte("sealed"), EventFilter: []string{"app.deployed"}, RetryPolicy: state.AppWebhookRetryDefault, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	personalApp, err := store.CreateApp(t.Context(), state.App{AccountID: creator.ID, Slug: "personal-hooks", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	personal := webhook(personalApp.ID)
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), creator.ID, hash, "read-only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	readOnly := map[string]string{"Authorization": "Bearer " + plain}

	org := apidserver.SeedSharedOrgWithOwner(t, e, "hook-targets", "Hook targets", api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "company-hooks"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create company app = %d %s", r.Code, r.Body)
	}
	companyApp, err := store.AppBySlug(t.Context(), "company-hooks")
	if err != nil {
		t.Fatal(err)
	}
	shared := webhook(companyApp.ID)
	_, asAdmin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, asViewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)

	for _, c := range []struct {
		name    string
		slug    string
		id      string
		headers map[string]string
		want    string
	}{
		{"personal app, admin key", "personal-hooks", personal.ID, nil, target},
		{"personal app, apps:read key", "personal-hooks", personal.ID, readOnly, masked},
		{"company app, admin", "company-hooks", shared.ID, asAdmin, target},
		{"company app, viewer", "company-hooks", shared.ID, asViewer, masked},
	} {
		base := "/v1/apps/" + c.slug + "/webhooks"
		r := e.Do(t, http.MethodGet, base, nil, c.headers)
		var listed []api.AppWebhookResponse
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &listed) != nil || len(listed) != 1 || listed[0].TargetURL != c.want {
			t.Fatalf("%s: list = %d %s, want target %q", c.name, r.Code, r.Body, c.want)
		}
		r = e.Do(t, http.MethodGet, base+"/"+c.id, nil, c.headers)
		var got api.AppWebhookResponse
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &got) != nil || got.TargetURL != c.want {
			t.Fatalf("%s: get = %d %s, want target %q", c.name, r.Code, r.Body, c.want)
		}
	}
}
