package outbound

// adr: 1432 — a write on an AI integration goes through only to a metered
// or known-free endpoint unless the AI policy allows unmetered calls, and
// Veo video generation reserves and settles its seconds at the price for
// its resolution.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/aiprices"
)

func TestAIWriteClassification(t *testing.T) {
	const (
		metered = "metered"
		free    = "free"
		refused = "refused"
	)
	for _, tc := range []struct {
		provider, path, want string
	}{
		{api.OutboundAIProviderOpenAI, "/v1/chat/completions", metered},
		{api.OutboundAIProviderOpenAI, "/v1/responses", metered},
		{api.OutboundAIProviderOpenAI, "/v1/batches", metered},
		{api.OutboundAIProviderOpenAI, "/v1/files", free},
		{api.OutboundAIProviderOpenAI, "/v1/uploads/upload_1/complete", free},
		{api.OutboundAIProviderOpenAI, "/v1/moderations", free},
		{api.OutboundAIProviderOpenAI, "/v1/batches/batch_1/cancel", free},
		{api.OutboundAIProviderOpenAI, "/v1/responses/input_tokens", free},
		{api.OutboundAIProviderOpenAI, "/v1/responses/resp_1/cancel", free},
		{api.OutboundAIProviderOpenAI, "/v1/chat/completions/chatcmpl_1", free},
		{api.OutboundAIProviderOpenAI, "/v1/conversations/conv_1/items", free},
		{api.OutboundAIProviderOpenAI, "/v1/fine_tuning/jobs/ftjob_1/cancel", free},
		{api.OutboundAIProviderOpenAI, "/v1/realtime/client_secrets", refused},
		{api.OutboundAIProviderOpenAI, "/v1/realtime/calls", refused},
		{api.OutboundAIProviderOpenAI, "/v1/vector_stores", refused},
		{api.OutboundAIProviderOpenAI, "/v1/vector_stores/vs_1/files", refused},
		{api.OutboundAIProviderOpenAI, "/v1/containers", refused},
		{api.OutboundAIProviderOpenAI, "/v1/fine_tuning/jobs", refused},
		{api.OutboundAIProviderOpenAI, "/v1/responses/compact", refused},
		{api.OutboundAIProviderOpenAI, "/v1/videos", refused},
		{api.OutboundAIProviderOpenAI, "/openai/v1/chat/completions", metered},
		{api.OutboundAIProviderAzureOpenAI, "/openai/files", free},
		{api.OutboundAIProviderAzureOpenAI, "/openai/deployments/gpt/chat/completions", metered},
		{api.OutboundAIProviderAzureOpenAI, "/openai/v1/realtime/client_secrets", refused},
		{api.OutboundAIProviderAnthropic, "/v1/messages", metered},
		{api.OutboundAIProviderAnthropic, "/v1/messages/count_tokens", free},
		{api.OutboundAIProviderAnthropic, "/v1/messages/batches/msgbatch_1/cancel", free},
		{api.OutboundAIProviderAnthropic, "/v1/complete", refused},
		{api.OutboundAIProviderGemini, "/v1beta/models/gemini-2.5-flash:generateContent", metered},
		{api.OutboundAIProviderGemini, "/v1beta/models/gemini-2.5-flash:countTokens", free},
		{api.OutboundAIProviderGemini, "/upload/v1beta/files", free},
		{api.OutboundAIProviderGemini, "/v1beta/batches/batch_1:cancel", free},
		{api.OutboundAIProviderGemini, "/v1beta/models/veo-3.1-generate-preview:predictLongRunning", metered},
		{api.OutboundAIProviderGemini, "/v1beta/models/gemini-2.5-flash:batchGenerateContent", refused},
		{api.OutboundAIProviderGemini, "/v1beta/cachedContents", refused},
		{api.OutboundAIProviderGemini, "/v1alpha/auth_tokens", refused},
		{api.OutboundAIProviderVertex, "/v1/projects/p/locations/l/publishers/google/models/gemini-2.5-pro:countTokens", free},
		{api.OutboundAIProviderVertex, "/v1/projects/p/locations/l/publishers/google/models/veo-3.1-generate-001:fetchPredictOperation", free},
		{api.OutboundAIProviderVertex, "/v1/projects/p/locations/l/publishers/google/models/veo-3.1-generate-001:predictLongRunning", metered},
		{api.OutboundAIProviderVertex, "/v1/projects/p/locations/l/batchPredictionJobs", refused},
		{api.OutboundAIProviderVertex, "/v1/projects/p/locations/l/endpoints/openapi/chat/completions", metered},
		{api.OutboundAIProviderBedrock, "/model/anthropic.claude-3-haiku/converse", metered},
		{api.OutboundAIProviderBedrock, "/model/anthropic.claude-3-haiku/count-tokens", free},
		{api.OutboundAIProviderBedrock, "/async-invoke", refused},
	} {
		got := refused
		switch {
		case classifyAIEndpoint(tc.provider, tc.path).metered():
			got = metered
		case aiWriteFree(tc.provider, tc.path):
			got = free
		}
		if got != tc.want {
			t.Errorf("%s POST %s = %s, want %s", tc.provider, tc.path, got, tc.want)
		}
	}
}

func TestUnmeteredWritesNeedTheOptIn(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"value":"ek_1"}`)
	}))
	defer upstream.Close()
	account, app := uuid.NewString(), uuid.NewString()
	integration := phase9Integration(t, "openai", upstream.URL, api.OutboundAIProviderOpenAI, account, app)
	integration.AllowedMethods = []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete}
	allowing := integration
	allowing.ID, allowing.AIAllowUnmetered = "allowing", true
	handler, backend := phase9Handler(t, upstream.Client(), integration, allowing)
	token := AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodPost, "/i/openai/v1/realtime/client_secrets", http.StatusForbidden},
		{http.MethodPatch, "/i/openai/v1/files/file_1", http.StatusForbidden},
		{http.MethodPost, "/i/openai/v1/files", http.StatusOK},
		{http.MethodGet, "/i/openai/v1/vector_stores", http.StatusOK},
		{http.MethodDelete, "/i/openai/v1/vector_stores/vs_1", http.StatusOK},
	} {
		got := asApp(handler, tc.method, tc.path, `{}`, token)
		if got.Code != tc.status || (tc.status == http.StatusForbidden && !strings.Contains(got.Body.String(), "outbound_ai_endpoint_unmetered")) ||
			got.Header().Get(AIUnmeteredHeader) != "" {
			t.Errorf("%s %s = %d %s", tc.method, tc.path, got.Code, got.Body.String())
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("refused writes reached the provider: %d calls", calls.Load())
	}
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		got := asApp(handler, method, "/i/allowing/v1/realtime/client_secrets", `{}`, token)
		if got.Code != http.StatusOK || got.Header().Get(AIUnmeteredHeader) != "allowed" {
			t.Errorf("an allowed unmetered %s = %d %s %v", method, got.Code, got.Body.String(), got.Header())
		}
	}
	if calls.Load() != 5 {
		t.Fatalf("allowed unmetered writes did not reach the provider: %d calls", calls.Load())
	}
	if row, ok := backend.AIUsageRollup("allowing", app, "unknown"); ok && (row.CostMillicents != 0 || row.InputTokens != 0) {
		t.Fatalf("an unmetered write was charged: %+v", row)
	}
	if err := (Integration{AIAllowUnmetered: true}).Validate(); err == nil {
		t.Fatal("allow_unmetered was accepted without an AI provider")
	}
}

func TestVeoVideoIsMeteredBySeconds(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"`+strings.TrimPrefix(r.URL.Path, "/v1beta/")+`/operations/op_1"}`)
	}))
	defer upstream.Close()
	account, app := uuid.NewString(), uuid.NewString()
	gemini := phase9Integration(t, "gemini", upstream.URL, api.OutboundAIProviderGemini, account, app)
	gemini.AllowedPathPrefixes, gemini.AIPrices = []string{"/v1beta"}, nil
	capped := gemini
	capped.ID = "capped"
	limit := int64(100_000)
	capped.DailyAICostLimit = &limit
	priced := gemini
	priced.ID = "priced"
	priced.AIPrices = ResolveAIPrices([]api.OutboundAIPriceEntry{
		{ModelPrefix: "veo-custom", MillicentsPerVideoSecond: int64Ptr(1000)},
		{ModelPrefix: "veo-custom-1@4k", MillicentsPerVideoSecond: int64Ptr(5000)},
	})
	vertex := phase9Integration(t, "vertex", upstream.URL, api.OutboundAIProviderVertex, account, app)
	vertex.AllowedPathPrefixes, vertex.AIPrices = []string{"/v1"}, nil
	handler, backend := phase9Handler(t, upstream.Client(), gemini, capped, priced, vertex)
	token := AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}
	veo := func(integration, model, parameters string) *httptest.ResponseRecorder {
		return asApp(handler, http.MethodPost, "/i/"+integration+"/v1beta/models/"+model+":predictLongRunning",
			`{"instances":[{"prompt":"a lighthouse at dawn"}],"parameters":`+parameters+`}`, token)
	}
	perSecond := func(provider, model string) int64 {
		entry, ok := aiprices.Default().Lookup(provider, model)
		if !ok || entry.Price.VideoSecond == nil {
			t.Fatalf("no catalog video price for %s", model)
		}
		return *entry.Price.VideoSecond
	}

	if got := veo("gemini", "veo-3.1-generate-preview", `{"durationSeconds":"8","resolution":"4k"}`); got.Code != http.StatusOK {
		t.Fatalf("a 4k Veo call = %d %s", got.Code, got.Body.String())
	}
	if got := veo("gemini", "veo-3.1-fast-generate-preview", `{"durationSeconds":6}`); got.Code != http.StatusOK {
		t.Fatalf("a 720p Veo call = %d %s", got.Code, got.Body.String())
	}
	ultra, ok := backend.AIUsageRollup("gemini", app, "veo-3.1-generate-preview")
	if want := 8 * perSecond(api.OutboundAIProviderGemini, "veo-3.1-generate-preview@4k"); !ok || ultra.VideoSeconds != 8 ||
		ultra.CostMillicents != want || ultra.InputTokens != 0 || ultra.UsageUnknownRequests != 0 {
		t.Fatalf("4k usage = %+v, %v (want cost %d)", ultra, ok, want)
	}
	fast, ok := backend.AIUsageRollup("gemini", app, "veo-3.1-fast-generate-preview")
	if want := 6 * perSecond(api.OutboundAIProviderGemini, "veo-3.1-fast-generate-preview"); !ok || fast.VideoSeconds != 6 || fast.CostMillicents != want {
		t.Fatalf("720p usage = %+v, %v (want cost %d)", fast, ok, want)
	}

	path := "/i/vertex/v1/projects/p/locations/us-central1/publishers/google/models/veo-3.1-generate-001:predictLongRunning"
	if got := asApp(handler, http.MethodPost, path, `{"instances":[{"prompt":"x"}],"parameters":{"durationSeconds":4,"sampleCount":2}}`, token); got.Code != http.StatusOK {
		t.Fatalf("a Vertex Veo call = %d %s", got.Code, got.Body.String())
	}
	if row, ok := backend.AIUsageRollup("vertex", app, "veo-3.1-generate-001"); !ok || row.VideoSeconds != 8 ||
		row.CostMillicents != 8*perSecond(api.OutboundAIProviderVertex, "veo-3.1-generate-001") {
		t.Fatalf("Vertex usage = %+v, %v", row, ok)
	}

	if got := veo("priced", "veo-custom-1", `{"durationSeconds":8,"resolution":"4k"}`); got.Code != http.StatusOK {
		t.Fatalf("a price-list Veo call = %d %s", got.Code, got.Body.String())
	}
	if row, _ := backend.AIUsageRollup("priced", app, "veo-custom-1"); row.CostMillicents != 8*5000 {
		t.Fatalf("a price-list resolution price = %+v", row)
	}

	before := calls.Load()
	for name, tc := range map[string]struct {
		integration, model, parameters string
		status                         int
		code                           string
	}{
		"over the cost cap":  {"capped", "veo-3.1-generate-preview", `{"durationSeconds":8,"resolution":"4k"}`, http.StatusTooManyRequests, ""},
		"unpriced model":     {"gemini", "veo-9-unknown", `{"durationSeconds":8}`, http.StatusForbidden, "outbound_ai_endpoint_unmetered"},
		"too many videos":    {"gemini", "veo-3.1-generate-preview", `{"numberOfVideos":99}`, http.StatusForbidden, "outbound_ai_endpoint_unmetered"},
		"invalid duration":   {"gemini", "veo-3.1-generate-preview", `{"durationSeconds":"eight"}`, http.StatusForbidden, "outbound_ai_endpoint_unmetered"},
		"invalid resolution": {"gemini", "veo-3.1-generate-preview", `{"resolution":"8K!"}`, http.StatusForbidden, "outbound_ai_endpoint_unmetered"},
	} {
		got := veo(tc.integration, tc.model, tc.parameters)
		if got.Code != tc.status || !strings.Contains(got.Body.String(), tc.code) {
			t.Errorf("%s = %d %s", name, got.Code, got.Body.String())
		}
	}
	if calls.Load() != before {
		t.Fatalf("a refused video call reached the provider")
	}
}

func TestVideoFacts(t *testing.T) {
	for _, tc := range []struct {
		body       string
		seconds    int64
		resolution string
		sized      bool
	}{
		{`{"instances":[{"prompt":"x"}]}`, 8, "720p", true},
		{`{"instances":[{"prompt":"x"}],"parameters":{"durationSeconds":"4","numberOfVideos":2,"resolution":"1080P"}}`, 8, "1080p", true},
		{`{"instances":[{"prompt":"x"}],"parameters":{"durationSeconds":5,"sampleCount":3}}`, 15, "720p", true},
		{`{"instances":[{"prompt":"x"},{"prompt":"y"}],"parameters":{"durationSeconds":8}}`, 16, "720p", true},
		{`{"instances":[]}`, 0, "", false},
		{`{"instances":[{"prompt":"x"}],"parameters":{"durationSeconds":0}}`, 0, "", false},
		{`{"instances":[{"prompt":"x"}],"parameters":"bad"}`, 0, "", false},
	} {
		facts := readAICallFacts(aiEndpoint{format: aiFormatGemini, media: aiMediaVideo, pathModel: "veo"}, []byte(tc.body))
		if facts.videoSeconds != tc.seconds || facts.videoResolution != tc.resolution || facts.videoSized != tc.sized {
			t.Errorf("%s = %d %q %v", tc.body, facts.videoSeconds, facts.videoResolution, facts.videoSized)
		}
	}
	usage := AIUsage{Model: "veo", Known: true, VideoSeconds: 3, VideoResolution: "4k"}
	if usage.priceModel() != "veo@4k" || (AIUsage{Model: "veo", VideoResolution: "4k"}).priceModel() != "veo" {
		t.Fatal("priceModel")
	}
	if cost := priceCostMillicents(AIPrice{VideoSecond: 7}, usage); cost != 21 {
		t.Fatalf("video cost = %d", cost)
	}
}

func int64Ptr(value int64) *int64 { return &value }
