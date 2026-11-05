# ADR-20261024123000: Management MCP plan drift details

## Status

Accepted — 2026-10-24. Extends ADR-20261024120000.

## Context

Applicability checks identify a stale original proposal but broad blocker codes
do not explain which baseline changed. Agents need a safe comparison to describe
what requires review without reconstituting native configuration or replacing a
possibly committed original request.

## Decision

Extend the existing `gregale_check_plan` and `gregale_check_environment_plan`
results with an additive `drift` array. Each entry has a fixed `field` and,
where safe, primitive `expected` and `observed` metadata values. Configuration,
identity mismatch and promotion-preview comparisons instead return only
`values_omitted: true`. The list has a fixed finite field set; no variable native
DTO, workload/configuration diff or upstream body is serialized. Zero revisions,
empty release IDs and false flags remain explicit comparison values.

First-attempt release checks compare normalized app settings, native configuration
baseline, serving deployment and latest deployment using the same passive capture
as the existing baseline guard. Scope changes disclose only an identity marker;
malformed deployment IDs leave applicability unknown rather than echoing invalid
metadata. Native apply code and its guards are unchanged. Budget checks separate
app policy and environment policy revision changes. Regression checks compare the
validated observation UUID for the exact selected deployment/route. Invalid or
unavailable evidence produces no invented expected/current pair.

Environment promotion checks distinguish an opaque preview-baseline change,
current promotion eligibility and config-sync flags. Rollback checks distinguish
opaque promotion-baseline changes, a running promotion and an existing rollback.
They retain partial native rollback coverage; comparisons do not claim captured
target releases still satisfy the native transaction. No preview hashes/tokens,
approval material, configuration values, SQL, routes or arbitrary status text
enter drift output. Scoped release IDs and observation versions are metadata,
not instructions, and pass through existing output sanitization.

Detected baseline differences set `baseline_check: changed` and retain the
existing blocker/eligibility outcome. Unknown or inconsistent reads retain
`not_verified` without fabricated drift; `drift: []` alone never establishes
freshness. Required approval remains unknown and unpolled. Attempted/receipted
plans skip all drift comparisons and return `retry_preserved` with an empty
array. Their native operation may already have committed, so state advanced by
that operation must not trigger a replacement request.

Reuse final saved-plan guards for identity, consent, diagnostic consent, expiry,
plan presence and storage. Compute independent primitive projections while
holding the management lock. Reads do not persist comparisons, update baselines,
renew expiry, resolve source refs, start approvals, probe workloads, or mutate
native state. No migration, SDK/native endpoint, new tool or limit is needed.

## Consequences

Catalog stays at 57 read/planning tools and 66 with existing writes enabled.
Tests cover both protocols, individual and combined drift, opaque-value exclusion,
zero/boundary policy revisions, unknown/invalid native evidence, encrypted restart,
revoked consent and ambiguity-preserving retries. Pinned Codex checks safe drift
on the same original source plan across restart and OAuth refresh and then reopens
the unchanged original proposal. Portable fixtures do not qualify native/provider
deployments.
