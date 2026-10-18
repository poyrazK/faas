package e2etest

import (
	"path/filepath"
	"strings"
	"testing"
)

// gatewayd-internal creates its durable queue roots before it binds a
// listener (ADR-1580 per-generation slots). The production defaults under
// /var/lib/faas cannot be created by an unprivileged CI runner, so a root the
// harness leaves at its default keeps every gateway from ever serving: nightly
// e2e shard 2 timed out on the control listener while the egress ledger root
// could not be moved.
func TestGatewayStateEnvKeepsDurableRootsInTheTestDir(t *testing.T) {
	dir := t.TempDir()
	env := gatewayStateEnv(dir)
	seen := map[string]string{}
	for _, name := range []string{
		"FAAS_CONSUMER_USAGE_OUTBOX_ROOT",
		"FAAS_GATEWAY_EGRESS_LEDGER_ROOT",
		"FAAS_GATEWAY_WAKE_AHEAD_STATE_DIR",
	} {
		got, ok := envValue(t, env, name)
		if !ok || !strings.HasPrefix(got, dir+string(filepath.Separator)) {
			t.Fatalf("%s=%q present=%v, want a directory under %s", name, got, ok, dir)
		}
		if other, dup := seen[got]; dup {
			t.Fatalf("%s and %s share %s", name, other, got)
		}
		seen[got] = name
	}
}
