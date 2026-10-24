# ADR-20261024160000: Management MCP saved-plan recovery resources

## Status

Accepted — 2026-10-24. Extends ADR-20261024140000.

## Context

Saved-plan recovery prompts provide a guarded recipe, but the pinned Codex
app-server API exposes resource reads rather than prompt-get. Clients need the
same structured read-only workflow through the MCP resource surface without
new tool names, replay authority or native reads beyond access verification.

## Decision

Add three JSON resource templates:

- `gregale://default/apps/{app}/plans/{plan_id}/review`
- `gregale://projects/{project}/environments/{environment}/apps/{app}/plans/{plan_id}/review`
- `gregale://projects/{project}/environments/{environment}/plans/{plan_id}/review`

Require canonical URIs, validated exact original IDs and native slug selectors.
Reject credentials, query/fragment suffixes, encoded or duplicate path segments,
unknown views and noncanonical spellings. Reuse the prompt selector validator,
fixed guidance, workflow builder and saved-plan guards. Both prompt and resource
recipes contain the same exact history/review/check tool calls and selectors;
no saved proposal prose, native DTOs, receipts, configuration values, API URLs,
preview/approval material or retry keys enter resource contents.

Return `review_kind: saved_plan_recovery_recipe`, `freshness: not_checked`,
`native_completion: not_observed`, fixed `guidance` and a structured `workflow`.
Recipe availability proves current read access and original unexpired plan
presence only. Identity verification is permitted; current baseline/receipt/
completion reads, source resolution, probes, planning, mutations, approval
polling/grants, persistence and TTL renewal are excluded. Ambiguous attempts keep
the original retry, and rollback coverage remains partial as in the prompts.

Release data only after final immutable account/API/app/environment, consent,
diagnostic consent, storage, plan-pointer and expiry checks. Environment plans
require both source/target environments and every project workload, checking the
target after the source. Other grants cannot read a known original plan URI.
Apply the existing sanitizer and resource-frame bound before returning JSON.
Plan-ID completion is non-enumerating; IDs come from consented history. Other
selector completion reuses existing consent filtering. Recipes are snapshots,
not subscriptions. Cached recipes grant no authority; reread after reconnecting
and use existing guarded tools for every subsequent operation.

This is additive: no native endpoint, migration, SDK or encrypted format change.
Catalog stays 57 read/planning tools, 66 with writes enabled and eight prompts;
resource templates increase from eight to eleven.

## Consequences

SDK tests cover all URI forms on both protocols, prompt/resource workflow parity,
canonical parsing, passive reads, completion, encrypted
restart, ambiguous outcomes, expiry, final consent/storage/presence checks, HTTP
grant isolation and revocation. Pinned real Codex 0.159.0-alpha.3 reads source,
worker, budget and environment-promotion recipes and follows their saved-review
calls across restart/OAuth refresh in all three read-only fixture profiles;
post-revocation reads expose no contents. Those fixtures make zero model requests
and native writes. Named app templates are covered by native SDK clients.
Portable fixtures do not qualify native/provider deployments.
