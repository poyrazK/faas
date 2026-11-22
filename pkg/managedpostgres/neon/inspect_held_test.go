// adr: 20261029174950 — a held database's endpoint is disabled; Inspect
// reports it instead of failing on the refused SQL connection.
package neon

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func heldInspectProvider(t *testing.T, disabled bool, state string, connections *int) *Provider {
	t.Helper()
	const project = "/api/v2/projects/quiet-river-12345678"
	provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case project:
			writeResponse(t, w, http.StatusOK, map[string]any{"project": map[string]any{"id": "quiet-river-12345678"}})
		case project + "/branches":
			writeResponse(t, w, http.StatusOK, map[string]any{"branches": []map[string]any{
				{"id": "br-main-123", "project_id": "quiet-river-12345678", "default": true, "current_state": "ready"}}})
		case project + "/endpoints":
			writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": []map[string]any{
				{"id": "ep-main-123", "project_id": "quiet-river-12345678", "branch_id": "br-main-123", "type": "read_write", "current_state": state, "disabled": disabled}}})
		case project + "/operations":
			writeResponse(t, w, http.StatusOK, map[string]any{"operations": []map[string]any{}})
		case project + "/connection_uri":
			*connections++
			writeResponse(t, w, http.StatusOK, connectionURIResponse{URI: "postgres://gregale_owner:test@ep-main-123.example.test/" + "gregale?sslmode=require"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			writeResponse(t, w, http.StatusNotFound, nil)
		}
	}))
	provider.databaseName = "gregale"
	provider.roles = &fakeCredentialRoles{}
	return provider
}

// A held database refuses connections, so Inspect neither opens one to
// restrict inherited logins nor fails: it reports the endpoint's state, which
// is how the deletion hold probe and the cap sweep see the compute suspend.
func TestInspectReportsAHeldDatabaseWithoutConnecting(t *testing.T) {
	connections := 0
	provider := heldInspectProvider(t, true, "idle", &connections)
	observed, err := provider.Inspect(t.Context(), "quiet-river-12345678/br-main-123")
	if err != nil {
		t.Fatalf("inspect held database = %v", err)
	}
	if observed.Status != managedpostgres.ProviderStatusReady || observed.ComputeState != managedpostgres.ComputeStateSuspended {
		t.Fatalf("held database observed %+v, want ready and suspended", observed)
	}
	if roles := provider.roles.(*fakeCredentialRoles); roles.restrictions != 0 || connections != 0 {
		t.Fatalf("held database opened %d owner connections and %d restrictions", connections, roles.restrictions)
	}
}

// Once released, the next Inspect restricts inherited logins again before
// anyone can connect, as before.
func TestInspectRestrictsInheritedLoginsOnceReleased(t *testing.T) {
	connections := 0
	provider := heldInspectProvider(t, false, "idle", &connections)
	observed, err := provider.Inspect(t.Context(), "quiet-river-12345678/br-main-123")
	if err != nil {
		t.Fatalf("inspect released database = %v", err)
	}
	if roles := provider.roles.(*fakeCredentialRoles); roles.restrictions != 1 || connections != 1 {
		t.Fatalf("released database: %d owner connections and %d restrictions, want 1 and 1", connections, roles.restrictions)
	}
	if observed.ComputeState != managedpostgres.ComputeStateActive {
		t.Fatalf("released database compute = %v, want active after the restriction connected", observed.ComputeState)
	}
}
