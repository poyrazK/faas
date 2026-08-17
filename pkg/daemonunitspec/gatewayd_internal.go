package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitGatewaydInternal is the canonical unit for faas-gatewayd-internal
// — routing + wake + proxy (Tier A7 split, ADR-070). It does NOT bind
// a public port; it listens on a unix socket inside the box. The public
// edge daemon (gatewayd-public) forwards every inbound request to it.
//
// Issue #675 / Tier A7 unified mux: the unix socket now serves BOTH the
// synth routes (schedd → /v1/synthesize, /v1/invocations:dispatch, /healthz)
// AND the customer publicHandler (gatewayd-public → customer traffic).
// The public→internal hop negotiates HTTP/2 cleartext via
// srv.Protocols.SetUnencryptedHTTP2(true) on the SynthServer's
// http.Server (Go 1.24+ stdlib API; the deprecated
// golang.org/x/net/http2/h2c wrapper is gone). The outer Caddy +
// TLS hop terminates H2; the in-box socket hop is plaintext, hence
// H2C rather than H2. See ADR-079 for the unified-mux + H2C
// architecture decision.
//
// Wipe-comments-load-bearing rationale:
//
//   - FAAS_GATEWAY_LISTEN=off is REQUIRED to skip the public listener.
//     Without it, both daemons bind :8080, gatewayd-internal starts
//     first and wins (sorted order in restart loop), and gatewayd-public
//     crash-loops with "address already in use" (run 31121004495).
//     The sentinel is checked in cmd/gatewayd-internal/run.go.
//   - RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6: gRPC to schedd/vmmd
//     is over loopback mTLS (AF_INET) and the unix socket to gatewayd-public
//     (AF_UNIX). AF_INET6 is allowed so the internal dial doesn't fail
//     on v6-only networks (defense in depth; we never connect OUT to v6).
//
// See ADR-078 for the migration that wiped these from the unit body.
func UnitGatewaydInternal() daemonunit.Unit {
	return withRestartPolicy(daemonunit.Unit{
		Description:   "onebox-faas gatewayd-internal — routing + wake + proxy (Tier A7 split, ADR-070)",
		Documentation: "https://docs.gregale.dev/ops/gatewayd-internal",
		After:         []string{"faas-cp.slice", "network-online.target", "faas-apid.service", "faas-schedd.service"},
		Wants:         []string{"faas-cp.slice", "faas-apid.service", "faas-schedd.service"},

		Type:      "simple",
		User:      "faas",
		Group:     "faas",
		ExecStart: `/opt/faas/current/bin/gatewayd-internal --config /etc/faas/gatewayd-internal.toml`,

		Slice:     "faas-cp.slice",
		MemoryMax: "512M",

		Environment: []daemonunit.KV{
			{Key: "FAAS_GATEWAY_LISTEN", Value: "off"},
		},

		NoNewPrivileges:         true,
		ProtectSystem:           "strict",
		ProtectHome:             true,
		PrivateTmp:              daemonunit.BoolPtr(true),
		PrivateDevices:          true,
		ProtectKernelTunables:   true,
		ProtectKernelModules:    true,
		ProtectControlGroups:    true,
		SystemCallArchitectures: "native",
		LockPersonality:         true,
		RestrictNamespaces:      true,
		RestrictRealtime:        true,
		RestrictSUIDSGID:        true,
		RestrictAddressFamilies: []string{"AF_UNIX", "AF_INET", "AF_INET6"},
		ProtectHostname:         true,
		ProtectClock:            true,
		ProtectProc:             "invisible",

		ReadWritePaths: []string{"/run/faas"},

		WantedBy: "multi-user.target",
	})
}
