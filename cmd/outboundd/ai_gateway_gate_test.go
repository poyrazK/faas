package main

// adr: 991 — the AI gateway release gate is off unless enabled.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/outbound"
)

func TestAIGatewayGateDefaultsOffAndHonorsTheOverride(t *testing.T) {
	env := func(value string) func(string) string {
		return func(name string) string {
			if name == "FAAS_AI_GATEWAY_ENABLED" {
				return value
			}
			return ""
		}
	}
	for _, tc := range []struct {
		toml bool
		env  string
		want bool
	}{
		{false, "", false}, {true, "", true}, {false, "1", true}, {true, "0", false}, {true, "true", false},
	} {
		cfg := Config{AIGatewayEnabled: tc.toml}
		if got := cfg.AIGateway(env(tc.env)); got != tc.want {
			t.Errorf("toml=%v env=%q -> %v, want %v", tc.toml, tc.env, got, tc.want)
		}
	}
}

func TestValidateAIGatewayRefusesOperatorAIIntegrationsWhileOff(t *testing.T) {
	ai := configuredIntegration{Record: outbound.IntegrationRecord{Policy: outbound.Integration{ID: "llm", AIProvider: "openai"}}}
	plain := configuredIntegration{Record: outbound.IntegrationRecord{Policy: outbound.Integration{ID: "plain"}}}
	if err := ValidateAIGateway([]configuredIntegration{plain, ai}, false); err == nil {
		t.Fatal("an AI integration started with the gateway off")
	}
	if err := ValidateAIGateway([]configuredIntegration{plain, ai}, true); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAIGateway([]configuredIntegration{plain}, false); err != nil {
		t.Fatal(err)
	}
}
