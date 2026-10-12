package outbound

// adr: 994 — X-Gregale-AI-User attributes calls to an end user, caps that
// user's day without touching other users, and never reaches the provider.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEndUserDailyLimitsAndAttribution(t *testing.T) {
	var forwarded []string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = append(forwarded, r.Header.Get(api.OutboundAIUserHeader)+r.Header.Get(api.OutboundAISessionHeader))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o","usage":{"prompt_tokens":40,"completion_tokens":10}}`))
	}))
	defer upstream.Close()
	integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AIProvider = api.OutboundAIProviderOpenAI
	integration.AIRequestLogEnabled = true
	integration.AIUserDailyTokenLimit = int64Ref(60)
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	call := func(headers map[string]string) *httptest.ResponseRecorder {
		r := gatewayRequest("/i/integration-1/v1/chat/completions", "token", "app-1", http.MethodPost, strings.NewReader(`{"model":"gpt-4o","max_tokens":5}`))
		for name, value := range headers {
			r.Header.Set(name, value)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		return recorder
	}
	alice := map[string]string{api.OutboundAIUserHeader: "user:alice@example.com", api.OutboundAISessionHeader: "chat-42"}
	if got := call(alice).Code; got != http.StatusOK {
		t.Fatalf("alice's first call = %d", got)
	}
	limited := call(alice)
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("X-Gregale-Outbound-Rejection") != ReasonUserDailyTokenLimit {
		t.Fatalf("alice over her limit = %d %q", limited.Code, limited.Header().Get("X-Gregale-Outbound-Rejection"))
	}
	if got := call(map[string]string{api.OutboundAIUserHeader: "user:bob"}).Code; got != http.StatusOK {
		t.Fatalf("bob was limited by alice's budget: %d", got)
	}
	if got := call(nil).Code; got != http.StatusOK {
		t.Fatalf("a call without an end user = %d", got)
	}
	for _, bad := range []map[string]string{{api.OutboundAIUserHeader: "has space"}, {api.OutboundAISessionHeader: strings.Repeat("s", 65)},
		{api.OutboundAIUserHeader: api.OutboundAIUserOverflow}} {
		if got := call(bad); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "outbound_ai_user_invalid") {
			t.Fatalf("invalid header %v = %d %s", bad, got.Code, got.Body.String())
		}
	}
	for _, value := range forwarded {
		if value != "" {
			t.Fatalf("an AI attribution header reached the provider: %q", value)
		}
	}
	if row, ok := backend.AIUserUsage("integration-1", "user:alice@example.com"); !ok || row.Requests != 1 || row.InputTokens != 40 || row.OutputTokens != 10 || row.CostMillicents == 0 {
		t.Fatalf("alice's rollup = %+v %v", row, ok)
	}
	log := backend.AIRequestLog("integration-1")
	if len(log) != 3 || log[0].EndUser != "user:alice@example.com" || log[1].EndUser != "user:bob" || log[2].EndUser != "" {
		t.Fatalf("request log end users = %+v", log)
	}
	// ADR-995: 50 of each user's 60 tokens crosses that user's 80% alert.
	userAlerts := map[string]int{}
	for _, alert := range backend.AIBudgetAlerts("integration-1") {
		if alert.Budget == api.OutboundAIBudgetUserDailyTokens {
			userAlerts[alert.EndUser] = alert.ThresholdPercent
		}
	}
	if len(userAlerts) != 2 || userAlerts["user:alice@example.com"] != 80 || userAlerts["user:bob"] != 80 {
		t.Fatalf("per-user alerts = %v", userAlerts)
	}
}

func TestMemoryBackendCapsDistinctEndUsers(t *testing.T) {
	backend := NewMemoryBackend()
	record := func(endUser string) {
		if err := backend.RecordAIUsage(context.Background(), AIUsageRecord{IntegrationID: "integration-1", Caller: "app-1",
			Outcome: AIUsageOutcomeKnown, Usage: AIUsage{Model: "m", Known: true, InputTokens: 1}, EndUser: endUser}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < api.OutboundAIUsersPerDay; i++ {
		record("u" + strconv.Itoa(i))
	}
	record("late-user")
	if _, ok := backend.AIUserUsage("integration-1", "late-user"); ok {
		t.Fatal("a user beyond the per-day cap got its own row")
	}
	if row, ok := backend.AIUserUsage("integration-1", api.OutboundAIUserOverflow); !ok || row.Requests != 1 {
		t.Fatalf("overflow row = %+v %v", row, ok)
	}
	if err := backend.RecordAIUsage(context.Background(), AIUsageRecord{IntegrationID: "integration-1", Caller: "app-1",
		Outcome: AIUsageOutcomeKnown, Usage: AIUsage{Model: "m"}, EndUser: "bad user"}); err == nil {
		t.Fatal("an invalid end user was recorded")
	}
}
