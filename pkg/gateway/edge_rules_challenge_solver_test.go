package gateway

// adr: 831 — amendment 7: the browser's solver must agree with the
// gateway's check, or no browser could ever earn a pass. Amendment 15: the
// solver hashes the token's whole blocks once, so it must still find the
// same counter as hashing each attempt from scratch.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type solverCase struct {
	Token      string `json:"t"`
	Difficulty int    `json:"d"`
	Start      int    `json:"s"`
}

// TestChallengeSolverJS runs the page's solver in Node. Its SHA-256 must
// match Go's on inputs of every block shape; a counter it finds for a
// production-length token must pass challengeWorkValid; and solve must
// return the first valid counter from its start, for token lengths that put
// the counter at every offset of the last block (one tail block or two) and
// starts where the counter gains a digit or a page chunk begins. Skipped
// where Node is not installed.
func TestChallengeSolverJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	var inputs []string
	for _, n := range []int{0, 3, 55, 56, 63, 64, 65, 119, 120, 128, 150} {
		inputs = append(inputs, strings.Repeat(string(rune('a'+n%26)), n))
	}
	token := "c1." + strings.Repeat("eyJhIjoiYXBwLTEifQ", 11) + ".c2lnbmF0dXJlYWJjZGVmZ2g"
	var cases []solverCase
	for n := 1; n <= 140; n++ {
		for _, start := range []int{0, 95, 9995, 20000, 99995} {
			cases = append(cases, solverCase{Token: strings.Repeat("c1.t", 35)[:n], Difficulty: 6, Start: start})
		}
	}
	in, err := json.Marshal(map[string]any{"inputs": inputs, "token": token, "cases": cases})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	harness := challengeSolverJS + `
var input = JSON.parse(require("fs").readFileSync(process.argv[2], "utf8")), out = [];
input.inputs.forEach(function (s) { out.push(sha256Word0(s)); });
out.push(solve(input.token, 12, 0, 10000000));
input.cases.forEach(function (c) { out.push(solve(c.t, c.d, c.s, 1000000)); });
process.stdout.write(out.join("\n") + "\n");
`
	script, data := filepath.Join(dir, "solver.cjs"), filepath.Join(dir, "input.json")
	if err := os.WriteFile(script, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(data, in, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, script, data).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	lines := strings.Fields(string(out))
	if len(lines) != len(inputs)+1+len(cases) {
		t.Fatalf("node printed %d lines, want %d", len(lines), len(inputs)+1+len(cases))
	}
	for i, in := range inputs {
		sum := sha256.Sum256([]byte(in))
		if want := strconv.FormatUint(uint64(binary.BigEndian.Uint32(sum[:4])), 10); lines[i] != want {
			t.Errorf("sha256Word0(%d bytes) = %s, want %s", len(in), lines[i], want)
		}
	}
	if counter := lines[len(inputs)]; !challengeWorkValid(token, counter, 12) {
		t.Errorf("counter %s from the browser solver fails the gateway check", counter)
	}
	for i, c := range cases {
		want := c.Start
		for !challengeWorkValid(c.Token, strconv.Itoa(want), c.Difficulty) {
			want++
		}
		if got := lines[len(inputs)+1+i]; got != strconv.Itoa(want) {
			t.Errorf("solve(%d-byte token, %d, from %d) = %s, want %d", len(c.Token), c.Difficulty, c.Start, got, want)
		}
	}
}
