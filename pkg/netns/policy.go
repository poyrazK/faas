// Package netns — host-side firewall rules for tenant egress.
//
// Source of truth for /etc/nftables.conf on the host. The Go render in this
// file is what `make egress-render` writes into the checked-in artifact at
// `deploy/ansible/roles/nftables/files/policy_nftables.conf`; ansible copies
// that artifact onto the host at `make bootstrap` time.
//
// Why Go rather than an ansible `content:` blob: the rendered text is the
// security contract (spec §11 + CLAUDE.md ship-blocker). It needs a regression
// net — `go test ./pkg/netns` runs on every dev box, CI runner, and Lima
// metal guest — and Go-side rendering lets us assert against the literal
// `ip daddr 10.0.0.0/8 drop` lines, not eyeball a YAML string.
//
// Spec §11 says: "Tenant egress: deny 25/465/587, deny RFC1918 + link-local +
// metadata ranges." This file owns the deny-lists. Forward chains see traffic
// from the per-instance netns bridged via `BridgeName`; input chains see
// public traffic to the host. Both honor spec §11.
//
// The bridge-name constant MUST match `TenantBridge` in config.go. The
// TestTenantBridgeMatches test in config_test.go fails CI if anyone drifts.
package netns

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
)

// HostPolicy is the parameter set for rendering the host nftables ruleset.
// Fields map 1:1 to spec §11 + §7 concepts. The export is on this type (not a
// package-level constructor) so tests can vary individual fields and assert
// the substitution behavior.
//
// Per-host rendering (ADR-055). The fields `PublicIface` and
// `MasqueradeCIDR` are the per-host substitution points. Default values
// live on `DefaultHostPolicy` (the EX44 default-local node shape: `eth0`
// + `10.100.0.0/16`). A Hetzner compute node on a different NIC name
// (e.g. `ens5`) overrides via `host_vars[<compute_node>].public_iface`
// in `deploy/ansible/roles/nftables/`. The Jinja2 template at
// `policy_nftables.conf.j2` mirrors these substitutions site-for-site;
// the Go render is the source of truth, and `make egress-render-cross-check`
// byte-compares the two for the default values. The two single-field
// tests in policy_test.go (TestHostPolicyRenderSubstitutesPublicIface
// and TestHostPolicyRenderSubstitutesMasqueradeCIDR) pin each
// substitution individually so the per-host rendering can't regress
// to a hard-coded default.
type HostPolicy struct {
	// BridgeName is the root-ns bridge that all per-instance veth host-sides
	// enslave to (set up by pkg/fcvm/manager.go via the TenantBridge constant
	// in this package). MUST equal `TenantBridge`.
	BridgeName string

	// PublicIface is the host's outward-facing NIC. Spec §7 deployment uses
	// eth0 on the EX44; on the Lima guest it's the NAT'd default route.
	PublicIface string

	// DenySet is the typed egress denylist (spec §11 + ADR-023 + ADR-034).
	// All three renderers (host / per-netns / oci) consume the same
	// NewDefaultDenySet() value so the firewall rules and the user-space
	// check can't drift. See pkg/netns/denylist.go.
	DenySet DenySet

	// InputAllowTCPPorts is the inbound TCP allowlist (spec §11 input chain):
	// 22 (sshd ops), 80 (CertMagic HTTP-01 for Pro), 443 (HTTPS). Everything
	// else on the public IFace is dropped by the input chain's `policy drop`.
	InputAllowTCPPorts []int

	// MasqueradeCIDR is the source-address set the postrouting nat chain
	// MASQUERADEs to the host's public IP on its way out PublicIface. Must be
	// the NETWORK form of HostBridgeCIDR (e.g. "10.100.0.0/16", not the host
	// IP ".1" form) — every bridged tenant VM's source falls in this range
	// because pkg/fcvm/alloc.go hands out 10.100.0.2+, never .1 (the
	// allocator reserves slot 0 for the bridge itself). Without this rule
	// the per-netns SNAT translates the guest source to 10.100.x.y, but no
	// root-ns rule rewrites that to the public IP — the public internet
	// has no route back to 10.100.x.y, so every bidirectional flow (TCP /
	// HTTPS / DNS replies) dies at the first SYN-ACK or A-record.
	// Tier-1 of the network roadmap.
	MasqueradeCIDR string

	// MasqueradeCIDR6 is the IPv6 counterpart of MasqueradeCIDR. Defaults
	// to "" so v4-only deployments ship an unchanged ruleset; v6 dual-stack
	// nodes override via host_vars. The renderer emits an
	// `ip6 saddr <CIDR6> oifname <iface> masquerade` sibling on the next
	// line of the postrouting chain so v6 tenant traffic reaches the
	// public internet under the host identity (same threat model as the
	// v4 rule). Byte-for-byte locked with deploy/ansible/roles/nftables/
	// files/policy_nftables.conf.j2; `make egress-check` is the gate.
	MasqueradeCIDR6 string

	// OverlayCIDRs (Mega-PR-B Commit 2) lists the per-host overlay
	// subnets the multi-host mesh uses — e.g. a public-range
	// WireGuard mesh (the operator's `AllowedIPs` from `wg0.conf`)
	// or a public-range VPC CIDR. Two renderer effects:
	//
	//   1. Postrouting chain — for each OverlayCIDRs entry, emit a
	//      MASQUERADE sibling: `ip saddr <overlay> oifname <iface>
	//      masquerade`. Compute-node-originated traffic that travels
	//      out the public IFace still gets the host's public IP.
	//   2. Forward chain — for each OverlayCIDRs entry, emit an
	//      `ip saddr <overlay> accept` rule BETWEEN the per-CIDR
	//      deny block and the broad `iifname BridgeName oifname
	//      PublicIface accept` allow. Compute-to-compute mesh
	//      traffic that arrives bridged from br-tenants would
	//      otherwise hit the §11 RFC1918 deny when the overlay
	//      CIDR lives inside 10/8 — a Tier-1 BLOCKING trap.
	//
	// SAFETY: Render() PANICS if any OverlayCIDRs entry is a
	// subset of any DenySet.Entries.Prefix — an overlay CIDR
	// inside a denied range (RFC1918, link-local, CGNAT, ULA, or
	// any of the §11 catalogue) would silently disable the
	// lateral-movement contract for that overlay. Tailscale
	// (`100.64.0.0/10`) and any WireGuard mesh sitting inside
	// RFC1918 are rejected by this gate; operators must use a
	// public-range overlay (e.g. operator-owned IPs routed over
	// the mesh, or a public-cloud VPC). See Render panic gate.
	OverlayCIDRs []string

	// OperatorExceptions (PR scale-out tier-1 residual Gap #4) is
	// the explicit accept-before-deny list. Each entry is a
	// netip.Prefix the renderer emits as
	// `ip saddr <ex> accept` BETWEEN the established/related
	// accept rule and the per-CIDR deny block on the forward
	// chain — i.e. BEFORE the deny, AFTER the conntrack shortcut.
	// Operators using an RFC1918 overlay (e.g. 10.42.0.0/24)
	// declare the exception here AND enable the
	// manifest-level `danger_accept_rfc1918_lateral_movement`
	// flag (the flag's name itself enforces the safety in code
	// review). Empty by default — single-host dev keeps the
	// legacy "deny wins on RFC1918" posture byte-identical.
	OperatorExceptions []netip.Prefix

	// StaticEgressRules (ADR-119 redesign) is the per-VM SNAT
	// tuple list the host renderer emits in `chain postrouting`
	// BEFORE the MasqueradeCIDR MASQUERADE rule. Each entry is a
	// (per-VM host IP, customer IP) tuple belonging to one live
	// VM of a static-egress-pinned app. The Renderer emits:
	//
	//   ip saddr <PerVMHostIP> oifname <PublicIface> snat to <CustomerIP>
	//
	// ORDERING: emitted BEFORE the broad MASQUERADE because
	// nftables NAT is first-match + terminal — once the broad
	// MASQUERADE rewrites the source, the specific SNAT never
	// fires. This is the BLOCKING bug the PR-997 per-netns design
	// had (the per-netns SNAT was placed AFTER the per-netns
	// MASQUERADE and was dead code).
	//
	// OWNERSHIP: vmmd's manager populates this slice on every
	// cache mutation (Wake, Teardown, drift, SIGHUP-reload of
	// the operator bundle). The renderer is a pure function of
	// the struct. Empty slice = zero SNAT rules emitted, which
	// is the byte-identical pre-ADR-119 output for hosts that
	// have no static-egress-pinned apps.
	StaticEgressRules []StaticEgressRule
}

// StaticEgressRule (ADR-119 redesign) is one (per-VM host IP →
// customer IP) SNAT tuple the host renderer emits in
// `chain postrouting` BEFORE the broad MASQUERADE rule.
//
// PerVMHostIP is the per-VM veth host-side address of one live
// microVM (allocated from the dedicated static-egress pool at
// 10.200.x.y/16 — see pkg/fcvm/alloc.go::AcquireStaticEgressIP).
// The customer's IP is the CustomerIP, which is aliased on
// br-tenants by vmmd's SetStaticEgressIPAliases at startup +
// SIGHUP. The kernel uses PerVMHostIP for the `ip saddr` match
// (the bridged packet's source on its way out PublicIface) and
// CustomerIP for the `snat to` rewrite target.
//
// Multiple rules can share the same CustomerIP (multiple live
// VMs of one app — each instance wakes its own per-VM host IP,
// and the SNAT rule list grows by one per live VM). Each rule's
// PerVMHostIP must be unique (the per-VM host IPs are slotted
// from a fixed range by the pool allocator).
//
// AccountID and AppID are metadata written to the rule's
// trailing comment so `nft list ruleset` is self-documenting
// for operators and the future nagios/metric panels. The
// renderer does not branch on them.
type StaticEgressRule struct {
	PerVMHostIP netip.Addr
	CustomerIP  netip.Addr
	AccountID   string
	AppID       string
}

// DefaultHostPolicy is the platform-wide host nftables policy. Source of
// truth for the deny-lists per spec §11. Do not inline these values
// anywhere — every consumer of the host ruleset goes through HostPolicy and
// this var.
var DefaultHostPolicy = HostPolicy{
	BridgeName:  TenantBridge, // br-tenants (config.go) — single source of truth.
	PublicIface: "eth0",
	DenySet:     NewDefaultDenySet(),

	InputAllowTCPPorts: []int{22, 80, 443},

	// Tenant source CIDR the postrouting nat chain MASQUERADEs. Network
	// form of HostBridgeCIDR — every bridged tenant VM's host-side IP
	// (10.100.x.y, x.y ≥ 0.2) falls in this range; the bridge IP (.1) is
	// ruled out by the allocator in pkg/fcvm/alloc.go, so this CIDR
	// exactly matches "tenant-originated, not the host" once routed out
	// PublicIface. See HostPolicy.MasqueradeCIDR doc for why this exists.
	MasqueradeCIDR: "10.100.0.0/16",
}

// ActiveHostPolicy is the runtime host nftables policy — the mutable
// that vmmd's egress watcher (cmd/vmmd/egress_watcher.go) reads on
// every SIGHUP-driven reload AND on every cache mutation (Wake /
// Teardown / drift). Render() is called against this var, not the
// package-level DefaultHostPolicy, so a live update to the static-
// egress rule list (e.g. an app's per-VM host IP being added on
// Wake) ships without an operator restart.
//
// Initial value is the same as DefaultHostPolicy; the watcher
// uses ActiveHostPolicy.Render() to drive the staged-reload
// pipeline. Pre-ADR-119 watchpaths hard-coded DefaultHostPolicy.
// Render() directly — that path was always read-only and never
// reflected per-VM live state. The redesign now reads the
// mutable, which the vmmd Manager mutates on every cache event
// the same way it mutates the bridge alias set.
//
// Concurrency: the value is replaced-by-swap (the watcher takes
// a snapshot of the pointer before calling Render — see
// liveHostPolicy in cmd/vmmd/egress_watcher.go). The Render
// method itself is a pure function of the struct value, so a
// snapshot guarantees a consistent ruleset for one reload.
// Replace-by-swap is preferred over fine-grained locking on
// individual fields because the watcher reloads are infrequent
// (one per cache event) and the swap is a single pointer copy.
//
// Initial pointer holds a copy of DefaultHostPolicy; subsequent
// updates use SwapActiveHostPolicy (read by the watcher via
// ActiveHostPolicyForRender).
var ActiveHostPolicy = &DefaultHostPolicy

// SwapActiveHostPolicy atomically replaces the active host
// policy with next. The watcher reads the new pointer on the
// next Render cycle. The next argument is taken by value so
// the caller can compose a fresh struct without aliasing an
// in-flight read.
func SwapActiveHostPolicy(next HostPolicy) {
	activeHostPolicyPtr.Store(&next)
}

// ActiveHostPolicyForRender returns the current pointer to the
// active host policy. The watcher reads this BEFORE calling
// Render, so the renderer sees a consistent snapshot for the
// lifetime of one reload. Callers MUST NOT mutate the returned
// pointer's value — use SwapActiveHostPolicy to install a new
// value.
func ActiveHostPolicyForRender() *HostPolicy {
	return activeHostPolicyPtr.Load()
}

// activeHostPolicyPtr is the canonical atomic backing store
// for ActiveHostPolicy. The package-level `ActiveHostPolicy`
// var above is the initial-value alias (not the live source);
// production reads MUST go through ActiveHostPolicyForRender.
var activeHostPolicyPtr atomic.Pointer[HostPolicy]

// init makes the atomic pointer match the package-level initial
// value. Called once at package init.
func init() {
	activeHostPolicyPtr.Store(&DefaultHostPolicy)
}

// OverlayCIDRError is returned by ValidateOverlayCIDRs when an
// overlay entry sits inside a DenySet entry. Both the overlay
// prefix and the deny entry that swallowed it are recorded so the
// operator can pick a non-overlapping CIDR without re-reading the
// spec. Used by HostPolicy.Render (which converts the error into a
// panic — the renderer runs late in boot after SIGHUP-driven policy
// reloads; the panic surfaces immediately at the failure site) AND
// by cmd/vmmd/config.go::LoadConfig (which converts the error into
// a startup-time config error so the operator sees the message
// before any SIGHUP-driven reload, instead of crash-looping
// vmmd on a pg_notify EgressPolicyChanged).
//
// Mega-PR-B review M3: a single validator shared by the config
// loader and the renderer is the only way to keep the two
// checks byte-equivalent — the renderer can't catch what the
// loader already let through, and the loader can't catch what
// the renderer later panics on. The struct + Error() here is
// the load-bearing shared shape.
type OverlayCIDRError struct {
	Index   int          // index into the operator's CIDR slice
	Overlay netip.Prefix // the operator-supplied overlay CIDR
	Deny    netip.Prefix // the DenySet entry the overlay is a subset of
	DenySrc string       // SourceADR of the swallowed deny entry (spec §11, ADR-NNN, RFC-NNN)
}

// Error renders the operator-facing message. Format is stable
// (operators grep on "subset of deny entry"); the panic / startup-
// error sites pass it through verbatim.
func (e *OverlayCIDRError) Error() string {
	return fmt.Sprintf(
		"netns: OverlayCIDRs[%d]=%s is a subset of deny entry %s "+
			"(source: %s) — an accept-before-deny rule would silently "+
			"disable the lateral-movement contract. Pick an overlay CIDR "+
			"that sits outside the §11 deny list.",
		e.Index, e.Overlay, e.Deny, e.DenySrc)
}

// ValidateOverlayCIDRs walks a slice of CIDR strings (the same
// shape HostPolicy.OverlayCIDRs and cmd/vmmd.config.go.
// ComputeNodeConfig.OverlayCIDR share) and rejects any entry that
// is a subset of any deny entry. Returns nil on success or the
// first *OverlayCIDRError found.
//
// Empty input is valid (single-host dev). A malformed CIDR
// returns a plain error (parse failures are operator typos, not
// security events). The same isSubset helper HostPolicy.Render
// uses is the load-bearing check (range-based; survives
// /23-vs-/24 and v4-vs-v6 edges — see the isSubset doc comment).
//
// Mega-PR-B review M3.
//
// PR scale-out tier-1 residual (Gap #4): this function remains the
// canonical "requireNotInDeny" validator for the overlay CIDR path.
// The deny-set exception path is gated behind a separate function
// (ValidateCIDRsAgainstDenySet with requireNotInDeny=false) — the
// two callers (overlay auto-detect + RFC1918 exception) cannot
// silently bypass each other.
func ValidateOverlayCIDRs(cidrs []string, deny DenySet) error {
	return ValidateCIDRsAgainstDenySet(cidrs, deny, true)
}

// ValidateCIDRsAgainstDenySet is the Gap #4 general validator.
//
// When requireNotInDeny is true, behavior matches the original
// ValidateOverlayCIDRs: every entry must NOT be a subset of any
// deny entry. Any subset match returns *OverlayCIDRError.
//
// When requireNotInDeny is false, the caller has explicitly
// authorised the lateral-movement cost (the manifest flag
// `danger_accept_rfc1918_lateral_movement: true` is set). The
// function returns nil for any valid CIDR; the renderer emits
// accept-before-deny rules for each entry. The flag + the deny
// exception are paired at the DB CHECK constraint level so a
// missing-flag path cannot silently widen the surface.
//
// Empty input is valid in both modes (no exception = legacy
// behavior). Malformed CIDRs return a plain error in both modes.
func ValidateCIDRsAgainstDenySet(cidrs []string, deny DenySet, requireNotInDeny bool) error {
	for i, cidrStr := range cidrs {
		prefix, err := netip.ParsePrefix(cidrStr)
		if err != nil {
			return fmt.Errorf("netns: OperatorExceptions[%d] %q: %w", i, cidrStr, err)
		}
		if !requireNotInDeny {
			continue
		}
		for _, e := range deny.Entries {
			if isSubset(prefix, e.Prefix) {
				return &OverlayCIDRError{
					Index:   i,
					Overlay: prefix,
					Deny:    e.Prefix,
					DenySrc: e.SourceADR,
				}
			}
		}
	}
	return nil
}

// Render produces the full /etc/nftables.conf body, including the shebang
// line (so the file is exec'd directly by `nft -f`) and a `flush ruleset`
// to clear any prior rules before loading ours.
//
// The shape is intentionally close to the existing ansible-side ruleset: a
// single `table inet faas` with three chains (input, forward, output). The
// input and forward chains both default-drop; the output chain accept-only
// (the host itself reaches anywhere; egress policy is FOR the tenant VMs, not
// for vmmd's own outbound).
//
// Order matters in `forward`: the §11 denylist MUST come BEFORE the
// `iifname BridgeName oifname PublicIface accept` allow, otherwise bridged
// tenant traffic matches the broad allow on its first rule and never
// reaches the SMTP / RFC1918 / IPv6 drops (nftables is first-match).
// The per-netns chain (`pkg/netns/config.go::NftCommands`) is the primary
// block at the guest-originated layer; this host-side ordering is
// defense-in-depth so a misconfigured or bypassed netns chain still
// fails closed at the host layer. `ct state established,related accept`
// stays first so replies on published connections survive — a reply on
// a published connection's daddr ∈ 10.100.0.0/16 ⊂ 10.0.0.0/8 would
// otherwise hit the new RFC1918 drop. v4 deny must stay directly above
// v6 deny — see ADR-023.
func (h HostPolicy) Render() string {
	if h.BridgeName == "" || h.PublicIface == "" || h.MasqueradeCIDR == "" {
		// Hard fail rather than render a broken ruleset. Concretely, an
		// empty MasqueradeCIDR would emit `ip saddr  oifname "eth0"
		// masquerade` — invalid nft(8) syntax (`saddr` requires an
		// argument) that `nft -f` rejects outright. The ruleset would
		// never load; we'd ship a box that fails open at the egress
		// layer. The forward/input empty-field paths silently drop
		// everything once loaded — equally broken, also panic-worthy.
		panic("netns: HostPolicy.Render: BridgeName, PublicIface, and MasqueradeCIDR are required")
	}

	// Mega-PR-B Commit 2 panic gate (delegated to ValidateOverlayCIDRs
	// so cmd/vmmd/config.go::LoadConfig and HostPolicy.Render share
	// exactly one range-based subset check — see M3 doc at the
	// validator). This remains the load-bearing last-line-of-defense
	// for programmatic misuse (a future caller could mutate
	// HostPolicy.OverlayCIDRs post-LoadConfig and bypass the
	// startup-time check); panic surfaces at the failure site so
	// SIGHUP-driven EgressPolicyChanged reloads can't crash-loop
	// silently.
	if err := ValidateOverlayCIDRs(h.OverlayCIDRs, h.DenySet); err != nil {
		panic(fmt.Sprintf("netns: HostPolicy.Render: %v", err))
	}

	// ADR-119 redesign panic gate: reject StaticEgressRule entries
	// whose PerVMHostIP sits inside the MasqueradeCIDR. The
	// SNAT rule emits `ip saddr <PerVMHostIP>` — if PerVMHostIP
	// is inside the bridge /16, the broad MASQUERADE on
	// `ip saddr <MasqueradeCIDR>` masks the specific SNAT, and
	// the customer's IP would never be reached. The legacy
	// per-VM SNAT bug (PR-997 blocker #1) had the same root
	// cause; this gate prevents a future maintainer from
	// re-introducing it via misconfigured per-VM host IPs.
	// Renders on every SIGHUP-driven EgressPolicyChanged
	// reload, so the panic surfaces at the failure site
	// instead of silently shipping a broken ruleset.
	masqPrefix, masqErr := netip.ParsePrefix(h.MasqueradeCIDR)
	if masqErr != nil {
		panic(fmt.Sprintf("netns: HostPolicy.Render: MasqueradeCIDR %q unparseable: %v", h.MasqueradeCIDR, masqErr))
	}
	for _, r := range h.StaticEgressRules {
		if !r.PerVMHostIP.IsValid() {
			panic(fmt.Sprintf("netns: HostPolicy.Render: StaticEgressRules has invalid PerVMHostIP for app=%s account=%s", r.AppID, r.AccountID))
		}
		if masqPrefix.Contains(r.PerVMHostIP) {
			panic(fmt.Sprintf("netns: HostPolicy.Render: StaticEgressRules[%s/%s] PerVMHostIP %s is inside MasqueradeCIDR %s — the broad MASQUERADE would shadow the specific SNAT. Use a per-VM host IP outside the bridge /16 (the 10.200.x.y/16 static-egress pool)",
				r.AccountID, r.AppID, r.PerVMHostIP, h.MasqueradeCIDR))
		}
	}

	denyPorts := h.DenySet.SMTPPortsCommaSet()
	allowPorts := joinInts(h.InputAllowTCPPorts, ",")

	var b strings.Builder
	b.WriteString("#!/usr/sbin/nft -f\n")
	b.WriteString("# onebox-faas nftables.conf (spec §7, §11)\n")
	b.WriteString("# Tenant egress denylist — SMTP, RFC1918, link-local, metadata.\n")
	b.WriteString("# Tap proxy NAT — used by gatewayd-internal for outbound guest traffic.\n")
	b.WriteString("\n")
	b.WriteString("flush ruleset\n")
	b.WriteString("\n")
	b.WriteString("table inet faas {\n")
	// PR-E: pre-declare every per-CIDR drop counter at table scope so
	// the forward-chain rules below can reference them by name. The
	// table is `inet faas` (combined v4 + v6 family), so a single
	// pre-declaration suffices. Same rationale as the per-netns
	// counter declarations in Config.NftCommands (config.go); without
	// the pre-declaration, nft v1.0.x rejects the rule's `counter name`
	// reference and v1.1.x silently ignores the counter. The vmmd
	// scrape adapter reads these names via `nft list counters` and
	// surfaces per-CIDR drops on <daemon>_egress_deny_total{cidr,family}.
	for _, e := range h.DenySet.Entries {
		fmt.Fprintf(&b, "  counter %s {}\n", e.CounterName)
	}
	b.WriteString("  chain input {\n")
	b.WriteString("    type filter hook input priority 0; policy drop;\n")
	b.WriteString("    ct state established,related accept\n")
	b.WriteString("    iif lo accept\n")
	fmt.Fprintf(&b, "    iifname %q accept\n", h.BridgeName)
	fmt.Fprintf(&b, "    tcp dport { %s } accept     # sshd + gatewayd-public public listener\n", allowPorts)
	b.WriteString("  }\n")
	b.WriteString("\n")
	b.WriteString("  chain forward {\n")
	b.WriteString("    type filter hook forward priority 0; policy drop;\n")
	b.WriteString("    ct state established,related accept\n")
	b.WriteString("\n")
	// PR scale-out tier-1 residual (Gap #4): per-exception accept
	// rules emitted BEFORE the per-CIDR deny block — nftables is
	// first-match, so an exception in 10/8 (e.g. operator's overlay
	// subnet 10.42.0.0/24) MUST take precedence over the matching
	// `ip daddr 10.0.0.0/8 drop`. The deny-set entries stay
	// byte-identical; only the new rules are additive. Gated by
	// the manifest flag `danger_accept_rfc1918_lateral_movement`
	// — operators must explicitly authorize the lateral-movement
	// consequence. Empty OperatorExceptions (single-host dev)
	// emits zero rules; rendered bytes are byte-identical to the
	// pre-Gap-4 output. The `established,related accept` above is
	// placed first so replies on existing flows keep flowing
	// regardless of any exception.
	for _, ex := range h.OperatorExceptions {
		family := familyKeywordV4
		if ex.Addr().Is6() && !ex.Addr().Is4In6() {
			family = familyKeywordV6
		}
		fmt.Fprintf(&b, "    %s saddr %s accept     # operator exception (RFC1918 lateral movement, gated by manifest flag)\n",
			family, ex.String())
	}
	b.WriteString("    # spec §11 denylist — evaluated BEFORE the bridged-tenant broad allow\n")
	b.WriteString("    # so tenant traffic to RFC1918 / SMTP / link-local is actually dropped at\n")
	b.WriteString("    # the host layer; the per-netns chain is the primary block, this is defense\n")
	b.WriteString("    # in depth. PR-E renders ONE RULE per DenySet entry so each CIDR's drop\n")
	b.WriteString("    # count is observable via `nft list counters` (the vmmd scrape adapter\n")
	b.WriteString("    # exposes them as <daemon>_egress_deny_total{cidr,family}). The previous\n")
	b.WriteString("    # aggregate `ip daddr { … } drop` shape produced one anonymous bucket\n")
	b.WriteString("    # for the entire list — useless for the per-tenant / per-CIDR observability\n")
	b.WriteString("    # question.\n")
	fmt.Fprintf(&b, "    tcp dport { %s } drop\n", denyPorts)
	for _, e := range h.DenySet.Entries {
		family := familyKeywordV4
		if e.Family == FamilyV6 {
			family = familyKeywordV6
		}
		fmt.Fprintf(&b, "    %s daddr %s counter name %q drop\n",
			family, e.Prefix.String(), e.CounterName)
	}
	// Mega-PR-B Commit 2: per-overlay accept rules emitted AFTER the
	// per-CIDR deny block and BEFORE the broad bridged-tenant allow.
	// The deny-set stays identical (lateral-movement contract intact);
	// the overlay-accept rules unblock bridge-to-overlay traffic that
	// would otherwise hit the §11 RFC1918 deny (Tier-1 BLOCKING trap
	// when the overlay CIDR lives inside 10/8). The panic gate above
	// ensures no overlay CIDR is a subset of a deny entry, so the
	// accept rules are *additive* — they cannot silently disable a
	// deny. Empty OverlayCIDRs (single-host dev) emits zero rules;
	// the rendered bytes are byte-identical to the pre-Commit-2 output.
	for _, overlayStr := range h.OverlayCIDRs {
		fmt.Fprintf(&b, "    ip saddr %s accept\n", overlayStr)
	}
	fmt.Fprintf(&b, "    iifname %q oifname %q accept\n", h.BridgeName, h.PublicIface)
	b.WriteString("  }\n")
	b.WriteString("\n")
	b.WriteString("  chain output {\n")
	b.WriteString("    type filter hook output priority 0; policy accept;\n")
	b.WriteString("  }\n")
	b.WriteString("\n")
	b.WriteString("  chain postrouting {\n")
	b.WriteString("    type nat hook postrouting priority srcnat; policy accept;\n")
	// ADR-119 redesign: per-VM static-egress SNAT rules. Each
	// StaticEgressRules entry is a (per-VM host IP, customer IP)
	// tuple belonging to one live VM of a static-egress-pinned
	// app. The renderer emits ONE rule per tuple, ordered BEFORE
	// the broad MasqueradeCIDR MASQUERADE below because nftables
	// NAT is first-match + terminal — once the broad MASQUERADE
	// rewrites the source, the specific SNAT never sees the
	// rewritten packet. The comment at the end of each rule
	// includes the account_id and app_id so `nft list ruleset`
	// is self-documenting for operators.
	//
	// The PerVMHostIP is allocated from the dedicated static-
	// egress pool (10.200.x.y/16, see pkg/fcvm/alloc.go::
	// AcquireStaticEgressIP) — the panic gate above ensures
	// Renders fail-closed if a PerVMHostIP accidentally sits
	// inside MasqueradeCIDR (which would re-introduce the
	// PR-997 BLOCKING bug).
	for _, r := range h.StaticEgressRules {
		comment := ""
		if r.AccountID != "" || r.AppID != "" {
			comment = fmt.Sprintf("     # account=%s app=%s", r.AccountID, r.AppID)
		}
		fmt.Fprintf(&b, "    ip saddr %s oifname %q snat to %s%s\n",
			r.PerVMHostIP.String(), h.PublicIface, r.CustomerIP.String(), comment)
	}
	fmt.Fprintf(&b, "    ip saddr %s oifname %q masquerade\n", h.MasqueradeCIDR, h.PublicIface)
	// Mega-PR-B Commit 2: per-overlay MASQUERADE siblings. Each
	// OverlayCIDRs entry produces one MASQUERADE sibling after the
	// bridge CIDR rule, so compute-node-originated overlay traffic
	// that leaves via PublicIface still exits under the host's
	// public IP. Empty OverlayCIDRs emits zero siblings; the
	// rendered bytes are byte-identical to the pre-Commit-2 output.
	for _, overlayStr := range h.OverlayCIDRs {
		fmt.Fprintf(&b, "    ip saddr %s oifname %q masquerade\n", overlayStr, h.PublicIface)
	}
	// IPv6 sibling — emitted only when MasqueradeCIDR6 is non-empty so
	// v4-only deployments ship an unchanged ruleset. The sibling mirrors
	// the v4 rule exactly (same oifname, same nat chain); without it,
	// v6 tenant traffic falls through `policy accept` and reaches the
	// public internet under the tenant's link-local source — a return-
	// routability black hole analogous to the v4 omission.
	if h.MasqueradeCIDR6 != "" {
		fmt.Fprintf(&b, "    ip6 saddr %s oifname %q masquerade\n", h.MasqueradeCIDR6, h.PublicIface)
	}
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

// joinInts renders a port slice as comma-joined digits: "25,465,587". The
// nftables tcp-dport set syntax is `{ 25,465,587 } drop`.
func joinInts(in []int, sep string) string {
	parts := make([]string, len(in))
	for i, n := range in {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, sep)
}

// isSubset reports whether every address in inner is also in outer
// (Mega-PR-B Commit 2 panic gate). Equal prefixes are considered
// subsets so the gate rejects both the overlap and the exact-match
// case (e.g. an overlay == part of a deny entry). Different address
// families (v4 vs v6) never compare as subsets.
//
// The check is range-based: the BROADCAST address of `inner` must
// fall at or below the broadcast address of `outer` (after masking).
// Naive byte comparison of network addresses misses the case where
// the inner prefix has a higher Bits() than outer but its network
// address is still inside outer's range — e.g. 10.42.0.0/24 has
// network 10.42.0.0 with Bits=24; 10.0.0.0/8 has broadcast 10.255.255.255.
// 10.42.0.255 (the highest address in /24) IS in 10.0.0.0/8 — so the
// /24 IS a subset of the /8.
func isSubset(inner, outer netip.Prefix) bool {
	if inner == outer {
		return true
	}
	if inner.Bits() < outer.Bits() {
		return false
	}
	if inner.Addr().BitLen() != outer.Addr().BitLen() {
		return false
	}
	// The broadcast of inner (highest address) must be <= broadcast of outer.
	// For prefixes, network+bits uniquely identifies the range; we walk the
	// top-of-inner vs top-of-outer.
	innerTop := prefixTopAddr(inner)
	outerTop := prefixTopAddr(outer)
	if compareAddr(innerTop, outerTop) > 0 {
		return false
	}
	// And the bottom of inner (its network address) must be >= bottom of outer.
	innerBot := inner.Masked().Addr()
	outerBot := outer.Masked().Addr()
	return compareAddr(innerBot, outerBot) >= 0
}

// prefixTopAddr returns the highest address in a prefix's range
// (network address | host bits all set). For 10.0.0.0/8 → 10.255.255.255;
// for 10.42.0.0/24 → 10.42.0.255. Renamed from lastAddr to avoid
// colliding with netip.Prefix.lastAddr (Go 1.22+ method of the same
// name).
func prefixTopAddr(p netip.Prefix) netip.Addr {
	addr := p.Addr()
	bits := p.Bits()
	addrLen := addr.BitLen()
	if bits == addrLen {
		return addr
	}
	// For a v4 prefix with Bits=8, the host bits span 24 positions.
	// We set them by OR-ing with a host-bits mask.
	bytes := addr.AsSlice()
	hostBits := uint(addrLen - bits)
	// Walk from the rightmost byte, set all host bits to 1.
	for i := len(bytes) - 1; i >= 0 && hostBits > 0; i-- {
		setInThis := hostBits
		if setInThis > 8 {
			setInThis = 8
		}
		mask := byte((1 << setInThis) - 1)
		bytes[i] |= mask
		hostBits -= setInThis
	}
	a, _ := netip.AddrFromSlice(bytes)
	return a
}

// compareAddr returns -1, 0, or +1 according to whether a < b, ==, or >.
// v4 < v6 ordering is preserved: all v4 addresses sort below all v6.
func compareAddr(a, b netip.Addr) int {
	if a == b {
		return 0
	}
	aLen, bLen := a.BitLen(), b.BitLen()
	if aLen != bLen {
		if aLen < bLen {
			return -1
		}
		return 1
	}
	ab, bb := a.AsSlice(), b.AsSlice()
	for i := range ab {
		if ab[i] < bb[i] {
			return -1
		}
		if ab[i] > bb[i] {
			return 1
		}
	}
	return 0
}
