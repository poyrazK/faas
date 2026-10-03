package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppsTCPTLSCommands(t *testing.T) {
	requests := 0
	out := api.TCPListenerResponse{Name: "echo", GuestPort: 9000, PublicPort: 40142, TLS: api.TCPListenerTLSConfig{Mode: api.TCPListenerTLSTerminate, Hostname: "echo.example"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		expectedPath := "/v1/apps/test-app/tcp-listeners"
		if r.Method == http.MethodPatch {
			expectedPath += "/echo"
		}
		if r.Header.Get("Authorization") != "Bearer fp_test" || r.URL.Path != expectedPath {
			t.Error("incorrect authenticated endpoint")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch {
			var request api.UpdateTCPListenerRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request.Enabled != nil || request.TLS == nil || *request.TLS != out.TLS {
				t.Errorf("update=%+v", request)
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]api.TCPListenerResponse{out})
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("method=%s", r.Method)
		}
		var request api.CreateTCPListenerRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Name != "echo" || request.GuestPort != 9000 || request.TLS != out.TLS {
			t.Errorf("request=%+v", request)
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")
	stdout, _, restore := swapIO(t)
	defer restore()
	previousJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = previousJSON }()
	if code := cmdAppsTCP("test-app", []string{"add", "--name", "echo", "--guest-port", "9000", "--tls-mode", "terminate", "--tls-hostname", "Echo.Example"}); code != 0 {
		t.Fatalf("add exit=%d", code)
	}
	if !strings.Contains(stdout.String(), "Created disabled TLS listener") || !strings.Contains(stdout.String(), "echo.example") {
		t.Fatalf("output=%s", stdout)
	}
	stdout.Reset()
	if code := cmdAppsTCP("test-app", nil); code != 0 {
		t.Fatalf("list exit=%d", code)
	}
	if !strings.Contains(stdout.String(), "terminate") || !strings.Contains(stdout.String(), "echo.example") {
		t.Fatalf("list=%s", stdout)
	}
	stdout.Reset()
	if code := cmdAppsTCP("test-app", []string{"tls", "echo", "--tls-mode", "terminate", "--tls-hostname", "Echo.Example"}); code != 0 {
		t.Fatalf("TLS update exit=%d", code)
	}
	if !strings.Contains(stdout.String(), "listener is disabled") {
		t.Fatalf("update output=%s", stdout)
	}
	before := requests
	if code := cmdAppsTCP("test-app", []string{"tls", "echo"}); code == 0 {
		t.Fatal("TLS update without policy accepted")
	}
	for _, flags := range [][]string{{"--tls-mode", "invalid"}, {"--tls-mode", "terminate"}, {"--tls-hostname", "echo.example"}, {"--tls-mode", "terminate", "--tls-hostname", "*.example"}} {
		args := append([]string{"add", "--name", "echo", "--guest-port", "9000"}, flags...)
		if code := cmdAppsTCP("test-app", args); code == 0 {
			t.Fatalf("invalid flags accepted: %v", flags)
		}
	}
	if requests != before {
		t.Fatal("invalid TLS flags made HTTP request")
	}
}
