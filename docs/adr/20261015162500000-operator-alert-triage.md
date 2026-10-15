# Durable operator alert triage

Status: Accepted, 2026-10-15. Extends retained operator alert episodes.

## Decision

Keep human triage separate from observed firing, resolved, or unknown status.
Persist a revision, acknowledgment flag, optional owner account ID, optional
snooze deadline, and update time on each immutable retained episode. Initial
revision is zero. Collector upserts never update these fields. A recurrence gets
its own episode ID and starts unacknowledged, unowned, and unsnoozed. Triage
expires with the episode under the existing 30-day retention policy; audit rows
remain under the existing independent audit-log retention policy.

POST `/v1/admin/obs/incidents/platform_alert/{episode_id}/triage` accepts
`action`, mandatory `expected_revision`, and `snoozed_until` only for snooze.
Actions are `acknowledge`, `unacknowledge`, `claim`, `release`, `snooze`, and
`unsnooze`. Require the existing allowlisted operator session, completed MFA,
recent five-minute step-up, trusted browser origin, and Idempotency-Key policy.
Bearer keys cannot mutate triage. Unknown request fields are rejected. Reads
retain existing operator read authorization.

Writes require the exact immutable episode ID. Legacy series aliases remain
readable but cannot be mutated. Lock the episode, check its revision, and commit
the update and `operator.action.alert_triage` audit row in one transaction.
Audit failure rolls back the update. Revision mismatch returns 409. Claiming an
owned episode or releasing another operator's claim also returns 409; this
surface provides no takeover or arbitrary assignment. Every accepted action,
including one that repeats the existing value, advances the revision and has
one audit entry. A replay through the existing idempotency middleware returns
its saved response; concurrent duplicate requests remain protected by revision
checking. Revision values are bounded to the exact JSON integer range.

Snooze deadlines must be strictly in the future and at most 24 hours ahead.
The stored deadline is preserved after expiry; `snoozed` is computed as deadline
strictly greater than the response's `as_of`. Expiry needs no background writer
and does not advance the revision. This snooze organizes the incident inbox;
it does not silence Prometheus, Alertmanager, or any notification delivery.
Acknowledgment and ownership can be updated on resolved retained episodes.

## Reads and CLI

Incident inbox items and evidence include optional `triage` metadata: revision,
acknowledgment, owned flag, owner account ID, snooze deadline and active flag,
update timestamp, and as-of timestamp. Live compatibility alerts without a
retained episode have no triage metadata and cannot be mutated. Missing triage
metadata from an older server must not be interpreted as revision zero.

Inbox filters require `type=platform_alert`:

- `acknowledgment=acknowledged|unacknowledged`
- `ownership=owned|unowned|mine`
- `snooze=active|inactive`

Both stores apply filters before ordering and pagination. Filters compose with
severity and observed `alert_status`. Each page is a current read, not a pinned
snapshot; restart pagination when changing filters. Before any successful
collection, triage-filtered reads return an empty page with history unavailable,
not a live-series projection pretending to have durable workflow state.

`gregalectl obs incidents` exposes these flags plus `--alert-status`. `mine`
uses the saved operator session. Other reads retain admin bearer compatibility.
`gregalectl obs triage` uses the saved operator session and requires an explicit
revision; it never fetches a revision and silently retries an action. For example:

```
gregalectl obs evidence --incident platform_alert/EPISODE_ID --json
gregalectl auth step-up
gregalectl obs triage --incident platform_alert/EPISODE_ID --action claim --expected-revision 0 --json
gregalectl obs triage --incident platform_alert/EPISODE_ID --action acknowledge --expected-revision 1
gregalectl obs incidents --type platform_alert --ownership mine --snooze inactive
```

Diagnostic bundle v1 includes triage through the existing evidence section,
with owner account ID removed before serialization. The owned boolean and
workflow revision remain, while the existing actor-identity exclusion holds.
Evidence and context remain separate reads; the bundle does not promise atomic
triage or resource snapshots. No frontend, rollout, or notification changes.

## Validation

Test both memory and PostgreSQL storage for concurrent claim conflicts,
revision rejection, recurrence isolation, ownership boundaries, snooze expiry,
filters before limits, and audited changes. Reopen PostgreSQL storage to check
durability and force audit rejection to verify rollback. Exercise API session,
origin, allowlist, idempotency, validation, evidence and filter behavior; CLI
session/revision requests and bundle identity redaction. Verify migration apply,
rollback/replay safety, SQLC drift, repository policy gates and configured lint.
