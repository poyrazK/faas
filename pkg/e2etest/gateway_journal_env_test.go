package e2etest

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewayDurableEnvSeparatesGatewayInstances(t *testing.T) {
	root := t.TempDir()
	seen := map[string]bool{}
	for _, process := range []string{"primary", "additional"} {
		t.Run(process, func(t *testing.T) {
			dir := filepath.Join(root, process)
			env := gatewaydDurableEnv(dir)
			if len(env) != 2 {
				t.Fatalf("durable environment has %d entries, want both journals", len(env))
			}
			want := map[string]string{
				"FAAS_GATEWAY_EGRESS_PENDING_PATH": filepath.Join(dir, "egress-meter", "pending.json"),
				"FAAS_CONSUMER_USAGE_OUTBOX_ROOT":  filepath.Join(dir, "consumer-usage"),
			}
			for _, entry := range env {
				name, value, ok := strings.Cut(entry, "=")
				if !ok || want[name] != value || value == "" {
					t.Fatalf("unexpected journal setting %q", entry)
				}
				if seen[value] {
					t.Fatalf("independent gateways share journal %q", value)
				}
				seen[value] = true
				delete(want, name)
			}
			if len(want) != 0 {
				t.Fatal("gateway omitted a durable journal")
			}
		})
	}
}
