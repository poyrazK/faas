package outbound

// adr: 20261128190000 — a call that names no output ceiling and whose
// model's longest response does not fit the budget left gets the largest
// ceiling that does, instead of a 429.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func outputCapTestHandler(t *testing.T, dailyCost int64) (*Handler, *atomic.Int64, *atomic.Int32) {
	t.Helper()
	var sentCeiling atomic.Int64
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			MaxTokens           int64 `json:"max_tokens"`
			MaxCompletionTokens int64 `json:"max_completion_tokens"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		sentCeiling.Store(max(body.MaxTokens, body.MaxCompletionTokens))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m-test","choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`))
	}))
	t.Cleanup(upstream.Close)
	integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AIProvider = api.OutboundAIProviderOpenAI
	// 10 and 20 millicents per 1,000 tokens: a 32,768-token default
	// reservation for a model the catalog does not know costs ~656.
	integration.AIPrices = ResolveAIPrices([]api.OutboundAIPriceEntry{
		{ModelPrefix: "m-test", InputMillicentsPerMTok: 10_000, OutputMillicentsPerMTok: 20_000},
	})
	integration.DailyAICostLimit = int64Ref(dailyCost)
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	return handler, &sentCeiling, &calls
}

func callOutputCap(handler *Handler, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, gatewayRequest("/i/integration-1/v1/chat/completions", "token", "app-1", http.MethodPost, strings.NewReader(body)))
	return recorder
}

// Prod hunt 2026-11-28: with no max_tokens, opus-5.5 reserved USD 2.56 a
// call, so an SDK that omits it was refused under an ordinary daily cap.
func TestCallWithoutMaxTokensIsCappedToTheBudgetLeft(t *testing.T) {
	handler, sent, _ := outputCapTestHandler(t, 300)
	recorder := callOutputCap(handler, `{"model":"m-test","messages":[{"role":"user","content":"hi"}]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	capped, err := strconv.ParseInt(recorder.Header().Get(AIOutputCappedHeader), 10, 64)
	if err != nil || capped < api.OutboundAIBudgetOutputCapMinTokens || capped >= api.OutboundAIUnknownModelOutputReserveTokens {
		t.Fatalf("%s = %q", AIOutputCappedHeader, recorder.Header().Get(AIOutputCappedHeader))
	}
	if got := sent.Load(); got != capped {
		t.Fatalf("provider got an output ceiling of %d, header says %d", got, capped)
	}
	// 300 millicents at 20 per 1,000 output tokens: about 15,000 tokens, less
	// the input; the largest that fits, not a token-wasting guess.
	if capped < 14_000 || capped > 15_000 {
		t.Fatalf("capped at %d, want the largest ceiling the 300 millicents left cover", capped)
	}
}

func TestOutputCapLeavesNamedCeilingsAndTinyBudgetsRefused(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dailyCost int64
		body      string
	}{
		// The caller chose this ceiling; cutting it is not the gateway's call.
		{"named max_tokens", 300, `{"model":"m-test","max_tokens":30000,"messages":[]}`},
		// 4 millicents cover 200 output tokens, under the useful minimum.
		{"budget below the minimum", 4, `{"model":"m-test","messages":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, _, calls := outputCapTestHandler(t, tc.dailyCost)
			recorder := callOutputCap(handler, tc.body)
			if recorder.Code != http.StatusTooManyRequests || !strings.Contains(recorder.Body.String(), CodeAIReservationExceedsBudget) {
				t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
			}
			if recorder.Header().Get(AIOutputCappedHeader) != "" || calls.Load() != 0 {
				t.Fatalf("refused call was capped or sent: header %q, upstream calls %d", recorder.Header().Get(AIOutputCappedHeader), calls.Load())
			}
		})
	}
}

// The cap divides among every answer asked for and also fits token budgets.
func TestCapOutputToBudgetCountsAnswersAndTokens(t *testing.T) {
	integration := Integration{AIProvider: api.OutboundAIProviderOpenAI, AIPrices: ResolveAIPrices([]api.OutboundAIPriceEntry{
		{ModelPrefix: "m-test", InputMillicentsPerMTok: 10_000, OutputMillicentsPerMTok: 20_000},
	})}
	body := `{"model":"m-test","n":4,"messages":[]}`
	ai := &aiRequest{body: []byte(body)}
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/", strings.NewReader(body))
	plan, rejection := planAICall(request, integration, "", "/v1/chat/completions", ai, nil)
	if rejection != nil {
		t.Fatal(rejection)
	}
	left := AIBudgetLeft{Tokens: 10_000, CostMillicents: -1}
	ceiling, ok := capOutputToBudget(request, integration, plan, ai, left)
	if !ok || !left.fits(plan.reserve) {
		t.Fatalf("cap = %d, %t; reserve %+v", ceiling, ok, plan.reserve)
	}
	if ceiling*4 > 10_000 || (ceiling+1)*4+plan.reserve.tokens-ceiling*4 <= 10_000 {
		t.Fatalf("per-answer ceiling %d is not the largest whose 4 answers fit 10,000 tokens (reserve %d)", ceiling, plan.reserve.tokens)
	}
	if !strings.Contains(string(ai.body), `_tokens":`+strconv.FormatInt(ceiling, 10)) {
		t.Fatalf("body %s does not carry the ceiling", ai.body)
	}
}
