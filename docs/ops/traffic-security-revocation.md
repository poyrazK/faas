# HTTP security revocation

ADR-375 separates a request's immutable traffic policy from emergency security
state. Postgres `traffic_security_epochs` records account suspension/deletion
pending and abuse holds, app deletion, and deployment security quarantine.
Every revoke and release advances a generation. Physical deletion retains a
tombstone without a cascading foreign key. Plan, ordinary configuration,
traffic weights and nonsecurity parking changes do not advance generations.

## Gateway behavior

Production internal gateways verify the table with a bounded read before
serving. Public HTTP enrolls its owner account and app before authentication,
upload and wake, then its selected deployment before dispatch. A retry checks
each additional deployment. Service calls enroll the verified caller app and,
when available, its source deployment before discovery; account and target app
follow tenant authorization, before wake. Each selected target deployment is
checked before forwarding. Identity comes from routing, tenant authorization
and node source-instance resolution, never an arbitrary scope header.

An independent lifetime fence survives successful streaming/gRPC/Upgrade
handshake detachment. Changing any enrolled generation cancels the exchange,
including a revoke/release pair missed between reads. Store failure or
inconsistent/regressed generations also cancel active tracked exchanges and
refuse new admission. A warm policy snapshot supplies no security allow lease.
Forwarding ownership and node permits remain until forwarding cleanup finishes.
Final handler cleanup unregisters the scopes after stopping response-write
guards, avoiding a late cancellation of the HTTP server's buffered flush.

`traffic_security_changed` is a wake-up to re-read authoritative rows, and
refreshes advisory route/app flags. Its JSON identifies scope kind, UUID and
generation; it is never treated as an allow/revoke instruction. Each registry
also reads active scopes once a second, with a 250 ms store-operation timeout,
so missed notifications do not leave an old exchange alive indefinitely.

Limits are in `pkg/api/limits.go`: 65,536 simultaneous registry registrations,
4,096 distinct active security scopes and 16 distinct scopes per request across
all attempts. Account/app and later deployment enrollment may use separate
registrations, so the registration bound is conservative for logical requests.
Capacity refusal includes the actual limit, observed count and documentation
URL; it does not silently skip tracking.

## Responses and recovery

Before headers, emergency cancellation returns `traffic_revoked`/403. Known
initial account holds and suspension preserve their existing problem contracts.
Verification failure returns `traffic_revocation_unavailable`/503 and
`Retry-After: 1`. Error delivery has a separate bounded 100 ms write allowance.
After headers, the forwarding transport aborts instead of appending a problem
document or completing a partial success body. HTTP/1 and HTTP/2 write guards
also interrupt a blocked compute-side response writer.

A released generation allows a fresh request. Cached old account hold/status
flags cannot override a verified release. Existing app security-quarantine
admission still refuses before wake, and notifications refresh its route facts.
Deleted identity tombstones and released generations survive gateway replacement.
Individual credentials, managed realtime, detached work and arbitrary guest
sockets remain outside this initial account/app/deployment fence.

## Rollout and local evidence

Apply `20260929230709001_traffic_security_epochs.sql` before rolling internal
gateways. A missing migration or unreadable store refuses new gateway startup.
Update every participating internal gateway and both its public-request and
service-proxy listeners before advertising fleet coverage. The public proxy
response/session metadata rollout remains compute-first, as described in
`traffic-total-deadline.md`. Older gateways do not provide this lifetime fence.

Local tests cover account/app/deployment cancellation, initial refusal, upload
spool removal, wake cancellation with real HTTP/1 and HTTP/2 error delivery,
public and service retry-deployment checks, scope capacity, cleanup ownership,
and real gRPC cancellation after successful handshake-budget detachment.
Blocked ordinary and streaming compute response writes use a 64 MiB body and
an open unread client over HTTP/1 and HTTP/2; both handlers and upstream RPCs
must finish before the test closes the client.

Two real HTTP service gateways against Postgres, with notifications deliberately
absent, canceled a missed hold/release pair through periodic repair. Closing
only one gateway's database pool canceled that gateway and refused its new
traffic while the healthy peer continued. A replacement verified the durable
released generation. These are gateway instances in one test process; source
identity and forwarding ownership are fixtures. The separate gateway tests use
real gRPC transports. Full daemon, all public-hop long-stream behavior, native
VM/network/leak, deployment and complete policy-path acceptance remain pending.
