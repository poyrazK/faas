package outbound

// adr: 991 — budget alerts at 80% and 100%, and the opt-in request log.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCrossedAIBudgetThresholds(t *testing.T) {
	limit := int64(1000)
	for _, tc := range []struct {
		before, after int64
		want          []int
	}{
		{0, 799, nil}, {0, 800, []int{80}}, {799, 1000, []int{80, 100}}, {800, 1500, []int{100}}, {1000, 2000, nil},
	} {
		got := crossedAIBudgetThresholds(&limit, tc.before, tc.after)
		if len(got) != len(tc.want) || (len(got) > 0 && got[len(got)-1] != tc.want[len(tc.want)-1]) {
			t.Errorf("(%d, %d] = %v, want %v", tc.before, tc.after, got, tc.want)
		}
	}
	odd := int64(7)
	if got := crossedAIBudgetThresholds(&odd, 5, 6); len(got) != 1 || got[0] != 80 {
		t.Fatalf("80%% of 7 rounds up to 6: %v", got)
	}
	if crossedAIBudgetThresholds(nil, 0, 100) != nil {
		t.Fatal("an unlimited budget alerted")
	}
}

func TestSettlementRecordsBudgetAlertsAndRequestLog(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o","usage":{"prompt_tokens":300,"completion_tokens":100}}`))
	}))
	defer upstream.Close()
	integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AIProvider = api.OutboundAIProviderOpenAI
	integration.DailyAITokenLimit = int64Ref(1000)
	integration.BindingAppIDs = map[string]struct{}{"app-1": {}}
	integration.BindingDailyAITokenLimits = map[string]*int64{"app-1": int64Ref(5000)}
	integration.AIRequestLogEnabled = true
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, gatewayRequest("/i/integration-1/v1/chat/completions", "token", "app-1", http.MethodPost, strings.NewReader(`{"max_tokens":5}`)))
		if recorder.Code != http.StatusOK {
			t.Fatalf("call %d = %d %s", i, recorder.Code, recorder.Body.String())
		}
	}
	alerts := backend.AIBudgetAlerts("integration-1")
	if len(alerts) != 2 || alerts[0].Budget != api.OutboundAIBudgetDailyTokens || alerts[0].ThresholdPercent != 80 || alerts[0].Used != 800 ||
		alerts[1].ThresholdPercent != 100 || alerts[1].Used != 1200 || alerts[1].Limit != 1000 {
		t.Fatalf("alerts = %+v", alerts)
	}
	logged := backend.AIRequestLog("integration-1")
	if len(logged) != 3 || logged[0].Log.Status != http.StatusOK || logged[0].Log.Attempts != 1 || logged[0].Usage.Model != "gpt-4o" ||
		logged[0].Caller != "app-1" {
		t.Fatalf("request log = %+v", logged)
	}
	// A fourth call: settled usage is at the limit, so admission refuses it
	// and nothing more is alerted or logged.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, gatewayRequest("/i/integration-1/v1/chat/completions", "token", "app-1", http.MethodPost, strings.NewReader(`{"max_tokens":5}`)))
	if recorder.Code != http.StatusTooManyRequests || len(backend.AIBudgetAlerts("integration-1")) != 2 || len(backend.AIRequestLog("integration-1")) != 3 {
		t.Fatalf("over-limit call = %d", recorder.Code)
	}
}

func TestAppBudgetAlertNamesTheApp(t *testing.T) {
	backend := NewMemoryBackend()
	spec := AdmissionSpec{IntegrationID: "integration-1", RatePerSecond: 10, Burst: 10, MaxInFlight: 10, LeaseTTL: time.Minute,
		BindingAppID: "app-1", BindingDailyAITokenLimit: int64Ref(100)}
	if decision, err := backend.Admit(t.Context(), spec); err != nil || !decision.Granted {
		t.Fatalf("admit = %+v %v", decision, err)
	}
	alerts, err := backend.RecordAIUsageAlerts(t.Context(), AIUsageRecord{IntegrationID: "integration-1", Caller: "app-1",
		Outcome: AIUsageOutcomeKnown, Usage: AIUsage{Model: "m", Known: true, InputTokens: 90}})
	if err != nil || len(alerts) != 1 || alerts[0].Budget != api.OutboundAIBudgetAppDailyTokens || alerts[0].AppID != "app-1" || alerts[0].ThresholdPercent != 80 {
		t.Fatalf("app alert = %+v %v", alerts, err)
	}
	if again, _ := backend.RecordAIUsageAlerts(t.Context(), AIUsageRecord{IntegrationID: "integration-1", Caller: "app-1",
		Outcome: AIUsageOutcomeKnown, Usage: AIUsage{Model: "m", Known: true, InputTokens: 1}}); len(again) != 0 {
		t.Fatalf("a threshold alerted twice: %+v", again)
	}
}
