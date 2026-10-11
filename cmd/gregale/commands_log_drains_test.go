package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func logDrainTestServer(t *testing.T, created *api.CreateAppLogDrainRequest) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		drain := api.AppLogDrainResponse{ID: "drain-1", Kind: "http_json", TargetURL: "https://logs.example.com/in", Enabled: true}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/web/log-drains":
			if err := json.NewDecoder(r.Body).Decode(created); err != nil {
				t.Errorf("decode create: %v", err)
			}
			drain.AuthHeaderMasked = api.AppLogDrainAuthHeaderMasked
			writeJSONTest(w, drain)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/web/log-drains":
			writeJSONTest(w, []api.AppLogDrainResponse{drain})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	return srv
}

func captureLogDrainOutput(t *testing.T, asJSON bool) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	osStdout, osStderr, jsonOutput = &out, io.Discard, asJSON
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	return &out
}

// production-us H4-63: log drains were reachable only through the raw API.
func TestCmdLogDrainsListJSON(t *testing.T) {
	logDrainTestServer(t, &api.CreateAppLogDrainRequest{})
	out := captureLogDrainOutput(t, false)
	jsonOutput = true
	if code := cmdLogDrains([]string{"list", "web"}); code != 0 {
		t.Fatalf("log-drains list exit = %d", code)
	}
	var drains []api.AppLogDrainResponse
	if err := json.Unmarshal(out.Bytes(), &drains); err != nil || len(drains) != 1 || drains[0].ID != "drain-1" {
		t.Fatalf("--json output = %q (%v)", out.String(), err)
	}
}

// The destination credential comes from the environment, reaches the API,
// and is never echoed back in the clear.
func TestCmdLogDrainsAddReadsCredentialFromEnvironment(t *testing.T) {
	var created api.CreateAppLogDrainRequest
	logDrainTestServer(t, &created)
	out := captureLogDrainOutput(t, false)
	t.Setenv("LOG_DRAIN_TOKEN", "Bearer drain-secret")
	if code := cmdLogDrains([]string{"add", "--app", "web", "--url", "https://logs.example.com/in", "--auth-header-env", "LOG_DRAIN_TOKEN"}); code != 0 {
		t.Fatalf("log-drains add exit = %d", code)
	}
	if created.AuthHeader != "Bearer drain-secret" || created.Kind != "http_json" {
		t.Fatalf("create request = %+v", created)
	}
	if strings.Contains(out.String(), "drain-secret") {
		t.Fatalf("credential echoed: %q", out.String())
	}
}

func TestCmdLogDrainsAddRefusesAnUnsetCredentialVariable(t *testing.T) {
	var created api.CreateAppLogDrainRequest
	logDrainTestServer(t, &created)
	captureLogDrainOutput(t, false)
	if code := cmdLogDrains([]string{"add", "--app", "web", "--url", "https://logs.example.com/in", "--auth-header-env", "LOG_DRAIN_TOKEN_UNSET"}); code == 0 {
		t.Fatal("an unset credential variable created a drain")
	}
	if created.TargetURL != "" {
		t.Fatalf("request reached the API: %+v", created)
	}
}
