# ADR-20261024120000: Management MCP advisory plan applicability

## Status

Accepted — 2026-10-24. Extends ADR-20261023184309.

## Context

Reopening a saved proposal deliberately does not check applicability. Agents need
to observe changed release, configuration, policy or regression baselines before
attempting a mutation, without turning a read into approval or execution.

## Decision

Add `gregale_check_plan` and `gregale_check_environment_plan`, using the same
exact target selectors and original connection-local plan IDs as saved review.
Reuse its identity, consent, storage, plan-presence and expiry guards, including
final source/target and diagnostic-consent checks. Missing, expired or inaccessible
plans return existing structured tool errors, never private proposals; checks do
not prune them. No new endpoint, migration, storage format or SDK is needed.

Return only plan ID, original expiry, observation time, baseline coverage, stable
blocker codes and advisory instructions. Do not return upstream error bodies,
plan payloads, approval credentials, replay keys or configuration values.
`apply_eligibility` is `no_observed_blockers`, `blocked`, `unknown`,
`retry_original_plan` or `receipt_recorded`. None grants apply authority or
promises a successful operation. Checks concern the original apply phase, not
subsequent request traffic promotion. Every native mutation guard remains unchanged.

For a first attempt, observe current write enablement and target write consent;
compare release/configuration baselines, both budget policy revisions, or the
selected regression observation version and absolute dismissal expiry. Regression
checks require current diagnostic consent and report missing triage write consent.
Environment promotion checks compare the exact fresh native preview and report
required approval as `unknown` / `approval_not_checked`, even when the bridge
holds a private credential: a read never validates, starts or polls approval.
Environment rollback checks only its retained promotion status/hash and explicitly
report `partial_native_rollback_guard_required`; the native transaction owns
captured target-release validation. Native read failure or invalid response is
unknown, never evidence of eligibility. Observations are snapshots, not atomic
reservations; target retention, admission, qualification and all other native
transaction gates remain authoritative.

An attempted plan may already have committed. Skip baseline comparisons for
attempted/receipted plans and retain original-retry guidance. State changes caused
by an unknown successful operation must not lead to a replacement request. Current
consent, write enablement and original expiry still constrain guarded retries.
A recorded receipt does not establish native completion or healthy traffic.

Checks perform passive reads only: no source resolution, probes, writes, storage
updates, new plans, TTL renewal, approval actions or mutation calls. Existing
saved reviews continue to return `apply_eligibility: not_checked`.

## Consequences

Catalog becomes 57 read/planning tools and 66 with existing writes enabled.
Tests cover both protocols, drift, native uncertainty, consent/expiry changes,
unchanged encrypted state, restart and unknown successful mutations followed by
an original idempotent retry. Pinned Codex acceptance checks original saved plans
across restart/refresh without model requests or native writes. Native/provider
deployment qualification remains separate from portable fixture evidence.
