# ADR: Publish typed application toolkits through the existing MCP starters

Status: Accepted (SDK preview)

## Context

Developers can assemble typed queries, resources, durable pipelines, and
approved external actions. Each adapter has its own descriptors and dispatch,
so putting them behind one MCP endpoint requires custom registration and
permission routing.

## Decision

Add matching Node and Python toolkit registries. A mount publishes selected
adapter operations under explicit OAuth scope rules. Discovery and direct calls
use the same rules, and resource links use the same template policy as resource
reads. Reject ambiguous, unknown, and duplicate registrations.

The Node and Python starters accept a toolkit at construction or startup and
merge its scope rules into their existing authenticated catalog policies. The
verified OAuth subject and scopes come from the starter. The app can map that
subject to its own principal. Existing adapters continue to authorize business
access per call and own checkpoints, approvals, and results.

Use the existing Streamable HTTP endpoints, OAuth implementations, and catalog
fingerprinting. The toolkit adds application SDK and starter code without a
platform API or platform state migration.

## Evidence

Shared SDK fixtures pin discovery, dispatch, and resource scope behavior. The
paid CSV test mounts query, compensation, and refund adapters in both starters.
It exercises signed two-user requests, direct denial, current ownership,
cross-language cursor and receipt continuation, and browser approval with
real PostgreSQL and protocol fixtures.
