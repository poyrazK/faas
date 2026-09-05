package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitGatewaydPublic is the canonical unit for faas-gatewayd-public —
// the plain-HTTP edge daemon (Tier A7 split, ADR-070). TLS terminates
// at Caddy upstream; this daemon binds 127.0.0.1:8080 and forwards to
// gatewayd-internal over a unix socket on one-box installs or a private TCP
// target on split-box control planes.
//
// Wipe-comments-load-bearing rationale:
//
//   - AmbientCapabilities is EMPTY: this daemon doesn't bind a low port.
//     Setting `=` (empty body) is the canonical way to say
//     "no caps elevated"; we emit the directive explicitly so a future
//     PR that adds a low-port bind fails PR review (a real AmbientCapabilities=
//     with a cap is a glaring diff).
//   - FAAS_PUBLIC_CONTROL_ADDR=127.0.0.1:9092 was the PR-J fix for the
//     :9090 collision between this daemon + gatewayd-internal. Both
//     daemons used the same control_addr default because gatewayd-public
//     was launched without --config and gatewayd-internal's TOML config
//     was never shipped. :9092 is the only port the rest of the platform
//     doesn't claim.
//   - RestrictAddressFamilies=AF_UNIX AF_INET: accepts Caddy's reverse-proxy
//     on 127.0.0.1:8080 and dials either the local socket or private TCP.
//     AF_INET6 dropped because the bind is loopback v4.
//
// See ADR-078 for the migration that wiped these from the unit body.
func UnitGatewaydPublic() daemonunit.Unit {
	return daemonunit.Unit{
		Description:           "onebox-faas gatewayd-public — plain-HTTP edge (Tier A7 split, ADR-070; TLS terminates at Caddy upstream)",
		Documentation:         "https://docs.gregale.dev/ops/gatewayd-public",
		After:                 []string{"faas-cp.slice", "network-online.target", "postgresql.service", "faas-apid.service"},
		Wants:                 []string{"faas-cp.slice", "faas-apid.service"},
		Requires:              []string{"postgresql.service"},
		StartLimitIntervalSec: "60s",
		StartLimitBurst:       "5",

		Type:               "simple",
		User:               "faas",
		Group:              "faas",
		ExecStart:          `/opt/faas/current/bin/gatewayd-public`,
		Restart:            "on-failure",
		RestartSec:         "2s",
		RestartCountExport: "SYSTEMD_RESTARTS_ON_FAILURE",

		Slice:     "faas-cp.slice",
		MemoryMax: "512M",

		AmbientCapabilities: []string{""}, // explicit empty body: "no caps elevated"

		Environment: []daemonunit.KV{
			{Key: "FAAS_PUBLIC_CONTROL_ADDR", Value: "127.0.0.1:9092"},
			{Key: "FAAS_INTERNAL_TARGET", Value: ""},
			{Key: "FAAS_COMPUTE_GATEWAY_DISCOVERY", Value: "database"},
			{Key: "FAAS_CONTROL_PLANE_API_TARGET", Value: "http://127.0.0.1:8081"},
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
		RestrictAddressFamilies: []string{"AF_UNIX", "AF_INET"},
		ProtectHostname:         true,
		ProtectClock:            true,
		ProtectProc:             "invisible",

		ReadOnlyPaths:  []string{"/etc/faas"},
		ReadWritePaths: []string{"/run/faas"},

		WantedBy: "multi-user.target",
	}
}
