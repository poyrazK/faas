package main_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
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
			main.WriteJSONTest(w, drain)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/web/log-drains":
			main.WriteJSONTest(w, []api.AppLogDrainResponse{drain})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/web/log-drains/drain-1":
			w.WriteHeader(http.StatusNoContent)
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
	oldOut, oldErr, oldJSON := (*main.OsStdout), (*main.OsStderr), (*main.JsonOutput)
	(*main.OsStdout), (*main.OsStderr), (*main.JsonOutput) = &out, io.Discard, asJSON
	t.Cleanup(func() { (*main.OsStdout), (*main.OsStderr), (*main.JsonOutput) = oldOut, oldErr, oldJSON })
	return &out
}

// production-us H4-63: log drains were reachable only through the raw API.
func TestCmdLogDrainsListJSON(t *testing.T) {
	logDrainTestServer(t, &api.CreateAppLogDrainRequest{})
	out := captureLogDrainOutput(t, false)
	(*main.JsonOutput) = true
	if code := main.CmdLogDrains([]string{"list", "web"}); code != 0 {
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
	if code := main.CmdLogDrains([]string{"add", "--app", "web", "--url", "https://logs.example.com/in", "--auth-header-env", "LOG_DRAIN_TOKEN"}); code != 0 {
		t.Fatalf("log-drains add exit = %d", code)
	}
	if created.AuthHeader != "Authorization: Bearer drain-secret" || created.Kind != "http_json" {
		t.Fatalf("create request = %+v", created)
	}
	if strings.Contains(out.String(), "drain-secret") {
		t.Fatalf("credential echoed: %q", out.String())
	}
}

// production-us hunt #9 (H9-107): the documented LOG_TOKEN='Bearer …' was
// sent verbatim and the API rejected it as not a "Name: value" pair.
func TestLogDrainAuthHeaderPairAcceptsTheDocumentedForms(t *testing.T) {
	for _, tc := range []struct {
		value, want string
		wantErr     bool
	}{
		{value: "Bearer drain-secret", want: "Authorization: Bearer drain-secret"},
		{value: "Splunk 0f3c-11", want: "Authorization: Splunk 0f3c-11"},
		{value: "Authorization: Bearer drain-secret", want: "Authorization: Bearer drain-secret"},
		{value: "DD-API-KEY: abc123", want: "DD-API-KEY: abc123"},
		{value: "Bearer a:b", wantErr: true},
		{value: "X-Key: a:b", wantErr: true},
		{value: "X-Key:", wantErr: true},
	} {
		got, err := main.LogDrainAuthHeaderPair(tc.value)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("logDrainAuthHeaderPair(%q) = %q, %v; want %q, err=%v", tc.value, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestCmdLogDrainsAddRefusesAnUnsetCredentialVariable(t *testing.T) {
	var created api.CreateAppLogDrainRequest
	logDrainTestServer(t, &created)
	captureLogDrainOutput(t, false)
	if code := main.CmdLogDrains([]string{"add", "--app", "web", "--url", "https://logs.example.com/in", "--auth-header-env", "LOG_DRAIN_TOKEN_UNSET"}); code == 0 {
		t.Fatal("an unset credential variable created a drain")
	}
	if created.TargetURL != "" {
		t.Fatalf("request reached the API: %+v", created)
	}
}

// H10-17: the usage line shows `rm --id <ID> [<slug>]`, but a slug after the
// flags was rejected as an unexpected positional argument.
func TestCmdLogDrainsRmAcceptsTheSlugAfterFlags(t *testing.T) {
	logDrainTestServer(t, &api.CreateAppLogDrainRequest{})
	for _, args := range [][]string{
		{"rm", "--id", "drain-1", "web"},
		{"rm", "web", "--id", "drain-1"},
		{"rm", "--app", "web", "--id", "drain-1"},
	} {
		out := captureLogDrainOutput(t, false)
		if code := main.CmdLogDrains(args); code != 0 {
			t.Fatalf("log-drains %q exit = %d", args, code)
		}
		if !strings.Contains(out.String(), "Deleted log drain drain-1") {
			t.Fatalf("log-drains %q output = %q", args, out.String())
		}
	}
	if code := main.CmdLogDrains([]string{"rm", "--id", "drain-1", "web", "extra"}); code == 0 {
		t.Fatal("two positional arguments must still be rejected")
	}
}

// TestCmdLogDrainsRmAcceptsAPositionalID pins H10-125: `log-drains rm <id>
// --app web` read the id as a second app slug and failed with "pass the app
// once". A positional UUID is the drain id when --id is not given.
func TestCmdLogDrainsRmAcceptsAPositionalID(t *testing.T) {
	const id = "f74a5211-7ff4-4ab8-9636-bb45df3bb4d4"
	deleted := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/web/log-drains/"+id {
			deleted++
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	for _, args := range [][]string{
		{"rm", id, "--app", "web"},
		{"rm", "--app", "web", id},
		{"rm", "web", id},
	} {
		captureLogDrainOutput(t, false)
		if code := main.CmdLogDrains(args); code != 0 {
			t.Fatalf("log-drains %q exit = %d", args, code)
		}
	}
	if deleted != 3 {
		t.Fatalf("deleted %d times, want 3", deleted)
	}
	if code := main.CmdLogDrains([]string{"rm", "web", "not-a-drain-id"}); code == 0 {
		t.Fatal("a non-UUID second positional must still be rejected")
	}
}
