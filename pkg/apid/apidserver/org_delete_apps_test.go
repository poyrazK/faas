package apidserver_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Organizations are never purged, so deleting one that still had apps left
// the apps and their artifacts in place forever and blocked their creators'
// account erasure (bug hunt 2026-10-28 B20).
func TestOrganizationWithAppsCannotBeDeleted(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, "acme-b20", "Acme B20", api.PlanPro)
	store := *e.Store()
	if _, err := store.CreateApp(t.Context(), state.App{
		AccountID: e.Acct().ID, OrgID: org.ID, Slug: "b20-app", Type: state.AppTypeFunction,
		Runtime: "node22", RAMMB: 256, MaxConcurrency: 1, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug, nil, map[string]string{"X-Active-Org": org.Slug})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "apps") {
		t.Fatalf("DELETE org with an app = %d %s, want 409 naming its apps", rec.Code, rec.Body.String())
	}
}
