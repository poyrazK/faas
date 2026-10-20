// adr: 202610171906

package postgresaccess

import (
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Resolving access before the handler must keep the published 404 code
// (cmd/e2e TestE2E_ManagedPostgres_ManifestFailsClosedAndIsolatesTenants).
func TestLookupProblemKeepsManagedPostgresNotFoundCode(t *testing.T) {
	for _, err := range []error{state.ErrNotFound, managedpostgres.ErrNotFound} {
		p := lookupProblem(err)
		if p.Status != http.StatusNotFound || p.Code != "managed_postgres_not_found" {
			t.Fatalf("lookupProblem(%v) = %d %s, want 404 managed_postgres_not_found", err, p.Status, p.Code)
		}
	}
	if p := lookupProblem(errors.New("database unavailable")); p.Status == http.StatusNotFound {
		t.Fatalf("an unavailable store answered 404: %+v", p)
	}
}
