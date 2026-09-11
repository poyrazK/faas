package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const testConfigTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

func TestConfigReadCommandsUseOperatorSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("faas_sid"); err != nil || cookie.Value != "opaque-session" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		switch r.URL.Path {
		case "/v1/admin/config":
			writeTestJSON(w, http.StatusOK, operatorConfigListResponse{Items: []api.OperatorRuntimeConfig{
				testHotConfigEntry(),
			}})
		case "/v1/admin/config/hsts_enabled/revisions":
			if r.URL.Query().Get("limit") != "20" {
				t.Errorf("history query = %q", r.URL.RawQuery)
			}
			writeTestJSON(w, http.StatusOK, operatorConfigHistoryResponse{Items: []api.OperatorRuntimeConfigRevision{{
				Key: "hsts_enabled", Version: 3, OldValue: json.RawMessage("false"), NewValue: json.RawMessage("true"),
				ActorID: "operator-1", Reason: "restore security header", CreatedAt: "2026-09-09T12:00:00Z",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "list", args: []string{"list"}, want: "key=hsts_enabled"},
		{name: "show", args: []string{"show", "--key", "hsts_enabled"}, want: "description=Emit HSTS"},
		{name: "history", args: []string{"history", "--key", "hsts_enabled", "--limit", "20"}, want: "version=3 old=false new=true"},
	}
	for _, tc := range tests {
		out.Reset()
		stderr.Reset()
		if code := cmdConfigDispatch(tc.args); code != 0 {
			t.Fatalf("%s exit = %d, stderr=%s", tc.name, code, stderr.String())
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("%s stdout = %q, want substring %q", tc.name, out.String(), tc.want)
		}
	}
}

func TestConfigSetUsesVersionConfirmationAndTrace(t *testing.T) {
	var patchSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/config":
			writeTestJSON(w, http.StatusOK, operatorConfigListResponse{Items: []api.OperatorRuntimeConfig{testHotConfigEntry()}})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/admin/config/hsts_enabled":
			patchSeen = true
			assertConfigMutationHeaders(t, r)
			var request operatorConfigPatchRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode patch: %v", err)
			}
			if string(request.Value) != "false" || request.Reason != "incident mitigation" || request.ExpectedVersion == nil || *request.ExpectedVersion != 3 {
				t.Errorf("patch request = %+v", request)
			}
			response := testHotConfigEntry()
			response.DesiredValue, response.EffectiveValue, response.Version = json.RawMessage("false"), json.RawMessage("false"), 4
			writeTestJSON(w, http.StatusOK, response)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()

	code := cmdConfigDispatch([]string{"set", "--key", "hsts_enabled", "--value", "false", "--reason", "incident mitigation", "--trace-id", testConfigTraceID, "--yes"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}
	if !patchSeen || !strings.Contains(out.String(), "version=4") || !strings.Contains(out.String(), "trace_id="+testConfigTraceID) {
		t.Fatalf("patchSeen=%t stdout=%q", patchSeen, out.String())
	}
}

func TestConfigRollbackUsesCurrentVersionPrecondition(t *testing.T) {
	var rollbackSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/config":
			writeTestJSON(w, http.StatusOK, operatorConfigListResponse{Items: []api.OperatorRuntimeConfig{testHotConfigEntry()}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/config/hsts_enabled/rollback":
			rollbackSeen = true
			assertConfigMutationHeaders(t, r)
			var request api.RollbackOperatorRuntimeConfigRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode rollback: %v", err)
			}
			if request.Version != 1 || request.Reason != "incident resolved" || request.ExpectedVersion == nil || *request.ExpectedVersion != 3 {
				t.Errorf("rollback request = %+v", request)
			}
			response := testHotConfigEntry()
			response.Version = 4
			writeTestJSON(w, http.StatusOK, response)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()

	code := cmdConfigDispatch([]string{"rollback", "--key", "hsts_enabled", "--version", "1", "--reason", "incident resolved", "--trace-id", testConfigTraceID, "--yes"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}
	if !rollbackSeen || !strings.Contains(out.String(), "trace_id="+testConfigTraceID) {
		t.Fatalf("rollbackSeen=%t stdout=%q", rollbackSeen, out.String())
	}
}

func TestConfigSetRefusesNonHotApply(t *testing.T) {
	mutationSeen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutationSeen = true
		}
		entry := testHotConfigEntry()
		entry.Key, entry.ApplyMode = "request_read_timeout", "graceful"
		writeTestJSON(w, http.StatusOK, operatorConfigListResponse{Items: []api.OperatorRuntimeConfig{entry}})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	_, stderr, restore := captureOperatorIO()
	defer restore()

	code := cmdConfigDispatch([]string{"set", "--key", "request_read_timeout", "--value", "60s", "--reason", "tune timeout", "--yes"})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if mutationSeen || !strings.Contains(stderr.String(), "cannot trigger a rollout") {
		t.Fatalf("mutationSeen=%t stderr=%q", mutationSeen, stderr.String())
	}
}

func TestConfigMutationRequiresReasonAndConfirmation(t *testing.T) {
	_, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdConfigDispatch([]string{"set", "--key", "hsts_enabled", "--value", "false", "--yes"}); code != 2 {
		t.Fatalf("missing reason exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "--reason must be") {
		t.Fatalf("missing reason stderr = %q", stderr.String())
	}
	stderr.Reset()
	if code := cmdConfigDispatch([]string{"rollback", "--key", "hsts_enabled", "--version", "1", "--reason", "incident resolved"}); code != 2 {
		t.Fatalf("missing confirmation exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "--yes required") {
		t.Fatalf("missing confirmation stderr = %q", stderr.String())
	}
}

func TestNormalizeOperatorConfigValue(t *testing.T) {
	if got := string(normalizeOperatorConfigValue(" false ")); got != "false" {
		t.Errorf("boolean = %q", got)
	}
	if got := string(normalizeOperatorConfigValue("60s")); got != `"60s"` {
		t.Errorf("plain string = %q", got)
	}
}

func testHotConfigEntry() api.OperatorRuntimeConfig {
	return api.OperatorRuntimeConfig{
		Key: "hsts_enabled", Label: "Strict transport security", Description: "Emit HSTS", Category: "Security", Kind: "boolean",
		DesiredValue: json.RawMessage("true"), EffectiveValue: json.RawMessage("true"), Source: "operator",
		ApplyMode: "hot", ControllerEnabled: true, Mutable: true, Status: "applied", Version: 3,
	}
}

func assertConfigMutationHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Header.Get("Idempotency-Key") == "" {
		t.Error("missing Idempotency-Key")
	}
	if got := r.Header.Get(operatorTraceIDHeader); got != testConfigTraceID {
		t.Errorf("trace id = %q, want %q", got, testConfigTraceID)
	}
	if cookie, err := r.Cookie("faas_sid"); err != nil || cookie.Value != "opaque-session" {
		t.Errorf("session cookie = %v, %v", cookie, err)
	}
}
