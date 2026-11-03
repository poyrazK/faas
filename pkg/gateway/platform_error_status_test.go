// adr: 20261030115139
package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Cloudflare replaces an origin's 502/504 body and passes 503 through, so a
// platform problem leaves the public edge as 503 with its body intact and its
// status member corrected; an app's own 502/504 and every other status pass
// unchanged.
func TestPlatformErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		name        string
		handler     http.HandlerFunc
		wantStatus  int
		wantCode    string
		wantBodySub string
		wantMember  int // status member of a JSON body; 0 skips the check
	}{
		{
			name: "platform 504 problem",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeRequestBudgetExceeded, "Request budget exceeded", "the app took too long"))
			},
			wantStatus: http.StatusServiceUnavailable, wantCode: api.CodeRequestBudgetExceeded,
			wantBodySub: "the app took too long", wantMember: http.StatusServiceUnavailable,
		},
		{
			name: "platform 502 problem",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				writeForwarderProblem(w, http.StatusBadGateway)
			},
			wantStatus: http.StatusServiceUnavailable, wantCode: api.CodeBadGateway,
			wantBodySub: "App connection failed", wantMember: http.StatusServiceUnavailable,
		},
		{
			name: "hand-written budget problem",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(api.ErrorCodeHeader, api.CodeRequestBudgetExceeded)
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusGatewayTimeout)
				_, _ = w.Write([]byte(`{"type":"about:blank","status":504,"code":"request_budget_exceeded"}`))
			},
			wantStatus: http.StatusServiceUnavailable, wantCode: api.CodeRequestBudgetExceeded,
			wantBodySub: "request_budget_exceeded", wantMember: http.StatusServiceUnavailable,
		},
		{
			name: "platform 504 as a browser page",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(api.ErrorCodeHeader, api.CodeRequestBudgetExceeded)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusGatewayTimeout)
				_, _ = io.WriteString(w, "<p>Gateway Timeout</p>")
			},
			wantStatus: http.StatusServiceUnavailable, wantCode: api.CodeRequestBudgetExceeded,
			wantBodySub: "<p>Gateway Timeout</p>",
		},
		{
			name: "app's own 502",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(w, `{"status":502}`)
			},
			wantStatus: http.StatusBadGateway, wantBodySub: `{"status":502}`, wantMember: http.StatusBadGateway,
		},
		{
			name: "platform 500 problem",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				api.WriteProblem(w, api.ErrInternal("boom"))
			},
			wantStatus: http.StatusInternalServerError, wantCode: api.CodeInternal,
			wantBodySub: "boom", wantMember: http.StatusInternalServerError,
		},
		{
			name: "success",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "ok")
			},
			wantStatus: http.StatusOK, wantBodySub: "ok",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			PlatformErrorStatus(tc.handler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Header().Get(api.ErrorCodeHeader); got != tc.wantCode {
				t.Fatalf("%s = %q, want %q", api.ErrorCodeHeader, got, tc.wantCode)
			}
			if !strings.Contains(rec.Body.String(), tc.wantBodySub) {
				t.Fatalf("body = %q, want substring %q", rec.Body.String(), tc.wantBodySub)
			}
			if tc.wantMember != 0 {
				var body struct {
					Status int `json:"status"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Status != tc.wantMember {
					t.Fatalf("status member = %d (err %v), want %d; body %q", body.Status, err, tc.wantMember, rec.Body.String())
				}
			}
		})
	}
}

func TestPlatformErrorStatusCorrectsContentLength(t *testing.T) {
	const body = `{"code":"long_poll_timeout","status":504,"title":"Timed out"}`
	rec := httptest.NewRecorder()
	PlatformErrorStatus(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(api.ErrorCodeHeader, "long_poll_timeout")
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusGatewayTimeout)
		_, _ = io.WriteString(w, body)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got, want := rec.Header().Get("Content-Length"), strconv.Itoa(rec.Body.Len()); got != want {
		t.Fatalf("Content-Length = %s, want %s for body %q", got, want, rec.Body.String())
	}
}

// A body too large to be one of Gregale's problems, or one flushed before the
// handler returns, is sent as written under 503.
func TestPlatformErrorStatusReleasesLargeOrFlushedBodies(t *testing.T) {
	large := `{"status":504,"detail":"` + strings.Repeat("x", platformProblemBodyLimit) + `"}`
	for _, tc := range []struct {
		name  string
		write func(w http.ResponseWriter)
	}{
		{name: "large", write: func(w http.ResponseWriter) { _, _ = io.WriteString(w, large) }},
		{name: "flushed", write: func(w http.ResponseWriter) {
			_, _ = io.WriteString(w, `{"status":504}`)
			_ = http.NewResponseController(w).Flush()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			PlatformErrorStatus(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(api.ErrorCodeHeader, api.CodeRequestBudgetExceeded)
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusGatewayTimeout)
				tc.write(w)
			})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", rec.Code)
			}
			if !rec.Flushed && tc.name == "flushed" {
				t.Fatal("flush did not reach the client")
			}
			if tc.name == "large" && rec.Body.String() != large {
				t.Fatalf("large body changed: %d bytes, want %d", rec.Body.Len(), len(large))
			}
		})
	}
}

func TestProblemWithStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{name: "top-level member", in: `{"code":"c","status":504}` + "\n", want: `{"code":"c","status":503}` + "\n"},
		{name: "nested status left alone", in: `{"release_approval":{"status":504},"status":504}`, want: `{"release_approval":{"status":504},"status":503}`},
		{name: "other status", in: `{"status":500}`, want: `{"status":500}`},
		{name: "no member", in: `{"code":"c"}`, want: `{"code":"c"}`},
		{name: "not JSON", in: `<html>`, want: `<html>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := problemWithStatus([]byte(tc.in), http.StatusGatewayTimeout, http.StatusServiceUnavailable); !bytes.Equal(got, []byte(tc.want)) {
				t.Fatalf("problemWithStatus(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Only Gregale marks a platform problem; a guest's copy is dropped on both
// forward paths so an app cannot have its own 502/504 rewritten.
func TestGuestErrorCodeHeaderIsDropped(t *testing.T) {
	dst := http.Header{}
	forwardedResponseHeader(t.Context(), dst, "x-faas-error-code", api.CodeRequestBudgetExceeded)
	forwardedResponseHeader(t.Context(), dst, "X-App", "kept")
	if got := dst.Get(api.ErrorCodeHeader); got != "" {
		t.Fatalf("guest error code forwarded: %q", got)
	}
	if dst.Get("X-App") != "kept" {
		t.Fatal("an ordinary guest header was dropped")
	}

	resp := &http.Response{Header: http.Header{api.ErrorCodeHeader: {api.CodeBadGateway}}}
	stripGuestEvidenceResponseHeaders(resp)
	if got := resp.Header.Get(api.ErrorCodeHeader); got != "" {
		t.Fatalf("legacy forward kept the guest error code %q", got)
	}
}
