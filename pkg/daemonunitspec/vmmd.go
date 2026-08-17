package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitVmmd is the canonical unit for faas-vmmd — microVM supervisor.
// vmmd is the ONLY root component (spec §4.4, CLAUDE.md component
// ownership). It owns firecracker + the jailer + the overlay mount
// RPC handler (DEPLOY-1).
//
// Wipe-comments-load-bearing rationale that USED to live in the unit
// file body, now preserved here:
//
//   - vmmd is the SOLE owner of /run/faas on disk
//     (`RuntimeDirectory=faas` + `RuntimeDirectoryMode=0775`).
//     Declaring RuntimeDirectory=faas on ANY other faas unit would
//     create a SECOND per-unit tmpfs whose bind-mount does not
//     propagate back to /run, and that daemon's .sock lands in the
//     wrong filesystem layer (run 30841750214, post-#619). schedd +
//     gatewayd-internal + imaged etc. write into the host /run/faas
//     via ReadWritePaths=/run/faas alone.
//   - RuntimeDirectory creates the dir as root:root. Mode 0775 gives
//     the OWNER group (root, not faas) write+execute; schedd sits in
//     the `faas` group but loses on the OWNER check. So the two
//     ExecStartPre= lines chown the dir to root:faas 0775 right after
//     systemd creates it. Idempotent (matching-ownership chown is a no-op).
//   - PrivateTmp MUST be `=no` — `=yes` makes /run/faas land inside
//     vmmd's per-mount-namespace tmpfs and become invisible from the
//     host, breaking every other daemon's dial of /run/faas/vmmd.sock
//     (run 30839233808).
//   - vmmd has CAP_NET_BIND_SERVICE so it can bind the /metrics low
//     TCP port if MetricsAddr is set in TOML.
//
// See ADR-078 for the migration that wiped these from the unit body.
func UnitVmmd() daemonunit.Unit {
	return withRestartPolicy(daemonunit.Unit{
		Description:   "onebox-faas vmmd — microVM supervisor (the only root component, spec §4.4)",
		Documentation: "https://docs.gregale.dev/ops/vmmd",
		After:         []string{"faas-tenant.slice", "faas-cp.slice"},
		Wants:         []string{"faas-tenant.slice", "faas-cp.slice"},

		Type: "simple",
		// No User=/Group=: vmmd is root by design.
		ExecStart: `/opt/faas/current/bin/vmmd --config /etc/faas/vmmd.toml`,
		ExecStartPre: []string{
			`/usr/bin/chown root:faas /run/faas`,
			`/usr/bin/chmod 0775 /run/faas`,
		},

		Slice:     "faas-cp.slice",
		MemoryMax: "256M",

		AmbientCapabilities: []string{"CAP_NET_BIND_SERVICE"},

		NoNewPrivileges:       true,
		ProtectSystem:         "strict",
		ProtectHome:           true,
		PrivateTmp:            daemonunit.BoolPtr(false), // SOLE RuntimeDirectory owner; see struct godoc.
		ProtectKernelTunables: true,
		ProtectKernelModules:  true,
		ProtectControlGroups:  true,

		RuntimeDirectory:     "faas",
		RuntimeDirectoryMode: "0775",
		ReadWritePaths:       []string{"/etc/faas/secrets", "/run/faas", "/srv/fc", "/var/log/faas"},

		WantedBy: "multi-user.target",
	})
}
