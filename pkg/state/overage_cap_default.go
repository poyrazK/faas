package state

import "github.com/onebox-faas/faas/pkg/api"

// planChangeOverageCap returns the spend cap an account moving from one plan
// to another holds afterwards (ADR-20261029180000):
//
//   - Becoming paid with no cap sets the new plan's default cap.
//   - A cap equal to the old plan's default follows the plan: another paid
//     plan's default, or none on Free.
//   - Any other cap, and a cap cleared on a paid plan, is the customer's
//     choice and stays as it is.
func planChangeOverageCap(from, to api.Plan, current *int64) *int64 {
	return PlanChangeOverageCap(from, to, current)
}

// PlanChangeOverageCap is planChangeOverageCap for callers that need the cap
// a plan change would leave before making it, such as the pre-downgrade
// object-storage check (ADR-20261101214152).
func PlanChangeOverageCap(from, to api.Plan, current *int64) *int64 {
	newDefault, newHasDefault := to.DefaultOverageCapCents()
	if current == nil {
		if newHasDefault && !from.IsPaid() {
			return &newDefault
		}
		return nil
	}
	if oldDefault, ok := from.DefaultOverageCapCents(); ok && from != to && *current == oldDefault {
		if newHasDefault {
			return &newDefault
		}
		return nil
	}
	return current
}
