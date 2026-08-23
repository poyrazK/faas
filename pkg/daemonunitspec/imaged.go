package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitImaged is the canonical unit for faas-imaged — OCI + rootfs layer
// builder (spec §4.6, ADR-003, ADR-005, ADR-053).
//
// Post-DEPLOY-1 / ADR-075 architecture: cap_sys_admin no longer lives on
// this daemon. The parent-ref overlay mount is an RPC to vmmd
// (pkg/imaged/vmmclient.go → MountOverlayParent on the vmmdgrpc unix
// socket); vmmd does the mount under cap_sys_admin. imaged now runs
// with a CapabilityBoundingSet that EXCLUDES cap_sys_admin, and the
// AmbientCapabilities=cap_sys_admin directive that PR-F added is GONE.
//
// Wipe-comments-load-bearing rationale:
//
//   - The CapabilityBoundingSet listed below is what imaged LEGITIMATELY
//     needs (setuid/setgid for OCI user-ns, dac_override for parent
//     staging, kill for SIGTERM, sys_chroot for builderd's chroot paths).
//     cap_sys_admin is the load-bearing omission — that's the architectural
//     boundary.
//   - FAAS_BASE_STAGING_ROOT=/dev/shm/faas-base-staging: tmpfs staging
//     for the parent-ref overlay. The kernel rejects overlay mounts whose
//     upper fs doesn't support tmpfile; host /tmp is ext4 on cd-controlplane
//     EX44, /dev/shm is tmpfs. This is the regression that broke every
//     cd-controlplane deploy 2026-08-04 → 2026-08-05 (PR-K.2 + ADR-053).
//   - ReadWritePaths covers the per-fc path: /srv/fc/{snap,base,sigs} +
//     /dev/shm/faas-base-staging + logs/spool. /srv/fc/sigs was missed
//     when ADR-038 landed and crash-looped imaged on the DO box.
//
// See ADR-078 for the migration that wiped these from the unit body.
//
// Issue #585 / ADR-127 — sealed.env is apid-only; imaged keeps compute-db.env
// (DATABASE_URL) but no longer inherits the full sealed.env. The host.age
// identity is delivered via LoadCredential= (the env var holds the
// %d/<name> tmpfs path), the same pattern apid uses. FAAS_BUILDER_BASE_REF
// is informational on the imaged path — the v1 sealed.env.example comment
// notes that the operator MUST pin a digest; the TOML form remains the
// canonical source for typed config (cmd/imaged/main.go:392-401).
func UnitImaged() daemonunit.Unit {
	return daemonunit.Unit{
		Description:   "onebox-faas imaged — image/snapshot orchestrator (spec §4.6, ADR-003, ADR-005)",
		Documentation: "https://docs.gregale.dev/ops/imaged",
		After:         []string{"network.target", "faas-cp.slice", "faas-vmmd.service"},
		Wants:         []string{"faas-cp.slice", "faas-vmmd.service"},

		Type:       "simple",
		User:       "faas-imaged",
		Group:      "faas",
		ExecStart:  `/opt/faas/current/bin/imaged --config /etc/faas/imaged.toml`,
		Restart:    "on-failure",
		RestartSec: "2s",

		Slice: "faas-cp.slice",
		// Base-image conversion invokes mkfs.ext4 over the OCI layer
		// tree. The Go runtime base peaks above 2G while its large
		// compiler tree is materialised, which otherwise turns first
		// boot into an OOM restart loop. Keep the daemon bounded at
		// the control-plane slice's 6G ceiling, but leave enough room
		// for one conversion at a time.
		MemoryMax: "4G",

		// No AmbientCapabilities — DEPLOY-1 erased cap_sys_admin; the
		// parent-ref mount is an RPC to vmmd now.

		CapabilityBoundingSet: []string{
			"cap_chown",
			"cap_dac_override",
			"cap_fowner",
			"cap_fsetid",
			"cap_kill",
			"cap_setgid",
			"cap_setuid",
			"cap_setpcap",
			"cap_net_bind_service",
			"cap_sys_chroot",
		},

		// Issue #585 / ADR-127: sealed.env dropped; per-daemon compute-db.env
		// (DATABASE_URL) is the only EnvironmentFile=. The optional '-' prefix
		// remains so an image-seeded compute node can boot before secrets
		// provisioning has populated the file.
		EnvironmentFile: "-/etc/faas/compute-db.env",
		Environment: []daemonunit.KV{
			// ProtectSystem=strict makes the host /tmp read-only. Keep OCI
			// layer verification and upload scratch on the writable base disk.
			{Key: "TMPDIR", Value: "/srv/fc/base"},
			{Key: "FAAS_BASE_STAGING_ROOT", Value: "/dev/shm/faas-base-staging"},
			{Key: "FAAS_BASE_EXTRACT_ROOT", Value: "/srv/fc/base-staging"},
			{Key: "FAAS_BASE_TMP_ROOT", Value: "/srv/fc/base"},
			{Key: "FAAS_HOST_AGE_IDENTITY_PATH", Value: "%d/faas_host_age_identity"},
		},
		LoadCredential: []daemonunit.LoadCred{
			{Name: "faas_host_age_identity", Path: "/etc/faas/secrets/host.age"},
		},

		NoNewPrivileges:       true,
		ProtectSystem:         "strict",
		ProtectHome:           true,
		PrivateTmp:            daemonunit.BoolPtr(true),
		ProtectKernelTunables: true,
		ProtectKernelModules:  true,
		ProtectControlGroups:  true,

		ReadOnlyPaths: []string{"/etc/faas"},
		ReadWritePaths: []string{
			"/srv/fc/snap", "/srv/fc/base", "/srv/fc/base-staging", "/srv/fc/scans", "/srv/fc/sigs",
			"/var/log/faas", "/var/spool/faas", "/var/lib/faas/cache",
			"/dev/shm/faas-base-staging",
		},

		WantedBy: "multi-user.target",
	}
}
