package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const us1Intake = "https://http-intake.logs.datadoghq.com/api/v2/logs"

func TestValidateDatadogAppLogDrain(t *testing.T) {
	for _, tc := range []struct {
		name          string
		kind, target  string
		header        *string
		requireHeader bool
		wantOK        bool
	}{
		{"other kinds are untouched", "http_json", "https://example.com", nil, true, true},
		{"supported intake with key", "datadog", us1Intake, strPtr("DD-API-KEY: abc"), true, true},
		{"header name is case-insensitive", "datadog", us1Intake, strPtr("dd-api-key: abc"), true, true},
		{"arbitrary target rejected", "datadog", "https://evil.example/api/v2/logs", strPtr("DD-API-KEY: abc"), true, false},
		{"lookalike host rejected", "datadog", "https://http-intake.logs.datadoghq.com.evil.example/api/v2/logs", strPtr("DD-API-KEY: abc"), true, false},
		{"wrong header name rejected", "datadog", us1Intake, strPtr("Authorization: Bearer abc"), true, false},
		{"empty key rejected", "datadog", us1Intake, strPtr("DD-API-KEY:  "), true, false},
		{"missing header on create rejected", "datadog", us1Intake, nil, true, false},
		{"unchanged header on update allowed", "datadog", us1Intake, nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prob := validateDatadogAppLogDrain(tc.kind, tc.target, tc.header, tc.requireHeader)
			if (prob == nil) != tc.wantOK {
				t.Fatalf("problem = %v, want ok=%v", prob, tc.wantOK)
			}
			if prob != nil && prob.Code != api.CodeAppLogDrainInvalid {
				t.Fatalf("code = %s, want %s", prob.Code, api.CodeAppLogDrainInvalid)
			}
		})
	}
}

func TestValidateUpdatedDatadogAppLogDrain(t *testing.T) {
	datadogKind := state.AppLogDrainKindDatadog
	otherTarget := "https://example.com/logs"

	// Switching an http_json drain to datadog must supply a key.
	httpDrain := state.AppLogDrain{Kind: state.AppLogDrainKindHTTPJSON, TargetURL: us1Intake}
	if prob := validateUpdatedDatadogAppLogDrain(httpDrain, state.UpdateAppLogDrainParams{Kind: &datadogKind}, nil); prob == nil {
		t.Fatal("switching to datadog without a key must fail")
	}
	// An existing datadog drain cannot be repointed at an arbitrary host.
	ddDrain := state.AppLogDrain{Kind: state.AppLogDrainKindDatadog, TargetURL: us1Intake}
	if prob := validateUpdatedDatadogAppLogDrain(ddDrain, state.UpdateAppLogDrainParams{TargetURL: &otherTarget}, nil); prob == nil {
		t.Fatal("repointing a datadog drain off-intake must fail")
	}
	// Toggling an existing datadog drain keeps its sealed key.
	if prob := validateUpdatedDatadogAppLogDrain(ddDrain, state.UpdateAppLogDrainParams{}, nil); prob != nil {
		t.Fatalf("no-op update failed: %v", prob)
	}
}
