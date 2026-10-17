# ADR-1415 · Acceptance-guarded blocker explanations for deadline investigations

Date: 2026-10-16
Status: Accepted

## Context

Go/CLI deadline investigation identifies current urgent recipients but returns only
their observed reason and scheduling metadata. The existing exact-recipient batch
endpoint can independently explain routing waits, guarded against reused event
identities, without recovery actions.

## Decision

Add opt-in `Explain` to Go investigation and CLI `--explain`. Require a normalized
page limit no greater than the existing 50-query batch limit before HTTP. After
validating one triage page, make at most one read-only blocker batch using exact
source/ID/subscription/acceptance guards. Share the existing five-second context
budget across both requests and preserve normal whole-request error propagation.

Keep one explanation item per page row. Respect the existing identity and encoded
body byte bounds, marking unsupported/unread rows explicitly rather than dropping
them or adding requests. Validate ordered query/result echoes, application scope,
acceptance, clocks and routing/ordering metadata. Project only known payload-free
fields. Keep per-item read errors independent and reject replacement evidence.

Label coverage as the selected triage page. Read completeness measures valid
scoped responses, including unavailable evidence, rather than usable blocker data
or delivery success. Preserve independent diagnostic timestamps and live state
changes without reconstructing alert membership or treating the triage window as
a diagnostic snapshot. Ordering lanes can span subscriptions; preserve the actual
blocker's subscription identity without following the chain.

CLI emits the result but exits 1 on incomplete diagnostic reads. Default behavior,
including request count and JSON shape, remains unchanged without explanation.
Reuse endpoint authorization and add no server route, schema, migration or mutation.
Expose the same types and implementation in the standalone Go SDK. Other SDK
investigation interfaces remain separate.

## Consequences

Operators can inspect current blocker details directly from a saved deadline alert.
Changed routing, retention loss, replaced acceptances and read failures remain
distinct observations. A complete read can still contain unavailable evidence,
and settled routing cannot certify handler completion or business success.

See [usage and result semantics](../event-deadline-alert-investigation.md#blocker-explanations).
