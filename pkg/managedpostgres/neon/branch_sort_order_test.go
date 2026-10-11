// adr: 590 — Neon pagination metadata must preserve the requested ordering.
package neon

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestProvisionRecoveryAcceptsNeonUppercaseBranchSortOrder(t *testing.T) {
	var posts atomic.Int32
	base := readyProjectHandler(t, &posts)
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/quiet-river-12345678/branches" {
			if r.URL.Query().Get("sort_by") != "created_at" || r.URL.Query().Get("sort_order") != "asc" {
				t.Error("branch inventory did not request stable ascending creation order")
			}
			writeResponse(t, w, http.StatusOK, map[string]any{
				"branches":   []branch{{ID: "br-main-123", ProjectID: "quiet-river-12345678", Default: true, CurrentState: "ready"}},
				"pagination": map[string]any{"sort_by": "created_at", "sort_order": "ASC"},
			})
			return
		}
		base.ServeHTTP(w, r)
	}))
	request := managedpostgres.ProvisionRequest{ResourceID: "22222222-2222-2222-2222-222222222222", IdempotencyKey: "recover", Spec: testDatabaseSpec()}
	actual, err := p.Provision(t.Context(), request)
	if err != nil || actual.ProviderResourceID != "quiet-river-12345678" || actual.Status != managedpostgres.ProviderStatusReady || actual.Spec != request.Spec || posts.Load() != 0 {
		t.Fatalf("uppercase provider ordering prevented recovery: %+v %v; mutations=%d", actual, err, posts.Load())
	}
}
