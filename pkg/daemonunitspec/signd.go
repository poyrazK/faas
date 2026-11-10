package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitSignd is the control-plane artifact signer (ADR-20261109170000). The
// signd_service role installs it only where faas_signd_listen is set and
// writes its listener and TLS paths in a drop-in. It runs as its own OS user,
// faas-signd, which logs in to Postgres only as faas_signd, and it reads the
// fleet signing key and its TLS key through LoadCredential, so neither file is
// readable by any other daemon user.
func UnitSignd() daemonunit.Unit {
	return daemonunit.Unit{
		Description:           "Gregale artifact signer (signd): signs the app layers compute nodes build",
		After:                 []string{"network-online.target", "postgresql.service", "faas-cp.slice"},
		Wants:                 []string{"network-online.target", "faas-cp.slice"},
		StartLimitIntervalSec: "60s",
		StartLimitBurst:       "5",

		Type:       "simple",
		User:       "faas-signd",
		Group:      "faas",
		ExecStart:  "/opt/faas/current/bin/signd",
		Restart:    "on-failure",
		RestartSec: "2s",

		Slice: FaasCPSlice,
		// One small RPC per published layer and a node registry snapshot.
		MemoryMax: "128M",

		// storage.env names the artifact store the signatures go to.
		EnvironmentFile: "-/etc/faas/storage.env -/etc/faas/otel.env",

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
