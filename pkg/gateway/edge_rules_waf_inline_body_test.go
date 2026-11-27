package gateway

// adr: 831 amendment 22 — under edge_waf_strict_inspection and
// edge_waf_inline_body, warn and block
// rules score the first api.EdgeWAFMaxInlineBodyBytes of the body in-path,
// read once and put back, so the app receives the body unchanged. Compressed
// bodies and streaming bodies of unknown length are left to the sampled check.

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

func TestApplyEdgeRuleWAFInlineBody(t *testing.T) {
	long := strings.Repeat("b", 3*api.EdgeWAFMaxInlineBodyBytes)
	for _, tc := range []struct {
		name          string
		body          string
		header        map[string]string
		chunked       bool
		flags         *platformflags.Set
		wantPrefix    string
		wantTruncated bool
	}{
		{name: "short body is scored whole", body: `{"q":"1' OR 1=1--"}`, wantPrefix: `{"q":"1' OR 1=1--"}`},
		{name: "long body is scored up to the bound", body: long, wantPrefix: long[:api.EdgeWAFMaxInlineBodyBytes], wantTruncated: true},
		{name: "body of exactly the bound", body: long[:api.EdgeWAFMaxInlineBodyBytes], wantPrefix: long[:api.EdgeWAFMaxInlineBodyBytes]},
		{name: "chunked JSON is scored", body: `{"a":1}`, chunked: true, header: map[string]string{"Content-Type": "application/json"}, wantPrefix: `{"a":1}`},
		{name: "compressed body is not scored", body: "\x1f\x8b\x08rest", header: map[string]string{"Content-Encoding": "gzip"}},
		{name: "chunked NDJSON is not scored", body: "{\"a\":1}\n", chunked: true, header: map[string]string{"Content-Type": "application/x-ndjson"}},
		{name: "baseline set gets no body", body: `{"q":"1' OR 1=1--"}`,
			flags: edgeFeatureSet("acct-1", platformflags.EdgeWAFBlocking, platformflags.EdgeWAFInlineBody)},
		{name: "strict without the body flag gets no body", body: `{"q":"1' OR 1=1--"}`,
			flags: edgeFeatureSet("acct-1", platformflags.EdgeWAFBlocking, platformflags.EdgeWAFStrictInspection)},
		{name: "strict with the body flag", body: `{"q":"1' OR 1=1--"}`, wantPrefix: `{"q":"1' OR 1=1--"}`,
			flags: edgeFeatureSet("acct-1", platformflags.EdgeWAFBlocking, platformflags.EdgeWAFStrictInspection, platformflags.EdgeWAFInlineBody)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inline := &fakeWAFInline{result: WAFInlineResult{Outcome: WAFInlineClean}}
			rule := &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", ParanoiaLevel: 1, AnomalyThreshold: 5, Mode: api.EdgeWAFModeBlock}
			h := &Handler{edgeRules: wafRuleMatcher{rule: rule}, wafInspector: &recordingWAFInspector{}, wafInline: inline}
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
			rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK, request: r}
			if handled, _ := h.applyEdgeRuleWAF(rec, r, App{ID: "app-1", AccountID: "acct-1"}, rec); handled {
				t.Fatal("a clean check refused the request")
			}
			if inline.calls != 1 {
				t.Fatalf("in-path checks = %d, want 1", inline.calls)
			}
			if string(inline.last.Body) != tc.wantPrefix || inline.last.BodyTruncated != tc.wantTruncated {
				t.Errorf("scored body = %d bytes (truncated %v), want %d bytes (truncated %v)",
					len(inline.last.Body), inline.last.BodyTruncated, len(tc.wantPrefix), tc.wantTruncated)
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
	rule := &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", ParanoiaLevel: 1, AnomalyThreshold: 5, Mode: api.EdgeWAFModeBlock}
	h := &Handler{edgeRules: wafRuleMatcher{rule: rule}, wafInspector: &recordingWAFInspector{}, wafInline: inline}
	r := httptest.NewRequest(http.MethodPost, "http://app.example.test/api/items", strings.NewReader(`{"q":"1' OR 1=1--"}`))
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
	if handled, _ := h.applyEdgeRuleWAF(rec, r, App{ID: "app-1", AccountID: "acct-1"}, rec); !handled || w.Code != http.StatusForbidden {
		t.Fatalf("handled=%v status=%d, want a 403", handled, w.Code)
	}
	if !bytes.Contains(inline.last.Body, []byte("OR 1=1")) {
		t.Errorf("in-path check scored %q, want the body", inline.last.Body)
	}
}

// A streaming client may send one message and wait for the reply; the gate
// must not wait for more of its body.
func TestApplyEdgeRuleWAFInlineBodyDoesNotWaitOnStreams(t *testing.T) {
	inline := &fakeWAFInline{result: WAFInlineResult{Outcome: WAFInlineClean}}
	rule := &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", ParanoiaLevel: 1, AnomalyThreshold: 5, Mode: api.EdgeWAFModeBlock}
	h := &Handler{edgeRules: wafRuleMatcher{rule: rule}, wafInspector: &recordingWAFInspector{}, wafInline: inline}
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	r := httptest.NewRequest(http.MethodPost, "http://app.example.test/pkg.Svc/Stream", pr)
	r.ContentLength = -1
	r.Header.Set("Content-Type", "application/grpc")
	done := make(chan struct{})
	go func() {
		defer close(done)
		rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK, request: r}
		h.applyEdgeRuleWAF(rec, r, App{ID: "app-1", AccountID: "acct-1"}, rec)
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
