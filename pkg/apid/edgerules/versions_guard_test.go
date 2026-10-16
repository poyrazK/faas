package edgerules

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type unversionedRules struct{ state.Store }
type versionGuardHost struct {
	Host
	store state.Store
}

func (h versionGuardHost) Store() state.Store { return h.store }

func TestIfMatchFailsClosedWithoutVersionStore(t *testing.T) {
	s := &Service{Host: versionGuardHost{store: unversionedRules{Store: state.NewMemStore()}}}
	r := httptest.NewRequest(http.MethodPatch, "/v1/edge-rules/rule", nil)
	r.Header.Set("If-Match", `"1"`)
	w := httptest.NewRecorder()
	if !s.edgeRuleIfMatchFailed(t.Context(), w, r, "app") || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unenforceable version allowed: %d %s", w.Code, w.Body.String())
	}
}
