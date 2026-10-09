package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const us1Events = "https://api.datadoghq.com/api/v1/events"

func TestValidateDatadogAppWebhook(t *testing.T) {
	key := strPtr("0123456789abcdef0123456789abcdef")
	for _, tc := range []struct {
		name          string
		format        string
		target        string
		events        []string
		secret        *string
		requireSecret bool
		wantOK        bool
	}{
		{"other formats untouched", "json", "https://example.com/hook", []string{"app.parked"}, nil, true, true},
		{"supported endpoint and events", "datadog", us1Events, []string{"deployment.live", "rollout.aborted"}, key, true, true},
		{"empty filter is defaulted by the caller", "datadog", us1Events, nil, key, true, true},
		{"arbitrary target rejected", "datadog", "https://evil.example/api/v1/events", nil, key, true, false},
		{"logs intake is not an events endpoint", "datadog", "https://http-intake.logs.datadoghq.com/api/v2/logs", nil, key, true, false},
		{"unsupported event rejected", "datadog", us1Events, []string{"app.parked"}, key, true, false},
		{"blank key rejected", "datadog", us1Events, nil, strPtr("  "), true, false},
		{"missing key on create rejected", "datadog", us1Events, nil, nil, true, false},
		{"kept key on update allowed", "datadog", us1Events, nil, nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prob := validateDatadogAppWebhook(tc.format, tc.target, tc.events, tc.secret, tc.requireSecret)
			if (prob == nil) != tc.wantOK {
				t.Fatalf("problem = %v, want ok=%v", prob, tc.wantOK)
			}
		})
	}
}

func TestValidateUpdatedDatadogAppWebhook_SwitchingNeedsNewKey(t *testing.T) {
	datadog := state.AppWebhookDeliveryFormat(api.AppWebhookDeliveryFormatDatadog)
	target := us1Events
	jsonHook := state.AppWebhook{DeliveryFormat: "json", TargetURL: "https://example.com/hook"}

	if prob := validateUpdatedDatadogAppWebhook(jsonHook, state.UpdateAppWebhookParams{DeliveryFormat: &datadog, TargetURL: &target}, nil); prob == nil {
		t.Fatal("switching to datadog must require a new key: the old secret is an HMAC key")
	}
	if prob := validateUpdatedDatadogAppWebhook(jsonHook, state.UpdateAppWebhookParams{DeliveryFormat: &datadog, TargetURL: &target}, strPtr("dd-key")); prob != nil {
		t.Fatalf("switch with key failed: %v", prob)
	}
	ddHook := state.AppWebhook{DeliveryFormat: datadog, TargetURL: us1Events}
	other := "https://example.com/hook"
	if prob := validateUpdatedDatadogAppWebhook(ddHook, state.UpdateAppWebhookParams{TargetURL: &other}, nil); prob == nil {
		t.Fatal("a datadog webhook must not be repointed off Datadog")
	}
	if prob := validateAppWebhookDeliveryFormat("datadog"); prob != nil {
		t.Fatalf("app webhooks must accept datadog: %v", prob)
	}
	if prob := validateWebhookDeliveryFormat("datadog"); prob == nil {
		t.Fatal("the shared validator (account and platform-tenant webhooks) must keep rejecting datadog")
	}
}
