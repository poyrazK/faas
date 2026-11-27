# Run pipeline completion and durable Agent continuation

Status: accepted for default-off preview; scoped control-plane qualification passed;
production release pending. See [the evidence](../ops/evidence/20261116-run-pipeline-completion/README.md).

## Context

ADR-20261116160000 executes bounded task graphs after the submitting process
exits. Its per-task completion bridge does not provide a single terminal result
for the complete pipeline. Agents need a durable completion identity and scoped
access to every attempt without receiving source, input or large result bytes in
their inbox.

## Decision

Persist a versioned, metadata-only completion manifest on the pipeline sidecar in
the same transaction that makes its managed job terminal and erases the encrypted
plan. The managed job UUID is the immutable completion identity. The manifest
records pipeline outcome, step/task pins, contract verdicts, physical attempts,
artifact metadata/references and aggregate host measurements. PostgreSQL workflow
lookups use a full scoped receipt through SQLC so continuation sees retry failure
codes, finish times, artifacts, contract verdicts and host usage. Wall and CPU time
sum all attempts; peak memory is the maximum attempt peak, not concurrent
residency or a billing total. Existing Run receipts retain results and artifact
bytes under their existing authorization and expiry. Snapshot size is bounded
in `pkg/api/limits.go`.

Emit `execution.pipeline.finished` through the existing signed account webhook
outbox. Capture the receiver obligations in the terminal transaction; existing
retry, dead-letter and redrive retain the same event identity. Payloads contain
only terminal metadata and the authorized workflow receipt reference.

Add `gregale_pipeline` to the existing RAP toolset behind the Run pipeline gates.
An app credential must own the callback endpoint; the callback URL alone cannot
spend compute. Arguments contain bounded graphs/batches of named task versions,
JSON inputs, dependency/result contracts and pipeline-owned artifact handoffs.
Raw source, input file capabilities, customer identity overrides and custom
workflow/callback identities are refused. Every task must be delegated to the
endpoint, checked at compilation, atomic pipeline creation and each attempt.
Each physical attempt retains the live endpoint/customer binding, task budget,
ordinary reservation and settlement. It does not create an individual task
callback: one terminal pipeline wakes the waiting tool call.

Derive workflow identity from endpoint, thread and logical tool-call ID. Bind the
normalized invocation and provider call identity to submission replay, while
canonicalizing Agent JSON object keys with number precision intact for JSONB
round trips and
retaining the originally captured callback issue time. Exact replay works with
writer gates disabled and after task retirement, without re-admission. Changed
intent returns conflict.

Pending Agent deliveries are durable pipeline rows, not process memory. Every
capable apid scans them and publishes into the existing inbox with the stable
thread/tool-result identity before marking delivery complete. Capability disable
retains pending obligations. Revocation or endpoint deletion suppresses delivery
and scoped reads. App credentials can read only pipeline manifests and Run
receipts/artifacts linked to their endpoint and live original thread grant.
The Node starter supplies pipeline dispatch and scoped manifest reads.

## Compatibility and recovery

All public response additions are optional. Existing managed status enums remain:
cancellation/deadline are managed `failed`, with a more precise manifest outcome.
Pipelines admitted before this increment obtain a manifest when a capable worker
finishes them; already-terminal legacy rows need no backfill. Cancellation and
deadline completion use non-secret metadata even without plan decryption keys.
Step-level contract verdicts unavailable on that path remain omitted.

Nullable sidecar columns, separately validated constraints, a concurrent partial
index and staged receiver-constraint expansion preserve N-1 readers. New submissions carry a completion-version marker. A database
fence prevents an old terminal writer from erasing a completion-enabled pipeline
without its manifest. Keep capable continuation/delivery workers until these obligations
have drained; disabling new writers preserves reads and delivery. Roll back
binaries only after that drain. No VM/guest protocol changes are introduced.

Delivery and inbox processing are at least once. Stable identities deduplicate
republication; applications still need idempotency for their own external
side effects. Run owns bounded execution; Agents owns reasoning and durable turns.
