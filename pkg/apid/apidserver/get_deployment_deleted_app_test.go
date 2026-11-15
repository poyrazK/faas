package apidserver_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Prod 2026-11-15 (findings #6): an app deleted while its deploy was in
// flight answered GET /v1/deployments/{id} with 503 capacity (its
// configuration read found no app), and `gregale deploy --wait` retried that
// forever. A deployment of a deleted app is final: 404.
func TestGetDeploymentOfADeletedAppIsNotFound(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	dep := apidserver.MustSeedDeployment(t, e, "deleted-mid-deploy")
	store := *e.Store()
	if _, err := store.ScheduleAppDeletion(t.Context(), dep.AppID, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodGet, "/v1/deployments/"+dep.ID, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body)
	}
}
