package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Alert rules and tenant surfaces named by id under /v1/apps/{slug}/ are
// served only for the {slug} app. An organization's company apps and their
// creator's personal apps share AccountID, so the old account-only match
// let a removed creator, or a key bound to one app, read, rewire or delete
// another app's rule or surface through any slug it could still use.
func TestAppNestedIDRoutesStayOnTheirApp(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	e := apidserver.Setup(t, api.PlanScale)
	store, acct := *e.Store(), *e.Acct()
	ctx := t.Context()
	newApp := func(slug string) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	newApp("mine")
	other := newApp("other")
	rule := func(appID string) state.AlertRule {
		row, err := store.CreateAlertRule(ctx, state.AlertRule{
			AccountID: acct.ID, AppID: appID, Name: "rule " + appID, Enabled: true,
			Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt, Threshold: 5, WindowSpec: state.AlertWindow5m,
			WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("ciphertext-not-real"),
			CooldownMinutes: api.AlertRuleDefaultCooldownMinutes,
		})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	otherRule, accountWide := rule(other.ID), rule("")
	limits, _ := api.LimitsFor(acct.Plan)
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: acct.ID, AppID: other.ID, Name: "other-surface", CertKind: state.CertKindPerHostSAN}, limits)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/v1/apps/mine/alerts/" + otherRule.ID, nil},
		{http.MethodGet, "/v1/apps/mine/alerts/" + otherRule.ID + "/deliveries", nil},
		{http.MethodPatch, "/v1/apps/mine/alerts/" + otherRule.ID, map[string]any{"webhook_url": "https://attacker.example/hook"}},
		{http.MethodPost, "/v1/apps/mine/alerts/" + otherRule.ID + "/rotate-secret", map[string]any{"webhook_secret": "attacker"}},
		{http.MethodDelete, "/v1/apps/mine/alerts/" + otherRule.ID, nil},
		{http.MethodGet, "/v1/apps/mine/tenant-surfaces/" + surface.ID, nil},
		{http.MethodPost, "/v1/apps/mine/tenant-surfaces/" + surface.ID + "/hostnames", map[string]any{"hostname": "shop.attacker.example"}},
		{http.MethodDelete, "/v1/apps/mine/tenant-surfaces/" + surface.ID, nil},
	} {
		if rec := e.Do(t, tc.method, tc.path, tc.body, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404: %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	if got, err := store.AlertRuleByID(ctx, otherRule.ID); err != nil || got.WebhookURL != "https://example.com/hook" {
		t.Fatalf("other app's rule after refused writes = %+v, %v", got, err)
	}
	if got, err := store.GetTenantSurfaceByID(ctx, surface.ID); err != nil || got.Status == state.SurfaceStatusDeleted {
		t.Fatalf("other app's surface after refused delete = %+v, %v", got, err)
	}

	// Each app still reaches its own rows, and account-wide rules stay
	// visible from any app, as the app's rule list shows them.
	for _, path := range []string{
		"/v1/apps/other/alerts/" + otherRule.ID,
		"/v1/apps/mine/alerts/" + accountWide.ID,
		"/v1/apps/other/tenant-surfaces/" + surface.ID,
	} {
		if rec := e.Do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200: %s", path, rec.Code, rec.Body.String())
		}
	}
}
