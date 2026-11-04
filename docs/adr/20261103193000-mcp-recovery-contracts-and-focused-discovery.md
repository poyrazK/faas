# ADR-20261103193000: MCP recovery contracts and focused discovery

Status: accepted

Extends [ADR-20261103155008](20261103155008-management-mcp-agent-workflows.md)
and [ADR-20261103171917](20261103171917-atomic-mcp-config-and-durable-provisioning.md).

## Context

Atomic native writes and durable provisioning preserve outcomes, but agents
still need to distinguish a refused operation from a lost response, follow the
correct recovery action and avoid loading every optional workflow into context.
Optional-only output envelopes could accept an empty result, and an attempted
configuration write was reported as unknown even after a known refusal.

## Decision

Successful plan, apply and core status responses require their stable identity
fields. Plan receipts and tool errors use recovery contract version 1. Statuses
and recovery actions are closed enums. Native diagnostic evidence remains
additive; it is not reduced to an invented fixed schema. Tool output schemas
describe both success and error variants. The bridge validates its successful
envelopes without converting exact JSON integers through floating point.

Errors and receipts provide `recovery.action`: `none`, `retry_read`,
`retry_same_plan`, `replan`, `inspect` or `request_access`. Where a safe next call
is known, `tool` and `arguments` identify it. Arguments contain only value-free
selectors. `reuse_original_inputs` refers to the caller's retained input; the
bridge never echoes configuration values. Calls are derived from known codes,
tracked progress and the connection's listed tools, never native error prose.
Missing catalog groups are reported as toolsets to select; a catalog choice
does not grant native API scopes or environment access.

Tracked single-step operations record a known initial native rejection as
`refused`; the old plan remains terminal and requires a fresh review. An earlier
uncertain attempt remains `outcome_unknown`, including when a later retry is
denied. Unclassified or potentially multi-step failures retain uncertainty.
The native configuration receipt is journaled before an optional restart.
When a replay confirms a previously uncertain commit, the checkpoint replaces
the older uncertainty before persistence.
`partial` therefore survives a crash between configuration commit and restart
or final receipt storage. Retrying preserves the original request ID and body.
Confirmed steps are not repeated. Configuration baseline initialization may
record metadata while refusing requested variable changes, as in the prior ADR.

Cancellation wins over automatic resumption when an interrupted journal is
restored. Current authorization is checked again before any explicit resume.
Recovery guidance does not imply rollback and does not delete created resources.

The default catalog is `core`. `all` is an explicit selection rather than the
same nil value used for omission. CLI defaults, standalone bridge defaults and
browser consent agree. A browser connection can narrow an operator's ceiling;
explicit `all` means all groups inside that ceiling. An omitted selection
chooses core when available, otherwise the operator's already focused ceiling.
An explicitly empty intersection stays empty after persistence. Legacy omitted
selections adopt the focused default. Clients needing advanced groups opt in
when connecting. Both browser sign-in and API-key onboarding retain selections
across bridge restarts.

## Validation

`make test-mcp-agent-workflows` runs deterministic MCP-client workflows through
the public protocol. A separate JSON Schema implementation validates published
schemas. Cases cover catalog selection, refused-write replanning, lost-response
recovery, configuration/restart partial completion, crash checkpoints,
cancellation and revoked provisioning access. Call budgets and mutation counts
bound recovery loops and detect duplicate effects. Catalog byte budgets cover
both local and hosted tool sets. These evaluations do not measure an LLM's
reasoning performance and do not replace the deployed journey.

The app-agent deployed journey checks focused discovery, a multi-key batch,
repeat receipts, explicit refusal and the recovery contract. Its execution
requires a configured target and credentials. No native REST DTO or database
migration changes are needed for this slice.
