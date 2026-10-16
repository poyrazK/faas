# ADR-1367: Consumer-aware baseline recovery

Status: Accepted

## Context

Named-consumer watches resume durable progress, while initialization and expiry
recovery still require manually carrying consumer guards through a separate raw
schedule baseline. Concurrent acknowledgments or retirement can invalidate the
consumer while that multi-page baseline is being collected.

## Decision

Add read-only `gregale_recover_environment_cache_activity_consumer` with app,
project, named environment, consumer, optional recovery_token and bounded limit.
Reuse raw schedule baseline discovery, including its checkpoint captured before
listing, identity/retention validation, live pagination and overtaken-baseline
restart behavior. Keep existing 20/50 page and 8192-byte recovery-token bounds;
no new quota or native endpoint is needed.

Capture the consumer revision and connection generation before the first page.
Wrap incomplete raw continuation metadata in a strict versioned token containing
those guards and a hash binding the exact consumer/immutable target to the
connection namespace. Embed the raw token structure instead of double-encoding
it. Validate namespace/consumer binding, generation and revision on every page;
validate endpoint/account/scoped identities and checkpoint forward progress.
Consumer and raw recovery tokens cannot be interchanged. Tokens remain untrusted
pagination metadata, never authorization or processing evidence; deliberate
editing cannot establish that prior pages were consumed.

Check durable grant/storage health and the captured consumer guards after every
native recovery call, even failed ones. A detected revision change returns
checkpoint_conflict; retirement returns consumer_generation_conflict. Withhold
all baseline entries, tokens and ACK drafts on failure. Validate any forwarded
retention-restart metadata against the original consumer account and immutable
scope. Do not hold journal locks across native calls.

Incomplete responses contain one page and a consumer continuation, without an
activity cursor or ACK draft. Only a complete response includes the original
baseline activity cursor and acknowledgment arguments with the captured guards.
Existing consumers get baseline_rebuilt=true in that draft because the saved
checkpoint can expire while the later baseline checkpoint remains valid. New
consumers use expected revision zero. A consumer at exhausted revision capacity
must be inspected and explicitly retired before reinitialization.

Recovery never creates or updates progress. The caller processes every baseline
page and explicitly invokes ACK afterward, then resumes activity by consumer
name. ACK still validates current permissions, cursor retention, revision and
generation. A draft is not a lease or proof of downstream processing. On conflict
or recovery_restart_required, discard partial results and restart without the
token after inspecting current state. Reconnect/restart may reuse a valid token
with the same encrypted journal/key and namespace; no recovery session is saved.

## Consequences

The catalog adds one read tool (55 tools with native writes disabled, 72 enabled).
There are no native writes, SQL changes, migrations, journal-format upgrades,
background jobs or automatic acknowledgments. Use short-request admission and
observation drain. Consumer get/watch/review guidance now points to the scoped
consumer recovery tool; the raw schedule recovery tool remains available.

Portable tests cover empty and paginated baselines, reconnect and journal reopen,
earlier-sorting concurrent activity replay, expired progress and overtaken
checkpoints, token bounds/scope/namespace validation, acknowledgment and
retirement conflicts between and during pages, permission loss, revocation,
cancellation and drain. Pinned real Codex acceptance exercises initial recovery,
expired-consumer pagination, retention restart, bridge reopen and continuation,
explicit final ACK arguments, and stale revision/generation rejection with zero
native mutations and model turns. Live baseline pages and processing side
effects retain the existing idempotency and overlap responsibilities.
