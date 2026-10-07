# ADR-643: read one customer Operation workflow instance

Status: accepted for the internal HTTP implementation.

## Context

ADR-642 gives each observed workflow mapping a stable app-provided instance ID.
Clients can currently list every retained milestone for a business reference
and group those facts themselves.

## Decision

The account and customer business-milestone feeds accept paired `workflow`
and `workflow_instance_id` selectors. The existing app, environment, customer,
and business-reference boundaries remain required. The server filters against
each milestone's pinned workflow step and extracts the ID using its pinned
JSON Pointer before applying the feed limit.

Both selectors are included in the opaque cursor's query identity. Results
contain only facts for the selected workflow instance, with matching pinned
step metadata. The Go, Node, and Python clients expose the selectors through
their business-milestone APIs.

## Consequences

Applications can load one workflow run with a bounded, paginated read. Facts
remain ordered by first platform publication time. Missing steps remain
unknown; this read does not infer business state or execute application logic.
