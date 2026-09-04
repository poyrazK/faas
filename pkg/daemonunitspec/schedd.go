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
//
// Issue #585 / ADR-127 — sealed.env is apid-only. schedd keeps compute-db.env
// (DATABASE_URL) + loads the internal-svc sealed blob via a per-daemon
// EnvironmentFile=/etc/faas/secrets/schedd/schedd.env (0400 root:root);
// the file holds `FAAS_INTERNAL_SVC_KEY_SEALED_BLOB=<base64-of-age-ciphertext>`,
// a content-shaped env var that the loader at cmd/schedd/internal_svc_minter.go:127
// expects via os.Getenv (NOT a path). The LoadCredential+%d/ pattern is for
// PATH-shaped env vars only — using it for content would set the env var
// to a tmpfs path string and break the unseal call. FAAS_NODE_NAME +
// FAAS_GATEWAY_SYNTH_TARGET are operator-set via the ansible 99-faas-node-name.conf
// drop-in (role-specific; not in this unit) so the same daemon binary
// ships across fsn-1 / fsn-2.
func UnitSchedd() daemonunit.Unit {
	return daemonunit.Unit{
		Description: "onebox-faas schedd — scheduler + lifecycle owner",
		After:       []string{"network.target", "faas-cp.slice", "faas-brokerq.slice"},
		Wants:       []string{"faas-cp.slice", "faas-brokerq.slice"},

		Type:  "simple",
		User:  "faas-schedd",
		Group: "faas",
		ExecStartPre: []string{
			`/usr/bin/install -d -o faas-schedd -g faas -m 0770 /var/lib/faas/oci-tmp`,
			// Apply the broker-egress tc qdisc on the brokerq
			// host interface before schedd starts polling. The
			// actual command is synthesised by
			// pkg/sched.BrokerTcCommands at first-boot from
			// FAAS_BROKER_EGRESS_MBIT — when EgressMbit == 0
			// the slice + ExecStartPre line are no-ops, matching
			// the Hobby / no-quota plan shape (ADR-118 §9).
			`/opt/faas/current/bin/schedd-brokerq-apply`,
		},
		ExecStart:          `/opt/faas/current/bin/schedd --config /etc/faas/schedd.toml`,
		Restart:            "on-failure",
		RestartSec:         "2s",
		RestartCountExport: "SYSTEMD_RESTARTS_ON_FAILURE",

		Slice:     "faas-cp.slice",
		MemoryMax: "256M",

		// Issue #585 / ADR-127: sealed.env dropped; per-daemon
		// schedd.env (FAAS_INTERNAL_SVC_KEY_SEALED_BLOB=<base64>)
		// loaded alongside compute-db.env. The optional '-' prefix
		// on both means a missing file at boot is non-fatal — the
		// loader then falls back to the plaintext-PEM path
		// (loadOrGenerateSchedKey).
		// Shared OCI storage is the authoritative snapshot/layer source on
		// multi-box deployments. The optional prefix keeps single-box/local
		// development bootable when storage.env has not been provisioned yet.
		EnvironmentFile: "-/etc/faas/compute-db.env -/etc/faas/secrets/schedd/schedd.env -/etc/faas/storage.env",
		Environment: []daemonunit.KV{
			{Key: "TMPDIR", Value: "/var/lib/faas/oci-tmp"},
			// ADR-143: every production gate is declared, never implied.
			// Jobs dispatch stays OFF until the vmmd job RPC lands
			// (Mega-1.5; cmd/schedd/main.go wires the fail-open stub).
			// Flip to "1" in the same PR that ships the RPC. Without an
			// explicit value the daemon logged "disabled" on every boot
			// and `POST /v1/jobs/{name}/runs` rows sat pending forever.
			{Key: "FAAS_JOBS_DISPATCH", Value: "0"},
			// Durable workflow dispatch is opt-in until the workflow
			// runtime rollout is enabled on this host. Operators can
			// override this unit default with a later systemd drop-in.
			{Key: "FAAS_WORKFLOWS_ENABLED", Value: "0"},
		},

		NoNewPrivileges:       true,
		ProtectSystem:         "strict",
		ProtectHome:           true,
		PrivateTmp:            daemonunit.BoolPtr(false), // inherits /run/faas via vmmd's bind-mount
		ProtectKernelTunables: true,
		ProtectKernelModules:  true,
		ProtectControlGroups:  true,

		ReadOnlyPaths:  []string{"/etc/faas"},
		ReadWritePaths: []string{"/run/faas", "/var/lib/faas", "/var/log/faas"},

		WantedBy: "multi-user.target",
	}
}
