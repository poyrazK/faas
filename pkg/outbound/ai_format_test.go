package outbound

// adr: 991 — per-call wire formats, model facts and provider usage formats.

import (
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestClassifyAIEndpoint(t *testing.T) {
	for _, tc := range []struct {
		provider, path string
		want           aiEndpoint
	}{
		{api.OutboundAIProviderOpenAI, "/v1/chat/completions", aiEndpoint{format: aiFormatOpenAI, generation: true, openAIOutputField: "max_completion_tokens"}},
		{api.OutboundAIProviderOpenAI, "/v1/embeddings", aiEndpoint{format: aiFormatOpenAI, embedding: true}},
		{api.OutboundAIProviderAzureOpenAI, "/openai/deployments/prod-4o/chat/completions", aiEndpoint{format: aiFormatOpenAI, generation: true, pathModel: "prod-4o", openAIOutputField: "max_completion_tokens"}},
		{api.OutboundAIProviderAnthropic, "/v1/messages", aiEndpoint{format: aiFormatAnthropic, generation: true}},
		{api.OutboundAIProviderAnthropic, "/v1/messages/count_tokens", aiEndpoint{format: aiFormatAnthropic}},
		{api.OutboundAIProviderGemini, "/v1beta/models/gemini-2.5-pro:streamGenerateContent", aiEndpoint{format: aiFormatGemini, generation: true, pathModel: "gemini-2.5-pro"}},
		{api.OutboundAIProviderGemini, "/v1beta/models/text-embedding-004:embedContent", aiEndpoint{format: aiFormatGemini, embedding: true, pathModel: "text-embedding-004"}},
		{api.OutboundAIProviderVertex, "/v1/projects/p/locations/us-east5/publishers/anthropic/models/claude-sonnet-4@20250514:streamRawPredict",
			aiEndpoint{format: aiFormatAnthropic, generation: true, pathModel: "claude-sonnet-4@20250514"}},
		{api.OutboundAIProviderVertex, "/v1/projects/p/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent",
			aiEndpoint{format: aiFormatGemini, generation: true, pathModel: "gemini-2.5-flash"}},
		{api.OutboundAIProviderVertex, "/v1/projects/p/locations/global/endpoints/openapi/chat/completions",
			aiEndpoint{format: aiFormatOpenAI, generation: true, openAIOutputField: "max_completion_tokens"}},
		{api.OutboundAIProviderBedrock, "/model/anthropic.claude-3-5-sonnet-20240620-v1:0/converse-stream", aiEndpoint{format: aiFormatBedrockConverse, generation: true, pathModel: "anthropic.claude-3-5-sonnet-20240620-v1:0"}},
		{api.OutboundAIProviderBedrock, "/model/arn%3Aaws%3Abedrock%3Aus-east-1%3A1%3Ainference-profile%2Fx/invoke", aiEndpoint{format: aiFormatBedrockInvoke, generation: true, pathModel: "arn:aws:bedrock:us-east-1:1:inference-profile/x"}},
		{api.OutboundAIProviderBedrock, "/guardrail/x/apply", aiEndpoint{}},
	} {
		if got := classifyAIEndpoint(tc.provider, tc.path); got != tc.want {
			t.Errorf("%s %s = %+v, want %+v", tc.provider, tc.path, got, tc.want)
		}
	}
}

func TestReadAICallFacts(t *testing.T) {
	for _, tc := range []struct {
		endpoint aiEndpoint
		body     string
		want     aiCallFacts
	}{
		{aiEndpoint{format: aiFormatOpenAI, openAIOutputField: "max_completion_tokens"}, `{"model":"gpt-4o","max_tokens":300}`, aiCallFacts{model: "gpt-4o", outputCeiling: 300, ceilingKnown: true}},
		{aiEndpoint{format: aiFormatAnthropic}, `{"model":"claude","max_tokens":1024}`, aiCallFacts{model: "claude", outputCeiling: 1024, ceilingKnown: true}},
		{aiEndpoint{format: aiFormatGemini, pathModel: "g"}, `{"generationConfig":{"maxOutputTokens":77}}`, aiCallFacts{model: "g", outputCeiling: 77, ceilingKnown: true}},
		{aiEndpoint{format: aiFormatBedrockConverse, pathModel: "b"}, `{"inferenceConfig":{"maxTokens":9}}`, aiCallFacts{model: "b", outputCeiling: 9, ceilingKnown: true}},
		{aiEndpoint{format: aiFormatBedrockInvoke, pathModel: "meta.llama"}, `{"prompt":"x","max_gen_len":5}`, aiCallFacts{model: "meta.llama"}},
		{aiEndpoint{format: aiFormatBedrockInvoke, pathModel: "anthropic.c"}, `{"anthropic_version":"bedrock-2023-05-31","max_tokens":5}`, aiCallFacts{model: "anthropic.c", outputCeiling: 5, ceilingKnown: true}},
	} {
		if got := readAICallFacts(tc.endpoint, []byte(tc.body)); got != tc.want {
			t.Errorf("%s = %+v, want %+v", tc.body, got, tc.want)
		}
	}
}

func TestGeminiUsageNormalization(t *testing.T) {
	observer := newAIUsageObserverFormat(aiFormatGemini, "application/json", nil)
	_, _ = observer.Write([]byte(`{"modelVersion":"gemini-2.5-flash","usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"thoughtsTokenCount":30,"cachedContentTokenCount":40}}`))
	usage := observer.Finish()
	if usage.Model != "gemini-2.5-flash" || usage.InputTokens != 100 || usage.CachedInputTokens != 40 || usage.OutputTokens != 50 || !usage.Known {
		t.Fatalf("gemini usage = %+v", usage)
	}
	stream := newAIUsageObserverFormat(aiFormatGemini, "text/event-stream", nil)
	_, _ = stream.Write([]byte("data: {\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":1}}\n\n" +
		"data: {\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":7}}\n\n"))
	if usage := stream.Finish(); usage.InputTokens != 10 || usage.OutputTokens != 7 {
		t.Fatalf("gemini stream usage = %+v", usage)
	}
	array := newAIUsageObserverFormat(aiFormatGemini, "application/json", nil)
	_, _ = array.Write([]byte(`[{"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1}},{"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4}}]`))
	if usage := array.Finish(); usage.InputTokens != 3 || usage.OutputTokens != 4 {
		t.Fatalf("gemini array usage = %+v", usage)
	}
}

func TestBedrockConverseUsageAndFallbackModel(t *testing.T) {
	observer := newAIUsageObserverFormat(aiFormatBedrockConverse, "application/json", nil)
	observer.fallbackModel = "anthropic.claude-3-5-haiku"
	_, _ = observer.Write([]byte(`{"output":{},"usage":{"inputTokens":100,"outputTokens":20,"cacheReadInputTokens":30,"cacheWriteInputTokens":5}}`))
	usage := observer.Finish()
	if usage.Model != "anthropic.claude-3-5-haiku" || usage.InputTokens != 135 || usage.CachedInputTokens != 30 || usage.CacheWriteTokens != 5 || usage.OutputTokens != 20 {
		t.Fatalf("converse usage = %+v", usage)
	}
}

func TestBedrockInvokeHeaderUsage(t *testing.T) {
	observer := newAIUsageObserverFormat(aiFormatBedrockInvoke, "application/json", nil)
	header := http.Header{}
	header.Set("X-Amzn-Bedrock-Input-Token-Count", "12")
	header.Set("X-Amzn-Bedrock-Output-Token-Count", "34")
	observer.applyHeaders(header)
	_, _ = observer.Write([]byte(`{"generation":"hi"}`))
	if usage := observer.Finish(); usage.InputTokens != 12 || usage.OutputTokens != 34 || !usage.Known {
		t.Fatalf("invoke header usage = %+v", usage)
	}
}

// awsEventFrame builds one event-stream message with a :message-type header.
func awsEventFrame(messageType string, payload []byte) []byte {
	name := ":message-type"
	headers := []byte{byte(len(name))}
	headers = append(headers, name...)
	headers = append(headers, 7)
	headers = binary.BigEndian.AppendUint16(headers, uint16(len(messageType)))
	headers = append(headers, messageType...)
	total := 12 + len(headers) + len(payload) + 4
	frame := binary.BigEndian.AppendUint32(nil, uint32(total))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(headers)))
	frame = binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
	frame = append(frame, headers...)
	frame = append(frame, payload...)
	return binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
}

func TestBedrockEventStreamUsage(t *testing.T) {
	converse := append(awsEventFrame("event", []byte(`{"contentBlockDelta":{"delta":{"text":"hi"}}}`)),
		awsEventFrame("event", []byte(`{"usage":{"inputTokens":9,"outputTokens":3},"metrics":{"latencyMs":5}}`))...)
	converse = append(converse, awsEventFrame("exception", []byte(`{"usage":{"inputTokens":999}}`))...)
	observer := newAIUsageObserverFormat(aiFormatBedrockConverse, awsEventStreamMediaType, nil)
	for i := 0; i < len(converse); i += 7 { // split frames across writes
		_, _ = observer.Write(converse[i:min(i+7, len(converse))])
	}
	if usage := observer.Finish(); usage.InputTokens != 9 || usage.OutputTokens != 3 {
		t.Fatalf("converse-stream usage = %+v", usage)
	}

	start := base64.StdEncoding.EncodeToString([]byte(`{"type":"message_start","message":{"model":"claude-3","usage":{"input_tokens":20,"output_tokens":1}}}`))
	stop := base64.StdEncoding.EncodeToString([]byte(`{"type":"message_stop","amazon-bedrock-invocationMetrics":{"inputTokenCount":20,"outputTokenCount":15}}`))
	invoke := append(awsEventFrame("event", []byte(`{"bytes":"`+start+`"}`)), awsEventFrame("event", []byte(`{"bytes":"`+stop+`"}`))...)
	stream := newAIUsageObserverFormat(aiFormatBedrockInvoke, awsEventStreamMediaType, nil)
	_, _ = stream.Write(invoke)
	if usage := stream.Finish(); usage.Model != "claude-3" || usage.InputTokens != 20 || usage.OutputTokens != 15 {
		t.Fatalf("invoke stream usage = %+v", usage)
	}
}

func TestAWSEventStreamSkipsOversizedFrames(t *testing.T) {
	var seen int
	reader := awsEventStreamReader{onPayload: func([]byte) { seen++ }}
	big := make([]byte, api.OutboundAIUsageEventMaxBytes+100)
	reader.write(awsEventFrame("event", big))
	reader.write(awsEventFrame("event", []byte(`{}`)))
	if seen != 1 {
		t.Fatalf("payloads seen = %d, want only the bounded one", seen)
	}
}
