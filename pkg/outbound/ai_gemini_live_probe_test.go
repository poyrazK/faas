package outbound

// adr: 20261019072722 — an opt-in probe that settles what Gemini Live's
// usageMetadata counts, so Live sessions can be metered: it runs one short
// session against the real API and reports every usage message per turn.
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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
}

func TestGeminiLiveUsageProbe(t *testing.T) {
	if os.Getenv("FAAS_GEMINI_LIVE_PROBE") != "1" {
		t.Skip("set FAAS_GEMINI_LIVE_PROBE=1 and a key to run against the real Live API")
	}
	url, header, model := liveProbeTarget(t)
	modality := envOr("FAAS_LIVE_PROBE_MODALITY", "AUDIO")
	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
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
	if first := readLiveProbe(t, conn); first.SetupComplete == nil {
		t.Fatalf("no setupComplete")
	}
	prompts := []string{
		"Answer with exactly one word: what colour is the sky on a clear day?",
		"In about fifty words, describe how a rainbow forms.",
		"Answer with exactly one word: what is frozen water called?",
	}
	var previousResponse int64 = -1
	shrank := false
	for turn, prompt := range prompts {
		message := map[string]any{"clientContent": map[string]any{"turnComplete": true,
			"turns": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}}}}
		if err := conn.WriteJSON(message); err != nil {
			t.Fatal(err)
		}
		usages := 0
		for {
			event := readLiveProbe(t, conn)
			complete := event.ServerContent != nil && event.ServerContent.TurnComplete
			if usage := event.UsageMetadata; usage != nil {
				usages++
				response := usage.ResponseTokenCount + usage.CandidatesTokenCount
				t.Logf("turn %d usage #%d: prompt=%d response=%d thoughts=%d total=%d turnComplete=%v",
					turn+1, usages, usage.PromptTokenCount, response, usage.ThoughtsTokenCount, usage.TotalTokenCount, complete)
				if previousResponse >= 0 && response < previousResponse {
					shrank = true
				}
				previousResponse = response
			}
			if complete && usages > 0 {
				break
			}
			if complete {
				// usageMetadata may follow turnComplete in its own message.
				event = readLiveProbe(t, conn)
				if usage := event.UsageMetadata; usage != nil {
					t.Logf("turn %d usage after turnComplete: prompt=%d response=%d total=%d", turn+1,
						usage.PromptTokenCount, usage.ResponseTokenCount+usage.CandidatesTokenCount, usage.TotalTokenCount)
				}
				break
			}
		}
		t.Logf("turn %d: %d usage messages", turn+1, usages)
	}
	if shrank {
		t.Logf("VERDICT: responseTokenCount fell between messages, so usageMetadata is per turn (or per message), not a session total")
	} else {
		t.Logf("VERDICT: responseTokenCount never fell; with turns of very different lengths this means session-so-far totals")
	}
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
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var message liveProbeMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatalf("decode %q: %v", raw[:min(len(raw), 200)], err)
	}
	return message
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
