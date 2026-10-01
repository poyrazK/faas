package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppsTCPTLSStatusCommand(t *testing.T) {
	observed := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	expiry := observed.Add(time.Hour)
	out := api.TCPListenerTLSStatusResponse{Name: "echo", TLS: api.TCPListenerTLSConfig{Mode: api.TCPListenerTLSTerminate, Hostname: "echo.example"}, Enabled: true, Scope: "observed_edges", Observations: []api.TCPListenerTLSCertificateStatus{{EdgeID: "edge-one", Status: "ready", ObservedAt: observed, NotAfter: &expiry}, {EdgeID: "edge-two", Status: "unknown", ObservedAt: observed}}}
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/test-app/tcp-listeners/echo/tls-status" || r.Header.Get("Authorization") != "Bearer fp_test" {
			t.Errorf("incorrect authenticated status request: method=%s path=%s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer backend.Close()
	t.Setenv("FAAS_API", backend.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")
	stdout, _, restore := swapIO(t)
	defer restore()
	previous := jsonOutput
	defer func() { jsonOutput = previous }()
	jsonOutput = false
	if code := cmdAppsTCP("test-app", []string{"tls-status", "echo"}); code != 0 {
		t.Fatalf("status exit=%d", code)
	}
	for _, text := range []string{"edge-one", "ready", "edge-two", "unknown", expiry.Format(time.RFC3339), "fleet coverage unknown"} {
		if !strings.Contains(stdout.String(), text) {
			t.Fatalf("missing %q in output: %s", text, stdout)
		}
	}
	stdout.Reset()
	jsonOutput = true
	if code := cmdAppsTCP("test-app", []string{"tls-status", "echo"}); code != 0 {
		t.Fatalf("JSON status exit=%d", code)
	}
	var decoded api.TCPListenerTLSStatusResponse
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil || decoded.Scope != out.Scope || len(decoded.Observations) != 2 || decoded.Observations[1].NotAfter != nil {
		t.Fatalf("JSON projection=%+v err=%v", decoded, err)
	}
	if code := cmdAppsTCP("test-app", []string{"tls-status"}); code == 0 || requests.Load() != 2 {
		t.Fatalf("missing listener sent request: exit=%d requests=%d", code, requests.Load())
	}
}
