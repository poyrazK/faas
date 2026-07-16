# deploy/ — host provisioning and runtime config

Bootstraps a fresh Hetzner EX44 to one-box FaaS-ready in one command:

```
make bootstrap          # `ansible-playbook -i deploy/ansible/inventory deploy/ansible/site.yml`
```

then verify the platform works end-to-end:

```
sudo make test-metal    # `go test -tags metal ./...` — boots a hello-Firecracker VM
sudo make leakcheck     # asserts zero leaked netns/taps/jails/cgroups
make build              # compile every daemon
make test               # cross-platform unit tests
```

- `ansible/` — idempotent EX44 bootstrap (`make bootstrap`): LVM layout
  (§8), systemd slices (§13), nftables (§7), cgroups v2 verify
  (ADR-008). 8 roles, ordered:
  `cgroups_v2 → grub → lvm → xfs → firecracker → systemd_slices →
  nftables → postgres`. See [`ansible/README.md`](ansible/README.md).
- `systemd/` — one unit + slice per daemon; memory.max fences the RAM
  ledger. Wired up in M5 (per-slice `.slice` units land in
  `ansible/roles/systemd_slices/`).
- `nftables/` — tenant + builder egress policy (§7): deny 25/465/587,
  deny RFC1918/link-local/metadata. Dropped as `/etc/nftables.conf`
  via the `nftables` ansible role.
- `scripts/` — ops helpers (`leakcheck.sh` for the shell-side check,
  restore drill planned for M8).
