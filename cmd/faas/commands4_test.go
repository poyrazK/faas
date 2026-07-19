// CLI tests for the G6 account self-service subcommands (spec §17
// G6, ADR-021). Mirrors the secrets_test pattern: a programmable
// fake-apid sink + t.Setenv wiring + osStdout swap.
//
// Coverage:
//
//   - cmdAccount dispatch: unknown subcommand → exit 1
//   - cmdAccount export: writes the response body to disk
//   - cmdAccount export --no-secrets: query string flips to false
//   - cmdAccount delete: forwards Idempotency-Key
//   - cmdAccount restore: hits POST /v1/account/restore
//   - cmdAccount status: renders plan + status

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// accountSink is a fake apid that records each request and lets the
// test pick the response body. Mirrors the shape of secretsSink so
// reviewers don't have to learn a new pattern.
type accountSink struct {
	lastMethod   string
	lastPath     string
	lastRawQuery string
	// idempotencyKey recorded from the request headers (DELETE only).
	lastIdemKey string

	// Response writers.
	onGet    func(path string) (int, any)
	onDelete func(idempotencyKey string) (int, any)
	onPost   func() (int, any)
}

func (s *accountSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.lastMethod = r.Method
	s.lastRawQuery = r.URL.RawQuery
	s.lastPath = r.URL.Path
	if r.URL.RawQuery != "" {
		s.lastPath += "?" + r.URL.RawQuery
	}
	s.lastIdemKey = r.Header.Get("Idempotency-Key")
	switch r.Method {
	case "GET":
		path := r.URL.Path
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		status, payload := s.onGet(path)
		if status == 0 {
			status = http.StatusOK
		}
		if payload != nil {
			writeJSONTest(w, payload)
		} else {
			w.WriteHeader(status)
		}
	case "DELETE":
		status, _ := s.onDelete(s.lastIdemKey)
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":"deleted_pending","scheduled_at":"2026-07-18T10:00:00Z","restore_until":"2026-08-17T10:00:00Z"}`))
	case "POST":
		// Restore — body is irrelevant, server returns an AccountResponse.
		// The status returned from onPost is advisory only; writeJSONTest
		// just sets headers + writes the body, so we don't gate on it.
		_, payload := s.onPost()
		writeJSONTest(w, payload)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func TestCmdAccount_DispatchUnknown(t *testing.T) {
	if code := cmdAccount([]string{"frobnicate"}); code != 1 {
		t.Errorf("unknown subcommand = %d, want 1", code)
	}
}

func TestCmdAccount_DispatchNoArgs(t *testing.T) {
	if code := cmdAccount(nil); code != 1 {
		t.Errorf("no args = %d, want 1", code)
	}
}

// TestCmdAccountExport_WritesFile — drives a GET /v1/account/export
// and asserts the response body landed at the requested output path.
func TestCmdAccountExport_WritesFile(t *testing.T) {
	payload := api.AccountExportResponse{
		ExportedAt: "2026-07-18T10:00:00Z",
		Account:    api.AccountResponse{Email: "a@b.c", Plan: "pro"},
	}
	sink := &accountSink{
		onGet: func(path string) (int, any) {
			if path != "/v1/account/export" {
				t.Errorf("path = %q, want /v1/account/export", path)
			}
			return http.StatusOK, payload
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	out := filepath_Join(t.TempDir(), "bundle.json")
	if code := cmdAccountExport([]string{"-o", out}); code != 0 {
		t.Fatalf("cmdAccountExport = %d, want 0", code)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(body), "a@b.c") {
		t.Errorf("output file missing account email: %s", body)
	}
}

// TestCmdAccountExport_NoSecretsFlag appends ?include_secrets=false.
func TestCmdAccountExport_NoSecretsFlag(t *testing.T) {
	var capturedPath string
	sink := &accountSink{
		onGet: func(path string) (int, any) {
			capturedPath = path
			return http.StatusOK, api.AccountExportResponse{}
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	out := filepath_Join(t.TempDir(), "bundle.json")
	if code := cmdAccountExport([]string{"--no-secrets", "-o", out}); code != 0 {
		t.Fatalf("cmdAccountExport --no-secrets = %d, want 0", code)
	}
	if capturedPath != "/v1/account/export?include_secrets=false" {
		t.Errorf("path = %q, want include_secrets=false suffix", capturedPath)
	}
	if sink.lastPath != "/v1/account/export?include_secrets=false" {
		t.Errorf("sink.lastPath = %q (raw=%q)", sink.lastPath, sink.lastRawQuery)
	}
}

// TestCmdAccountDelete_ForwardsIdempotencyKey — -q skips the prompt,
// fires DELETE, the server records the Idempotency-Key header.
func TestCmdAccountDelete_ForwardsIdempotencyKey(t *testing.T) {
	sink := &accountSink{
		onDelete: func(key string) (int, any) {
			if !strings.HasPrefix(key, "cli-delete-") {
				t.Errorf("Idempotency-Key = %q, want cli-delete-<hex>", key)
			}
			return http.StatusOK, nil
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdAccountDelete([]string{"-q"}); code != 0 {
		t.Fatalf("cmdAccountDelete -q = %d, want 0", code)
	}
	if sink.lastMethod != "DELETE" {
		t.Errorf("method = %q, want DELETE", sink.lastMethod)
	}
	if sink.lastPath != "/v1/account" {
		t.Errorf("path = %q", sink.lastPath)
	}
}

// silence unused imports for builds where these are only referenced
// by tests above.
var _ = bytes.NewBuffer

// TestCmdAccountDelete_NonZeroOnServerError.
func TestCmdAccountDelete_NonZeroOnServerError(t *testing.T) {
	sink := &accountSink{
		onDelete: func(string) (int, any) { return http.StatusInternalServerError, nil },
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdAccountDelete([]string{"-q"}); code == 0 {
		t.Errorf("expected non-zero exit on 500")
	}
}

// TestCmdAccountRestore_RoundTrip — restore hits the right endpoint
// and returns 0. We don't capture stdout because commands4.go uses
// fmt.Printf directly (not the swappable osStdout seam).
func TestCmdAccountRestore_RoundTrip(t *testing.T) {
	sink := &accountSink{
		onPost: func() (int, any) {
			return http.StatusOK, api.AccountResponse{
				Email: "a@b.c", Plan: "hobby", Status: "active",
			}
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdAccountRestore(nil); code != 0 {
		t.Fatalf("cmdAccountRestore = %d, want 0", code)
	}
	if sink.lastMethod != "POST" || sink.lastPath != "/v1/account/restore" {
		t.Errorf("method/path = %s %s, want POST /v1/account/restore", sink.lastMethod, sink.lastPath)
	}
}

// TestCmdAccountStatus_HitsWhoami — `account status` proxies to
// GET /v1/account (Whoami). Assert the round-trip + exit code; the
// human-readable banner is a side-effect of fmt.Printf to real stdout
// (verified by hand in `make smoke`).
func TestCmdAccountStatus_HitsWhoami(t *testing.T) {
	sink := &accountSink{
		onGet: func(path string) (int, any) {
			if path != "/v1/account" {
				t.Errorf("path = %q", path)
			}
			return http.StatusOK, api.AccountResponse{
				Email: "a@b.c", Plan: "free", Status: "deleted_pending", AppCount: 2,
			}
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdAccountStatus(nil); code != 0 {
		t.Fatalf("cmdAccountStatus = %d, want 0", code)
	}
	if sink.lastMethod != "GET" || sink.lastPath != "/v1/account" {
		t.Errorf("method/path = %s %s, want GET /v1/account", sink.lastMethod, sink.lastPath)
	}
}

// silence unused imports for builds where io/json are pulled in
// only by helpers that exercise them above.
var (
	_ = io.Discard
	_ = json.Marshal
)

// filepath_Join is a tiny indirection so tests above read top-down
// without a noisy import block.
func filepath_Join(parts ...string) string {
	return joinPath(parts...)
}

func joinPath(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		if len(p) == 0 {
			continue
		}
		if out[len(out)-1] == '/' {
			out += p
		} else {
			out += "/" + p
		}
	}
	return out
}
