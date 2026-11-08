package gateway

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261108100000 — the gateway normalizes dot segments and duplicate
// slashes before edge rules run, so a higher-priority exception on
// /public/** is not selected for /public/../upload, which the app serves as
// /upload, and the app receives /upload.
func TestNormalizedPathSelectsTheServedPathsRule(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)
	_, client, _ := net.ParseCIDR("198.51.100.0/24")
	_, everyone, _ := net.ParseCIDR("0.0.0.0/0")
	newHandler := func() *Handler {
		b := &fakeBackend{
			app:      App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
			host:     "jane-api.apps.dom",
			upstream: upstream.Listener.Addr().String(),
		}
		h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
		h.WithEdgeRules(pathIPMatcher{
			byPrefix: map[string]*EdgeRuleIPResolved{
				"/public/": {ID: "public-open", AccountID: "acct-1", AppID: "app-1", Allow: []*net.IPNet{everyone}},
				"/upload":  {ID: "upload-deny", AccountID: "acct-1", AppID: "app-1", Deny: []*net.IPNet{client}},
			},
		}, nil, nil)
		return h
	}
	for _, target := range []string{"/public/../upload", "/public/%2e%2e/upload", "//upload", "/public/./../upload"} {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom"+target, nil)
			req.Header.Set("X-Forwarded-For", "198.51.100.7")
			rec := httptest.NewRecorder()
			newHandler().ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body %q, want 403 from the /upload deny", rec.Code, rec.Body)
			}
		})
	}
	gotPath = ""
	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/public/a/../b//c/", nil)
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || gotPath != "/public/b/c/" {
		t.Fatalf("status = %d, app got %q; want 200 and /public/b/c/", rec.Code, gotPath)
	}
}

func TestNormalizeRequestPathKeepsCleanPathsAndTheOriginalURI(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/files/a%2Fb", nil)
	normalizeRequestPath(req)
	if req.URL.EscapedPath() != "/files/a%2Fb" {
		t.Fatalf("clean path rewritten to %q; escapes must survive", req.URL.EscapedPath())
	}
	req = httptest.NewRequest(http.MethodGet, "/x/../etc/passwd?q=1", nil)
	normalizeRequestPath(req)
	if req.URL.Path != "/etc/passwd" {
		t.Fatalf("path = %q", req.URL.Path)
	}
	if got := originalRequestURI(req); got != "/x/../etc/passwd?q=1" {
		t.Fatalf("WAF URI = %q; want the request line as received", got)
	}
	req = httptest.NewRequest(http.MethodGet, "http://app.example.test/x/../etc/passwd?q=1", nil)
	normalizeRequestPath(req)
	if got := originalRequestURI(req); got != "/x/../etc/passwd?q=1" {
		t.Fatalf("WAF URI for an absolute-form target = %q; want its origin form, unnormalized", got)
	}
}
