package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitPgproxyd is the PostgreSQL proxy of native managed databases
// (ADR-1027), the only public listener on a database node. It is optional:
// it runs only on hosts the operator converges as database nodes. It runs as
// its own unprivileged user because it faces the internet; its secrets
// (public TLS key, decoy key, read-only database URL) are owner-only files
// the daemon refuses to read when they are group or world readable.
func UnitPgproxyd() daemonunit.Unit {
	return daemonunit.Unit{
		Description:           "Gregale PostgreSQL proxy for managed databases",
		Documentation:         "https://gregale.dev/docs/managed-postgres",
		After:                 []string{"network-online.target", "faas-vmmd.service"},
		Wants:                 []string{"network-online.target"},
		StartLimitIntervalSec: "60s",
		StartLimitBurst:       "5",

		Type:       "notify",
		User:       "faas-pgproxyd",
		Group:      "faas-pgproxyd",
		ExecStart:  "/opt/faas/current/bin/pgproxyd -config /etc/faas/pgproxyd.toml",
		Restart:    "on-failure",
		RestartSec: "2s",
		// ADR-190: data-path daemon; replace a stalled runtime quickly.
		WatchdogSec: "60s",
		MemoryHigh:  "768M",
		MemoryMax:   "1G",
		TasksMax:    "4096",

		CapabilityBoundingSet: []string{},
		EnvironmentFile:       "-/etc/faas/otel.env",

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

		ReadOnlyPaths: []string{"/etc/faas"},

		WantedBy: "multi-user.target",
	}
}
