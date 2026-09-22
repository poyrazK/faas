package netns

// Egress circuit breaking (ADR-201 §3).
//
// When an app's declared upstream is proven unhealthy by the ADR-098 probe,
// schedd opens a circuit and the guest's connections to that (address, port)
// are REJECTED rather than allowed to hang. That distinction is the entire
// product value: a blackholed dependency otherwise costs the app a full TCP
// connect timeout on every request, which burns the kind=budget deadline and
// then holds the wake slot, turning one dependency outage into a per-app
// capacity outage. `reject with tcp reset` makes the guest's own error
// handling run in microseconds instead.
//
// Why a named set rather than inline rules: a circuit opens and closes on a
// 30 s probe cadence, while the per-netns ruleset is rendered once at wake.
// A named set can be updated live with `nft add element` / `nft delete
// element` without re-rendering anything, so a circuit transition never
// touches the rest of the tenant's firewall.
//
// §11 posture: this adds a DENY, never an allow. It cannot widen a tenant's
// reachable surface, it holds no plaintext, and it terminates no TLS — it is
// the same mechanism §11 already uses for ports 25/465/587 and the RFC1918
// lateral-movement block.

import (
	"fmt"
	"net/netip"
	"strconv"
)

const (
	// EgressCircuitSetName is the named nftables set holding the currently
	// open (address, port) circuits for one instance's netns.
	EgressCircuitSetName = "egress_circuit_open"
	// EgressCircuitCounter observes packets rejected by an open circuit, so
	// an operator can tell "the breaker is working" from "the app stopped
	// calling its database". Surfaces as egress_circuit_rejects_total.
	EgressCircuitCounter = "reject_egress_circuit"
)

// EgressCircuitTarget is one open circuit: a resolved upstream address and
// port whose connections must fail fast.
//
// The address is resolved, not a hostname, because nftables matches packets.
// A DNS change while the circuit is open is picked up at the next half-open
// probe, which re-resolves — that staleness window is bounded by the probe
// interval and is the reason half-open exists.
type EgressCircuitTarget struct {
	Addr netip.Addr
	Port int
}

// Valid reports whether the target can be rendered into a set element. IPv6
// is accepted but rendered into the ip6 table; an invalid address or an
// out-of-range port renders nothing rather than emitting a malformed rule
// that would fail the whole nft batch.
func (t EgressCircuitTarget) Valid() bool {
	return t.Addr.IsValid() && t.Port > 0 && t.Port <= 65535
}

// element renders the set element form, e.g. "10.1.2.3 . 5432".
func (t EgressCircuitTarget) element() string {
	return t.Addr.String() + " . " + strconv.Itoa(t.Port)
}

// egressCircuitRules declares the set, its counter and the reject rule for
// one address family. Emitted only when the feature is enabled, so a node
// with FAAS_EGRESS_CIRCUIT_BREAKER off produces byte-identical per-netns
// output to the pre-ADR-201 renderer.
//
// The rule is placed by the caller AFTER the established/related accept —
// an open circuit must not tear down connections the guest already has, only
// refuse new ones. Killing established connections would turn a recoverable
// blip into a guaranteed request failure for every in-flight call.
func (c Config) egressCircuitRules(nft func(...string) []string, family string) [][]string {
	if !c.EgressCircuitEnabled {
		return nil
	}
	addrType := "ipv4_addr"
	if family == "ip6" {
		addrType = "ipv6_addr"
	}
	return [][]string{
		nft("add", "set", family, "faas", EgressCircuitSetName,
			"{", "type", addrType, ".", "inet_service", ";", "}"),
		nft("add", "counter", family, "faas", EgressCircuitCounter, "{}"),
		nft("add", "rule", family, "faas", "forward", "iifname", c.Tap,
			family, "daddr", ".", "tcp", "dport", "@"+EgressCircuitSetName,
			"counter", "name", EgressCircuitCounter,
			"reject", "with", "tcp", "reset"),
	}
}

// EgressCircuitSetCommands renders the argv that makes this instance's
// circuit set exactly `targets` — a flush followed by one add per family.
//
// Whole-set convergence, NOT add/delete deltas. Two reasons, both load-bearing:
//
//  1. `nft delete element` errors when the element is absent. After a vmmd
//     restart the netns is re-rendered with an EMPTY set while schedd still
//     believes circuits are open, so every close would fail forever against a
//     set that was already in the desired state.
//  2. Flush + add converges from any prior state, so a restart, a missed
//     notify, or a partially-applied batch self-heals on the next reconcile
//     instead of needing a repair path.
//
// This also matches UpdateEgressAllowlist, which pushes the whole list rather
// than a delta — one convention for live netns mutation.
//
// An empty target list still emits the flush: "no open circuits" is a real
// desired state and is how a close is expressed.
func (c Config) EgressCircuitSetCommands(targets []EgressCircuitTarget) [][]string {
	if !c.EgressCircuitEnabled {
		return nil
	}
	nx := []string{"ip", "netns", "exec", c.Netns, "nft"}
	nft := func(parts ...string) []string { return append(append([]string{}, nx...), parts...) }

	var v4, v6 []string
	for _, t := range targets {
		if !t.Valid() {
			continue
		}
		if t.Addr.Is4() {
			v4 = append(v4, t.element())
			continue
		}
		v6 = append(v6, t.element())
	}
	// Flush both families unconditionally. Flushing only the families that
	// have new elements would strand a v6 circuit when the desired set drops
	// to v4-only.
	cmds := [][]string{
		nft("flush", "set", "ip", "faas", EgressCircuitSetName),
		nft("flush", "set", "ip6", "faas", EgressCircuitSetName),
	}
	if len(v4) > 0 {
		cmds = append(cmds, nft("add", "element", "ip", "faas", EgressCircuitSetName,
			"{", joinElements(v4), "}"))
	}
	if len(v6) > 0 {
		cmds = append(cmds, nft("add", "element", "ip6", "faas", EgressCircuitSetName,
			"{", joinElements(v6), "}"))
	}
	return cmds
}

func joinElements(elems []string) string {
	out := ""
	for i, e := range elems {
		if i > 0 {
			out += ", "
		}
		out += e
	}
	return out
}

// ParseEgressCircuitTarget builds a target from a host address string and
// port, rejecting anything that would render into a malformed element.
func ParseEgressCircuitTarget(addr string, port int) (EgressCircuitTarget, error) {
	parsed, err := netip.ParseAddr(addr)
	if err != nil {
		return EgressCircuitTarget{}, fmt.Errorf("netns: egress circuit target %q: %w", addr, err)
	}
	t := EgressCircuitTarget{Addr: parsed, Port: port}
	if !t.Valid() {
		return EgressCircuitTarget{}, fmt.Errorf("netns: egress circuit target %s:%d is out of range", addr, port)
	}
	return t, nil
}
