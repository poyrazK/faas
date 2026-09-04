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
//   - /run/faas is a host-shared tmpfs directory provisioned by tmpfiles.d
//     and reasserted by vmmd's host-side install/chown fixups. vmmd must
//     not declare RuntimeDirectory=faas: systemd's per-unit runtime mount
//     can remove or hide gatewayd/schedd sockets during a vmmd restart.
//     schedd + gatewayd-internal + imaged write into the shared host path
//     via ReadWritePaths=/run/faas.
//   - PrivateTmp MUST be `=no` — `=yes` makes /run/faas land inside
//     vmmd's per-mount-namespace tmpfs and become invisible from the
//     host, breaking every other daemon's dial of /run/faas/vmmd.sock
//     (run 30839233808).
//   - vmmd has CAP_NET_BIND_SERVICE so it can bind the /metrics low
//     TCP port if MetricsAddr is set in TOML.
//
// See ADR-078 for the migration that wiped these from the unit body.
//
// Issue #585 / ADR-127 — sealed.env is apid-only; vmmd keeps compute-db.env
// (DATABASE_URL) but no longer inherits the full sealed.env. The host.age
// recipient path (PUBLIC half, mode 0444) is set as a literal Environment=
// entry — vmmd only ever opens it read-only for envelope sealing on the
// apid path (which vmmd does not do directly; apid does), so the env var
// is informational for vmmd.
func UnitVmmd() daemonunit.Unit {
	return daemonunit.Unit{
		Description:   "onebox-faas vmmd — microVM supervisor (the only root component, spec §4.4)",
		Documentation: "https://docs.gregale.dev/ops/vmmd",
		After: []string{
			"faas-tenant.slice", "faas-cp.slice", "faas-cp-build.slice",
			"faas-tenant-free.slice", "faas-tenant-hobby.slice",
			"faas-tenant-pro.slice", "faas-tenant-scale.slice",
		},
		Wants: []string{
			"faas-tenant.slice", "faas-cp.slice", "faas-cp-build.slice",
			"faas-tenant-free.slice", "faas-tenant-hobby.slice",
			"faas-tenant-pro.slice", "faas-tenant-scale.slice",
		},

		Type: "simple",
		// No User=/Group=: vmmd is root by design.
		ExecStart: `/opt/faas/current/bin/vmmd --config /etc/faas/vmmd.toml`,
		ExecStartPre: []string{
			`/usr/bin/install -d -o root -g faas -m 0775 /run/faas`,
			`/usr/bin/chmod 0775 /run/faas`,
		},
		// Re-assert the shared host-directory ownership after startup.
		// This keeps a hand-edited or manually repaired /run tree safe for
		// gatewayd and schedd after a vmmd restart.
		ExecStartPost: []string{
			`/usr/bin/chown root:faas /run/faas`,
			`/usr/bin/chmod 0775 /run/faas`,
		},
		Restart:            "on-failure",
		RestartSec:         "2s",
		RestartCountExport: "SYSTEMD_RESTARTS_ON_FAILURE",

		Slice: "faas-cp.slice",

		// vmmd was the only one of the nine gated daemons without a
		// memory bound (the others run 256M-4G). The cap appears to
		// have been dropped when Delegate=yes was added, on the
		// assumption it would also bound the delegated children.
		//
		// It does not. Verified on a live compute node: vmmd's own
		// cgroup holds exactly one process at ~19 MB, while firecracker
		// VMs live under faas-tenant.slice/<plan>/<uuid> — a sibling
		// tree the jailer creates with its own memory.max = plan + 8 MB
		// (§11). Nothing tenant-facing sits in vmmd's cgroup, so a
		// bound here constrains only vmmd.
		//
		// 2026-09-03: under sustained load vmmd reached 2.1 GB RSS and
		// the shared 3 GB faas-cp.slice (FaasCPSliceMemoryMax) OOM-killed
		// it. systemd restarted it in 2 s, but the node never returned to
		// rotation — schedd's watchdog had already set
		// compute_nodes.active=false, and neither the heartbeat (which
		// enumerates active nodes only) nor UpsertComputeNodeFromVmmd
		// (which preserves active on conflict) can clear it. Every app on
		// the node served 503 until an operator intervened. See
		// docs/runbooks/FaasComputeNodeStuckInactive.md.
		//
		// That growth is no longer undiagnosed, and MemoryHigh=512M /
		// MemoryMax=1G (the first containment attempt) made things
		// strictly worse. Both facts are measured:
		//
		//  1. The 2.1 GB was LocalCacheBackend.Put teeing a snapshot blob
		//     into a bytes.Buffer — ~2x the blob with doubling growth.
		//     Not a leak. Fixed by streaming that Put to a spool file.
		//
		//  2. A per-unit memory bound on vmmd does not measure vmmd.
		//     tmpfs pages are charged to whichever cgroup WRITES them,
		//     and vmmd populates each jail chroot on /srv/fc/jail (tmpfs)
		//     with a ~400 MB layer.ext4 per instance. Verified on a live
		//     node: writing 200 MB into the jail from a shell charged
		//     user.slice, not vmmd, while vmmd's own shmem sat at 450 MB
		//     of jail content it had written. So vmmd's cgroup carries
		//     roughly (live instances x layer size), and a single Scale
		//     instance already exceeded MemoryHigh=512M.
		//
		//     Worse, shmem with no swap is unreclaimable. MemoryHigh does
		//     not throttle-and-reclaim against it, it spins: measured
		//     pgscan 138,686,137 against pgsteal 2,494,654 (55 pages
		//     scanned per page freed) with 2.1M refaults, because the only
		//     evictable pages left were the executables' own text. Every
		//     process vmmd forked thrashed on its own code — a `tc qdisc
		//     add` sat in D state in filemap_fault for a full 35 s
		//     cold-boot budget until it was killed. Lifting the bound live
		//     took setup_network from 18,586 ms to 87 ms.
		//
		// So: no MemoryHigh (it cannot do its job against unreclaimable
		// shmem), and MemoryMax deliberately equal to the slice ceiling —
		// vmmd is bounded BY ITS SLICE, not by a tighter per-unit cap that
		// would really be a cap on jail residency. imaged sets 4G on the
		// same reasoning. The CI hardening gate still sees a MemoryMax.
		//
		// The remaining design smell is that jail tmpfs is charged to a
		// 3 GB control-plane slice at all; sizing that properly (or moving
		// the charge to the tenant scope, which widenSnapshotMemoryCgroup
		// already does for the mem file) needs an ADR.
		MemoryMax: FaasCPSliceMemoryMax,

		// vmmd owns the node-local snapshot fan-out worker, so it must see
		// the same shared OCI registry configuration as the control plane.
		EnvironmentFile: "-/etc/faas/compute-db.env -/etc/faas/storage.env",
		Environment: []daemonunit.KV{
			{Key: "TMPDIR", Value: "/srv/fc/base"},
			// Public half of the host X25519 age key — read by vmmd's
			// seal path (which does NOT exist in vmmd; this env var is
			// retained so future age-sealed payloads don't require a
			// restart with a fresh sealed.env — see ADR-057).
			{Key: "FAAS_HOST_AGE_RECIPIENT_PATH", Value: "/etc/faas/secrets/host.age.pub"},
		},

		AmbientCapabilities: []string{"CAP_NET_BIND_SERVICE"},

		NoNewPrivileges: true,
		// jailer creates and manages the per-VM cgroup below the
		// systemd-owned tenant/build slices. ProtectControlGroups would
		// make that write path read-only and leave the Firecracker child
		// charged to vmmd's 256M supervisor limit.
		Delegate:      true,
		ProtectSystem: "strict",
		ProtectHome:   true,
		PrivateTmp:    daemonunit.BoolPtr(false), // /run/faas is host-shared.
		// vmmd writes the delegated per-VM memory/cpu fences under the
		// systemd cgroup hierarchy. ProtectKernelTunables would remount
		// those control files read-only inside the service namespace even
		// though ProtectControlGroups is disabled.
		ProtectKernelTunables: false,
		ProtectKernelModules:  true,
		ProtectControlGroups:  false,

		ReadWritePaths: []string{"/etc/faas/secrets", "/run/faas", "/run/netns", "/srv/fc", "/var/log/faas", "/var/lib/faas/cache"},

		WantedBy: "multi-user.target",
	}
}
