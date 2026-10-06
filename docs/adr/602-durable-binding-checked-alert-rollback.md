# ADR-602: Durable binding-checked alert rollback

Status: Accepted

Date: 2026-10-05

## Context

An alert fire previously committed a webhook delivery and cooldown before invoking an in-process deployment callback. A meterd crash or webhook-secret failure could lose that callback. Automatic rollback used the legacy historical rollback API, which rejects stored binding enforcement and could choose a different release on retries.

## Decision

- Capture an `alert_rollback_actions` outbox receipt in the same transaction as a production `ClaimAlertFire` for `action=rollback`. Pin the sole active canary and its sole older, positive-traffic predecessor in the same scope, under the app lock. Duplicate alert claims create one action. Missing, ambiguous, account-wide and service targets fail closed immediately; they never select a target later. Pre-auth metrics never enqueue deployment mutations. This is meterd alert bookkeeping; APID remains the only deployment writer.
- APID owns bounded, restart-safe processing. The existing exact canary recovery primitive aborts the candidate and restores the retained predecessor under fresh binding policy, inventory, evidence expiry and revision fences. Workers perform no probes. A blocked action retains its pair and sanitized blockers; a replacement APID retries that pair. Changed pairs, disabled or changed-action rules fail terminally.
- Lock the app, action receipt and rule for final selection validation. Commit traffic, aborted state, `deployment_audit.alert_rule_id`, fire attribution, completion receipt and transactional routing notification together. Duplicate processing returns the same terminal receipt and adds no audit. Late blocked writes cannot replace completion. This workflow does not bypass service gateway ACK/drain barriers: service rollback automation remains unsupported by this slice.
- Add a loopback action-token route to process a known fire for low-latency meterd callbacks. A callback never submits or reselects deployment IDs. APID's sweep handles the crash window even when that callback never runs. Do not fall back to legacy rollback when a durable fire is missing or the server is older.
- Expose owned, read-only list/detail endpoints and `gregale alerts actions --app APP [--fire UUID]`. Show pending/blocked/complete/failed, exact IDs, rule, audit and binding blockers. These reads never trigger recovery. Match existing alert MFA and read scopes. Go, Node and Python SDK contracts are additive.
- Existing alert delivery retention and deletion apply: deleting a rule cascades its deliveries and action ledger. Ledger receipts contain bounded blocker metadata and no secret values or reusable binding grants.

This supersedes ADR-601's temporary legacy automatic alert rollback behavior. Historical rollback remains its separate readiness-gated workflow. Demote and promote action behavior is unchanged.

## Validation

Memory/PostgreSQL parity covers atomic capture, cooldown deduplication, immutable selection, ambiguous targets, pre-auth exclusion, disabled rules, concurrent completion, stale/expired evidence, late blocker writes and audit attribution. API tests cover fresh predecessor evidence, replacement-APID recovery, duplicate internal callbacks, public status ownership and GET-only CLI reads. Generated SQL, OpenAPI and SDKs remain synchronized.

This slice changes alert bookkeeping and APID traffic recovery only; it adds no VM lifecycle calls. The native x86_64 Linux KVM acceptance gap from ADR-601 remains pending.
