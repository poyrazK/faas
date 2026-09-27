# ADR-285 · Persistent managed realtime callback outbox

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Store realtimed's bounded callback outbox under
  `/var/lib/faas/realtime-callbacks` by default. Provision the directory on
  each compute node with mode `0700`, grant only realtimed write access through
  its systemd unit, and migrate pending and dead-lettered files from the old
  `/run/faas/realtime-callbacks` location before opening the new outbox.
- **Why:** Message and disconnect callbacks are fsynced before delivery, but
  the old default lived on tmpfs. A host reboot could discard every queued
  callback and dead letter even though a process restart replayed them.
- **Consequences:** The migration copies and fsyncs each destination file and
  its directory before removing the legacy copy. It is safe to retry after an
  interrupted rollout; conflicting event IDs fail startup for inspection.
  Existing `FAAS_REALTIME_CALLBACK_OUTBOX` overrides remain authoritative.
  The spool stays node-local. Pending events remain capped at 64 MiB by
  default; dead letters are outside that cap and need operator retention
  management. Callback delivery remains at-least-once, so application handlers
  must deduplicate using event IDs. The directory contains callback payloads
  and bearer tokens and must be excluded from broadly readable archives.
- **Rejected alternatives:** Keeping the default in `/run` leaves reboot loss
  in the standard deployment. Putting callbacks in the control-plane database
  adds a cross-node dependency to the socket owner. A systemd bind mount over
  the old path would hide pending tmpfs files during rollout.
