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

// adr: 1700 — the gate is a live platform flag, so outboundd reports
// operator AI integrations configured while it is off instead of refusing to
// start.
func TestAIIntegrationsWhileGatewayOffAreReported(t *testing.T) {
	ai := configuredIntegration{Record: outbound.IntegrationRecord{Policy: outbound.Integration{ID: "llm", AIProvider: "openai"}}}
	plain := configuredIntegration{Record: outbound.IntegrationRecord{Policy: outbound.Integration{ID: "plain"}}}
	if got := AIIntegrationsWhileGatewayOff([]configuredIntegration{plain, ai}, false); len(got) != 1 || got[0] != "llm" {
		t.Fatalf("reported = %v, want [llm]", got)
	}
	if got := AIIntegrationsWhileGatewayOff([]configuredIntegration{plain, ai}, true); got != nil {
		t.Fatalf("reported %v with the gateway on", got)
	}
	if got := AIIntegrationsWhileGatewayOff([]configuredIntegration{plain}, false); got != nil {
		t.Fatalf("reported %v for a plain integration", got)
	}
}

// adr: 998 — the staged rollout list comes from TOML, and
// FAAS_AI_GATEWAY_ACCOUNTS replaces it.
func TestAIGatewayRolloutConfig(t *testing.T) {
	listed, override := "6f2c1d3e-1a2b-4c5d-8e9f-0a1b2c3d4e5f", "9a8b7c6d-5e4f-4a3b-9c2d-1e0f2a3b4c5d"
	cfg := Config{AIGatewayAccounts: []string{listed}}
	rollout, err := cfg.AIGatewayRollout(func(string) string { return "" })
	if err != nil || !rollout.Allows(listed) || rollout.Allows(override) {
		t.Fatalf("TOML rollout = %v, %v", rollout, err)
	}
	rollout, err = cfg.AIGatewayRollout(func(name string) string {
		if name == "FAAS_AI_GATEWAY_ACCOUNTS" {
			return override
		}
		return ""
	})
	if err != nil || rollout.Allows(listed) || !rollout.Allows(override) {
		t.Fatalf("overridden rollout = %v, %v", rollout, err)
	}
	if rollout, err := (&Config{}).AIGatewayRollout(nil); err != nil || rollout != nil {
		t.Fatalf("an unset rollout = %v, %v", rollout, err)
	}
	if _, err := (&Config{AIGatewayAccounts: []string{"acme"}}).AIGatewayRollout(nil); err == nil {
		t.Fatal("a rollout entry that is not a UUID was accepted")
	}
}
