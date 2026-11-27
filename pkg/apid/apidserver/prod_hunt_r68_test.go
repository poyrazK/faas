package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

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
