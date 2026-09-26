package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitOutboundd is the canonical unit for faas-outboundd, the explicit
// request-aware third-party API gateway. Operator provider values arrive via
// the per-daemon secret environment; customer values are sealed in Postgres
// and opened with the fleet identity delivered through LoadCredential.
func UnitOutboundd() daemonunit.Unit {
	return daemonunit.Unit{
		Description: "onebox-faas outboundd — request-aware third-party API gateway",
		After:       []string{"network.target", "postgresql.service", "faas-cp.slice"},
		Wants:       []string{"faas-cp.slice"},

		Type:       "simple",
		User:       "faas-outboundd",
		Group:      "faas",
		ExecStart:  `/opt/faas/current/bin/outboundd --config /etc/faas/outboundd.toml`,
		Restart:    "on-failure",
		RestartSec: "2s",

		Slice:     "faas-cp.slice",
		MemoryMax: "256M",

		EnvironmentFile: "-/etc/faas/compute-db.env -/etc/faas/secrets/outboundd/outboundd.env -/etc/faas/otel.env",
		Environment: []daemonunit.KV{
			{Key: "FAAS_FLEET_AGE_IDENTITY_PATH", Value: "%d/faas_fleet_age_identity"},
		},
		LoadCredential: []daemonunit.LoadCred{
			{Name: "faas_fleet_age_identity", Path: "/etc/faas/secrets/fleet.age"},
		},

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
	}
}
