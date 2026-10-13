package logdrain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEncodeDatadog_ShapesRecordForIntake(t *testing.T) {
	at := time.Date(2026, 10, 9, 12, 0, 0, 123, time.UTC)
	body, contentType, err := encode(KindDatadog, "Shop-API", Record{
		AppID: "app-1", DeploymentID: "dep-1", InstanceID: "inst-1", Region: "EU West",
		CommitSHA: "0123456789abcdef0123", Environment: "Production", TraceID: "trace-1",
		Sequence: 7, Stream: "stderr", Line: "boom", WrittenAt: at,
	})
	if err != nil || contentType != "application/json" {
		t.Fatalf("encode = %q, %v", contentType, err)
	}
	var got []map[string]any
	if err := json.Unmarshal(body, &got); err != nil || len(got) != 1 {
		t.Fatalf("body %s is not a one-entry array: %v", body, err)
	}
	entry := got[0]
	for key, want := range map[string]any{
		"message": "boom", "ddsource": "gregale", "service": "shop-api", "hostname": "inst-1",
		"status": "error", "app_id": "app-1", "trace_id": "trace-1", "timestamp": at.Format(time.RFC3339Nano),
	} {
		if entry[key] != want {
			t.Errorf("%s = %v, want %v", key, entry[key], want)
		}
	}
	if tags := entry["ddtags"]; tags != "env:production,version:0123456789ab,deployment_id:dep-1,region:eu_west" {
		t.Errorf("ddtags = %v", tags)
	}
	if _, leaked := entry["line"]; leaked {
		t.Error("datadog entry must use message, not the http_json line field")
	}
}

func TestEncodeDatadog_StdoutIsInfoAndTagValuesCannotInject(t *testing.T) {
	body, _, err := encode(KindDatadog, "app", Record{Stream: "stdout", DeploymentTag: "v1,env:evil"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"status":"info"`) {
		t.Errorf("stdout should be info: %s", body)
	}
	if !strings.Contains(string(body), `"ddtags":"version:v1_env_evil"`) {
		t.Errorf("tag value must not add a tag: %s", body)
	}
}

func TestEncodeHTTPJSON_OmitsEnvironment(t *testing.T) {
	body, _, err := encode(KindHTTPJSON, "app", Record{Environment: "production", Line: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "production") {
		t.Errorf("http_json wire shape must not change: %s", body)
	}
}

func TestNew_DatadogRequiresIntakeURL(t *testing.T) {
	if _, err := New(Config{Kind: KindDatadog, TargetURL: "https://logs.example.com/ingest", AuthHeader: "DD-API-KEY: k"}); err == nil {
		t.Fatal("a datadog drain must not accept an arbitrary URL")
	}
	if _, err := New(Config{Kind: KindDatadog, TargetURL: "https://http-intake.logs.datadoghq.eu/api/v2/logs", AuthHeader: "DD-API-KEY: k"}); err != nil {
		t.Fatalf("eu1 intake rejected: %v", err)
	}
}
