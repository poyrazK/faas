package api_test

import (
	"math"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessPagerDutyFallbackValidation(t *testing.T) {
	zero, one, max := int64(0), int64(1), int64(math.MaxInt64)
	on, off := true, false
	valid := api.SetMCPReadinessFallbackRequest{ExpectedVersion: &zero, Enabled: &on, Provider: "pagerduty", IntegrationKey: strings.Repeat("a", 32), AfterFailures: 2}
	for _, region := range []string{"", "us", "eu"} {
		r := valid
		r.Region = region
		if err := api.ValidateMCPReadinessFallbackRequest(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func(*api.SetMCPReadinessFallbackRequest){
		func(r *api.SetMCPReadinessFallbackRequest) { r.Provider = "other" },
		func(r *api.SetMCPReadinessFallbackRequest) { r.Provider = "webhook" },
		func(r *api.SetMCPReadinessFallbackRequest) { r.Region = "ap" },
		func(r *api.SetMCPReadinessFallbackRequest) { r.IntegrationKey = "bad" },
		func(r *api.SetMCPReadinessFallbackRequest) { r.WebhookURL = "https://example.com/hook" },
		func(r *api.SetMCPReadinessFallbackRequest) { r.WebhookSecret = "secret" },
		func(r *api.SetMCPReadinessFallbackRequest) { r.AfterFailures = 0 },
		func(r *api.SetMCPReadinessFallbackRequest) { r.ExpectedVersion = &max },
		func(r *api.SetMCPReadinessFallbackRequest) { r.Enabled = &off },
	} {
		r := valid
		mutate(&r)
		if api.ValidateMCPReadinessFallbackRequest(r) == nil {
			t.Fatal("invalid provider configuration accepted")
		}
	}
	binding := api.SetMCPReadinessPagerDutyAcknowledgementsRequest{ExpectedVersion: &zero, Enabled: &on, Destination: "fallback", ExpectedFallbackVersion: &one, ServiceID: "PSVC001", SigningSecret: strings.Repeat("s", 32)}
	if err := api.ValidateMCPReadinessPagerDutyAcknowledgementsRequest(binding); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*api.SetMCPReadinessPagerDutyAcknowledgementsRequest){
		func(r *api.SetMCPReadinessPagerDutyAcknowledgementsRequest) { r.ExpectedFallbackVersion = nil },
		func(r *api.SetMCPReadinessPagerDutyAcknowledgementsRequest) { r.ExpectedFallbackVersion = &zero },
		func(r *api.SetMCPReadinessPagerDutyAcknowledgementsRequest) { r.ExpectedPagerDutyVersion = &one },
		func(r *api.SetMCPReadinessPagerDutyAcknowledgementsRequest) { r.Destination = "primary" },
		func(r *api.SetMCPReadinessPagerDutyAcknowledgementsRequest) { r.Destination = "other" },
		func(r *api.SetMCPReadinessPagerDutyAcknowledgementsRequest) { r.Enabled = &off },
	} {
		r := binding
		mutate(&r)
		if api.ValidateMCPReadinessPagerDutyAcknowledgementsRequest(r) == nil {
			t.Fatal("ambiguous binding accepted")
		}
	}
}
