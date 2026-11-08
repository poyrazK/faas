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
	next := planChangeOverageCapUncapped(from, to, current)
	// ADR-20261105120000: a cap above the target plan's self-service ceiling
	// (one set on a larger plan) comes down to that ceiling. A paid subject
	// left without a cap is held to the ceiling when the cap is read
	// (EffectiveOverageCapCents).
	if ceiling, paid := to.MaxSelfServiceOverageCapCents(); paid && next != nil && *next > ceiling {
		return &ceiling
	}
	return next
}

// EffectiveOverageCapCents is the spend cap enforced for a subject on plan
// whose stored cap is stored (nil = NULL). A paid subject without one is held
// to its plan's self-service ceiling (ADR-20261105120000); before that record
// NULL meant no cap at all. Free without a cap has none: it stops at its
// allowance.
func EffectiveOverageCapCents(plan api.Plan, stored *int64) (int64, bool) {
	if stored != nil {
		return *stored, true
	}
	return plan.MaxSelfServiceOverageCapCents()
}

func planChangeOverageCapUncapped(from, to api.Plan, current *int64) *int64 {
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
