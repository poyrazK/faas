package productcontrol

import "github.com/onebox-faas/faas/pkg/api"

func CustomerActivation(status *api.ProductOnboardingStatus) api.ProductCustomerActivation {
	if status == nil {
		return api.ProductCustomerActivation{State: "not_recorded"}
	}
	a := api.ProductCustomerActivation{OnboardingID: status.Receipt.ID, SetupAt: &status.Receipt.CreatedAt, SetupCurrent: status.SetupCurrent, AccessConfigured: status.AccessConfigured, AccessVerifiedAt: status.AccessVerifiedAt, FirstSuccess: status.FirstSuccess}
	a.State = CustomerActivationState(a)
	return a
}

func CustomerActivationState(a api.ProductCustomerActivation) string {
	switch {
	case a.OnboardingID == "":
		return "not_recorded"
	case !a.SetupCurrent || !a.AccessConfigured:
		return "setup_changed"
	case a.FirstSuccess != nil:
		return "first_success_observed"
	case a.AccessVerifiedAt != nil:
		return "awaiting_first_success"
	default:
		return "awaiting_workspace_visit"
	}
}

func CustomerActivationLabel(state string) string {
	switch state {
	case "not_recorded":
		return "No guided activation record"
	case "setup_changed":
		return "Review changed setup"
	case "first_success_observed":
		return "First success recorded"
	case "awaiting_first_success":
		return "Waiting for first success"
	case "awaiting_workspace_visit":
		return "Waiting for workspace visit"
	default:
		return "Activation unverified"
	}
}
