package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitAgentd runs Gregale Agents runs (ADR-20261027090010, ADR-20261027090014 §1): it claims
// queued agent runs from Postgres and drives each through schedd's RunAgent
// stream, then delivers their results. Like livequeryd it stays out of
// Registry: it idles until FAAS_AGENTD_AGENT_RUNS=1, which the agentd_service
// role writes in a drop-in with the run's other switches. It reads the
// database URL and the artifact store from its secret and storage
// environment files and reaches schedd and githubd on their local sockets.
func UnitAgentd() daemonunit.Unit {
	return daemonunit.Unit{
		Description:           "Gregale Agents (agentd): claims agent runs and drives them through schedd",
		After:                 []string{"network-online.target", "postgresql.service", "faas-cp.slice", "faas-schedd.service"},
		Wants:                 []string{"network-online.target", "faas-cp.slice"},
		StartLimitIntervalSec: "60s",
		StartLimitBurst:       "5",

		Type:       "simple",
		User:       "faas",
		Group:      "faas",
		ExecStart:  "/opt/faas/current/bin/agentd",
		Restart:    "on-failure",
		RestartSec: "2s",

		Slice: FaasCPSlice,
		// Run output and delivery bodies are bounded (result.json 64 KiB
		// inline, comments 60 KB); checkouts stream through to the artifact
		// store under their archive cap.
		MemoryMax: "256M",

		// The database URL arrives in agentd's own secret env file, as schedd's
		// does; the artifact store in the control plane's storage.env.
		EnvironmentFile: "-/etc/faas/secrets/agentd/agentd.env -/etc/faas/storage.env -/etc/faas/otel.env",

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
		// A local artifact store (FAAS_STORAGE_BACKEND=local) keeps
		// checkouts and output files under its roots.
		ReadWritePaths: []string{"/srv/fc", "/var/lib/faas"},

		WantedBy: "multi-user.target",
	}
}
