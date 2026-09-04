package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitBuilderd is the canonical unit for faas-builderd — build
// orchestrator + ephemeral builder microVMs (spec §4.5, ADR-003,
// ADR-005).
//
// builderd is a non-root daemon (User=faas-builderd Group=faas); it
// runs on the compute-only box (fsn-2 per ADR-092). builderd does
// NOT touch /dev/kvm directly — its ephemeral VMs are spawned
// through vmmd's jailer (the [compute_node] register row vmmd
// writes is the inbound point for the per-box capacity signal).
//
// The exec line uses /opt/faas/current/bin/builderd with no
// --config flag; builderd reads FAAS_BUILDERD_CONFIG env var
// (cmd/builderd/main.go:62-66). The 99-faas-role.conf drop-in
// (deploy/ansible/roles/builderd_service/templates/) wires
// FAAS_BUILDERD_ROLE=compute-only on fsn-2.
//
// builderd's ReadWritePaths= list is the load-bearing pair with the
// role's `file:` module mkdir list — a drift between the two is
// caught by the role's `assert every ReadWritePaths= target exists`
// task (Deploy-tree fail-loud shape, mirrors imaged + vmmd). The
// content-addressed build cache is included explicitly so ProtectSystem
// does not turn an otherwise successful build into a best-effort cache miss.
//
// Cross-host dep note (Mega-PR-C, issue #911 / ADR-110): builderd
// is intentionally NOT After= faas-apid.service. apid runs only on
// the control-plane box (fsn-1); on the compute-only box (fsn-2)
// where builderd lives, the faas-apid unit doesn't exist. `After=`
// applied per-host would silently no-op into a 90s boot timeout
// before systemd failed the unit (systemd waits the full 90s for a
// unit that will never activate). builderd schedules builds via
// gRPC over the wire to apid on fsn-1 (the [apphub] layer), so
// there is no same-host ordering need at unit-activation time.
//
// Issue #585 / ADR-127 — sealed.env is apid-only; builderd keeps compute-db.env
// (DATABASE_URL) but no longer inherits the full sealed.env. The
// FAAS_BUILDERD_CONFIG path is set as a literal Environment= entry so
// cmd/builderd/main.go:67 falls back to it without needing the env var
// from sealed.env.
func UnitBuilderd() daemonunit.Unit {
	return daemonunit.Unit{
		Description:   "onebox-faas builderd — build orchestrator + ephemeral builder microVMs (spec §4.5, ADR-003, ADR-005)",
		Documentation: "https://docs.gregale.dev/ops/builderd",
		After:         []string{"network.target", "faas-cp.slice", "faas-vmmd.service"},
		Wants:         []string{"faas-cp.slice", "faas-vmmd.service"},

		Type:               "simple",
		User:               "faas-builderd",
		Group:              "faas",
		ExecStart:          "/opt/faas/current/bin/builderd",
		Restart:            "on-failure",
		RestartSec:         "2s",
		RestartCountExport: "SYSTEMD_RESTARTS_ON_FAILURE",

		Slice:     "faas-cp.slice",
		MemoryMax: "512M",

		// The source-layer path follows the same shared OCI storage contract
		// as vmmd/imaged on a multi-box fleet. Runtime base refs are loaded
		// from the same digest-pinned contract consumed by imaged.
		EnvironmentFile: "-/etc/faas/compute-db.env -/etc/faas/storage.env -/etc/faas/runtime-bases.env",
		Environment: []daemonunit.KV{
			{Key: "FAAS_BUILDERD_CONFIG", Value: "/etc/faas/builderd.toml"},
		},

		NoNewPrivileges:       true,
		ProtectSystem:         "strict",
		ProtectHome:           true,
		PrivateTmp:            daemonunit.BoolPtr(true),
		ProtectKernelTunables: true,
		ProtectKernelModules:  true,
		ProtectControlGroups:  true,

		ReadOnlyPaths:  []string{"/etc/faas"},
		ReadWritePaths: []string{"/srv/fc/builder", "/srv/fc/base", "/var/log/faas", "/var/spool/faas", "/var/lib/faas/cache", "/var/cache/faas/builds"},

		WantedBy: "multi-user.target",
	}
}
