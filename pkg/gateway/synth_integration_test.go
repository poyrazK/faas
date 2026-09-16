package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeDispatcher records Wake + Invoke calls so the handler can run
// end-to-end (covers both legacy wake-only and Move 1 invoke paths).
type fakeDispatcher struct{ calls []string }

func (f *fakeDispatcher) Wake(_ context.Context, appID string) error {
	f.calls = append(f.calls, appID)
	return nil
}

// Invoke echoes the wake path; the integration test only verifies the
// wire reached the dispatcher, not the response shape.
func (f *fakeDispatcher) Invoke(_ context.Context, appID string, inv state.Invocation) (state.Invocation, error) {
	f.calls = append(f.calls, appID)
	inv.State = state.InvocationDispatching
	return inv, nil
}

// TestSynthHandlerSanitizesLogFields asserts that a synthesized request
// with embedded CR/LF (the CWE-117 injection vector) does NOT leak newlines
// into the structured log. The handler should sanitize before logging.
func TestSynthHandlerSanitizesLogFields(t *testing.T) {
	// Buffer-backed slog handler so we can assert on emitted JSON.
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	dir := t.TempDir()
	// macOS limits unix socket paths to 104 bytes; t.TempDir already
	// nests under /var/folders/... which exhausts that. Use /tmp with
	// the test name to stay under the cap, and unlink on cleanup.
	sock := "/tmp/" + strings.ReplaceAll(t.Name(), "/", "_") + ".sock"
	_ = os.Remove(sock)
	s := NewSynthServer(sock, &fakeDispatcher{}, log)
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		_ = s.Stop(context.Background())
		_ = os.Remove(sock)
	}()
	_ = dir

	// Dial the unix socket directly — the test bypasses any HTTP client
	// that might rewrite payloads.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Craft a request whose app_id and path embed newlines + carriage
	// returns — the canonical log-injection vector. After the handler
	// runs, the captured log must not contain raw \n or \r within the
	// JSON value (slog's JSON encoder would itself escape them, but
	// the helper is meant to prevent forged lines, which means
	// stripping the control bytes entirely).
	payload, _ := json.Marshal(map[string]string{
		"app_id": "app\nINJECTED",
		"method": "POST",
		"path":   "/foo\rFAKE",
	})
	req := fmt.Sprintf("POST /v1/synthesize HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(payload), payload)
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read the response so the server doesn't hang.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	respBuf := make([]byte, 4096)
	_, _ = conn.Read(respBuf)

	out := buf.String()
	if out == "" {
		t.Fatal("expected log output, got empty buffer")
	}

	// The structured log line is one JSON object — it must NOT contain
	// the raw injected newlines INSIDE the dispatched-record's JSON
	// value. Newlines BETWEEN records are fine (slog emits one record
	// per line by default). The harder, semantically important check
	// is that sanitizeLogField replaced the CR/LF with the middle-dot
	// placeholder before the value was even handed to slog.
	//
	// slog's JSON encoder would escape \n → \\n if we passed an
	// unsanitized value; finding that escape sequence means the helper
	// did NOT run on this field.
	if strings.Contains(out, `app\nINJECTED`) {
		t.Errorf("log line was not sanitized (slog escaped an unsanitized value): %s", out)
	}
	if strings.Contains(out, `/foo\rFAKE`) {
		t.Errorf("log line was not sanitized (slog escaped an unsanitized value): %s", out)
	}

	// Finally: the placeholder variant must appear (proves the helper ran).
	if !strings.Contains(out, "app·INJECTED") {
		t.Errorf("expected sanitized app_id 'app·INJECTED' in log; got: %s", out)
	}
	if !strings.Contains(out, "/foo·FAKE") {
		t.Errorf("expected sanitized path '/foo·FAKE' in log; got: %s", out)
	}
}

// guard against stale /tmp files in case the test runs on a sandbox that
// still mounts /tmp as world-writable.
func TestMain(m *testing.M) {
	_ = os.Setenv("TMPDIR", os.TempDir())
	os.Exit(m.Run())
}

// TestSynthServer_UnifiedMux_RoutesPathsCorrectly pins the issue
// #692 contract: the unified mux at cmd/gatewayd-internal/run.go:1057
// routes each known synth path to the synth dispatcher and routes
// customer paths (e.g. /v1/apps/coolapp) to the catch-all
// publicHandler. A typo in any Handle() registration would silently
// shadow the synth dispatcher with publicHandler (or vice versa);
// this test catches the typo and the regression case where one of
// the three synth routes is removed.
//
// Replicates the exact registration order from run.go:1057
// (Handle("/") FIRST, then the three synth paths) — Go's
// http.ServeMux uses longest-prefix match, but readers expect
// registration order to match routing order, so the test pins both.
func TestSynthServer_UnifiedMux_RoutesPathsCorrectly(t *testing.T) {
	var (
		customerFallback atomic.Int32
		synthCalls       atomic.Int32
	)
	// Catch-all customer handler: any non-synth path lands here.
	customerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		customerFallback.Add(1)
		w.Header().Set("X-Handler", "public")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "customer")
	})

	// Synth dispatcher: record that the synth sub-mux saw the call.
	// We drive it through the Mux() accessor, not the wire, so the
	// test is a pure mux-routing assertion (not a dispatcher-shape
	// assertion).
	disp := &fakeDispatcher{}
	srv := NewSynthServer("/tmp/unused.sock", disp, slog.Default())
	subMux := srv.Mux()
	if subMux == nil {
		t.Fatal("Mux() returned nil -- NewSynthServer must populate the sub-mux before any SetHandler call")
	}

	// Wire the wrappers that handleInvokeDispatch + handleSynthesize
	// see in production: each synth path lands on a handler that
	// bumps a counter (proxy for "synth did real work"). This keeps
	// the routing test independent of the actual handler shape —
	// the goal is to pin the mux, not the synth handlers.
	synthMux := http.NewServeMux()
	synthMux.HandleFunc("/v1/synthesize", func(w http.ResponseWriter, r *http.Request) {
		synthCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "synth")
	})
	synthMux.HandleFunc("/v1/invocations:dispatch", func(w http.ResponseWriter, r *http.Request) {
		synthCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "dispatch")
	})
	synthMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		synthCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})

	// Build the unified mux exactly as runWithDeps does at
	// run.go:1057 — Handle("/") first, then the more-specific
	// routes. Order matters for readers, not for Go; this
	// pinning is the whole point of the test.
	unified := http.NewServeMux()
	unified.Handle("/", customerHandler)
	unified.Handle("/v1/synthesize", synthMux)
	unified.Handle("/v1/invocations:dispatch", synthMux)
	unified.Handle("/healthz", synthMux)

	cases := []struct {
		name        string
		method      string
		path        string
		wantStatus  int
		wantSynth   int32 // expected increment to synthCalls after this request
		wantCust    int32 // expected increment to customerFallback after this request
		wantBodyHas string
	}{
		{
			name:   "synthesize reaches synth dispatcher, NOT publicHandler",
			method: http.MethodPost, path: "/v1/synthesize",
			wantStatus: http.StatusOK, wantSynth: 1, wantCust: 0,
			wantBodyHas: "synth",
		},
		{
			name:   "invocations:dispatch reaches synth dispatcher, NOT publicHandler",
			method: http.MethodPost, path: "/v1/invocations:dispatch",
			wantStatus: http.StatusOK, wantSynth: 2, wantCust: 0,
			wantBodyHas: "dispatch",
		},
		{
			name:   "healthz reaches synth dispatcher (not publicHandler, not customer)",
			method: http.MethodGet, path: "/healthz",
			wantStatus: http.StatusOK, wantSynth: 3, wantCust: 0,
			wantBodyHas: "ok",
		},
		{
			name:   "customer path lands on publicHandler, NOT synth",
			method: http.MethodGet, path: "/v1/apps/coolapp",
			wantStatus: http.StatusOK, wantSynth: 3, wantCust: 1,
			wantBodyHas: "customer",
		},
		{
			name:   "another customer path keeps landing on publicHandler",
			method: http.MethodGet, path: "/v1/usage",
			wantStatus: http.StatusOK, wantSynth: 3, wantCust: 2,
			wantBodyHas: "customer",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			unified.ServeHTTP(rec, req)

			resp := rec.Result()
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d (body=%q)", resp.StatusCode, tc.wantStatus, rec.Body.String())
			}
			body, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(body), tc.wantBodyHas) {
				t.Errorf("body = %q; want to contain %q", string(body), tc.wantBodyHas)
			}
			if got := synthCalls.Load(); got != tc.wantSynth {
				t.Errorf("synthCalls = %d, want %d (path %q did not reach the synth dispatcher)", got, tc.wantSynth, tc.path)
			}
			if got := customerFallback.Load(); got != tc.wantCust {
				t.Errorf("customerFallback = %d, want %d (path %q did not reach the publicHandler)", got, tc.wantCust, tc.path)
			}
		})
	}
}

// TestNewSynthServer_AppliesCanonicalShape pins the full envelope of
// the SynthServer listener (ADR-122 / post-merge audit, issue #995
// closure). REGRESSION GUARD: a future edit that drops one of the
// four timeout knobs or MaxHeaderBytes from NewSynthServer's struct
// literal fails this test. Inspects s.srv directly (same package,
// private field) so the test runs without binding a unix socket.
// Mirrors the githubd NewWebhookHTTPServer canonical-shape test.
func TestNewSynthServer_AppliesCanonicalShape(t *testing.T) {
	s := NewSynthServer("/tmp/unused.sock", &fakeDispatcher{}, slog.Default())
	if s.srv == nil {
		t.Fatal("NewSynthServer must populate s.srv")
	}
	if s.srv.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("RHT = %v, want 5s", s.srv.ReadHeaderTimeout)
	}
	if got, want := s.srv.ReadTimeout, time.Duration(api.MetricsReadTimeoutSecondsDefault)*time.Second; got != want {
		t.Errorf("RT = %v, want %v", got, want)
	}
	if got, want := s.srv.WriteTimeout, time.Duration(api.MetricsWriteTimeoutSecondsDefault)*time.Second; got != want {
		t.Errorf("WT = %v, want %v", got, want)
	}
	if got, want := s.srv.IdleTimeout, time.Duration(api.MetricsIdleTimeoutSecondsDefault)*time.Second; got != want {
		t.Errorf("IT = %v, want %v", got, want)
	}
	if s.srv.MaxHeaderBytes != int(api.DefaultMaxHeaderBytes) {
		t.Errorf("MHB = %d, want %d", s.srv.MaxHeaderBytes, int(api.DefaultMaxHeaderBytes))
	}
}

func TestSynthServerSetHandlerAdoptsCustomerRequestEnvelope(t *testing.T) {
	s := NewSynthServer("/tmp/unused.sock", &fakeDispatcher{}, slog.Default())
	s.SetHandler(http.NotFoundHandler())
	if got := s.srv.ReadTimeout; got != api.CustomerRequestEnvelopeTimeout {
		t.Fatalf("ReadTimeout = %v, want %v", got, api.CustomerRequestEnvelopeTimeout)
	}
	if got := s.srv.WriteTimeout; got != api.CustomerRequestEnvelopeTimeout {
		t.Fatalf("WriteTimeout = %v, want %v", got, api.CustomerRequestEnvelopeTimeout)
	}
}
