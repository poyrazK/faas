package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type convergingEdgeRuleMatcher struct{ noOpEdgeRuleMatcher }

func (convergingEdgeRuleMatcher) Converging(string) bool { return true }

func TestHandlerFailsClosedDuringEdgeRuleConvergence(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	h.edgeRules = convergingEdgeRuleMatcher{}

	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	if !strings.Contains(rec.Body.String(), "Edge policy update in progress") {
		t.Fatalf("response did not explain convergence: %s", rec.Body.String())
	}
	if got := backend.pickCalls.Load(); got != 0 {
		t.Fatalf("backend Pick calls = %d, want 0 during convergence", got)
	}
	if got := backend.admits; got != 0 {
		t.Fatalf("backend Admit calls = %d, want 0 during convergence", got)
	}
}
