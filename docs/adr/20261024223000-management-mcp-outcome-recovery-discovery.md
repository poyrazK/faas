# ADR-20261024223000: Discover retained route-budget outcome recovery

## Status

Accepted — 2026-10-24. Extends ADR-20261024180000 and ADR-20261024210000.

## Context

The native receipt reader can resolve retained route-budget attempts after their
original plan expires. History previously advertised recovery resources and
prompts only while the mutation plan remained available. Agents need exact
read-only calls for the retained attempt without reconstructing expired intent.

## Decision

For attempted route-budget history with a valid retained encrypted lookup bound
to the original immutable action, add `recovery.outcome` with tool
`gregale_get_action_outcome` and exact original app/project/environment/plan ID
arguments. Keep the existing resource URI and prompt reference discoverable
independently of mutation-plan availability. An available live plan retains its
existing review/check links; an unavailable plan has no review, check or retry
call. Other action kinds and attempts without an eligible lookup keep their
existing recovery behavior.

Extend the existing app saved-plan prompt and default/named app resource recipes.
A live recipe can add outcome and current-policy observation calls. An expired
eligible recipe contains only history, outcome and observation calls; omit
review/check fields rather than supplying empty or misleading calls. Environment
promotion/rollback recipes continue to require their original live plans.
Reuse existing canonical selector validation and resource addresses. No new
catalog entries, templates, quotas, native API endpoints, SDKs, encrypted formats
or migrations are needed.

Recipe retrieval verifies current storage/read consent, account and immutable
targets, retained history, and the exact private lookup reference again before
release. It fetches neither receipts nor policy baselines. History final guards
remove unavailable outcome hints and never upgrade a snapshot with newly
appearing references or plan availability. Clone ephemeral links before changing
hints; preserve original phases, receipts, pagination and ordering. Discovery and
following read-only calls never change durable history, expiry or native policy.

Guidance distinguishes a verified historical receipt from current policy and
runtime health, and follows the original outcome call with the exact existing
`gregale_get_route_budgets` observation. Missing evidence stays unresolved and
cannot prove non-commit. Cached recipes, expired plans and returned selectors
grant no write/retry/approval authority. Never replace ambiguous original requests
or expose private UUIDs, hashes, payloads, reason text or configuration in recipes.

## Consequences

SDK tests follow list/detail discovery through resources, prompts, receipt lookup
and current-policy observation on both MCP protocols and default/named scopes.
Encrypted restart discards expired mutation bodies while retaining only the
existing bounded lookup and history. Tests distinguish receipt revision 1 from
later current policy revision 2 and prove retrieval performs no receipt reads,
mutations or state writes. Final guards cover missing/changed references,
history removal/expiry/binding, storage, consent and recreated environments;
references appearing mid-read do not upgrade the snapshot.

Pinned real Codex follows expired discovery and resources across restart/refresh,
explicitly reads a synthetic native receipt and then later current policy, and
loses resource access after revocation in all three read-only profiles. Fixture
controls seed only bridge-local history and synthetic GET metadata; zero model
requests/native writes do not establish native/provider deployment qualification.
