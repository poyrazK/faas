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

		// Type=notify lets `systemctl reload` hand off to a second
		// generation, which becomes the main process once ready (ADR-1736,
		// ADR-20261128150000): a release no longer cuts AI calls that run
		// for minutes.
		Type:         "notify",
		NotifyAccess: "main",
		ExecReload:   handoffReload,
		OOMPolicy:    "continue",
		User:         "faas-outboundd",
		Group:        "faas",
		ExecStart:    `/opt/faas/current/bin/outboundd --config /etc/faas/outboundd.toml`,
		Restart:      "on-failure",
		RestartSec:   "2s",
		// A notify unit pings the watchdog from a live main loop (ADR-190).
		WatchdogSec: "60s",

		Slice: "faas-cp.slice",
		// Two generations share it during a hand-off, with the cache
		// snapshot (at most ~96 MB, in the runtime directory's tmpfs)
		// between them; 256M was one process's.
		MemoryMax: "512M",

		EnvironmentFile: "-/etc/faas/compute-db.env -/etc/faas/secrets/outboundd/outboundd.env -/etc/faas/otel.env",
		Environment: []daemonunit.KV{
			{Key: "FAAS_FLEET_AGE_IDENTITY_PATH", Value: "%d/faas_fleet_age_identity"},
			// ADR-489: authorize stored workflow integration steps that
			// schedd sends (beta, 2026-11-21).
			{Key: "FAAS_WORKFLOW_OUTBOUND_ENABLED", Value: "1"},
			// ADR-20261128150000: the caches a stopping or handing-off
			// generation writes for the next one.
			{Key: "FAAS_OUTBOUNDD_CACHE_SNAPSHOT_DIR", Value: "/run/faas-outboundd"},
		},
		LoadCredential: []daemonunit.LoadCred{
			{Name: "faas_fleet_age_identity", Path: "/etc/faas/secrets/fleet.age"},
			{Name: "faas_fleet_age_identity_previous", Path: "/etc/faas/secrets/fleet.age.previous", Optional: true},
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
		// Private tmpfs for the cache snapshot: kept across a restart so the
		// next start loads it, removed when the unit stops, so cached
		// provider responses never outlive the service.
		RuntimeDirectory:         "faas-outboundd",
		RuntimeDirectoryMode:     "0700",
		RuntimeDirectoryPreserve: "restart",

		WantedBy: "multi-user.target",
	}
}
