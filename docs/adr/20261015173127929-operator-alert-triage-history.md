# Operator alert triage history

Status: Accepted, 2026-10-15. Extends ADR-20261015162500000.

## Decision

Read the existing `operator.action.alert_triage` audit receipts for each retained
immutable episode. Every committed triage action advances the episode revision
and writes one receipt in the same transaction. Do not introduce a second
journal or change the mutation path. Order receipts by descending revision,
not received time: clocks can move backwards between serialized writes.

GET `/v1/admin/obs/incidents/platform_alert/{id}/triage-history` uses the
existing operator read scope, allowlist and MFA boundary. Default limit is 25,
maximum 100, with a five-second store budget. Legacy series aliases resolve to
the latest retained episode on an initial read. Responses identify the exact
episode. A cursor pins that episode, the first page's upper revision, the next
exclusive revision bound and snapshot time, and expires after 15 minutes.
Changing limits is permitted. Restart pagination when changing episodes.
An alias moving to another recurrence invalidates its earlier cursor.

PostgreSQL reads episode state, collection state, receipt counts and the page
in a read-only repeatable-read transaction. Memory uses one mutex. Evidence
includes the first history page in the same snapshot as its current triage
state. Later pages preserve the upper revision but can observe newer current
state and changes to receipt retention; pagination is not a durable database
snapshot across HTTP requests.

Coverage counts distinct retained revisions across the full snapshot range,
not just the page. `complete` means all revisions 1 through the snapshot
revision have receipts; `gaps` and `missing_receipts` report the difference.
Missing receipts do not identify the cause of absence. Duplicate or malformed
revision metadata and malformed selected receipt projections fail the read
rather than report complete history. Storage failures return unavailable, not
an empty page. Revision-zero episodes have an explicitly complete empty page.
Expired episodes return 404 even if independent audit receipts survive.
The existing episode retention policy is unchanged; this change introduces no
audit retention deletion policy or guaranteed audit lifetime.

Only receipt UUID, revision, action, received time, post-action acknowledgment
and owned flags, optional snooze deadline and validated trace links leave the
reader. Actor identities, owner account IDs, emails and raw audit payloads are
excluded from history. SQL projects this closed payload before decoding.
A partial index on exact episode ID and safely parsed revision supports the
read; malformed historic revision values index as NULL without failing DDL.
Full coverage still requires examining all matching episode revisions and is
bounded by the query deadline, not by the page limit.

## Evidence and CLI

Incident evidence retains its independent signal lifecycle history and adds
optional `triage_history`. Older servers or live compatibility alerts lacking
retained metadata remain unknown, not silently complete. `gregalectl obs
triage-history --incident platform_alert/EPISODE_ID --limit 25 --cursor CURSOR`
reads further pages; `--json` preserves the structured response. Human evidence
output includes its first page and coverage.

Diagnostic bundle v1 includes this first page through its existing evidence
read, without additional requests or following cursors or trace links. It
validates episode identity, bounds, revisions and coverage consistency and
reports gaps, truncation or missing history metadata. Typed decoding discards
unknown payload fields; current triage owner IDs remain redacted. Frontend,
rollout, deployment, notification delivery and public tenant SDK coverage are
outside this private operator read change.

## Validation

Exercise memory and PostgreSQL ordering with backwards clocks, concurrent
atomic snapshots, revision-pinned paging, recurrence isolation, duplicate and
missing receipts, malformed payloads, storage failures and PostgreSQL reopen.
Test shared Store conformance, operator read authorization, query validation,
evidence consistency, CLI pagination and bundle redaction/coverage. Check
migration apply/down/replay, SQLC drift, OpenAPI parity, generated Node client,
repository policies and configured lint.
