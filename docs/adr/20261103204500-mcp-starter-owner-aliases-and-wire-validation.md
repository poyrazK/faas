# ADR-20261103204500: MCP starter owner aliases and wire validation

## Status

Accepted; extends ADR-20261017191501 and ADR-426.

## Context

Canonical and earlier Go Task owner hashes identified the same caller but used
independent execution buckets. Native Go/Python v2 payloads also used a different
KDF and AAD from Node's explicit key ring. Go/Python ignored disabled legacy
mode and modern request header mismatches. Gateway preflight omitted annotated
parameter headers already supported by starter CORS policy.

## Decision

Add `gregale_mcp_task_owners` to each standalone starter's explicit schema apply.
It maps namespace-scoped owner HMACs to an opaque group hash. Authenticated
caller operations derive and associate all known canonical, earlier Go and
retained-key aliases. No raw principal is persisted. Association merges groups
under the shared namespace execution lock used by claims and lease renewal.
Admission takes its lock before the execution lock; cleanup uses the same order.
All running tasks in a group consume one owner's capacity, and Node observation
uses that same grouping. Keep all aliases until the group's last unexpired Task
is gone, then prune them during existing worker cleanup.

Workers cannot reverse an HMAC to reconstruct missing aliases in a retained
queue. Unregistered owners therefore share one conservative unknown bucket per
namespace, subject to the existing per-owner limit. Their next authenticated
operation establishes the group, including aliases without existing Task rows.
This can temporarily reduce throughput, but cannot create additional capacity.

Node accepts the native v2 owner-key ID, original payload KDF and base AAD as an
authenticated fallback alongside its existing explicit key-ring format. Resolve
current and retained native owner keys consistently for migrations, runtime and
doctor. Retained keys also contribute lookup aliases. Preserve Node's existing
write format, fingerprint enforcement and stable owner-secret requirement.

Go/Python apply `legacy: false` to discovery, initialize negotiation, HTTP version
selection and notifications. Validate explicit modern requests' method, name,
protocol metadata and annotated primitive argument headers before dispatch;
reject mismatches with HTTP 400 and RPC code -32020. Notifications are exempt
from required method/name headers. Preserve unversioned compatibility requests.

Gateway preflight validates the requested method and header names and reflects
only existing supported headers or well-formed `MCP-Param-*` names. Vary on
Origin and preflight method/headers. The configured exact Origin allowlist and
actual-request authentication remain authoritative; reject duplicate origins.

## Consequences

Run each starter's migration command and refresh runtime/observer grants before
rollout. Drain and upgrade every worker sharing a namespace before relying on
alias-aware limits; older workers do not use the registry. Retain previous owner
keys until Task TTL ends and use matching owner IDs across runtime handoffs.
Runtime accounts receive only the required DML and observers only SELECT.
Local PostgreSQL, HTTP, ciphertext and concurrency regressions cover these
contracts. Native KVM and real OAuth/provider qualification remain release gates.
