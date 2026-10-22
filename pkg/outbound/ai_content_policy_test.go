package outbound

// adr: 997 — the content policy redacts or blocks findings before a request
// reaches the provider, and captured content is always redacted.

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func contentPolicyHandler(t *testing.T, policy api.OutboundAIContentPolicy, capture bool) (*Handler, *MemoryBackend, *[]byte) {
	t.Helper()
	var forwarded []byte
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o","choices":[{"message":{"content":"reach me at bot@example.com"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
	}))
	t.Cleanup(upstream.Close)
	integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AIProvider = api.OutboundAIProviderOpenAI
	integration.AIContentPolicy = policy
	integration.AIRequestLogEnabled, integration.AIRequestLogContent = capture, capture
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	return handler, backend, &forwarded
}

func postPolicyAI(handler *Handler, contentType string, body []byte) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := gatewayRequest("/i/integration-1/v1/chat/completions", "token", "app-1", http.MethodPost, bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestContentPolicyRedactsAndBlocks(t *testing.T) {
	handler, _, forwarded := contentPolicyHandler(t, api.OutboundAIContentPolicy{
		Email: api.OutboundAIContentRedact, Secret: api.OutboundAIContentBlock, BlockedTerms: []string{"Project Falcon"},
	}, false)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"mail ana@example.com about <b>x</b>"},` +
		`{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,QUJD@x.io"}}]}]}`
	recorder := postPolicyAI(handler, "application/json", []byte(body))
	if recorder.Code != http.StatusOK || recorder.Header().Get(AIContentRedactionsHeader) != "email=1" {
		t.Fatalf("redacted call = %d %q %s", recorder.Code, recorder.Header().Get(AIContentRedactionsHeader), recorder.Body.String())
	}
	if got := string(*forwarded); !strings.Contains(got, "mail [REDACTED:email] about <b>x</b>") || strings.Contains(got, "ana@example.com") ||
		!strings.Contains(got, "data:image/png;base64,QUJD@x.io") {
		t.Fatalf("forwarded body = %s", got)
	}
	*forwarded = nil
	for name, blocked := range map[string]string{
		"secret": `{"model":"gpt-4o","messages":[{"role":"user","content":"key AKIAIOSFODNN7EXAMPLE"}]}`,
		"term":   `{"model":"gpt-4o","messages":[{"role":"user","content":"status of project falcon?"}]}`,
	} {
		recorder := postPolicyAI(handler, "application/json", []byte(blocked))
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "outbound_ai_content_blocked") || *forwarded != nil {
			t.Fatalf("%s = %d %s", name, recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), "AKIA") || strings.Contains(recorder.Body.String(), "falcon") {
			t.Fatalf("%s: the problem echoed the content: %s", name, recorder.Body.String())
		}
	}
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	_ = writer.WriteField("model", "gpt-image-1")
	_ = writer.WriteField("prompt", "a card for ana@example.com")
	_ = writer.Close()
	if recorder := postPolicyAI(handler, writer.FormDataContentType(), form.Bytes()); recorder.Code != http.StatusBadRequest {
		t.Fatalf("a form with a finding to redact = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := postPolicyAI(handler, "application/json", []byte(`{"model":`)); recorder.Code != http.StatusBadRequest ||
		!strings.Contains(recorder.Body.String(), "outbound_ai_content_unreadable") {
		t.Fatalf("an unreadable JSON body = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestContentPolicyRefusesBodiesItCannotRead(t *testing.T) {
	handler, _, _ := contentPolicyHandler(t, api.OutboundAIContentPolicy{Email: api.OutboundAIContentBlock}, false)
	large := `{"model":"gpt-4o","pad":"` + strings.Repeat("x", int(api.OutboundAIRequestBufferMaxBytes)) + `"}`
	if recorder := postPolicyAI(handler, "application/json", []byte(large)); recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an unbuffered body under a content policy = %d", recorder.Code)
	}
	open, _, _ := contentPolicyHandler(t, api.OutboundAIContentPolicy{}, false)
	if recorder := postPolicyAI(open, "application/json", []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"ana@example.com"}]}`)); recorder.Code != http.StatusOK ||
		recorder.Header().Get(AIContentRedactionsHeader) != "" {
		t.Fatalf("a call without a policy = %d", recorder.Code)
	}
}

func TestCapturedContentIsAlwaysRedacted(t *testing.T) {
	handler, backend, _ := contentPolicyHandler(t, api.OutboundAIContentPolicy{}, true)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"card 4111 1111 1111 1111"}]}`
	if recorder := postPolicyAI(handler, "application/json", []byte(body)); recorder.Code != http.StatusOK {
		t.Fatalf("call = %d", recorder.Code)
	}
	logged := backend.AIRequestLog("integration-1")
	if len(logged) != 1 || logged[0].Log.Content == nil {
		t.Fatalf("request log = %+v", logged)
	}
	content := logged[0].Log.Content
	if !strings.Contains(string(content.Request), "[REDACTED:payment_card]") || strings.Contains(string(content.Request), "4111") ||
		!strings.Contains(string(content.Response), "[REDACTED:email]") {
		t.Fatalf("captured content = %s / %s", content.Request, content.Response)
	}
	// A secret cut by the capture limit is still redacted.
	secret := "sk-proj-" + strings.Repeat("A", 40)
	cut := []byte(strings.Repeat("x", api.OutboundAIRequestContentMaxBytes-10) + secret)
	capture := newAIContentCapture(&aiRequest{body: cut}, "application/json")
	if bytes.Contains(capture.content.Request, []byte("sk-proj-")) || !capture.content.RequestTruncated ||
		len(capture.content.Request) > api.OutboundAIRequestContentMaxBytes {
		t.Fatalf("a secret at the limit was kept: %q", capture.content.Request[len(capture.content.Request)-20:])
	}
}

func TestContentPolicyValidation(t *testing.T) {
	for name, policy := range map[string]api.OutboundAIContentPolicy{
		"action":       {Email: "mask"},
		"empty term":   {BlockedTerms: []string{""}},
		"padded term":  {BlockedTerms: []string{" x"}},
		"control term": {BlockedTerms: []string{"a\nb"}},
		"duplicate":    {BlockedTerms: []string{"Falcon", "falcon"}},
		"long term":    {BlockedTerms: []string{strings.Repeat("a", api.OutboundAIBlockedTermMaxBytes+1)}},
	} {
		if policy.Valid() {
			t.Errorf("%s: %+v was accepted", name, policy)
		}
	}
	if !(api.OutboundAIContentPolicy{Secret: api.OutboundAIContentBlock, BlockedTerms: []string{"Projekt Falke"}}).Valid() {
		t.Fatal("a valid policy was refused")
	}
	if (api.OutboundAIContentPolicy{Email: api.OutboundAIContentOff}).Active() {
		t.Fatal("an all-off policy is active")
	}
	integration, err := NewIntegration("i", "https://api.example.com", "token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AIContentPolicy = api.OutboundAIContentPolicy{Email: api.OutboundAIContentRedact}
	if err := integration.Validate(); err == nil || !strings.Contains(err.Error(), "content policy") {
		t.Fatalf("a content policy without a provider = %v", err)
	}
}
