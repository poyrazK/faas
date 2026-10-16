# ADR-1178: Idempotent subscription capacity commands

Status: Accepted
Date: 2026-10-13
Related: ADR-1161, ADR-1174, ADR-1177

## Decision

Add optional command_id to the existing subscription capacity PUT and CLI
--command-id UUID. Require a nonzero hyphenated UUID, normalize casing, and preserve
legacy behavior when omitted. Do not generate command UUIDs automatically: callers
save one UUID and repeat the same proposed value and optional expectations after
an uncertain response. Go/Node/Python request and response models expose command
evidence without adding routes.

Persist successful commands on event_subscription_capacity_commands, keyed by
subscription and command UUID with owned account/app scope. Store the normalized
request and immutable receipt as metadata-only JSON, plus recorded/retention times.
Record previous saved value, proposed value, original plan/effective allowance,
original expectations and applied/unchanged outcome. No event payloads or
credentials. Unchanged means the saved value was already equal; a first no-op can
still create/update control metadata, as with existing setters.

Acquire the existing plan row, capacity, app target and subscription locks. Check
owned scope before lookup. A known retained command is compared to its original
normalized request before current guards, plan proposal validation or receipt
budget. Matching repeats return the original acknowledgment and current control
state without changing capacity or its updated_at, consuming another slot or
extending retention. Later configuration and plan changes do not invalidate a
known successful acknowledgment. Different proposed values or guard presence/values
with the same retained UUID return 409 event_subscription_capacity_command_changed.
Malformed requests remain validation errors.

For a new UUID, validate current guards and plan allowance, enforce at most 128
retained receipts per subscription, and commit the setting and receipt together.
A full budget returns 429 event_subscription_capacity_command_limit with limit,
observed and documentation metadata, without applying. Known repeats and legacy
commands remain available. New no-op commands consume slots. Rejected requests
have no successful receipt and do not reserve the UUID. PostgreSQL transaction
rollback and MemStore's mutex/staged serialization prevent a successful setting
from lacking its acknowledgment. Deep-copy request/receipt evidence to avoid
caller mutation affecting later repeats.

## Retention and recovery

Fix each receipt's lifetime at 30 days from first success; repeats do not extend
it. Source limits live in pkg/api/limits.go. Expiry is checked at lookup and budget
reads, with expired per-subscription rows cleared on new successful command use.
A bounded scheduler sweep deletes up to 500 expired receipts on the existing event
retention cadence. MemStore mirrors logical expiry and bounded cleanup. PostgreSQL
foreign keys cascade parent control/app/account deletion; deleted targets are
unavailable and MemStore cleanup removes orphan evidence. Cleanup never changes
capacity settings.

Idempotency lasts only while the receipt is retained. After expiry or parent
deletion, do not treat an old UUID as proof: inspect status, review a fresh preview
and choose a fresh command UUID. An unguarded expired command may be applied again.
Keep the original proposed value and expectations for a retained repeat rather than
refreshing its guards; changing them conflicts. Guards still protect new commands
from stale settings, while successful repeats bypass those now-stale expectations.
No automatic retries, fallback or replacement UUIDs are introduced by the CLI.

CLI verifies receipt identity, scope, requested value, original expectations,
effective result and retention before reporting acknowledgment. Human output
separates original command outcome from current state. Missing/incompatible success
receipts remain unconfirmed. Repeats still require current ownership, write scope
and MFA; UUID possession does not authorize access. This is not an actor audit log,
capacity reservation, delivery-order guarantee or exactly-once handler execution.

## Rollout and validation

Apply the additive migration before upgraded API/scheduler writers. Legacy requests
remain supported, but only upgraded writers support command receipts. Downgrade
refuses while evidence rows remain. No migration is executed during implementation.
Prepare the eight-column operational clone registry table entry separately under
the user's registry restriction; apply only with explicit approval. Existing DTO
scanner and SDK route mapping already cover the extended operation.

Validate state, API, scheduler and SDK builds, generation/source parsing, SQLC
parity, SDK coverage and static ordering/protected-file checks. No local tests are
added or run. Database concurrency and retention qualification remain for CI.
