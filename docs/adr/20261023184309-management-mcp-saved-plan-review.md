# ADR-20261023184309: Management MCP saved-plan review

## Status

Accepted — 2026-10-23. Extends ADR-20261022161757,
ADR-20261022192200 and ADR-20261023130958.

## Context

Connection-local action history recovers original plan IDs after a lost response
or encrypted bridge restart. Its redacted metadata deliberately omits proposed
changes. An agent that lost its initial planning response needs to reopen the
original proposal before guarded apply or retry, without creating a replacement
plan or mistaking saved intent for current native eligibility.

## Decision

Add read-only `gregale_get_plan` with the same app/project/environment selector
as app action detail, and `gregale_get_environment_plan` with an exact project
and target environment. Both require the original connection-local plan ID.
History entries with available plans provide exact review tool/argument hints;
expired entries remain historical metadata without a review handle.

Return a fixed projection of the original unexpired proposal: target identities,
reason/display text, traffic or consumer effect, expiry, immutable image or
source provenance, pinned baseline revisions and workload policy where relevant.
Budget reviews expose before/proposed advisory settings without request IDs.
Regression reviews expose the original selected route, observation version and
triage change only under current diagnostic consent. Environment reviews expose
saved workload release identities/revisions and approval requirements. Rollback
plans retain only their exact promotion reference; explicitly label that limited
coverage rather than inventing a captured workload diff.

Construct projections field by field. Exclude native request/retry keys,
execution receipts, promotion hashes/tokens, approval credentials, browser-review
URLs/tickets/exchange codes, secret changes and environment-variable values. Never
serialize a stored plan or arbitrary native DTO wholesale. Apply the existing
credential/display sanitizer, and treat all returned text as untrusted data.
Encode while holding the management lock to produce an independent immutable
snapshot and preserve integer revision precision.

Bind reads to current account/API, immutable app/project/environment identities
and read consent before releasing data. Environment reviews require both source
and target consent and every current project workload. Recheck the target after
source lookups, and recheck regression diagnostic consent, storage availability,
original plan presence and expiry at the end of the read. Storage failure or a
changed/recreated target withholds the saved proposal. Different grants cannot
read one another's private plans.

Label the result `review_kind: saved_original_proposal`,
`apply_eligibility: not_checked` and `native_completion: not_observed`. Reopening
performs identity/consent reads only: it never resolves a source branch again,
refreshes native evidence or release previews, polls human approval, changes
replay payloads, writes state, or extends expiry. The original guarded mutation
tool still enforces current authorization, native CAS and human approval.
Expiry returns no private proposal; action history and explicit native reads
remain the recovery path. There is no new replay authority or implicit replan.

Reuse existing encrypted plan storage without a new namespace or format version.
N-1 bridges retain their known tools and stored plans; the two tools and optional
history review hints are additive. No native endpoint, migration or SDK change
is needed, and existing limits remain unchanged.

## Consequences

Catalog becomes 55 read/planning tools and 64 with existing writes enabled.
Tests cover both protocols, original image/source/rollout/budget/environment
reviews, stale baselines, integer precision, credential/configuration redaction,
immutable snapshots, encrypted restart, expiry and consent/identity races.
Pinned real Codex acceptance reopens the same source, worker, budget and
environment proposals across restart and OAuth refresh with zero model requests
and native writes in read-only fixtures. Native/provider deployment
qualification remains separate.
