# ADR 1044: Consumer-group delivery deadlines

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Opt in at group creation with immutable `deadline_metadata_key`, a valid metadata
field of at most 64 bytes. Empty disables deadlines. Ordered groups require the
immutable `skip` dead-letter policy; allowing `block` would leave permanently
unreplayable expired predecessors blocking a key. Creation and schema constraints
reject that combination. Existing groups and snapshots retain no deadlines.

At assignment, freeze an RFC3339 timestamp from that metadata field. Missing/empty
values mean no deadline. Require years 1–9999, normalize UTC, and truncate fractional
precision to PostgreSQL microseconds. Malformed values freeze `deadline_invalid`
and become terminal `invalid_delivery_deadline` failures rather than repeatedly
rolling back assignment. Valid expired values become `delivery_expired` failures.
Neither result is handed to a worker. The immutable snapshot cannot repair malformed
metadata: correct the source and publish new work deliberately.

Use group-locked claim/replay maintenance to transition ready or leased expired
snapshots to dead, clearing lease credentials in the existing transaction. Delayed
ready retries also expire. No background timer is introduced: counters/webhooks
are lazy observations, and untouched groups can retain past-due ready/leased rows
until maintenance. Unassigned history is evaluated when assigned. A paused claim
can maintain assigned rows before returning paused, but does not load new history.
Materialized dead failures are already terminal and do not emit another expiry
when their delivery deadlines later pass.

Exclude expired/invalid snapshots from ready selection and recheck in the lease
UPDATE to cover time crossing between selection and leasing. If that final guard
loses the race, maintain expiry and continue bounded admission. Every claim returns
nullable `delivery_deadline`; initial leases and renewals use the earlier of normal
lease expiry and the deadline. Settlement still requires a current unexpired lease.
Existing fencing counters may have gaps if a selected deadline crosses before a
lease is issued, but committed claims remain monotonically increasing.

The shared Node account/scoped-worker helper observes the deadline, aborts its
handler signal and renewal on expiry, and avoids settlement after local expiry.
Timers are bounded and cleaned up when processing ends. Client clock skew can cause
early cancellation; server/database checks are authoritative. Network transit can
consume the remaining deadline after a claim is committed. Signal cancellation
is cooperative and cannot undo external effects or stop an uncooperative handler;
external storage must continue enforcing idempotency/fencing as appropriate.

## Recovery, visibility and events

Expired/invalid snapshots remain dead letters, preserving bounded capacity,
existing retention clocks, and explicit recovery controls. Single replay rejects
passed deadlines or invalid flags with 409. Bulk replay returns `deadline_expired`
or `invalid_deadline` per matching selection. Frozen deadlines are never extended
by replay. Future deadlines on ordinary dead failures can still be replayed before
they pass. Under skip ordering, later work can proceed past terminal expiry; no
historical ordering restoration is promised. To keep capacity available, configure
compatible dead-letter retention or explicitly retire/recreate the group.

An AFTER UPDATE trigger atomically increments cumulative decimal-string
`expired_deliveries` for deadline-expired and invalid-deadline transitions and
snapshots signed `realtime.consumer_group.delivery_expired` webhook recipients.
Payload includes event/app/endpoint/group IDs, channel/group, decimal sequence,
attempts, failure code, nullable delivery deadline and observation time. No message
body or lease credential is included. Existing dead-letter webhooks also apply;
expiry counters do not count successful completion and survive snapshot cleanup.
The inspector shows the immutable deadline field and terminal rejection counter.

New deadline freeze/expiry/replay-guard SQL uses generated sqlc methods and the
existing compile-time table projection. OpenAPI and generated Node/Python SDKs
expose creation fields, claim deadline, group counter and additive bulk outcomes.

## Rollout and verification

Apply migration `20261012102700501_realtime_delivery_deadlines.sql` after prior
realtime migrations. Upgrade all server replicas before creating deadline-enabled
groups, and upgrade workers before relying on cooperative cancellation. Older
servers do not enforce deadlines. Rollback removes deadlines, invalid flags and
counters, retains dead snapshots and queued webhook vocabulary, and cannot restore
expired work or its original deadline protections. Retire deadline-dependent work
before rolling back; already clipped lease timestamps remain until ordinary expiry.

Go/Node builds, SQLC regeneration parity, OpenAPI parity and Python static checks
are used for this change. Runtime tests, migration execution and inspector rendering
remain deferred at the user's request.
