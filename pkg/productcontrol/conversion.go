package productcontrol

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// ValidateTrialConversion checks the plans that keep a customer after their
// trial only with a payment method on file (ADR-20261022180921). Such a plan
// offers a trial and names a different fallback plan of the catalog that
// costs nothing and requires nothing; a Paddle signup cannot offer it,
// because a Paddle customer keeps no card on file.
func ValidateTrialConversion(spec api.ProductCatalogSpec) error {
	for name, plan := range spec.Plans {
		if !plan.RequiresPaymentMethod {
			if plan.FallbackPlan != "" {
				return fmt.Errorf("plan %q names a fallback_plan without requires_payment_method", name)
			}
			continue
		}
		fallback, ok := spec.Plans[plan.FallbackPlan]
		if plan.TrialDays <= 0 || !ok || plan.FallbackPlan == name {
			return fmt.Errorf("plan %q requires a payment method, so it needs trial_days and a fallback_plan naming another plan", name)
		}
		if fallback.MonthlyFeeMillicents > 0 || fallback.TrialDays > 0 || fallback.RequiresPaymentMethod {
			return fmt.Errorf("fallback plan %q of %q must have no monthly fee, no trial and no payment method requirement", plan.FallbackPlan, name)
		}
		if spec.Signup != nil && spec.Signup.Provider == "paddle" && SignupOffers(spec.Signup, name) {
			return fmt.Errorf("plan %q requires a payment method, which a Paddle signup cannot keep on file", name)
		}
	}
	return nil
}
