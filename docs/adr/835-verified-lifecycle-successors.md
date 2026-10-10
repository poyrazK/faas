# ADR-835: Verified lifecycle successors across applications and custom domains

Status: Accepted

## Context

A lifecycle successor receipt previously compared only operations on the source
application's canonical HTTPS hostname. Moving an operation to another owned
application or a verified custom domain needs an authoritative destination,
not an arbitrary URL or a fetched external document.

## Decision

Mappings may additionally pin `successor_app_id`, `successor_deployment_id`, and
`successor_contract_sha256`. Supply all three together, using the authoritative
capture metadata hash. Omitting them preserves the source candidate default.
The source and target must have the same account and owning organization;
account administrator/owner authorization and completed MFA remain required.
Foreign or unavailable destinations produce a generic denial.

Approval loads target configuration, rules and captured bytes inside the source
account transaction. Another application's destination must be its single
production routing deployment at 100 percent. A same-application target must
be the candidate. The compatibility checker compares the original operation
against the resolved destination operation, including request, response and
security contracts, without outbound HTTP requests.

Canonical HTTPS hosts and exact verified application-wide custom domains are
supported. Ports, tenant hostname claims, environment domains, wildcard-only
routes, and hostnames inside the reserved platform suffix that do not identify
the pinned app are rejected. Potentially applicable routing, rewrite, redirect,
async and maintenance rules are rejected, including conditional rules. Because
URLs may contain operation templates, a matching host and method are enough to
require further routing review; a single path/header sample is not proof.
Stronger target application authentication and maintenance also block approval.
Declared-route ingress allowlists also require further review because they use
separate app-owned policy evidence. Project-owned target apps require a future release-graph/settings binding and
are rejected rather than resolved from mutable application intent.

Receipts store a database successor snapshot: target application ownership and
visibility, policy configuration, exact hostname binding/tenant claim, capture
identity and document digest, and destination production routing distribution.
Insertion compares the snapshot taken before validation against current inputs.
Receipt lookup and production traffic triggers compare it again, and the
traffic trigger locks consumed receipt rows against concurrent invalidation.
Domain, routing, capture and rule changes, plus application identity, authentication,
maintenance and declared-route policy changes, permanently invalidate affected
receipts. Other captured configuration differences also make the binding unusable
while changed. Restoring permanently invalidated inputs cannot revive a receipt. Source traffic changes themselves do not invalidate a receipt
used for that source's candidate; target traffic changes do.

Existing receipts lack this binding and are invalidated by the migration. The
one-hour TTL, baseline-specific approvals, and inability to waive other
lifecycle/removal findings remain unchanged. Memory store checks mirror the
binding and perform permanent invalidation after routing mutations.

## Consequences

Cross-app migrations and verified exact custom domains can be reviewed with
local captured evidence. Ambiguous weighted targets fail closed. Unrelated
policy changes may conservatively require a new review. Release graphs,
request-dependent routing chains, environment/wildcard domains and tenant
surfaces remain explicit follow-up work.

The operator's canonical hostname suffix remains process configuration, outside
the database. API review checks its live fingerprint, but generic workers cannot
observe a suffix change; operators must invalidate/review existing receipts when
changing that configuration, as in ADR-834. No claim of automatic invalidation
for operator configuration is made.
