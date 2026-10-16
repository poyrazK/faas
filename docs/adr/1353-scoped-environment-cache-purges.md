# ADR-1353: Scoped environment cache purges with replay generations

Status: Accepted

## Context

The platform already has app-wide physical response-cache purges. Management
MCP needs exact named-environment authority, retry-safe requests and honest
fleet completion. Invalidation by scanning/deleting shared entries alone can
be undone by an origin fetch that began before purge and writes afterward.

## Decision

Add a separate owner-keyed durable environment purge ledger. Targets are one
immutable environment host and either an exact decoded URL path or the whole
host (explicit empty path). No wildcard, query, fragment, percent escape,
control or dot-segment targets. Every request carries immutable app/project/
environment IDs and an account-scoped retry UUID. The row itself is both
command and stable receipt; repeated identical requests allocate no generation.
Serialize generation allocation with a dedicated transaction advisory lock
before identity allocation, so committed global IDs are safe replay cursors.
Use sqlc for new production queries and preserve apid intent ownership.

Gateways replay retained commands in pages of 100, alongside the existing
five-second environment-policy observer using its fenced current-process SID.
Each process starts at cursor zero, bypassing named-environment response-cache
reads and writes until caught up. Apply commands to monotonic per-app/environment
whole-host and exact-path generation maps; publish the cursor only after the
page is applied and the current frontier is caught. Replay and acknowledgement
retries are idempotent. Late/outdated sessions cannot acknowledge a new process.

Include immutable environment identity and whole/path generations in response
cache keys. Keep non-environment key encoding unchanged. The local cache drops
matching entries when a generation advances. Shared Redis entries in prior
namespaces age out by bounded TTL and are never consulted by caught-up gateways.
In-flight writes use the earlier captured key, so they cannot repopulate the
new namespace. New processes reconstruct the same tokens before enabling cache.
This is logical invalidation, not a physical Redis deletion API. Other hosts,
environments and cache policy remain unchanged. Deployment-preview bypass and
existing auth/cacheability gates remain in force.

Expose native create/status routes and MCP plan/apply/status tools. Plans bind
exact target and retry UUID for 15 minutes, require environment read consent,
and apply additionally requires current environment write consent. Encrypt and
validate durable plan/receipt bindings across restart. Cached receipts still
require current consent and identity. Purges do not change policy revisions.

Status uses the authoritative registered serving compute roster, exact current
session/endpoint and node/session heartbeats within 90 seconds. A current-session
checkpoint at or beyond the command generation counts as acknowledged. Zero
fleet or stale/missing evidence is degraded; fresh incomplete replay is pending;
only all acknowledged is completed. Return aggregate counts without node names
or addresses, recheck registry identity, and scope reads to exact ownership.

## Consequences

Agents can invalidate application-data responses with scoped authority and
verify sampled fleet propagation. Upgrade the database, apid and every serving
gateway before enabling purges; old gateways do not honor the namespace. Use
FAAS_NODE_NAME registration; unnamed processes bypass named-environment caching.
Commands/receipts and distinct-path maps grow with retained purge history;
startup replay may keep caching bypassed for a large or failing backlog. Retain
commands until owner deletion so restarts can reconstruct namespaces. Runtime
checkpoints are observation metadata, not customer intent. No response-drain,
unregistered-process, continuous-attestation or origin-freshness guarantee is
implied. Existing app-wide physical purges remain independent.
