package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitBridged ships the operator-gated development relay without adding it to
// fleet activation. It needs neither privileged VM access nor sealed secrets.
func UnitBridged() daemonunit.Unit {
	return daemonunit.Unit{
		Description: "Gregale development laptop relay",
		After:       []string{"network.target", "postgresql.service", "faas-cp.slice"},
		Wants:       []string{"faas-cp.slice"},
		Type:        "simple", User: "faas-bridged", Group: "faas",
		ExecStart: "/opt/faas/current/bin/bridged", Restart: "on-failure", RestartSec: "2s",
		StartLimitIntervalSec: "60s", StartLimitBurst: "5",
		Slice: FaasCPSlice, MemoryMax: "256M",
		EnvironmentFile: "/etc/faas/dev-bridge.env -/etc/faas/otel.env",
		NoNewPrivileges: true, ProtectSystem: "strict", ProtectHome: true,
		PrivateTmp: daemonunit.BoolPtr(true), PrivateDevices: true,
		ProtectKernelTunables: true, ProtectKernelModules: true, ProtectControlGroups: true,
		SystemCallArchitectures: "native", LockPersonality: true,
		RestrictNamespaces: true, RestrictRealtime: true, RestrictSUIDSGID: true,
		RestrictAddressFamilies: []string{"AF_UNIX", "AF_INET", "AF_INET6"},
		ProtectHostname:         true, ProtectClock: true, ProtectProc: "invisible",
		ReadOnlyPaths: []string{"/etc/faas"}, WantedBy: "multi-user.target",
	}
}
