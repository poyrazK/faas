package outbound

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// openai-format routes also accept OpenAI embeddings requests (ADR-993).
// OpenAI and Azure OpenAI targets get them unchanged; Gemini and Vertex AI
// Google models are translated. Anthropic and Bedrock have no translated
// embeddings.

type openAIEmbeddingRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	Dimensions     *int64          `json:"dimensions"`
	EncodingFormat string          `json:"encoding_format"`
}

// embeddingInputs reads input as one string or an array of strings. Token
// arrays are OpenAI-only.
func embeddingInputs(raw json.RawMessage) ([]string, error) {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, nil
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil || len(many) == 0 {
		return nil, refuseTranslation("embeddings input must be a string or an array of strings for this target")
	}
	return many, nil
}

func translateEmbeddingsRequest(integration Integration, providerPath, rawQuery string, body []byte, model string) (routeTranslation, error) {
	switch integration.AIProvider {
	case api.OutboundAIProviderOpenAI:
		if model != "" {
			rewritten, err := withModel(body, model)
			if err != nil {
				return routeTranslation{}, errTranslationUnsupported
			}
			body = rewritten
		}
		return routeTranslation{path: providerPath, rawQuery: rawQuery, body: body}, nil
	case api.OutboundAIProviderAzureOpenAI:
		rewritten, err := withModel(body, model)
		if err != nil {
			return routeTranslation{}, errTranslationUnsupported
		}
		return routeTranslation{path: "/openai/v1/embeddings", body: rewritten}, nil
	case api.OutboundAIProviderGemini, api.OutboundAIProviderVertex:
	default:
		return routeTranslation{}, refuseTranslation(integration.AIProvider + " targets have no translated embeddings")
	}
	var request openAIEmbeddingRequest
	if json.Unmarshal(body, &request) != nil || model == "" {
		return routeTranslation{}, errTranslationUnsupported
	}
	if request.EncodingFormat != "" && request.EncodingFormat != "float" && request.EncodingFormat != "base64" {
		return routeTranslation{}, refuseTranslation(`encoding_format must be "float" or "base64"`)
	}
	inputs, err := embeddingInputs(request.Input)
	if err != nil {
		return routeTranslation{}, err
	}
	translator := &embeddingTranslator{model: model, base64: request.EncodingFormat == "base64"}
	if integration.AIProvider == api.OutboundAIProviderGemini {
		requests := make([]map[string]any, 0, len(inputs))
		for _, input := range inputs {
			entry := map[string]any{"model": "models/" + model, "content": map[string]any{"parts": []geminiPart{{"text": input}}}}
			if request.Dimensions != nil {
				entry["outputDimensionality"] = *request.Dimensions
			}
			requests = append(requests, entry)
		}
		translated, err := json.Marshal(map[string]any{"requests": requests})
		if err != nil {
			return routeTranslation{}, errTranslationUnsupported
		}
		return routeTranslation{path: "/v1beta/models/" + url.PathEscape(model) + ":batchEmbedContents", body: translated,
			translator: translator}, nil
	}
	base, ok := api.OutboundVertexBasePath(integration.AllowedPathPrefixes)
	if !ok {
		return routeTranslation{}, refuseTranslation("the Vertex integration's allowed path prefixes must name one /v1/projects/{project}/locations/{location}")
	}
	publisher, name := vertexPublisherModel(model)
	if publisher != "google" || name == "" {
		return routeTranslation{}, refuseTranslation("Vertex embeddings targets must be Google models")
	}
	instances := make([]map[string]string, 0, len(inputs))
	for _, input := range inputs {
		instances = append(instances, map[string]string{"content": input})
	}
	out := map[string]any{"instances": instances}
	if request.Dimensions != nil {
		out["parameters"] = map[string]any{"outputDimensionality": *request.Dimensions}
	}
	translated, err := json.Marshal(out)
	if err != nil {
		return routeTranslation{}, errTranslationUnsupported
	}
	translator.model = name
	return routeTranslation{path: base + "/publishers/google/models/" + url.PathEscape(name) + ":predict", body: translated,
		translator: translator}, nil
}

// embeddingTranslator turns Gemini batchEmbedContents and Vertex AI predict
// responses into an OpenAI embeddings list. Float values pass through as
// the provider wrote them; base64 encodes little-endian float32s, as OpenAI
// does.
type embeddingTranslator struct {
	model  string
	base64 bool
}

func (t *embeddingTranslator) json(status int, body []byte) []byte {
	if status < 200 || status > 299 {
		return openAIErrorBody(status, body)
	}
	var response struct {
		// Gemini batchEmbedContents.
		Embeddings []struct {
			Values json.RawMessage `json:"values"`
		} `json:"embeddings"`
		// Vertex AI predict.
		Predictions []struct {
			Embeddings struct {
				Values     json.RawMessage `json:"values"`
				Statistics struct {
					TokenCount int64 `json:"token_count"`
				} `json:"statistics"`
			} `json:"embeddings"`
		} `json:"predictions"`
	}
	if json.Unmarshal(body, &response) != nil {
		return openAIErrorBody(http.StatusBadGateway, nil)
	}
	var vectors []json.RawMessage
	var tokens int64
	for _, embedding := range response.Embeddings {
		vectors = append(vectors, embedding.Values)
	}
	for _, prediction := range response.Predictions {
		vectors = append(vectors, prediction.Embeddings.Values)
		tokens += max(prediction.Embeddings.Statistics.TokenCount, 0)
	}
	var out bytes.Buffer
	out.WriteString(`{"object":"list","data":[`)
	for index, vector := range vectors {
		if index > 0 {
			out.WriteByte(',')
		}
		out.WriteString(`{"object":"embedding","index":` + strconv.Itoa(index) + `,"embedding":`)
		if !t.writeVector(&out, vector) {
			return openAIErrorBody(http.StatusBadGateway, nil)
		}
		out.WriteByte('}')
	}
	model, _ := json.Marshal(t.model)
	out.WriteString(`],"model":` + string(model) + `,"usage":{"prompt_tokens":` + strconv.FormatInt(tokens, 10) +
		`,"total_tokens":` + strconv.FormatInt(tokens, 10) + `}}`)
	return out.Bytes()
}

func (t *embeddingTranslator) writeVector(out *bytes.Buffer, values json.RawMessage) bool {
	if !t.base64 {
		if len(bytes.TrimSpace(values)) == 0 {
			return false
		}
		out.Write(values)
		return true
	}
	var floats []float64
	if json.Unmarshal(values, &floats) != nil {
		return false
	}
	packed := make([]byte, 4*len(floats))
	for i, value := range floats {
		binary.LittleEndian.PutUint32(packed[4*i:], math.Float32bits(float32(value)))
	}
	encoded, _ := json.Marshal(base64.StdEncoding.EncodeToString(packed))
	out.Write(encoded)
	return true
}

// Embeddings responses never stream.
func (t *embeddingTranslator) event([]byte) []byte { return nil }
func (t *embeddingTranslator) end() []byte         { return nil }
