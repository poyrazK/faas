// vmmd overlay IP detection (Mega-PR-B Commit 3, PR scale-out
// tier-1 residual Gap #5).
//
// Wraps `tailscale ip -4` with CIDR-preference scoring so a multi-NIC
// host (subnet router, exit node) picks the IP that lives in the
// operator-declared overlay subnet, not whichever line tailscale
// happened to print first. Falls back to the legacy first-line
// behavior when no candidate matches PreferCIDR — preserves the v1
// single-host dev path.
//
// PR scale-out tier-1 residual (Gap #5): when the operator sets
// PinnedInterface, the detector pins to that NIC's IPv4 address
// before falling back to the CIDR-scoring path. Operators with
// multiple NICs (LAN + tail/wg) on one host disambiguate via
// PinnedInterface. When the pinned NIC has no IPv4 address, the
// detector falls through to the CIDR-scoring path (preserves the
// v1 contract — never silently fail).
//
// The detector is split out of defaultDetectOverlayIP so the
// scoring logic is unit-testable without shelling out; production
// callers go through defaultDetectOverlayIP, which constructs the
// zero-value detector.

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strings"
)

// OverlayDetector bundles the knobs that change how we pick an IP
// out of the tailscale response. Zero-value is the v1 behavior:
//
//   - TailscaleBinaryPath: LookPath("tailscale")
//   - PreferCIDR: empty (no preference)
//   - PinnedInterface: empty (no pin)
//   - Run: cmd.Output-style exec.CommandContext("tailscale", "ip", "-4")
//
// Tests vary the knobs to cover each scoring branch without
// shelling out.
type OverlayDetector struct {
	// TailscaleBinaryPath overrides exec.LookPath("tailscale"). Used
	// in tests to inject a binary that lives at a known path (or to
	// force LookPath failure without polluting PATH).
	TailscaleBinaryPath string

	// PreferCIDR is the overlay subnet that scores a candidate IP
	// higher than the rest. Empty prefix (zero value) means "no
	// preference" — every candidate scores equal and the first line
	// wins (v1 behavior). Setting this to api.DefaultOverlayCIDR()
	// yields the Tailscale-friendly selector; WireGuard/VPC overlays
	// pass the operator's AllowedIPs here.
	PreferCIDR netip.Prefix

	// PinnedInterface is the operator-pinned NIC whose IPv4 address
	// the detector returns. Empty means "no pin" — the detector
	// falls through to PreferCIDR scoring (the v1 contract). Set
	// via [compute_node].overlay_interface in vmmd.toml (or the
	// FAAS_OVERLAY_INTERFACE env overlay). Operators with multiple
	// NICs (LAN + tail/wg) on one host set this to disambiguate.
	// PR scale-out tier-1 residual (Gap #5). When the pinned NIC
	// has no IPv4 address, the detector falls through to the
	// PreferCIDR scoring path (the same fallback posture as an
	// unset PinnedInterface).
	PinnedInterface string

	// Run produces the raw `tailscale ip -4` output. nil means
	// defaultDetectOverlayIP's production shell-out. Tests inject a
	// stub that returns canned bytes (no exec, no env mutation).
	Run func(ctx context.Context) ([]byte, error)
}

// pinnedInterfaceIPFunc is the test seam for readPinnedInterfaceIP.
// Production is exec.CommandContext("ip", "-4", "-o", ...); tests
// inject a stub that returns canned values so the detector can be
// exercised without the `ip` binary on PATH. PR scale-out
// tier-1 residual (Gap #5).
var pinnedInterfaceIPFunc = readPinnedInterfaceIP

// detectOverlayIP picks the best IP from `tailscale ip -4` for the
// given PreferCIDR. Returns ("", nil) when tailscale isn't on PATH
// (preserves the legacy soft-success), ("", err) on actual exec
// failure or empty output (legacy behavior), or the highest-scoring
// IP (the first line, in iteration order, when multiple candidates
// tie on PreferCIDR).
//
// PR scale-out tier-1 residual (Gap #5): when det.PinnedInterface
// is set, the detector first tries `ip -4 -o addr show dev <iface>`
// to read that NIC's IPv4 address. On a hit the function returns
// immediately without consulting tailscale. On a miss (NIC missing
// or no IPv4 address on the pinned NIC) the function falls through
// to the existing tailscale + PreferCIDR scoring path — preserves
// the v1 contract that the detector never silently fails.
func detectOverlayIP(ctx context.Context, det OverlayDetector) (string, error) {
	// Gap #5 pin path — runs BEFORE the tailscale shell-out so an
	// operator-pinned NIC wins over CIDR scoring (operator intent
	// is the load-bearing signal). Errors from the pin path fall
	// through to the tailscale scoring path rather than fail the
	// boot — operators running vmmd in a chroot that lacks `ip`
	// (some test harnesses) keep the existing behavior.
	if det.PinnedInterface != "" {
		if ip, ok, err := pinnedInterfaceIPFunc(ctx, det.PinnedInterface); err == nil && ok {
			return ip.String(), nil
		}
	}
	binary := det.TailscaleBinaryPath
	if binary == "" {
		lp, err := exec.LookPath("tailscale")
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				return "", nil
			}
			return "", fmt.Errorf("tailscale LookPath: %w", err)
		}
		binary = lp
	}
	if !det.PreferCIDR.IsValid() && det.Run != nil {
		_ = det.PreferCIDR // intentional no-op: PreferCIDR empty + Run set is the "force stub, no scoring" path used by tests; the comment block above documents the rationale.
	}
	runner := det.Run
	if runner == nil {
		command := exec.CommandContext(ctx, binary, "ip", "-4")
		command.Env = append(os.Environ(), "TS_NO_LOGS_NO_SUPPORT=true")
		runner = func(ctx context.Context) ([]byte, error) {
			return command.Output()
		}
	}
	out, err := runner(ctx)
	if err != nil {
		return "", fmt.Errorf("tailscale ip -4: %w", err)
	}
	if len(out) == 0 {
		return "", errors.New("tailscale ip -4 returned empty")
	}
	addrs, err := parseTailscaleIPLines(out)
	if err != nil {
		return "", err
	}
	if len(addrs) == 0 {
		return "", errors.New("tailscale ip -4 returned no IPv4 candidates")
	}
	best := scoreByCIDR(addrs, det.PreferCIDR)
	return best.String(), nil
}

// readPinnedInterfaceIP shells out to `ip -4 -o addr show dev <iface>`
// and returns the FIRST IPv4 address on that NIC. Returns
// (zero, false, nil) when:
//
//   - the `ip` binary is not on PATH (test chroots, slim prod images)
//   - the pinned NIC does not exist
//   - the pinned NIC has no IPv4 address (v6-only is rare but legal)
//
// In all three cases the caller falls through to the tailscale +
// PreferCIDR scoring path. The fallback posture is intentional:
// the pinned interface is an operator hint, not a hard contract —
// the detector's "never silently fail" posture (v1 contract) wins.
//
// Returns (zero, false, err) on actual exec failures (e.g. the `ip`
// binary exists but rejects the dev argument). The caller also
// falls through to the tailscale path on err — err is surfaced
// only via slog at the caller's discretion.
func readPinnedInterfaceIP(ctx context.Context, iface string) (netip.Addr, bool, error) {
	ipPath, err := exec.LookPath("ip")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return netip.Addr{}, false, nil
		}
		return netip.Addr{}, false, fmt.Errorf("ip LookPath: %w", err)
	}
	command := exec.CommandContext(ctx, ipPath, "-4", "-o", "addr", "show", "dev", iface)
	out, err := command.Output()
	if err != nil {
		// `ip ... dev <missing>` exits non-zero with stderr
		// "Device does not exist". That is a "fall through to
		// scoring" signal, not a hard error.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return netip.Addr{}, false, nil
		}
		return netip.Addr{}, false, fmt.Errorf("ip -4 -o addr show dev %s: %w", iface, err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		// Each line of `ip -4 -o addr show dev <iface>` looks like:
		//   <idx>: <iface> inet <addr>/<mask> brd ...
		// The `-o` flag is the one-line format — fields are space-
		// delimited. We grab the field AFTER `inet` (the v4 address
		// with prefix length) and strip the prefix.
		fields := strings.Fields(scanner.Text())
		for i := 0; i < len(fields)-1; i++ {
			if fields[i] != "inet" {
				continue
			}
			rawAddr := strings.SplitN(fields[i+1], "/", 2)[0]
			addr, perr := netip.ParseAddr(rawAddr)
			if perr != nil {
				continue
			}
			if !addr.Is4() {
				continue
			}
			return addr, true, nil
		}
	}
	if serr := scanner.Err(); serr != nil {
		return netip.Addr{}, false, fmt.Errorf("scan ip output: %w", serr)
	}
	return netip.Addr{}, false, nil
}

// parseTailscaleIPLines turns the multi-line `tailscale ip -4`
// output into a slice of IPv4 candidates. Skips blank lines and
// IPv6 addresses (the `-4` flag already filters, but a misconfigured
// tailscale.conf that hands us a v6-only answer still produces no
// parse error — we just return an empty slice). Trailing whitespace
// is trimmed per line. Garbage lines that aren't parseable IPs are
// an error, so a corrupted tailscale.conf doesn't silently flip to
// a "no candidates" answer.
func parseTailscaleIPLines(out []byte) ([]netip.Addr, error) {
	var addrs []netip.Addr
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		ip, err := netip.ParseAddr(line)
		if err != nil {
			return nil, fmt.Errorf("parse tailscale line %q: %w", line, err)
		}
		if !ip.Is4() {
			// Skip IPv6 candidates silently — a v6-only tailscale
			// output is valid (`-4` plus v6 happens on dual-stack
			// exit nodes), and the legacy first-line contract only
			// ever expected v4.
			continue
		}
		addrs = append(addrs, ip)
	}
	return addrs, nil
}

// scoreByCIDR picks the candidate that lives in det.PreferCIDR
// when one (or more) candidates match. When det.PreferCIDR is
// invalid (zero-value), every candidate scores equal and addrs[0]
// wins (v1 first-line behavior). When two or more candidates tie
// on scoring, the one earlier in `addrs` wins (stable order — the
// caller can rely on this for assertion).
func scoreByCIDR(addrs []netip.Addr, prefer netip.Prefix) netip.Addr {
	if len(addrs) == 0 {
		return netip.Addr{}
	}
	if !prefer.IsValid() {
		return addrs[0]
	}
	for _, a := range addrs {
		if prefer.Contains(a) {
			return a
		}
	}
	// No candidate matched; fall back to first-line so a misconfigured
	// CIDR doesn't lose the v1 contract (an operator who forgot to
	// set overlay.cidr still gets the host's Tailscale IP).
	return addrs[0]
}
