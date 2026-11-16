package main

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// ADR 20261027132936 amendment 1: the gauges FaasAIGatewayEnabledWithoutAppRelay
// reads follow the live flag and the relay listener.
func TestAIGatewayReadinessGauges(t *testing.T) {
	registry := prometheus.NewRegistry()
	enabled := false
	if err := registerAIGatewayReadiness(registry, func() bool { return enabled }, false); err != nil {
		t.Fatal(err)
	}
	read := func(name string) float64 {
		t.Helper()
		families, err := registry.Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, family := range families {
			if family.GetName() == name {
				return family.GetMetric()[0].GetGauge().GetValue()
			}
		}
		t.Fatalf("%s is not exported", name)
		return 0
	}
	if read("outbound_ai_gateway_enabled") != 0 || read("outbound_ai_app_relay_ready") != 0 {
		t.Fatal("gauges start at the flag and relay state")
	}
	enabled = true
	if read("outbound_ai_gateway_enabled") != 1 {
		t.Fatal("the enabled gauge does not follow the live flag")
	}
	if n := testutil.CollectAndCount(registry); n != 2 {
		t.Fatalf("collected %d series, want 2", n)
	}
}
