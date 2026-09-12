# FaasHostJournalPressure

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `vmmd_networkd_dispatcher_errors_last_5m`,
`vmmd_journal_disk_usage_bytes`, and `vmmd_host_journal_metrics_up`.
Issue: #2350. Severity: warn.

## Meaning

The compute host has persistent networkd-dispatcher errors, its local journal
is approaching the configured bound, its journal is growing faster than 120
MiB/hour, or vmmd cannot collect those host signals.

Gregale's Firecracker network setup creates `vh<slot>` host veths and removes
them during routine wake and park cycles. A networkd-dispatcher D-Bus event may
arrive after one of those links has disappeared. The compute-node systemd
drop-in discards only the two stale-ifindex messages and a failed status lookup
for the closed `vh[0-9]+` namespace. Errors for persistent interfaces, failed
dispatcher hooks, and a failed `networkctl list` remain visible and contribute
to the alert.

The host-hardening role caps the persistent journal at 512 MiB, reserves 2 GiB
of free disk, caps the runtime journal at 128 MiB, and retains no more than
seven days. The alert fires at 400 MiB so an operator has time to inspect the
source before rotation removes older entries.

## Verify

On the affected compute node:

```bash
systemctl status networkd-dispatcher.service --no-pager
systemctl cat networkd-dispatcher.service
journalctl -u networkd-dispatcher.service --since '-10 minutes' --no-pager
journalctl --disk-usage
systemd-analyze cat-config systemd/journald.conf
```

Confirm the effective dispatcher unit contains
`60-faas-firecracker-links.conf` and the journal configuration contains
`60-faas-journal-bounds.conf`. Query the same values Prometheus sees:

```bash
curl -fsS http://127.0.0.1:9104/metrics \
  | grep -E '^vmmd_(networkd_dispatcher|journal_disk|host_journal)'
```

On a split compute node, use its configured private vmmd metrics address in
place of loopback.

## Triage

List remaining dispatcher messages with their priorities:

```bash
journalctl -u networkd-dispatcher.service --since '-30 minutes' \
  --output=short-iso --no-pager
```

- `networkctl list failed` means dispatcher cannot refresh any link. Check
  `systemctl status systemd-networkd` and `networkctl list` directly.
- A failed status lookup for `eth*`, `ens*`, the provider private interface,
  or `br-tenants` is actionable. Check `networkctl status <interface>`, its
  carrier, addresses, and routes.
- A dispatcher hook exit is actionable. Run the named script with the logged
  `IFACE` and `STATE` inputs after reviewing it; do not suppress the whole unit.
- If journal usage is growing, identify the loudest units:

  ```bash
  journalctl --since '-30 minutes' -o json \
    | jq -r '._SYSTEMD_UNIT // .SYSLOG_IDENTIFIER // "unknown"' \
    | sort | uniq -c | sort -nr | head -20
  ```

## Recovery

Repair the persistent interface, systemd-networkd state, or failing hook, then
confirm the dispatcher gauge returns to zero after its five-minute window. If
the collector is down, verify vmmd runs as root and both `journalctl` and GNU
`du` are installed before restarting vmmd.

Do not broaden the `LogFilterPatterns` expressions or lower the whole service's
log level. A broad filter would hide provider-interface and tenant-bridge
failures. Do not manually vacuum the journal during triage unless the host is
under immediate disk pressure; capture the relevant entries first.
