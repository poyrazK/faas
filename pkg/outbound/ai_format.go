package outbound

import (
	"bytes"
	"encoding/json"
	"net/url"
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
	// estimate and settles when the batch ends (ADR-1360).
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
	}
	return facts
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
