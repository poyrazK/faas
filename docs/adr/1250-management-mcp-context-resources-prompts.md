# ADR-1250: Consent-scoped management MCP resources and prompts

## Status

Accepted. Extends ADR-1249.

## Decision

Advertise canonical read-only Gregale resources for connection context, bounded
inventory, release reviews, passive health and scoped operational budget findings.
Use existing customer API reads, safe projections, credential redaction, response
bounds and pre/post-read consent checks. Generic templates never enumerate
unconsented app or environment names. Do not introduce resource writes,
subscriptions, arbitrary URI fetching, background polling or a new intent writer.

Provide deployment review, rollout diagnosis and rollback review prompts with
validated app/environment selectors and exact deployment ownership when needed.
Return trusted review recipes and resource URIs, not embedded logs or secret
values. Prompt retrieval creates no plans and invokes no mutations or approval.
Existing guarded tools, workload semantics and human approval gates remain
required for subsequent authorized changes.

## Consequences

Clients can discover structured context independently of tools. Inventory stays
bounded; larger views require paginated tools. Errors use sanitized stable codes
rather than raw upstream failures. Prompt retrieval support differs by client;
standard MCP SDK coverage verifies the protocol even where Codex's app-server
interface exposes resources but no prompt-get method.
