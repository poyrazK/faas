# ADR-1524: Durable operator recovery submissions

Date: 2026-10-17
Status: Accepted; portable verification passed

## Context

Restart, park and cold-boot HTTP requests insert an intent before the generic
24-hour response cache is saved. A lost response or API crash can leave work
accepted without a replay record. A later retry can submit another recovery
action. The cache also ignores query parameters, including the audit reason.

## Decision

1. These three routes retain every provider mutation authentication gate:
   MFA, admin scope, operator allowlist, fresh session step-up and same origin.
   Their domain handler requires an Idempotency-Key and an empty request body;
   only one confirm=true and at most one reason query parameter are accepted.
   They bypass the generic expiring response cache.
2. One transaction serializes each authenticated actor/key pair, checks its
   durable submission before mutable target validation, and atomically inserts
   a pending intent, the exact 202 response bytes, and an attributed request
   audit outbox item. The response and original trace survive API replacement,
   target changes, worker completion and outbox pruning. Notification is a
   best-effort wakeup after commit; the existing scheduler safety tick recovers
   missed wakeups.
3. Keys are hashed at rest and scoped to the actor account across these three
   routes. A versioned fingerprint binds kind, requested target and normalized
   reason. Instance UUID spellings are canonicalized. Omitted/empty reasons
   select the documented default. Trace IDs are attribution, not parameters.
   A changed kind, target or reason with the same key returns 409. Replays
   always run current authentication and return the original response bytes.
4. Cold-boot resolves the slug's latest deployment once, at acceptance. Replay
   returns that deployment even after another deploy or slug recreation.
   Instance operations retain their existing eligibility gates. Unresolvable
   ownership fails closed rather than accepting an unattributed target.
5. Receipts have no automatic expiry or pruning. Their foreign key prevents
   intent deletion from erasing the replay boundary. Audit outbox IDs are
   evidence without a foreign key to the prunable delivery queue. The response
   expires_at remains an advisory polling horizon, not receipt retention.
6. gregalectl prints a reusable request key to stderr before submission and
   accepts --request-key for explicit retry. instances wait --intent-id resumes
   polling without a POST. Unknown outcomes still stop automatic redispatch.

## Rollout and legacy boundary

Apply the migration, drain all old apid processes and outstanding recovery
POSTs, then start upgraded apid processes. Mixed old/new recovery writers are
unsupported. An extant generic cache/reservation for the same actor/key and
route returns 409 operator_submission_legacy, even if expired: its parameters
and atomic acceptance cannot be established. Reconcile the existing intent
before choosing a new key. Older uncertain submissions whose cache write was
lost or already pruned cannot be reconstructed; do not resubmit them as new
requests during rollout. Existing intent polling remains available.

Downgrade refuses to discard retained submission receipts. No VM lifecycle or
scheduler ownership changes are introduced by this decision.

## Verification

Shared memory/PostgreSQL tests cover original-byte replay, target changes,
fingerprint conflicts, actor isolation, concurrency and outbox pruning.
PostgreSQL fault injection checks intent, audit, receipt and commit failures
leave no partial acceptance. HTTP tests cover lost replies, fresh API instances,
current authentication, input validation and cache bypass. CLI tests cover key
reuse after uncertain submission and polling without another POST.

Shared memory/PostgreSQL cases, seven database fault cases, retained-evidence
and expired-legacy-reservation tests pass. HTTP recovery, admin security, legacy
force-action, intent polling and audit delivery regressions pass. CLI recovery
and memory race tests pass. SQLC regeneration, restored-schema parity, OpenAPI
AST/embedded-spec checks, scoped production/test lint and apid/gregalectl link
builds pass. Node/Python SDKs are regenerated; Node builds, and both SDKs preserve
explicit keys/empty bodies in request smoke checks. State checks run as external
file suites to avoid unrelated test-package compiler pressure. No deployment
has been performed. This submission change adds no VM lifecycle behavior;
ADR-1522/1523 native qualification requirements remain in effect. Full OpenAPI
rule lint passes with the same pre-existing warnings/informs as HEAD.
