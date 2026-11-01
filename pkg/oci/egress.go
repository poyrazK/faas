package oci

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
)

// Egress policy for the OCI puller (spec §11). Tenant egress is fenced by
// nftables; the puller's own HTTP client applies the same denylist in
// user-space so a misconfigured firewall never lets a public pull reach an
// internal address. The list is the conservative union of:
//
//   - RFC1918 (10/8, 172.16/12, 192.168/16)
//   - loopback (127/8)
//   - link-local (169.254/16) — covers the cloud metadata service (IMDS)
//   - IPv6 unique-local (fc00::/7) and link-local (fe80::/10)
//   - IPv4/IPv6 unspecified + multicast
//
// We DENY every denied range and only allow addresses in the public ranges.
// The transport refuses both DNS lookups that resolve into a denied range
// AND direct IPs in a denied range — closes the DNS-rebinding hole that a
// naive IP allowlist leaves open.
//
// The base list is the shared netns.DefaultDenySet so the user-space
// check and the firewall rules can't drift apart (PR-A, ADR-034).
// OCI adds a small set of client-only entries that the host firewall
// doesn't need (loopback / 0.0.0.0/8 / IETF-assigned / benchmarking /
// reserved) because the puller runs out of init — it's the process that
// happens to call into the OCI registry, and a misconfigured firewall
// plus a pull to a loopback is exactly the regression the user-space
// check is meant to catch. The OCI-only entries are listed in
// ociOnlyDenyCIDRs and union'd at dial time.
// ociOnlyDenyCIDRsV4 are ranges the OCI puller denies in addition
// to the shared netns.DefaultDenySet. The host firewall already
// covers RFC1918 / link-local / metadata via the per-netns
// chain; these are the client-side hardening extras (PR-D).
//
// Typed as []netns.DenyEntry so each entry carries the same
// ADR-backed provenance the shared catalog uses. The SourceADR
// pin is ADR-034 (the PR-A slice that introduced the typed
// catalog); the rationale follows the OCI-only rationale that
// lived as inline comments before the refactor. These entries
// are deliberately NOT in netns.NewDefaultDenySet() — they are
// process-level hardening, not platform-wide policy, and the
// host firewall / per-netns renderer do not need them.
// OCIOnlyDenyCIDRsV4 returns a copy of the OCI-only client-hardening
// entries as provenance-bearing netns.DenyEntry records. Exported
// (PR-D feedback) so cmd/denylist-md can consume the typed slice
// directly instead of maintaining a duplicate literal table — the
// previous shape had a silent drift surface (a future edit to
// ociOnlyDenyCIDRsV4 below would not auto-update the generated
// docs/denylist.md). The accessor returns a copy so the caller
// cannot mutate the runtime union. Read-only contract.
func OCIOnlyDenyCIDRsV4() []netns.DenyEntry {
	out := make([]netns.DenyEntry, len(ociOnlyDenyCIDRsV4))
	copy(out, ociOnlyDenyCIDRsV4)
	return out
}

// OCIOnlyDenyCounterLabels returns the (counterName, family) tuples
// for every OCI-only client-hardening entry. The paired projection
// exists so cmd/imaged can pre-instantiate the imaged-side mirror
// counter without importing pkg/netns directly (depguard boundary:
// pkg/oci is the only egress-policy surface non-vmmd daemons are
// allowed to depend on). The catalog portion of the labels is
// already pre-instantiated by wire.NewOpsMetrics("imaged"); this
// helper adds the OCI-only extras on top.
func OCIOnlyDenyCounterLabels() []CounterLabel {
	out := make([]CounterLabel, 0, len(ociOnlyDenyCIDRsV4))
	for _, e := range ociOnlyDenyCIDRsV4 {
		out = append(out, CounterLabel{
			CounterName: netns.DropCounterName(e.Family, e.Prefix.String()),
			Family:      e.Family.String(),
		})
	}
	return out
}

// CounterLabel is the (CounterName, Family) pair that the imaged
// /metrics counter expects. Returned by OCIOnlyDenyCounterLabels so
// the OCI package is the only place the catalog-projection logic
// lives.
type CounterLabel struct {
	CounterName string
	Family      string
}

var ociOnlyDenyCIDRsV4 = []netns.DenyEntry{
	{
		Family:    netns.FamilyV4,
		Prefix:    netip.MustParsePrefix("0.0.0.0/8"),
		SourceADR: "ADR-034",
		Comment:   "unspecified IPv4 source range (defence-in-depth)",
	},
	{
		Family:    netns.FamilyV4,
		Prefix:    netip.MustParsePrefix("127.0.0.0/8"),
		SourceADR: "ADR-034",
		Comment:   "loopback range; OCI puller runs outside tenant netns",
	},
	{
		Family:    netns.FamilyV4,
		Prefix:    netip.MustParsePrefix("192.0.0.0/24"),
		SourceADR: "ADR-034",
		Comment:   "IETF protocol assignments",
	},
	{
		Family:    netns.FamilyV4,
		Prefix:    netip.MustParsePrefix("198.18.0.0/15"),
		SourceADR: "ADR-034",
		Comment:   "benchmarking range",
	},
	{
		Family:    netns.FamilyV4,
		Prefix:    netip.MustParsePrefix("240.0.0.0/4"),
		SourceADR: "ADR-034",
		Comment:   "reserved IPv4 range",
	},
}

// deniedCIDRs builds the OCI deny set as the union of the shared
// netns.DefaultDenySet + the OCI-only extras above. Built once at
// init; ipAllowed reads from it on every dial. The shared set has
// been growing since PR-A (ADR-034 added 6to4 + Teredo) — a future
// editorial pass will decide whether the OCI-only entries deserve
// to land on the host side too; today the union is the cheapest
// contract that closes the regression.
//
// PR-E: the union preserves the typed DenyEntry records (with
// CounterName) instead of projecting to bare Prefix. The runtime
// deny check (ipAllowed) walks the typed slice for parity with
// netns policy.go's per-CIDR rule shape; the EgressDenyHook
// receives the matched entry's CounterName so the imaged-side
// metric has a stable, catalog-derived label.
var (
	deniedEntriesV4 = func() []netns.DenyEntry {
		base := netns.NewDefaultDenySet()
		out := make([]netns.DenyEntry, 0, len(base.V4DenyCIDRs)+len(ociOnlyDenyCIDRsV4))
		// Project the shared catalog's v4 entries (which already have
		// CounterName from netns.NewDefaultDenySet) into the union.
		for _, e := range base.Entries {
			if e.Family != netns.FamilyV4 {
				continue
			}
			out = append(out, e)
		}
		// OCI-only entries come from a typed slice but have no
		// CounterName (they're not in the catalog). Synthesise a
		// stable name using the same sanitize rule the catalog uses
		// so the metric label is deterministic across runs.
		for _, e := range ociOnlyDenyCIDRsV4 {
			out = append(out, netns.DenyEntry{
				Family:      e.Family,
				Prefix:      e.Prefix,
				CounterName: netns.DropCounterName(e.Family, e.Prefix.String()),
				SourceADR:   e.SourceADR,
				Comment:     e.Comment,
			})
		}
		return out
	}()
	deniedEntriesV6 = func() []netns.DenyEntry {
		base := netns.NewDefaultDenySet()
		out := make([]netns.DenyEntry, 0, len(base.V6DenyCIDRs))
		for _, e := range base.Entries {
			if e.Family != netns.FamilyV6 {
				continue
			}
			out = append(out, e)
		}
		return out
	}()
)

// EgressDialContext returns a DialContext that rejects every address that
// resolves into a denied range. It is the transport that cmd/imaged and
// pkg/builderd plug into the OCI puller.
//
// The check happens AFTER DNS resolution so a hostname that resolves to a
// denied IP is refused (this is the same class of attack the firewall
// denylist on the box itself tries to catch — the user-space check is the
// belt-and-braces duplicate the financial model relies on: a bug in either
// layer still leaves the other holding).
func EgressDialContext(parent *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if parent == nil {
		parent = &net.Dialer{}
	}
	resolver := parent.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("oci: egress: bad addr %q: %w", addr, err)
		}
		ips, err := resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("oci: egress: resolve %s: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("oci: egress: no addresses for %s", host)
		}
		// Reject the whole dial if ANY returned address is denied — a hostname
		// that resolves to both a public and a private IP would otherwise be
		// race-able, and the §11 policy is the conservative deny.
		for _, ipa := range ips {
			addr, ok := netip.AddrFromSlice(ipa.IP)
			if !ok {
				return nil, fmt.Errorf("oci: egress: unparseable address for %s", host)
			}
			addr = addr.Unmap()
			if !ipAllowed(addr) {
				// PR-E: surface the matched catalog entry to the
				// EgressDenyHook so cmd/imaged can emit a labelled
				// counter (oci_egress_deny_total{cidr,family}) —
				// matches the vmmd-side per-CIDR deny counter
				// emitted by the nft poll adapter. nil hook is safe
				// (no-op) so existing callers that don't care about
				// the metric keep working unchanged.
				if EgressDenyHook != nil {
					if entry := matchedDenyEntry(addr); entry.CounterName != "" {
						EgressDenyHook(addr, entry.CounterName, entry.Family.String())
					}
				}
				// ADR-021: lift the egress-denial failure mode to a
				// sentinel that pkg/api.SentinelToCode maps to the
				// RFC 7807 CodeImageEgressDenied (security-class
				// signal, 403). The legacy ErrEgressDenied sentinel
				// below is wrapped inside this one for backwards
				// compat — pkg/oci consumers that already check
				// errors.Is(err, ErrEgressDenied) continue to work,
				// and pkg/api.SentinelToCode picks up the new
				// canonical sentinel.
				return nil, fmt.Errorf("%w: %w: address %s (%s)",
					ErrImageEgressDenied, ErrEgressDenied, host, addr)
			}
		}
		// Dial the first public IP explicitly to defeat DNS rebinding across
		// the resolution/dial gap.
		return parent.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

// NewEgressHTTPClient returns an *http.Client that uses EgressDialContext as
// its transport dialer. Callers who already have an http.Client with options
// (proxy, timeouts) can build one with http.DefaultTransport and override
// only the dial hook:
//
//	tr := &http.Transport{DialContext: oci.EgressDialContext(nil)}
//	hc := &http.Client{Transport: tr, Timeout: 30*time.Second}
func NewEgressHTTPClient() *http.Client {
	return &http.Client{Transport: newEgressTransport(EgressDialContext(nil))}
}

// Connection-pool bounds for every egress transport. A bare
// http.Transport keeps an idle keep-alive connection open until the far
// end closes it (IdleConnTimeout zero means "never") and puts no ceiling
// on the idle pool, so a process that talks to many customer-chosen hosts
// (webhook receivers, log drains, JWKS URLs) held one parked connection,
// two goroutines and a file descriptor per host for as long as each
// receiver tolerated it. The values match http.DefaultTransport except
// MaxIdleConnsPerHost, which is raised from 2 so a burst of deliveries to
// one receiver can reuse its connections. Request deadlines stay with each
// caller (client Timeout, or the registry's per-phase timeouts).
const (
	EgressIdleConnTimeout     = 90 * time.Second
	EgressMaxIdleConns        = 100
	EgressMaxIdleConnsPerHost = 4
)

// newEgressTransport is the one place the egress transports are built, so
// the guarded client and its loopback test twin pool connections alike.
// It never reads proxy settings from the environment: a proxy would carry
// the request past the dial-time guard.
func newEgressTransport(dial func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Transport {
	return &http.Transport{
		DialContext:         dial,
		IdleConnTimeout:     EgressIdleConnTimeout,
		MaxIdleConns:        EgressMaxIdleConns,
		MaxIdleConnsPerHost: EgressMaxIdleConnsPerHost,
	}
}

// NewEgressHTTPClientAllowLoopback is the dev/test-only escape hatch
// from the §11 egress policy. It builds an HTTP client whose
// DialContext is the standard net.Dialer (no deny check) so a
// webhook receiver on 127.0.0.1 can be reached. The companion
// helper EgressAllowLoopbackFromEnv() gates the env var
// FAAS_EGRESS_ALLOW_LOOPBACK=1 that callers (pkg/webhookout.NewDispatcher)
// consult before choosing this client over the production one.
//
// DO NOT call this from production code paths. The whole point of
// the dial-time SSRF guard is that no customer-supplied URL can
// reach loopback/RFC1918/metadata — this helper bypasses that.
//
// Returns nil if the env var is unset so a typo or a missing env
// passthrough silently falls back to NewEgressHTTPClient (the safe
// default).
func NewEgressHTTPClientAllowLoopback() *http.Client {
	if !EgressAllowLoopbackFromEnv() {
		return nil
	}
	return &http.Client{Transport: newEgressTransport((&net.Dialer{}).DialContext)}
}

// EgressAllowLoopbackFromEnv reports whether the dial-time SSRF
// guard is opted out via the test-only env var
// FAAS_EGRESS_ALLOW_LOOPBACK=1. The check is exact-match
// (no case folding) and tolerates no extra characters so a typo
// ("=true", "=yes") defaults to "deny loopback" — the safe
// fallback. The constant lives in pkg/oci so cmd/meterd and
// pkg/webhookout can both read it without duplicating the env key.
func EgressAllowLoopbackFromEnv() bool {
	return os.Getenv("FAAS_EGRESS_ALLOW_LOOPBACK") == "1"
}

// ipAllowed reports whether ip is in a publicly routable range. It is the
// single place the egress policy is enforced; add a new denied range to
// deniedEntriesV4 / deniedEntriesV6 and the test in egress_test.go picks it up.
func ipAllowed(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	if ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
		return false
	}
	denied := deniedEntriesV4
	if !ip.Is4() && !ip.Is4In6() {
		denied = deniedEntriesV6
	}
	for _, e := range denied {
		if e.Prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// matchedDenyEntry walks the OCI deny union (preserving typed DenyEntry
// records with CounterName) and returns the first entry whose prefix
// contains ip. The zero DenyEntry is returned when no match is found
// (which ipAllowed has already verified cannot happen at the call
// sites that invoke this helper).
//
// Used by EgressDialContext to feed EgressDenyHook a stable CounterName
// for the metric label; without this helper the hook would only see the
// raw target address, which has unbounded cardinality (every public IP
// the OCI puller touches becomes a separate series).
func matchedDenyEntry(ip netip.Addr) netns.DenyEntry {
	if !ip.IsValid() {
		return netns.DenyEntry{}
	}
	denied := deniedEntriesV4
	if !ip.Is4() && !ip.Is4In6() {
		denied = deniedEntriesV6
	}
	for _, e := range denied {
		if e.Prefix.Contains(ip) {
			return e
		}
	}
	return netns.DenyEntry{}
}

// EgressIPAllowed reports whether addr is permitted by the OCI
// puller's egress policy. It is the testable mirror of the
// internal ipAllowed predicate (PR-D) and exists so external
// packages — notably pkg/netns_test — can assert the shared
// catalog is consumed by the OCI dialer without exposing the
// internal slices or the resolver/dial plumbing. Adding a new
// catalog entry to netns.NewDefaultDenySet() is automatically
// picked up here because deniedEntriesV4 / deniedEntriesV6 are built
// from that source at init.
func EgressIPAllowed(addr netip.Addr) bool { return ipAllowed(addr) }

// ErrEgressDenied is returned (wrapped) when a dial target violates the
// §11 policy. Callers can errors.Is against it.
var ErrEgressDenied = errors.New("oci: egress denied")

// EgressDenyHook is the PR-E dialer-side observability hook: invoked
// from EgressDialContext when a target address is denied by the
// user-space egress policy (nftables does NOT see this path — the
// dialer refuses before any kernel-layer counter could fire). The
// hook is set once at imaged startup; nil is safe and the dialer
// silently skips the metric emit (preserves the pre-PR-E behaviour
// for any caller that hasn't wired the hook yet).
//
// Parameters:
//
//   - target is the denied IP that triggered the refusal. The hook
//     is invoked with the post-Unmap() form (v4-in-v6 collapse).
//   - counterName is the catalog-derived DenyEntry.CounterName
//     (e.g. "drop_v4_10_0_0_0_8") — a stable, bounded label.
//   - family is the nft family keyword for the matching entry
//     ("ip" / "ip6") — same convention as netns.Family.String().
//
// Concurrency: the dialer is called concurrently by every imaged
// goroutine. Hooks MUST be safe for concurrent invocation; the
// typical wiring is a Prometheus counter Inc() (atomic on the
// caller's side). The hook is invoked from inside the dialer
// goroutine BEFORE the dial error is returned, so a slow hook
// delays the dial — keep the hook fast (counter Inc only).
var EgressDenyHook func(target netip.Addr, counterName, family string)
