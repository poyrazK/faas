# ADR-20261026163015: Audit log anchors

- **Status:** accepted
- **Date:** 2026-10-26
- **Amends:** ADR-035 (audit log), ADR-20261023104734 (database roles)

## Context

`audit_log` (migration 00163) records what every account and operator did.
Since #5285, daemons that log in as `faas_runtime` members may only insert
into it. The schema owner `faas` can still rewrite or delete any row, and
until the role rollout finishes every daemon logs in as that owner. Nothing
would notice: an attacker with a database write could erase the record of
what they did, and a restored backup could carry rewritten history. The
2026-10-23 security review ranked this MED ("no hash chain"), and the
section-3 follow-up on 2026-10-26 took it up.

A per-row hash chain needs a total insertion order. `audit_log` has none:
`received_at` is the writer's clock, rows commit out of order, and adding
an ordered column to a large, hot table is a rewrite. Its `received_at`
index already exists.

## Decision

1. **Anchors seal windows of time.** A new append-only table
   `audit_log_anchors` holds, per window `[window_start, window_end)` of
   `received_at`, the number of audit rows and a SHA-256 digest over them in
   `(received_at, id)` order. Each field is length-prefixed; `received_at` is
   taken to the microsecond, and `data` is jsonb's canonical text, so the
   digest does not depend on how a row was written. The first window starts
   at the Unix epoch; each later one starts where the previous one ended.
2. **Anchors chain by an HMAC the database never holds.** Each anchor's MAC
   covers its seq, window, count, digest, key id and the previous anchor's
   MAC. The key is derived (HMAC-SHA256, label `gregale-audit-anchor-key/v1`)
   from apid's audit HMAC key, which lives in `/etc/faas/sealed.env` or
   `/var/lib/faas/audit-hmac.key`, never in PostgreSQL. It is already staged
   on the standby and in the control-plane state backup, so the anchor key
   needs no copies of its own. Rewriting, erasing or adding a row inside a
   sealed window changes the digest; making an anchor match again needs
   the key.
3. **Sealing.** `gregalectl audit anchor`, run by `faas-audit-anchor.timer`
   every 15 minutes on the control plane, seals the window from the newest
   anchor's end to now less 10 minutes (truncated to the minute). The lag
   lets rows a slow writer stamped earlier commit before their window is
   sealed. It refuses to extend a chain whose newest anchor does not verify,
   or was sealed under another key, unless `--accept-key-change` is given
   after a lost key. It logs each anchor as one JSON line
   (`audit anchor sealed`, with seq, window, rows, digest and MAC). The
   journal ships that line off the host to Loki, so an attacker who also
   deletes the newest anchors cannot remove the copy.
4. **Verification.** Each anchor run re-verifies the anchors whose windows
   end in the last 48 hours. `faas-audit-verify.timer` runs
   `gregalectl audit verify` daily over the whole chain. A finding is
   `rows_changed`, `mac_mismatch`, `chain_broken`, `missing_anchor`,
   `window_gap` or `key_unknown`. Both runs write a textfile metric:
   `FaasAuditLogTampered` pages on any finding, `FaasAuditAnchorErased` pages
   when the newest seq falls, and `FaasAuditAnchorStale` warns when the runs
   stop.
5. **Append-only for daemons.** `runtime-roles.sql` revokes UPDATE and DELETE
   on `audit_log_anchors` from `faas_runtime`, as it does for `audit_log`.
   `TestAuditLogStaysAppendOnly` keeps any code path from changing either.

## Consequences

- Detection, not prevention. Someone holding both the database and the
  control plane's root can forge anchors; the off-host log lines and the
  Prometheus history of `faas_audit_anchor_seq` still show what was sealed
  before they had both.
- A row written more than about 10 minutes after its `received_at`, into a
  window already sealed, is reported as `rows_changed`. No current writer
  backdates audit rows; a future backfill must write into unsealed time or
  be followed by an operator note.
- A point-in-time restore removes the anchors and rows after the restore
  point. `FaasAuditAnchorErased` pages; the runbook confirms the restore and
  the chain continues from the restored head.
- Losing the audit HMAC key already breaks audit joins and recovery codes.
  It also means the old anchors cannot be checked any more; the chain
  continues under the new key with `--accept-key-change`.
- The first anchor reads every existing audit row once; later windows read
  15 minutes of rows. The daily verification reads the whole table through
  the `received_at` index.
- Release N-1: nothing older reads or writes the table. The timers fail
  until the release that ships `gregalectl audit anchor` is installed, and
  the alerts stay silent until the first run writes its metrics.

## Alternatives

- **A hash chain per row** (a trigger stamping each row with the previous
  row's hash): needs serialized inserts or a sequence column on a hot table,
  and a trigger the owner can drop.
- **Shipping every audit row off-host**: the log pipeline keeps 14 days and
  is not a system of record; anchors make the database itself checkable.
- **An external timestamping service** for each anchor: a dependency the
  platform does not have; the off-host journal line serves as the witness.
