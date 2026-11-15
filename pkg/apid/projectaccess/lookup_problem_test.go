package projectaccess

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod 2026-11-15: a project lookup cut by the request deadline answered 503
// "could not authorize project access", which the CLI shows as "Briefly at
// capacity", so a slow upload looked like platform load.
func TestLookupProblemNamesTimeoutsAsTimeouts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not found", state.ErrNotFound, http.StatusNotFound, api.CodeNotFound},
		{"deadline", fmt.Errorf("read project: %w", context.DeadlineExceeded), http.StatusGatewayTimeout, api.CodeRequestBudgetExceeded},
		{"canceled", context.Canceled, http.StatusGatewayTimeout, api.CodeRequestBudgetExceeded},
		{"store failure", errors.New("connection refused"), http.StatusServiceUnavailable, api.CodeCapacity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := lookupProblem(tc.err)
			if p == nil || p.Status != tc.status || p.Code != tc.code {
				t.Fatalf("lookupProblem(%v) = %+v, want %d %s", tc.err, p, tc.status, tc.code)
			}
		})
	}
}
