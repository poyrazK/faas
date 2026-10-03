# ADR-500: Bounded workflow iteration

Status: Accepted

## Context

Customer automations can branch and join, but a list of recipients, invoices or
records still needs a custom handler to orchestrate repeated calls. A durable
batch must recover without restarting completed items, expanding indefinitely,
changing retry inputs or bypassing managed integration authorization.

## Decision

Add `for_each: {items: input.items, action: {run: send}}` as an exclusive native
workflow step target. Its source is a JSON array selected through the existing
reference grammar, from run input or a declared direct dependency's output.
The body is exactly one app handler (`run` or `path`) or managed `outbound`
action, with optional method, input mapping, timeout and retry. It cannot contain
dependencies, guards, exception routes, waits, joins or nested iteration.
The parent can have dependencies and a guard, but no execution options or
exception routes; it cannot be an exception-handler target.

The first version runs sequentially and stops on the first terminal item failure.
Limits in `pkg/api/limits.go` permit 128 items, 64 UTF-8 bytes in a parent name,
1 MiB in the source array, 1 MiB in the total prepared inputs, and 1 MiB in the
collected output. Resolve and persist the entire array and every action input in
one transaction before any item executes. Omitted body input sends the item;
explicit templates read `input.item`, `input.index`, `input.input` (original run
input) and parent dependency outputs. Inserted values are never interpreted as
templates again. Invalid sources or mappings fail before any item action.

Reuse workflow step and attempt tables for item records. Names encode the parent
with raw URL-safe base64 followed by a canonical zero-based decimal index under
the reserved `_foreach.` prefix. Persist `foreach_parent` and `foreach_index` on
items, and `foreach_count` on the parent. A self foreign key and uniqueness/check
constraints enforce the identity. The parent stays pending during collection,
consumes zero attempts and succeeds with an ordered JSON array of item outputs.
An empty source succeeds with `[]`. Failed parents retain the completed prefix;
remaining items become skipped with `dependency_failed`.

The scheduler advances through repeated passes without recursion, releasing
each pass's snapshots before reading the next. Item expansion must not retain
every earlier copy of run output and step state on the call stack.

All store mutations acquire the run lock before step locks. Starting an item
requires a live claimed run, the initialized pending parent, every previous item
succeeded, the snapshotted input, and the next attempt number. Successful
completion checks the aggregate output cap before closing the attempt. Retries
keep their durable deadline. Interrupted item attempts are closed and consumed,
so an obsolete worker cannot complete a replacement attempt. Completion and
retry also reject expired item leases. App calls poll the current attempt and
cancel when its nonce/lease or run eligibility changes. Cancellation atomically
closes active item attempts and skips unfinished records.

Each item uses `workflow/<run UUID>/<internal item name>` as its stable
idempotency key. App handlers must honor it to deduplicate effects after an
uncertain result; execution remains at least once. Managed integrations retain
ADR-489's policy: unknown mutating results are terminal unless the provider
supports idempotency; GET/HEAD are replayable. Failed response bodies are omitted
from item errors. Native action outputs retain `{status, body}`.

Publication validates nested managed actions against the same account/app
bindings and sealed credentials. outboundd resolves the action only from the
immutable parent definition and the canonical persisted item identity. Existing
lease, nonce, attempt, route, body hash, credential revocation and policy checks
remain in force. Neither a parent nor a queued or obsolete item can authorize a
provider call. Loop metadata appears in existing inspection APIs; item attempt
history uses the existing attempts endpoint. OpenAPI, Node/Python SDKs and
JSON/YAML manifests support the target. No frontend or VM lifecycle change applies.

## Rollout and rollback

Apply `20261003160000001_workflow_foreach.sql` before deploying updated apid,
schedd and outboundd, then publish iteration definitions. The existing workflow
and native outbound gates apply; no new production flag is set by this change.

Before downgrading, pause starts, drain/cancel iteration runs and replace every
API and manifest definition containing `for_each`. Stop updated binaries before
reverting the migration. The Down migration deletes internal item records and
their cascading attempt history so older schedulers never see unsupported
records; export that history first if it is required. Parent/run history remains.

## Verification

API/manifest tests reject unsupported targets, options and references and retain
typed mappings. Memory and actual PostgreSQL race tests exercise atomic snapshots,
sequential admission, cancellation, output limits, explicit and expired-lease
recovery, stale completion fences and outbound replay policy. Scheduler tests
verify ordered results, empty lists, retry deadlines, guards and stopping on
failure. PostgreSQL authorization tests verify nested binding validation and
reject forged, queued, recovered and cancelled identities. API and SDK tests
preserve zero counts/indexes and inspect ordered results. Migration Up/Down is
checked against a disposable PostgreSQL database.
