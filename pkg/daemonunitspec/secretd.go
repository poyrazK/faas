package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitSecretd is the control plane's opener of customer runtime secrets
// (ADR-20261110210000 §3). The secretd_service role installs it only where
// faas_secretd_listen is set and writes its listener, TLS paths and
// credentials in a drop-in. It runs as its own OS user, faas-secretd, which
// logs in to Postgres only as faas_secretd, and it reads the runtime seal
// domain's identity and its TLS key through LoadCredential, so neither file
// is readable by any other daemon user. It needs no object storage and no
// tenant network: only Postgres and its mTLS listener.
func UnitSecretd() daemonunit.Unit {
	return daemonunit.Unit{
		Description:           "Gregale secret opener (secretd): opens customer runtime secrets for the compute node that runs the app",
		After:                 []string{"network-online.target", "postgresql.service", "faas-cp.slice"},
		Wants:                 []string{"network-online.target", "faas-cp.slice"},
		StartLimitIntervalSec: "60s",
		StartLimitBurst:       "5",

		Type:       "simple",
		User:       "faas-secretd",
		Group:      "faas",
		ExecStart:  "/opt/faas/current/bin/secretd",
		Restart:    "on-failure",
		RestartSec: "2s",

		Slice: FaasCPSlice,
		// One small RPC per wake that carries secrets and a node registry
		// snapshot.
		MemoryMax: "128M",

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

		WantedBy: "multi-user.target",
	}
}
