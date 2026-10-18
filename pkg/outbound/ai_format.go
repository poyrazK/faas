package outbound

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// aiWireFormat is the request and usage format of one AI call. It follows
// from the integration's provider and the provider path (ADR-991), because
// Vertex and Bedrock serve several formats behind one origin.
type aiWireFormat int

const (
	aiFormatNone aiWireFormat = iota
	aiFormatOpenAI
	aiFormatAnthropic
	aiFormatGemini
	aiFormatBedrockConverse
	aiFormatBedrockInvoke
)

// aiMediaKind marks an endpoint billed for media rather than, or besides,
// tokens (ADR-997).
type aiMediaKind int

const (
	aiMediaNone aiMediaKind = iota
	// aiMediaImage is image generation, edits and variations, and Imagen
	// predict.
	aiMediaImage
	// aiMediaTranscription is audio transcription and translation.
	aiMediaTranscription
	// aiMediaSpeech is text to speech.
	aiMediaSpeech
	// aiMediaVideo is Veo video generation, billed per second (ADR-1432).
	aiMediaVideo
)

// aiEndpoint is what the path says about one AI call.
type aiEndpoint struct {
	format aiWireFormat
	// generation marks an endpoint that produces output tokens, so output
	// ceilings and reservations apply.
	generation bool
	// embedding marks an endpoint that bills input tokens for embeddings;
	// with generation it makes a call billable, so unknown usage settles
	// at the reservation (ADR-996).
	embedding bool
	// pathModel is the model named by the path (Gemini, Vertex, Bedrock and
	// Azure deployments); empty when the body names it.
	pathModel string
	// openAIOutputField is the OpenAI-format ceiling field for this path.
	openAIOutputField string
	// media marks an image, transcription or speech endpoint (ADR-997).
	media aiMediaKind
	// batch marks a batch creation endpoint: it reserves the batch's
	// estimate and settles when the batch ends (ADR-1431).
	batch bool
}

// classifyAIEndpoint maps a provider path to its wire format.
func classifyAIEndpoint(provider, path string) aiEndpoint {
	switch provider {
	case api.OutboundAIProviderOpenAI, api.OutboundAIProviderAzureOpenAI:
		endpoint := openAIEndpoint(path)
		if provider == api.OutboundAIProviderAzureOpenAI {
			endpoint.pathModel = pathSegmentAfter(path, "/deployments/")
		}
		return endpoint
	case api.OutboundAIProviderAnthropic:
		return aiEndpoint{format: aiFormatAnthropic, generation: strings.HasSuffix(path, "/messages"),
			batch: strings.HasSuffix(path, "/messages/batches")}
	case api.OutboundAIProviderGemini:
		model, verb := modelAndVerb(path)
		endpoint := aiEndpoint{format: aiFormatGemini, pathModel: model, generation: geminiGenerationVerb(verb),
			embedding: verb == "embedContent" || verb == "batchEmbedContents"}
		if verb == "predict" && imagenModel(model) {
			endpoint.media = aiMediaImage
		}
		if verb == "predictLongRunning" && videoModel(model) {
			endpoint.media = aiMediaVideo
		}
		return endpoint
	case api.OutboundAIProviderVertex:
		return vertexEndpoint(path)
	case api.OutboundAIProviderBedrock:
		return bedrockEndpoint(path)
	}
	return aiEndpoint{}
}

func openAIEndpoint(path string) aiEndpoint {
	endpoint := aiEndpoint{format: aiFormatOpenAI}
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		endpoint.generation, endpoint.openAIOutputField = true, "max_completion_tokens"
	case strings.HasSuffix(path, "/responses"):
		endpoint.generation, endpoint.openAIOutputField = true, "max_output_tokens"
	case strings.HasSuffix(path, "/completions"):
		endpoint.generation, endpoint.openAIOutputField = true, "max_tokens"
	case strings.HasSuffix(path, "/embeddings"):
		endpoint.embedding = true
	case strings.HasSuffix(path, "/images/generations") || strings.HasSuffix(path, "/images/edits") ||
		strings.HasSuffix(path, "/images/variations"):
		endpoint.media = aiMediaImage
	case strings.HasSuffix(path, "/audio/transcriptions") || strings.HasSuffix(path, "/audio/translations"):
		endpoint.media = aiMediaTranscription
	case strings.HasSuffix(path, "/audio/speech"):
		endpoint.media = aiMediaSpeech
	case strings.HasSuffix(path, "/batches"):
		endpoint.batch = true
	}
	return endpoint
}

// billable reports whether a call through the endpoint is charged by the
// provider: generation, embeddings and media, not token counting or
// listings.
func (e aiEndpoint) billable() bool { return e.generation || e.embedding || e.media != aiMediaNone }

// metered reports an endpoint whose charges the gateway reserves and
// settles (ADR-1432): a billable call or a batch.
func (e aiEndpoint) metered() bool { return e.billable() || e.batch }

// videoModel reports a Veo video model served by predictLongRunning
// (ADR-1432).
func videoModel(model string) bool { return strings.Contains(model, "veo") }

// imagenModel reports an Imagen image model served by predict (ADR-997).
func imagenModel(model string) bool {
	return strings.Contains(model, "imagen") || strings.HasPrefix(model, "imagegeneration")
}

func vertexEndpoint(path string) aiEndpoint {
	if strings.Contains(path, "/endpoints/openapi/") {
		endpoint := openAIEndpoint(path)
		return endpoint
	}
	model, verb := modelAndVerb(path)
	switch {
	case strings.Contains(path, "/publishers/anthropic/"):
		return aiEndpoint{format: aiFormatAnthropic, pathModel: model, generation: verb == "rawPredict" || verb == "streamRawPredict"}
	case strings.Contains(path, "/publishers/google/"):
		// predict serves embedding models, billed in tokens, and Imagen,
		// billed per image (ADR-997).
		endpoint := aiEndpoint{format: aiFormatGemini, pathModel: model, generation: geminiGenerationVerb(verb),
			embedding: verb == "embedContent" || verb == "batchEmbedContents" || (verb == "predict" && strings.Contains(model, "embedding"))}
		if verb == "predict" && imagenModel(model) {
			endpoint.media = aiMediaImage
		}
		if verb == "predictLongRunning" && videoModel(model) {
			endpoint.media = aiMediaVideo
		}
		return endpoint
	}
	return aiEndpoint{}
}

func bedrockEndpoint(path string) aiEndpoint {
	rest, ok := strings.CutPrefix(path, "/model/")
	if !ok {
		return aiEndpoint{}
	}
	escapedModel, operation, found := strings.Cut(rest, "/")
	if !found {
		return aiEndpoint{}
	}
	model, err := url.PathUnescape(escapedModel)
	if err != nil {
		return aiEndpoint{}
	}
	switch operation {
	case "converse", "converse-stream":
		return aiEndpoint{format: aiFormatBedrockConverse, pathModel: model, generation: true}
	case "invoke", "invoke-with-response-stream":
		return aiEndpoint{format: aiFormatBedrockInvoke, pathModel: model, generation: true}
	}
	return aiEndpoint{}
}

// modelAndVerb reads ".../models/{model}:{verb}".
func modelAndVerb(path string) (string, string) {
	segment := pathSegmentAfter(path, "/models/")
	model, verb, _ := strings.Cut(segment, ":")
	return model, verb
}

func geminiGenerationVerb(verb string) bool {
	return verb == "generateContent" || verb == "streamGenerateContent"
}

// pathSegmentAfter returns the unescaped segment following marker.
func pathSegmentAfter(path, marker string) string {
	index := strings.LastIndex(path, marker)
	if index < 0 {
		return ""
	}
	segment, _, _ := strings.Cut(path[index+len(marker):], "/")
	if unescaped, err := url.PathUnescape(segment); err == nil {
		return unescaped
	}
	return ""
}

// aiCallFacts are the guardrail-relevant fields of a buffered AI body.
type aiCallFacts struct {
	model string
	// outputCeiling is the request's own output-token limit, 0 when absent.
	outputCeiling int64
	// ceilingKnown reports that the format has a ceiling field the gateway
	// can read and add.
	ceilingKnown bool
	// images is the image count an image call asks for; characters is the
	// length of a speech call's input text (ADR-997).
	images     int64
	characters int64
	// videoSeconds is the generated seconds a video call asks for, its
	// duration times its videos, at videoResolution; videoSized is false
	// when the body does not say (ADR-1432).
	videoSeconds    int64
	videoResolution string
	videoSized      bool
	// tools is the built-in tool uses the request declares and reserves
	// (ADR-1433).
	tools aiToolReserve
}

// aiToolReserve is what a request's built-in tools reserve (ADR-1433):
// web searches, file searches and GB of code-interpreter container
// sessions. chatSearch marks a chat completion on a search model, which
// runs one search per call; codeGB is the container memory a session is
// charged at.
type aiToolReserve struct {
	webSearches, fileSearches, codeSessionGB int64
	chatSearch                               bool
	codeGB                                   int64
}

// readAICallFacts reads the model and output ceiling from a JSON body.
func readAICallFacts(endpoint aiEndpoint, body []byte) aiCallFacts {
	facts := aiCallFacts{model: endpoint.pathModel}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		fields = nil
	}
	if facts.model == "" {
		facts.model = jsonString(fields["model"])
	}
	switch endpoint.format {
	case aiFormatOpenAI:
		facts.ceilingKnown = endpoint.openAIOutputField != ""
		for _, name := range []string{"max_completion_tokens", "max_tokens", "max_output_tokens"} {
			facts.outputCeiling = max(facts.outputCeiling, jsonInt(fields[name]))
		}
	case aiFormatAnthropic:
		facts.ceilingKnown, facts.outputCeiling = true, jsonInt(fields["max_tokens"])
	case aiFormatGemini:
		facts.ceilingKnown, facts.outputCeiling = true, jsonInt(jsonObjectField(fields["generationConfig"], "maxOutputTokens"))
	case aiFormatBedrockConverse:
		facts.ceilingKnown, facts.outputCeiling = true, jsonInt(jsonObjectField(fields["inferenceConfig"], "maxTokens"))
	case aiFormatBedrockInvoke:
		// Only the Anthropic messages body has a known ceiling field.
		if _, anthropic := fields["anthropic_version"]; anthropic {
			facts.ceilingKnown, facts.outputCeiling = true, jsonInt(fields["max_tokens"])
		}
	}
	switch endpoint.media {
	case aiMediaImage:
		count := jsonInt(fields["n"])
		if endpoint.format == aiFormatGemini {
			count = jsonInt(jsonObjectField(fields["parameters"], "sampleCount"))
		}
		facts.images = imageCount(endpoint, count)
	case aiMediaSpeech:
		facts.characters = int64(utf8.RuneCountInString(jsonString(fields["input"])))
	case aiMediaVideo:
		facts.videoSeconds, facts.videoResolution, facts.videoSized = videoFacts(fields)
	}
	if endpoint.generation {
		facts.tools = readToolReserve(endpoint, facts.model, fields)
	}
	return facts
}

// readToolReserve reads the built-in tools a generation request declares
// (ADR-1433): OpenAI Responses web_search, file_search and code_interpreter
// tools, a chat completion on a search model, Anthropic web_search with its
// max_uses, and Gemini google_search grounding.
func readToolReserve(endpoint aiEndpoint, model string, fields map[string]json.RawMessage) aiToolReserve {
	reserve := aiToolReserve{codeGB: 1}
	if endpoint.format == aiFormatOpenAI && endpoint.openAIOutputField == "max_completion_tokens" {
		_, options := fields["web_search_options"]
		if options || strings.Contains(model, "-search-") || strings.HasSuffix(model, "-search") {
			reserve.chatSearch, reserve.webSearches = true, 1
		}
	}
	var tools []map[string]json.RawMessage
	if json.Unmarshal(fields["tools"], &tools) != nil {
		return reserve
	}
	for _, tool := range tools {
		kind := jsonString(tool["type"])
		switch {
		case endpoint.format == aiFormatOpenAI && strings.HasPrefix(kind, "web_search"):
			reserve.webSearches = max(reserve.webSearches, api.OutboundAIToolReserveWebSearches)
		case endpoint.format == aiFormatOpenAI && kind == "file_search":
			reserve.fileSearches = api.OutboundAIToolReserveFileSearches
		case endpoint.format == aiFormatOpenAI && kind == "code_interpreter":
			reserve.codeGB = containerMemoryGB(tool["container"])
			reserve.codeSessionGB = reserve.codeGB
		case endpoint.format == aiFormatAnthropic && strings.HasPrefix(kind, "web_search"):
			uses := jsonInt(tool["max_uses"])
			if uses <= 0 {
				uses = api.OutboundAIToolReserveWebSearches
			}
			reserve.webSearches += min(uses, api.OutboundAIToolUsesPerCallMax)
		case endpoint.format == aiFormatGemini:
			for _, key := range []string{"google_search", "googleSearch", "google_search_retrieval", "googleSearchRetrieval"} {
				if _, ok := tool[key]; ok {
					reserve.webSearches = api.OutboundAIToolReserveWebSearches
					if geminiBillsPerPrompt(model) {
						reserve.webSearches = 1
					}
				}
			}
		}
	}
	reserve.webSearches = min(reserve.webSearches, api.OutboundAIToolUsesPerCallMax)
	return reserve
}

// containerMemoryGB reads a code_interpreter tool's container memory_limit
// ("1g" to "64g"); an absent or unknown value is the provider's 1 GB.
func containerMemoryGB(raw json.RawMessage) int64 {
	var container struct {
		MemoryLimit string `json:"memory_limit"`
	}
	if json.Unmarshal(raw, &container) != nil {
		return 1
	}
	limit := strings.ToLower(strings.TrimSpace(container.MemoryLimit))
	gb, err := strconv.ParseInt(strings.TrimSuffix(limit, "g"), 10, 64)
	if err != nil || !strings.HasSuffix(limit, "g") || gb < 1 || gb > api.OutboundAIContainerMemoryGBMax {
		return 1
	}
	return gb
}

// videoResolutionText bounds a requested video resolution ("720p", "4k").
var videoResolutionText = regexp.MustCompile(`^[0-9a-z]{1,16}$`)

// videoFacts reads a Veo request's generated seconds and resolution: the
// duration (default OutboundAIVideoDefaultSeconds) times the videos asked
// for (numberOfVideos on Gemini, sampleCount on Vertex, default one) and
// instances. A request outside the bounds is not sized.
func videoFacts(fields map[string]json.RawMessage) (int64, string, bool) {
	var parameters map[string]json.RawMessage
	if raw, ok := fields["parameters"]; ok && json.Unmarshal(raw, &parameters) != nil {
		return 0, "", false
	}
	var instances []json.RawMessage
	if json.Unmarshal(fields["instances"], &instances) != nil || len(instances) == 0 {
		return 0, "", false
	}
	seconds, ok := jsonIntOrString(parameters["durationSeconds"], api.OutboundAIVideoDefaultSeconds)
	if !ok || seconds < 1 || seconds > api.OutboundAIVideoSecondsMax {
		return 0, "", false
	}
	count, ok := jsonIntOrString(parameters["numberOfVideos"], 0)
	if !ok {
		return 0, "", false
	}
	if count == 0 {
		if count, ok = jsonIntOrString(parameters["sampleCount"], 1); !ok {
			return 0, "", false
		}
	}
	videos := count * int64(len(instances))
	if count < 1 || videos > api.OutboundAIVideosPerCallMax {
		return 0, "", false
	}
	resolution := strings.ToLower(jsonString(parameters["resolution"]))
	if _, given := parameters["resolution"]; !given {
		resolution = "720p"
	}
	if !videoResolutionText.MatchString(resolution) {
		return 0, "", false
	}
	return seconds * videos, resolution, true
}

// jsonIntOrString reads a whole number sent as a JSON number or a numeric
// string; an absent or null value is fallback.
func jsonIntOrString(raw json.RawMessage, fallback int64) (int64, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fallback, true
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		value, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		return value, err == nil
	}
	var value json.Number
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return 0, false
	}
	whole, err := value.Int64()
	return whole, err == nil
}

// aiWriteFree reports a write a provider does not charge for (ADR-1432):
// uploads, token counting, moderation, cancellations and metadata updates.
// Every other write the gateway does not meter is refused unless the AI
// policy allows unmetered calls.
func aiWriteFree(provider, path string) bool {
	switch provider {
	case api.OutboundAIProviderOpenAI, api.OutboundAIProviderAzureOpenAI:
		return matchesAnySegments(apiSegments(path), openAIFreeWrites)
	case api.OutboundAIProviderAnthropic:
		return matchesAnySegments(apiSegments(path), anthropicFreeWrites)
	case api.OutboundAIProviderGemini:
		segments := apiSegments(path)
		if _, verb := modelAndVerb(path); verb == "countTokens" {
			return true
		}
		return matchesAnySegments(segments, [][]string{{"files"}}) ||
			(len(segments) == 2 && (segments[0] == "batches" || segments[0] == "operations") && strings.HasSuffix(segments[1], ":cancel"))
	case api.OutboundAIProviderVertex:
		if strings.Contains(path, "/endpoints/openapi/") {
			return matchesAnySegments(apiSegments(path[strings.Index(path, "/endpoints/openapi/")+len("/endpoints/openapi"):]), openAIFreeWrites)
		}
		_, verb := modelAndVerb(path)
		return verb == "countTokens" || verb == "computeTokens" || verb == "fetchPredictOperation"
	case api.OutboundAIProviderBedrock:
		return matchesAnySegments(apiSegments(path), [][]string{{"model", "*", "count-tokens"}})
	}
	return false
}

// openAIFreeWrites and anthropicFreeWrites are free writes under the API
// root; "*" is one path segment.
var openAIFreeWrites = [][]string{
	{"files"}, {"uploads"}, {"uploads", "*", "parts"}, {"uploads", "*", "complete"}, {"uploads", "*", "cancel"},
	{"moderations"}, {"batches", "*", "cancel"}, {"fine_tuning", "jobs", "*", "cancel"}, {"responses", "*", "cancel"},
	{"responses", "input_tokens"}, {"chat", "completions", "*"}, {"conversations"}, {"conversations", "*"},
	{"conversations", "*", "items"},
}

var anthropicFreeWrites = [][]string{
	{"messages", "count_tokens"}, {"messages", "batches", "*", "cancel"}, {"files"}, {"skills"}, {"skills", "*", "versions"},
}

// apiSegments splits a provider path after its API root: "/v1/",
// "/v1beta/" or "/v1alpha/", else Azure's "/openai/", else the whole path.
func apiSegments(path string) []string {
	rest := strings.TrimPrefix(path, "/")
	found := false
	for _, root := range []string{"/v1beta/", "/v1alpha/", "/v1/"} {
		if index := strings.Index(path, root); index >= 0 {
			rest, found = path[index+len(root):], true
			break
		}
	}
	if !found {
		if index := strings.Index(path, "/openai/"); index >= 0 {
			rest = path[index+len("/openai/"):]
		}
	}
	return strings.Split(strings.Trim(rest, "/"), "/")
}

func matchesAnySegments(segments []string, patterns [][]string) bool {
	for _, pattern := range patterns {
		if len(pattern) != len(segments) {
			continue
		}
		matched := true
		for index, part := range pattern {
			if part != "*" && part != segments[index] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// imageCount bounds a requested image count. OpenAI defaults to one image
// and Imagen to four.
func imageCount(endpoint aiEndpoint, requested int64) int64 {
	if requested <= 0 {
		requested = 1
		if endpoint.format == aiFormatGemini {
			requested = 4
		}
	}
	return min(requested, api.OutboundAIImagesPerCallMax)
}

// applyFormFacts fills the model and image count of a multipart call from
// its form fields (ADR-997).
func applyFormFacts(endpoint aiEndpoint, facts *aiCallFacts, form map[string]string) {
	if len(form) == 0 {
		return
	}
	if facts.model == "" {
		facts.model = sanitizeAIModel(form["model"])
	}
	if endpoint.media == aiMediaImage {
		requested, _ := strconv.ParseInt(form["n"], 10, 64)
		facts.images = imageCount(endpoint, requested)
	}
}

// withOutputCeiling adds the format's output ceiling field to a JSON body
// that has none.
func withOutputCeiling(endpoint aiEndpoint, body []byte, ceiling int64) ([]byte, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return nil, false
	}
	value, err := json.Marshal(ceiling)
	if err != nil {
		return nil, false
	}
	switch endpoint.format {
	case aiFormatOpenAI:
		if endpoint.openAIOutputField == "" {
			return nil, false
		}
		fields[endpoint.openAIOutputField] = value
	case aiFormatAnthropic, aiFormatBedrockInvoke:
		fields["max_tokens"] = value
	case aiFormatGemini:
		if !setNestedField(fields, "generationConfig", "maxOutputTokens", value) {
			return nil, false
		}
	case aiFormatBedrockConverse:
		if !setNestedField(fields, "inferenceConfig", "maxTokens", value) {
			return nil, false
		}
	default:
		return nil, false
	}
	rewritten, err := json.Marshal(fields)
	return rewritten, err == nil
}

func setNestedField(fields map[string]json.RawMessage, object, field string, value json.RawMessage) bool {
	nested := map[string]json.RawMessage{}
	if raw, ok := fields[object]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if json.Unmarshal(raw, &nested) != nil || nested == nil {
			return false
		}
	}
	nested[field] = value
	encoded, err := json.Marshal(nested)
	if err != nil {
		return false
	}
	fields[object] = encoded
	return true
}

func jsonString(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

// jsonInt reads a non-negative integer; anything else is 0.
func jsonInt(raw json.RawMessage) int64 {
	var value float64
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value < 0 || value > float64(api.OutboundAIMaxTokensPerResponse) {
		return 0
	}
	return int64(value)
}

func jsonObjectField(raw json.RawMessage, name string) json.RawMessage {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return fields[name]
}
