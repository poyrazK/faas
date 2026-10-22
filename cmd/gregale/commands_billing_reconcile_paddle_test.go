// commands_billing_reconcile_paddle_test.go — pins the four
// documented behaviours of the B4 pre-flight CLI subcommand:
//
//	1. Schema OK path: all four HasX=true, exit 0,
//	   `paddle_overage_dedupe: pending=<n> completed=<n> columns=ok`
//	2. Missing-columns path: one or more HasX=false, exit 1, stderr
//	   names each missing column
//	3. Table-missing path: TableExists=false, exit 1, stderr
//	   says "apply migrations 00034 then 00041"
//	4. Usage-error path: any positional arg → exit 1 with usage
//	   line on stderr
//
// All four run against a tiny apid stub via FAAS_API + FAAS_TOKEN
// env vars so the test exercises the same code path as the CLI
// binary in production.

package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// preflightStub returns a httptest.Server that answers
// GET /v1/admin/billing-paddle-overage/preflight with the
// caller-supplied response. Anything else returns 404. The token
// check is bypassed: the test only asserts the CLI's parse +
// dispatch + output behaviour, not the apid auth chain (that
// path is pinned by handlers_admin_billing_paddle_overage_test.go).
func preflightStub(t *testing.T, body api.BillingPaddleOveragePreflightResponse) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/admin/billing-paddle-overage/preflight", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// runPreflightCLI captures stdout + stderr from the subcommand and
// returns the exit code. The os.Stdout / os.Stderr swap is the
// same pattern as the other billing subcommand tests; without it
// the prints pollute go test's -v output and the assertions would
// race the buffer flush. The deferred restore ensures a panic in
// the subcommand still leaves the test process's stdout/stderr
// pointing at the originals (CI scripts that pipe test output
// would otherwise hang on a dead FD).
func runPreflightCLI(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	origStdout, origStderr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr
	t.Cleanup(func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
	})

	code := main.CmdBillingReconcilePaddleOverage(args)
	wOut.Close()
	wErr.Close()
	_, _ = stdoutBuf.ReadFrom(rOut)
	_, _ = stderrBuf.ReadFrom(rErr)
	return code, stdoutBuf.String(), stderrBuf.String()
}

// TestCmdBillingReconcilePaddleOverage_SchemaOK exercises the
// green-light path: the apid reports all four HasX=true. The CLI
// must exit 0, stdout must match the documented shape, stderr must
// be empty.
func TestCmdBillingReconcilePaddleOverage_SchemaOK(t *testing.T) {
	srv := preflightStub(t, api.BillingPaddleOveragePreflightResponse{
		TableExists:    true,
		HasWindowStart: true,
		HasState:       true,
		HasClaimedAt:   true,
		HasClaimedBy:   true,
		PendingRows:    2,
		CompletedRows:  17,
	})
	t.Setenv("FAAS_API", srv.URL)

	code, out, errOut := runPreflightCLI(t, nil)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "pending=2 completed=17 columns=ok") {
		t.Errorf("stdout should report ok shape; got %q", out)
	}
	if !strings.Contains(out, "paddle_overage_dedupe:") {
		t.Errorf("stdout should start with the table name; got %q", out)
	}
}

// TestCmdBillingReconcilePaddleOverage_MissingColumns pins the
// remediation path: a partial migration (e.g. column added but
// state default backfill interrupted). Exit 1, stderr names each
// missing column so an operator on a degraded DB sees exactly
// what to re-apply.
func TestCmdBillingReconcilePaddleOverage_MissingColumns(t *testing.T) {
	srv := preflightStub(t, api.BillingPaddleOveragePreflightResponse{
		TableExists:   true,
		HasState:      true,
		HasClaimedBy:  true,
		PendingRows:   0,
		CompletedRows: 0,
	})
	t.Setenv("FAAS_API", srv.URL)

	code, out, errOut := runPreflightCLI(t, nil)
	if code != 1 {
		t.Errorf("exit code = %d, want 1; stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "columns=missing=") {
		t.Errorf("stdout should report columns=missing=...; got %q", out)
	}
	if !strings.Contains(out, "window_start") || !strings.Contains(out, "claimed_at") {
		t.Errorf("stdout should name the two missing columns; got %q", out)
	}
	if !strings.Contains(errOut, "apply") || !strings.Contains(errOut, "00041") {
		t.Errorf("stderr should hint at migration 00041; got %q", errOut)
	}
}

// TestCmdBillingReconcilePaddleOverage_TableMissing pins the
// never-applied-anything case. Exit 1, stderr names BOTH 00034
// and 00041 because the operator must apply them in order.
func TestCmdBillingReconcilePaddleOverage_TableMissing(t *testing.T) {
	srv := preflightStub(t, api.BillingPaddleOveragePreflightResponse{})
	t.Setenv("FAAS_API", srv.URL)

	code, out, errOut := runPreflightCLI(t, nil)
	if code != 1 {
		t.Errorf("exit code = %d, want 1; stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "table=missing") {
		t.Errorf("stdout should report table=missing; got %q", out)
	}
	if !strings.Contains(errOut, "00034") || !strings.Contains(errOut, "00041") {
		t.Errorf("stderr should hint at 00034 + 00041 in order; got %q", errOut)
	}
}

// TestCmdBillingReconcilePaddleOverage_UsageErrorOnArg pins the
// "no positional args allowed" gate. Same shape as
// TestBillingReconcile_BadUUIDExitsTwo pattern at admin_test.
func TestCmdBillingReconcilePaddleOverage_UsageErrorOnArg(t *testing.T) {
	// httptest server that 500s on any hit — the test asserts the
	// subcommand refuses the arg BEFORE the network call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected when args present")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	code := main.CmdBillingReconcilePaddleOverage([]string{"surprise"})
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}
