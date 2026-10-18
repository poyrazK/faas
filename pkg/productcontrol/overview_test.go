package productcontrol

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductControlCustomerActivationEvidence(t *testing.T) {
	now := time.Now().UTC()
	if a := CustomerActivation(nil); a.State != "not_recorded" || a.FirstSuccess != nil || a.AccessVerifiedAt != nil {
		t.Fatalf("legacy assignment inferred activation: %+v", a)
	}
	for _, tc := range []struct {
		name, want                           string
		visit, success, changed, unavailable bool
	}{
		{name: "setup is not a visit", want: "awaiting_workspace_visit"},
		{name: "visit is not success", visit: true, want: "awaiting_first_success"},
		{name: "verified success", visit: true, success: true, want: "first_success_observed"},
		{name: "changed assignment keeps historical proof", visit: true, success: true, changed: true, want: "setup_changed"},
		{name: "unavailable mapping keeps historical proof", success: true, unavailable: true, want: "setup_changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := api.ProductOnboardingStatus{Receipt: api.ProductOnboardingReceipt{ID: "recorded-setup", CreatedAt: now}, SetupCurrent: !tc.changed, AccessConfigured: !tc.unavailable}
			if tc.visit {
				s.AccessVerifiedAt = &now
			}
			if tc.success {
				s.FirstSuccess = &api.ProductOnboardingSuccess{OperationID: "recorded-success", VerifiedAt: now}
			}
			a := CustomerActivation(&s)
			if a.State != tc.want || (a.FirstSuccess != nil) != tc.success || a.OnboardingID != s.Receipt.ID {
				t.Fatalf("activation claim: %+v", a)
			}
		})
	}
}
