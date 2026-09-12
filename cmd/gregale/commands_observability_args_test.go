package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestObservabilityCommandsAcceptDocumentedArgumentOrder executes the public
// usage examples with flags after the app slug, plus the existing flags-first
// form. Go's flag package stops at the first positional unless the command
// normalizes its arguments before parsing.
func TestObservabilityCommandsAcceptDocumentedArgumentOrder(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	oldOut := osStdout
	osStdout = io.Discard
	t.Cleanup(func() {
		osStdout = oldOut
		resetJSONOutput()
	})

	tests := []struct {
		name      string
		args      []string
		wantPath  string
		queryKey  string
		queryWant string
	}{
		{"metrics documented", []string{"metrics", "demo", "--range", "5m", "--json"}, "/v1/apps/demo/metrics", "range", "5m"},
		{"metrics flags first", []string{"metrics", "--range", "1h", "demo", "--json"}, "/v1/apps/demo/metrics", "range", "1h"},
		{"slo documented", []string{"slo", "demo", "--window", "1h", "--json"}, "/v1/apps/demo/slo", "window", "1h"},
		{"slo flags first", []string{"slo", "--window", "7d", "demo", "--json"}, "/v1/apps/demo/slo", "window", "7d"},
		{"throttle documented", []string{"throttle-suggestions", "demo", "--range", "5m", "--json"}, "/v1/apps/demo/throttle-suggestions", "range", "5m"},
		{"throttle flags first", []string{"throttle-suggestions", "--range", "1h", "demo", "--json"}, "/v1/apps/demo/throttle-suggestions", "range", "1h"},
		{"throttle bool flag first", []string{"throttle-suggestions", "--dry-run", "--candidate-rps", "5", "demo", "--json"}, "/v1/apps/demo/throttle-suggestions", "dry_run", "true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetJSONOutput()
			gotPath, gotQuery = "", nil
			if code := run(tt.args); code != 0 {
				t.Fatalf("run(%q) = %d, want 0", tt.args, code)
			}
			if gotPath != tt.wantPath {
				t.Fatalf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if got := gotQuery.Get(tt.queryKey); got != tt.queryWant {
				t.Fatalf("query %s = %q, want %q", tt.queryKey, got, tt.queryWant)
			}
		})
	}
}

func TestObservabilityCommandsStillRejectInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		run  func() int
	}{
		{"metrics unknown flag", func() int { return cmdMetrics([]string{"demo", "--wat", "x"}) }},
		{"metrics extra positional", func() int { return cmdMetrics([]string{"demo", "extra", "--range", "5m"}) }},
		{"slo unknown flag", func() int { return cmdSLO([]string{"demo", "--wat", "x"}) }},
		{"slo extra positional", func() int { return cmdSLO([]string{"demo", "extra", "--window", "1h"}) }},
		{"throttle unknown flag", func() int { return cmdThrottleSuggestions([]string{"demo", "--wat", "x"}) }},
		{"throttle extra positional", func() int { return cmdThrottleSuggestions([]string{"demo", "extra", "--range", "5m"}) }},
		{"throttle invalid range", func() int { return cmdThrottleSuggestions([]string{"demo", "--range", "yesterday"}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code := tt.run(); code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
		})
	}
}
