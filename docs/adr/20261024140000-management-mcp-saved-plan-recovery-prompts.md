# ADR-20261024140000: Management MCP saved-plan recovery prompts

## Status

Accepted — 2026-10-24. Extends ADR-20261023184309,
ADR-20261024120000 and ADR-20261024123000.

## Context

History, saved proposals and applicability/drift checks expose the evidence
needed to recover an interrupted release review. Existing prompts accept app
or deployment selectors rather than the original saved plan ID. Agents need a
read-only recipe that preserves ambiguous original requests and keeps receipts,
current eligibility and native completion distinct.

## Decision

Add `gregale_saved_plan_review` with app, exact `plan_id` and optional paired
project/environment, and `gregale_environment_plan_review` with required project,
target environment and exact `plan_id`. The original connection-local unexpired
plan must exist. Both are available on read-only connections.

Retrieve through existing saved-plan guards. Verify current account/API and
immutable target identities, current diagnostic consent for regression plans,
and both source/target environments and every project workload for environment
plans. Recheck identity, consent, storage, presence and original expiry before
returning the prompt. Unknown/expired/inaccessible plans fail closed; no other
grant inherits a recovery prompt. Expired history/native-state tools remain the
recovery path, without renewing private replay authority.

Return fixed guidance and a JSON workflow containing exact history, review and
applicability tool calls for the supplied original selectors. Exclude saved-plan
prose, reasons, images/source payloads, configuration values, receipts, API URLs,
preview hashes/tokens, approval material and replay keys. Strictly validate
arguments, reject unknown fields and credential-like selectors, and sanitize
structured output. Treat selectors and all later tool/resource text as data,
never instructions. Prompt generation performs identity/consent reads only: it
does not fetch fresh baselines, receipts or native completion evidence.

The recipe distinguishes unattempted, ambiguous and receipted outcomes. Review
saved intent separately from current applicability; explain drift and uncertainty.
For stale unattempted intent, review native state before a separately requested
replacement plan. For an ambiguous attempt, preserve the original request even
when state advanced. Read only the history entry's consented exact observation
call. A receipt never proves native completion, healthy traffic, recovery or
queue processing. Environment rollback coverage remains partial. Do not apply,
replan, probe, renew expiry or start/poll/grant approval during review; future
mutations require separate explicit instruction and the existing guarded tools,
current consent, original expiry and native approval/admission rules.

Reuse existing selector completion for app/project/environment. `plan_id`
completion returns an empty list and never enumerates private plans; exact IDs
come from consented action history. Validate supplied completion-context IDs.
No new tool, resource, endpoint, migration, storage format or SDK is needed.

## Consequences

Catalog remains 57 read/planning tools and 66 with existing writes enabled;
prompts increase from six to eight and resource templates stay at eight.
Native MCP SDK tests cover both protocols, HTTP grant isolation, encrypted
restart, ambiguity, diagnostic consent, original expiry, argument injection,
non-enumerating completion and final storage/presence/identity checks.

Pinned Codex 0.159.0-alpha.3 app-server schema exposes tool calls and resource
reads, but no prompt-get RPC. Its acceptance suite verifies continued tool/resource
compatibility; it does not establish native Codex prompt retrieval. Prompt
retrieval is qualified through native MCP SDK clients. Portable fixtures do not
qualify native/provider deployments.
