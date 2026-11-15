package projectaccess

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// prod 2026-11-15: a project lookup that ran out of request time answered
// 503 "Briefly at capacity"; it is a 504 budget error now.
func TestLookupProblemDeadlineIs504(t *testing.T) {
	p := lookupProblem(fmt.Errorf("project by slug: %w", context.DeadlineExceeded))
	if p.Status != http.StatusGatewayTimeout || p.Code != api.CodeRequestBudgetExceeded {
		t.Fatalf("deadline problem = %d %s", p.Status, p.Code)
	}
	if p := lookupProblem(errors.New("boom")); p.Status != http.StatusServiceUnavailable {
		t.Fatalf("other error = %d, want 503", p.Status)
	}
}
