package outbound

// adr: 20261114180740 — in Postgres: a prompt call resolves the live
// version, logs the prompt and version on its request-log row and adds to
// the version's minute row; stats count calls and feedback per version; an
// active canary is judged and rolled back by the route rule; the worker
// runs a prompt evaluation on both versions and stores its verdict.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func tieChatCompletion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"{\"winner\": \"tie\"}"}}],"usage":{"prompt_tokens":20,"completion_tokens":10}}`))
}

// The gateway's own in-process calls and replays name no app binding; on a
// customer integration Postgres admission must still admit them, while an
// unbound call that is not the gateway's own is refused.
func TestPostgresAdmitsGatewayOwnCallsWithoutBinding(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	spec := f.spec()
	spec.BindingAppID = ""
	if _, err := backend.Admit(context.Background(), spec); err == nil {
		t.Fatal("an unbound app call on a customer integration was admitted")
	}
	spec.GatewayOwn = true
	if decision, err := backend.Admit(context.Background(), spec); err != nil || !decision.Granted {
		t.Fatalf("gateway's own call = %+v %v", decision, err)
	}
}

func TestPostgresManagedPrompts(t *testing.T) {
	f := newAIPreviewFixture(t, tieChatCompletion, state.OutboundAIPolicy{RequestLogEnabled: true})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	push := func(system string) state.OutboundAIPromptVersion {
		t.Helper()
		_, version, _, err := f.store.PushOutboundAIPromptVersion(ctx, state.OutboundAIPromptPush{ID: uuid.NewString(), AccountID: f.account.ID,
			Name: "support", MaxPrompts: 5, Version: state.OutboundAIPromptVersion{
				Messages: []api.OutboundAIPromptMessage{{Role: api.OutboundAIPromptRoleSystem, Content: system},
					{Role: api.OutboundAIPromptRoleUser, Content: "{{question}}"}},
				Target: api.OutboundAIPromptTarget{IntegrationID: f.offer.ID, Model: "gpt-4o"}}})
		if err != nil {
			t.Fatal(err)
		}
		return version
	}
	push("Be kind.")
	push("Be brief.")
	prompt, err := f.store.GetOutboundAIPrompt(ctx, f.account.ID, "support")
	if err != nil || prompt.LiveVersion == nil || *prompt.LiveVersion != 1 {
		t.Fatalf("prompt = %+v %v", prompt, err)
	}

	handler := f.handler(nil, backend)
	if handler.Prompts, err = NewPostgresPromptResolver(f.pool); err != nil {
		t.Fatal(err)
	}
	callCtx := withRelayPrincipal(ctx, relayPrincipal{app: WorkloadIdentity{AccountID: f.account.ID, AppID: f.app.ID}})
	request := httptest.NewRequestWithContext(callCtx, http.MethodPost, "http://outboundd/p/support",
		strings.NewReader(`{"variables":{"question":"hello"}}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get(api.OutboundAIPromptVersionHeader) != "1" {
		t.Fatalf("prompt call = %d %s", recorder.Code, recorder.Body.String())
	}

	var logID uuid.UUID
	var promptID *uuid.UUID
	var version *int
	if err := f.pool.QueryRow(ctx, `SELECT id, prompt_id, prompt_version FROM outbound_ai_request_log WHERE integration_id = $1`,
		uuid.MustParse(f.offer.ID)).Scan(&logID, &promptID, &version); err != nil {
		t.Fatal(err)
	}
	if promptID == nil || promptID.String() != prompt.ID || version == nil || *version != 1 {
		t.Fatalf("request log prompt = %v %v", promptID, version)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_request_log SET feedback = 'good', feedback_at = now() WHERE id = $1`, logID); err != nil {
		t.Fatal(err)
	}
	stats, err := f.store.OutboundAIPromptStats(ctx, f.account.ID, "support")
	if err != nil || len(stats.Versions) != 1 || stats.Versions[0].Version != 1 || stats.Versions[0].Calls != 1 ||
		stats.Versions[0].GoodFeedback != 1 || stats.Versions[0].CostMillicents <= 0 {
		t.Fatalf("stats = %+v %v", stats, err)
	}

	// A canary that errors more than the live version is rolled back.
	if _, err := f.store.SetOutboundAIPromptCanary(ctx, f.account.ID, "support", state.OutboundAIPromptCanary{
		Version: 2, Percent: 10, MaxErrorRateIncrease: 5, MinCalls: 10, WindowMinutes: 60}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO outbound_ai_prompt_version_minutes (prompt_id, version, minute, calls, errors)
		VALUES ($1, 1, date_trunc('minute', now()), 100, 1), ($1, 2, date_trunc('minute', now()), 20, 10)
		ON CONFLICT (prompt_id, version, minute) DO UPDATE SET calls = EXCLUDED.calls, errors = EXCLUDED.errors`,
		uuid.MustParse(prompt.ID)); err != nil {
		t.Fatal(err)
	}
	evaluator := &AIRouteCanaryEvaluator{Store: backend, Prompts: backend}
	if rolled, err := evaluator.EvaluateOnce(ctx); err != nil || rolled != 1 {
		t.Fatalf("rolled back = %d %v", rolled, err)
	}
	prompt, _ = f.store.GetOutboundAIPrompt(ctx, f.account.ID, "support")
	if prompt.Canary == nil || prompt.Canary.State != api.OutboundAIRouteCanaryRolledBack || prompt.Canary.Reason != api.OutboundAIRouteCanaryReasonErrorRate {
		t.Fatalf("canary after rollback = %+v", prompt.Canary)
	}

	// The worker runs both versions on each case and stores the verdict.
	created, err := f.store.CreateOutboundAIPromptEvaluation(ctx, state.OutboundAIPromptEvaluation{ID: uuid.NewString(),
		AccountID: f.account.ID, PromptID: prompt.ID, CandidateVersion: 2,
		Cases: []map[string]string{{"question": "a"}, {"question": "b"}}, JudgeIntegrationID: f.offer.ID, JudgeModel: "gpt-4o",
		MaxLossRate: 20})
	if err != nil {
		t.Fatal(err)
	}
	worker := &AIPromptEvaluationWorker{Store: backend, Handler: handler}
	if processed, err := worker.ProcessOne(ctx); err != nil || !processed {
		t.Fatalf("process = %v %v", processed, err)
	}
	evaluation, err := f.store.GetOutboundAIPromptEvaluation(ctx, f.account.ID, created.ID)
	if err != nil || evaluation.State != api.OutboundAIEvaluationDone || evaluation.Verdict != api.OutboundAIEvaluationPass ||
		evaluation.Ties != 2 || len(evaluation.Items) != 2 || evaluation.CandidateCostMillicents <= 0 {
		for _, item := range evaluation.Items {
			if item.BaselineStatus != nil && item.CandidateStatus != nil {
				t.Logf("case %d: baseline %d candidate %d", item.Case, *item.BaselineStatus, *item.CandidateStatus)
			}
		}
		t.Fatalf("evaluation = %+v %v", evaluation, err)
	}
	if processed, err := worker.ProcessOne(ctx); err != nil || processed {
		t.Fatalf("second process = %v %v", processed, err)
	}
}
