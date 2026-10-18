// adr: 202610171904
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOrgResourceRequestBudgetSpansCreatorNamespaces(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.setLegacyHot()
	backend.app.Plan = api.PlanFree
	backend.app.ResourceOwnerID = "company-budget"
	h.WithLimiter(NewLimiter().WithNoop())
	fixed := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	h.WithAccountLimiter(NewLimiterWithClock(func() time.Time { return fixed }))
	// Alternating namespaces must still drain one aggregate company bucket.
	for i := 0; i < 350; i++ {
		backend.app.AccountID = []string{"creator-a", "creator-b"}[i%2]
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil))
		if rec.Code == http.StatusTooManyRequests {
			if got := rec.Header().Get("x-faas-rate-limit-scope"); got != "account" {
				t.Fatalf("429 scope = %q, want account", got)
			}
			return
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	t.Fatal("company request budget was split across creator namespaces")
}
