// adr: 531
package api

import "testing"

func TestTotalDeadlineValidation(t *testing.T) {
	for _, tc := range []struct {
		ms    int
		valid bool
	}{{-1, false}, {0, true}, {1, true}, {int(RequestBudgetMax.Milliseconds()), true}, {int(RequestBudgetMax.Milliseconds()) + 1, false}} {
		a := EdgeRuleBudgetAction{BudgetMs: 1000, TotalDeadlineMs: tc.ms}
		if valid := a.Validate() == nil; valid != tc.valid {
			t.Errorf("total_deadline_ms=%d valid=%v want=%v", tc.ms, valid, tc.valid)
		}
	}
}
