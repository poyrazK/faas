package api

// adr: 20261108100000 — tracing.forward names a base OTLP/HTTP URL without
// credentials and an optional headers secret, needs tracing enabled, and
// leaves configs without it unchanged on the wire.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

func TestTracingForwardValidate(t *testing.T) {
	valid := []TracingForward{
		{},
		{Endpoint: "https://otlp.example.com"},
		{Endpoint: "https://otlp.example.com/otlp/", HeadersSecret: "OTLP_HEADERS"},
		{Endpoint: "http://collector.internal:4318"},
	}
	for _, f := range valid {
		if err := f.Validate(); err != nil {
			t.Fatalf("%+v: %v", f, err)
		}
	}
	invalid := map[string]TracingForward{
		"headers without endpoint": {HeadersSecret: "OTLP_HEADERS"},
		"relative":                 {Endpoint: "otlp.example.com"},
		"other scheme":             {Endpoint: "grpc://otlp.example.com"},
		"credentials":              {Endpoint: "https://user:pass@otlp.example.com"},
		"query":                    {Endpoint: "https://otlp.example.com/?api_key=x"},
		"fragment":                 {Endpoint: "https://otlp.example.com/#x"},
		"signal path":              {Endpoint: "https://otlp.example.com/v1/traces"},
		"logs path":                {Endpoint: "https://otlp.example.com/v1/logs/"},
		"bad secret name":          {Endpoint: "https://otlp.example.com", HeadersSecret: "not-a-name"},
		"too long":                 {Endpoint: "https://otlp.example.com/" + strings.Repeat("a", TraceForwardMaxEndpointBytes)},
	}
	for name, f := range invalid {
		if err := f.Validate(); err == nil {
			t.Fatalf("%s: %+v accepted", name, f)
		} else if strings.Contains(err.Error(), "pass") {
			t.Fatalf("%s: error echoes credentials: %v", name, err)
		}
	}
}

func TestTracingConfigForwardNeedsTracing(t *testing.T) {
	cfg := &TracingConfig{Forward: TracingForward{Endpoint: "https://otlp.example.com"}}
	if err := cfg.Validate(PlanPro); err == nil || !strings.Contains(err.Error(), "tracing.enabled") {
		t.Fatalf("forward without tracing: %v", err)
	}
	cfg.Enabled = true
	if err := cfg.Validate(PlanPro); err != nil {
		t.Fatalf("forward with tracing: %v", err)
	}
	cfg.Forward.Endpoint = "ftp://x"
	if err := cfg.Validate(PlanPro); err == nil {
		t.Fatal("invalid forward endpoint passed TracingConfig.Validate")
	}
}

func TestTracingForwardSignalURL(t *testing.T) {
	for endpoint, want := range map[string]string{
		"https://otlp.example.com":       "https://otlp.example.com/v1/traces",
		"https://otlp.example.com/otlp/": "https://otlp.example.com/otlp/v1/traces",
	} {
		if got := (TracingForward{Endpoint: endpoint}).SignalURL(TraceLocalTracesPath); got != want {
			t.Fatalf("%s: %s, want %s", endpoint, got, want)
		}
	}
}

func TestTracingConfigForwardWireShape(t *testing.T) {
	plain := TracingConfig{Enabled: true}
	encoded, _ := json.Marshal(plain)
	if string(encoded) != `{"enabled":true}` {
		t.Fatalf("config without forward changed shape: %s", encoded)
	}
	var y bytes.Buffer
	if err := yaml.NewEncoder(&y).Encode(plain); err != nil || strings.Contains(y.String(), "forward") {
		t.Fatalf("yaml = %q, %v", y.String(), err)
	}
	var tm bytes.Buffer
	if err := toml.NewEncoder(&tm).Encode(plain); err != nil || strings.Contains(tm.String(), "forward") {
		t.Fatalf("toml = %q, %v", tm.String(), err)
	}

	withForward := TracingConfig{Enabled: true, Forward: TracingForward{Endpoint: "https://otlp.example.com", HeadersSecret: "OTLP_HEADERS"}}
	encoded, _ = json.Marshal(withForward)
	var back TracingConfig
	if err := json.Unmarshal(encoded, &back); err != nil || back != withForward {
		t.Fatalf("json round trip = %+v, %v", back, err)
	}
	var fromYAML TracingConfig
	if err := yaml.Unmarshal([]byte("enabled: true\nforward:\n  endpoint: https://otlp.example.com\n  headers_secret: OTLP_HEADERS\n"), &fromYAML); err != nil || fromYAML != withForward {
		t.Fatalf("yaml = %+v, %v", fromYAML, err)
	}
	var fromTOML TracingConfig
	if _, err := toml.Decode("enabled = true\n[forward]\nendpoint = \"https://otlp.example.com\"\nheaders_secret = \"OTLP_HEADERS\"\n", &fromTOML); err != nil || fromTOML != withForward {
		t.Fatalf("toml = %+v, %v", fromTOML, err)
	}
}
