# ADR-20261024180000: Management MCP history recovery discovery

## Status

Accepted — 2026-10-24. Extends ADR-20261024140000 and ADR-20261024160000.

## Context

History exposes original plan IDs and saved-review calls, while recovery
resources, prompts and applicability checks require separate discovery. Agents
should recover from server-provided links bound to the original action rather
than constructing resource addresses and argument sets.

## Decision

Add optional `recovery` metadata to app and environment action list/detail rows:

- `resource_uri`: the exact canonical recovery resource URI;
- `prompt`: the saved-plan prompt `name` and exact string `arguments`;
- `check`: the applicability tool and exact original target/plan arguments.

Preserve the existing `review` call for older clients. Default/named app rows
and environment promotion/rollback rows use their existing resource and prompt
families. Omit new recovery links when the shared prompt/resource validator cannot
render the original selectors exactly, including credential-like identifiers.
Links contain fixed names and validated saved selectors only, with no
proposal prose, credentials, configuration/preview values or replay payloads.

Only advertise a currently available original plan. Match plan ID/action,
account/API and all immutable target/scope bindings; environment plans also
match both original environments. Read-only connections receive read-only
recovery hints independently of write enablement. A hint grants no authority:
resource/prompt/check calls still enforce current consent, original expiry and
native guards. Applicability checks concern the original apply phase, not a
subsequent request traffic promotion. Recorded apply/promotion uncertainty stays
separate, and no hint triggers a mutation or new plan.

After final history identity/consent checks, recheck storage availability and
plan availability under the management lock. If a previously available plan
expired, disappeared or lost its binding during native reads, return retained
history with unavailable-plan guidance and omit review/recovery/retry hints.
Keep recorded phases, receipts, ordering, pagination and observation metadata
from the original snapshot. Do not upgrade an initially unavailable snapshot
when a plan appears later. Clone page rows before changing ephemeral hints;
never persist availability changes, renew expiry or refresh native baselines.
Storage or consent failure withholds the result as before.

This is additive response metadata only. No native endpoint, migration, encrypted
format, SDK, tool, prompt, resource-template or limit changes are needed. Catalog
stays 57 read/planning tools, 66 with writes enabled, eight prompts and eleven
resource templates.

## Consequences

SDK tests follow list/detail links through resource reads, prompt retrieval and
applicability calls on both protocols, for default/named app and environment
promotion/rollback plans. Race tests cover expiry/removal/binding changes during
history reads, retained ambiguity, no state writes, encrypted restart, read-only
connections and no availability upgrades. Existing consent and redaction gates
remain applicable.

Pinned real Codex reads history and follows returned resource URIs/check calls
across restart and OAuth refresh, verifying stable original selectors. Its
app-server API still lacks prompt-get; native SDK tests qualify following prompt
links. Read-only fixtures make zero model requests and native writes. Portable
fixtures do not qualify native/provider deployments.
