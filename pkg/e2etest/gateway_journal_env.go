package e2etest

import "path/filepath"

// gatewaydDurableEnv gives each gateway instance private journal roots. Its
// replacement generations inherit the same roots and claim separate slots.
// Independent fixtures must not claim production or another gateway's records.
func gatewaydDurableEnv(dir string) []string {
	return []string{
		"FAAS_GATEWAY_EGRESS_PENDING_PATH=" + filepath.Join(dir, "egress-meter", "pending.json"),
		"FAAS_CONSUMER_USAGE_OUTBOX_ROOT=" + filepath.Join(dir, "consumer-usage"),
	}
}
