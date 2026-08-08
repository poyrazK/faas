// Package egresssocket centralises the unix-domain socket path that
// the egress byte-counter gRPC producer/consumer pair
// (pkg/gateway/egressgrpc + cmd/meterd) share. The two endpoints
// (gatewayd-internal as server, meterd as client) historically pointed
// at /run/faas/gatewayd-egress.sock — the "gatewayd-" prefix is the
// pre-Tier-A7 monolithic daemon name that the ADR-070 split
// superseded. This package introduces the new default
// /run/faas/egress.sock and the resolver that prefers the new path
// while keeping the legacy path readable for one release cycle
// (read-both-prefer-new).
//
// # Why this lives in a leaf package
//
// cmd/meterd, cmd/gatewayd-internal/egress_grpc, and the operator
// TOML all need the same default + precedence rules. Duplicating
// them across three call sites is the bug surface this package
// closes — every time the operator-facing field set grows, all
// three would otherwise need synchronized edits.
//
// # Scope (PR-A, refactor; PR-C+D flips the default)
//
// This package is a pure move. It introduces the constants and the
// resolver. PR-A wired cmd/meterd/config.go to use LegacySocketPath
// as its default — preserving the pre-PR-A wire behavior on every
// box. PR-C+D flips that default to DefaultSocketPath; the legacy
// socket path remains readable through the resolver's
// legacyEnvVal + legacyCfgVal slots for one release cycle.
//
// # Precedence (PR-C extension)
//
// When ResolveSocketPath is called with all four sources, the
// preference order is:
//
//  1. envVal              (FAAS_EGRESS_SOCKET)
//  2. legacyEnvVal        (FAAS_GATEWAY_EGRESS_SOCKET, deprecated)
//  3. cfgVal              (egress_socket = ...)
//  4. legacyCfgVal        (gateway_egress_socket = ..., deprecated)
//  5. DefaultSocketPath   (/run/faas/egress.sock)
//
// Each non-empty source is honoured as-is; no normalization, no
// env-var expansion. Callers that need a path they can `os.Stat`
// should use the result unchanged.
package egresssocket

import "os"

// DefaultSocketPath is the post-PR-C default unix-domain socket
// path the egress gRPC server binds and the meterd client dials.
// The leaf "egress.sock" matches the daemon-independent naming the
// Tier A9 / PR-cluster establishes (the proto package renamed to
// onebox.faas.egress.v1; this is the runtime mirror).
const DefaultSocketPath = "/run/faas/egress.sock"

// LegacySocketPath is the pre-PR-C default the monolithic
// gatewayd daemon used when it served the egress channel directly.
// Kept here so existing deployments with the legacy socket path
// continue to work for one release cycle via the resolver's
// legacyEnvVal + legacyCfgVal slots (PR-C+D flip). PR-E + a
// follow-up PR removes this constant.
const LegacySocketPath = "/run/faas/gatewayd-egress.sock"

// ResolveSocketPath returns the unix-domain socket path the egress
// producer/consumer pair should use, given the four operator-facing
// sources (in preference order):
//
//	envVal       — the new env var FAAS_EGRESS_SOCKET (added in PR-C)
//	legacyEnvVal — the legacy env var FAAS_GATEWAY_EGRESS_SOCKET
//	cfgVal       — the new TOML key egress_socket (added in PR-C)
//	legacyCfgVal — the legacy TOML key gateway_egress_socket
//
// Empty strings are skipped. If every source is empty the function
// returns DefaultSocketPath. The function never returns "".
//
// PR-A wires this into cmd/meterd/config.go with legacyEnvVal +
// legacyCfgVal both populated from the existing field. PR-C
// introduces envVal + cfgVal as the new (precedence-winning) fields;
// the function's signature already accepts all four so no PR-C code
// change is needed.
func ResolveSocketPath(envVal, legacyEnvVal, cfgVal, legacyCfgVal string) string {
	if envVal != "" {
		return envVal
	}
	if legacyEnvVal != "" {
		return legacyEnvVal
	}
	if cfgVal != "" {
		return cfgVal
	}
	if legacyCfgVal != "" {
		return legacyCfgVal
	}
	return DefaultSocketPath
}

// EnvGetter is the function shape os.Getenv satisfies; the
// signature lets tests inject deterministic env values without
// touching process state. Kept local to this package — pkg/wire
// has a parallel shape (GetEnvFunc) but copying the four-line
// type keeps egresssocket import-light.
type EnvGetter func(key string) string

// ResolveFromOS is a convenience wrapper that resolves using the
// two env-var keys FAAS_EGRESS_SOCKET (new) and
// FAAS_GATEWAY_EGRESS_SOCKET (legacy). Both keys are checked
// regardless of whether the caller passed cfg-level overrides —
// the env var always wins, per Unix convention.
//
// Tests should call ResolveSocketPath directly with literal values
// to pin behaviour; ResolveFromOS is the production call site in
// cmd/meterd/main.go and cmd/gatewayd-internal/egress_grpc.go.
//
// env is the getter (production: os.Getenv). Passing nil falls
// back to os.Getenv so the production call site can omit the
// argument explicitly via the variadic below.
func ResolveFromOS(getEnv EnvGetter, cfgVal, legacyCfgVal string) string {
	if getEnv == nil {
		getEnv = os.Getenv
	}
	return ResolveSocketPath(
		getEnv("FAAS_EGRESS_SOCKET"),
		getEnv("FAAS_GATEWAY_EGRESS_SOCKET"),
		cfgVal,
		legacyCfgVal,
	)
}
