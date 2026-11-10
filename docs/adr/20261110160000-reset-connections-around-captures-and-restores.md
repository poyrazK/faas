# ADR-20261110160000: Reset a guest's TCP connections before a capture and after a restore

- Status: Accepted (owner decision 2026-11-10)
- Date: 2026-11-10
- Amends: ADR-20261109130000 (reset an app guest's TCP connections before
  destroying it)
- Related: ADR-159 (idle park reuses the retained capture), ADR-20261107160000
  (snapshot contract for plain apps)

## Context

ADR-20261109130000 resets an app guest's off-guest TCP connections before
vmmd destroys it, and left one case open: the guest that takes a capture.
Idle park with a capture and the deploy prime pause the guest, write the
snapshot and destroy it through `Park`, so:

- the capture's sessions on the app's database, cache or upstream stay open
  until the peer's keepalive gives up, once per capture;
- every instance restored from the snapshot wakes holding the same
  established sockets. Their peer state belongs to the capture's guest and
  the restored network namespace has no conntrack for them, so the first use
  gets a reset or hangs on retransmits until the app's own timeout.

Resetting before the capture puts aborted sockets into the snapshot. A
pool sees them fail at once and opens fresh ones, which is what every restored
instance needs anyway.

## Decision

1. **Before a capture.** `Park` asks guest-init to reset the guest's
   off-guest connections (message type 8, `pkg/connreleaseproto`) after the
   forwards drain and the probes stop, then takes the snapshot. Same
   eligibility as before destroy (app guests; not builders, executions,
   repair, qualification or warm spares).
2. **After a restore.** A guest restored from a snapshot resets the
   connections it inherited before it is published: in `wake` after a
   restore that is not kept paused, and when a warm-pool spare is resumed
   to serve. This covers snapshots taken before this change and warm-tier
   captures (`SnapshotKeepAlive`), whose guest keeps serving and is not
   reset.
3. **Not at every resume.** The resume hook also runs when a quiet-paused or
   frozen guest is resumed in place, whose connections are still valid, so
   the restore reset is sent by vmmd only where it knows the guest came from
   a snapshot.
4. **Switch and budget.** Both follow
   `disable_release_connections_on_destroy` /
   `FAAS_RELEASE_CONNECTIONS_ON_DESTROY` and the 600 ms host budget. Failures
   are logged at debug; the park or wake goes ahead.

## Consequences

- A capture no longer orphans its sessions; restored instances fail fast on
  inherited sockets and their pools reconnect.
- Park takes up to 600 ms longer before the capture (usually milliseconds).
  A restore adds one vsock round trip before publication; with sockets
  already reset in the snapshot it finds nothing to do.
- Warm-tier captures keep their guest serving; restores from them reset what
  they inherited.
- Validation: unit tests for the order (reset before snapshot) and for which
  wakes reset; metal before release: park an app holding a Postgres pool and
  check that `pg_stat_activity` drops the capture's sessions at the park, and
  that a restored instance's first query succeeds after its pool reconnects.
