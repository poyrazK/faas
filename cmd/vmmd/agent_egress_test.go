// adr: 20261117185346 §3 — vmmd's agent tunnel comes from the egress
// drop-in, and an incomplete one refuses to start.
package main

import (
	"testing"
	"time"
)

func TestAgentEgressConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cfg, err := agentEgressConfigFromEnv(env(map[string]string{"FAAS_AGENT_EGRESS_IFACE": "wg-agent", "FAAS_AGENT_EGRESS_SLICE": "10.78.8.0/22"}))
	if err != nil || cfg.Iface != "wg-agent" || cfg.Slice.String() != "10.78.8.0/22" || cfg.Table != 51821 || cfg.HandshakeMaxAge != 180*time.Second {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
	for _, bad := range []map[string]string{
		{},
		{"FAAS_AGENT_EGRESS_IFACE": "wg-agent"},
		{"FAAS_AGENT_EGRESS_SLICE": "10.78.8.0/22"},
		{"FAAS_AGENT_EGRESS_IFACE": "wg agent", "FAAS_AGENT_EGRESS_SLICE": "10.78.8.0/22"},
		{"FAAS_AGENT_EGRESS_IFACE": "wg-agent", "FAAS_AGENT_EGRESS_SLICE": "not-a-cidr"},
	} {
		if _, err := agentEgressConfigFromEnv(env(bad)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
