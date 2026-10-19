# ADR-1476: Provider issue callbacks as durable refresh hints

Date: 2026-10-15

Status: Accepted

## Context

Native tracker delegation normally checks a policy every five minutes. Provider
callbacks should make external edits visible sooner without granting their payload
lifecycle authority or bypassing connection/group and workflow guards.

## Decision

GitHub `issues` events use the existing edge HMAC verification, githubd signature
recheck, durable delivery inbox and processing retries. Validate installation,
immutable repository ID and canonical ticket identity before inbox acceptance.
Legacy installation-secret mode requires its secret-selection header to match the
signed payload installation. Issue events bypass edge replay acknowledgment so an
upstream failure can be redelivered to the durable inbox. Other event paths retain
their existing replay behavior. githubd writes only bounded hint receipts; apid is
the sole scheduler and provider lifecycle writer.

Linear uses POST `/hooks/issue-trackers/linear/{app_id}` on the Gregale API origin.
Owners optionally provide `webhook_secret` with their verified Linear connection;
seal it inside the account/app/workspace/team/viewer credential envelope. Return
only `webhook_enabled` and `callback_path`. The receiver uses the signed Linear
protocol and follows the existing HMAC-webhook exclusion from Bearer DTO/SDK
route parity; its wire contract is documented here and in the Issues guide. Replacement without a secret disables
callbacks, and disconnect clears both credentials. Connection revision changes
still require explicit delegation review/re-enable. CLI configuration accepts
`--webhook-secret-env`; dashboard connection forms use an unfilled password input.

Verify Linear's raw-body SHA-256 HMAC, delivery UUID, signed workspace/team/issue
identity and signed `webhookTimestamp`. Unknown deliveries must be within one
minute in either direction. Authenticated known retries are acknowledged even
when their sending timestamp is old, without another wake. Canonical payload
hashing excludes only the sending timestamp so a refreshed retry timestamp does
not create another receipt. Reused delivery IDs with changed semantic payloads
are refused. Return HTTP 200 after durable acceptance, duplicate detection or an
untracked/paused reference; transient storage failures return non-200 for retry.

Receipts contain metadata and hashes, not Linear raw payloads or credentials.
Dedupe is scoped to GitHub installation or Linear app/connection revision, by
both delivery identity and payload hash. Match only current owned installations/
repository identities or current connected Linear workspace/team/revision and
active delegated ticket identities. Untracked, disabled and paused policies are
ignored. Receive at most 1 MiB, retain at most 10,000 receipts per scope, and keep
processed receipts for seven days. Limit both scheduling and cleanup to 100 per
worker pass, delivery IDs to 128 bytes, and signing secrets to 32–4096 bytes.
Linear receipt requests have a four-second budget, below its five-second sender
response deadline. All bounds live in `pkg/api/limits.go`.

Before claiming due delegation work, apid consumes hints transactionally and
advances eligible policies' next-check time. Lock policy rows in a stable order.
If any matching policy has a live reconciliation lease, keep the receipt pending;
its completion cannot overwrite a consumed wake. Invalidate expired claims before
scheduling so stale completions cannot erase the hint. Mark the receipt processed
in the same transaction. The normal worker rereads the provider using existing
scoped guards; manual edits become conflicts, and callbacks never pull resolutions
or resume paused delegation. Periodic polling remains recovery.

## Limits

Scheduling normally runs on the next maintenance pass, approximately a minute;
backlogs and active claims can delay it. Pending receipts are preserved instead of
being expired. The bounded scope can fill during sustained traffic or outages;
polling remains available. Provider and Gregale do not share an atomic transaction,
and already-dispatched writes cannot be recalled. GitHub App subscriptions must
include Issues events. Linear webhook creation remains a provider-admin step;
configure a team-scoped webhook for Issue events. Live provider credentials are
not part of local acceptance.

## Validation

Provider tests cover raw-body tampering, scope, freshness, stable retry hashing,
canonical identity and ignored pull requests. Credential tests preserve token
access while refusing cross-scope signing-secret access. GitHub tests exercise
signed durable inbox acceptance, failed scheduling retries and secret-installation
mismatches. Edge proxy tests ensure an upstream failure does not swallow a retry.
PostgreSQL acceptance covers both providers, duplicate/stale/foreign deliveries,
claim deferral and eventual wake, manual-edit conflicts without inbound resolution,
paused policies and disconnect. CLI/SDK contracts retain write-only secret handling
and expose only callback metadata.

## Operational visibility

The owner dashboard shows retained verified delivery count, pending hints, last
received time and last processed time for each linked ticket. Read these aggregates
in a repeatable-read transaction after checking the current app/account, canonical
issue group and link. Match only the currently owned GitHub installation/repository
or connected Linear revision/workspace/team. A disconnected or replaced connection
must not display old receipts as evidence of current callback health. Paused policies
can still display retained evidence without being resumed.

The status refresh control performs the existing explicit provider preview, including
fresh connection and workflow scope checks. It does not enqueue callback replay,
change the policy, or dispatch a provider write. Owners must explicitly review and
align a paused policy before re-enabling. Normal worker polling recovers missed
hints and observes uncertain writes with the existing safeguards.

Counts cover retained tracked deliveries, not all incoming HTTP requests. Duplicate
retries do not increase them; ignored and rejected requests are not retained. Last
processed means the hint was consumed, not provider confirmation. Paused or outdated
hints are consumed without scheduling.
ADR-1477 adds scoped Linear rejection counters and recent signed delivery evidence.
Live provider webhook registration checks remain unavailable; the dashboard must
describe this limit rather than imply provider-side registration was inspected. The callback path, connected team, configured signing
secret flag and fresh provider preview help owners check local setup.
