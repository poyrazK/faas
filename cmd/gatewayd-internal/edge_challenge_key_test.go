package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gatewayd-internal holds no session key (ADR-20261102191500): edge
// challenges and traffic deadlines are keyed by the gateway fleet key, and
// a gateway without it refuses to start rather than sign with a per-host key.
func TestLoadGatewayFleetKey(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	env := func(vars map[string]string) func(string) string {
		return func(key string) string { return vars[key] }
	}
	fleetHex := strings.Repeat("ab", 32)
	want := bytes.Repeat([]byte{0xab}, 32)

	if _, err := loadGatewayFleetKey(env(nil)); err == nil || !strings.Contains(err.Error(), gatewayFleetKeyEnv) {
		t.Fatalf("no key file = %v; want a refusal naming %s", err, gatewayFleetKeyEnv)
	}
	path := write("fleet.key", fleetHex+"\n")
	for name, vars := range map[string]map[string]string{
		"fleet key file":       {gatewayFleetKeyEnv: path},
		"legacy edge key file": {gatewayFleetKeyLegacyEnv: path},
		"fleet key wins":       {gatewayFleetKeyEnv: path, gatewayFleetKeyLegacyEnv: write("other.key", strings.Repeat("cd", 32))},
	} {
		got, err := loadGatewayFleetKey(env(vars))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s = %x, %v; want %x", name, got, err, want)
		}
	}
	for name, content := range map[string]string{"short": "abcd", "not hex": strings.Repeat("zz", 32)} {
		if _, err := loadGatewayFleetKey(env(map[string]string{gatewayFleetKeyEnv: write(name, content)})); err == nil {
			t.Fatalf("%s key file accepted", name)
		}
	}
	if _, err := loadGatewayFleetKey(env(map[string]string{gatewayFleetKeyEnv: filepath.Join(dir, "missing")})); err == nil {
		t.Fatal("missing key file accepted")
	}
	if signer, err := newTrafficDeadlineSigner(want); err != nil || signer == nil {
		t.Fatalf("deadline signer = %v, %v", signer, err)
	}
}
