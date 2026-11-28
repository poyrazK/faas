package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Prod hunt r69: `billing usage summary --month 2099-01` answered zero usage
// with "Included: 0 GB-hours". A month that has not started is a 400.
func TestUsageSummaryRefusesAFutureMonth(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	for _, path := range []string{"/v1/usage/summary?month=2099-01", "/v1/account/usage?month=2099-01"} {
		rec := e.Do(t, http.MethodGet, path, nil, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "has not started") {
			t.Fatalf("%s: %d %s, want 400 has not started", path, rec.Code, rec.Body)
		}
	}
	rec := e.Do(t, http.MethodGet, "/v1/usage/summary?month="+time.Now().UTC().Format("2006-01"), nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("current month: %d %s", rec.Code, rec.Body)
	}
}

// Prod hunt r68: `gregale rollback shop --to nope` reached the uuid column,
// failed to parse and answered 503 "Deployment temporarily unavailable".
// A reference that is neither an id nor a vN revision is a 404.
func TestRollbackToMalformedReferenceIsNotFound(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	dep := apidserver.MustSeedDeployment(t, e, "rb-ref")
	if err := (*e.Store()).MarkDeploymentLive(context.Background(), dep.ID); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"nope", "v", "abc-123"} {
		body := api.RollbackRequest{TargetDeploymentID: &ref}
		rec := e.Do(t, http.MethodPost, "/v1/apps/rb-ref/rollback", body, nil)
		apidserver.AssertProblem(t, rec, http.StatusNotFound, api.CodeRollbackTargetNotFound)
		if !strings.Contains(rec.Body.String(), "not a deployment id or a vN revision") {
			t.Fatalf("%q: body %s", ref, rec.Body)
		}
	}
}
