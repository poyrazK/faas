package outbound

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestUnpricedModelIsRefusedWhileACostCapApplies(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"`+body.Model+`","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3}}`)
	}))
	defer upstream.Close()
	dailyCost := int64(100_000)
	for _, tc := range []struct {
		name            string
		model           string
		costCap         bool
		allowUnmetered  bool
		wantStatus      int
		wantProblemCode string
	}{
		{name: "unpriced under a cost cap", model: "zz-unknown-model", costCap: true, wantStatus: http.StatusForbidden, wantProblemCode: "outbound_ai_model_unpriced"},
		{name: "priced under a cost cap", model: "gpt-4o", costCap: true, wantStatus: http.StatusOK},
		{name: "unpriced with no cost cap", model: "zz-unknown-model", wantStatus: http.StatusOK},
		{name: "unpriced and allowed unmetered", model: "zz-unknown-model", costCap: true, allowUnmetered: true, wantStatus: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			integration.AIProvider = api.OutboundAIProviderOpenAI
			integration.AIAllowUnmetered = tc.allowUnmetered
			if tc.costCap {
				integration.DailyAICostLimit = &dailyCost
			}
			resolver, err := NewStaticResolver([]Integration{integration})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := NewHandler(resolver, NewMemoryBackend(), upstream.Client())
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/i/integration-1/v1/chat/completions",
				strings.NewReader(`{"model":"`+tc.model+`","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(TokenHeader, "token")
			req.Header.Set(AppHeader, "app-1")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus || (tc.wantProblemCode != "" && !strings.Contains(rec.Body.String(), tc.wantProblemCode)) {
				t.Fatalf("status = %d %s; want %d %s", rec.Code, rec.Body.String(), tc.wantStatus, tc.wantProblemCode)
			}
		})
	}
}

func TestSettledCostPricesAnUnpricedAnswerAsTheRequestedModel(t *testing.T) {
	integration := Integration{AIProvider: api.OutboundAIProviderOpenAI}
	usage := AIUsage{Model: "zz-unknown-snapshot", Known: true, InputTokens: 1_000_000, OutputTokens: 1_000_000}
	if _, priced := aiSettledCost(integration, usage, ""); priced {
		t.Fatal("an unknown model with no requested model was priced")
	}
	cost, priced := aiSettledCost(integration, usage, "gpt-4o")
	asked := usage
	asked.Model = "gpt-4o"
	if !priced || cost <= 0 || cost != aiCallCostMillicents(integration, asked) {
		t.Fatalf("cost = %d, %t; want the requested model's price %d", cost, priced, aiCallCostMillicents(integration, asked))
	}
}
