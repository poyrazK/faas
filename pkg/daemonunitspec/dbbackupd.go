package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitDbbackupd keeps native managed databases' WAL and base backups
// (ADR-1027 durability) on a database node: vmmd relays each database VM's
// archive stream to its socket, it stores the stream on the node's disk
// and ships it to object storage. It is optional, like pgproxyd: it runs
// only where the operator converges database nodes with archiving. It runs
// as its own user; its socket directory is its own, and vmmd (root) is the
// only client.
func UnitDbbackupd() daemonunit.Unit {
	return daemonunit.Unit{
		Description:           "Gregale archive keeper for managed databases",
		Documentation:         "https://gregale.dev/docs/managed-postgres",
		After:                 []string{"network-online.target"},
		Wants:                 []string{"network-online.target"},
		StartLimitIntervalSec: "60s",
		StartLimitBurst:       "5",

		Type:       "notify",
		User:       "faas-dbbackupd",
		Group:      "faas-dbbackupd",
		ExecStart:  "/opt/faas/current/bin/dbbackupd -config /etc/faas/dbbackupd.toml",
		Restart:    "on-failure",
		RestartSec: "2s",
		// ADR-190: PostgreSQL's archive_command blocks on this daemon;
		// replace a stalled runtime quickly.
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

		ReadOnlyPaths:        []string{"/etc/faas"},
		ReadWritePaths:       []string{"/srv/db/archive"},
		RuntimeDirectory:     "faas-dbbackupd",
		RuntimeDirectoryMode: "0700",

		WantedBy: "multi-user.target",
	}
}
