package outbound

// adr: 996 — opt-in content capture keeps the start of a logged call's
// request and response bodies, never headers, and only with the request log.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestLoggedCallsCaptureBodiesOnlyWhenOptedIn(t *testing.T) {
	answer := `{"model":"gpt-4o","choices":[{"message":{"content":"` + strings.Repeat("z", api.OutboundAIRequestContentMaxBytes) +
		`"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	defer upstream.Close()
	call := func(content bool) AIUsageRecord {
		t.Helper()
		integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		integration.AIProvider = api.OutboundAIProviderOpenAI
		integration.AIRequestLogEnabled, integration.AIRequestLogContent = true, content
		resolver, err := NewStaticResolver([]Integration{integration})
		if err != nil {
			t.Fatal(err)
		}
		backend := NewMemoryBackend()
		handler, err := NewHandler(resolver, backend, upstream.Client())
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		request := gatewayRequest("/i/integration-1/v1/chat/completions", "token", "app-1", http.MethodPost,
			strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || recorder.Body.String() != answer {
			t.Fatalf("call = %d, %d bytes", recorder.Code, recorder.Body.Len())
		}
		logged := backend.AIRequestLog("integration-1")
		if len(logged) != 1 {
			t.Fatalf("request log = %+v", logged)
		}
		return logged[0]
	}
	if record := call(false); record.Log.Content != nil {
		t.Fatalf("captured without opting in: %+v", record.Log.Content)
	}
	content := call(true).Log.Content
	if content == nil || string(content.Request) != `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}` || content.RequestTruncated ||
		len(content.Response) != api.OutboundAIRequestContentMaxBytes || !content.ResponseTruncated ||
		!bytes.Equal(content.Response, []byte(answer)[:api.OutboundAIRequestContentMaxBytes]) || content.ResponseContentType != "application/json" {
		t.Fatalf("content = %+v", content)
	}
}

func TestContentCaptureBounds(t *testing.T) {
	long := &aiRequest{body: bytes.Repeat([]byte("a"), api.OutboundAIRequestContentMaxBytes+1)}
	capture := newAIContentCapture(long, "text/event-stream\x01")
	if len(capture.content.Request) != api.OutboundAIRequestContentMaxBytes || !capture.content.RequestTruncated || capture.content.ResponseContentType != "" {
		t.Fatalf("long request = %d bytes, truncated %v, type %q", len(capture.content.Request), capture.content.RequestTruncated, capture.content.ResponseContentType)
	}
	long.body[0] = 'b'
	if capture.content.Request[0] != 'a' {
		t.Fatal("the capture shares the request buffer")
	}
	if unbuffered := newAIContentCapture(nil, "application/json"); len(unbuffered.content.Request) != 0 || !unbuffered.content.RequestTruncated {
		t.Fatalf("an unbuffered request = %+v", unbuffered.content)
	}
	capture.Write([]byte("ab"))
	capture.Write(bytes.Repeat([]byte("c"), api.OutboundAIRequestContentMaxBytes))
	capture.Write([]byte("d"))
	if finished := capture.finish(); len(finished.Response) != api.OutboundAIRequestContentMaxBytes || !finished.ResponseTruncated ||
		string(finished.Response[:3]) != "abc" {
		t.Fatalf("response = %d bytes, truncated %v", len(finished.Response), finished.ResponseTruncated)
	}
	if err := validateAIRequestLog(&AIRequestLog{Status: 200, Attempts: 1,
		Content: &AIRequestContent{Response: make([]byte, api.OutboundAIRequestContentMaxBytes+1)}}); err == nil {
		t.Fatal("an oversized capture was accepted")
	}
	integration, err := NewIntegration("integration-1", "https://api.example.com", "token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AIProvider, integration.AIRequestLogContent = api.OutboundAIProviderOpenAI, true
	if err := integration.Validate(); err == nil {
		t.Fatal("content capture without the request log was accepted")
	}
}

func TestMemoryBackendCapsDailyCaptures(t *testing.T) {
	backend := NewMemoryBackend()
	ctx := context.Background()
	record := func() AIUsageRecord {
		return AIUsageRecord{IntegrationID: "integration-1", Caller: "app-1", Outcome: AIUsageOutcomeKnown,
			Usage: AIUsage{Model: "m", Known: true, InputTokens: 1},
			Log:   &AIRequestLog{Status: http.StatusOK, Attempts: 1, Content: &AIRequestContent{Request: []byte("{}")}}}
	}
	for i := 0; i <= api.OutboundAIContentCapturesPerDay; i++ {
		if err := backend.RecordAIUsage(ctx, record()); err != nil {
			t.Fatal(err)
		}
	}
	logged := backend.AIRequestLog("integration-1")
	if len(logged) != api.OutboundAIContentCapturesPerDay+1 || logged[0].Log.Content == nil || logged[len(logged)-1].Log.Content != nil {
		t.Fatalf("captures past the daily cap were kept: %d logged", len(logged))
	}
}
