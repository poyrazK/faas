// commands_invocation_test.go — `gregale invoke <slug>` exit-code
// contract.
//
// `invoke` is documented as the day-1 functional smoke test, which
// makes its exit code the whole product: a CI job runs it and branches
// on $?. It previously returned 0 for every 200 response, including
// ones whose body carried status=failed, so a parked or broken app
// reported success. These tests pin the terminal-state mapping.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdInvoke_ExitCodeByTerminalState(t *testing.T) {
	// Vocabulary is the invocations_state_check CHECK in
	// migrations/00064_invocations_dead_letter.sql.
	cases := []struct {
		status   string
		wantCode int
	}{
		{"completed", 0},
		{"failed", 1},
		{"cancelled", 1},
		{"dead_letter", 1},
		// Non-terminal states: a sync invoke that returns without
		// reaching a terminal state has not shown the app works.
		{"pending", 1},
		{"dispatching", 1},
	}
	for _, c := range cases {
		t.Run(c.status, func(t *testing.T) {
			resetJSONEnv(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(api.InvokeResponse{
					ID:     "5a0d1c2e-0000-4000-8000-000000000001",
					Status: c.status,
				})
			}))
			defer srv.Close()

			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")

			if got := cmdInvoke([]string{"some-app"}); got != c.wantCode {
				t.Errorf("cmdInvoke(status=%s) = %d, want %d", c.status, got, c.wantCode)
			}
		})
	}
}

func TestCmdInvoke_JSONFailedExitsOne(t *testing.T) {
	resetJSONEnv(t)
	jsonOutput = true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.InvokeResponse{
			ID:     "5a0d1c2e-0000-4000-8000-000000000003",
			Status: "failed",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if got := cmdInvoke([]string{"some-app"}); got != 1 {
		t.Errorf("cmdInvoke(JSON failed) = %d, want 1", got)
	}
}

// The async path only queues the work, so a successful enqueue is a
// successful command regardless of how the invocation later resolves.
func TestCmdInvoke_Async_ExitsZeroOnQueued(t *testing.T) {
	resetJSONEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{
			ID:        "5a0d1c2e-0000-4000-8000-000000000002",
			StatusURL: "https://api.example.com/v1/invocations/5a0d1c2e",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if got := cmdInvoke([]string{"--async", "some-app"}); got != 0 {
		t.Errorf("cmdInvoke(--async) = %d, want 0", got)
	}
}

func TestInvokeStatusOK(t *testing.T) {
	if !invokeStatusOK("completed") {
		t.Errorf("invokeStatusOK(completed) = false, want true")
	}
	for _, s := range []string{"failed", "cancelled", "dead_letter", "pending", "dispatching", ""} {
		if invokeStatusOK(s) {
			t.Errorf("invokeStatusOK(%q) = true, want false", s)
		}
	}
}
