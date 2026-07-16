# deploy/ — host provisioning and runtime config

- `ansible/` — idempotent EX44 bootstrap (`make bootstrap`): LVM layout (§8),
  systemd slices (§13), nftables (§7), cgroups v2 verify (ADR-008).
- `systemd/` — one unit + slice per daemon; memory.max fences the RAM ledger.
- `nftables/` — tenant + builder egress policy (§7): deny 25/465/587, deny
  RFC1918/link-local/metadata.
- `scripts/` — ops helpers (`leakcheck.sh`, restore drill).
