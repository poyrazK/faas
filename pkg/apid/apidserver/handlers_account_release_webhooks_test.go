package apidserver_test

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
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

const accountReleaseWebhooksPath = "/v1/account/release-webhooks"

func accountReleaseWebhookReq() api.CreateAccountReleaseWebhookRequest {
	return api.CreateAccountReleaseWebhookRequest{
		TargetURL: "https://example.com/account-releases", WebhookSecret: "account-secret",
		EventFilter: []string{"deployment.live", "rollout.aborted"},
	}
}

func createAccountReleaseWebhookForTest(t *testing.T, e apidserver.TestEnv) api.AccountReleaseWebhookResponse {
	t.Helper()
	rec := e.Do(t, http.MethodPost, accountReleaseWebhooksPath, accountReleaseWebhookReq(), nil)
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
	appID := apidserver.MustSeedApp(t, e, "account-release-crud")
	appHook := mustCreateWebhook(t, e, "account-release-crud", webhookReq())
	created := createAccountReleaseWebhookForTest(t, e)
	if created.ID == "" || created.Scope != "account" || created.AccountID != (*e.Acct()).ID || created.WebhookSecretSealedMasked != "***" {
		t.Fatalf("created response = %+v", created)
	}
	if strings.Contains(string(e.Do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+created.ID, nil, nil).Body.Bytes()), "account-secret") || created.DeliveryFormat != "json" || created.RetryPolicy != "default" {
		t.Fatalf("unexpected defaults or secret in response: %+v", created)
	}
	row, err := (*e.Store()).AppWebhookByID(context.Background(), created.ID)
	if err != nil || row.Scope != state.AppWebhookScopeAccount || row.AppID != "" || strings.Contains(string(row.SecretSealed), "account-secret") {
		t.Fatalf("stored account subscription = %+v, %v", row, err)
	}
	list := e.Do(t, http.MethodGet, accountReleaseWebhooksPath, nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", list.Code, list.Body)
	}
	var listed []api.AccountReleaseWebhookResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("account list = %+v, %v", listed, err)
	}
	if got := e.Do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+appHook.ID, nil, nil); got.Code != http.StatusNotFound {
		t.Fatalf("app subscription exposed on account route: %d %s", got.Code, got.Body)
	}
	healthRec := e.Do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+created.ID+"/health", nil, nil)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("account health status %d: %s", healthRec.Code, healthRec.Body)
	}
	var health api.AppWebhookDeliveryHealthResponse
	if err := json.Unmarshal(healthRec.Body.Bytes(), &health); err != nil || health.WebhookID != created.ID || health.PendingCount != 0 {
		t.Fatalf("account health = %+v, err=%v", health, err)
	}
	if got := e.Do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+appHook.ID+"/health", nil, nil); got.Code != http.StatusNotFound {
		t.Fatalf("app health exposed on account route: %d %s", got.Code, got.Body)
	}
	if got := e.Do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+created.ID, nil, nil); got.Code != http.StatusOK {
		t.Fatalf("get status %d: %s", got.Code, got.Body)
	}

	newFilter := []string{"deployment.failed"}
	newURL := "https://example.com/account-releases-updated"
	updated := e.Do(t, http.MethodPatch, accountReleaseWebhooksPath+"/"+created.ID,
		api.UpdateAccountReleaseWebhookRequest{TargetURL: &newURL, EventFilter: &newFilter}, nil)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status %d: %s", updated.Code, updated.Body)
	}
	var updateOut api.AccountReleaseWebhookResponse
	if err := json.Unmarshal(updated.Body.Bytes(), &updateOut); err != nil || updateOut.TargetURL != newURL ||
		len(updateOut.EventFilter) != 1 || updateOut.EventFilter[0] != "deployment.failed" {
		t.Fatalf("updated response = %+v, %v", updateOut, err)
	}
	rotate := e.Do(t, http.MethodPost, accountReleaseWebhooksPath+"/"+created.ID+"/rotate-secret",
		api.RotateAppWebhookSecretRequest{WebhookSecret: "replacement-secret"}, nil)
	if rotate.Code != http.StatusOK || strings.Contains(rotate.Body.String(), "replacement-secret") {
		t.Fatalf("rotate status %d: %s", rotate.Code, rotate.Body)
	}
	row, err = (*e.Store()).AppWebhookByID(context.Background(), created.ID)
	if err != nil || strings.Contains(string(row.SecretSealed), "replacement-secret") {
		t.Fatalf("rotation stored plaintext: %v", err)
	}

	// Account deliveries retain the source app ID and can be retried through
	// the account route without knowing an app slug.
	delivery, err := (*e.Store()).RecordAppWebhookDelivery(context.Background(), state.AppWebhookDelivery{
		WebhookID: created.ID, AppID: appID, AccountID: (*e.Acct()).ID,
		Event: state.AppWebhookEventDeploymentFailed, Payload: json.RawMessage(`{"app_id":"` + appID + `"}`),
		Status: state.AppWebhookDeliveryDead, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	deliveries := e.Do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+created.ID+"/deliveries", nil, nil)
	if deliveries.Code != http.StatusOK || !strings.Contains(deliveries.Body.String(), delivery.ID) || !strings.Contains(deliveries.Body.String(), appID) {
		t.Fatalf("deliveries status %d: %s", deliveries.Code, deliveries.Body)
	}
	retry := e.Do(t, http.MethodPost, accountReleaseWebhooksPath+"/"+created.ID+"/deliveries/"+delivery.ID+"/retry", nil, nil)
	if retry.Code != http.StatusOK || !strings.Contains(retry.Body.String(), "pending") {
		t.Fatalf("retry status %d: %s", retry.Code, retry.Body)
	}
	claimed, err := (*e.Store()).ClaimDueAppWebhookDeliveries(t.Context(), 1, time.Now().Add(time.Second))
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim replayed release delivery = %+v, err=%v", claimed, err)
	}
	if err := (*e.Store()).MarkAppWebhookDeliverySucceeded(t.Context(), delivery.ID, 204, claimed[0].Attempt, claimed[0].NextAttemptAt, time.Now()); err != nil {
		t.Fatal(err)
	}
	attempts := e.Do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+created.ID+"/deliveries/"+delivery.ID+"/attempts", nil, nil)
	if attempts.Code != http.StatusOK || !strings.Contains(attempts.Body.String(), `"replay_generation":1`) || !strings.Contains(attempts.Body.String(), `"response_code":204`) {
		t.Fatalf("release attempts status %d: %s", attempts.Code, attempts.Body)
	}
	if other := e.Do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+appHook.ID+"/deliveries/"+delivery.ID+"/attempts", nil, nil); other.Code != http.StatusNotFound {
		t.Errorf("app webhook on release attempt path = %d: %s", other.Code, other.Body)
	}
	if got := e.Do(t, http.MethodDelete, accountReleaseWebhooksPath+"/"+created.ID, nil, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete status %d: %s", got.Code, got.Body)
	}
	if got := e.Do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+created.ID, nil, nil); got.Code != http.StatusNotFound {
		t.Fatalf("deleted webhook remains visible: %d %s", got.Code, got.Body)
	}
}

func TestAccountReleaseWebhooks_ValidationAndIsolation(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	for _, events := range [][]string{nil, {}, {"app.parked"}, {"deployment.live", "deployment.live"}} {
		req := accountReleaseWebhookReq()
		req.EventFilter = events
		rec := e.Do(t, http.MethodPost, accountReleaseWebhooksPath, req, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("filter %v: status %d: %s", events, rec.Code, rec.Body)
		}
	}
	created := createAccountReleaseWebhookForTest(t, e)
	empty := []string{}
	if got := e.Do(t, http.MethodPatch, accountReleaseWebhooksPath+"/"+created.ID,
		api.UpdateAccountReleaseWebhookRequest{EventFilter: &empty}, nil); got.Code != http.StatusBadRequest {
		t.Fatalf("empty update filter status %d: %s", got.Code, got.Body)
	}
	other, err := (*e.Store()).CreateAccount(context.Background(), "foreign-release-hook@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := (*e.Store()).CreateAccountReleaseWebhookIfUnderQuota(context.Background(), state.AppWebhook{
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
		got := e.Do(t, method, accountReleaseWebhooksPath+"/"+foreign.ID, body, nil)
		if got.Code != http.StatusNotFound {
			t.Errorf("%s foreign webhook: %d %s", method, got.Code, got.Body)
		}
	}
}

func TestAccountReleaseWebhooks_FreePlanDenied(t *testing.T) {
	e := setupWebhookTest(t, api.PlanFree)
	if got := e.Do(t, http.MethodPost, accountReleaseWebhooksPath, accountReleaseWebhookReq(), nil); got.Code != http.StatusPaymentRequired {
		t.Fatalf("free create status %d: %s", got.Code, got.Body)
	}
}

// adr: 20261027090010 — agent_run.finished is part of the account receiver vocabulary
// only for accounts the agents capability is released to; elsewhere it is
// refused like any unknown event.
func TestAccountReleaseWebhooks_AgentRunEventFollowsAgentsCapability(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	installAgentGate(t, *e.S())
	agentHook := accountReleaseWebhookReq()
	agentHook.TargetURL = "https://example.com/agent-runs"
	agentHook.EventFilter = []string{"deployment.live", api.AgentRunFinishedWebhookEvent}
	if rec := e.Do(t, http.MethodPost, accountReleaseWebhooksPath, agentHook, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("agents off: status %d: %s", rec.Code, rec.Body)
	}
	created := createAccountReleaseWebhookForTest(t, e)
	agentOnly := []string{api.AgentRunFinishedWebhookEvent}
	update := api.UpdateAccountReleaseWebhookRequest{EventFilter: &agentOnly}
	if rec := e.Do(t, http.MethodPatch, accountReleaseWebhooksPath+"/"+created.ID, update, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("update with agents off: status %d: %s", rec.Code, rec.Body)
	}

	releaseAgentsTo(t, *e.S(), (*e.Acct()).ID)
	if rec := e.Do(t, http.MethodPost, accountReleaseWebhooksPath, agentHook, nil); rec.Code != http.StatusCreated {
		t.Fatalf("agents released: status %d: %s", rec.Code, rec.Body)
	}
	rec := e.Do(t, http.MethodPatch, accountReleaseWebhooksPath+"/"+created.ID, update, nil)
	var out api.AccountReleaseWebhookResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil ||
		strings.Join(out.EventFilter, ",") != api.AgentRunFinishedWebhookEvent {
		t.Fatalf("update with agents released: status %d: %s", rec.Code, rec.Body)
	}
}
