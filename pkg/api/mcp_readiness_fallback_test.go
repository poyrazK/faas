package api_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessFallbackValidation(t *testing.T) {
	zero := int64(0)
	yes, no := true, false
	valid := api.SetMCPReadinessFallbackRequest{ExpectedVersion: &zero, Enabled: &yes, WebhookURL: "https://fallback.example.com/hook", WebhookSecret: "receiver-key", AfterFailures: 2}
	if err := api.ValidateMCPReadinessFallbackRequest(valid); err != nil {
		t.Fatal(err)
	}
	if err := api.ValidateMCPReadinessFallbackRequest(api.SetMCPReadinessFallbackRequest{ExpectedVersion: &zero, Enabled: &no}); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*api.SetMCPReadinessFallbackRequest){
		func(r *api.SetMCPReadinessFallbackRequest) { r.ExpectedVersion = nil },
		func(r *api.SetMCPReadinessFallbackRequest) { r.Enabled = nil },
		func(r *api.SetMCPReadinessFallbackRequest) { r.Enabled = &no },
		func(r *api.SetMCPReadinessFallbackRequest) { r.AfterFailures = 0 },
		func(r *api.SetMCPReadinessFallbackRequest) { r.AfterFailures = 11 },
		func(r *api.SetMCPReadinessFallbackRequest) { r.WebhookSecret = " " },
		func(r *api.SetMCPReadinessFallbackRequest) { r.WebhookURL = "https://user:password@example.com/hook" },
		func(r *api.SetMCPReadinessFallbackRequest) { r.WebhookURL = "https://example.com/hook#fragment" },
		func(r *api.SetMCPReadinessFallbackRequest) { r.WebhookURL = "file:///tmp/webhook" },
		func(r *api.SetMCPReadinessFallbackRequest) { r.WebhookURL = "/relative" },
	} {
		req := valid
		change(&req)
		if api.ValidateMCPReadinessFallbackRequest(req) == nil {
			t.Fatal("accepted invalid fallback request", req)
		}
	}
}
