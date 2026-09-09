// Metal sibling of bridge_h2c_terminator_e2e_test.go. The non-metal
// tests above already exercise the real bridge binary end-to-end
// against a local H2C guest listener; the metal tests below do
// the same wiring but the "guest" is a real Firecracker VM booted
// on the bare-metal x86_64 control-plane node (or on M3+ Apple
// Silicon via `make metal-lima` — see CLAUDE.md + deploy/lima/).
//
// Build tag: //go:build metal — gated by the same convention as
// the rest of the metal suite. Only runs in `make test-metal` /
// `make metal-lima`.
//
// What the metal suite adds on top of the non-metal test:
//   - real Firecracker microVM as the "guest" (10.0.0.2:8080 is
//     an actual VM-bound interface, not a loopback listener);
//   - real vmmd-stream-bridge process spawned inside the VM's
//     per-instance netns via `ip netns exec <netns> bridge …`;
//   - real guest-init + runner stack (the H2C-capable :8080
//     listener from guest/runners/internal/h2c_listener.go).
//
// Acceptance gates per spec §14 (M8 row 5 — wire-protocol
// selector PR-D cutover):
//
//  1. App with app_protocol=http1 → HTTP/1.1 request reaches
//     the guest (curl -v shows H1 framing on the wire; the
//     guest's net/http logs the H1 path).
//
//  2. App with app_protocol=http2 → HTTP/2 prior-knowledge
//     request reaches the guest (curl --http2-prior-knowledge
//     works; the guest's listener is H2C-capable per
//     guest/runners/internal/h2c_listener.go).
//
//  3. App with app_protocol=grpc → a real Go gRPC client hits
//     a unary + a server-streaming RPC; trailer pair
//     (grpc-status: 0, grpc-message: "") survives end-to-end.
//
//  4. Surgical rollback: setting FAAS_BRIDGE_PROTOCOL=h1 on
//     vmmd → the http2/grpc apps fall back to the legacy
//     H1+chunked path on the wire. The customer-facing behavior
//     reverts to today's shape; the bridge keeps serving.
//
//  5. Wholesale rollback: setting FAAS_STREAM_BRIDGE_VERSION=v1
//     on vmmd → the v1 shell bridge takes over entirely. This is
//     the disaster rollback (pre-existing ADR-028 amendment).
//
// The operator-owned G19.3 harness provisions the fixtures and runs these
// gates (per spec §14 — "A bare-metal x86_64 control-plane node remains the
// source of truth for the §14 metal acceptance gates"). These Go tests invoke
// that harness when a metal runner is configured; the non-metal
// bridge_h2c_terminator_e2e_test.go tests remain the CI-safe bridge pins.
//
//go:build metal

package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const defaultMetalH2CFixtureEnv = "/etc/faas/metal-h2c-acceptance/fixture-apps.env"

// runMetalH2CAcceptanceGate delegates the real-VM assertions to the G19.3
// harness installed by deploy/ansible/roles/metal-h2c-acceptance. Keeping the
// fixture provisioning and rollback commands in that operator-owned harness
// avoids inventing credentials or service-mutation logic in a Go test. The
// FAAS_H2C_ACCEPTANCE_HARNESS override lets a metal runner use its installed
// copy; the repository copy is useful for an explicit local acceptance run.
func runMetalH2CAcceptanceGate(t *testing.T, gate string, fixtures ...string) {
	t.Helper()
	root := repoRoot()
	if root == "" {
		t.Skip("module root not reachable — run from the repo root or fix cwd")
	}

	harness := os.Getenv("FAAS_H2C_ACCEPTANCE_HARNESS")
	if harness == "" {
		candidates := []string{
			"/opt/faas/metal-h2c-acceptance/metal-acceptance.sh",
			filepath.Join(root, "deploy", "ansible", "roles", "metal-h2c-acceptance", "files", "metal-acceptance.sh"),
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				harness = candidate
				break
			}
		}
	}
	if harness == "" {
		t.Skip("G19.3 metal H2C harness is not installed; set FAAS_H2C_ACCEPTANCE_HARNESS")
	}
	if _, err := os.Stat(harness); err != nil {
		t.Skipf("G19.3 metal H2C harness is unavailable at %s: %v", harness, err)
	}

	fixtureEnv := os.Getenv("FAAS_H2C_FIXTURE_ENV")
	if fixtureEnv == "" {
		fixtureEnv = defaultMetalH2CFixtureEnv
	}
	if _, err := os.Stat(fixtureEnv); err != nil {
		t.Skipf("G19.3 fixture environment is unavailable at %s: %v", fixtureEnv, err)
	}

	args := append([]string{harness, gate}, fixtures...)
	cmd := exec.Command("bash", args...)
	cmd.Env = append(os.Environ(), "FAAS_H2C_FIXTURE_ENV="+fixtureEnv)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("metal H2C gate %s failed: %v", gate, err)
	}
}

// TestMetal_AppProtocolH2CPriorKnowledge is the M8 row 5 metal
// acceptance gate: a real Firecracker guest running the
// H2C-capable runner listener receives an H2 prior-knowledge
// request end-to-end. The fixture (deployed app, running VM,
// bridged netns) is set up by the operator's G19.3 metal harness; this Go
// test invokes its H2 framing gate.
func TestMetal_AppProtocolH2CPriorKnowledge(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	runMetalH2CAcceptanceGate(t, "h2c-prior-knowledge", "app_http2_prior_knowledge")
}

// TestMetal_AppProtocolGRPCTrailers is the M8 row 5 gRPC
// trailer preservation gate: a real Go gRPC client hits Echo
// (unary) and ServerStreamingEcho; the trailer pair must
// round-trip end-to-end through the H2C terminator.
func TestMetal_AppProtocolGRPCTrailers(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	runMetalH2CAcceptanceGate(t, "grpc-trailers", "app_grpc_unary", "app_grpc_server_streaming")
}

// TestMetal_AppProtocolH1Default is the regression: app with
// app_protocol=http1 continues to receive H1 framing on the wire.
func TestMetal_AppProtocolH1Default(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	runMetalH2CAcceptanceGate(t, "http1", "app_http1_default")
}

// TestMetal_BridgeSurgicalRollback asserts
// FAAS_BRIDGE_PROTOCOL=h1 forces the legacy path for any
// app_protocol (the surgical rollback switch per ADR-126
// §Decision 7).
func TestMetal_BridgeSurgicalRollback(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	runMetalH2CAcceptanceGate(t, "surgical-rollback", "app_surgical_rollback_target")
}

// TestMetal_BridgeWholesaleRollback asserts
// FAAS_STREAM_BRIDGE_VERSION=v1 reverts to the v1 shell bridge
// (pre-existing ADR-028 amendment disaster rollback).
func TestMetal_BridgeWholesaleRollback(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	runMetalH2CAcceptanceGate(t, "wholesale-rollback", "app_surgical_rollback_target")
}
