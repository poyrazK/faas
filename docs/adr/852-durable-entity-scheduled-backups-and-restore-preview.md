# ADR-852: Scheduled application-state backups and restore preview

Status: local implementation; qualification pending.

## Decision

Add an operator preview worker, default off, gated by
`FAAS_DURABLE_ENTITY_BACKUPS_ENABLED=1` and the existing invocation preview and app
allowlist. Startup probes delimiter/flat listing, conditional create and deletion
using a unique backup probe. No production setting is changed by this work.

The worker visits eight entity prefixes every thirty seconds with a twenty-second
scan deadline and two-second per-entity deadline. It captures at most one backup
per entity per UTC hour. The central limits in `pkg/api/limits.go` define these
budgets and a seven-day retention policy. Capture time is the hourly slot start,
not an exact wall-clock timestamp for the underlying application transition.
Scheduling is best effort: large scans, downtime or storage failures can miss hours.
There is no automatic backfill of missed slots. Cursor progress is process-local;
restarts begin discovery again. Partial failures advance rotation and retry on the
next visit. Aggregate scan outcomes have no entity IDs or customer data as labels.

Backups live in the private `gregale/durable-entity-backups/v1/` namespace, keyed by
the hash of the complete immutable entity scope and an exact UTC hourly slot.
They contain ADR-850 application exports, not ownership, receipts, alarms or outgoing
work. Reads verify identity, export checksum and slot. Conditional create makes the
first successful capture authoritative for a slot. Concurrent writers read the
winning object; uncertain writes can be confirmed on the next visit. Capture does
not acquire ownership, change business versions or invoke guest code.

After confirming a current-hour backup, each visit lists up to thirty-two keys and
deletes only strictly older-than-seven-days, syntactically valid backup slot keys.
Deletion never touches live entity objects. Provider listing must be ordered by
key for pruning from the first page to progress. Delayed old captures can appear
after pruning and are reclaimed on a subsequent visit. Retention is eventual;
failed writes never authorize pruning. Disabling the worker, removing an app from
the allowlist, or deleting an entity stops these visits. Operators must also apply
a backup-prefix lifecycle/deletion policy for orphaned data and account erasure.
The private bucket remains in the same failure domain; this is not off-provider
disaster recovery. Backup bytes are separately retained physical storage and are
not included in the live entity's committed storage accounting.

## Owner read surface

Add read-scoped, MFA/rate-limit wrapped owner routes:

- `GET /v1/apps/{slug}/entities/backups`: bounded metadata page with cursor.
- `GET /v1/apps/{slug}/entities/backups/get`: exact slot's sensitive backup export.
- `POST /v1/apps/{slug}/entities/restore/preview`: read-only diagnostic using a
  restore-shaped body. The request ID is not consumed or journaled.

They resolve authorized immutable account/app/environment/tenant scope and require
app enablement; they permit diagnostic reads after plan downgrade/tenant suspension.
Responses are private/no-store and contain no claim token or bucket path. No generic
HTTP mutation idempotency cache is used. Backup reads remain available after the
backup worker is disabled, while the entity preview/app allowlist remains enabled.

Preview verifies export scope/checksum and reports one consistent observed current
version, source version, expected-version match, recognizable application schema
versions and their relation, current alarm/outbox counts and exhaustion. A second
manifest read detects concurrent publication/authority metadata changes. Compatibility
is always `unverified`; matching schema numbers cannot validate application data or
the deployed application's validators. Unwrapped/malformed schema envelopes report
an unknown relation. The preview does not simulate storage accounting or receipt
publication and cannot promise capacity. Actual restore independently validates,
acquires an existing-only claim, checks the expected version and commits via CAS.

OpenAPI, embedded spec, Go source-mirrored methods, generated Node/Python clients
and Node convenience wrappers expose all three operations. Node wrappers reject
unsafe business versions. Keep export JSON unchanged to preserve checksum fidelity.

## Qualification

Written source cases cover immutable hourly capture, uncertain write recovery,
separate retention, no prune without a current capture, corrupt/foreign backup
rejection, allowlist scanning, read-only preview, preserved exhausted work, owner
read-key API flow, selectors/cursors and SDK version precision. Tests, builds and
provider qualification have not run here, per the user's testing-agent handoff.
No PR, deployment or production gate enablement is included. Qualification must
also exercise multi-process capture, provider list/delete semantics, missing slots,
slow scans, lifecycle/account deletion and live-bucket round trips before release.
