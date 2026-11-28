package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod hunt r73: a deleted organization stayed in GET /v1/orgs forever. It is
// left out unless ?include_deleted=true asks for it (ADR-061 amendment 1).
func TestOrgListHidesDeletedOrgs(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	acct, key := flowAccountKey(t, store, "org-list-deleted@example.com", true)
	creator, ok := store.(state.SharedOrgCreationStore)
	if !ok {
		t.Skip("store cannot create shared orgs")
	}
	org, err := creator.CreateSharedOrg(context.Background(), acct.ID, "gone-team", "Gone")
	if err != nil {
		t.Fatalf("CreateSharedOrg: %v", err)
	}
	if err := store.SoftDeleteOrg(context.Background(), org.ID); err != nil {
		t.Fatalf("SoftDeleteOrg: %v", err)
	}
	rec := flowBearer(h, http.MethodGet, "/v1/orgs", key, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "gone-team") {
		t.Fatalf("GET /v1/orgs = %d %s, want the deleted org hidden", rec.Code, rec.Body.String())
	}
	rec = flowBearer(h, http.MethodGet, "/v1/orgs?include_deleted=true", key, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "gone-team") {
		t.Fatalf("GET /v1/orgs?include_deleted=true = %d %s, want the deleted org", rec.Code, rec.Body.String())
	}
}
