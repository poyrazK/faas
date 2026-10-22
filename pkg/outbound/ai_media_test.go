package outbound

// adr: 997 — image, transcription and speech calls are billable: they are
// reserved and settled in the unit their price bills.

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestClassifyMediaEndpoints(t *testing.T) {
	for _, tc := range []struct {
		provider, path string
		media          aiMediaKind
		embedding      bool
	}{
		{api.OutboundAIProviderOpenAI, "/v1/images/generations", aiMediaImage, false},
		{api.OutboundAIProviderOpenAI, "/v1/images/edits", aiMediaImage, false},
		{api.OutboundAIProviderOpenAI, "/v1/audio/transcriptions", aiMediaTranscription, false},
		{api.OutboundAIProviderOpenAI, "/v1/audio/translations", aiMediaTranscription, false},
		{api.OutboundAIProviderOpenAI, "/v1/audio/speech", aiMediaSpeech, false},
		{api.OutboundAIProviderAzureOpenAI, "/openai/deployments/dalle/images/generations", aiMediaImage, false},
		{api.OutboundAIProviderGemini, "/v1beta/models/imagen-4.0-generate-001:predict", aiMediaImage, false},
		{api.OutboundAIProviderVertex, "/v1/projects/p/locations/l/publishers/google/models/imagen-3.0-generate-002:predict", aiMediaImage, false},
		{api.OutboundAIProviderVertex, "/v1/projects/p/locations/l/publishers/google/models/text-embedding-005:predict", aiMediaNone, true},
		{api.OutboundAIProviderOpenAI, "/v1/models", aiMediaNone, false},
	} {
		endpoint := classifyAIEndpoint(tc.provider, tc.path)
		if endpoint.media != tc.media || endpoint.embedding != tc.embedding || endpoint.billable() != (tc.media != aiMediaNone || tc.embedding) {
			t.Errorf("%s %s = %+v", tc.provider, tc.path, endpoint)
		}
	}
}

func TestMediaPricing(t *testing.T) {
	price := AIPrice{Input: 1_000_000, CachedInput: 1_000_000, CacheWrite: 1_000_000, Output: 2_000_000,
		InputImage: 3_000_000, OutputImage: 4_000_000, Image: 500, AudioMinute: 600, Characters: 1_500_000}
	for _, tc := range []struct {
		name  string
		usage AIUsage
		want  int64
	}{
		// 10 text + 5 image input, 2 text + 3 image output tokens.
		{"image tokens", AIUsage{Known: true, InputTokens: 15, InputImageTokens: 5, OutputTokens: 5, OutputImageTokens: 3}, 10 + 15 + 4 + 12},
		{"per image", AIUsage{Known: true, Images: 3}, 1500},
		{"no double charge", AIUsage{Known: true, Images: 3, OutputTokens: 10, OutputImageTokens: 10}, 40},
		{"audio minute", AIUsage{Known: true, AudioSeconds: 61}, 610},
		{"rounds up", AIUsage{Known: true, AudioSeconds: 1, Characters: 1}, 10 + 2},
		{"characters", AIUsage{Known: true, Characters: 1000}, 1500},
		{"unknown", AIUsage{Images: 3}, 0},
	} {
		if got := priceCostMillicents(price, tc.usage); got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, got, tc.want)
		}
	}
	resolved := ResolveAIPrices([]api.OutboundAIPriceEntry{{ModelPrefix: "m", InputMillicentsPerMTok: 7, OutputMillicentsPerMTok: 9}})
	if resolved[0].InputImage != 7 || resolved[0].OutputImage != 9 || resolved[0].Image != 0 {
		t.Fatalf("image-token prices did not fall back: %+v", resolved[0])
	}
}

func TestMediaUsageObserver(t *testing.T) {
	observe := func(media aiMediaKind, format aiWireFormat, contentType string, chunks ...string) AIUsage {
		t.Helper()
		observer := newAIUsageObserverFormat(format, contentType, nil)
		observer.media, observer.fallbackModel = media, "m"
		for _, chunk := range chunks {
			_, _ = observer.Write([]byte(chunk))
		}
		return observer.Finish()
	}
	gptImage := observe(aiMediaImage, aiFormatOpenAI, "application/json",
		`{"data":[{"b64_json":"aaaa"},{"b64_json":"bbbb"}],"usage":{"input_tokens":50,"output_tokens":2000,"input_tokens_details":{"text_tokens":40,"image_tokens":10}}}`)
	if !gptImage.Known || gptImage.Images != 2 || gptImage.InputImageTokens != 10 || gptImage.OutputImageTokens != 2000 || gptImage.OutputTokens != 2000 {
		t.Fatalf("gpt-image usage = %+v", gptImage)
	}
	dalle := observe(aiMediaImage, aiFormatOpenAI, "application/json", `{"data":[{"url":"https://x"}]}`)
	if !dalle.Known || dalle.Images != 1 || dalle.OutputImageTokens != 0 {
		t.Fatalf("url image usage = %+v", dalle)
	}
	// A large base64 response: images are counted by marker across writes
	// and usage is read from the tail.
	big := `{"data":[{"b64_json":"` + strings.Repeat("A", api.OutboundAIUsageJSONParseMaxBytes) + `"},{"b64_json":"QQ=="}],"usage":{"input_tokens":5,"output_tokens":4160}}`
	var chunks []string
	for start := 0; start < len(big); start += 7 {
		chunks = append(chunks, big[start:min(start+7, len(big))])
	}
	large := observe(aiMediaImage, aiFormatOpenAI, "application/json", chunks...)
	if !large.Known || large.Images != 2 || large.OutputTokens != 4160 || large.OutputImageTokens != 4160 {
		t.Fatalf("large image usage = %+v", large)
	}
	imagen := observe(aiMediaImage, aiFormatGemini, "application/json",
		`{"predictions":[{"bytesBase64Encoded":"aa","mimeType":"image/png"},{"raiFilteredReason":"x"},{"bytesBase64Encoded":"bb"}]}`)
	if !imagen.Known || imagen.Images != 2 {
		t.Fatalf("imagen usage = %+v", imagen)
	}
	stream := observe(aiMediaImage, aiFormatOpenAI, "text/event-stream",
		"data: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"x\"}\n\n",
		"data: {\"type\":\"image_generation.completed\",\"b64_json\":\"y\",\"usage\":{\"input_tokens\":3,\"output_tokens\":272}}\n\n")
	if !stream.Known || stream.Images != 1 || stream.OutputImageTokens != 272 {
		t.Fatalf("streamed image usage = %+v", stream)
	}
	duration := observe(aiMediaTranscription, aiFormatOpenAI, "application/json", `{"text":"hi","usage":{"type":"duration","seconds":3.2}}`)
	verbose := observe(aiMediaTranscription, aiFormatOpenAI, "application/json", `{"text":"hi","duration":61.01}`)
	tokens := observe(aiMediaTranscription, aiFormatOpenAI, "application/json",
		`{"text":"hi","usage":{"type":"tokens","input_tokens":120,"output_tokens":8}}`)
	if duration.AudioSeconds != 4 || verbose.AudioSeconds != 62 || tokens.InputTokens != 120 || tokens.AudioSeconds != 0 || !tokens.Known {
		t.Fatalf("transcription usage = %+v, %+v, %+v", duration, verbose, tokens)
	}
	gemini := observe(aiMediaNone, aiFormatGemini, "application/json",
		`{"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":1300,"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":10},{"modality":"IMAGE","tokenCount":1290}]}}`)
	if gemini.OutputTokens != 1300 || gemini.OutputImageTokens != 1290 || gemini.Images != 0 {
		t.Fatalf("gemini image tokens = %+v", gemini)
	}
	chat := observe(aiMediaNone, aiFormatOpenAI, "application/json", `{"data":[1,2],"duration":5,"usage":{"prompt_tokens":1}}`)
	if chat.Images != 0 || chat.AudioSeconds != 0 {
		t.Fatalf("a non-media response counted media: %+v", chat)
	}
}

func TestMarkerCounterCountsEachMarkerOnce(t *testing.T) {
	body := []byte(`{"data":[{"b64_json":"x"},{"b64_json":"y"}],"predictions":[{"bytesBase64Encoded":"z"}]}`)
	for size := 1; size <= len(body); size++ {
		var counter markerCounter
		for start := 0; start < len(body); start += size {
			counter.write(body[start:min(start+size, len(body))])
		}
		if counter.count != 3 {
			t.Fatalf("chunk size %d counted %d markers", size, counter.count)
		}
	}
}

func TestMultipartFieldsFromPrefix(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", "whisper-1")
	file, _ := writer.CreateFormFile("file", "a.mp3")
	_, _ = file.Write(bytes.Repeat([]byte{0xff}, 4096))
	_ = writer.WriteField("n", "2")
	_ = writer.Close()
	full := multipartFields(writer.FormDataContentType(), body.Bytes())
	prefix := multipartFields(writer.FormDataContentType(), body.Bytes()[:1024])
	if full["model"] != "whisper-1" || full["n"] != "2" || prefix["model"] != "whisper-1" || prefix["n"] != "" {
		t.Fatalf("fields = %v, prefix %v", full, prefix)
	}
	if multipartFields("application/json", body.Bytes()) != nil {
		t.Fatal("a JSON body was read as a form")
	}
}

func TestMediaCallsReserveAndSettle(t *testing.T) {
	var lastPath string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		switch {
		case strings.HasSuffix(r.URL.Path, "/audio/speech"):
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte{0x49, 0x44, 0x33})
		case strings.HasSuffix(r.URL.Path, "/audio/transcriptions"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"text":"hello","usage":{"type":"duration","seconds":90}}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"url":"https://a"},{"url":"https://b"}]}`))
		}
	}))
	defer upstream.Close()
	integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AIProvider = api.OutboundAIProviderOpenAI
	integration.DailyAICostLimit = int64Ref(1_000_000)
	perImage, perMinute, perMChars := int64(4000), int64(600), int64(1_500_000)
	integration.AIPrices = ResolveAIPrices([]api.OutboundAIPriceEntry{
		{ModelPrefix: "dall-e-3", MillicentsPerImage: &perImage},
		{ModelPrefix: "whisper-1", MillicentsPerAudioMinute: &perMinute},
		{ModelPrefix: "tts-1", MillicentsPerMillionCharacters: &perMChars},
	})
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	call := func(path, contentType string, body []byte) {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := gatewayRequest("/i/integration-1"+path, "token", "app-1", http.MethodPost, bytes.NewReader(body))
		request.Header.Set("Content-Type", contentType)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, recorder.Code, recorder.Body.String())
		}
	}
	call("/v1/images/generations", "application/json", []byte(`{"model":"dall-e-3","prompt":"a cat","n":2}`))
	call("/v1/audio/speech", "application/json", []byte(`{"model":"tts-1","input":"héllo wörld","voice":"alloy"}`))
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	_ = writer.WriteField("model", "whisper-1")
	file, _ := writer.CreateFormFile("file", "a.mp3")
	_, _ = file.Write(bytes.Repeat([]byte{1}, 8192))
	_ = writer.Close()
	call("/v1/audio/transcriptions", writer.FormDataContentType(), form.Bytes())
	if lastPath != "/v1/audio/transcriptions" {
		t.Fatalf("upstream path = %s", lastPath)
	}
	totals := map[string]AIUsageTotals{}
	for _, model := range []string{"dall-e-3", "tts-1", "whisper-1"} {
		totals[model], _ = backend.AIUsageRollup("integration-1", "app-1", model)
	}
	if got := totals["dall-e-3"]; got.Images != 2 || got.CostMillicents != 8000 || got.UsageUnknownRequests != 0 {
		t.Fatalf("image rollup = %+v", got)
	}
	if got := totals["tts-1"]; got.Characters != 11 || got.CostMillicents != 17 || got.UsageUnknownRequests != 0 {
		t.Fatalf("speech rollup = %+v", got)
	}
	if got := totals["whisper-1"]; got.AudioSeconds != 90 || got.CostMillicents != 900 {
		t.Fatalf("transcription rollup = %+v", got)
	}
	if daily, _ := backend.AICost("integration-1"); daily != 8000+17+900 {
		t.Fatalf("daily cost = %d", daily)
	}
}

func TestMediaReservations(t *testing.T) {
	perImage := int64(4000)
	integration := Integration{AIProvider: api.OutboundAIProviderGemini, AIPrices: ResolveAIPrices([]api.OutboundAIPriceEntry{
		{ModelPrefix: "imagen-4.0", MillicentsPerImage: &perImage},
	})}
	plan := func(provider, path, body string) aiReservation {
		t.Helper()
		integration.AIProvider = provider
		request := httptest.NewRequest(http.MethodPost, "http://gateway.test"+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		planned, rejection := planAICall(request, integration, "", path, &aiRequest{body: []byte(body)}, nil)
		if rejection != nil {
			t.Fatalf("%s rejected: %+v", path, rejection)
		}
		return planned.reserve
	}
	imagen := plan(api.OutboundAIProviderGemini, "/v1beta/models/imagen-4.0-generate-001:predict", `{"instances":[{"prompt":"x"}]}`)
	if imagen.estimate.Images != 4 || imagen.estimate.OutputTokens != 0 || imagen.cost != 16000 {
		t.Fatalf("imagen reservation = %+v", imagen)
	}
	unpriced := plan(api.OutboundAIProviderOpenAI, "/v1/images/generations", `{"model":"unpriced-image","prompt":"x","n":3}`)
	if unpriced.estimate.OutputTokens != 3*api.OutboundAIImageReserveOutputTokens || unpriced.estimate.OutputImageTokens != unpriced.estimate.OutputTokens {
		t.Fatalf("token-priced image reservation = %+v", unpriced)
	}
	speech := plan(api.OutboundAIProviderOpenAI, "/v1/audio/speech", `{"model":"unpriced-tts","input":"hello"}`)
	if speech.estimate.Characters != 5 || speech.estimate.OutputTokens != api.OutboundAIDefaultOutputReserveTokens {
		t.Fatalf("speech reservation = %+v", speech)
	}
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/audio/transcriptions", strings.NewReader("x"))
	request.ContentLength = 20_000
	integration.AIProvider = api.OutboundAIProviderOpenAI
	// The catalog prices gpt-4o-transcribe per audio minute, so its 10
	// estimated seconds are reserved as audio; an unpriced model reserves
	// audio tokens.
	planned, _ := planAICall(request, integration, "", "/v1/audio/transcriptions", nil, map[string]string{"model": "gpt-4o-transcribe"})
	if planned.facts.model != "gpt-4o-transcribe" || planned.reserve.estimate.AudioSeconds != 10 || planned.reserve.tokens != 0 || planned.reserve.cost == 0 {
		t.Fatalf("transcription reservation = %+v, model %q", planned.reserve, planned.facts.model)
	}
	stt, _ := planAICall(request, integration, "", "/v1/audio/transcriptions", nil, map[string]string{"model": "unpriced-stt"})
	if stt.reserve.estimate.InputTokens != 10*api.OutboundAIAudioReserveTokensPerSecond || stt.reserve.estimate.AudioSeconds != 0 {
		t.Fatalf("unpriced transcription reservation = %+v", stt.reserve)
	}
}
