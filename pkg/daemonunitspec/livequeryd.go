package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitLivequeryd is the live-query hub for one database (ADR-20261016151947).
// Like s3-gatewayd it stays out of Registry: it cannot start until an operator
// names the database it serves. The livequeryd_service role writes that as a
// drop-in which replaces ExecStart with the database's flags and loads its
// secrets as systemd credentials, so the connection strings, the live-key
// identity and the TLS key reach the hub as files under %d and never appear
// in its command line.
func UnitLivequeryd() daemonunit.Unit {
	return daemonunit.Unit{
		Description:           "Gregale live queries (livequeryd): incremental SQL results for one database",
		After:                 []string{"network-online.target", "postgresql.service", "faas-cp.slice"},
		Wants:                 []string{"network-online.target", "faas-cp.slice"},
		StartLimitIntervalSec: "60s",
		StartLimitBurst:       "5",

		Type:       "notify",
		User:       "faas",
		Group:      "faas",
		ExecStart:  "/opt/faas/current/bin/livequeryd",
		Restart:    "on-failure",
		RestartSec: "2s",
		// ADR-190: a hub that stops pinging stops serving its subscribers.
		WatchdogSec: "60s",

		Slice: FaasCPSlice,
		// The SQL parser runs as WebAssembly: it reserves ~280 MiB of address
		// space and spikes RSS on the first parse. 600 subscriptions peaked at
		// 197 MiB RSS with the parser in process (ADR-20261016151947, "Memory,
		// and the compiler split").
		MemoryMax: "512M",

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
		// The checkpoint (-checkpoint) lets a restart resume from the slot.
		ReadWritePaths: []string{"/var/lib/faas/livequeryd"},

		WantedBy: "multi-user.target",
	}
}
