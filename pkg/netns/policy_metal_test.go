//go:build metal

// Metal integration tests for the host nftables policy renderer.
//
// Runs on the dev EX44 or Lima arm64 guest via `make test-metal` or
// `make metal-lima`. Skips gracefully when `nft` is not on PATH (the
// common macOS dev case) or the local nftables version predates 1.0
// (older nft silently ignored `-c` with `-f` and applied the ruleset
// instead of checking, so the test would give a false pass).
package netns

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMetalHostPolicySyntaxChecks pipes DefaultHostPolicy.Render() through
// `nft -c -f` against the live kernel. Exit 0 + empty stderr means the
// ruleset is real nftables syntax (no Go-side substitution typo can
// survive this).
//
// The tested-on-the-live-kernel half of "does the host egress policy work"
// lives in the M5 e2e test (issue #28) — there a guest VM actively attempts
// SMTP / RFC1918 / link-local egress and we assert the deny. This test is
// only the syntax gate.
func TestMetalHostPolicySyntaxChecks(t *testing.T) {
	nft, err := exec.LookPath("nft")
	if err != nil {
		t.Skip("nft not on PATH; install nftables (apt install nftables) on a Linux host to run this gate")
	}
	if !nftVersionOK(t, nft) {
		t.Skip("nft --version reports a pre-1.0 nftables; `-c` semantics are unreliable, skipping")
	}

	dir := t.TempDir()
	path := dir + "/policy_nftables.conf"
	if err := os.WriteFile(path, []byte(DefaultHostPolicy.Render()), 0o644); err != nil {
		t.Fatalf("write render: %v", err)
	}

	cmd := exec.Command(nft, "-c", "-f", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nft -c -f failed: %v\noutput:\n%s\nfile:\n%s",
			err, out, DefaultHostPolicy.Render())
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		t.Fatalf("nft -c -f printed output on success: %q", msg)
	}
}

// TestMetalBridgeNameMatches is a sanity test that confirms the runtime
// bridge name actually exists on the live host. The bridge is created by
// the control-plane daemons (vmmd on EX44, ansible on first bootstrap)
// and is what `iif "br-tenants"` in the ruleset refers to. If the bridge
// is missing, the forward-chain allow rule will silently never match
// (the bug this whole PR is designed to prevent regressing).
//
// Skips when /sys/class/net isn't readable (chroots, sandboxes).
func TestMetalBridgeNameMatches(t *testing.T) {
	const sysNet = "/sys/class/net/" + TenantBridge
	if _, err := os.Stat(sysNet); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("bridge %q not present on this host (run `make bootstrap` first); %v", TenantBridge, err)
		}
		t.Fatalf("stat %s: %v", sysNet, err)
	}
}

// nftVersionOK returns true iff `nft --version` parses and starts with
// `nftables v1.` (or higher). Pre-1.0 nftables silently treated `-c -f`
// as a load, not a check, so the syntax test would lie.
func nftVersionOK(t *testing.T, nft string) bool {
	t.Helper()
	out, err := exec.Command(nft, "--version").CombinedOutput()
	if err != nil {
		t.Logf("nft --version: %v (skipping)", err)
		return false
	}
	// Output is `nftables v1.0.x (eLxr ...)` on the apt package.
	line := strings.SplitN(string(out), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 2 {
		t.Logf("unexpected nft --version output: %q", line)
		return false
	}
	v := strings.TrimPrefix(fields[1], "v")
	if v == fields[1] {
		// No "v" prefix; try the whole second field as a version.
		v = fields[1]
	}
	dotIdx := strings.Index(v, ".")
	if dotIdx < 0 {
		t.Logf("couldn't find major version in %q", line)
		return false
	}
	major := v[:dotIdx]
	// nftables 1.0+ is the supported gate. 0.x and unknown fail.
	return major >= "1"
}
