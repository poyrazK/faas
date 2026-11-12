package outbound

// adr: 20261027132936 — amendment 2: long-context and one-hour cache write
// prices, translated output limits and the account-wide AI rate.

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestLongContextPricesTheWholeCall(t *testing.T) {
	price, ok := aiPriceFor(Integration{AIProvider: api.OutboundAIProviderVertex}, "claude-sonnet-4-5")
	if !ok || price.Long == nil || price.LongContextThreshold != 200_000 {
		t.Fatalf("vertex claude-sonnet-4-5 = %+v, %v", price, ok)
	}
	for name, tc := range map[string]struct {
		input, output, want int64
	}{
		// $3 and $15 per million below the threshold, $6 and $22.50 above.
		"at the threshold":    {200_000, 1_000, 60_000 + 1_500},
		"above the threshold": {300_000, 1_000, 180_000 + 2_250},
	} {
		usage := AIUsage{Known: true, InputTokens: tc.input, OutputTokens: tc.output}
		if got := priceCostMillicents(price, usage); got != tc.want {
			t.Errorf("%s: cost = %d, want %d", name, got, tc.want)
		}
	}
}

func TestOneHourCacheWritesUseTheirOwnPrice(t *testing.T) {
	price, ok := aiPriceFor(Integration{AIProvider: api.OutboundAIProviderAnthropic}, "claude-sonnet-4-5")
	if !ok || price.CacheWrite != 375_000 || price.CacheWrite1h != 600_000 {
		t.Fatalf("anthropic claude-sonnet-4-5 = %+v, %v", price, ok)
	}
	for name, tc := range map[string]struct {
		hour, want int64
	}{
		"five-minute writes": {0, 375_000},
		"one-hour writes":    {1_000_000, 600_000},
		"mixed":              {500_000, 187_500 + 300_000},
		"more than written":  {5_000_000, 600_000},
	} {
		usage := AIUsage{Known: true, InputTokens: 1_000_000, CacheWriteTokens: 1_000_000, CacheWrite1hTokens: tc.hour}
		if got := priceCostMillicents(price, usage); got != tc.want {
			t.Errorf("%s: cost = %d, want %d", name, got, tc.want)
		}
	}
	// A price list without a one-hour price charges the base write price.
	listed := ResolveAIPrices([]api.OutboundAIPriceEntry{{ModelPrefix: "m", InputMillicentsPerMTok: 100,
		CacheWriteMillicentsPerMTok: int64Ptr(200)}})
	if len(listed) != 1 || listed[0].CacheWrite1h != listed[0].CacheWrite {
		t.Fatalf("listed = %+v", listed)
	}
}

func TestAnthropicUsageReportsOneHourWrites(t *testing.T) {
	body := `{"type":"message","model":"claude-sonnet-5","content":[],
		"usage":{"input_tokens":10,"cache_creation_input_tokens":5,"cache_creation":{"ephemeral_5m_input_tokens":1,"ephemeral_1h_input_tokens":4},"output_tokens":2}}`
	observer := newAIUsageObserver(api.OutboundAIProviderAnthropic, "application/json", nil)
	_, _ = observer.Write([]byte(body))
	got := observer.Finish()
	if got.CacheWriteTokens != 5 || got.CacheWrite1hTokens != 4 || got.InputTokens != 15 {
		t.Fatalf("usage = %+v", got)
	}
}

func TestTranslatedClaudeCallsGetTheModelOutputLimit(t *testing.T) {
	translate := func(provider, body, model string) map[string]any {
		t.Helper()
		translation, err := translateRouteRequest(Integration{AIProvider: provider}, "/v1/chat/completions", "", []byte(body), model)
		if err != nil {
			t.Fatal(err)
		}
		return decodeJSONMap(t, translation.body)
	}
	plain := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	capped := `{"model":"gpt-4o","max_tokens":20,"messages":[{"role":"user","content":"hi"}]}`
	if got := translate(api.OutboundAIProviderAnthropic, plain, "claude-sonnet-4-5")["max_tokens"]; got != float64(64_000) {
		t.Errorf("anthropic default max_tokens = %v, want the model's 64000", got)
	}
	if got := translate(api.OutboundAIProviderAnthropic, capped, "claude-sonnet-4-5")["max_tokens"]; got != float64(20) {
		t.Errorf("anthropic caller max_tokens = %v, want 20", got)
	}
	if got := translate(api.OutboundAIProviderAnthropic, plain, "claude-unknown-model")["max_tokens"]; got != float64(api.OutboundAIDefaultOutputReserveTokens) {
		t.Errorf("unpriced model max_tokens = %v", got)
	}
	gemini := translate(api.OutboundAIProviderGemini, plain, "gemini-2.5-flash")
	if config, ok := gemini["generationConfig"].(map[string]any); ok && config["maxOutputTokens"] != nil {
		t.Errorf("gemini without a caller limit sets maxOutputTokens = %v", config["maxOutputTokens"])
	}
	bedrock := translate(api.OutboundAIProviderBedrock, plain, "anthropic.claude-sonnet-4-5")
	if config, ok := bedrock["inferenceConfig"].(map[string]any); ok && config["maxTokens"] != nil {
		t.Errorf("bedrock without a caller limit sets maxTokens = %v", config["maxTokens"])
	}
}

func TestAccountAIRateIsSharedAcrossIntegrations(t *testing.T) {
	limits, ok := api.LimitsFor(api.PlanFree)
	if !ok || limits.OutboundAIAccountRatePerSecondMax <= 0 {
		t.Fatalf("free plan has no account AI rate: %+v", limits.OutboundAIAccountRatePerSecondMax)
	}
	burst := 2 * limits.OutboundAIAccountRatePerSecondMax
	var rates aiAccountRates
	now := time.Unix(1_800_000_000, 0)
	for i := range burst {
		if !rates.allow("acct", api.PlanFree, now) {
			t.Fatalf("call %d of the burst refused", i)
		}
	}
	if rates.allow("acct", api.PlanFree, now) {
		t.Fatal("a call past the burst was allowed")
	}
	if !rates.allow("other", api.PlanFree, now) {
		t.Fatal("another account shares the bucket")
	}
	if !rates.allow("acct", api.PlanFree, now.Add(time.Second/time.Duration(limits.OutboundAIAccountRatePerSecondMax))) {
		t.Fatal("the bucket does not refill at the plan rate")
	}
	if !rates.allow("", api.PlanFree, now) || !rates.allow("acct", api.Plan("unknown"), now) {
		t.Fatal("a call without an account or plan ceiling was refused")
	}
}

