# ADR-100 — Trigger primitive (event-source mappings)

## Status

Accepted, single-branch mega-PR (commit #1..#21).

## Context

Gregale today has six customer-facing invocation surfaces — HTTP, cron, async invoke, per-app FIFO queue, delayed tasks, and outbound webhook — implemented as four unrelated products with no shared "trigger" abstraction. The unified `invocations` table (`pkg/sched/drain.go`) is fan-in for the producer side, but each surface has its own DTO, route family, store helper, and audit-event kind. There is no pull-from-stream primitive at all (no SQS/Kafka/NATS/Redis-streams), no batching beyond a single-record synthetic wake, no platform-side filter evaluation, and no partial-failure handling.

Issue #757 (`Event source mappings: Kafka / Kinesis / DynamoDB Streams / MSK / RabbitMQ / DocumentDB`) closes that gap by introducing one unified `Trigger` resource with a `kind` discriminator.

## Decision

### Resource model

One `Trigger` row per customer surface, with `kind ∈ {cron, kafka, nats, redis_streams, sqs_compat, queue}`. Five of the six kinds share the same batch/filter/retry/`ReportBatchItemFailures` machinery; cron remains a thin pointer to the existing `crons` table so the 5-field robfig schedule parser path is unchanged.

`pkg/api/trigger.go` (commit #6) is the canonical wire shape. `pkg/gregalemanifest/manifest.go` (commit #7) accepts the same shape via `gregale.yaml`. The apid HTTP layer (commits #5/#6) and the manifest application path share the same per-kind validator to keep the two surfaces in lock-step.

### Storage

Three new tables in one migration (slot 00267):

- `triggers` — the resource itself
- `trigger_records` — per-record audit ledger (FSM: pending → claimed → succeeded/retry/dead_letter)
- `trigger_dead_letter` — terminal failures with a CHECK-pinned `reason` enum

The `invocations.source` CHECK is widened to admit `esm` so a single audit row can trace back from a trigger fire to the broker-delivered record. The `instances.kind` CHECK is NOT widened — ESM runs on the same VM flavor as customer HTTP (`wake`); only the audit vocabulary gains a new `trigger.*` prefix.

### Runtime

`pkg/sched/dispatch_triggers.go::runTriggerTick` (commit #14) is the sibling of `runCronTick`. 1-second cadence, walks every enabled trigger, polls its broker adapter (commit #8..12), claims the per-record FSM rows via `ClaimTriggerRecords` (FOR UPDATE SKIP LOCKED, ADR-099 PR-C precedent), batches the records (size + 6MB cap), posts the batch envelope to `pkg/gateway/synth.go::handleInvocationDispatchBatch` (commit #13), parses per-record status, and transitions rows.

The dispatch tick is the ONLY consumer that needs the new batching + `ReportBatchItemFailures` machinery. Existing async_invoke / cron / queue traffic keeps its single-record semantics.

### Wire envelope

`POST /v1/invocations:dispatch_batch` (commit #13) on the gateway. The function returns `{"batchItemFailures":[{"itemIdentifier":"..."}]}` — stolen verbatim from AWS Lambda. Empty/missing ⇒ full success.

### Audit + wire events

Four new audit kinds (commit #15):

- `trigger.fired`         per-record: broker delivered + dispatched
- `trigger.fired.batch`   per-batch: aggregated outcome counts
- `trigger.retry`         per-record: state → retry, next_fire_at
- `trigger.dlq`           per-record: state → dead_letter

Two new pg_notify channels (commit #16):

- `NotifyTriggerReady`    schedd wakeup (every broker-delivered record)
- `NotifyTriggerChanged`  apid → schedd + dashboard SSE (CRUD + pause/resume)

## Consequences

### Positive

- One unified resource + one batch envelope + one FSM for every event-source-mapping surface.
- Plan caps in one table (`pkg/api/limits.go::Limits`, commit #4) — Free 0, Hobby 2/app, Pro 10/app, Scale 50/app.
- Existing cron / async / queue traffic is untouched; the trigger path is additive.
- Broker adapters are one file each (commits #8..12) so a single broker can be reverted without losing the others.

### Negative

- Six CI gates simultaneously (broker deps + sqlc regen + plan caps + apid + manifest + broker adapters).
- Broker client CVE exposure — every broker library added in commit #1 must be on the supply-chain watchlist.
- Daily rebase against main for the entire 21-commit cluster.

## Deviations from the spec

- **Mega-PR not PR-cluster**: the user explicitly picked a single-branch mega-PR rather than the staged PR-cluster pattern from ADR-099. The 5-layer internal commit structure (deps / schema / api+manifest / drain+poller / spec+SDK) preserves partial cherry-pick windows if the branch stalls.
- **Instances.kind NOT widened**: ESM uses `wake`, not a new execution-environment class. Mirrors Lambda's ESM.
- **In-platform queue unifies FIFO + delayed_task** under `kind=queue` with a `source` discriminator (`queue` or `delayed_task`). The pre-existing `invocations.source IN ('queue','delayed_task')` rows are read by `poller_queue.go`.

## Implementation commits

See commit #1..#21 of feat-triggers-mega. Layer boundaries (for partial cherry-picks):

- Layer 1 (1-3): schema-only; can land as default-OFF storage.
- Layer 2 (4-7): plan + api + manifest; no broker yet, no runtime dispatch.
- Layer 3 (8, 13-16): drain+poller scaffolding; broker-independent.
- Layer 4 (9-12): broker integrations; one commit per broker.
- Layer 5 (17-21): spec + SDK + e2e + leakcheck.

## Spec reconciliation (issue #757 closure, 2026-08-19)

PR #910 shipped 12/12 plan items but only 1/9 literal
acceptance criteria from issue #757's body — the issue was
filed in the older "ESM" framing (`pkg/esm/`, `kafka_sources`,
`schedd_esm_*` metrics, `esm.*` audit kinds,
`faas-tenant.slice` egress) that was deliberately folded into
the broader `trigger.*` / `pkg/sched/` namespace during
planning. The closure pass (ADR-118, mega-PR commits 1-11)
restores the operator-facing vocabulary without re-forking
the codebase:

- `esm.*` audit kinds are dual-emitted from the same call sites
  as `trigger.*` — both land in the audit log inside one
  transaction. See ADR-118 §1.
- `schedd_esm_*` Prometheus collectors are the operator-facing
  metric names, even though the Go types live under
  `pkg/sched/`. The 3 collectors (polls, records_consumed,
  lag_seconds) cover the metrics surface from issue #757
  criterion 7. See ADR-118 §2.
- `pkg/esm/` is NOT created. The ESM drain/extension lives in
  `pkg/sched/dispatch_triggers.go` (this ADR's §Deviations).
  Issue #757 criterion 3 maps to the spec reconciliation note
  rather than a code change. See ADR-118 §3.

The remaining 5 unmet criteria (#2 SASL/TLS, #4 FilterCriteria,
#5 Kafka e2e, #6 plan caps, #9 broker egress) are addressed
in commits 1-10 of the closure mega-PR. ADR-118 is the
narrative; this section is the bridge.

## Addendum (issue #757 closure complete)

The 11-commit closure mega-PR has landed. The trigger primitive
is the superset (this ADR); the ESM operator vocabulary is the
prefix alias (ADR-118). Customers see both surfaces. SDKs and
the OpenAPI document both. New triggers can be created via
either surface; the wire is the same row.

## Security and plan-policy addendum (2026-09-09)

The account response is the canonical console capability handshake. Free
accounts may not create external triggers; Hobby accounts may create only
`sqs_compat` and `queue`; Pro and Scale accounts may create all five
non-cron kinds. Both single-create and manifest batch-create enforce this
matrix. Omitted delivery settings resolve to the smaller of the platform
default and the account plan cap, while explicit over-cap values fail.

Kafka `sasl.password` and `tls.client_key` are write-only credentials. Apid
seals each leaf independently with the host X25519 recipient before writing
JSONB, under the namespaces `trigger_kafka_sasl_password` and
`trigger_kafka_tls_client_key`. Customer responses expose only
`password_set` and `client_key_set`; neither plaintext nor the internal
ciphertext field names cross the response boundary.

Schedd opens credentials only in a copied trigger row immediately before
poller construction. It loads the current and `.previous` host identities so
rows remain readable during the rotation overlap. Legacy plaintext rows are
accepted temporarily for rollout compatibility; every new or updated secret
is sealed. Missing key material or corrupt envelopes fail closed with a
sanitized error and no credential or ciphertext content in logs or responses.

Kafka config replacement preserves a stored secret only when its containing
`sasl` or `tls` block is supplied without the secret leaf. Omitting the whole
block removes it and its credential. Supplying a new secret rotates it.
