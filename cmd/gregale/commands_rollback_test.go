package main

// CLI tests for cmdRollback (SAFE-RELEASES-G, issue #976). Pins the
// wire shape of POST /v1/apps/{slug}/rollback under the new --to flag:
// when --to is set the body carries {"target_deployment_id": "<uuid>"};
// when --to is empty the body is omitted (legacy behaviour). Also pins
// the --to missing-value and unknown-flag error paths so a future
// refactor of the inline flag parser can't silently regress.
//
// httptest.NewServer catches the actual request body so we can assert
// the JSON shape, not just the exit code.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdRollback_LegacyNoBodySendsNoBody(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/my-app/rollback" {
			t.Errorf("path = %q, want /v1/apps/my-app/rollback", r.URL.Path)
			http.Error(w, "bad path", 500)
			return
		}
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: uuid.NewString(), Status: "live"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdRollback([]string{"my-app"}); code != 0 {
		t.Fatalf("cmdRollback legacy = %d, want 0", code)
	}
	if len(capturedBody) != 0 {
		t.Errorf("legacy body = %q, want empty (no --to)", string(capturedBody))
	}
}

func TestCmdRollback_ToFlagSendsTargetID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var capturedBody map[string]any
	targetID := uuid.NewString()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: targetID, Status: "live"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdRollback([]string{"my-app", "--to", targetID}); code != 0 {
		t.Fatalf("cmdRollback --to = %d, want 0", code)
	}
	got, ok := capturedBody["target_deployment_id"].(string)
	if !ok {
		t.Fatalf("body.target_deployment_id missing or wrong type; body=%+v", capturedBody)
	}
	if got != targetID {
		t.Errorf("body.target_deployment_id = %q, want %q", got, targetID)
	}
}

func TestCmdRollback_ToFlagEqualsFormSendsTargetID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var capturedBody map[string]any
	targetID := uuid.NewString()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: targetID, Status: "live"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	// --to=<uuid> should be accepted (equals form).
	if code := cmdRollback([]string{"my-app", "--to=" + targetID}); code != 0 {
		t.Fatalf("cmdRollback --to=<id> = %d, want 0", code)
	}
	if got := capturedBody["target_deployment_id"].(string); got != targetID {
		t.Errorf("body.target_deployment_id = %q, want %q", got, targetID)
	}
}

func TestCmdRollback_ToFlagMissingValueFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	// --to with no value: should fail BEFORE making a network call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no network call expected; got %s %s", r.Method, r.URL.Path)
		http.Error(w, "should not reach", 500)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdRollback([]string{"my-app", "--to"}); code != 1 {
		t.Errorf("cmdRollback --to (no value) = %d, want 1", code)
	}
}

func TestCmdRollback_NoArgsFails(t *testing.T) {
	// Pre-existing "usage: gregale rollback <slug>" branch. Pins the
	// back-compat invariant: callers without a slug still get the
	// usage line, not a network call.
	if code := cmdRollback([]string{}); code != 1 {
		t.Errorf("cmdRollback() = %d, want 1", code)
	}
}

func TestCmdRollback_UnknownFlagFails(t *testing.T) {
	// Defense-in-depth: an unknown flag after <slug> must be
	// rejected rather than silently passed through. Catches a
	// future refactor of the inline parser that drops the unknown-
	// flag check.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no network call expected; got %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdRollback([]string{"my-app", "--bogus"}); code != 1 {
		t.Errorf("cmdRollback --bogus = %d, want 1", code)
	}
}

// Issue #3362: `--yes` is accepted (rollback never prompts) and an extra
// positional is reported as an argument, not a flag.
func TestCmdRollback_YesFlagAndPositionals(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: uuid.NewString(), Status: "live"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	cases := []struct {
		args      []string
		wantCode  int
		wantCalls int
	}{
		{args: []string{"my-app", "--to", "v2", "--yes"}, wantCode: 0, wantCalls: 1},
		{args: []string{"my-app", "-y"}, wantCode: 0, wantCalls: 1},
		{args: []string{"my-app", "v2"}, wantCode: 1, wantCalls: 0},
	}
	for _, tc := range cases {
		calls = 0
		if code := cmdRollback(tc.args); code != tc.wantCode {
			t.Errorf("cmdRollback(%q) = %d, want %d", tc.args, code, tc.wantCode)
		}
		if calls != tc.wantCalls {
			t.Errorf("cmdRollback(%q) made %d API call(s), want %d", tc.args, calls, tc.wantCalls)
		}
	}
}
