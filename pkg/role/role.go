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
//
// Resolution order (FromConfig):
//
//  1. Non-empty envKey env var wins.
//  2. Else tomlValue (post-decode). Empty TOML → RoleSingleBox.
//  3. Both empty → RoleSingleBox (single-box dev default).
//
// The function does NOT validate that tomlValue / env is a known
// role — unknown values pass through so the daemon's Require call
// at boot surfaces the typed error. An operator who typoed
// `compute-onry` needs to see the bad value in the systemd journal,
// not a silent default that masks the typo.
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

// IsKnown reports whether r is one of AllRoles. The check is the
// single source of truth for the "is this a recognised role?" predicate;
// FromConfig deliberately uses it to passthrough unknowns (see package
// doc), but tests + a future --box-role flag will use it to validate
// operator-supplied input.
func (r Role) IsKnown() bool {
	return slices.Contains(AllRoles, r)
}

// FromConfig resolves the observed role for a daemon at startup.
// envKey is the env name to consult (e.g. "FAAS_SCHEDD_ROLE" for
// schedd); when the env var is non-empty, it wins over the TOML
// value (the per-deploy override path — ansible sets
// FAAS_<DAEMON>_ROLE from host_vars at the systemd unit level).
// Both empty defaults to RoleSingleBox (single-box dev back-compat).
//
// tomlValue is the post-decode value of the daemon's `role` TOML
// field. Empty string means "TOML had no key" (single-box default).
//
// Use this from each daemon's LoadConfig AFTER toml.Unmarshal so the
// post-decode c.Role is consulted. Setting Role inside the
// defaults-struct literal then letting toml.Unmarshal decode the
// file over it makes the env override silently dead — the env
// resolution never re-runs after the decode.
//
// Unknown TOML / env values pass through unchanged so the daemon's
// Require call at boot can surface the typed error. An operator who
// typoed `compute-onry` needs to see the bad value in the systemd
// journal, not a silent default.
func FromConfig(tomlValue, envKey string) Role {
	if envKey != "" {
		if v := os.Getenv(envKey); v != "" {
			return Role(v)
		}
	}
	if tomlValue == "" {
		return RoleSingleBox
	}
	return Role(tomlValue)
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
// as <observed> (allowed: <list>)" sentence shape as schedd's existing
// default-local guard at cmd/schedd/config.go:259. The runbook reads
// the same way across every daemon.
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
