package outboundai

// adr: 20261021171712 — apid stores online evaluation and drift alert
// settings, shows quality flags, ratings and scores on logged calls, rates
// a logged call on a person's behalf, and lists drift alerts.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPutPolicyStoresQualitySettings(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	judge := aiIntegration(t, store, acct.ID, api.OutboundAIProviderOpenAI)
	path := "/v1/outbound/integrations/x/ai-policy"
	evaluation := func(extra string) string {
		return `{"ai_provider":"openai","request_log_enabled":true,"request_log_content":true,"online_evaluation":{` + extra + `}}`
	}
	for _, body := range []string{
		`{"ai_provider":"","quality_alerts":true}`,
		`{"ai_provider":"openai","request_log_enabled":true,"online_evaluation":{"sample_percent":5,"judge_integration_id":"` + judge + `","judge_model":"m"}}`,
		evaluation(`"sample_percent":0,"judge_integration_id":"` + judge + `","judge_model":"m"`),
		evaluation(`"sample_percent":5,"judge_integration_id":"` + judge + `","judge_model":""`),
		evaluation(`"sample_percent":5,"judge_integration_id":"` + judge + `","judge_model":"m","rubric":"` + strings.Repeat("r", 1001) + `"`),
		evaluation(`"sample_percent":5,"judge_integration_id":"` + uuid.NewString() + `","judge_model":"m"`),
	} {
		if w := serve(service.PutPolicy, acct, http.MethodPut, path, integrationID, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", body, w.Code, w.Body.String())
		}
	}
	body := `{"ai_provider":"openai","request_log_enabled":true,"request_log_content":true,"quality_alerts":true,` +
		`"online_evaluation":{"sample_percent":5,"judge_integration_id":"` + judge + `","judge_model":"gpt-5-mini","rubric":"Cite a source."}}`
	if w := serve(service.PutPolicy, acct, http.MethodPut, path, integrationID, body); w.Code != http.StatusNoContent {
		t.Fatalf("quality settings = %d %s", w.Code, w.Body.String())
	}
	offers, err := store.ListOutboundIntegrationOffers(context.Background(), acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, offer := range offers {
		if offer.ID != integrationID {
			continue
		}
		if offer.AIOnlineEvalPercent == nil || *offer.AIOnlineEvalPercent != 5 || offer.AIOnlineEvalJudgeIntegrationID != judge ||
			offer.AIOnlineEvalModel != "gpt-5-mini" || offer.AIOnlineEvalRubric != "Cite a source." || !offer.AIQualityAlerts {
			t.Fatalf("offer = %+v", offer)
		}
		return
	}
	t.Fatal("the integration is not listed")
}

func serveFeedback(service *Service, acct state.Account, integrationID, requestID, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPut, "/v1/outbound/integrations/x/ai-requests/"+requestID+"/feedback", strings.NewReader(body))
	r.SetPathValue("integration", integrationID)
	r.SetPathValue("request", requestID)
	w := httptest.NewRecorder()
	service.PutRequestFeedback(w, r, acct)
	return w
}

func TestQualityFieldsFeedbackAndAlerts(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	score := int32(4)
	logID := uuid.NewString()
	store.SeedOutboundAIRequestLog(integrationID, state.OutboundAIRequestLogEntry{ID: logID, CreatedAt: time.Now().UTC(),
		Caller: uuid.NewString(), Model: "gpt-5", Status: 200, Outcome: "known", QualityFlags: []string{"truncated", "invalid_json"},
		EvaluationScore: &score})
	if w := serveFeedback(service, acct, integrationID, logID, `{"rating":"bad","label":"wrong_answer"}`); w.Code != http.StatusNoContent {
		t.Fatalf("feedback = %d %s", w.Code, w.Body.String())
	}
	for requestID, body := range map[string]string{logID: `{"rating":"meh"}`, uuid.NewString(): `{"rating":"good"}`} {
		if w := serveFeedback(service, acct, integrationID, requestID, body); w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d", requestID, body, w.Code)
		}
	}
	w := serve(service.ListRequestLog, acct, http.MethodGet, "/v1/outbound/integrations/x/ai-requests", integrationID, "")
	var page struct {
		Items []api.OutboundAIRequestLogEntry `json:"items"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 {
		t.Fatalf("request log = %d %s", w.Code, w.Body.String())
	}
	if item := page.Items[0]; strings.Join(item.QualityFlags, ",") != "truncated,invalid_json" || item.Feedback == nil || *item.Feedback != "bad" ||
		*item.FeedbackLabel != "wrong_answer" || item.EvaluationScore == nil || *item.EvaluationScore != 4 {
		t.Fatalf("entry = %+v", item)
	}

	now := time.Now().UTC()
	store.SeedOutboundAIQualityAlert(integrationID,
		state.OutboundAIQualityAlert{ID: uuid.NewString(), IntegrationID: integrationID, UsageDate: now.Format("2006-01-02"), Model: "gpt-5",
			Metric: "truncated", CurrentValue: 1_250, BaselineValue: 200, CurrentCount: 400, BaselineCount: 9_000, DetectedAt: now.Add(-time.Hour)},
		state.OutboundAIQualityAlert{ID: uuid.NewString(), IntegrationID: integrationID, UsageDate: now.Format("2006-01-02"), Model: "gpt-5",
			Metric: "evaluation_score", CurrentValue: 310, BaselineValue: 420, CurrentCount: 30, BaselineCount: 200, DetectedAt: now})
	alertsPath := "/v1/outbound/integrations/x/ai-quality-alerts"
	w = serve(service.ListQualityAlerts, acct, http.MethodGet, alertsPath, integrationID, "")
	var alerts api.OutboundAIQualityAlertList
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &alerts) != nil || len(alerts.Items) != 2 ||
		alerts.Items[0].Metric != "evaluation_score" || alerts.Items[1].CurrentValue != 1_250 {
		t.Fatalf("alerts = %d %s", w.Code, w.Body.String())
	}
	if w := serve(service.ListQualityAlerts, acct, http.MethodGet, alertsPath+"?limit=1", integrationID, ""); w.Code != http.StatusOK ||
		strings.Count(w.Body.String(), `"metric"`) != 1 {
		t.Fatalf("one alert = %d %s", w.Code, w.Body.String())
	}
	if w := serve(service.ListQualityAlerts, acct, http.MethodGet, alertsPath+"?limit=101", integrationID, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("limit 101 = %d", w.Code)
	}
	other, err := store.CreateAccount(context.Background(), "other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if w := serve(service.ListQualityAlerts, other, http.MethodGet, alertsPath, integrationID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("another account's alerts = %d", w.Code)
	}
	if w := serveFeedback(service, other, integrationID, logID, `{"rating":"good"}`); w.Code != http.StatusNotFound {
		t.Fatalf("another account's feedback = %d", w.Code)
	}
}
