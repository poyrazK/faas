package outbound

// adr: 20261019072722, 20261020161811 — an opt-in probe that settled what
// Gemini Live's usageMetadata counts, so Live sessions could be metered: it
// runs one short session against the real API and reports every usage
// message per turn.
//
// Gemini API (key in GEMINI_API_KEY):
//
//	FAAS_GEMINI_LIVE_PROBE=1 GEMINI_API_KEY=... go test -run TestGeminiLiveUsageProbe -v ./pkg/outbound/
//
// Vertex AI (FAAS_LIVE_PROBE_BACKEND=vertex, an OAuth token from
// `gcloud auth print-access-token`, VERTEX_PROJECT and VERTEX_LOCATION):
//
//	FAAS_GEMINI_LIVE_PROBE=1 FAAS_LIVE_PROBE_BACKEND=vertex VERTEX_ACCESS_TOKEN=... \
//	  VERTEX_PROJECT=... VERTEX_LOCATION=us-central1 go test -run TestGeminiLiveUsageProbe -v ./pkg/outbound/
//
// FAAS_LIVE_PROBE_MODEL picks the model and FAAS_LIVE_PROBE_MODALITY the
// response modality (AUDIO by default; TEXT for half-cascade models). The
// session asks for a one-word answer, a fifty-word one, then a one-word one:
// a session-so-far total never shrinks, so responseTokenCount falling on the
// third turn means the counts are per turn. It costs well under a cent.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
)

type liveProbeMessage struct {
	SetupComplete *json.RawMessage `json:"setupComplete"`
	ServerContent *struct {
		TurnComplete bool `json:"turnComplete"`
	} `json:"serverContent"`
	UsageMetadata *struct {
		PromptTokenCount     int64 `json:"promptTokenCount"`
		ResponseTokenCount   int64 `json:"responseTokenCount"`
		CandidatesTokenCount int64 `json:"candidatesTokenCount"`
		TotalTokenCount      int64 `json:"totalTokenCount"`
		ThoughtsTokenCount   int64 `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
	// rawUsage keeps the whole usageMetadata, including the per-modality
	// token details, because audio and text tokens are priced apart.
	rawUsage json.RawMessage
	// binary marks a message that came in binary frames: a proxy that
	// meters only text messages would miss it.
	binary bool
}

func TestGeminiLiveUsageProbe(t *testing.T) {
	if os.Getenv("FAAS_GEMINI_LIVE_PROBE") != "1" {
		t.Skip("set FAAS_GEMINI_LIVE_PROBE=1 and a key to run against the real Live API")
	}
	target, header, model := liveProbeTarget(t)
	totals := runLiveProbe(t, target, header, model)
	if totals.shrank {
		t.Logf("VERDICT: responseTokenCount fell between messages, so usageMetadata is per turn (or per message), not a session total")
	} else {
		t.Logf("VERDICT: responseTokenCount never fell; with turns of very different lengths this means session-so-far totals")
	}
}

// adr: 20261020161811 — the same session through outboundd's handler,
// which holds the provider credential: what the gateway settles must be
// what the provider reported, turn by turn.
//
//	FAAS_GEMINI_LIVE_PROBE=1 FAAS_LIVE_PROBE_GATEWAY=1 (credentials as above) \
//	  go test -run TestGeminiLiveThroughTheGatewayProbe -v ./pkg/outbound/
func TestGeminiLiveThroughTheGatewayProbe(t *testing.T) {
	if os.Getenv("FAAS_GEMINI_LIVE_PROBE") != "1" || os.Getenv("FAAS_LIVE_PROBE_GATEWAY") != "1" {
		t.Skip("set FAAS_GEMINI_LIVE_PROBE=1, FAAS_LIVE_PROBE_GATEWAY=1 and a key to run a real Live session through the gateway")
	}
	target, header, model := liveProbeTarget(t)
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	provider, credential := api.OutboundAIProviderGemini, "Bearer "+header.Get("X-Goog-Api-Key")
	if os.Getenv("FAAS_LIVE_PROBE_BACKEND") == "vertex" {
		provider, credential = api.OutboundAIProviderVertex, header.Get("Authorization")
	}
	account, app := uuid.NewString(), uuid.NewString()
	integration := phase9Integration(t, "live", "https://"+parsed.Host, provider, account, app)
	integration.AllowedPathPrefixes, integration.AIPrices = []string{"/ws"}, nil
	handler, backend := phase9Handler(t, http.DefaultClient, integration)
	handler.CredentialResolver = &changingCredential{value: credential}
	token := AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(withAccessToken(r.Context(), token)))
	}))
	defer gateway.Close()
	totals := runLiveProbe(t, "ws"+strings.TrimPrefix(gateway.URL, "http")+"/i/live"+parsed.Path, http.Header{}, model)
	settledModel := liveSetupModel([]byte(`{"setup":{"model":"` + model + `"}}`))
	deadline := time.Now().Add(10 * time.Second)
	for {
		row, ok := backend.AIUsageRollup("live", app, settledModel)
		if ok && row.Requests >= totals.turns {
			t.Logf("gateway settled %+v", row)
			if row.Requests != totals.turns || row.InputTokens != totals.prompt || row.OutputTokens != totals.response ||
				row.UsageUnknownRequests != 0 {
				t.Fatalf("gateway settled %d turns, %d in, %d out; provider reported %d turns, %d in, %d out",
					row.Requests, row.InputTokens, row.OutputTokens, totals.turns, totals.prompt, totals.response)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no settlement of %d turns for %s: %+v", totals.turns, settledModel, row)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// liveProbeTotals is what a probe session's usage messages reported.
type liveProbeTotals struct {
	turns, prompt, response int64
	shrank                  bool
}

// runLiveProbe runs the three-turn session and logs every usage message.
func runLiveProbe(t *testing.T, target string, header http.Header, model string) liveProbeTotals {
	t.Helper()
	modality := envOr("FAAS_LIVE_PROBE_MODALITY", "AUDIO")
	conn, resp, err := websocket.DefaultDialer.Dial(target, header)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	setup := map[string]any{"setup": map[string]any{"model": model,
		"generationConfig": map[string]any{"responseModalities": []string{modality}}}}
	if err := conn.WriteJSON(setup); err != nil {
		t.Fatal(err)
	}
	first := readLiveProbe(t, conn)
	if first.SetupComplete == nil {
		t.Fatalf("no setupComplete")
	}
	t.Logf("setupComplete binary=%v", first.binary)
	prompts := []string{
		"Answer with exactly one word: what colour is the sky on a clear day?",
		"In about fifty words, describe how a rainbow forms.",
		"Answer with exactly one word: what is frozen water called?",
	}
	var previousResponse int64 = -1
	var totals liveProbeTotals
	for turn, prompt := range prompts {
		message := map[string]any{"clientContent": map[string]any{"turnComplete": true,
			"turns": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}}}}
		if err := conn.WriteJSON(message); err != nil {
			t.Fatal(err)
		}
		usages, contents := 0, 0
		for {
			event := readLiveProbe(t, conn)
			complete := event.ServerContent != nil && event.ServerContent.TurnComplete
			if event.ServerContent != nil {
				contents++
			}
			if usage := event.UsageMetadata; usage != nil {
				usages++
				response := usage.ResponseTokenCount + usage.CandidatesTokenCount
				t.Logf("turn %d usage #%d after %d content messages: prompt=%d response=%d thoughts=%d total=%d turnComplete=%v",
					turn+1, usages, contents, usage.PromptTokenCount, response, usage.ThoughtsTokenCount, usage.TotalTokenCount, complete)
				t.Logf("turn %d usage #%d binary=%v raw: %s", turn+1, usages, event.binary, event.rawUsage)
				if previousResponse >= 0 && response < previousResponse {
					totals.shrank = true
				}
				previousResponse = response
				totals.add(event)
			}
			if complete && usages > 0 {
				break
			}
			if complete {
				// usageMetadata may follow turnComplete in its own message.
				event = readLiveProbe(t, conn)
				if usage := event.UsageMetadata; usage != nil {
					t.Logf("turn %d usage after turnComplete: prompt=%d response=%d total=%d raw: %s", turn+1,
						usage.PromptTokenCount, usage.ResponseTokenCount+usage.CandidatesTokenCount, usage.TotalTokenCount, event.rawUsage)
					totals.add(event)
				}
				break
			}
		}
		t.Logf("turn %d: %d usage messages", turn+1, usages)
	}
	return totals
}

// add counts one usage message the way the gateway reads it: thinking is
// output.
func (totals *liveProbeTotals) add(event liveProbeMessage) {
	usage := event.UsageMetadata
	totals.turns++
	totals.prompt += usage.PromptTokenCount
	totals.response += usage.ResponseTokenCount + usage.CandidatesTokenCount + usage.ThoughtsTokenCount
}

func liveProbeTarget(t *testing.T) (string, http.Header, string) {
	t.Helper()
	header := http.Header{}
	if os.Getenv("FAAS_LIVE_PROBE_BACKEND") == "vertex" {
		token, project, location := os.Getenv("VERTEX_ACCESS_TOKEN"), os.Getenv("VERTEX_PROJECT"), envOr("VERTEX_LOCATION", "us-central1")
		if token == "" || project == "" {
			t.Fatal("vertex needs VERTEX_ACCESS_TOKEN and VERTEX_PROJECT")
		}
		header.Set("Authorization", "Bearer "+token)
		model := envOr("FAAS_LIVE_PROBE_MODEL", "gemini-live-2.5-flash-native-audio")
		return "wss://" + location + "-aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent",
			header, "projects/" + project + "/locations/" + location + "/publishers/google/models/" + model
	}
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Fatal("set GEMINI_API_KEY")
	}
	header.Set("x-goog-api-key", key)
	model := envOr("FAAS_LIVE_PROBE_MODEL", "gemini-2.5-flash-native-audio-preview-12-2025")
	if !strings.HasPrefix(model, "models/") {
		model = "models/" + model
	}
	return "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent", header, model
}

func readLiveProbe(t *testing.T, conn *websocket.Conn) liveProbeMessage {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	kind, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	message := liveProbeMessage{binary: kind == websocket.BinaryMessage}
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatalf("decode %q: %v", raw[:min(len(raw), 200)], err)
	}
	var usage struct {
		UsageMetadata json.RawMessage `json:"usageMetadata"`
	}
	if err := json.Unmarshal(raw, &usage); err == nil {
		message.rawUsage = usage.UsageMetadata
	}
	return message
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
