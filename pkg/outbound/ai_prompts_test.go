package outbound

// adr: 20261114180740 — a managed prompt call renders the picked version
// into an OpenAI chat completions request, serves it through the route
// path, names the version in a header and records the attempt for the
// version's minute row; missing variables, unknown prompts, other accounts'
// prompts and gone routes are refused.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const testPromptID = "8d1c3f4e-2a6b-4c8d-9e0f-1a2b3c4d5e6f"

type staticPromptResolver map[string]AIPrompt

func (s staticPromptResolver) Prompt(_ context.Context, accountID, name string, _ int) (AIPrompt, error) {
	prompt, ok := s[name]
	if !ok || prompt.AccountID != accountID {
		return AIPrompt{}, ErrPromptNotFound
	}
	return prompt, nil
}

func promptTestVersion(number int, system string, target api.OutboundAIPromptTarget) AIPromptVersion {
	temperature := 0.2
	return AIPromptVersion{Version: number, Target: target, Defaults: map[string]string{"tone": "kind"},
		Params: api.OutboundAIPromptParams{Temperature: &temperature},
		Messages: []api.OutboundAIPromptMessage{{Role: api.OutboundAIPromptRoleSystem, Content: system},
			{Role: api.OutboundAIPromptRoleUser, Content: "{{question}}"}}}
}

type promptTestRig struct {
	handler *Handler
	backend *MemoryBackend
	mu      sync.Mutex
	bodies  []map[string]any
}

func newPromptTestRig(t *testing.T) *promptTestRig {
	t.Helper()
	rig := &promptTestRig{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		rig.mu.Lock()
		rig.bodies = append(rig.bodies, body)
		rig.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`)
	}))
	t.Cleanup(upstream.Close)
	integration, err := NewIntegration("oa", upstream.URL, "unused", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.ProviderAuthMode, integration.AIProvider = ProviderAuthManaged, api.OutboundAIProviderOpenAI
	integration.AllowedMethods, integration.AllowedPathPrefixes = []string{http.MethodPost}, []string{"/v1"}
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	rig.backend = NewMemoryBackend()
	if rig.handler, err = NewHandler(resolver, rig.backend, upstream.Client()); err != nil {
		t.Fatal(err)
	}
	if err := rig.handler.SetManagedAuthorizations(map[string]string{"oa": "Bearer sk-test"}); err != nil {
		t.Fatal(err)
	}
	direct := api.OutboundAIPromptTarget{IntegrationID: "oa", Model: "gpt-4o"}
	rig.handler.Prompts = staticPromptResolver{
		"support": {ID: testPromptID, AccountID: "account-1", LiveVersion: 1, Versions: map[int]AIPromptVersion{
			1: promptTestVersion(1, "You are {{tone}}.", direct),
			2: promptTestVersion(2, "Be brief and {{tone}}.", direct),
		}},
		"canaried": {ID: testPromptID, AccountID: "account-1", LiveVersion: 1, CanaryVersion: 2, CanaryPercent: 100,
			Versions: map[int]AIPromptVersion{1: promptTestVersion(1, "v1", direct), 2: promptTestVersion(2, "v2", direct)}},
		"gone-route": {ID: testPromptID, AccountID: "account-1", LiveVersion: 1, Versions: map[int]AIPromptVersion{
			1: promptTestVersion(1, "x", api.OutboundAIPromptTarget{RouteID: "missing"})}},
		"other": {ID: testPromptID, AccountID: "account-2", LiveVersion: 1, Versions: map[int]AIPromptVersion{
			1: promptTestVersion(1, "x", direct)}},
	}
	rig.handler.Routes = StaticRouteResolver{}
	return rig
}

func (rig *promptTestRig) call(path, body string, relay bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:2774"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if relay {
		req = req.WithContext(withRelayIdentity(context.Background(), WorkloadIdentity{AccountID: "account-1", AppID: "app-1", InstanceID: "i-1"}))
	}
	recorder := httptest.NewRecorder()
	rig.handler.ServeHTTP(recorder, req)
	return recorder
}

func TestPromptCallRendersLiveVersionAndRecordsIt(t *testing.T) {
	rig := newPromptTestRig(t)
	recorder := rig.call("/p/support", `{"variables":{"question":"What is {{tone}}?"},"messages":[{"role":"user","content":"and more"}]}`, true)
	if recorder.Code != http.StatusOK || recorder.Header().Get(api.OutboundAIPromptVersionHeader) != "1" {
		t.Fatalf("call = %d %q %s", recorder.Code, recorder.Header().Get(api.OutboundAIPromptVersionHeader), recorder.Body.String())
	}
	rig.mu.Lock()
	sent := rig.bodies[0]
	rig.mu.Unlock()
	messages := sent["messages"].([]any)
	if sent["model"] != "gpt-4o" || sent["temperature"] != 0.2 || len(messages) != 3 ||
		messages[0].(map[string]any)["content"] != "You are kind." ||
		// A value is plain text: its own {{tone}} is not substituted.
		messages[1].(map[string]any)["content"] != "What is {{tone}}?" ||
		messages[2].(map[string]any)["content"] != "and more" {
		t.Fatalf("sent = %v", sent)
	}
	attempts := rig.backend.AIPromptAttempts()
	if len(attempts) != 1 || attempts[0].PromptID != testPromptID || attempts[0].Version != 1 || attempts[0].Error {
		t.Fatalf("prompt attempts = %+v", attempts)
	}
}

func TestPromptCallPicksPinnedAndCanaryVersions(t *testing.T) {
	rig := newPromptTestRig(t)
	if recorder := rig.call("/p/support", `{"variables":{"question":"q"},"version":2}`, true); recorder.Header().Get(api.OutboundAIPromptVersionHeader) != "2" {
		t.Fatalf("pinned = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := rig.call("/p/canaried", `{"variables":{"question":"q"}}`, true); recorder.Header().Get(api.OutboundAIPromptVersionHeader) != "2" {
		t.Fatalf("canary = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := rig.call("/p/support", `{"variables":{"question":"q"},"version":9}`, true); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown pinned version = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestPromptCallRefusals(t *testing.T) {
	rig := newPromptTestRig(t)
	for name, tc := range map[string]struct {
		path, body string
		relay      bool
		status     int
		code       string
	}{
		"missing variable":  {"/p/support", `{}`, true, http.StatusBadRequest, "outbound_ai_prompt_variable_missing"},
		"bad message":       {"/p/support", `{"variables":{"question":"q"},"messages":[{"content":"no role"}]}`, true, http.StatusBadRequest, "outbound_ai_prompt_request_invalid"},
		"not json":          {"/p/support", `nope`, true, http.StatusBadRequest, "outbound_ai_prompt_request_invalid"},
		"unknown prompt":    {"/p/nope", `{}`, true, http.StatusNotFound, "outbound_ai_prompt_not_found"},
		"another account's": {"/p/other", `{}`, true, http.StatusNotFound, "outbound_ai_prompt_not_found"},
		"bad name":          {"/p/Not_A_Label", `{}`, true, http.StatusNotFound, "outbound_ai_prompt_not_found"},
		"route gone":        {"/p/gone-route", `{"variables":{"question":"q"}}`, true, http.StatusConflict, "outbound_ai_prompt_target_unavailable"},
		"no relay identity": {"/p/support", `{"variables":{"question":"q"}}`, false, http.StatusForbidden, "outbound_route_requires_relay"},
	} {
		recorder := rig.call(tc.path, tc.body, tc.relay)
		var problem struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &problem)
		if recorder.Code != tc.status || problem.Code != tc.code {
			t.Errorf("%s: %d %s, want %d %s", name, recorder.Code, recorder.Body.String(), tc.status, tc.code)
		}
	}
	if attempts := rig.backend.AIPromptAttempts(); len(attempts) != 0 {
		t.Fatalf("refused calls recorded attempts: %+v", attempts)
	}
}

func TestPromptCanaryRollbackUsesTheRouteRule(t *testing.T) {
	store := &fakePromptCanaryStore{judgements: []AIPromptCanaryJudgement{
		{PromptID: testPromptID, Version: 2, Rule: AIRouteCanaryJudgement{MaxErrorRateIncrease: 5, MinCalls: 10,
			Baseline: AIRouteTargetCounts{Calls: 100, Errors: 1}, Canary: AIRouteTargetCounts{Calls: 20, Errors: 5}}},
		{PromptID: testPromptID, Version: 3, Rule: AIRouteCanaryJudgement{MaxErrorRateIncrease: 5, MinCalls: 10,
			Baseline: AIRouteTargetCounts{Calls: 100, Errors: 1}, Canary: AIRouteTargetCounts{Calls: 5, Errors: 5}}},
	}}
	evaluator := &AIRouteCanaryEvaluator{Store: &fakeRouteCanaryStore{}, Prompts: store}
	rolled, err := evaluator.EvaluateOnce(context.Background())
	if err != nil || rolled != 1 || len(store.rolledBack) != 1 || store.rolledBack[0] != "2:error_rate" {
		t.Fatalf("rolled = %d %v %v", rolled, store.rolledBack, err)
	}
}

type fakePromptCanaryStore struct {
	judgements []AIPromptCanaryJudgement
	rolledBack []string
}

func (f *fakePromptCanaryStore) ActiveAIPromptCanaries(context.Context) ([]AIPromptCanaryJudgement, error) {
	return f.judgements, nil
}

func (f *fakePromptCanaryStore) RollBackAIPromptCanary(_ context.Context, _ string, version int, reason string) (bool, error) {
	f.rolledBack = append(f.rolledBack, strconv.Itoa(version)+":"+reason)
	return true, nil
}

type fakeRouteCanaryStore struct{}

func (fakeRouteCanaryStore) ActiveAIRouteCanaries(context.Context) ([]AIRouteCanaryJudgement, error) {
	return nil, nil
}

func (fakeRouteCanaryStore) RollBackAIRouteCanary(context.Context, string, int, string) (bool, error) {
	return false, nil
}
