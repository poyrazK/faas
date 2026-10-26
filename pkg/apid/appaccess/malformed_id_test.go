package appaccess

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestMalformedPathIDIsNotFound: an app slug where a deployment, invocation
// or edge rule ID belongs reached Postgres as a uuid cast and came back 503
// "Briefly at capacity" on production-us rc.294 (Hunt 14 H14-65). It is the
// route family's own 404, like any absent ID.
func TestMalformedPathIDIsNotFound(t *testing.T) {
	malformed := fmt.Errorf("lookup: %w", &pgconn.PgError{Code: "22P02"})
	for _, tc := range []struct {
		path   string
		err    error
		status int
		code   string
	}{
		{"/v1/deployments/h14-py/canary/profile-gate", malformed, http.StatusNotFound, api.CodeNotFound},
		{"/v1/invocations/notauuid", malformed, http.StatusNotFound, api.CodeInvocationNotFound},
		{"/v1/edge-rules/notauuid", malformed, http.StatusNotFound, api.CodeEdgeRuleNotFound},
		{"/v1/deployments/h14-py", fmt.Errorf("lookup: %w", &pgconn.PgError{Code: "57P01"}), http.StatusServiceUnavailable, ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			problem := lookupProblem(r, tc.err)
			if problem.Status != tc.status || (tc.code != "" && problem.Code != tc.code) {
				t.Fatalf("got %d %s, want %d %s (%+v)", problem.Status, problem.Code, tc.status, tc.code, problem)
			}
		})
	}
}
