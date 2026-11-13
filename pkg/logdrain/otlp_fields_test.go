package logdrain

// The OTLP drain uses the standard log record and resource fields, so a
// receiver links lines to traces, filters by level and groups by service
// without custom attribute mapping. The faas.* attributes stay for existing
// queries.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const validTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

func otlpResourceValue(t *testing.T, payload otlpLogsPayload, key string) (string, bool) {
	t.Helper()
	for _, kv := range payload.ResourceLogs[0].Resource.Attributes {
		if kv.Key == key {
			return kv.Value.StringValue, true
		}
	}
	return "", false
}

func TestOTLPLogRecordCarriesStandardTraceID(t *testing.T) {
	payload := makeOTLPPayload("my-api", Record{AppID: "app-1", TraceID: validTraceID, Stream: "stdout", Line: "x", WrittenAt: time.Unix(1, 0)})
	record := payload.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	if record.TraceID != validTraceID {
		t.Fatalf("traceId = %q, want %q", record.TraceID, validTraceID)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"traceId":"`+validTraceID+`"`) {
		t.Fatalf("OTLP/JSON body lacks traceId: %s", body)
	}
	found := false
	for _, kv := range record.Attributes {
		if kv.Key == "faas.trace.id" && kv.Value.StringValue == validTraceID {
			found = true
		}
	}
	if !found {
		t.Fatal("faas.trace.id attribute was dropped; existing queries rely on it")
	}
}

func TestOTLPLogRecordOmitsInvalidTraceID(t *testing.T) {
	for _, id := range []string{"", "trace-1", strings.Repeat("0", 32), strings.ToUpper(validTraceID), validTraceID + "00", "zz" + validTraceID[2:]} {
		record := makeOTLPPayload("my-api", Record{TraceID: id}).ResourceLogs[0].ScopeLogs[0].LogRecords[0]
		if record.TraceID != "" {
			t.Fatalf("trace id %q encoded as traceId %q; a receiver would reject the export", id, record.TraceID)
		}
		body, _ := json.Marshal(record)
		if strings.Contains(string(body), `"traceId"`) {
			t.Fatalf("trace id %q: body carries traceId: %s", id, body)
		}
	}
}

func TestOTLPLogRecordSeverityFollowsStream(t *testing.T) {
	cases := []struct {
		stream string
		number int
		text   string
	}{
		{"stdout", 9, "INFO"},
		{"stderr", 17, "ERROR"},
		{"ai_gateway", 9, "INFO"},
		{"", 9, "INFO"},
	}
	for _, tc := range cases {
		record := makeOTLPPayload("my-api", Record{Stream: tc.stream}).ResourceLogs[0].ScopeLogs[0].LogRecords[0]
		if record.SeverityNumber != tc.number || record.SeverityText != tc.text {
			t.Fatalf("stream %q: severity = %d %q, want %d %q", tc.stream, record.SeverityNumber, record.SeverityText, tc.number, tc.text)
		}
		// Consistent with the Datadog drain's status for the same line.
		if got := makeDatadogLog("my-api", Record{Stream: tc.stream}).Status; strings.ToUpper(got) != tc.text {
			t.Fatalf("stream %q: datadog status %q disagrees with OTLP %q", tc.stream, got, tc.text)
		}
	}
}

func TestOTLPResourceNamesTheApp(t *testing.T) {
	payload := makeOTLPPayload("my-api", Record{
		AppID: "app-1", InstanceID: "vm-1", CommitSHA: "abc123", Environment: "production", Region: "eu-fsn1",
	})
	want := map[string]string{
		"service.name":                "my-api",
		"service.instance.id":         "vm-1",
		"service.version":             "abc123",
		"deployment.environment.name": "production",
		"cloud.region":                "eu-fsn1",
	}
	for key, value := range want {
		if got, ok := otlpResourceValue(t, payload, key); !ok || got != value {
			t.Fatalf("resource %s = %q (present %v), want %q", key, got, ok, value)
		}
	}
}

func TestOTLPResourceServiceNameFallbacks(t *testing.T) {
	if got, _ := otlpResourceValue(t, makeOTLPPayload("", Record{AppID: "app-1"}), "service.name"); got != "app-1" {
		t.Fatalf("service.name without a slug = %q, want the app id", got)
	}
	if got, _ := otlpResourceValue(t, makeOTLPPayload("", Record{}), "service.name"); got != "gregale" {
		t.Fatalf("service.name without slug or app = %q, want gregale", got)
	}
	payload := makeOTLPPayload("my-api", Record{})
	for _, key := range []string{"service.instance.id", "service.version", "deployment.environment.name", "cloud.region"} {
		if _, ok := otlpResourceValue(t, payload, key); ok {
			t.Fatalf("empty %s was sent as a resource attribute", key)
		}
	}
}

func TestOTLPBatchKeepsServicePerRecord(t *testing.T) {
	body, contentType, err := encodeBatch(KindOTLP, "my-api", []Record{
		{AppID: "app-1", TraceID: validTraceID, Stream: "stderr", Line: "a"},
		{AppID: "app-1", Stream: "stdout", Line: "b"},
	})
	if err != nil || contentType != "application/json" {
		t.Fatalf("encodeBatch: %v %q", err, contentType)
	}
	var decoded otlpLogsPayload
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.ResourceLogs) != 2 {
		t.Fatalf("resourceLogs = %d, want 2", len(decoded.ResourceLogs))
	}
	for i, rl := range decoded.ResourceLogs {
		if rl.Resource.Attributes[0].Key != "service.name" || rl.Resource.Attributes[0].Value.StringValue != "my-api" {
			t.Fatalf("record %d resource = %+v", i, rl.Resource.Attributes)
		}
	}
	first := decoded.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	second := decoded.ResourceLogs[1].ScopeLogs[0].LogRecords[0]
	if first.TraceID != validTraceID || first.SeverityNumber != 17 || second.TraceID != "" || second.SeverityNumber != 9 {
		t.Fatalf("records = %+v / %+v", first, second)
	}
}
