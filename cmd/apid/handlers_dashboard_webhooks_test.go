package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestParseAppWebhooksPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want string
		ok   bool
	}{
		{name: "bare", path: "demo/webhooks", want: "demo", ok: true},
		{name: "trailing slash", path: "demo/webhooks/", want: "demo", ok: true},
		{name: "nested", path: "demo/other/webhooks", ok: false},
		{name: "missing slug", path: "/webhooks", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseAppWebhooksPath(tc.path)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("parseAppWebhooksPath(%q) = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestDashboardHandler_AppWebhooks(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanPro); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "hooks-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	webhook, err := store.CreateAppWebhook(t.Context(), state.AppWebhook{
		AccountID: acct.ID, AppID: app.ID, TargetURL: "https://example.com/events", SecretSealed: []byte("sealed"),
		EventFilter: []string{"app.deployed"}, RetryPolicy: state.AppWebhookRetryDefault, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAppWebhook: %v", err)
	}
	delivery, err := store.RecordAppWebhookDelivery(t.Context(), state.AppWebhookDelivery{
		WebhookID: webhook.ID, AppID: app.ID, AccountID: acct.ID, Event: state.AppWebhookEventAppDeployed,
		Status: state.AppWebhookDeliveryDead, Attempt: 7, LastError: "receiver returned 503", LastResponseCode: 503,
		NextAttemptAt: time.Now(), Payload: json.RawMessage(`{"deployment_id":"redacted-from-page"}`),
	})
	if err != nil {
		t.Fatalf("RecordAppWebhookDelivery: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/hooks-app/webhooks", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		"Webhooks for", "https://example.com/events", "app.deployed", "receiver returned 503", "Retry", "Rotate secret", "Create webhook", delivery.ID,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q\n%s", want, rec.Body.String())
		}
	}
	if strings.Contains(rec.Body.String(), "redacted-from-page") {
		t.Error("delivery payload leaked into dashboard page")
	}
	if cookie := findDashboardCookie(rec.Result().Cookies(), dashboardWebhooksCSRFCookie); cookie == nil || cookie.Value == "" {
		t.Fatalf("GET webhooks: missing %s cookie", dashboardWebhooksCSRFCookie)
	}
}

func TestDashboardAppWebhookCreateRequiresNamedCSRF(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanPro); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "hooks-create", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := dashboardPOST(t, h, cookie, "/dashboard/apps/hooks-create/webhooks", map[string]string{
		"target_url": "https://example.com/events", "webhook_secret": "secret", "enabled": "true",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing csrf status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), middleware.FormFieldName) && !strings.Contains(rec.Body.String(), "csrf") {
		t.Fatalf("missing csrf problem: %s", rec.Body.String())
	}
}
