// Package role is the per-daemon box-role gate for Gate-B cross-box
// mTLS hardening (issue #297, ADR-051 follow-on).
//
// A box role is the deployment-shape a control-plane node inhabits:
//
//   - RoleSingleBox: a single host running every daemon. Pre-Gate-B
//     posture. Every daemon's allow-list includes this role.
//   - RoleControlPlane: a multi-box install where this host runs the
//     control-plane daemons (apid, schedd, gatewayd-public, githubd,
//     meterd). Compute-only daemons (vmmd, gatewayd-internal,
//     builderd, imaged) must refuse to start.
//   - RoleComputeOnly: a multi-box install where this host runs the
//     compute-only daemons. Control-plane daemons must refuse to
//     start.
//
// The role is set at deploy time via host_vars (faas_box_role) and
// surfaces in two places per daemon:
//
//   - The `[faas].role` TOML field on each Config struct that has
//     one (the daemons with a config.go: schedd, vmmd, meterd,
//     builderd, githubd, gatewayd-internal).
//   - An env override (FAAS_<DAEMON>_ROLE) for daemons that don't
//     have a config.go today (apid, imaged, gatewayd-public). The
//     env wins when set; empty env falls back to TOML; empty TOML
//     resolves to RoleSingleBox.
//
// The refusal gate is independent from the mTLS handshake layer
// (ADR-052 + ADR-056). Gate-B is operational scaffolding: a daemon
// refuses to start when its observed role is not in its allow-list,
// so an operator who runs the wrong systemd unit on the wrong box
// fails fast and visibly rather than silently masking the
// misconfiguration with a default-local row.
//
// Single-box dev (make bootstrap against 127.0.0.1) keeps working
// because every daemon's allow-list includes RoleSingleBox and the
// default resolves to RoleSingleBox.
package role

import (
	"fmt"
	"os"
	"slices"
)

// Role is the box-side shape a control-plane node inhabits. The
// string values are stable on-disk representations: TOML keys,
// host_vars values, and env vars all carry the same canonical
// spelling. Do not rename without updating every reader.
type Role string

const (
	// RoleSingleBox is the pre-Gate-B single-host posture. Every
	// daemon's allow-list includes this role, so a single-box dev
	// install boots without any role configuration.
	RoleSingleBox Role = "single-box"

	// RoleControlPlane is the multi-box shape where this host
	// runs the control-plane daemons (apid, schedd,
	// gatewayd-public, githubd, meterd). Compute-only daemons
	// refuse to start under this role.
	RoleControlPlane Role = "control-plane"

	// RoleComputeOnly is the multi-box shape where this host
	// runs the compute-only daemons (vmmd, gatewayd-internal,
	// builderd, imaged). Control-plane daemons refuse to start
	// under this role.
	RoleComputeOnly Role = "compute-only"
)

// AllRoles is the canonical, sorted set of valid roles. Useful for
// validation in tests and operator-facing diagnostics.
var AllRoles = []Role{RoleSingleBox, RoleControlPlane, RoleComputeOnly}

// Parse returns the Role for raw. Empty string resolves to
// RoleSingleBox (the default when no config is set). Any other
// unknown value returns a typed error listing the valid set so the
// caller can map it straight to a TOML key or env name.
func Parse(raw string) (Role, error) {
	if raw == "" {
		return RoleSingleBox, nil
	}
	r := Role(raw)
	if !slices.Contains(AllRoles, r) {
		return "", fmt.Errorf("role: unknown %q (valid: %v)", raw, AllRoles)
	}
	return r, nil
}

// parseOrPassthrough is the FromConfig-only fallback for the TOML
// value. Empty input is the default case (RoleSingleBox); unknown
// values are passed through unchanged so the daemon's Require call
// at boot surfaces the typed error. The strict Parse path is
// reserved for operator-supplied flags (--box-role on the CLI),
// where the call site is responsible for surfacing the error.
func parseOrPassthrough(raw string) Role {
	if raw == "" {
		return RoleSingleBox
	}
	r := Role(raw)
	if !slices.Contains(AllRoles, r) {
		return r
	}
	return r
}

// StrictParse is the same as Parse but rejects empty as an error.
// Useful for input that is required (e.g. an operator-supplied
// --box-role flag); not used for the box-side gate where empty
// must default to RoleSingleBox.
func StrictParse(raw string) (Role, error) {
	if raw == "" {
		return "", fmt.Errorf("role: empty (valid: %v)", AllRoles)
	}
	return Parse(raw)
}

// FromConfig resolves the observed role from a TOML value or env
// override. envKey is the env name to consult (e.g. "FAAS_SCHEDD_ROLE"
// for schedd); when the env var is non-empty, it wins over the TOML
// value. Both empty defaults to RoleSingleBox.
//
// Use this from each daemon's LoadConfig so the resulting Config
// carries a fully-resolved Role field — the caller never has to
// reconsult the env at runtime.
//
// Empty TOML empty env → RoleSingleBox. Unknown TOML / env values
// pass through unchanged so the daemon's Require call at boot can
// surface the typed error (the operator needs to see the actual
// bad value, not a silent default that masks the typo).
func FromConfig(tomlValue, envKey string) Role {
	if envKey != "" {
		if v := os.Getenv(envKey); v != "" {
			return Role(v)
		}
	}
	return parseOrPassthrough(tomlValue)
}

// ErrRefused is returned by Require when the observed role is not
// in the allow-list. The error wraps the observed + allowed list
// so operators reading the systemd journal can map the message
// straight to a host_vars fix.
type ErrRefused struct {
	Daemon   string
	Observed Role
	Allowed  []Role
}

// Error renders the refusal in the same "<daemon>: refusing to start
// as <observed> on a multi-node fleet (...)" sentence shape as
// schedd's existing default-local guard at cmd/schedd/config.go:259.
// The runbook reads the same way across every daemon.
func (e *ErrRefused) Error() string {
	return fmt.Sprintf("%s: refusing to start as role %q (allowed: %v)",
		e.Daemon, e.Observed, e.Allowed)
}

// Require refuses to start when observed is not in allow. Returning
// a typed error lets the caller decide whether to log + continue
// (none of the daemons do this) or log + exit. We follow the
// existing schedd pattern: return the error, the wire.Daemon harness
// logs it at ERROR and exits non-zero.
//
// The allow-list is variadic so the call site reads as
// `role.Require("apid", r, RoleSingleBox, RoleControlPlane)` — the
// caller names the daemon's allowed roles in the same order as the
// per-daemon role table in the runbook.
func Require(daemon string, observed Role, allow ...Role) error {
	if slices.Contains(allow, observed) {
		return nil
	}
	return &ErrRefused{Daemon: daemon, Observed: observed, Allowed: allow}
}
