// Command faas-nft-render prints the host nftables ruleset to stdout.
//
// Used by `make egress-render` to (re)generate the checked-in artifact at
// `deploy/ansible/roles/nftables/files/policy_nftables.conf`. The artifact is
// what ansible copies onto the host at `make bootstrap` time; this binary is
// the single source of truth.
//
// Per-host rendering (ADR-055). The renderer accepts four optional
// overrides:
//
//   - --public-iface <name>          (env: FAAS_PUBLIC_IFACE)
//   - --masquerade-cidr <cidr>       (env: FAAS_MASQUERADE_CIDR)
//   - --overlay-cidr <cidr>          (env: FAAS_OVERLAY_CIDRS, repeatable,
//     comma-separated; multi-host mesh)
//   - --masquerade-cidr-v6 <cidr>    (env: FAAS_MASQUERADE_CIDR_V6)
//
// When all are unset, the render uses `pkg/netns.DefaultHostPolicy`
// (the EX44 default-local node shape: `eth0` + `10.100.0.0/16`,
// no overlay, no v6). A Hetzner compute node on a different NIC
// name (e.g. `ens5`) overrides via the flag or env so the
// rendered artifact matches the per-host deployment. `make
// egress-render` + `make egress-render-cross-check` + `make
// egress-render-matrix` exercise every branch (default +
// non-default public_iface + overlay + v6).
//
// Flag precedence: explicit flag > env var > `DefaultHostPolicy`.
// --overlay-cidr is repeatable; the env var is a comma-separated
// list. Both forms collapse to the []string HostPolicy.OverlayCIDRs
// field that the renderer iterates.
//
// stdout, exit 0 only. Failure to render panics — that's a build-time bug,
// not a runtime concern.
package main

import (
	"flag"
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/netns"
)

func main() {
	// Default-empty string + explicit fallback inside render via
	// `DefaultHostPolicy`: an empty flag/env means "use the Go
	// default", which is the source-of-truth for the EX44 shape.
	// We don't fail-open on garbage here because the renderer
	// itself panics on empty required fields (policy.go:111-120).
	publicIface := flag.String("public-iface", "",
		"host's outward-facing NIC (e.g. eth0, ens5). Defaults to pkg/netns.DefaultHostPolicy.PublicIface (=eth0). Env: FAAS_PUBLIC_IFACE.")
	masqueradeCIDR := flag.String("masquerade-cidr", "",
		"source-address CIDR for postrouting MASQUERADE (e.g. 10.100.0.0/16). Defaults to pkg/netns.DefaultHostPolicy.MasqueradeCIDR (=10.100.0.0/16). Env: FAAS_MASQUERADE_CIDR.")
	var overlayCIDRs multiFlag
	flag.Var(&overlayCIDRs, "overlay-cidr",
		"per-host overlay CIDR (e.g. 100.64.0.0/14). Repeatable; the renderer emits one accept + one MASQUERADE per entry. Env: FAAS_OVERLAY_CIDRS (comma-separated).")
	masqueradeCIDRv6 := flag.String("masquerade-cidr-v6", "",
		"source-address v6 CIDR for the postrouting v6 MASQUERADE sibling (e.g. fc00::/7). Defaults to empty (no v6 rule emitted). Env: FAAS_MASQUERADE_CIDR_V6.")
	var overlayExceptions multiFlag
	flag.Var(&overlayExceptions, "overlay-exception",
		"PR scale-out tier-1 residual (Gap #4): CIDR accepted BEFORE the §11 deny block on the host forward chain. Operators using an RFC1918 overlay (e.g. 10.42.0.0/24) declare the exception here. Env: FAAS_OVERLAY_EXCEPTIONS (comma-separated). Each entry is parsed via netip.ParsePrefix; malformed CIDRs fail at startup.")
	dangerAccept := flag.Bool("danger-accept-rfc1918-lateral-movement", false,
		"PR scale-out tier-1 residual (Gap #4): enable the deny-set exception path. When true, the renderer emits per-CIDR accept rules BEFORE the §11 deny block. Default: false. Operators using an RFC1918 overlay MUST set this AND list the overlay CIDR in --overlay-exception; the manifest schema enforces the same pair at the DB CHECK constraint level.")
	flag.Parse()

	policy := netns.DefaultHostPolicy
	if iface := pickValue(*publicIface, "FAAS_PUBLIC_IFACE"); iface != "" {
		policy.PublicIface = iface
	}
	if cidr := pickValue(*masqueradeCIDR, "FAAS_MASQUERADE_CIDR"); cidr != "" {
		policy.MasqueradeCIDR = cidr
	}
	// Overlay is a slice; merge flag-set + env-var (comma-split) so
	// the make egress-render-matrix target can pass either via env
	// (matrix iterates fork-only) or via flag (single-call).
	merged := append([]string(nil), overlayCIDRs...)
	if env := os.Getenv("FAAS_OVERLAY_CIDRS"); env != "" {
		merged = append(merged, strings.Split(env, ",")...)
	}
	if len(merged) > 0 {
		policy.OverlayCIDRs = merged
	}
	if cidr := pickValue(*masqueradeCIDRv6, "FAAS_MASQUERADE_CIDR_V6"); cidr != "" {
		policy.MasqueradeCIDR6 = cidr
	}
	// Gap #4: deny-set exception path. Operators using an RFC1918
	// overlay (e.g. 10.42.0.0/24) MUST pair --danger-accept-rfc1918-
	// lateral-movement with at least one --overlay-exception; the
	// same pair is enforced at the manifest validator level (the
	// DB CHECK constraint forces the same pair at the row level).
	// The flag is the CLI escape hatch — the manifest schema flag
	// name is the load-bearing safety in code review (operators
	// skim it before flipping).
	if *dangerAccept {
		mergedExceptions := append([]string(nil), overlayExceptions...)
		if env := os.Getenv("FAAS_OVERLAY_EXCEPTIONS"); env != "" {
			mergedExceptions = append(mergedExceptions, strings.Split(env, ",")...)
		}
		if len(mergedExceptions) == 0 {
			fmt.Fprintln(os.Stderr, "faas-nft-render: --danger-accept-rfc1918-lateral-movement set but no --overlay-exception entries (and FAAS_OVERLAY_EXCEPTIONS empty). Pair them.")
			os.Exit(1)
		}
		for _, ex := range mergedExceptions {
			p, err := netip.ParsePrefix(strings.TrimSpace(ex))
			if err != nil {
				fmt.Fprintf(os.Stderr, "faas-nft-render: --overlay-exception %q: %v\n", ex, err)
				os.Exit(1)
			}
			policy.OperatorExceptions = append(policy.OperatorExceptions, p)
		}
	} else if len(overlayExceptions) > 0 || os.Getenv("FAAS_OVERLAY_EXCEPTIONS") != "" {
		// Operator listed exceptions but didn't flip the flag —
		// refuse to render silently. The pair IS the contract;
		// silently dropping exceptions would surprise the operator.
		fmt.Fprintln(os.Stderr, "faas-nft-render: --overlay-exception entries present without --danger-accept-rfc1918-lateral-movement; the flag is the gate.")
		os.Exit(1)
	}

	if _, err := os.Stdout.WriteString(policy.Render()); err != nil {
		// Render() never returns an error today; this branch is the future
		// hook if the renderer ever returns one.
		fmt.Fprintln(os.Stderr, "faas-nft-render: write stdout:", err)
		os.Exit(1)
	}
}

// pickValue returns the explicit flag value if non-empty, otherwise
// the env var (which may itself be empty). The renderer's panic-on-
// empty contract (policy.go:111-120) means an empty result here
// falls back to the package default downstream.
func pickValue(flagVal, envName string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv(envName)
}

// multiFlag is a flag.Value that accumulates repeated --overlay-cidr
// occurrences into a slice. The flag package's default string slice
// would also work but appends would dedupe identically; the multi
// form lets the matrix express "two overlays" with two --overlay-cidr
// flags rather than quoting a comma-separated env.
type multiFlag []string

func (m *multiFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
