package outbound

// adr: 20261021171712 — every AI call returns its ID; settlement marks
// truncated, refused, empty, invalid-JSON and tool-error answers from every
// metered format; apps rate their calls by ID; sampled calls are scored by
// a judge; and drift against a model's previous days raises alerts.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// flagsOf feeds a response one byte at a time to an observer that checks
// quality, and returns its flags.
func flagsOf(observer *aiUsageObserver, quality aiQuality, body []byte) []string {
	quality.checked = true
	observer.quality = quality
	toolCallsOf(observer, body)
	return observer.qualityFlags()
}

func TestQualityFlagsAreReadFromEveryFormat(t *testing.T) {
	openai := func(contentType string) *aiUsageObserver {
		return newAIUsageObserver(api.OutboundAIProviderOpenAI, contentType, nil)
	}
	anthropic := func(contentType string) *aiUsageObserver {
		return newAIUsageObserver(api.OutboundAIProviderAnthropic, contentType, nil)
	}
	gemini := func() *aiUsageObserver { return newAIUsageObserver(api.OutboundAIProviderGemini, "application/json", nil) }
	converse := func() *aiUsageObserver {
		return newAIUsageObserverFormat(aiFormatBedrockConverse, "application/json", nil)
	}
	var converseStream []byte
	for _, payload := range []string{`{"contentBlockIndex":0,"delta":{"text":"par"}}`, `{"stopReason":"max_tokens"}`,
		`{"usage":{"inputTokens":3,"outputTokens":4}}`} {
		converseStream = append(converseStream, awsEventFrame("event", []byte(payload))...)
	}
	jsonMode := aiQuality{jsonMode: true}
	for _, tc := range []struct {
		name     string
		observer *aiUsageObserver
		quality  aiQuality
		body     []byte
		want     string
	}{
		{"chat answer", openai("application/json"), aiQuality{},
			[]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"hi"}}]}`), ""},
		{"chat length", openai("application/json"), aiQuality{},
			[]byte(`{"choices":[{"finish_reason":"length","message":{"content":"hi"}}]}`), "truncated"},
		{"chat refusal", openai("application/json"), aiQuality{},
			[]byte(`{"choices":[{"finish_reason":"stop","message":{"content":null,"refusal":"I can't help"}}]}`), "refused"},
		{"chat content filter", openai("application/json"), aiQuality{},
			[]byte(`{"choices":[{"finish_reason":"content_filter","message":{"content":""}}]}`), "refused"},
		{"chat empty", openai("application/json"), aiQuality{},
			[]byte(`{"choices":[{"finish_reason":"stop","message":{"content":""}}]}`), "empty"},
		{"chat tool call is not empty", openai("application/json"), aiQuality{},
			[]byte(`{"choices":[{"message":{"tool_calls":[{"id":"c","function":{"name":"f","arguments":"{}"}}]}}]}`), ""},
		{"chat stream", openai("text/event-stream"), aiQuality{},
			sseEvents(`{"choices":[{"delta":{"content":"hi"}}]}`, `{"choices":[{"delta":{},"finish_reason":"length"}]}`, `[DONE]`), "truncated"},
		{"chat JSON answer", openai("application/json"), jsonMode,
			[]byte(`{"choices":[{"message":{"content":" {\"a\": 1} "}}]}`), ""},
		{"chat invalid JSON", openai("application/json"), jsonMode,
			[]byte(`{"choices":[{"message":{"content":"Sure! {\"a\": 1}"}}]}`), "invalid_json"},
		{"chat streamed JSON", openai("text/event-stream"), jsonMode,
			sseEvents(`{"choices":[{"delta":{"content":"{\"a\":"}}]}`, `{"choices":[{"delta":{"content":" 1}"}}]}`, `[DONE]`), ""},
		{"chat invalid tool arguments", openai("application/json"), aiQuality{},
			[]byte(`{"choices":[{"message":{"tool_calls":[{"id":"c","function":{"name":"f","arguments":"{\"q\":"}}]}}]}`), "invalid_json"},
		{"tool error and answer", openai("application/json"), aiQuality{toolError: true},
			[]byte(`{"choices":[{"message":{"content":"sorry"}}]}`), "tool_error"},
		{"responses incomplete", openai("application/json"), aiQuality{},
			[]byte(`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`), "truncated"},
		{"responses refusal", openai("application/json"), aiQuality{},
			[]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"no"}]}]}`), "refused"},
		{"responses stream JSON", openai("text/event-stream"), jsonMode, sseEvents(
			`{"type":"response.output_text.delta","delta":"{\"a\""}`, `{"type":"response.output_text.delta","delta":":1}"}`,
			`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"a\":1}"}]}]}}`), ""},
		{"anthropic max tokens", anthropic("application/json"), aiQuality{},
			[]byte(`{"content":[{"type":"text","text":"hi"}],"stop_reason":"max_tokens"}`), "truncated"},
		{"anthropic refusal", anthropic("application/json"), aiQuality{},
			[]byte(`{"content":[],"stop_reason":"refusal"}`), "refused"},
		{"anthropic thinking only", anthropic("application/json"), aiQuality{},
			[]byte(`{"content":[{"type":"thinking","thinking":"hmm"}],"stop_reason":"end_turn"}`), "empty"},
		{"anthropic stream", anthropic("text/event-stream"), aiQuality{}, sseEvents(
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`), "truncated"},
		{"gemini safety", gemini(), aiQuality{}, []byte(`{"candidates":[{"finishReason":"SAFETY"}]}`), "refused"},
		{"gemini blocked prompt", gemini(), aiQuality{}, []byte(`{"promptFeedback":{"blockReason":"SAFETY"}}`), "refused"},
		{"gemini malformed call", gemini(), aiQuality{}, []byte(`{"candidates":[{"finishReason":"MALFORMED_FUNCTION_CALL"}]}`), "empty,invalid_json"},
		{"gemini thought is not text", gemini(), aiQuality{},
			[]byte(`{"candidates":[{"content":{"parts":[{"text":"plan","thought":true}]},"finishReason":"STOP"}]}`), "empty"},
		{"gemini JSON", gemini(), jsonMode,
			[]byte(`{"candidates":[{"content":{"parts":[{"text":"[1,2]"}]},"finishReason":"STOP"}]}`), ""},
		{"bedrock guardrail", converse(), aiQuality{},
			[]byte(`{"output":{"message":{"content":[{"text":"blocked"}]}},"stopReason":"guardrail_intervened"}`), "refused"},
		{"bedrock stream", newAIUsageObserverFormat(aiFormatBedrockConverse, awsEventStreamMediaType, nil), aiQuality{},
			converseStream, "truncated"},
	} {
		if got := strings.Join(flagsOf(tc.observer, tc.quality, tc.body), ","); got != tc.want {
			t.Errorf("%s: flags %q, want %q", tc.name, got, tc.want)
		}
	}
	unchecked := newAIUsageObserver(api.OutboundAIProviderOpenAI, "application/json", nil)
	toolCallsOf(unchecked, []byte(`{"data":[{"embedding":[1]}],"usage":{"prompt_tokens":1}}`))
	if flags := unchecked.qualityFlags(); flags != nil {
		t.Fatalf("an unchecked endpoint got flags %v", flags)
	}
}

func TestRequestQualityChecks(t *testing.T) {
	for _, tc := range []struct {
		body      string
		json      bool
		toolError bool
	}{
		{`{"response_format":{"type":"json_object"}}`, true, false},
		{`{"response_format":{"type":"json_schema","json_schema":{}}}`, true, false},
		{`{"response_format":{"type":"text"}}`, false, false},
		{`{"text":{"format":{"type":"json_schema"}}}`, true, false},
		{`{"generationConfig":{"responseMimeType":"application/json"}}`, true, false},
		{`{"generation_config":{"response_mime_type":"application/json"}}`, true, false},
		{`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","is_error":true}]}]}`, false, true},
		// An earlier failure repeats in every later request; only the newest
		// turn counts.
		{`{"messages":[{"role":"user","content":[{"type":"tool_result","is_error":true}]},{"role":"assistant","content":"ok"},` +
			`{"role":"user","content":[{"type":"tool_result","is_error":false}]}]}`, false, false},
		{`{"messages":[{"role":"user","content":[{"toolResult":{"toolUseId":"t","status":"error"}}]}]}`, false, true},
		{`{"messages":[{"role":"user","content":"is_error"}]}`, false, false},
	} {
		if got := requestWantsJSON([]byte(tc.body)); got != tc.json {
			t.Errorf("%s: JSON mode %v", tc.body, got)
		}
		if got := requestReportsToolError([]byte(tc.body)); got != tc.toolError {
			t.Errorf("%s: tool error %v", tc.body, got)
		}
	}
}

func (f *traceFixture) post(path, body string, headers ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Add(headers[i], headers[i+1])
	}
	request = request.WithContext(withAccessToken(request.Context(), f.token))
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

func TestCallsReturnTheirIDAndCarryQualityFlags(t *testing.T) {
	f := newTraceFixture(t, func(i *Integration) { i.AIRequestLogEnabled, i.AILogExport = true, true })
	f.respond(`{"model":"gpt-5","choices":[{"finish_reason":"length","message":{"content":"{\"a\":"}}],` +
		`"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	got := f.post("/i/llm/v1/chat/completions", `{"model":"gpt-5","max_tokens":100,"messages":[],"response_format":{"type":"json_object"}}`)
	callID := got.Header().Get(api.OutboundAICallHeader)
	if got.Code != http.StatusOK || !validUUID(callID) {
		t.Fatalf("call = %d, call id %q", got.Code, callID)
	}
	logged := f.backend.AIRequestLog("llm")
	if len(logged) != 1 || logged[0].CallID != callID || strings.Join(logged[0].QualityFlags, ",") != "truncated,invalid_json" {
		t.Fatalf("request log = %+v", logged)
	}
	exports := f.backend.AILogExports()
	if len(exports) != 1 || !strings.Contains(exports[0].Line, `"call_id":"`+callID+`"`) ||
		!strings.Contains(exports[0].Line, `"quality_flags":["truncated","invalid_json"]`) ||
		exports[0].Attributes["gregale.ai.quality_flags"] != "truncated,invalid_json" {
		t.Fatalf("export = %+v", exports)
	}
}

func TestAppsRateTheirOwnLoggedCalls(t *testing.T) {
	f := newTraceFixture(t, func(i *Integration) { i.AIRequestLogEnabled = true })
	callID := f.call().Header().Get(api.OutboundAICallHeader)
	feedback := func(body string) *httptest.ResponseRecorder { return f.post("/i/llm"+AIFeedbackPath, body) }
	if got := feedback(`{"call_id":"` + callID + `","rating":"bad","label":"wrong_answer"}`); got.Code != http.StatusNoContent {
		t.Fatalf("feedback = %d %s", got.Code, got.Body.String())
	}
	if got := feedback(`{"call_id":"` + callID + `","rating":"good"}`); got.Code != http.StatusNoContent {
		t.Fatalf("second feedback = %d", got.Code)
	}
	if stored, ok := f.backend.AIFeedbackFor("llm", callID); !ok || stored.Rating != "good" || stored.Label != "" {
		t.Fatalf("stored = %+v, %v", stored, ok)
	}
	for body, want := range map[string]int{
		`{"call_id":"` + uuid.NewString() + `","rating":"good"}`:  http.StatusNotFound,
		`{"call_id":"` + callID + `","rating":"meh"}`:             http.StatusBadRequest,
		`{"call_id":"` + callID + `","rating":"bad","label":"A"}`: http.StatusBadRequest,
		`{"call_id":"not-a-uuid","rating":"good"}`:                http.StatusBadRequest,
		`not json`: http.StatusBadRequest,
	} {
		if got := feedback(body); got.Code != want {
			t.Errorf("%s = %d, want %d", body, got.Code, want)
		}
	}
	// Another app of the account cannot rate this app's call.
	f.token.AppID = uuid.NewString()
	if got := feedback(`{"call_id":"` + callID + `","rating":"bad"}`); got.Code != http.StatusForbidden && got.Code != http.StatusNotFound {
		t.Fatalf("another app's feedback = %d", got.Code)
	}
	if len(f.headers) != 1 {
		t.Fatalf("feedback reached the provider: %d calls", len(f.headers))
	}
}

func TestSampledCallsQueueForOnlineEvaluation(t *testing.T) {
	f := newTraceFixture(t, func(i *Integration) {
		i.AIRequestLogEnabled, i.AIRequestLogContent, i.AIOnlineEvalPercent = true, true, 100
	})
	f.respond(`{"model":"gpt-5","choices":[{"message":{"content":"Paris"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	callID := f.post("/i/llm/v1/chat/completions",
		`{"model":"gpt-5","max_tokens":100,"messages":[{"role":"user","content":"Capital of France?"}]}`).Header().Get(api.OutboundAICallHeader)
	if pending := f.backend.AIPendingEvaluations("llm"); len(pending) != 1 || pending[0] != callID {
		t.Fatalf("pending = %v, want %s", pending, callID)
	}
	off := newTraceFixture(t, func(i *Integration) { i.AIRequestLogEnabled, i.AIRequestLogContent = true, true })
	off.call()
	if pending := off.backend.AIPendingEvaluations("llm"); len(pending) != 0 {
		t.Fatalf("sampled without online evaluation: %v", pending)
	}
}

// fakeOnlineEvaluationStore hands out its jobs once and keeps the scores.
type fakeOnlineEvaluationStore struct {
	mu     sync.Mutex
	jobs   []AIOnlineEvaluationJob
	scores map[string]int
}

func (s *fakeOnlineEvaluationStore) ClaimAIOnlineEvaluations(context.Context, int) ([]AIOnlineEvaluationJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := s.jobs
	s.jobs = nil
	return jobs, nil
}

func (s *fakeOnlineEvaluationStore) FinishAIOnlineEvaluation(_ context.Context, job AIOnlineEvaluationJob, score int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scores[job.LogID] = score
	return nil
}

func TestOnlineEvaluationWorkerScoresThroughTheJudge(t *testing.T) {
	var mu sync.Mutex
	var judged []string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		judged = append(judged, string(body))
		mu.Unlock()
		reply := `{\"score\": 4}`
		if strings.Contains(string(body), "Unreadable") {
			reply = "I think it is fine"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-5","choices":[{"message":{"content":"`+reply+`"}}],"usage":{"prompt_tokens":50,"completion_tokens":5}}`)
	}))
	t.Cleanup(upstream.Close)
	account := uuid.NewString()
	judge := phase9Integration(t, "judge", upstream.URL, api.OutboundAIProviderOpenAI, account)
	handler, _ := phase9Handler(t, upstream.Client(), judge)
	request := func(question string) []byte {
		return []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"` + question + `"}]}`)
	}
	answer := []byte(`{"choices":[{"message":{"content":"Paris"}}]}`)
	job := func(id, question string, captured bool) AIOnlineEvaluationJob {
		return AIOnlineEvaluationJob{LogID: id, IntegrationID: "llm", AccountID: account, JudgeIntegrationID: "judge", JudgeModel: "gpt-5",
			Rubric: "Answers name one city.", Captured: captured, RequestBody: request(question), ResponseBody: answer,
			ResponseContentType: "application/json"}
	}
	store := &fakeOnlineEvaluationStore{scores: map[string]int{}, jobs: []AIOnlineEvaluationJob{
		job("a", "Capital of France?", true), job("b", "Unreadable verdict", true), job("c", "Expired", false),
		{LogID: "d", AccountID: uuid.NewString(), JudgeIntegrationID: "judge", JudgeModel: "gpt-5", Captured: true,
			RequestBody: request("Other account"), ResponseBody: answer, ResponseContentType: "application/json"},
	}}
	worker := &AIOnlineEvaluationWorker{Store: store, Handler: handler}
	if claimed, err := worker.ProcessBatch(context.Background()); err != nil || claimed != 4 {
		t.Fatalf("batch = %d, %v", claimed, err)
	}
	if store.scores["a"] != 4 || store.scores["b"] != 0 || store.scores["c"] != 0 || store.scores["d"] != 0 {
		t.Fatalf("scores = %v", store.scores)
	}
	if len(judged) != 2 || !strings.Contains(judged[0]+judged[1], "Answers name one city.") || !strings.Contains(judged[0]+judged[1], "Paris") {
		t.Fatalf("judge requests = %v", judged)
	}
}

func TestJudgeScoreReadsOneToFive(t *testing.T) {
	for text, want := range map[string]int{
		`{"score": 5}`: 5, `Score: {"score":1}`: 1, `{"score": "3"}`: 3, `{"score": 6}`: 0, `{"score": 2.5}`: 0, "no": 0,
	} {
		if got, _ := judgeScore(text); got != want {
			t.Errorf("%q = %d, want %d", text, got, want)
		}
	}
}

func TestDetectAIQualityDrift(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	base := AIQualityTotals{Answered: 1_000, Truncated: 20, Refused: 10, GoodFeedback: 90, BadFeedback: 10, Evaluated: 50, ScoreSum: 210}
	window := func(today AIQualityTotals) AIQualityWindow {
		return AIQualityWindow{IntegrationID: "i", Model: "gpt-5", UsageDate: now, Today: today, Baseline: base}
	}
	metrics := func(today AIQualityTotals) string {
		var names []string
		for _, alert := range detectAIQualityDrift(window(today), now) {
			names = append(names, alert.Metric)
		}
		return strings.Join(names, ",")
	}
	// Truncation 2% -> 10% and refusals 1% -> 3%: only truncation passes
	// both the 5-point rise and the doubling.
	if got := metrics(AIQualityTotals{Answered: 200, Truncated: 20, Refused: 6}); got != "truncated" {
		t.Fatalf("metrics = %q", got)
	}
	if got := metrics(AIQualityTotals{Answered: 99, Truncated: 50}); got != "" {
		t.Fatalf("a day below the minimum alerted: %q", got)
	}
	// Bad ratings 10% -> 40% and mean score 4.2 -> 3.0.
	if got := metrics(AIQualityTotals{Answered: 10, GoodFeedback: 12, BadFeedback: 8, Evaluated: 20, ScoreSum: 60}); got != "bad_feedback,evaluation_score" {
		t.Fatalf("metrics = %q", got)
	}
	alert := detectAIQualityDrift(window(AIQualityTotals{Answered: 200, Truncated: 20}), now)[0]
	if alert.CurrentValue != 1_000 || alert.BaselineValue != 200 || alert.CurrentCount != 200 || alert.BaselineCount != 1_000 {
		t.Fatalf("alert = %+v", alert)
	}
}
