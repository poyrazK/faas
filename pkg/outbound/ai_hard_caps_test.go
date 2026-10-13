package outbound

// adr: 996 — a billable call whose usage cannot be read settles at its
// reservation, and with per-user limits set a new end user beyond the day's
// tracking cap is refused instead of passing unlimited.

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

func TestUnknownUsageSettlesAtTheReservation(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, ":batchEmbedContents") {
			_, _ = w.Write([]byte(`{"embeddings":[{"values":[0.1,0.2]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"totalTokens":12}`))
	}))
	defer upstream.Close()
	integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AIProvider = api.OutboundAIProviderGemini
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	embed := `{"requests":[{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"` + strings.Repeat("a", 380) + `"}]}}]}`
	call := func(path, body string) int {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, gatewayRequest("/i/integration-1"+path, "token", "app-1", http.MethodPost, strings.NewReader(body)))
		return recorder.Code
	}
	if code := call("/v1beta/models/gemini-embedding-001:batchEmbedContents", embed); code != http.StatusOK {
		t.Fatalf("embed = %d", code)
	}
	want := (int64(len(embed)) + api.OutboundAIReserveBytesPerToken - 1) / api.OutboundAIReserveBytesPerToken
	if got := backend.DailyAITokenCount("integration-1"); got != want {
		t.Fatalf("embedding without usage settled %d tokens, want the %d-token estimate", got, want)
	}
	// Token counting is free at the provider, so unknown usage stays zero.
	if code := call("/v1beta/models/gemini-2.5-flash:countTokens", `{"contents":[]}`); code != http.StatusOK {
		t.Fatalf("countTokens = %d", code)
	}
	if got := backend.DailyAITokenCount("integration-1"); got != want {
		t.Fatalf("countTokens changed the token count to %d", got)
	}
	usage, _, ok := estimatedAIUsage(AIUsageOutcomeError, AIUsage{Model: "m"}, aiSettlement{billable: true, reserve: aiReservation{tokens: 5, estimate: AIUsage{InputTokens: 5}}})
	if ok || usage.InputTokens != 0 {
		t.Fatalf("an error settled the estimate: %+v", usage)
	}
}

func TestNewEndUsersBeyondTheTrackingCapAreRefusedWithLimits(t *testing.T) {
	backend := NewMemoryBackend()
	ctx := context.Background()
	for i := 0; i < api.OutboundAIUsersPerDay; i++ {
		if err := backend.RecordAIUsage(ctx, AIUsageRecord{IntegrationID: "integration-1", Caller: "app-1", Outcome: AIUsageOutcomeKnown,
			Usage: AIUsage{Model: "m", Known: true, InputTokens: 1}, EndUser: "u" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	limit := int64(1_000)
	admit := func(endUser string, limited bool) Decision {
		spec := AdmissionSpec{IntegrationID: "integration-1", RatePerSecond: 1000, Burst: 1000, MaxInFlight: 100,
			AIEndUser: endUser, AIReservedTokens: 10, LeaseTTL: time.Minute}
		if limited {
			spec.UserDailyAITokenLimit = &limit
		}
		decision, err := backend.Admit(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Granted {
			_ = backend.Release(ctx, "integration-1", decision.LeaseID)
		}
		return decision
	}
	if decision := admit("newcomer", true); decision.Granted || decision.Reason != ReasonUserTrackingFull {
		t.Fatalf("untracked user with limits = %+v", decision)
	}
	if decision := admit("u7", true); !decision.Granted {
		t.Fatalf("tracked user = %+v", decision)
	}
	if decision := admit("newcomer", false); !decision.Granted {
		t.Fatalf("untracked user without per-user limits = %+v", decision)
	}
	if decision := admit("", true); !decision.Granted {
		t.Fatalf("call without an end user = %+v", decision)
	}
}
