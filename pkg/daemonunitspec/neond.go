package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitNeond holds Gregale's Neon API keys (ADR-20261112160000). The
// neond_service role installs it only where faas_neond_enabled is set and
// writes its socket, callers and key credential in a drop-in. It runs as its
// own OS user, faas-neond, which logs in to Postgres only as faas_neond (read
// on managed_postgres_databases), and it reads the Neon admin key through
// LoadCredential, so no other daemon user can read it. apid reaches it on
// /run/faas/neond.sock; neond admits only faas-apid by SO_PEERCRED.
func UnitNeond() daemonunit.Unit {
	return daemonunit.Unit{
		Description:           "Gregale Neon key holder (neond): forwards apid's Neon API calls under an allow-list",
		After:                 []string{"network-online.target", "postgresql.service", "faas-cp.slice"},
		Wants:                 []string{"network-online.target", "faas-cp.slice"},
		StartLimitIntervalSec: "60s",
		StartLimitBurst:       "5",

		Type:       "simple",
		User:       "faas-neond",
		Group:      "faas",
		ExecStart:  "/opt/faas/current/bin/neond",
		Restart:    "on-failure",
		RestartSec: "2s",

		Slice: FaasCPSlice,
		// One forwarded request per Neon call apid makes, bodies under 4 MiB.
		MemoryMax: "192M",

		EnvironmentFile: "-/etc/faas/otel.env",

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
		// The socket apid dials.
		ReadWritePaths: []string{"/run/faas"},

		WantedBy: "multi-user.target",
	}
}
