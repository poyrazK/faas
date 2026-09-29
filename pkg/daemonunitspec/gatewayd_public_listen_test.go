package daemonunitspec

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// gatewayd-public runs with FAAS_NODE_NAME on every managed node (the
// node-name drop-in), which puts it in ADR-126's multi-host posture: an
// implicit loopback listen default is refused and the service never starts.
// The socket unit owns the real bind, so the service must declare that same
// address explicitly. A fresh split-box control plane (production-us) could
// not start gatewayd-public without it.
func TestGatewaydPublicDeclaresTheSocketListenAddress(t *testing.T) {
	var listen string
	for _, kv := range UnitGatewaydPublic().Environment {
		if kv.Key == "FAAS_PUBLIC_LISTEN_ADDR" {
			listen = kv.Value
		}
	}
	if listen == "" {
		t.Fatal("faas-gatewayd-public does not declare FAAS_PUBLIC_LISTEN_ADDR; ADR-126's multi-host check refuses to start it")
	}
	socket, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "gatewayd_public_service", "files", "faas-gatewayd-public.socket"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^ListenStream=(\S+)$`).FindStringSubmatch(string(socket))
	if m == nil {
		t.Fatal("faas-gatewayd-public.socket has no ListenStream")
	}
	if listen != m[1] {
		t.Fatalf("FAAS_PUBLIC_LISTEN_ADDR=%s, but the socket binds %s", listen, m[1])
	}
}
