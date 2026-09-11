// cmd/vmmd/caps.go — DEPLOY-1 / ADR-075 cap declaration.
//
// vmmd is the ONLY daemon in this fleet that holds CAP_SYS_ADMIN
// (spec §11 / CLAUDE.md "vmmd is the ONLY component that
// touches firecracker/jailer"). The Allow list below mirrors the
// caps vmmd actually exercises; the Deny list is what a future
// PR MUST add caps to if vmmd's footprint grows beyond this
// set (anything in Bnd that's not in Allow is a regression
// waiting to happen).
//
// The runtimecheck (pkg/capdecl/runtimecheck) is invoked from
// cmd/vmmd/main.go's startup path so a misconfigured
// CapabilityBoundingSet / AmbientCapabilities pair fails fast
// at process start rather than silently at first syscall.
//
// Caps in this list (kernel 6.17, /usr/include/linux/capability.h):
//   - cap_sys_admin   — firecracker / jailer / loopback mounts
//     / overlayfs mounts (ADR-053 + ADR-075).
//   - cap_net_admin   — netns setup (ADR-009) + nft + iptables.
//   - cap_net_bind_service — gRPC listener on /run/faas/vmmd.sock
//     (unix-socket, doesn't strictly need
//     this cap, but the existing unit
//     file has it and we're not stripping
//     in DEPLOY-1).
//   - cap_kill        — SIGTERM to jailed firecracker instances
//     during shutdown.
//   - cap_dac_override — rootfs staging + chroot (jailer
//     chroots live under /srv/fc/jail as
//     tmpfs).
//   - cap_chown       — chown 0:0 on the staged parent ext4 /
//     jailer chroot layout.
//   - cap_fowner      — chown ops on the chroot mountpoints.
//   - cap_setuid      — jailer drops to a 20000-29999 uid per
//     instance.
//   - cap_setgid      — same, group dimension.
//   - cap_sys_chroot  — jailer chroot.
//   - cap_setpcap     — jailer drops its bounding set before execing
//     firecracker.
//   - cap_sys_ptrace  — vmmd enters and inspects the mount namespace of
//     the cross-UID jailer child while repairing its device tree.
//   - cap_mknod       — the in-jail mount helper provisions /dev/kvm.
//
// Deny list is the full set of caps vmmd doesn't use. The Deny
// list is the more important side of the contract: a future
// PR that adds, say, cap_bpf to vmmd MUST extend the Allow list
// OR the runtimecheck will fail at boot.
package main

import "github.com/onebox-faas/faas/pkg/capdecl"

// capsDecl is the canonical declaration vmmd enforces at boot.
// Tests (cmd/vmmd/caps_test.go) assert the declaration is
// well-formed and matches the deploy/systemd/faas-vmmd.service
// unit file's CapabilityBoundingSet.
//
// Adding a cap: edit this list AND the systemd unit AND the
// ADR. The lint rule (pkg/capdecl + .golangci.yml depguard)
// rejects pkg/vmmdmount imports outside cmd/vmmd/** + pkg/vmmd/**
// so the cap stays scoped to vmmd.
var capsDecl = capdecl.Declaration{
	Allow: []string{
		"cap_sys_admin",
		"cap_net_admin",
		"cap_net_bind_service",
		"cap_kill",
		"cap_dac_override",
		"cap_chown",
		"cap_fowner",
		"cap_setuid",
		"cap_setgid",
		"cap_setpcap",
		"cap_sys_chroot",
		"cap_sys_ptrace",
		"cap_mknod",
	},
	// Deny is empty by intent. vmmd's contract is "we have the
	// caps we need; deny lists on root components have caused
	// more confusion than they're worth in practice". A future
	// PR can populate this if a specific cap is a known footgun
	// (e.g. cap_bpf — see eBPF audit).
	Deny: nil,
}
