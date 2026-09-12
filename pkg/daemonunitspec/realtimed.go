package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitRealtimed is the canonical unit for the managed realtime connection
// owner. The daemon terminates WebSockets on a private Unix socket; the
// gateway's reserved namespace forwards to it, so no public listener or VM
// capability is required here.
func UnitRealtimed() daemonunit.Unit {
	return daemonunit.Unit{
		Description:   "onebox-faas realtimed — managed realtime connection owner",
		Documentation: "https://docs.gregale.dev/ops/realtime",
		After:         []string{"faas-cp.slice", "faas-vmmd.service"},
		Wants:         []string{"faas-cp.slice", "faas-vmmd.service"},

		Type:               "simple",
		User:               "faas",
		Group:              "faas",
		ExecStart:          "/opt/faas/current/bin/realtimed",
		Restart:            "on-failure",
		RestartSec:         "2s",
		RestartCountExport: "SYSTEMD_RESTARTS_ON_FAILURE",
		Slice:              FaasCPSlice,
		MemoryMax:          "512M",

		EnvironmentFile: "-/etc/faas/secrets/realtimed/realtimed.env",
		Environment: []daemonunit.KV{
			{Key: "FAAS_REALTIME_SOCKET", Value: "/run/faas/realtimed.sock"},
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

		ReadOnlyPaths:  []string{"/etc/faas"},
		ReadWritePaths: []string{"/run/faas"},
		WantedBy:       "multi-user.target",
	}
}
