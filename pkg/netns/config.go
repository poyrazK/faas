package netns

import (
	"fmt"
	"net/netip"
	"strings"
)

// Inner-world constants (ADR-009, spec §7). Every guest sees the IDENTICAL
// network: guest 10.0.0.2/30 behind tap0, gateway 10.0.0.1. That sameness is
// exactly what lets one snapshot restore as N concurrent instances — the
// per-instance uniqueness lives entirely on the host side (veth + host IP),
// never inside the guest. Do not make these per-VM.
const (
	GuestIP        = "10.0.0.2"
	GuestGateway   = "10.0.0.1"
	GuestPrefix    = "10.0.0.2/30"
	TapPrefix      = "10.0.0.1/30" // host (tap0) side of the /30 inside the netns
	AppPort        = 8080          // the :8080 contract (spec §2)
	TenantBridge   = "br-tenants"  // root-ns bridge the veth host-side enslaves to
	HostBridgeCIDR = "10.100.0.1/16"
	// nft chain-policy words (ADR-031). Forwarded as the `policy`
	// value in the per-netns forward-chain argv, so goconst demands
	// the literals live in named constants.
	nftPolicyAccept = "accept"
	nftPolicyDrop   = "drop"
)

// Config is the per-instance network plan. All uniqueness (Netns name, veth
// names, HostIP) comes from the fcvm allocator; this package only turns it into
// the exact command sequence to realise and tear down the topology:
//
//	root ns:  br-tenants ── vethHost ─┐
//	                                  │  (veth pair)
//	netns fc-<instance>:  vethPeer(10.100.x.y/16) ── [route/DNAT] ── tap0(10.0.0.1) ── guest(10.0.0.2)
type Config struct {
	Instance   string     // instance id
	Netns      string     // fc-<instance>
	Tap        string     // tap0 (identical in every netns)
	VethHost   string     // root-ns end, enslaved to br-tenants
	VethPeer   string     // netns end, holds HostIP
	HostIP     netip.Addr // routable identity, 10.100.x.y
	HostBits   int        // prefix length for HostIP (16)
	EgressMbit int        // per-plan egress cap via tc on VethHost; 0 = no cap (legacy / disabled)
	// ConntrackCap caps the per-instance conntrack table size at
	// `forward` time (spec §7 line 344 of docs/faas_implementation_spec.md,
	// ADR-018 deferral resolved). 0 = no cap rule emitted, so existing
	// callers that haven't been wired yet stay silent. Production sets
	// it to api.DefaultConntrackCap at every Wake (pkg/fcvm/manager.go).
	// Rule shape: `nft add rule ip faas forward ct count over N
	// counter name "faas_cap" drop`. The named counter is `nft list
	// counters`-readable so PR-C can surface cap-hit telemetry without
	// a netlink dependency.
	ConntrackCap int64
	// EgressAllowlist (ADR-031 + ADR-032, tier-2 of the network
	// roadmap): per-app outbound IP allowlist. Empty slice = no
	// allowlist rule emitted (current behaviour preserved —
	// chain-policy accept stays effective). Non-empty → NftCommands
	// emits one or two rules in the per-netns forward chain(s),
	// partitioned by `prefix.Addr().Is4()`:
	//   * the v4 half lands on `ip faas forward` as
	//     `iifname "tap0" ip daddr { … } accept`
	//   * the v6 half lands on `ip6 faas forward` as
	//     `iifname "tap0" ip6 daddr { … } accept`
	// (nft rejects mixing `ip` and `ip6` matches in one table —
	// ADR-023; the renderer partitions internally so the wire shape
	// stays a single repeated string at proto AppSpec field 7).
	// Both rules are placed AFTER the lateral-movement deny + SMTP
	// drops, so deny > allow on overlap (an operator typo into
	// RFC1918 still gets dropped); and BEFORE the chain's default
	// policy, so unlisted destinations drop when an allowlist
	// exists on either chain. Free/Hobby plans never populate this
	// field — apid gates the PATCH upstream. The non-/0 contract
	// is enforced by the DB trigger
	// `apps_egress_allowlist_cidr` (migration 00030, ADR-032).
	EgressAllowlist []netip.Prefix
}

// NewConfig fills the constant fields (tap name, /16) around the allocated names
// and IP.
func NewConfig(instance, netnsName, vethHost, vethPeer string, hostIP netip.Addr) Config {
	return Config{
		Instance: instance,
		Netns:    netnsName,
		Tap:      "tap0",
		VethHost: vethHost,
		VethPeer: vethPeer,
		HostIP:   hostIP,
		HostBits: 16,
	}
}

// hostCIDR renders HostIP with its prefix, e.g. "10.100.0.2/16".
func (c Config) hostCIDR() string {
	return fmt.Sprintf("%s/%d", c.HostIP, c.HostBits)
}

// SetupCommands returns the ordered argv list that creates the namespace, veth
// pair, tap device, and addressing. Each element is a full command (no shell).
// The metal layer executes them in order; a failure at step N must trigger
// Teardown so nothing leaks (invariant §6.2-4/5, `make leakcheck`).
func (c Config) SetupCommands() [][]string {
	nx := []string{"ip", "netns", "exec", c.Netns} // prefix for in-netns commands
	cmd := func(parts ...string) []string { return parts }
	inNetns := func(parts ...string) []string { return append(append([]string{}, nx...), parts...) }

	return [][]string{
		// Namespace + loopback.
		cmd("ip", "netns", "add", c.Netns),
		inNetns("ip", "link", "set", "lo", "up"),
		// veth pair; host end onto the tenant bridge, peer end into the netns.
		cmd("ip", "link", "add", c.VethHost, "type", "veth", "peer", "name", c.VethPeer),
		cmd("ip", "link", "set", c.VethHost, "master", TenantBridge),
		cmd("ip", "link", "set", c.VethHost, "up"),
		cmd("ip", "link", "set", c.VethPeer, "netns", c.Netns),
		inNetns("ip", "addr", "add", c.hostCIDR(), "dev", c.VethPeer),
		inNetns("ip", "link", "set", c.VethPeer, "up"),
		// tap0 for firecracker; host side of the guest /30.
		inNetns("ip", "tuntap", "add", c.Tap, "mode", "tap"),
		inNetns("ip", "addr", "add", TapPrefix, "dev", c.Tap),
		inNetns("ip", "link", "set", c.Tap, "up"),
		// Route guest traffic; enable forwarding inside the netns only.
		inNetns("sysctl", "-w", "net.ipv4.ip_forward=1"),
		// Netns default route via the bridge IP (HostBridgeCIDR). Without
		// this, the kernel only knows two connected subnets inside the netns
		// — 10.0.0.0/30 on tap0 and 10.100.0.0/16 on VethPeer — so a guest
		// packet to e.g. 8.8.8.8 has no matching route and the kernel returns
		// ENETUNREUCH (was the silent tenant-egress P0: outbound HTTP from
		// any guest never worked on the production EX44). The real reason
		// it slipped through CI/dev: no //go:build metal test exists for
		// guest->public egress (the only Test*Metal* in pkg/netns is a
		// syntax check), and TestMetalHelloBoot only probes host->guest
		// DNAT in pkg/fcvm/manager_metal_test.go. The Lima nested-VM
		// shim also brings up br-tenants + root forwarding which masks the
		// gap, but does not install a netns default route either, so even
		// Lima does not exercise this code path end to end. The bridge IP
		// is reserved by pkg/fcvm/alloc.go (allocator hands out
		// 10.100.0.2+, never .1), so no slot-0 collision is possible.
		inNetns("ip", "route", "add", "default", "via", "10.100.0.1", "dev", c.VethPeer),
	}
}

// TeardownCommands returns the argv list to remove everything Setup created.
//
// Order matters and is the opposite of what an intuitive "delete-from-outside-in"
// would suggest: we delete the namespace FIRST, then the host-side veth end.
//
// `ip netns del` removes every interface inside the namespace (the peer veth
// and tap0) atomically, and the host-side veth follows. Doing it in the other
// order — `ip link del vhHost` then `ip netns del` — leaves the peer veth
// orphaned (only its root-ns half is gone) and the namespace delete fails
// silently in some iproute2 versions because the orphaned peer still pins a
// reference. Verified on the Lima arm64 nested-KVM guest; the EX44's iproute2
// has the same behavior, so the fix is universal.
//
// Errors from either command are tolerated by the caller (cleanup() in
// pkg/fcvm/manager.go) — a teardown that gives up would leak.
func (c Config) TeardownCommands() [][]string {
	return [][]string{
		{"ip", "netns", "del", c.Netns},
		{"ip", "link", "del", c.VethHost},
	}
}

// NftCommands returns the per-instance nftables ruleset (spec §7) as a sequence
// of argv commands applied inside the netns. It both publishes the instance and
// enforces the ship-blocking tenant egress policy (§11). Two responsibilities:
//
//   - Publish + NAT: traffic arriving on the uplink (VethPeer) to the host
//     identity's :8080 is DNAT'd to the guest's :8080 contract; guest egress is
//     masqueraded behind the host identity.
//   - Egress filter: matched on iifname tap0 so it only touches guest-ORIGINATED
//     traffic and can never break the inbound DNAT path. Deny SMTP (25/465/587 —
//     spam is a Hetzner-abuse existential risk) and deny RFC1918 + link-local
//     (169.254.0.0/16 covers the 169.254.169.254 metadata IP) so a tenant cannot
//     move laterally into the control plane. Default policy is accept (§7
//     default-allow 80/443/53).
//
// Rules live in the netns, so TeardownCommands' `netns del` drops them — no
// explicit nft teardown is needed. Each command is a full argv (nft joins its
// argv and parses it), so this works with the same stdin-less Runner that
// executes SetupCommands. Egress bandwidth (per-plan tc) is applied by
// Config.TcCommands, called by Manager.setupNetwork between SetupCommands
// and NftResetCommands — same argv shape, runs in the root ns since tc
// operates on VethHost. Per-instance conntrack cap (spec §7) is not yet
// wired — separate follow-up.
//
// The returned slice is FATAL — every argv must exit 0. For idempotency-reset
// commands (delete/flush, which exit non-zero when the table is absent), see
// NftResetCommands; those run best-effort ahead of the ruleset so a Wake that
// re-enters an existing netns doesn't fail on `delete table`.
func (c Config) NftCommands() [][]string {
	nx := []string{"ip", "netns", "exec", c.Netns, "nft"}
	nft := func(parts ...string) []string { return append(append([]string{}, nx...), parts...) }
	port := fmt.Sprintf("%d", AppPort)
	// Builder (not a literal slice) so the optional §7 conntrack-cap
	// rule can be appended without leaving a nil element when
	// ConntrackCap == 0.
	cmds := make([][]string, 0, 16)
	add := func(argv ...string) { cmds = append(cmds, nft(argv...)) }
	add("add", "table", "ip", "faas")
	// Chain policy (ADR-031). Empty EgressAllowlist → keep the
	// historical `policy accept` (every non-deny-listed destination
	// reaches the public internet, same as today). Non-empty → flip
	// to `policy drop` so the allowlist accept rule becomes the only
	// egress path; the explicit deny rules (lateral movement + SMTP)
	// still run BEFORE the accept rule so deny > allow on overlap.
	// Forwarding the policy word through a helper keeps the v4 and
	// v6 chains in lock-step — a future change to the policy
	// decision lives in one place.
	forwardPolicy := c.forwardChainPolicy()
	// Counter object for the §7 conntrack cap rule (faas_cap). Must be
	// declared before the rule that references it; nftables requires a
	// named counter to be defined as a table-level object first, then
	// referenced by name in the rule. Without this, nftables v1.0.x
	// rejects "no such file or directory" and v1.1.x silently ignores the
	// counter in the rule (the counter never increments).
	if c.ConntrackCap > 0 {
		add("add", "counter", "ip", "faas", "faas_cap", "{}")
	}
	// NAT: publish :8080 to the guest; masquerade the guest's egress.
	add("add", "chain", "ip", "faas", "prerouting", "{", "type", "nat", "hook", "prerouting", "priority", "dstnat", ";", "}")
	add("add", "rule", "ip", "faas", "prerouting", "iifname", c.VethPeer, "tcp", "dport", port, "dnat", "to", fmt.Sprintf("%s:%d", GuestIP, AppPort))
	add("add", "chain", "ip", "faas", "postrouting", "{", "type", "nat", "hook", "postrouting", "priority", "srcnat", ";", "}")
	add("add", "rule", "ip", "faas", "postrouting", "oifname", c.VethPeer, "masquerade")
	// Egress filter (§11): default-accept, deny from the guest side only.
	add("add", "chain", "ip", "faas", "forward", "{", "type", "filter", "hook", "forward", "priority", "filter", ";", "policy", forwardPolicy, ";", "}")
	// Accept reply traffic first. The inbound DNAT'd request is published from
	// the host identity (10.100.x.y ∈ 10.100.0.0/16), so the guest's reply
	// leaves iifname tap0 with daddr in that range — which is ALSO inside the
	// 10.0.0.0/8 lateral-movement deny below. Without this established/related
	// accept the deny would drop every reply and no published request would
	// ever complete. Guest-INITIATED (ct state new) traffic still falls through
	// to the denies, so lateral movement stays blocked.
	add("add", "rule", "ip", "faas", "forward", "ct", "state", "established,related", "accept")
	// Spec §7 cap (only when ConntrackCap > 0): drop new forward flows whose
	// origin conntrack table already holds > N entries, so one misbehaving
	// tenant can't exhaust the host-wide conntrack table. Sits AFTER the
	// established/related accept (reply packets on existing flows keep flowing
	// regardless of count) and BEFORE the SMTP / lateral-movement drops (a
	// misbehaving app scanning many denied destinations still hits the cap).
	// `ct count over` is the native nft conntrack primitive (nft ≥ 1.0.7);
	// the named counter makes `nft list counters` PR-C-readable.
	if rule := c.forwardConnlimitRule(nft); rule != nil {
		cmds = append(cmds, rule)
	}
	add("add", "rule", "ip", "faas", "forward", "iifname", c.Tap, "tcp", "dport", "{", "25,", "465,", "587", "}", "drop")
	// CGN (100.64.0.0/10) included for symmetry with pkg/netns.DefaultHostPolicy
	// .ForwardDenyCIDRs. IPv6 sibling follows — see ADR-023 and
	// pkg/oci/egress.go::deniedCIDRv6.
	add("add", "rule", "ip", "faas", "forward", "iifname", c.Tap, "ip", "daddr", "{", "10.0.0.0/8,", "172.16.0.0/12,", "192.168.0.0/16,", "169.254.0.0/16,", "100.64.0.0/10", "}", "drop")
	// ADR-031 + ADR-032 per-app egress allowlist. Placed AFTER the
	// lateral-movement deny + SMTP drops so deny > allow on overlap
	// (an operator typo landing a sensitive CIDR in the allowlist
	// still gets dropped). Placed BEFORE the chain's default-accept
	// policy so unlisted destinations drop when an allowlist exists.
	// Empty EgressAllowlist means no rule and current behaviour is
	// preserved (chain-default accept does the work). The v4 helper
	// is called here, after the v4 lateral-movement drop; the v6
	// helper is called later, after the v6 lateral-movement drop, so
	// each rule sits inside its chain block (the ruleset's per-chain
	// ordering — established,related accept → cap → SMTP → deny →
	// allowlist → chain-policy — holds for both v4 and v6).
	if rule := c.forwardAllowlistRule(nft); rule != nil {
		cmds = append(cmds, rule)
	}
	// The per-netns table is `ip faas` (not `inet faas` — nft requires an
	// ip6-family table for `ip6 daddr` rules; mixing `ip` and `ip6` matches
	// in one table is rejected). We keep the host-level table as `inet faas`
	// and accept the table-family divergence here. A future migration to a
	// per-netns `inet faas` table is a follow-up if we want to collapse the
	// two; see ADR-023 "rejected alternatives" for the trade-off.
	add("add", "table", "ip6", "faas")
	// Same counter object for the v6 chain — faas_cap is scoped per table,
	// so ip faas.faas_cap and ip6 faas.faas_cap are independent (ADR-023).
	if c.ConntrackCap > 0 {
		add("add", "counter", "ip6", "faas", "faas_cap", "{}")
	}
	add("add", "chain", "ip6", "faas", "forward", "{", "type", "filter", "hook", "forward", "priority", "filter", ";", "policy", forwardPolicy, ";", "}")
	// Accept reply traffic first (mirrors the v4 chain above) so a published
	// request's IPv6 reply isn't dropped by the lateral-movement deny.
	add("add", "rule", "ip6", "faas", "forward", "ct", "state", "established,related", "accept")
	// §7 cap mirrored on the v6 chain: spec mandates one per-instance budget
	// without distinguishing v4 vs v6 entries. Without this sibling a guest
	// could flood only IPv6 to exhaust the conntrack table separately. Placed
	// AFTER the established/related accept and BEFORE the ip6 daddr deny,
	// mirroring the v4 placement. Same named counter — the v4 rule's
	// faas_cap and this rule's faas_cap are independent counters (nft named
	// counters are scoped per chain/table); PR-C will need to sum them when
	// it reads cap-hit telemetry. See comments on forwardConnlimitRule.
	if rule := c.forwardConnlimitRule6(nft); rule != nil {
		cmds = append(cmds, rule)
	}
	add("add", "rule", "ip6", "faas", "forward", "iifname", c.Tap, "ip6", "daddr", "{", "fe80::/10,", "fc00::/7,", "ff00::/8,", "::1/128,", "::/128", "}", "drop")
	// ADR-032 — per-app egress allowlist v6 mirror. Same placement
	// contract as the v4 sibling above (AFTER deny, BEFORE
	// chain-policy), only on the v6 chain. Empty EgressAllowlist or
	// a v4-only allowlist returns nil here — v6 stays at
	// chain-policy drop (because forwardChainPolicy flips when the
	// single field is non-empty), with no per-chain accept rule.
	if rule := c.forwardAllowlistRule6(nft); rule != nil {
		cmds = append(cmds, rule)
	}
	return cmds
}

// forwardConnlimitRule emits a single-element argv (or nothing when the
// cap is disabled) for the §7 per-instance conntrack cap on the IPv4
// forward chain. Factored out from NftCommands so the disabled/zero
// branch is unit-testable without forking the entire ruleset, and so
// the "stringly-quoted counter name" stays in one place. See Config.
// ConntrackCap for the rule's contract.
//
// Internal to NftCommands — do not invoke from anywhere else.
func (c Config) forwardConnlimitRule(nft func(...string) []string) []string {
	if c.ConntrackCap <= 0 {
		return nil
	}
	return nft("add", "rule", "ip", "faas", "forward", "ct", "count", "over",
		fmt.Sprintf("%d", c.ConntrackCap), "counter", "name", `"faas_cap"`, "drop")
}

// forwardConnlimitRule6 is the IPv6 sibling of forwardConnlimitRule.
// Same ConntrackCap value, table family switched to `ip6`. Both
// counters are named "faas_cap" — nft scopes named counters per
// chain/table family, so the v4 and v6 counters don't collide even
// though they share the name. PR-C reads both via `nft list
// counters` and sums them.
//
// Internal to NftCommands — do not invoke from anywhere else.
func (c Config) forwardConnlimitRule6(nft func(...string) []string) []string {
	if c.ConntrackCap <= 0 {
		return nil
	}
	return nft("add", "rule", "ip6", "faas", "forward", "ct", "count", "over",
		fmt.Sprintf("%d", c.ConntrackCap), "counter", "name", `"faas_cap"`, "drop")
}

// forwardChainPolicy returns the nft `policy` word for the per-netns
// forward chain (shared between the v4 and v6 sibling chains so the
// two stay in lock-step). Empty EgressAllowlist keeps the historical
// `accept` default — every non-deny-listed destination reaches the
// public internet, unchanged from before ADR-031. Non-empty flips
// to `drop`: the allowlist accept rule becomes the only egress path,
// the explicit deny rules (lateral movement + SMTP) still run BEFORE
// the accept rule (deny > allow on overlap), and unlisted
// destinations fall through to the chain policy and are dropped.
//
// Why not keep `policy accept` always and append a terminal
// `iifname "tap0" drop`? Two reasons: (1) the operator's intent —
// "I pinned an allowlist" — is expressed most clearly by an actual
// change of policy, not by a *new* drop rule that's easy to miss in
// argv sub-list reasoning; (2) the chain-policy word is the same
// concept across both v4 and v6 chains and stays in one helper
// instead of being duplicated in two argv builders.
//
// Internal to NftCommands — do not invoke from anywhere else.
func (c Config) forwardChainPolicy() string {
	if len(c.EgressAllowlist) == 0 {
		return nftPolicyAccept
	}
	return nftPolicyDrop
}

// forwardAllowlistRule (ADR-031 + ADR-032) emits the IPv4 half of
// the per-app outbound IP allowlist accept rule, or nil when
// EgressAllowlist is empty or contains no v4 entries (so current
// behaviour — chain-policy accept — stays the default for any vmmd
// that hasn't been wired, any app that didn't PATCH the list, or
// any list that holds only v6 entries — the v6 half is emitted by
// forwardAllowlistRule6). Shape:
//
//	nft add rule ip faas forward
//	  iifname "tap0" ip daddr { CIDR1,CIDR2,… } accept
//
// The CIDR set uses comma-joined values inside `{ … }` with NO
// trailing whitespace, matching the modern-nft syntax gate at
// pkg/netns/policy.go (PR #128's comma-required regression net —
// memory `nft-cidr-set-comma-required`). Order in the v4 chain:
// AFTER lateral-movement deny + SMTP drops (deny > allow on
// overlap), BEFORE chain-default accept (unlisted destinations
// drop when an allowlist exists on the v4 chain).
//
// Internal to NftCommands — do not invoke from anywhere else.
func (c Config) forwardAllowlistRule(nft func(...string) []string) []string {
	var v4 []string
	for _, p := range c.EgressAllowlist {
		if p.Addr().Is4() {
			v4 = append(v4, p.String())
		}
	}
	if len(v4) == 0 {
		return nil
	}
	return nft("add", "rule", "ip", "faas", "forward",
		"iifname", c.Tap, "ip", "daddr", "{", strings.Join(v4, ","), "}", "accept")
}

// forwardAllowlistRule6 (ADR-032 — v6 mirror of forwardAllowlistRule)
// emits the per-app outbound IPv6 allowlist accept rule, or nil when
// EgressAllowlist is empty or contains no v6 entries. Same shape as
// the v4 helper but with the `ip6` family keyword and the
// `ip6 faas forward` chain (nft rejects mixing `ip` and `ip6 daddr`
// matches in one table — ADR-023). Order in the v6 chain: AFTER
// the v6 lateral-movement deny (deny > allow on overlap), BEFORE
// chain-default accept (unlisted destinations drop when an
// allowlist exists on the v6 chain).
//
// Internal to NftCommands — do not invoke from anywhere else.
func (c Config) forwardAllowlistRule6(nft func(...string) []string) []string {
	var v6 []string
	for _, p := range c.EgressAllowlist {
		if !p.Addr().Is4() {
			v6 = append(v6, p.String())
		}
	}
	if len(v6) == 0 {
		return nil
	}
	return nft("add", "rule", "ip6", "faas", "forward",
		"iifname", c.Tap, "ip6", "daddr", "{", strings.Join(v6, ","), "}", "accept")
}

// NftResetCommands returns the best-effort argv list that brings the
// per-netns nftables table to a clean slate before NftCommands runs.
// Each `delete table` exits non-zero when the table is absent (first
// boot, or just-rebuilt netns) — that is expected; the caller logs the
// failure and continues. The reset succeeds when the table persists
// across two back-to-back runs without an intervening TeardownCommands,
// e.g. a re-run of NftCommands after a partial failure (add table ran,
// rules didn't install), or a future code path that rerenders without
// re-creating the netns. Cold boot itself does NOT hit this branch —
// pkg/fcvm/manager.go:cleanup() always runs TeardownCommands, which
// `ip netns del`s the netns and drops every per-netns nft table with it.
//
// Best-effort, not fatal — this is the only place in the per-instance
// lifecycle where we accept nft returning non-zero. Splitting it from
// NftCommands keeps the ruleset's own commands strictly atomic.
func (c Config) NftResetCommands() [][]string {
	nx := []string{"ip", "netns", "exec", c.Netns, "nft"}
	nft := func(parts ...string) []string { return append(append([]string{}, nx...), parts...) }
	return [][]string{
		nft("delete", "table", "ip", "faas"),
		// Mirror the IPv6 table reset (ADR-023 split). Same best-effort
		// shape as the v4 entry: if the table is absent (first boot, freshly
		// created netns) the delete fails and the caller continues; if the
		// table persists across rerun (partial-failure rerender), the
		// delete succeeds and the subsequent `add table ip6 faas` does not
		// collide. See NftResetCommands doc comment for why cold boot
		// itself does not exercise this branch.
		nft("delete", "table", "ip6", "faas"),
	}
}

// TcCommands returns the argv list that applies the per-plan egress
// bandwidth cap on the host-side veth (spec §7: per-instance egress cap
// 10 / 25 / 100 / 250 Mbit via tc). VethHost is the host-side end of the
// pair (root-ns, enslaved to br-tenants); the qdisc lives there so it
// caps bytes regardless of which direction the kernel attributes them.
// No ip netns exec prefix — tc runs in the root namespace.
//
// tbf is the simplest single-rate shaper; an htb class hierarchy is
// unnecessary for a per-veth root cap where every instance gets its own
// qdisc on its own link. burst 32kbit / latency 400ms are conservative
// defaults — burst covers ~4× the smallest rate's packet-per-MTU
// comfortably inside tbf's limit ceiling.
//
// When EgressMbit == 0 the caller MUST skip TcCommands entirely (see
// Manager.setupNetwork's `if nc.EgressMbit > 0` guard). Returning an
// empty slice here would silently swallow a misconfigured cap; refusing
// to run the argv when zero is the clearer behaviour.
func (c Config) TcCommands() [][]string {
	return [][]string{
		{"tc", "qdisc", "add", "dev", c.VethHost, "root", "tbf",
			"rate", fmt.Sprintf("%dmbit", c.EgressMbit),
			"burst", "32kbit", "latency", "400ms"},
	}
}

// TcResetCommands returns the best-effort argv list that removes any
// existing root qdisc on VethHost before TcCommands runs. On a fresh
// boot VethHost is brand-new and `tc qdisc del` exits non-zero (no
// qdisc to delete); the caller logs and continues. On a snapshot-
// restore Wake the netns — and VethHost — outlive the VM (the qdisc
// was applied on the prior wake and the link was kept up across
// park), so `tc qdisc del` succeeds and lets the subsequent `add`
// win. Mirrors the NftResetCommands pattern (PR #36).
//
// No teardown is needed: ip link del VethHost in TeardownCommands
// drops the qdisc when it drops the link.
func (c Config) TcResetCommands() [][]string {
	return [][]string{
		{"tc", "qdisc", "del", "dev", c.VethHost, "root"},
	}
}
