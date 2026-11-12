package apidserver_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/financial"
)

// Hunt 34 H34-15: each failed activation condition gets its own advice.
func TestFinancialBudgetProblemNamesTheFailedCondition(t *testing.T) {
	for reason, want := range map[string]string{
		financial.ComputeUnavailableRunning:    "gregale app park",
		financial.ComputeUnavailableAccounting: "Try again in a few minutes",
		financial.ComputeUnavailableNodes:      "Nothing in your account blocks it",
		"other":                                "Activation requires quiet covered workloads",
	} {
		problem := apidserver.FinancialBudgetProblem(errors.Join(financial.ComputeUnavailableBecause(reason)))
		if problem.Code != "financial_compute_not_ready" || problem.Status != 422 || !strings.Contains(problem.Detail, want) {
			t.Errorf("reason %s: %+v; want detail containing %q", reason, problem, want)
		}
	}
	if problem := apidserver.FinancialBudgetProblem(financial.ErrComputeUnavailable); !strings.Contains(problem.Detail, "Activation requires") {
		t.Errorf("bare sentinel: %+v", problem)
	}
}
