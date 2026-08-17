package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitSchedd is the canonical unit for faas-schedd — scheduler +
// instance state machine owner (spec §4.3, §6).
//
// Wipe-comments-load-bearing rationale:
//
//   - schedd is the SOLE writer to the `instances` table
//     (CLAUDE.md Component ownership).
//   - `PrivateTmp=` MUST be `no` for the same reason as vmmd: with
//     `=yes`, schedd.sock lands inside schedd's per-mount-namespace tmpfs
//     and imaged (which dials from its own mount ns) gets "no such file
//     or directory" even though `ss -lnx` shows the LISTEN entry
//     (run 30839233808). schedd does NOT declare RuntimeDirectory=faas;
//     it inherits the bind-mount from vmmd's declaration via
//     ReadWritePaths=/run/faas.
//
// See ADR-078 for the migration that wiped this from the unit body.
func UnitSchedd() daemonunit.Unit {
	return withRestartPolicy(daemonunit.Unit{
		Description: "onebox-faas schedd — scheduler + lifecycle owner",
		After:       []string{"network.target", "faas-cp.slice"},
		Wants:       []string{"faas-cp.slice"},

		Type:      "simple",
		User:      "faas-schedd",
		Group:     "faas",
		ExecStart: `/opt/faas/current/bin/schedd --config /etc/faas/schedd.toml`,

		Slice:     "faas-cp.slice",
		MemoryMax: "256M",

		EnvironmentFile: "/etc/faas/sealed.env",

		NoNewPrivileges:       true,
		ProtectSystem:         "strict",
		ProtectHome:           true,
		PrivateTmp:            daemonunit.BoolPtr(false), // inherits /run/faas via vmmd's bind-mount
		ProtectKernelTunables: true,
		ProtectKernelModules:  true,
		ProtectControlGroups:  true,

		ReadOnlyPaths:  []string{"/etc/faas"},
		ReadWritePaths: []string{"/run/faas", "/var/log/faas"},

		WantedBy: "multi-user.target",
	})
}
