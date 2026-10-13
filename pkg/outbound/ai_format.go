package outbound

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"

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
		return aiEndpoint{format: aiFormatAnthropic, generation: strings.HasSuffix(path, "/messages")}
	case api.OutboundAIProviderGemini:
		model, verb := modelAndVerb(path)
		return aiEndpoint{format: aiFormatGemini, pathModel: model, generation: geminiGenerationVerb(verb),
			embedding: verb == "embedContent" || verb == "batchEmbedContents"}
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
	}
	return endpoint
}

// billable reports whether a call through the endpoint is charged by the
// provider: generation and embeddings, not token counting or listings.
func (e aiEndpoint) billable() bool { return e.generation || e.embedding }

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
		// predict also serves non-token models (images); only embedding
		// models are billed in tokens.
		return aiEndpoint{format: aiFormatGemini, pathModel: model, generation: geminiGenerationVerb(verb),
			embedding: verb == "embedContent" || verb == "batchEmbedContents" || (verb == "predict" && strings.Contains(model, "embedding"))}
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
	return facts
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
