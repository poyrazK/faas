# Host kernel patch with a drained reboot

Unattended upgrades install security kernels on every host but never reboot
(`faas_hardening_unattended_reboot: false`), so a fixed kernel protects
nothing until the host boots it. `deploy/ansible/kernel_patch_reboot.yml`
does that one compute node at a time without dropping guests:

1. Skip the node if `uname -r` is already the target.
2. Install `linux-image-<target>` and `linux-modules-<target>` while the node
   still serves, and refuse if a newer kernel is installed (GRUB would boot it).
3. Drain to an empty maintenance hold (`gregalectl compute-nodes drain`, up to
   10 minutes). If the drain never empties, the node is re-activated on its
   old kernel and the play stops.
4. Reboot, then require the target kernel, cgroup v2, every enabled `faas-*`
   unit active and `vmmd base-adoption-check` passing. Any failure leaves the
   node **drained** and stops the play before the next node.
5. Re-activate the node if it was active before.

## Run it

Run from a host that reaches the compute nodes over SSH (the fleet runner, or
an IAP tunnel; the `fsn-*.gregale.dev` names resolve to Cloudflare, not the
hosts). Render the inventory from the `production-manifest.yaml` published
with the running release, then roll one node and check it before the next:

```bash
make manifest-ansible MANIFEST=production-manifest.yaml
cd deploy/ansible
ansible-playbook -i .generated/inventory/hosts.ini kernel_patch_reboot.yml \
  -e faas_kernel_target_release=7.0.0-1017-gcp --limit fsn-2
# check: gregalectl compute-nodes list; wake a test app placed on fsn-2
ansible-playbook -i .generated/inventory/hosts.ini kernel_patch_reboot.yml \
  -e faas_kernel_target_release=7.0.0-1017-gcp --limit fsn-3
```

The drain moves instances to the other nodes, so the rest of the fleet needs
room for them. Run it outside peak hours, and never with `--limit` covering
more than the nodes you mean to roll.

## The control-plane host

The play targets `compute_nodes` only. The control-plane host (`fsn-1`,
`gregale-cp`) has no peer to drain to: rebooting it stops the API, scheduling
and wakes for a few minutes. Patch it in an announced maintenance window:
`apt-get install linux-image-<target> linux-modules-<target>`, `reboot`,
then check `uname -r` and `systemctl --failed`.

## When a node stays drained

The failure message names the task that failed. Fix the host (`journalctl -b`,
`systemctl --failed`), then return it to service by hand:

```bash
gregalectl compute-nodes activate --node <node> --reason kernel_patch_reboot_done
```

A re-run skips a node that already runs the target kernel, so it does not
re-activate it for you.

## History

- 2026-11: PwnEscape (CVE-2026-99999, guest-to-host through virtio-net
  checksum metadata) is fixed in Ubuntu noble `linux-gcp-7.0`
  7.0.0-1017.19~24.04.1, so the target release is `7.0.0-1017-gcp`.
  Firecracker already strips guest offloads (ADR-20261108170000), so this
  closes the host side.
