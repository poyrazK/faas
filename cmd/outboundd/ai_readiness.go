package main

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// registerAIGatewayReadiness exports whether the AI gateway is on and whether
// deployed apps can reach it. Apps call the gateway through the app relay
// (ADR-989), which needs relay_listen and the workload-identity JWKS; with
// the ai_gateway flag on and the relay off, no app can call it, and
// FaasAIGatewayEnabledWithoutAppRelay fires (ADR 20261027132936 amendment 1).
func registerAIGatewayReadiness(registry prometheus.Registerer, enabled func() bool, relayReady bool) error {
	gauges := []prometheus.Collector{
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "outbound_ai_gateway_enabled",
			Help: "1 while the ai_gateway platform flag is globally on.",
		}, func() float64 { return boolGauge(enabled()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "outbound_ai_app_relay_ready",
			Help: "1 when this outboundd serves the app relay that deployed apps use to reach the AI gateway.",
		}, func() float64 { return boolGauge(relayReady) }),
	}
	for _, gauge := range gauges {
		if err := registry.Register(gauge); err != nil {
			return fmt.Errorf("outboundd: register AI gateway readiness: %w", err)
		}
	}
	return nil
}

func boolGauge(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
