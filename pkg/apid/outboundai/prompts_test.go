package outboundai

// adr: 20261114180740 — the managed prompt API: push validates the version
// and creates the prompt on its first push, an identical push answers 200,
// the plan limit answers 429, live/rollback/canary keep the pointers, stats
// and evaluations read back, and evaluation answers are withheld without
// content scope.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func servePromptRequest(handler func(http.ResponseWriter, *http.Request, state.Account), acct state.Account, method, body string,
	values map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/v1/outbound/ai-prompts/x", strings.NewReader(body))
	for key, value := range values {
		r.SetPathValue(key, value)
	}
	w := httptest.NewRecorder()
	handler(w, r, acct)
	return w
}

func promptPushBody(integrationID, system string, live bool) string {
	body, _ := json.Marshal(api.PushOutboundAIPromptVersionRequest{
		Messages: []api.OutboundAIPromptMessage{{Role: "system", Content: system}, {Role: "user", Content: "{{question}}"}},
		Target:   api.OutboundAIPromptTarget{IntegrationID: integrationID, Model: "gpt-4o"}, Live: live,
	})
	return string(body)
}

func decodeBody[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return out
}

func TestPromptAPILifecycle(t *testing.T) {
	service, store, acct, _ := newTestService(t)
	openai := uuid.NewString()
	store.SeedOutboundIntegrationOffer(state.OutboundIntegrationOffer{ID: openai, AccountID: acct.ID, Name: "openai",
		Origin: "https://api.openai.com", Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed",
		AIProvider: api.OutboundAIProviderOpenAI})
	name := map[string]string{"prompt": "support"}

	for label, tc := range map[string]struct {
		values map[string]string
		body   string
		want   int
	}{
		"bad name":            {map[string]string{"prompt": "Bad_Name"}, promptPushBody(openai, "Hi", false), http.StatusBadRequest},
		"no messages":         {name, `{"target":{"integration_id":"` + openai + `","model":"gpt-4o"},"messages":[]}`, http.StatusBadRequest},
		"unknown integration": {name, promptPushBody(uuid.NewString(), "Hi", false), http.StatusBadRequest},
		"default without var": {name, `{"messages":[{"role":"user","content":"hi"}],"defaults":{"x":"1"},"target":{"integration_id":"` + openai + `","model":"m"}}`, http.StatusBadRequest},
	} {
		if w := servePromptRequest(service.PushPromptVersion, acct, http.MethodPost, tc.body, tc.values); w.Code != tc.want {
			t.Fatalf("%s = %d %s", label, w.Code, w.Body.String())
		}
	}
	w := servePromptRequest(service.PushPromptVersion, acct, http.MethodPost, promptPushBody(openai, "Be kind.", false), name)
	first := decodeBody[api.PushOutboundAIPromptVersionResponse](t, w)
	if w.Code != http.StatusCreated || !first.Created || first.Prompt.LiveVersion == nil || *first.Prompt.LiveVersion != 1 ||
		len(first.Version.Variables) != 1 || first.Version.Variables[0] != "question" {
		t.Fatalf("first push = %d %s", w.Code, w.Body.String())
	}
	w = servePromptRequest(service.PushPromptVersion, acct, http.MethodPost, promptPushBody(openai, "Be kind.", false), name)
	if again := decodeBody[api.PushOutboundAIPromptVersionResponse](t, w); w.Code != http.StatusOK || again.Created || again.Version.Version != 1 {
		t.Fatalf("identical push = %d %s", w.Code, w.Body.String())
	}
	w = servePromptRequest(service.PushPromptVersion, acct, http.MethodPost, promptPushBody(openai, "Be brief.", false), name)
	if second := decodeBody[api.PushOutboundAIPromptVersionResponse](t, w); w.Code != http.StatusCreated || *second.Prompt.LiveVersion != 1 {
		t.Fatalf("second push = %d %s", w.Code, w.Body.String())
	}

	if w := servePromptRequest(service.PutPromptCanary, acct, http.MethodPut, `{"version":1,"percent":10}`, name); w.Code != http.StatusBadRequest {
		t.Fatalf("canary on live version = %d %s", w.Code, w.Body.String())
	}
	if w := servePromptRequest(service.PutPromptCanary, acct, http.MethodPut, `{"version":2,"percent":10}`, name); w.Code != http.StatusOK {
		t.Fatalf("canary = %d %s", w.Code, w.Body.String())
	}
	w = servePromptRequest(service.PromotePromptCanary, acct, http.MethodPost, "", name)
	if promoted := decodeBody[api.OutboundAIPrompt](t, w); w.Code != http.StatusOK || *promoted.LiveVersion != 2 || promoted.Canary != nil {
		t.Fatalf("promote = %d %s", w.Code, w.Body.String())
	}
	w = servePromptRequest(service.RollBackPrompt, acct, http.MethodPost, "", name)
	if rolled := decodeBody[api.OutboundAIPrompt](t, w); w.Code != http.StatusOK || *rolled.LiveVersion != 1 {
		t.Fatalf("rollback = %d %s", w.Code, w.Body.String())
	}
	if w := servePromptRequest(service.PutPromptLive, acct, http.MethodPut, `{"version":9}`, name); w.Code != http.StatusBadRequest {
		t.Fatalf("live missing version = %d %s", w.Code, w.Body.String())
	}
	w = servePromptRequest(service.GetPromptVersion, acct, http.MethodGet, "", map[string]string{"prompt": "support", "version": "2"})
	if version := decodeBody[api.OutboundAIPromptVersion](t, w); w.Code != http.StatusOK || version.Messages[0].Content != "Be brief." {
		t.Fatalf("get version = %d %s", w.Code, w.Body.String())
	}
	w = servePromptRequest(service.ListPromptVersions, acct, http.MethodGet, "", name)
	if list := decodeBody[api.OutboundAIPromptVersionList](t, w); w.Code != http.StatusOK || len(list.Items) != 2 {
		t.Fatalf("versions = %d %s", w.Code, w.Body.String())
	}
	if w := servePromptRequest(service.GetPromptStats, acct, http.MethodGet, "", name); w.Code != http.StatusOK {
		t.Fatalf("stats = %d %s", w.Code, w.Body.String())
	}

	evaluationBody := `{"version":2,"cases":[{"question":"hi"}],"judge_integration_id":"` + openai + `","judge_model":"gpt-4o"}`
	w = servePromptRequest(service.CreatePromptEvaluation, acct, http.MethodPost, evaluationBody, name)
	created := decodeBody[api.OutboundAIPromptEvaluation](t, w)
	if w.Code != http.StatusAccepted || created.BaselineVersion != 1 || created.Cases != 1 || created.State != api.OutboundAIEvaluationPending {
		t.Fatalf("evaluation = %d %s", w.Code, w.Body.String())
	}
	if w := servePromptRequest(service.CreatePromptEvaluation, acct, http.MethodPost, `{"version":2,"cases":[]}`, name); w.Code != http.StatusBadRequest {
		t.Fatalf("evaluation without cases = %d %s", w.Code, w.Body.String())
	}
	if w := servePromptRequest(service.ListPromptEvaluations, acct, http.MethodGet, "", name); w.Code != http.StatusOK {
		t.Fatalf("list evaluations = %d %s", w.Code, w.Body.String())
	}

	if w := servePromptRequest(service.DeletePrompt, acct, http.MethodDelete, "", name); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}
	if w := servePromptRequest(service.GetPrompt, acct, http.MethodGet, "", name); w.Code != http.StatusNotFound {
		t.Fatalf("deleted prompt = %d %s", w.Code, w.Body.String())
	}
}

func TestPromptAPIPlanLimit(t *testing.T) {
	service, store, acct, _ := newTestService(t)
	openai := uuid.NewString()
	store.SeedOutboundIntegrationOffer(state.OutboundIntegrationOffer{ID: openai, AccountID: acct.ID, Name: "openai",
		Origin: "https://api.openai.com", Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed",
		AIProvider: api.OutboundAIProviderOpenAI})
	free := acct
	free.Plan = api.PlanFree
	w := servePromptRequest(service.PushPromptVersion, free, http.MethodPost, promptPushBody(openai, "Hi", false), map[string]string{"prompt": "a"})
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "outbound_ai_prompt_limit") {
		t.Fatalf("free plan push = %d %s", w.Code, w.Body.String())
	}
}

func TestPromptEvaluationWithholdsAnswersWithoutContentScope(t *testing.T) {
	evaluation := state.OutboundAIPromptEvaluation{Cases: []map[string]string{{}},
		Items: []state.OutboundAIPromptEvaluationItem{{Verdict: "win", BaselineOutputText: "old", CandidateOutputText: "new"}}}
	withheld := promptEvaluationResponse(evaluation, false)
	if !withheld.ContentWithheld || withheld.Items[0].BaselineOutputText != "" || withheld.Items[0].CandidateOutputText != "" {
		t.Fatalf("withheld = %+v", withheld)
	}
	full := promptEvaluationResponse(evaluation, true)
	if full.ContentWithheld || full.Items[0].CandidateOutputText != "new" {
		t.Fatalf("full = %+v", full)
	}
}
