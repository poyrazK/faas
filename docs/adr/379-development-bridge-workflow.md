# ADR-379 · Complete the local development bridge workflow

- **Status:** accepted for the operator-gated HTTP capability
- **Date:** 2026-09-30
- **Decision:** Extend ADR-378 with supervised local execution, account-scoped
  session inventory and activity, framework propagation helpers, a dashboard
  view, and a native split-box acceptance harness.

The CLI may launch an explicit command after creating loopback dependency
proxies. It supplies dependency URLs through named environment variables, checks
local readiness, forwards signals, preserves the child exit status and revokes
the lease on exit. Credentials remain inside the bridge; child configuration
contains loopback URLs and session identifiers, never attachment authority.
Existing attach-to-an-IDE usage remains supported.

Node and Python helpers capture request authority in task-local contexts and
strip bridge credentials on all outbound requests. Propagation is limited to
single-label Gregale discovery names. Each redirect boundary must re-evaluate
the destination. Node scoped fetch returns redirects for explicit handling;
HTTPX transports enforce the same policy on each redirect hop.

apid continues to own durable sessions. Inventory reads are account-scoped and
bounded. apid observes authenticated laptop upgrades and scoped HTTP proxy
requests in bounded memory, with generation-fenced disconnect cleanup. This
single-control-plane observation is informational, never routing authority.
Restart clears the activity window; expiry/revocation overrides connection
status. No headers, query strings or bodies appear in inspection. The relay
retains its read-only database contract.

The dashboard uses the same inventory and activity projection as the CLI, with
normal session authentication and action-specific CSRF protection for revocation.
Native acceptance must traverse the deployed public edge and real workload
service identity, using an explicitly designated development fixture. Ordinary
traffic, two developer sessions, production caller denial, reconnect and lease
revocation are required assertions. A compiled harness or an HTTP fixture does
not constitute native acceptance evidence.

HTTP scope, existing leases, credentials, VM ownership and the default-disabled
rollout gate remain governed by ADR-378. WebSocket/gRPC forwarding and multiple
local targets require separate transport and routing decisions.
