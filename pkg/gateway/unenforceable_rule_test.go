package gateway

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// adr: 091 — a host failed closed by a broken rule answers 503 naming the
// rule, not a generic "retry shortly" (amendment 2026-11-03, review round 9).
func TestUnenforceableEdgeRuleRefusalNamesTheRule(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for name, tc := range map[string]struct {
		err      error
		contains string
	}{
		"named rule":   {&UnenforceableEdgeRuleError{RuleID: "er_broken"}, "edge rule er_broken on this host cannot be enforced"},
		"unnamed rule": {&UnenforceableEdgeRuleError{}, "Retry shortly"},
		"other error":  {errors.New("x"), "Retry shortly"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.writeUnenforceableEdgeRule(rec, httptest.NewRequest(http.MethodGet, "/", nil), tc.err)
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), tc.contains) {
				t.Fatalf("response = %d %s, want 503 containing %q", rec.Code, rec.Body, tc.contains)
			}
		})
	}
}
