package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitEdgednsd keeps the app hosts' proxied address records in step with the
// compute nodes whose edges are ready (ADR-20261102081618 §3). It stays out of
// Registry: the edgednsd_service role installs it only when the manifest's
// public_edge.dns names hosts to manage, and writes its settings in a drop-in.
// It reads the database URL from its own secret env file and the zone's
// DNS: Edit token through LoadCredential, so the token never sits in its
// environment or on a compute node.
func UnitEdgednsd() daemonunit.Unit {
	return daemonunit.Unit{
		Description:           "Gregale edge DNS (edgednsd): sends Cloudflare to the ready compute edges",
		After:                 []string{"network-online.target", "postgresql.service", "faas-cp.slice"},
		Wants:                 []string{"network-online.target", "faas-cp.slice"},
		StartLimitIntervalSec: "60s",
		StartLimitBurst:       "5",

		Type:       "simple",
		User:       "faas",
		Group:      "faas",
		ExecStart:  "/opt/faas/current/bin/edgednsd",
		Restart:    "on-failure",
		RestartSec: "2s",

		Slice: FaasCPSlice,
		// A registry read, one probe per compute node and a handful of DNS
		// records per tick.
		MemoryMax: "128M",

		EnvironmentFile: "-/etc/faas/secrets/edgednsd/edgednsd.env -/etc/faas/otel.env",

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
