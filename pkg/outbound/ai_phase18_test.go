package outbound

// adr: 20261019072722 — OpenAI Realtime sessions pass through outboundd
// byte for byte; each response is admitted against the caps and settled
// from its usage with audio tokens at audio prices, a refused response ends
// the session with an error event and close code 1008, and upgrades the
// gateway cannot meter are refused unless the AI policy allows them.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/trace"
)

// realtimeProviderScript is what the fake provider sends after the caller's
// first message.
var realtimeProviderScript = []string{
	`{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`,
	`{"type":"response.output_audio.delta","response_id":"resp_1","delta":"AAAA"}`,
	`{"type":"response.done","response":{"id":"resp_1","status":"completed","usage":{"total_tokens":1500,"input_tokens":1000,` +
		`"output_tokens":500,"input_token_details":{"cached_tokens":200,"text_tokens":300,"audio_tokens":700,` +
		`"cached_tokens_details":{"text_tokens":100,"audio_tokens":100}},"output_token_details":{"text_tokens":100,"audio_tokens":400}}}}`,
	`{"type":"conversation.item.input_audio_transcription.completed","item_id":"item_1","transcript":"hi",` +
		`"usage":{"type":"tokens","input_tokens":50,"output_tokens":10,"total_tokens":60,"input_token_details":{"audio_tokens":50}}}`,
}

type realtimeFixture struct {
	gateway  *httptest.Server
	backend  *MemoryBackend
	token    AIAccessToken
	provider chan *http.Request
	received chan string
}

// newRealtimeFixture serves an OpenAI integration whose provider runs the
// script, behind a gateway that takes every caller as the token's app.
func newRealtimeFixture(t *testing.T, configure func(*Integration), wrap ...func(http.Handler) http.Handler) realtimeFixture {
	t.Helper()
	fixture := realtimeFixture{provider: make(chan *http.Request, 1), received: make(chan string, 4)}
	upgrader := websocket.Upgrader{}
	upstream := newTLSUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		fixture.provider <- r
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.created","session":{"model":"gpt-realtime",`+
			`"audio":{"input":{"transcription":{"model":"gpt-4o-transcribe"}}}}}`))
		_, message, err := conn.ReadMessage()
		if err != nil {
			return
		}
		fixture.received <- string(message)
		for _, event := range realtimeProviderScript {
			if conn.WriteMessage(websocket.TextMessage, []byte(event)) != nil {
				return
			}
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"),
			time.Now().Add(time.Second))
		_, _, _ = conn.ReadMessage()
	})
	account, app := uuid.NewString(), uuid.NewString()
	integration := phase9Integration(t, "llm", upstream.URL, api.OutboundAIProviderOpenAI, account, app)
	integration.AIPrices = ResolveAIPrices([]api.OutboundAIPriceEntry{
		{ModelPrefix: "gpt-realtime", InputMillicentsPerMTok: 1_000_000, CachedInputMillicentsPerMTok: ptr(int64(100_000)),
			OutputMillicentsPerMTok: 2_000_000, InputAudioMillicentsPerMTok: ptr(int64(8_000_000)),
			CachedInputAudioMillicentsPerMTok: ptr(int64(400_000)), OutputAudioMillicentsPerMTok: ptr(int64(16_000_000))},
		{ModelPrefix: "gpt-4o-transcribe", InputMillicentsPerMTok: 1_000_000, OutputMillicentsPerMTok: 2_000_000,
			InputAudioMillicentsPerMTok: ptr(int64(6_000_000))},
	})
	if configure != nil {
		configure(&integration)
	}
	handler, backend := phase9Handler(t, upstream.Client(), integration)
	fixture.backend = backend
	fixture.token = AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}
	var served http.Handler = handler
	for _, layer := range wrap {
		served = layer(served)
	}
	fixture.gateway = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.ServeHTTP(w, r.WithContext(withAccessToken(r.Context(), fixture.token)))
	}))
	t.Cleanup(fixture.gateway.Close)
	return fixture
}

func (f realtimeFixture) dial(t *testing.T, path string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.gateway.URL, "http")+path, nil)
	status, body := 0, ""
	if resp != nil {
		status = resp.StatusCode
		raw, _ := io.ReadAll(resp.Body)
		body = string(raw)
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial %s: %v (status %d: %s)", path, err, status, body)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readRealtime(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, message, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(message)
}

// waitRollup waits for a session's settlements, which finish as it ends.
func waitRollup(t *testing.T, backend *MemoryBackend, caller, model string) AIUsageTotals {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if row, ok := backend.AIUsageRollup("llm", caller, model); ok {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s rollup", model)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRealtimeSessionSettlesEachResponseAtAudioPrices(t *testing.T) {
	fixture := newRealtimeFixture(t, nil)
	conn := fixture.dial(t, "/i/llm/v1/realtime?model=gpt-realtime")
	upgrade := <-fixture.provider
	if upgrade.Header.Get("Authorization") != "Bearer provider-secret" || upgrade.Header.Get("Sec-WebSocket-Extensions") != "" {
		t.Fatalf("provider handshake headers = %v", upgrade.Header)
	}
	if got := readRealtime(t, conn); !strings.Contains(got, `"session.created"`) {
		t.Fatalf("first event = %s", got)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create"}`)); err != nil {
		t.Fatal(err)
	}
	if got := <-fixture.received; got != `{"type":"response.create"}` {
		t.Fatalf("provider received %q", got)
	}
	for _, want := range realtimeProviderScript {
		if got := readRealtime(t, conn); got != want {
			t.Fatalf("forwarded %s, want %s", got, want)
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatalf("session end = %v", err)
	}
	// 600 uncached audio at 8, 200 text at 1, 100 cached text at 0.1, 100
	// cached audio at 0.4, 100 text out at 2 and 400 audio out at 16 (in
	// millicents per thousand tokens): 4800 + 200 + 10 + 40 + 200 + 6400.
	row := waitRollup(t, fixture.backend, fixture.token.AppID, "gpt-realtime")
	if row.Requests != 1 || row.InputTokens != 1000 || row.OutputTokens != 500 || row.InputAudioTokens != 700 ||
		row.OutputAudioTokens != 400 || row.CostMillicents != 11650 {
		t.Fatalf("response rollup = %+v", row)
	}
	transcription := waitRollup(t, fixture.backend, fixture.token.AppID, "gpt-4o-transcribe")
	if transcription.Requests != 1 || transcription.InputAudioTokens != 50 || transcription.CostMillicents != 320 {
		t.Fatalf("transcription rollup = %+v", transcription)
	}
}

// The relay and public listeners serve the handler through otelhttp; the
// session must still take over the connection.
func TestRealtimeSessionThroughTheTracingMiddleware(t *testing.T) {
	fixture := newRealtimeFixture(t, nil, func(next http.Handler) http.Handler { return trace.HTTPHandler("outboundd-relay", next) })
	conn := fixture.dial(t, "/i/llm/v1/realtime?model=gpt-realtime")
	<-fixture.provider
	readRealtime(t, conn)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create"}`)); err != nil {
		t.Fatal(err)
	}
	for _, want := range realtimeProviderScript {
		if got := readRealtime(t, conn); got != want {
			t.Fatalf("forwarded %s, want %s", got, want)
		}
	}
	if row := waitRollup(t, fixture.backend, fixture.token.AppID, "gpt-realtime"); row.Requests != 1 {
		t.Fatalf("rollup = %+v", row)
	}
}

func TestRealtimeResponseRefusedByACapEndsTheSession(t *testing.T) {
	fixture := newRealtimeFixture(t, func(integration *Integration) {
		integration.DailyAITokenLimit = ptr(int64(1000))
	})
	conn := fixture.dial(t, "/i/llm/v1/realtime?model=gpt-realtime")
	<-fixture.provider
	readRealtime(t, conn)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create"}`)); err != nil {
		t.Fatal(err)
	}
	// The first response reserves more than the limit: the caller gets an
	// error event and close code 1008 instead of response.created.
	refusal := readRealtime(t, conn)
	var event struct {
		Type  string `json:"type"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(refusal), &event) != nil || event.Type != "error" || event.Error.Code != "outbound_budget_exhausted" {
		t.Fatalf("refusal = %s", refusal)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err := conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.ClosePolicyViolation || closeErr.Text != "outbound_budget_exhausted" {
		t.Fatalf("close = %v", err)
	}
	if row, ok := fixture.backend.AIUsageRollup("llm", fixture.token.AppID, "gpt-realtime"); ok {
		t.Fatalf("a refused response settled: %+v", row)
	}
}

func TestUnmeteredRealtimeUpgradesPassWhenAllowed(t *testing.T) {
	fixture := newRealtimeFixture(t, func(integration *Integration) {
		integration.AIAllowUnmetered = true
	})
	conn := fixture.dial(t, "/i/llm/v1/other-live")
	<-fixture.provider
	readRealtime(t, conn)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create"}`)); err != nil {
		t.Fatal(err)
	}
	for range realtimeProviderScript {
		readRealtime(t, conn)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, _ = conn.ReadMessage()
	time.Sleep(50 * time.Millisecond)
	if row, ok := fixture.backend.AIUsageRollup("llm", fixture.token.AppID, "gpt-realtime"); ok {
		t.Fatalf("an unmetered session settled: %+v", row)
	}
}

func TestRealtimeUpgradesTheGatewayCannotMeterAreRefused(t *testing.T) {
	upgrade := func(handler http.Handler, path string, token AIAccessToken) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Connection", "Upgrade")
		request.Header.Set("Upgrade", "websocket")
		request.Header.Set("Sec-WebSocket-Version", "13")
		request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		request = request.WithContext(withAccessToken(request.Context(), token))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	for _, tc := range []struct {
		name      string
		path      string
		configure func(*Integration)
		code      string
	}{
		{"unknown socket", "/i/llm/v1/other-live", nil, "outbound_ai_endpoint_unmetered"},
		{"content policy", "/i/llm/v1/realtime", func(i *Integration) { i.AIContentPolicy.Email = api.OutboundAIContentRedact },
			"outbound_ai_realtime_policy_unenforceable"},
		{"response checks", "/i/llm/v1/realtime", func(i *Integration) { i.AIContentPolicy.Response.Secret = api.OutboundAIContentBlock },
			"outbound_ai_realtime_policy_unenforceable"},
	} {
		account, app := uuid.NewString(), uuid.NewString()
		integration := phase9Integration(t, "llm", "https://provider.invalid", api.OutboundAIProviderOpenAI, account, app)
		if tc.configure != nil {
			tc.configure(&integration)
		}
		handler, _ := phase9Handler(t, http.DefaultClient, integration)
		token := AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}
		got := upgrade(handler, tc.path, token)
		if got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), tc.code) {
			t.Errorf("%s: %d %s", tc.name, got.Code, got.Body.String())
		}
	}
	for _, tc := range []struct {
		provider, path string
		want           bool
	}{
		{api.OutboundAIProviderOpenAI, "/v1/realtime", true},
		{api.OutboundAIProviderOpenAI, "/v1/realtime/calls", false},
		{api.OutboundAIProviderAzureOpenAI, "/openai/v1/realtime", true},
		{api.OutboundAIProviderAzureOpenAI, "/openai/realtime", true},
		// adr: 20261020161811 — Live sessions are metered, its ephemeral-token
		// variant is not.
		{api.OutboundAIProviderGemini, "/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent", true},
		{api.OutboundAIProviderGemini, "/ws/google.ai.generativelanguage.v1alpha.GenerativeService.BidiGenerateContent", true},
		{api.OutboundAIProviderGemini, "/ws/google.ai.generativelanguage.v1alpha.GenerativeService.BidiGenerateContentConstrained", false},
		{api.OutboundAIProviderVertex, "/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent", true},
		{api.OutboundAIProviderVertex, "/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent", true},
		{api.OutboundAIProviderVertex, "/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent", false},
		{api.OutboundAIProviderOpenAI, "/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent", false},
	} {
		if got := realtimePath(tc.provider, tc.path); got != tc.want {
			t.Errorf("realtimePath(%s, %s) = %v", tc.provider, tc.path, got)
		}
	}
}

func TestWebSocketFrames(t *testing.T) {
	for _, size := range []int{0, 125, 126, 70_000} {
		payload := bytes.Repeat([]byte("a"), size)
		frame, err := readWSFrame(bufio.NewReader(bytes.NewReader(wsServerFrame(wsOpText, payload))), 1<<20)
		if err != nil || !frame.fin || frame.opcode != wsOpText || !bytes.Equal(frame.payload, payload) ||
			!bytes.Equal(append(frame.header, frame.payload...), wsServerFrame(wsOpText, payload)) {
			t.Fatalf("size %d: %+v, %v", size, frame.header, err)
		}
	}
	if _, err := readWSFrame(bufio.NewReader(bytes.NewReader(wsServerFrame(wsOpText, make([]byte, 200)))), 100); !errors.Is(err, errWSFrameTooLarge) {
		t.Fatalf("oversized frame: %v", err)
	}
	compressed := wsServerFrame(wsOpText, []byte("x"))
	compressed[0] |= 0x40
	if _, err := readWSFrame(bufio.NewReader(bytes.NewReader(compressed)), 100); !errors.Is(err, errWSCompressed) {
		t.Fatalf("compressed frame: %v", err)
	}
	closeFrame := wsCloseFrame(wsClosePolicyViolation, strings.Repeat("r", 200))
	frame, err := readWSFrame(bufio.NewReader(bytes.NewReader(closeFrame)), 1<<20)
	if err != nil || frame.opcode != wsOpClose || binary.BigEndian.Uint16(frame.payload) != wsClosePolicyViolation || len(frame.payload) != 125 {
		t.Fatalf("close frame: %+v, %v", frame, err)
	}
	// A fragmented message with a ping between its fragments.
	message := wsMessage{maxSize: 1 << 20}
	parts := []wsFrame{{opcode: wsOpText, payload: []byte(`{"type":`)}, {fin: true, opcode: wsOpPing},
		{fin: true, opcode: wsOpContinuation, payload: []byte(`"x"}`)}}
	var got []byte
	for _, part := range parts {
		out, err := message.add(part)
		if err != nil {
			t.Fatal(err)
		}
		if out != nil {
			got = out
		}
	}
	if string(got) != `{"type":"x"}` {
		t.Fatalf("reassembled %q", got)
	}
}

func TestAudioUsageIsReadAndPricedApart(t *testing.T) {
	var raw aiRawUsage
	raw.merge(&aiUsageFields{PromptTokens: ptr(int64(1000)), CompletionTokens: ptr(int64(500)),
		PromptTokensDetails: &struct {
			CachedTokens *int64 `json:"cached_tokens"`
			AudioTokens  *int64 `json:"audio_tokens"`
		}{AudioTokens: ptr(int64(800))},
		CompletionTokensDetails: &struct {
			AudioTokens *int64 `json:"audio_tokens"`
		}{AudioTokens: ptr(int64(300))}})
	usage := raw.normalize(aiFormatOpenAI, aiMediaNone)
	if usage.InputAudioTokens != 800 || usage.OutputAudioTokens != 300 {
		t.Fatalf("chat completions audio usage = %+v", usage)
	}
	usage.Known = true
	text := AIPrice{Input: 1_000_000, CachedInput: 1_000_000, Output: 2_000_000}
	resolveAudioPrices(&text, nil, nil, nil)
	audio := text
	resolveAudioPrices(&audio, ptr(int64(8_000_000)), nil, ptr(int64(16_000_000)))
	// Without audio prices audio tokens cost as text: 1000 + 1000.
	if got := priceCostMillicents(text, usage); got != 2000 {
		t.Fatalf("text-priced cost = %d", got)
	}
	// 200 text at 1, 800 audio at 8, 200 text out at 2, 300 audio out at 16.
	if got := priceCostMillicents(audio, usage); got != 200+6400+400+4800 {
		t.Fatalf("audio-priced cost = %d", got)
	}
}
