# ADR 20261104085150 · Log volume has a per-plan hourly allowance

- **Status:** accepted
- **Date:** 2026-11-04
- **Amends:** issue #562 (log archive), ADR-20261026151103 (log drains)
- **Related:** ADR-1430 (plan entitlements), cost-control audit 2026-11-03

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Logs have no separate charge, and nothing bounded their volume:

- An instance's ring keeps about 10 MiB in memory. Past that, lines go to the
  node's archive spool. The spool bounds one instance only to 1/8 of its
  10 GiB budget between 5-minute flushes, so one instance could archive about
  15 GiB an hour.
- The archive keeps every line for 90 days on every plan, so a Free account's
  lines are stored 90 times longer than it can read them.
- Log drains (up to 25 per app and 100 per account) forward every line with
  no rate limit. `http_json` sends one POST per line, on Gregale's network.

So a chatty or hostile app turned into storage, request and egress cost no
price covered. One instance could also fill a node's shared spool and drop
other tenants' lines.

## Decision

1. **An hourly allowance per instance.** `api.Limits.LogBytesPerInstanceHour`
   bounds the line bytes one instance writes per UTC hour: Free 16 MiB,
   Hobby 64 MiB, Pro 256 MiB, Scale 1 GiB. A typical app writes well under
   1 MiB a minute.
   - vmmd enforces it where lines enter the instance's ring
     (`logbuf.Ring.SetHourlyByteAllowance`), using the plan the lease
     carries. The live view, the archive, `gregale logs` and every drain
     therefore read the same bounded stream.
   - Past the allowance, the hour's remaining lines are dropped. The first
     drop commits one notice line (`logbuf.AllowanceNotice`), so readers see
     why output stops. Dropped lines take no sequence numbers.
   - Builder VMs and rings without an app keep no allowance.
2. **A delivery rate per drain.** A drain accepts at most
   `api.LogDrainBytesPerSecond` (1 MiB/s, bursts to `api.LogDrainBurstBytes`,
   4 MiB). A line past the budget is dropped through the sender's drop path,
   as a full outbox's is, and counted in the drain's health.

## Consequences

- The worst-case monthly log volume is bounded by plan, instance-hours and
  the allowance. A Free account's 20 GB-hours of 256 MB instances (about 80
  instance-hours) write at most about 1.25 GiB.
- An app that logs more than its allowance loses the rest of the hour, with
  a notice. A log drain or an external log service keeps every line only up
  to the drain's rate.
- The archive still keeps every line for 90 days on every plan; the
  allowance bounds what that costs. Deleting each plan's lines at its
  readable window is a separate change.
