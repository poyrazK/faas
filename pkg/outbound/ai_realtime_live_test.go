package outbound

// adr: 20261020161811 — Gemini and Vertex Live sessions pass through
// outboundd byte for byte; the caller's setup names the model, each model
// turn is admitted at its first output and settled from its usageMetadata
// (per turn, the turn's context as input), provider messages are read from
// binary frames, and a refusal closes the session with 1008.

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
)

// liveTurns is what the fake Live provider sends for each of the caller's
// turns, in binary frames as Vertex sends them. The first turn reports
// usage in Vertex's shape with turnComplete, the second in the Gemini API's
// shape after it, with search grounding and video frames.
var liveTurns = [][]string{
	{
		`{"serverContent":{"modelTurn":{"parts":[{"inlineData":{"mimeType":"audio/pcm;rate=24000","data":"AAAA"}}]}}}`,
		`{"serverContent":{"modelTurn":{"parts":[{"inlineData":{"mimeType":"audio/pcm;rate=24000","data":"BBBB"}}]}}}`,
		`{"serverContent":{"turnComplete":true},"usageMetadata":{"promptTokenCount":1000,"candidatesTokenCount":500,` +
			`"totalTokenCount":1500,"promptTokensDetails":[{"modality":"TEXT","tokenCount":600},{"modality":"AUDIO","tokenCount":400}],` +
			`"candidatesTokensDetails":[{"modality":"AUDIO","tokenCount":500}]}}`,
	},
	{
		`{"serverContent":{"outputTranscription":{"text":"Rain"}}}`,
		`{"serverContent":{"modelTurn":{"parts":[{"inlineData":{"mimeType":"audio/pcm;rate=24000","data":"CCCC"}}]},` +
			`"groundingMetadata":{"webSearchQueries":["how do rainbows form"]}}}`,
		`{"serverContent":{"turnComplete":true}}`,
		`{"usageMetadata":{"promptTokenCount":2000,"responseTokenCount":1000,"totalTokenCount":3000,` +
			`"promptTokensDetails":[{"modality":"TEXT","tokenCount":800},{"modality":"AUDIO","tokenCount":1000},{"modality":"VIDEO","tokenCount":200}],` +
			`"responseTokensDetails":[{"modality":"AUDIO","tokenCount":1000}]}}`,
	},
}

type liveFixture struct {
	realtimeFixture
	path string
}

// newLiveFixture serves a Live integration of the provider whose fake
// provider answers setup, then plays one of liveTurns per caller message.
func newLiveFixture(t *testing.T, provider string, configure func(*Integration)) liveFixture {
	t.Helper()
	fixture := liveFixture{realtimeFixture: realtimeFixture{provider: make(chan *http.Request, 1), received: make(chan string, 4)}}
	upgrader := websocket.Upgrader{}
	upstream := newTLSUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		fixture.provider <- r
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, setup, err := conn.ReadMessage()
		if err != nil {
			return
		}
		fixture.received <- string(setup)
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte(`{"setupComplete":{}}`))
		for _, turn := range liveTurns {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			for _, message := range turn {
				if conn.WriteMessage(websocket.BinaryMessage, []byte(message)) != nil {
					return
				}
			}
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"),
			time.Now().Add(time.Second))
		_, _, _ = conn.ReadMessage()
	})
	account, app := uuid.NewString(), uuid.NewString()
	integration := phase9Integration(t, "llm", upstream.URL, provider, account, app)
	integration.AllowedPathPrefixes = []string{"/ws"}
	prices := api.OutboundAIPriceEntry{InputMillicentsPerMTok: 50_000, OutputMillicentsPerMTok: 200_000,
		InputImageMillicentsPerMTok: ptr(int64(100_000)), InputAudioMillicentsPerMTok: ptr(int64(300_000)),
		OutputAudioMillicentsPerMTok: ptr(int64(1_200_000)), MillicentsPerWebSearch: ptr(int64(3_500))}
	gemini, vertex := prices, prices
	gemini.ModelPrefix, vertex.ModelPrefix = "gemini-2.5-flash-native-audio", "gemini-live-2.5-flash-native-audio"
	integration.AIPrices = ResolveAIPrices([]api.OutboundAIPriceEntry{gemini, vertex})
	fixture.path = "/i/llm/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"
	if provider == api.OutboundAIProviderVertex {
		fixture.path = "/i/llm/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent"
	}
	if configure != nil {
		configure(&integration)
	}
	handler, backend := phase9Handler(t, upstream.Client(), integration)
	fixture.backend = backend
	fixture.token = AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}
	fixture.gateway = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(withAccessToken(r.Context(), fixture.token)))
	}))
	t.Cleanup(fixture.gateway.Close)
	return fixture
}

func liveClientTurn(t *testing.T, conn *websocket.Conn, text string) {
	t.Helper()
	turn := `{"clientContent":{"turnComplete":true,"turns":[{"role":"user","parts":[{"text":"` + text + `"}]}]}}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(turn)); err != nil {
		t.Fatal(err)
	}
}

// waitLiveTurns waits for a session's turns to settle: each settles in the
// background as its usage arrives.
func waitLiveTurns(t *testing.T, backend *MemoryBackend, caller, model string, turns int64) AIUsageTotals {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		row, ok := backend.AIUsageRollup("llm", caller, model)
		if ok && row.Requests >= turns {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s rollup after 5s: %+v", model, row)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readLiveClose(t *testing.T, conn *websocket.Conn) *websocket.CloseError {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, message, err := conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		t.Fatalf("got %q, %v instead of a close", message, err)
	}
	return closeErr
}

func TestLiveSessionSettlesEachTurnFromItsUsage(t *testing.T) {
	for _, tc := range []struct {
		provider, model, setupModel, credentialHeader, credential string
	}{
		{api.OutboundAIProviderGemini, "gemini-2.5-flash-native-audio-preview-12-2025",
			"models/gemini-2.5-flash-native-audio-preview-12-2025", "X-Goog-Api-Key", "provider-secret"},
		{api.OutboundAIProviderVertex, "gemini-live-2.5-flash-native-audio",
			"projects/p/locations/us-central1/publishers/google/models/gemini-live-2.5-flash-native-audio", "Authorization", "Bearer provider-secret"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			fixture := newLiveFixture(t, tc.provider, nil)
			conn := fixture.dial(t, fixture.path)
			if upgrade := <-fixture.provider; upgrade.Header.Get(tc.credentialHeader) != tc.credential {
				t.Fatalf("provider handshake headers = %v", upgrade.Header)
			}
			setup := `{"setup":{"model":"` + tc.setupModel + `","generationConfig":{"responseModalities":["AUDIO"]}}}`
			if err := conn.WriteMessage(websocket.TextMessage, []byte(setup)); err != nil {
				t.Fatal(err)
			}
			if got := <-fixture.received; got != setup {
				t.Fatalf("provider received %q", got)
			}
			if kind, got, err := conn.ReadMessage(); err != nil || kind != websocket.BinaryMessage || string(got) != `{"setupComplete":{}}` {
				t.Fatalf("setupComplete = %d %q %v", kind, got, err)
			}
			for i, turn := range liveTurns {
				liveClientTurn(t, conn, "turn")
				for _, want := range turn {
					if got := readRealtime(t, conn); got != want {
						t.Fatalf("turn %d forwarded %s, want %s", i+1, got, want)
					}
				}
			}
			if closeErr := readLiveClose(t, conn); closeErr.Code != websocket.CloseNormalClosure {
				t.Fatalf("session end = %v", closeErr)
			}
			// Turn 1: 600 text at 50, 400 audio at 300, 500 audio out at 1200
			// (millicents per thousand tokens): 30 + 120 + 600. Turn 2: 800
			// text, 1000 audio and 200 video frames at the image price of 100,
			// 1000 audio out, and one grounded prompt at 3500:
			// 40 + 300 + 20 + 1200 + 3500.
			row := waitLiveTurns(t, fixture.backend, fixture.token.AppID, tc.model, 2)
			if row.Requests != 2 || row.InputTokens != 3000 || row.OutputTokens != 1500 || row.InputAudioTokens != 1400 ||
				row.OutputAudioTokens != 1500 || row.WebSearches != 1 || row.UsageUnknownRequests != 0 || row.CostMillicents != 750+5060 {
				t.Fatalf("rollup = %+v", row)
			}
		})
	}
}

func TestLiveTurnRefusedByACapClosesTheSession(t *testing.T) {
	fixture := newLiveFixture(t, api.OutboundAIProviderGemini, func(integration *Integration) {
		integration.DailyAITokenLimit = ptr(int64(1000))
	})
	conn := fixture.dial(t, fixture.path)
	<-fixture.provider
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"setup":{"model":"models/gemini-2.5-flash-native-audio-preview-12-2025"}}`)); err != nil {
		t.Fatal(err)
	}
	<-fixture.received
	readRealtime(t, conn)
	liveClientTurn(t, conn, "hello")
	// The first turn reserves more than the limit: the caller gets close
	// code 1008 naming the refusal, with no message before it.
	if closeErr := readLiveClose(t, conn); closeErr.Code != websocket.ClosePolicyViolation || closeErr.Text != "outbound_budget_exhausted" {
		t.Fatalf("close = %v", closeErr)
	}
	if row, ok := fixture.backend.AIUsageRollup("llm", fixture.token.AppID, "gemini-2.5-flash-native-audio-preview-12-2025"); ok {
		t.Fatalf("a refused turn settled: %+v", row)
	}
}

func TestLiveSessionWithoutAModelInSetupNeverReachesTheProvider(t *testing.T) {
	for _, first := range []string{`{"setup":{}}`, `{"clientContent":{"turnComplete":true}}`, `not json`} {
		fixture := newLiveFixture(t, api.OutboundAIProviderGemini, nil)
		conn := fixture.dial(t, fixture.path)
		<-fixture.provider
		if err := conn.WriteMessage(websocket.TextMessage, []byte(first)); err != nil {
			t.Fatal(err)
		}
		if closeErr := readLiveClose(t, conn); closeErr.Code != websocket.ClosePolicyViolation || closeErr.Text != "outbound_ai_realtime_unreadable" {
			t.Fatalf("%s: close = %v", first, closeErr)
		}
		select {
		case got := <-fixture.received:
			t.Fatalf("%s: provider received %q", first, got)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestLiveSetupModelAndCallerFrames(t *testing.T) {
	for _, tc := range []struct{ setup, want string }{
		{`{"setup":{"model":"models/gemini-2.5-flash-native-audio-preview-12-2025"}}`, "gemini-2.5-flash-native-audio-preview-12-2025"},
		{`{"setup":{"model":"projects/p/locations/l/publishers/google/models/gemini-live-2.5-flash-native-audio"}}`,
			"gemini-live-2.5-flash-native-audio"},
		{`{"setup":{"model":"gemini-3.1-flash-live-preview"}}`, "gemini-3.1-flash-live-preview"},
		{`{"setup":{"model":"models/"}}`, ""},
		{`{"setup":{}}`, ""},
		{`{"realtimeInput":{}}`, ""},
	} {
		if got := liveSetupModel([]byte(tc.setup)); got != tc.want {
			t.Errorf("liveSetupModel(%s) = %q, want %q", tc.setup, got, tc.want)
		}
	}
	// A caller's frame is masked: unmasked reads the payload as written
	// and leaves the forwarded frame's bytes alone.
	key := []byte{0x11, 0x22, 0x33, 0x44}
	payload := []byte(`{"setup":{}}`)
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ key[i%4]
	}
	frame := wsFrame{fin: true, opcode: wsOpBinary, header: append([]byte{0x82, 0x80 | byte(len(payload))}, key...), payload: masked}
	if got := frame.unmasked(); !bytes.Equal(got.payload, payload) || !bytes.Equal(frame.payload, masked) {
		t.Fatalf("unmasked %q, frame %q", got.payload, frame.payload)
	}
	// Binary messages reassemble only when the session reads them.
	for _, binary := range []bool{false, true} {
		message := wsMessage{maxSize: 1 << 20, binary: binary}
		got, err := message.add(wsFrame{fin: true, opcode: wsOpBinary, payload: payload})
		if err != nil || (got != nil) != binary {
			t.Errorf("binary=%v: %q, %v", binary, got, err)
		}
	}
}
