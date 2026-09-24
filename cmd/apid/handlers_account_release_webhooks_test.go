package main

// ADR-224: account receivers are usable through the public API, without
// exposing app subscriptions or another tenant's delivery history.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const accountReleaseWebhooksPath = "/v1/account/release-webhooks"

func accountReleaseWebhookReq() api.CreateAccountReleaseWebhookRequest {
	return api.CreateAccountReleaseWebhookRequest{
		TargetURL: "https://example.com/account-releases", WebhookSecret: "account-secret",
		EventFilter: []string{"deployment.live", "rollout.aborted"},
	}
}

func createAccountReleaseWebhookForTest(t *testing.T, e testEnv) api.AccountReleaseWebhookResponse {
	t.Helper()
	rec := e.do(t, http.MethodPost, accountReleaseWebhooksPath, accountReleaseWebhookReq(), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", rec.Code, rec.Body)
	}
	var out api.AccountReleaseWebhookResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAccountReleaseWebhooks_CRUDAndScope(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	appID := mustSeedApp(t, e, "account-release-crud")
	appHook := mustCreateWebhook(t, e, "account-release-crud", webhookReq())
	created := createAccountReleaseWebhookForTest(t, e)
	if created.ID == "" || created.Scope != "account" || created.AccountID != e.acct.ID || created.WebhookSecretSealedMasked != "***" {
		t.Fatalf("created response = %+v", created)
	}
	if strings.Contains(string(e.do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+created.ID, nil, nil).Body.Bytes()), "account-secret") || created.DeliveryFormat != "json" || created.RetryPolicy != "default" {
		t.Fatalf("unexpected defaults or secret in response: %+v", created)
	}
	row, err := e.store.AppWebhookByID(context.Background(), created.ID)
	if err != nil || row.Scope != state.AppWebhookScopeAccount || row.AppID != "" || strings.Contains(string(row.SecretSealed), "account-secret") {
		t.Fatalf("stored account subscription = %+v, %v", row, err)
	}
	list := e.do(t, http.MethodGet, accountReleaseWebhooksPath, nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", list.Code, list.Body)
	}
	var listed []api.AccountReleaseWebhookResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("account list = %+v, %v", listed, err)
	}
	if got := e.do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+appHook.ID, nil, nil); got.Code != http.StatusNotFound {
		t.Fatalf("app subscription exposed on account route: %d %s", got.Code, got.Body)
	}
	if got := e.do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+created.ID, nil, nil); got.Code != http.StatusOK {
		t.Fatalf("get status %d: %s", got.Code, got.Body)
	}

	newFilter := []string{"deployment.failed"}
	newURL := "https://example.com/account-releases-updated"
	updated := e.do(t, http.MethodPatch, accountReleaseWebhooksPath+"/"+created.ID,
		api.UpdateAccountReleaseWebhookRequest{TargetURL: &newURL, EventFilter: &newFilter}, nil)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status %d: %s", updated.Code, updated.Body)
	}
	var updateOut api.AccountReleaseWebhookResponse
	if err := json.Unmarshal(updated.Body.Bytes(), &updateOut); err != nil || updateOut.TargetURL != newURL ||
		len(updateOut.EventFilter) != 1 || updateOut.EventFilter[0] != "deployment.failed" {
		t.Fatalf("updated response = %+v, %v", updateOut, err)
	}
	rotate := e.do(t, http.MethodPost, accountReleaseWebhooksPath+"/"+created.ID+"/rotate-secret",
		api.RotateAppWebhookSecretRequest{WebhookSecret: "replacement-secret"}, nil)
	if rotate.Code != http.StatusOK || strings.Contains(rotate.Body.String(), "replacement-secret") {
		t.Fatalf("rotate status %d: %s", rotate.Code, rotate.Body)
	}
	row, err = e.store.AppWebhookByID(context.Background(), created.ID)
	if err != nil || strings.Contains(string(row.SecretSealed), "replacement-secret") {
		t.Fatalf("rotation stored plaintext: %v", err)
	}

	// Account deliveries retain the source app ID and can be retried through
	// the account route without knowing an app slug.
	delivery, err := e.store.RecordAppWebhookDelivery(context.Background(), state.AppWebhookDelivery{
		WebhookID: created.ID, AppID: appID, AccountID: e.acct.ID,
		Event: state.AppWebhookEventDeploymentFailed, Payload: json.RawMessage(`{"app_id":"` + appID + `"}`),
		Status: state.AppWebhookDeliveryDead, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	deliveries := e.do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+created.ID+"/deliveries", nil, nil)
	if deliveries.Code != http.StatusOK || !strings.Contains(deliveries.Body.String(), delivery.ID) || !strings.Contains(deliveries.Body.String(), appID) {
		t.Fatalf("deliveries status %d: %s", deliveries.Code, deliveries.Body)
	}
	retry := e.do(t, http.MethodPost, accountReleaseWebhooksPath+"/"+created.ID+"/deliveries/"+delivery.ID+"/retry", nil, nil)
	if retry.Code != http.StatusOK || !strings.Contains(retry.Body.String(), "pending") {
		t.Fatalf("retry status %d: %s", retry.Code, retry.Body)
	}
	if got := e.do(t, http.MethodDelete, accountReleaseWebhooksPath+"/"+created.ID, nil, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete status %d: %s", got.Code, got.Body)
	}
	if got := e.do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+created.ID, nil, nil); got.Code != http.StatusNotFound {
		t.Fatalf("deleted webhook remains visible: %d %s", got.Code, got.Body)
	}
}

func TestAccountReleaseWebhooks_ValidationAndIsolation(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	for _, events := range [][]string{nil, {}, {"app.parked"}, {"deployment.live", "deployment.live"}} {
		req := accountReleaseWebhookReq()
		req.EventFilter = events
		rec := e.do(t, http.MethodPost, accountReleaseWebhooksPath, req, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("filter %v: status %d: %s", events, rec.Code, rec.Body)
		}
	}
	created := createAccountReleaseWebhookForTest(t, e)
	empty := []string{}
	if got := e.do(t, http.MethodPatch, accountReleaseWebhooksPath+"/"+created.ID,
		api.UpdateAccountReleaseWebhookRequest{EventFilter: &empty}, nil); got.Code != http.StatusBadRequest {
		t.Fatalf("empty update filter status %d: %s", got.Code, got.Body)
	}
	other, err := e.store.CreateAccount(context.Background(), "foreign-release-hook@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := e.store.CreateAccountReleaseWebhookIfUnderQuota(context.Background(), state.AppWebhook{
		AccountID: other.ID, Scope: state.AppWebhookScopeAccount, TargetURL: "https://example.com/foreign-release",
		SecretSealed: []byte("sealed"), EventFilter: []string{"deployment.live"}, Enabled: true,
	}, api.Limits{WebhookPerAccount: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		var body any
		if method == http.MethodPatch {
			body = api.UpdateAccountReleaseWebhookRequest{}
		}
		got := e.do(t, method, accountReleaseWebhooksPath+"/"+foreign.ID, body, nil)
		if got.Code != http.StatusNotFound {
			t.Errorf("%s foreign webhook: %d %s", method, got.Code, got.Body)
		}
	}
}

func TestAccountReleaseWebhooks_FreePlanDenied(t *testing.T) {
	e := setupWebhookTest(t, api.PlanFree)
	if got := e.do(t, http.MethodPost, accountReleaseWebhooksPath, accountReleaseWebhookReq(), nil); got.Code != http.StatusPaymentRequired {
		t.Fatalf("free create status %d: %s", got.Code, got.Body)
	}
}
