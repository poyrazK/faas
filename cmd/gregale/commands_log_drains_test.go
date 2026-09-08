package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const logDrainTestID = "0123456789abcdef0123456789abcdef"

func TestCmdLogDrainAdd_ReadsAuthHeaderFromStdin(t *testing.T) {
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	var gotMethod, gotPath string
	var gotBody api.CreateAppLogDrainRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(api.AppLogDrainResponse{
			ID: logDrainTestID, Kind: "http_json", TargetURL: "https://logs.example/ingest",
			AuthHeaderMasked: api.AppLogDrainAuthHeaderMasked, Enabled: true,
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldStdin := osStdin
	osStdin = strings.NewReader("Authorization: Bearer secret-value\n")
	t.Cleanup(func() { osStdin = oldStdin })
	var stdout bytes.Buffer
	code := captureStdoutSwap(t, &stdout, func() int {
		return cmdLogDrainAdd([]string{
			"--app", "demo", "--kind", "http_json", "--target-url", "https://logs.example/ingest",
			"--auth-header-stdin",
		})
	})
	if code != 0 {
		t.Fatalf("add = %d", code)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/apps/demo/log-drains" {
		t.Fatalf("request = %s %s, want POST /v1/apps/demo/log-drains", gotMethod, gotPath)
	}
	if gotBody.AuthHeader != "Authorization: Bearer secret-value" {
		t.Errorf("auth_header = %q", gotBody.AuthHeader)
	}
	if strings.Contains(stdout.String(), "secret-value") {
		t.Errorf("plaintext auth header leaked to stdout: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), logDrainTestID) {
		t.Errorf("stdout missing drain id: %q", stdout.String())
	}
}

func TestCmdLogDrainsList_RendersMaskedAuth(t *testing.T) {
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo/log-drains" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]api.AppLogDrainResponse{
			{ID: logDrainTestID, Kind: "otlp", TargetURL: "https://otel.example/v1/logs", AuthHeaderMasked: api.AppLogDrainAuthHeaderMasked, Enabled: false, UpdatedAt: "2026-09-08T09:00:00Z"},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var stdout bytes.Buffer
	code := captureStdoutSwap(t, &stdout, func() int {
		return cmdLogDrainsList([]string{"--app", "demo"})
	})
	if code != 0 {
		t.Fatalf("list = %d", code)
	}
	got := stdout.String()
	for _, want := range []string{logDrainTestID, "otlp", "https://otel.example/v1/logs", "disabled", api.AppLogDrainAuthHeaderMasked} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q: %q", want, got)
		}
	}
}

func TestCmdLogDrainUpdate_RejectsConflictingStateFlags(t *testing.T) {
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	if code := cmdLogDrainUpdate([]string{"--app", "demo", "--enable", "--disable", logDrainTestID}); code == 0 {
		t.Fatal("update accepted --enable and --disable together")
	}
}

func TestCmdLogDrainAdd_RejectsUnknownKindBeforeNetwork(t *testing.T) {
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	if code := cmdLogDrainAdd([]string{"--app", "demo", "--kind", "syslog", "--target-url", "https://logs.example/ingest"}); code == 0 {
		t.Fatal("add accepted unknown drain kind")
	}
}
