// cmd/imaged/caps.go — DEPLOY-1 / ADR-075 cap declaration.
//
// BEFORE DEPLOY-1 imaged held cap_sys_admin via
// AmbientCapabilities=cap_sys_admin in
// deploy/systemd/faas-imaged.service, and issued
// unix.Mount(2) directly in pkg/imaged/mount_overlay_linux.go.
// That was the silent CLAUDE.md invariant violation: spec §11
// says vmmd is the only root component. PR-K added the
// syscall (replacing exec /bin/mount under NNP) and PR-K.2
// moved the staging dir to /dev/shm; both papered over the
// architectural rot. DEPLOY-1 erases the violation entirely:
// imaged's parent-ref overlay mount now flows through
// MountOverlayParent / UmountOverlayParent RPCs to vmmd.
//
// After DEPLOY-1 the capsDecl for imaged is:
//   - Allow: cap_chown and cap_dac_override. imaged is User=faas-imaged +
//     NoNewPrivileges=yes, but OCI extraction must preserve the uid/gid
//     declared by each layer and then traverse restrictive customer-owned
//     directories while inspecting and packaging the extracted tree. These
//     capabilities do not permit mount operations.
//   - Deny: cap_sys_admin. The runtimecheck asserts the
//     daemon does NOT have cap_sys_admin in Bnd. The matching
//     edit in deploy/systemd/faas-imaged.service shrinks
//     CapabilityBoundingSet= to exclude cap_sys_admin (so the
//     runtimecheck passes), while AmbientCapabilities= contains
//     only the two extraction capabilities above.
//
// Review finding M1: pre-M1 the declaration was Allow/Deny=nil
// — a no-op assertion that allowed any cap to slip in. Adding
// cap_sys_admin to Deny is the tripwire: a future PR that
// silently re-adds AmbientCapabilities=cap_sys_admin (the
// regression that drove DEPLOY-1) will now surface at boot as
// a *runtimecheck.Violation{Kind: ViolationDenyPresent, Caps:
// ["cap_sys_admin"]}. The unit's CapabilityBoundingSet= list
// is the receipt that the cap was deliberately dropped.
//
// The remaining caps in the unit's CapabilityBoundingSet=
// (cap_fowner, cap_fsetid, cap_kill,
// cap_setgid, cap_setuid, cap_setpcap, cap_net_bind_service,
// cap_sys_chroot) are NOT in Allow — they're a "may have" list
// the runtimecheck does not enforce. A future DEPLOY-3
// hardening pass can promote them into Allow (then the
// runtimecheck requires them in Bnd) — out of scope here.
package main

import "github.com/onebox-faas/faas/pkg/capdecl"

// capsDecl is the canonical declaration imaged enforces at boot.
// The Deny: cap_sys_admin entry is load-bearing — it is what
// makes the runtimecheck fail loud if a future PR silently
// restores the cap_sys_admin ambient + bounding entry.
var capsDecl = capdecl.Declaration{
	Allow: []string{
		"cap_chown",
		"cap_dac_override",
	},
	Deny: []string{
		// cap_sys_admin is vmmd-only (spec §11 / CLAUDE.md
		// "vmmd is the ONLY component that mounts filesystems").
		// imaged's parent-ref overlay mount goes through
		// MountOverlayParent RPC; imaged has no syscall that
		// would need cap_sys_admin. Listing it in Deny turns
		// the Bnd set into a tripwire for regressions.
		"cap_sys_admin",
	},
}
