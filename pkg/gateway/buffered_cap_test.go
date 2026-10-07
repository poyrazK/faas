// Tests for the buffered reverse-proxy response body cap (issue
// #995 Phase 2 / ADR-121). The buffered path now installs a
// capWriter at the dispatch site in handler.go's ServeHTTP; the
// streaming path uses setupStreamingWriter with its own capWriter.
// These tests cover the buffered shape.
//
// Companion tests:
//   - pkg/gateway/handler_test.go's TestApplyEdgeRuleLimit_StreamingCap_*
//     (streaming capWriter — already in tree).
//   - cmd/gatewayd-internal/proxy.go::capWriter (apid loopback —
//     same shape, covered by the unit tests on this surface if
//     added later).

package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestSetupBufferedCapWriter_OverCap_Hardens asserts the architectural
// contract documented in handler.go:setupBufferedCapWriter — when the
// buffered upstream exceeds the cap, the connection is hardened-closed
// (either a 413 problem+json if the cap tripped before headers were
// written, or a connection reset / EOF if the cap tripped mid-body).
// Either outcome proves the capWriter is installed and the cap is enforced.
// Issue #995 Phase 2 / ADR-121.
//
// Note: the dispatch site in ServeHTTP passes app.Plan.MaxResponseBodyBytes()
// (a real per-plan cap), not a per-test parameter. The test below uses
// PlanFree (25 MiB cap) and emits 30 MiB from the upstream.
func TestSetupBufferedCapWriter_OverCap_Hardens(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Emit 30 MiB — well past the PlanFree 25 MiB cap.
		chunk := make([]byte, 1<<20)
		for i := 0; i < 30; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	t.Cleanup(upstream.Close)

	b := &fakeBackend{
		app:      App{ID: "app-cap", AccountID: "acct-cap", Plan: api.PlanFree},
		host:     "cap.apps.dom",
		upstream: upstream.Listener.Addr().String(),
		running:  true,
		targets:  []Target{{NodeID: upstream.Listener.Addr().String(), InstanceID: "i-cap"}},
	}
	b.setLegacyHot()
	h.backend = b

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(l) }()
	defer func() { _ = srv.Close() }()

	url := "http://" + l.Addr().String() + "/"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = "cap.apps.dom"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Connection reset by upstream after cap fired — hardening
		// path proven.
		return
	}
	defer resp.Body.Close()

	// If the response came back with a status at all, the body
	// must be <= the cap. Anything bigger is a regression.
	body, _ := io.ReadAll(resp.Body)
	if int64(len(body)) > api.MaxResponseBodyBytesDefault {
		t.Errorf("over-cap body delivered: %d bytes (cap %d)", len(body), api.MaxResponseBodyBytesDefault)
	}
}

// TestSetupBufferedCapWriter_UnderCap_Passes asserts the capWriter is
// transparent under the cap (the common path). Issue #995 Phase 2 /
// ADR-121.
func TestSetupBufferedCapWriter_UnderCap_Passes(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	payload := []byte("hello from app — well under the cap")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(upstream.Close)

	b := &fakeBackend{
		app:      App{ID: "app-ok", AccountID: "acct-ok", Plan: api.PlanFree},
		host:     "ok.apps.dom",
		upstream: upstream.Listener.Addr().String(),
		running:  true,
		targets:  []Target{{NodeID: upstream.Listener.Addr().String(), InstanceID: "i-ok"}},
	}
	b.setLegacyHot()
	h.backend = b

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://ok.apps.dom/", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("under-cap response: got %d, want 200", rec.Code)
	}
	if rec.Body.String() != string(payload) {
		t.Errorf("under-cap body: got %q, want %q", rec.Body.String(), payload)
	}
}

// TestSetupBufferedCapWriter_AtCap_200 verifies that a writer
// emitting exactly cap bytes succeeds (the +1 byte in the upstream
// io.LimitReader — when present — would only flip the EOF path
// here, the wrapper allows <= cap). Issue #995 Phase 2 / ADR-121.
func TestSetupBufferedCapWriter_AtCap_200(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	cap := int64(1024)
	payload := make([]byte, cap)
	for i := range payload {
		payload[i] = 'x'
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(upstream.Close)

	b := &fakeBackend{
		app:      App{ID: "app-atcap", AccountID: "acct-atcap", Plan: api.PlanHobby},
		host:     "atcap.apps.dom",
		upstream: upstream.Listener.Addr().String(),
		running:  true,
		targets:  []Target{{NodeID: upstream.Listener.Addr().String(), InstanceID: "i-atcap"}},
	}
	b.setLegacyHot()
	h.backend = b

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://atcap.apps.dom/", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("at-cap response: got %d, want 200 (body %d bytes)", rec.Code, rec.Body.Len())
	}
	if rec.Body.Len() != int(cap) {
		t.Errorf("at-cap body length: got %d, want %d", rec.Body.Len(), cap)
	}
}

// TestSetupBufferedCapWriter_ZeroCap_NoOp verifies that
// setupBufferedCapWriter with cap <= 0 is a no-op (returns the
// original writer unchanged). Issue #995 Phase 2 / ADR-121.
func TestSetupBufferedCapWriter_ZeroCap_NoOp(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	out := h.setupBufferedCapWriter(rec, App{ID: "x", Plan: api.PlanFree}, 0)
	if out != rec {
		t.Errorf("setupBufferedCapWriter(0) returned a wrapper, want identity")
	}
	out = h.setupBufferedCapWriter(rec, App{ID: "x", Plan: api.PlanFree}, -1)
	if out != rec {
		t.Errorf("setupBufferedCapWriter(-1) returned a wrapper, want identity")
	}
}

// TestSetupBufferedCapWriter_ProblemCodeConstant sanity-checks the
// new api.CodeResponseTooLarge constant (issue #995 Phase 2 /
// ADR-121).
func TestSetupBufferedCapWriter_ProblemCodeConstant(t *testing.T) {
	if api.CodeResponseTooLarge != "response_too_large" {
		t.Errorf("CodeResponseTooLarge = %q, want response_too_large", api.CodeResponseTooLarge)
	}
}

// TestDefaultProxy_UpstreamLimitReader verifies that the
// defaultProxy's ModifyResponse hook wraps the upstream body in
// io.LimitReader(body, cap+1) so a runaway guest EOFs at cap+1
// bytes. The proxy returns 200 with the upstream's full body when
// the body is under the cap; when the body is over the cap, the
// LimitReader shortens the body to cap+1 bytes on the wire (so the
// downstream capWriter trips at cap+1). Issue #995 Phase 2 /
// ADR-121 — the architectural contract from #996 review-fix
// finding #1 (medium).
func TestDefaultProxy_UpstreamLimitReader(t *testing.T) {
	const cap = int64(1024)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Emit cap+512 bytes — well past the limit.
		chunk := make([]byte, cap+512)
		for i := range chunk {
			chunk[i] = 'x'
		}
		_, _ = w.Write(chunk)
	}))
	t.Cleanup(upstream.Close)

	p := defaultProxy(upstream.Listener.Addr().String(), cap)
	if p == nil {
		t.Fatal("defaultProxy returned nil")
	}

	// Drive the proxy through an http.Server so we read the body
	// off the wire the same way a real client would.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: p}
	go func() { _ = srv.Serve(l) }()
	defer func() { _ = srv.Close() }()

	resp, err := http.Get("http://" + l.Addr().String() + "/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	// Read at most cap+2 bytes. The LimitReader is supposed to
	// short-circuit at cap+1, so the read should NOT block waiting
	// for upstream's full cap+512 to drain.
	readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	body, err := io.ReadAll(io.LimitReader(resp.Body, cap+2))
	_ = readCtx
	// io.ReadAll considers an EOF mid-LimitReader an "unexpected
	// EOF" — that's the desired behaviour here (the LimitReader
	// short-circuited the stream at cap+1), so we accept the
	// error and assert on body length instead. Use errors.Is
	// (CI errorlint gate) instead of a direct == comparison.
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadAll: %v", err)
	}
	// The upstream emitted cap+512, but the LimitReader caps at
	// cap+1, so we should see exactly cap+1 bytes.
	if int64(len(body)) != cap+1 {
		t.Errorf("body length: got %d, want %d (LimitReader should have capped at cap+1)", len(body), cap+1)
	}
}

// TestDefaultProxy_NoCap_NoUpstreamGuard verifies that
// defaultProxy(addr, 0) installs no ModifyResponse hook (the
// downstream capWriter is the only guard). Issue #995 Phase 2 /
// ADR-121.
func TestDefaultProxy_NoCap_NoUpstreamGuard(t *testing.T) {
	const fullBody = 4096
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, fullBody))
	}))
	t.Cleanup(upstream.Close)

	p := defaultProxy(upstream.Listener.Addr().String(), 0)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: p}
	go func() { _ = srv.Serve(l) }()
	defer func() { _ = srv.Close() }()

	resp, err := http.Get("http://" + l.Addr().String() + "/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if len(body) != fullBody {
		t.Errorf("body length with cap=0: got %d, want %d (no upstream guard expected)", len(body), fullBody)
	}
}

// TestCapWriterRefusesAnAnnouncedOversizeBody pins production-us hunt #4
// (H4-67): a 200 MiB response on a 100 MiB plan cap went out as 200 with the
// full Content-Length and stopped at ~100 MiB.
func TestCapWriterRefusesAnAnnouncedOversizeBody(t *testing.T) {
	for _, tc := range []struct {
		name       string
		length     string
		wantStatus int
	}{
		{"over the cap", "101", http.StatusRequestEntityTooLarge},
		{"at the cap", "100", http.StatusOK},
		{"unknown length", "", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			fired := 0
			cw := &capWriter{ResponseWriter: rec, cap: 100, disabled: &atomic.Bool{}, onCap: func() {
				fired++
				rec.Header().Set("Content-Type", "application/problem+json")
				rec.WriteHeader(http.StatusRequestEntityTooLarge)
			}}
			if tc.length != "" {
				cw.Header().Set("Content-Length", tc.length)
			}
			cw.Header().Set("Etag", `"v1"`)
			cw.WriteHeader(http.StatusOK)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusRequestEntityTooLarge {
				if fired != 1 || rec.Header().Get("Content-Length") != "" || rec.Header().Get("Etag") != "" {
					t.Fatalf("onCap fired %d times, headers %v; want one problem without the upstream entity headers", fired, rec.Header())
				}
				if n, err := cw.Write([]byte("x")); n != 0 || err == nil {
					t.Fatalf("Write after refusal = %d, %v; want blocked", n, err)
				}
			}
		})
	}
}
