package gateway

// adr: 831 — amendment 7: the browser's solver must agree with the
// gateway's check, or no browser could ever earn a pass.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestChallengeSolverJS runs the page's solver in Node: its SHA-256 must
// match Go's on one- and multi-block inputs, and a counter it finds must
// pass challengeWorkValid. Skipped where Node is not installed.
func TestChallengeSolverJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	inputs := []string{"abc", strings.Repeat("x", 55), strings.Repeat("y", 56), strings.Repeat("z", 150)}
	token := "c1.eyJhIjoiYXBwLTEifQ.c2lnbmF0dXJl"
	harness := challengeSolverJS + `
var inputs = JSON.parse(process.argv[2]);
for (var i = 0; i < inputs.length; i++) process.stdout.write(sha256Word0(inputs[i]) + "\n");
process.stdout.write(solve(process.argv[3], 12, 0, 10000000) + "\n");
`
	path := filepath.Join(t.TempDir(), "solver.js")
	if err := os.WriteFile(path, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	encoded := `["` + strings.Join(inputs, `","`) + `"]`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, path, encoded, token).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	lines := strings.Fields(string(out))
	if len(lines) != len(inputs)+1 {
		t.Fatalf("node output: %q", out)
	}
	for i, in := range inputs {
		sum := sha256.Sum256([]byte(in))
		if want := strconv.FormatUint(uint64(binary.BigEndian.Uint32(sum[:4])), 10); lines[i] != want {
			t.Errorf("sha256Word0(%d bytes) = %s, want %s", len(in), lines[i], want)
		}
	}
	counter := lines[len(inputs)]
	if !challengeWorkValid(token, counter, 12) {
		t.Errorf("counter %s from the browser solver fails the gateway check", counter)
	}
}
