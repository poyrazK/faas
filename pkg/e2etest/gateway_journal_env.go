package e2etest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gatewaydDurableEnv gives each gateway instance private journal roots. Its
// replacement generations inherit the same roots and claim separate slots.
// Independent fixtures must not claim production or another gateway's records.
func gatewaydDurableEnv(dir string) []string {
	return []string{
		"FAAS_GATEWAY_EGRESS_PENDING_PATH=" + filepath.Join(dir, "egress-meter", "pending.json"),
		"FAAS_CONSUMER_USAGE_OUTBOX_ROOT=" + filepath.Join(dir, "consumer-usage"),
	}
}

// gatewaydFleetKeyEnv points every gateway of a harness at one gateway fleet
// key (ADR-20261102191500): gatewayd-internal refuses to start without it,
// and gateways check the edge challenges and deadlines others sign.
func gatewaydFleetKeyEnv(t testing.TB, root string) string {
	t.Helper()
	path := filepath.Join(root, "gateway-fleet.key")
	if _, err := os.Stat(path); err != nil {
		if err := os.WriteFile(path, []byte(strings.Repeat("5e", 32)+"\n"), 0o600); err != nil {
			t.Fatalf("write gateway fleet key: %v", err)
		}
	}
	return "FAAS_GATEWAY_FLEET_KEY_FILE=" + path
}
