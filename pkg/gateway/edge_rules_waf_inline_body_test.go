package gateway

// adr: 831 amendment 22 — under edge_waf_strict_inspection and
// edge_waf_inline_body, warn and block rules score the first
// api.EdgeWAFMaxInlineBodyBytes of the body in-path, read once and put back,
// so the app receives the body unchanged. The body is read only when upload
// admission would read it to the end anyway, under admission's deadlines;
// compressed bodies are left to the sampled check.

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// deadlineRecorder is a ResponseWriter whose connection takes read
// deadlines, as the hop server's does (http.ResponseController).
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (d *deadlineRecorder) SetReadDeadline(t time.Time) error {
	d.deadlines = append(d.deadlines, t)
	return nil
}

func newDeadlineRecorder() *deadlineRecorder {
	return &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func inlineBodyHandler(inline *fakeWAFInline, mode string) *Handler {
	rule := &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", ParanoiaLevel: 1, AnomalyThreshold: 5, Mode: mode}
	return &Handler{edgeRules: wafRuleMatcher{rule: rule}, wafInspector: &recordingWAFInspector{}, wafInline: inline}
}

func TestApplyEdgeRuleWAFInlineBody(t *testing.T) {
	long := strings.Repeat("b", 3*api.EdgeWAFMaxInlineBodyBytes)
	for _, tc := range []struct {
		name          string
		body          string
		header        map[string]string
		chunked       bool
		app           App
		flags         *platformflags.Set
		wantPrefix    string
		wantTruncated bool
	}{
		{name: "short body is scored whole", body: `{"q":"1' OR 1=1--"}`, wantPrefix: `{"q":"1' OR 1=1--"}`},
		{name: "long body is scored up to the bound", body: long, wantPrefix: long[:api.EdgeWAFMaxInlineBodyBytes], wantTruncated: true},
		{name: "body of exactly the bound", body: long[:api.EdgeWAFMaxInlineBodyBytes], wantPrefix: long[:api.EdgeWAFMaxInlineBodyBytes]},
		{name: "chunked JSON is scored", body: `{"a":1}`, chunked: true, header: map[string]string{"Content-Type": "application/json"}, wantPrefix: `{"a":1}`},
		{name: "long chunked body is scored up to the bound", body: long, chunked: true, wantPrefix: long[:api.EdgeWAFMaxInlineBodyBytes], wantTruncated: true},
		// Admission reads these to the end before the app wakes, so the
		// gate may too.
		{name: "chunked octet-stream is scored", body: "x=1", chunked: true, header: map[string]string{"Content-Type": "application/octet-stream"}, wantPrefix: "x=1"},
		{name: "gRPC to an HTTP app is scored", body: "\x00\x00\x00\x00\x02hi", header: map[string]string{"Content-Type": "application/grpc"}, wantPrefix: "\x00\x00\x00\x00\x02hi"},
		{name: "compressed body is not scored", body: "\x1f\x8b\x08rest", header: map[string]string{"Content-Encoding": "gzip"}},
		{name: "native gRPC is not read", body: "\x00\x00\x00\x00\x02hi", chunked: true, header: map[string]string{"Content-Type": "application/grpc+proto"},
			app: App{AppProtocol: api.AppProtocolGRPC}},
		{name: "baseline set gets no body", body: `{"q":"1' OR 1=1--"}`,
			flags: edgeFeatureSet("acct-1", platformflags.EdgeWAFBlocking, platformflags.EdgeWAFInlineBody)},
		{name: "strict without the body flag gets no body", body: `{"q":"1' OR 1=1--"}`,
			flags: edgeFeatureSet("acct-1", platformflags.EdgeWAFBlocking, platformflags.EdgeWAFStrictInspection)},
		{name: "strict with the body flag", body: `{"q":"1' OR 1=1--"}`, wantPrefix: `{"q":"1' OR 1=1--"}`,
			flags: edgeFeatureSet("acct-1", platformflags.EdgeWAFBlocking, platformflags.EdgeWAFStrictInspection, platformflags.EdgeWAFInlineBody)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inline := &fakeWAFInline{result: WAFInlineResult{Outcome: WAFInlineClean}}
			h := inlineBodyHandler(inline, api.EdgeWAFModeBlock)
			if tc.flags != nil {
				h.WithEdgeFeatureFlags(tc.flags)
			}
			r := httptest.NewRequest(http.MethodPost, "http://app.example.test/api/items", strings.NewReader(tc.body))
			if tc.chunked {
				r.ContentLength = -1
			}
			for k, v := range tc.header {
				r.Header.Set(k, v)
			}
			app := tc.app
			app.ID, app.AccountID = "app-1", "acct-1"
			w := newDeadlineRecorder()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
			if handled, _ := h.applyEdgeRuleWAF(w, r, app, rec); handled {
				t.Fatal("a clean check refused the request")
			}
			if inline.calls != 1 {
				t.Fatalf("in-path checks = %d, want 1", inline.calls)
			}
			if string(inline.last.Body) != tc.wantPrefix || inline.last.BodyTruncated != tc.wantTruncated {
				t.Errorf("scored body = %d bytes (truncated %v), want %d bytes (truncated %v)",
					len(inline.last.Body), inline.last.BodyTruncated, len(tc.wantPrefix), tc.wantTruncated)
			}
			if tc.wantPrefix != "" {
				// Read under a deadline, cleared afterwards for admission.
				if n := len(w.deadlines); n < 2 || w.deadlines[0].IsZero() || !w.deadlines[n-1].IsZero() {
					t.Errorf("read deadlines = %v, want set then cleared", w.deadlines)
				}
			}
			// The app receives the whole body, unchanged.
			got, err := io.ReadAll(r.Body)
			if err != nil || string(got) != tc.body {
				t.Errorf("app received %d bytes (%v), want the %d-byte body unchanged", len(got), err, len(tc.body))
			}
		})
	}
}

func TestApplyEdgeRuleWAFInlineBodyBlocks(t *testing.T) {
	inline := &fakeWAFInline{result: WAFInlineResult{Outcome: WAFInlineDetected, RuleIDs: []int{942100}, Categories: []string{"sqli"}}}
	h := inlineBodyHandler(inline, api.EdgeWAFModeBlock)
	r := httptest.NewRequest(http.MethodPost, "http://app.example.test/api/items", strings.NewReader(`{"q":"1' OR 1=1--"}`))
	w := newDeadlineRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
	if handled, _ := h.applyEdgeRuleWAF(w, r, App{ID: "app-1", AccountID: "acct-1"}, rec); !handled || w.Code != http.StatusForbidden {
		t.Fatalf("handled=%v status=%d, want a 403", handled, w.Code)
	}
	if !bytes.Contains(inline.last.Body, []byte("OR 1=1")) {
		t.Errorf("in-path check scored %q, want the body", inline.last.Body)
	}
}

// A warn detection still reaches the app, so the rest of its body is
// sampled and reported.
func TestApplyEdgeRuleWAFWarnDetectionWithBodyIsSampled(t *testing.T) {
	inline := &fakeWAFInline{result: WAFInlineResult{Outcome: WAFInlineDetected, RuleIDs: []int{942100}}}
	h := inlineBodyHandler(inline, api.EdgeWAFModeWarn)
	for name, tc := range map[string]struct {
		body       io.Reader
		wantSample bool
	}{
		"with a body":    {strings.NewReader(`{"q":"1' OR 1=1--"}`), true},
		"without a body": {nil, false},
	} {
		r := httptest.NewRequest(http.MethodPost, "http://app.example.test/api/items", tc.body)
		w := newDeadlineRecorder()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
		handled, submit := h.applyEdgeRuleWAF(w, r, App{ID: "app-1", AccountID: "acct-1"}, rec)
		if handled || (submit != nil) != tc.wantSample {
			t.Errorf("%s: handled=%v sampled=%v, want passed and sampled=%v", name, handled, submit != nil, tc.wantSample)
		}
	}
}

// Without a read deadline the gate cannot bound the read, so it leaves the
// body to admission.
func TestApplyEdgeRuleWAFInlineBodyNeedsReadDeadline(t *testing.T) {
	inline := &fakeWAFInline{result: WAFInlineResult{Outcome: WAFInlineClean}}
	h := inlineBodyHandler(inline, api.EdgeWAFModeBlock)
	r := httptest.NewRequest(http.MethodPost, "http://app.example.test/api/items", strings.NewReader(`{"a":1}`))
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
	h.applyEdgeRuleWAF(w, r, App{ID: "app-1", AccountID: "acct-1"}, rec)
	if len(inline.last.Body) != 0 {
		t.Errorf("scored %d body bytes without a read deadline, want none", len(inline.last.Body))
	}
}

// Without an in-path checker there is nothing to score the body with.
func TestApplyEdgeRuleWAFInlineBodyNotReadWithoutChecker(t *testing.T) {
	h := inlineBodyHandler(nil, api.EdgeWAFModeBlock)
	h.wafInline = nil
	r := httptest.NewRequest(http.MethodPost, "http://app.example.test/api/items", strings.NewReader(`{"a":1}`))
	w := newDeadlineRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
	h.applyEdgeRuleWAF(w, r, App{ID: "app-1", AccountID: "acct-1"}, rec)
	if len(w.deadlines) != 0 {
		t.Errorf("body read without an in-path checker (deadlines %v)", w.deadlines)
	}
}

// stallingBody returns its bytes, then the timeout error the connection's
// read deadline produces when the client stalls.
type stallingBody struct{ r io.Reader }

func (s *stallingBody) Read(p []byte) (int, error) {
	if n, _ := s.r.Read(p); n > 0 {
		return n, nil
	}
	return 0, os.ErrDeadlineExceeded
}
func (s *stallingBody) Close() error { return nil }

// A client that stalls mid-body is held only until the read deadline; the
// bytes read and the timeout are put back for admission to answer.
func TestApplyEdgeRuleWAFInlineBodyStalledClient(t *testing.T) {
	inline := &fakeWAFInline{result: WAFInlineResult{Outcome: WAFInlineClean}}
	h := inlineBodyHandler(inline, api.EdgeWAFModeBlock)
	r := httptest.NewRequest(http.MethodPost, "http://app.example.test/api/items", nil)
	r.Body, r.ContentLength = &stallingBody{r: strings.NewReader("abc")}, 4096
	w := newDeadlineRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
	if handled, _ := h.applyEdgeRuleWAF(w, r, App{ID: "app-1", AccountID: "acct-1"}, rec); handled {
		t.Fatal("a stalled body was refused by the WAF gate")
	}
	if len(inline.last.Body) != 0 {
		t.Errorf("scored %q of a body whose read failed, want nothing", inline.last.Body)
	}
	if len(w.deadlines) == 0 || w.deadlines[0].IsZero() || w.deadlines[0].After(time.Now().Add(api.RequestUploadIdleTimeout)) {
		t.Errorf("first read deadline = %v, want within the upload idle timeout", w.deadlines)
	}
	got, err := io.ReadAll(r.Body)
	var netErr net.Error
	if string(got) != "abc" || !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Errorf("body replayed %q, %v; want the bytes read, then the timeout", got, err)
	}
}

// A native gRPC stream may send one message and wait for the reply; the
// gate must not wait for more of its body.
func TestApplyEdgeRuleWAFInlineBodyDoesNotWaitOnStreams(t *testing.T) {
	inline := &fakeWAFInline{result: WAFInlineResult{Outcome: WAFInlineClean}}
	h := inlineBodyHandler(inline, api.EdgeWAFModeBlock)
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	r := httptest.NewRequest(http.MethodPost, "http://app.example.test/pkg.Svc/Stream", pr)
	r.ContentLength = -1
	r.Header.Set("Content-Type", "application/grpc")
	app := App{ID: "app-1", AccountID: "acct-1", AppProtocol: api.AppProtocolGRPC}
	done := make(chan struct{})
	go func() {
		defer close(done)
		w := newDeadlineRecorder()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
		h.applyEdgeRuleWAF(w, r, app, rec)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the gate waited on a streaming body")
	}
	if len(inline.last.Body) != 0 {
		t.Errorf("scored %d body bytes of a stream, want none", len(inline.last.Body))
	}
}
