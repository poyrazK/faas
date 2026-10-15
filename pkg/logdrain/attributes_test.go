package logdrain

// adr: 1340 — a structured line's attributes reach OTLP drains as log
// record attributes in key order, and HTTP JSON drains as an object.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOTLPPayloadCarriesRecordAttributes(t *testing.T) {
	payload := makeOTLPPayload(Record{AppID: "app-1", AccountID: "acct-1", InstanceID: "ai-gateway", Sequence: 7, Stream: "ai_gateway",
		Line: `{"source":"ai_gateway"}`, WrittenAt: time.Unix(1, 0),
		Attributes: map[string]string{"gen_ai.system": "openai", "gen_ai.response.model": "gpt-5"}})
	attrs := payload.ResourceLogs[0].ScopeLogs[0].LogRecords[0].Attributes
	var keys []string
	for _, attr := range attrs {
		keys = append(keys, attr.Key)
	}
	joined := strings.Join(keys, ",")
	if !strings.HasSuffix(joined, ",gen_ai.response.model,gen_ai.system") {
		t.Fatalf("OTLP attributes = %s", joined)
	}
	encoded, err := json.Marshal(Record{AppID: "app-1", Attributes: map[string]string{"gen_ai.system": "openai"}})
	if err != nil || !strings.Contains(string(encoded), `"attributes":{"gen_ai.system":"openai"}`) {
		t.Fatalf("HTTP JSON record = %s, %v", encoded, err)
	}
	plain, _ := json.Marshal(Record{AppID: "app-1"})
	if strings.Contains(string(plain), "attributes") {
		t.Fatalf("a runtime line without attributes changed shape: %s", plain)
	}
}
