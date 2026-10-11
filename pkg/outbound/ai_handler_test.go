package outbound

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func aiTestHandler(t *testing.T, upstream *httptest.Server, provider string, dailyTokenLimit *int64) (*Handler, *MemoryBackend) {
	t.Helper()
	integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AIProvider = provider
	integration.DailyAITokenLimit = dailyTokenLimit
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	return handler, backend
}

func TestHandlerFlushesEachEventStreamChunk(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte("data: {\"model\":\"m\",\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n"))
	}))
	defer upstream.Close()
	defer close(release)
	handler, backend := aiTestHandler(t, upstream, api.OutboundAIProviderOpenAI, nil)
	gateway := httptest.NewServer(handler)
	defer gateway.Close()

	req, err := http.NewRequest(http.MethodPost, gateway.URL+"/i/integration-1/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(TokenHeader, "token")
	req.Header.Set(AppHeader, "app-1")
	resp, err := gateway.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	firstLine := make(chan string, 1)
	reader := bufio.NewReader(resp.Body)
	go func() {
		line, _ := reader.ReadString('\n')
		firstLine <- line
	}()
	select {
	case line := <-firstLine:
		if !strings.Contains(line, "first") {
			t.Fatalf("first streamed line = %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first event was held by the gateway until the provider finished")
	}
	release <- struct{}{}
	rest, err := readAllString(reader)
	if err != nil || !strings.Contains(rest, "[DONE]") {
		t.Fatalf("remaining stream = %q, %v", rest, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for backend.DailyAITokenCount("integration-1") != 5 {
		if time.Now().After(deadline) {
			t.Fatalf("streamed usage was not settled: %d tokens", backend.DailyAITokenCount("integration-1"))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readAllString(reader *bufio.Reader) (string, error) {
	var b strings.Builder
	_, err := reader.WriteTo(&b)
	return b.String(), err
}

func TestHandlerSettlesAITokensAndEnforcesDailyTokenLimit(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"message","model":"claude-sonnet-5","usage":{"input_tokens":50,"output_tokens":30}}`))
	}))
	defer upstream.Close()
	limit := int64(100)
	handler, backend := aiTestHandler(t, upstream, api.OutboundAIProviderAnthropic, &limit)
	call := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, gatewayRequest("/i/integration-1/v1/messages", "token", "app-1", http.MethodPost, strings.NewReader(`{"max_tokens":10}`)))
		return recorder
	}
	// Each call reserves 5 input + 10 output tokens (ADR-984), which still
	// fits below the 100-token limit until settled usage reaches it.
	for i, wantTokens := range []int64{80, 160} {
		if recorder := call(); recorder.Code != http.StatusOK {
			t.Fatalf("call %d status = %d body=%s", i+1, recorder.Code, recorder.Body.String())
		}
		if got := backend.DailyAITokenCount("integration-1"); got != wantTokens {
			t.Fatalf("after call %d tokens = %d, want %d", i+1, got, wantTokens)
		}
	}
	rejected := call()
	if rejected.Code != http.StatusTooManyRequests || rejected.Header().Get("X-Gregale-Outbound-Rejection") != ReasonDailyTokenLimit {
		t.Fatalf("over-limit call = %d %q", rejected.Code, rejected.Header().Get("X-Gregale-Outbound-Rejection"))
	}
	row, ok := backend.AIUsageRollup("integration-1", "app-1", "claude-sonnet-5")
	if !ok || row.Requests != 2 || row.InputTokens != 100 || row.OutputTokens != 60 || row.ErrorRequests != 0 {
		t.Fatalf("rollup = %+v, %v", row, ok)
	}
}

func TestHandlerRecordsAIErrorsWithoutTokensAndSkipsReads(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusInternalServerError)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"model":"gpt-4o","usage":{"prompt_tokens":40,"completion_tokens":2}}`))
	}))
	defer upstream.Close()
	handler, backend := aiTestHandler(t, upstream, api.OutboundAIProviderOpenAI, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, gatewayRequest("/i/integration-1/v1/chat/completions", "token", "app-1", http.MethodPost, strings.NewReader(`{}`)))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("provider status was not passed through: %d", recorder.Code)
	}
	row, ok := backend.AIUsageRollup("integration-1", "app-1", "gpt-4o")
	if !ok || row.Requests != 1 || row.ErrorRequests != 1 || row.InputTokens != 0 || backend.DailyAITokenCount("integration-1") != 0 {
		t.Fatalf("error call rollup = %+v, %v; tokens=%d", row, ok, backend.DailyAITokenCount("integration-1"))
	}
	status.Store(http.StatusOK)
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, gatewayRequest("/i/integration-1/v1/models", "token", "app-1", http.MethodGet, nil))
	if read.Code != http.StatusOK {
		t.Fatalf("GET status = %d", read.Code)
	}
	if row, _ := backend.AIUsageRollup("integration-1", "app-1", "gpt-4o"); row.Requests != 1 {
		t.Fatalf("GET was recorded as an AI call: %+v", row)
	}
}

func TestMemoryBackendCapsDistinctAIModelsPerDay(t *testing.T) {
	backend := NewMemoryBackend()
	for i := 0; i < api.OutboundAIUsageModelsPerDay+5; i++ {
		model := "model-" + strings.Repeat("x", i)
		if err := backend.RecordAIUsage(t.Context(), AIUsageRecord{
			IntegrationID: "integration-1", Caller: "app-1", Outcome: AIUsageOutcomeKnown,
			Usage: AIUsage{Model: model, Known: true, InputTokens: 1, OutputTokens: 1},
		}); err != nil {
			t.Fatal(err)
		}
	}
	row, ok := backend.AIUsageRollup("integration-1", "app-1", aiUsageOverflowModel)
	if !ok || row.Requests != 5 {
		t.Fatalf("overflow rollup = %+v, %v", row, ok)
	}
	if got := backend.DailyAITokenCount("integration-1"); got != int64(2*(api.OutboundAIUsageModelsPerDay+5)) {
		t.Fatalf("token counter = %d", got)
	}
}
