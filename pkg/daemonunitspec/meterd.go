package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitMeterd is the canonical unit for faas-meterd — metering + billing.
// Note: meterd is the only daemon ReadWritePaths listed as
// `/var/log/faas` alone (no /var/lib/faas; meterd writes usage_minutes
// rows to the database via the pool, not to local disk).
//
// Wipe-comments-load-bearing rationale:
//
//   - meterd reads DATABASE_URL from /etc/faas/sealed.env
//     (EnvironmentFile=). The DSN points at the local Postgres;
//     billing writes are durable across reboots because Postgres is
//     started out of band by ansible (cp-ans role does this), not via
//     a `Requires=postgresql.service` directive. (Spec §11 single-public-
//     listener invariant; CLAUDE.md component ownership.)
//
// See ADR-078 for the migration that wiped these from the unit body.
func UnitMeterd() daemonunit.Unit {
	return withRestartPolicy(daemonunit.Unit{
		Description: "onebox-faas meterd — metering and billing",
		After:       []string{"network.target", "postgresql.service", "faas-schedd.service", "faas-cp.slice"},
		Wants:       []string{"faas-cp.slice", "faas-schedd.service"},

		Type:      "simple",
		User:      "faas-meterd",
		Group:     "faas",
		ExecStart: `/opt/faas/current/bin/meterd --config /etc/faas/meterd.toml`,

		Slice:     "faas-cp.slice",
		MemoryMax: "256M",

		EnvironmentFile: "/etc/faas/sealed.env",

		NoNewPrivileges:       true,
		ProtectSystem:         "strict",
		ProtectHome:           true,
		PrivateTmp:            daemonunit.BoolPtr(true),
		ProtectKernelTunables: true,
		ProtectKernelModules:  true,
		ProtectControlGroups:  true,

		ReadOnlyPaths:  []string{"/etc/faas"},
		ReadWritePaths: []string{"/var/log/faas"},

		WantedBy: "multi-user.target",
	})
}
