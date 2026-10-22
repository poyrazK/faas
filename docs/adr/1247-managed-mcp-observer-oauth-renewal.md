# ADR-1247: OAuth renewal for managed MCP catalog observers

## Context

Managed role observers load sealed bearer secrets on each check, but customer
rotation is required when those tokens expire. The control plane has OAuth
provider metadata validation; it does not have an observer refresh connection.

## Decision

Each managed role may opt into an explicit HTTPS token endpoint, client ID,
initial refresh-token app secret reference and optional client-secret reference.
The initial grant is obtained outside Gregale. This change does not introduce
browser authorization, discovery-directed token requests or dynamic registration.
Static bearer and OAuth configurations are mutually exclusive. Credential binding
requires the existing deploy intent permission plus secrets:read or admin.

The apid domain worker uses its existing role revision and 45-second durable
lease to serialize renewal. It refreshes before expiry with a 60-second margin,
using the guarded public egress transport with proxies and redirects disabled.
Responses are bounded to 64 KiB, tokens to 16 KiB and lifetimes to one year;
lifetimes of 60 seconds or less cannot meet this renewal margin and are refused.
Public clients use client_id; confidential clients use client_secret_basic.

Access and rotated refresh tokens are sealed under the current host age key in
the owning role's monitor payload. Previous host keys can decrypt during key
rotation. They are never written to guest environment secrets. Renewal commits
before probing under the live role revision/lease fence. Public projections copy
connection metadata and remove ciphertext and source fingerprint. Provider error
bodies and credential values never enter receipts or logs.

An invalid_grant response marks the connection reauthorization_required and
suppresses further grant attempts until the customer rotates the initial refresh
secret. Other errors use the existing unavailable state and bounded retry backoff.
The referenced refresh secret is checked on every use: deletion fails closed and
ciphertext rotation resets the connection. Client-secret rotation uses its current
value without discarding the already rotated refresh token. Connections are
independent per role; each role needs a distinct provider refresh grant.

Pause, resume, restart, stop and baseline review retire old leases and catalog
runtime evidence, but preserve sealed OAuth credentials for an identical OAuth
configuration. Removing or changing that configuration removes its retained
credentials. A newly changed provider/client binding requires a fresh initial
grant. An append-only migration constrains config and private state; rollback
refuses while OAuth configuration remains.

## Consequences

Normal expiry no longer requires manual bearer-token updates. Connection status
reports expiry, last successful renewal and reauthorization needs. The worker
continues to use normal gateway routing and billing, without changing VM owners.

A provider refresh exchange and a PostgreSQL commit cannot be atomic. If a
provider rotates a single-use grant and the worker crashes, loses its lease or
cannot persist the response, reauthorization may be required. Lease fencing
prevents stale writes; it cannot undo provider-side token issuance. Scheduling is
best effort, and provider outages can make a role unavailable. Public metadata
checks remain separate from real token issuance and authorization evidence.
