package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitMeterd is the canonical unit for faas-meterd — metering + billing.
// Note: meterd is the only daemon ReadWritePaths listed as
// `/var/log/faas` alone (no /var/lib/faas; meterd writes usage_minutes
// rows to the database via the pool, not to local disk).
//
// Wipe-comments-load-bearing rationale:
//
//   - meterd reads DATABASE_URL from /etc/faas/compute-db.env
//     (EnvironmentFile=). The DSN points at the local Postgres;
//     billing writes are durable across reboots because the unit waits for
//     and requires the local PostgreSQL aggregate target before it opens
//     its pool. (Spec §11 single-public-listener invariant; CLAUDE.md
//     component ownership.)
//
// See ADR-078 for the migration that wiped these from the unit body.
//
// Issue #585 / ADR-127 — sealed.env is apid-only; meterd loads the
// billing-provider secrets (FAAS_PADDLE_*, FAAS_BILLING_*, FAAS_MAIL_*
// when meterd acts as the mail sender) from /etc/faas/secrets/meterd/
// billing.env (0400 root:root), not from the full sealed.env.
func UnitMeterd() daemonunit.Unit {
	return daemonunit.Unit{
		Description:           "onebox-faas meterd — metering and billing",
		After:                 []string{"network.target", "postgresql.service", "faas-schedd.service", "faas-cp.slice"},
		Wants:                 []string{"faas-cp.slice", "faas-schedd.service"},
		Requires:              []string{"postgresql.service"},
		StartLimitIntervalSec: "60s",
		StartLimitBurst:       "5",

		Type:       "notify",
		User:       "faas-meterd",
		Group:      "faas",
		ExecStart:  `/opt/faas/current/bin/meterd --config /etc/faas/meterd.toml`,
		Restart:    "on-failure",
		RestartSec: "2s",

		Slice:                 "faas-cp.slice",
		MemoryMax:             "256M",
		CapabilityBoundingSet: []string{},
		AmbientCapabilities:   []string{""},

		EnvironmentFile: "-/etc/faas/compute-db.env -/etc/faas/secrets/meterd/billing.env -/etc/faas/otel.env",
		Environment: []daemonunit.KV{
			{Key: "FAAS_PROMETHEUS_URL", Value: "http://127.0.0.1:9095"},
			{Key: "FAAS_HOST_AGE_IDENTITY_PATH", Value: "%d/faas_host_age_identity"},
		},
		LoadCredential: []daemonunit.LoadCred{
			{Name: "faas_host_age_identity", Path: "/etc/faas/secrets/host.age"},
			{Name: "faas_host_age_identity_previous", Path: "/etc/faas/secrets/host.age.previous", Optional: true},
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
		ReadWritePaths: []string{"/var/log/faas"},

		WantedBy: "multi-user.target",
	}
}
