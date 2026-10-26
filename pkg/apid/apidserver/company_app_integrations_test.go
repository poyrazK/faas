package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A company app's alert rules, log drains and webhooks belong to the
// organization: its admins manage them without the app's creator, each row
// records who created it, and the creator's own account-wide rules and
// notification channels stay the creator's. Viewers read but do not change.
//
// adr: 20261026151103
func TestOrgGovernsCompanyAppIntegrations(t *testing.T) {
	useTestSealRecipient(t)
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ctx := t.Context()
	creator := *e.Acct()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "integrations", "Integrations", api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "company-api"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create company app = %d %s", r.Code, r.Body)
	}
	admin, asAdmin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, asViewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	personal, err := store.CreateAlertRule(ctx, state.AlertRule{AccountID: creator.ID, Name: "my spend", Enabled: true,
		Metric: state.AlertMetricAccountSpendEUR, Comparison: state.AlertGt, Threshold: 100, WindowSpec: state.AlertWindow5m,
		Action: state.AlertActionWebhook, WebhookURL: "https://me.example/hook", WebhookSecretSealed: []byte("sealed"), CooldownMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}

	created := func(path string, body any, headers map[string]string) string {
		t.Helper()
		r := e.Do(t, http.MethodPost, path, body, headers)
		var out struct {
			ID string `json:"id"`
		}
		if r.Code != http.StatusCreated || json.Unmarshal(r.Body.Bytes(), &out) != nil || out.ID == "" {
			t.Fatalf("POST %s = %d %s", path, r.Code, r.Body)
		}
		return out.ID
	}
	rule := alertRuleRequest()
	ruleID := created("/v1/apps/company-api/alerts", rule, asAdmin)
	drainID := created("/v1/apps/company-api/log-drains", api.CreateAppLogDrainRequest{Kind: "http_json", TargetURL: "https://logs.example.com/ingest"}, asAdmin)
	hookID := created("/v1/apps/company-api/webhooks", webhookReq(), asAdmin)
	if row, err := store.AlertRuleByID(ctx, ruleID); err != nil || row.CreatedByAccountID != admin.ID || row.AccountID != creator.ID {
		t.Fatalf("admin's rule = %+v, %v", row, err)
	}
	if row, err := store.AppLogDrainByID(ctx, drainID); err != nil || row.AccountID != creator.ID {
		t.Fatalf("admin's drain = %+v, %v", row, err)
	}
	if _, err := store.AppWebhookByID(ctx, hookID); err != nil {
		t.Fatalf("admin's webhook: %v", err)
	}

	listed := func(headers map[string]string) map[string]bool {
		t.Helper()
		r := e.Do(t, http.MethodGet, "/v1/apps/company-api/alerts", nil, headers)
		var rules []api.AlertRuleResponse
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &rules) != nil {
			t.Fatalf("list alerts = %d %s", r.Code, r.Body)
		}
		ids := map[string]bool{}
		for _, rule := range rules {
			ids[rule.ID] = true
		}
		return ids
	}
	if got := listed(asAdmin); !got[ruleID] || got[personal.ID] {
		t.Fatalf("admin's list = %v, want the company rule and not the creator's own", got)
	}
	if got := listed(nil); !got[ruleID] || !got[personal.ID] {
		t.Fatalf("creator's list = %v, want both", got)
	}
	if r := e.Do(t, http.MethodGet, "/v1/apps/company-api/alerts/"+personal.ID, nil, asAdmin); r.Code != http.StatusNotFound {
		t.Fatalf("admin reading the creator's own rule = %d", r.Code)
	}
	withChannel := alertRuleRequest()
	withChannel.Name, withChannel.WebhookURL, withChannel.WebhookSecret, withChannel.ChannelIDs = "channel", "", "", []string{uuid.NewString()}
	if r := e.Do(t, http.MethodPost, "/v1/apps/company-api/alerts", withChannel, asAdmin); r.Code != http.StatusForbidden {
		t.Fatalf("admin binding the creator's channels = %d %s", r.Code, r.Body)
	}

	if got := listed(asViewer); !got[ruleID] {
		t.Fatalf("viewer's list = %v", got)
	}
	viewerRule := alertRuleRequest()
	viewerRule.Name = "viewer"
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/v1/apps/company-api/alerts"},
		{http.MethodDelete, "/v1/apps/company-api/log-drains/" + drainID},
		{http.MethodDelete, "/v1/apps/company-api/webhooks/" + hookID},
	} {
		if r := e.Do(t, c.method, c.path, viewerRule, asViewer); r.Code != http.StatusForbidden {
			t.Fatalf("viewer %s %s = %d %s", c.method, c.path, r.Code, r.Body)
		}
	}
	for _, path := range []string{"/v1/apps/company-api/webhooks/" + hookID, "/v1/apps/company-api/log-drains/" + drainID, "/v1/apps/company-api/alerts/" + ruleID} {
		if r := e.Do(t, http.MethodDelete, path, nil, asAdmin); r.Code != http.StatusNoContent {
			t.Fatalf("admin DELETE %s = %d %s", path, r.Code, r.Body)
		}
	}
}

// When the creator of a company app leaves, the webhook they set up on it
// stops, an admin's stays, and the removal's activity entry tells the
// organization what was paused.
//
// adr: 20261026151103
func TestDepartedCreatorsCompanyIntegrationsPause(t *testing.T) {
	useTestSealRecipient(t)
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ctx := t.Context()
	creator := *e.Acct()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "departure", "Departure", api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "departure-api"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create company app = %d %s", r.Code, r.Body)
	}
	admin, asAdmin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	hookOf := func(headers map[string]string) string {
		t.Helper()
		req := webhookReq()
		req.TargetURL = "https://example.com/hook-" + uuid.NewString()
		r := e.Do(t, http.MethodPost, "/v1/apps/departure-api/webhooks", req, headers)
		var out api.AppWebhookResponse
		if r.Code != http.StatusCreated || json.Unmarshal(r.Body.Bytes(), &out) != nil {
			t.Fatalf("create webhook = %d %s", r.Code, r.Body)
		}
		return out.ID
	}
	creatorHook, adminHook := hookOf(nil), hookOf(asAdmin)
	if err := store.TransferOrgOwnership(ctx, org.ID, creator.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/members/"+creator.ID, nil, asAdmin); r.Code >= 300 {
		t.Fatalf("remove creator = %d %s", r.Code, r.Body)
	}
	for id, want := range map[string]bool{creatorHook: false, adminHook: true} {
		if hook, err := store.AppWebhookByID(ctx, id); err != nil || hook.Enabled != want {
			t.Errorf("webhook %s enabled = %v (%v), want %v", id, hook.Enabled, err, want)
		}
	}
	r := e.Do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/activity?kind_prefix=org.member.removed", nil, asAdmin)
	var page api.ListOrgActivityResponse
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.Items) != 1 ||
		!strings.Contains(page.Items[0].Summary, "paused 1 alerts, log drains and webhooks") {
		t.Fatalf("removal activity = %d %s", r.Code, r.Body)
	}
}

// alertRuleRequest is a valid app alert rule with a webhook.
func alertRuleRequest() api.CreateAlertRuleRequest {
	return api.CreateAlertRuleRequest{Name: "p99 > 500ms", Metric: "latency_p99_ms", Comparison: "gt", Threshold: 500, WindowSpec: "5m",
		WebhookURL: "https://example.com/hook", WebhookSecret: "shh"}
}

// useTestSealRecipient lets the test seal webhook secrets: apid refuses to
// store one without the host's age recipient.
func useTestSealRecipient(t *testing.T) {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	previous := *apidserver.SetSecretRecipient
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = previous })
}
