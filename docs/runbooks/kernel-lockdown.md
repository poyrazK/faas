# Kernel lockdown and module lock

host_hardening raises kernel lockdown to `integrity` on every host through
`faas-kernel-lockdown.service`, and can turn module loading off
([ADR-20261107220000](../adr/20261107220000-kernel-lockdown.md)).

## Check a host

```bash
cat /sys/kernel/security/lockdown        # none [integrity] confidentiality
cat /proc/sys/kernel/modules_disabled    # 1 when the module lock is on
sysctl kernel.kexec_load_disabled kernel.io_uring_disabled
journalctl -u faas-kernel-lockdown -o cat
```

The kernel logs each refusal as `Lockdown: <program>: <what> is restricted`.
Alloy ships kernel notices off the host and counts these as the
`kernel_lockdown` event (ADR-20261107203000). On a host where nobody was
debugging, a refusal is an intrusion signal:
see [FaasHostSecurityEvent](FaasHostSecurityEvent.md).

## Something legitimate is refused

Lockdown and the module lock last until reboot; nothing turns them off on a
running host.

1. Set `faas_hardening_kernel_lockdown: ""` (and
   `faas_hardening_modules_lock: false`) for that one host in the inventory.
2. Converge it, then reboot it. The unit no longer raises anything.
3. Do the work, then restore the variables, converge and reboot again.

## Turning the module lock on for a host group

A module the platform needs but the lock did not preload fails with `EPERM`,
usually far from the cause (`nft` refusing a rule, `tc` refusing a qdisc,
a tunnel not coming up). Before enabling it:

1. On each kind of host (compute, control plane, database node; each
   provider), after it has run every feature for a few days (builds,
   deploys, snapshots, TCP and UDP ingress, private networks, the overlay):

   ```bash
   lsmod | awk 'NR>1 {print $1}' | sort
   ```
2. Add any module missing from `faas_hardening_modules_preload` that is not
   hardware (hardware drivers load during boot, before the lock).
3. Set `faas_hardening_modules_lock: true` for the group, converge one host,
   reboot it and run the native metal suite and a deploy against it before
   the rest.
4. The unit logs `could not load: <modules>` for any listed module that
   exists but failed to load; check that line after the first boot.
