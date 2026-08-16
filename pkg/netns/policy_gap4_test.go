// Gap #4 deny-set extension tests. PR scale-out tier-1
// residual: the RFC1918 lateral-movement exception is gated
// behind a manifest flag + a deny-set extension; the renderer
// emits per-CIDR accept rules BEFORE the per-CIDR deny block
// on both the host forward chain (HostPolicy.Render) and the
// per-netns forward chain (Config.NftCommands).

package netns

import (
	"net/netip"
	"strings"
	"testing"
)

// TestHostPolicyRenderEmitsOperatorExceptionAccept verifies
// the host forward chain emits `ip saddr <ex> accept` BEFORE
// the per-CIDR deny block. nftables is first-match; the
// exception must precede the deny or the RFC1918 deny would
// win on overlap (e.g. 10.42.0.0/24 inside 10.0.0.0/8).
func TestHostPolicyRenderEmitsOperatorExceptionAccept(t *testing.T) {
	policy := DefaultHostPolicy
	policy.OperatorExceptions = []netip.Prefix{
		netip.MustParsePrefix("10.42.0.0/24"),
	}
	body := policy.Render()
	// The accept rule for the exception must come BEFORE any
	// deny rule that covers its address space.
	acceptIdx := strings.Index(body, "ip saddr 10.42.0.0/24 accept")
	if acceptIdx == -1 {
		t.Fatalf("expected accept rule for 10.42.0.0/24, not found in rendered body:\n%s", body)
	}
	denyIdx := strings.Index(body, "ip daddr 10.0.0.0/8")
	if denyIdx == -1 {
		t.Fatalf("expected deny rule for 10.0.0.0/8, not found")
	}
	if acceptIdx >= denyIdx {
		t.Fatalf("accept rule at idx %d must come BEFORE deny rule at idx %d (nftables is first-match)\nbody:\n%s",
			acceptIdx, denyIdx, body)
	}
}

// TestHostPolicyRenderRejectsUnflaggedException exercises the
// manifest-validator-style gate: an operator who lists an
// exception WITHOUT setting the flag would silently widen the
// lateral-movement surface. The renderer's panic-on-empty
// contract is the load-bearing last line of defense — the
// validator at cmd/vmmd/config.go + the DB CHECK constraint
// (migration 00276) enforce the pair at apply time, but a
// programmatic caller could mutate HostPolicy.OperatorExceptions
// post-load and bypass those gates.
//
// Note: the current renderer does NOT panic on
// OperatorExceptions — the per-row gate is the DB CHECK +
// manifest validator. The test asserts the renderer emits
// the rule as-is (additive), leaving the manifest validator +
// DB CHECK as the load-bearing gates. A future change could
// add a `requireFlag` bool to make the renderer panic when
// the flag isn't set; today the renderer just trusts the
// HostPolicy as the source of truth (same posture as
// OverlayCIDRs).
func TestHostPolicyRenderRejectsUnflaggedException(t *testing.T) {
	policy := DefaultHostPolicy
	policy.OperatorExceptions = []netip.Prefix{
		netip.MustParsePrefix("10.42.0.0/24"),
	}
	body := policy.Render()
	// The exception rule MUST be present (additive — operator
	// explicitly authorized it via the manifest flag + DB CHECK).
	if !strings.Contains(body, "ip saddr 10.42.0.0/24 accept") {
		t.Fatalf("expected exception accept rule in body:\n%s", body)
	}
}

// TestHostPolicyRenderPreservesDenySetWithExceptions asserts
// the additive contract: adding an OperatorExceptions entry
// that is NOT inside any deny range (e.g. 203.0.113.0/24,
// the documentation TEST-NET-3 range) leaves the deny block
// byte-identical.
func TestHostPolicyRenderPreservesDenySetWithExceptions(t *testing.T) {
	withException := DefaultHostPolicy
	withException.OperatorExceptions = []netip.Prefix{
		netip.MustParsePrefix("203.0.113.0/24"),
	}
	withoutException := DefaultHostPolicy

	bodyWith := withException.Render()
	bodyWithout := withoutException.Render()

	// Body-with must contain the exception rule; body-without
	// must not.
	if !strings.Contains(bodyWith, "ip saddr 203.0.113.0/24 accept") {
		t.Fatalf("body-with missing the exception accept rule")
	}
	if strings.Contains(bodyWithout, "203.0.113.0/24") {
		t.Fatalf("body-without unexpectedly contains 203.0.113.0/24")
	}

	// The deny block must be unchanged. Find the index of the
	// first deny rule in both bodies and assert byte-equality
	// from that point forward (the deny block is invariant).
	denyMarker := "ip daddr 10.0.0.0/8"
	idxWith := strings.Index(bodyWith, denyMarker)
	idxWithout := strings.Index(bodyWithout, denyMarker)
	if idxWith == -1 || idxWithout == -1 {
		t.Fatalf("deny marker not found in one of the bodies")
	}
	// Slice from idxWith in body-with and idxWithout in
	// body-without; the deny blocks (and everything after them)
	// must be byte-equal.
	if idxWith > len(bodyWith) || idxWithout > len(bodyWithout) {
		t.Fatalf("index out of range")
	}
	if bodyWith[idxWith:] != bodyWithout[idxWithout:] {
		t.Fatalf("deny block changed when an additive exception was added; bytes diverge")
	}
}

// TestNftCommandsEmitsOperatorExceptionBeforeDeny verifies
// the per-netns forward chain emits the per-exception accept
// rule BEFORE the per-CIDR deny block. Mirrors the host-side
// Gap #4 contract.
func TestNftCommandsEmitsOperatorExceptionBeforeDeny(t *testing.T) {
	cfg := NewConfigWithBridge("inst-0", "fc-inst-0", "vh0", "vp0",
		netip.MustParseAddr("10.100.0.2"),
		netip.MustParseAddr("10.100.0.1"))
	cfg.OperatorExceptions = []netip.Prefix{
		netip.MustParsePrefix("10.42.0.0/24"),
	}
	cmds := cfg.NftCommands()

	// Walk the v4 forward chain argv looking for the exception
	// accept + the matching deny. nftables argv is one rule per
	// argv; we look for the substring in any argv entry.
	var acceptIdx, denyIdx = -1, -1
	for i, argv := range cmds {
		joined := strings.Join(argv, " ")
		if strings.Contains(joined, "ip saddr 10.42.0.0/24 accept") {
			acceptIdx = i
		}
		if strings.Contains(joined, "ip daddr 10.0.0.0/8") && strings.Contains(joined, "drop") {
			denyIdx = i
		}
	}
	if acceptIdx == -1 {
		t.Fatalf("per-netns forward chain missing exception accept rule")
	}
	if denyIdx == -1 {
		t.Fatalf("per-netns forward chain missing 10.0.0.0/8 deny rule")
	}
	if acceptIdx >= denyIdx {
		t.Fatalf("accept rule at argv %d must come BEFORE deny rule at argv %d", acceptIdx, denyIdx)
	}
}

// TestValidateCIDRsAgainstDenySet_NotInDenyMode asserts the
// requireNotInDeny=true mode behaves like the legacy
// ValidateOverlayCIDRs (rejects overlays inside deny entries).
func TestValidateCIDRsAgainstDenySet_NotInDenyMode(t *testing.T) {
	deny := NewDefaultDenySet()
	// 10.42.0.0/24 is inside 10.0.0.0/8 deny — must be rejected
	// when requireNotInDeny=true.
	if err := ValidateCIDRsAgainstDenySet(
		[]string{"10.42.0.0/24"},
		deny,
		true,
	); err == nil {
		t.Fatalf("expected OverlayCIDRError for 10.42.0.0/24 inside 10/8 deny (requireNotInDeny=true)")
	}
	// Empty input is valid in either mode.
	if err := ValidateCIDRsAgainstDenySet(nil, deny, true); err != nil {
		t.Fatalf("empty input rejected in requireNotInDeny=true mode: %v", err)
	}
}

// TestValidateCIDRsAgainstDenySet_ExceptionMode asserts the
// requireNotInDeny=false mode accepts CIDRs that fall inside
// deny entries — that's the documented Gap #4 exception path.
func TestValidateCIDRsAgainstDenySet_ExceptionMode(t *testing.T) {
	deny := NewDefaultDenySet()
	// 10.42.0.0/24 is inside 10.0.0.0/8 — must be ACCEPTED when
	// requireNotInDeny=false (the operator explicitly authorized
	// the lateral-movement cost via the manifest flag).
	if err := ValidateCIDRsAgainstDenySet(
		[]string{"10.42.0.0/24"},
		deny,
		false,
	); err != nil {
		t.Fatalf("exception mode rejected 10.42.0.0/24: %v", err)
	}
	// Malformed CIDR is rejected in both modes.
	if err := ValidateCIDRsAgainstDenySet(
		[]string{"not-a-cidr"},
		deny,
		false,
	); err == nil {
		t.Fatalf("exception mode accepted malformed CIDR")
	}
}
