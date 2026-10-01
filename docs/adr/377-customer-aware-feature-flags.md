# ADR-377 · Customer-aware application feature flags

- **Status:** accepted
- **Date:** 2026-09-30
- **Decision:** apid publishes immutable, atomic flag configurations per project
  environment. Runtime SDKs evaluate locally against verified gateway customer
  identity and a request-pinned configuration snapshot. Flags select already
  deployed behavior; they do not select deployments or grant permissions.
- **Why:** Customer platforms need selective behavior release with an explanation
  and the same customer attribution used by requests and durable work.
- **Consequences:** Owners explicitly add application checks. Configuration writes
  serialize on the environment row and require the observed version. Rollback is
  a new publication. Server-generated seeds are stable for an environment/key,
  including removal and recreation. Ordered rules use first-match precedence;
  customer lists, group membership, and eligible-customer percentage constraints
  combine with AND. Anonymous requests do not match customer rules.

The capability is internal until operational and native restore qualification;
`FAAS_FLAGS_ENABLED=1` enables owner access for operator qualification.
The initial runtime client is the server-only Node SDK. It retrieves configuration
with the existing loopback workload identity endpoint and an RS256 assertion for
`gregale:flags`. apid derives account, project and environment from the live
instance and its deployment. Account API keys cannot use the runtime endpoint;
app-scoped deploy keys cannot use project management endpoints. Preview apps
are excluded until their independent configuration scope is defined. Operators
install public JWKS on apid; no remote key discovery occurs on the request path.

Each request uses one immutable snapshot. A resumed process detects elapsed or
regressed wall time before evaluation; if configuration is older than its bounded
freshness window, it attempts a bounded refresh and otherwise uses the caller's
explicit fallback. This does not guarantee instantaneous changes. Disabling a
flag returns its configured default, which may be true; setting default false is
necessary for an off switch. Missing or stale configuration uses the application
fallback. Existing requests retain their pinned version.

The SDK emits a bounded response header containing decisions and an explicit
`used` marker. Both streaming and legacy gateway response paths consume and
strip it. The gateway supplies verified request-time customer attribution.
Evidence is application-reported, not proof of business side effects. Canonical
flag evidence participates in telemetry aggregation so values, rules, versions,
and fallback states cannot collapse together. The protobuf extension is optional
and backward compatible. Deployment and tenant identity still come from the
existing trusted gateway paths. Billing remains independent of this metadata.

Flag evidence shares debugger retention, rate caps and bounded delivery. The
inspector filters retained requests by flag, value, exposure and customer within
the environment's deployments, returning weighted request counts and latency
representatives. It is not an exactly-once exposure ledger or a causal experiment
analysis. Managed request caching and early edge responses do not imply a new
application exposure. For flag-sensitive content, applications must use private
or customer-aware caching, with appropriate invalidation on behavior changes.

The first acceptance gate runs the real Node SDK, apid, gateway and PostgreSQL
with four customers: three select the new implementation, one retains the old
implementation; forged customer headers are replaced, new-path errors are
filterable, and the same process refreshes disablement after simulated inactivity.
This is application/configuration acceptance, not native KVM park/restore proof.
No VM lifecycle behavior is changed by this decision. SDK allocation vectors and
rollout monotonicity, optimistic concurrency, tenant ownership, history, rollback,
workload-token audience and scope, and evidence stripping are separate gates.

Multivariate flags, arbitrary user attributes, automatic progressive release,
and explicit authenticated inheritance across services or queued work are future
extensions. Current downstream or async HTTP delivery reevaluates configuration
in that workload's environment using its existing verified tenant identity; no
client-supplied flag header establishes an inherited decision.

Operational owner: apid and SDK maintainers. Recover by publishing a known good
version with the current expected version, or using explicit application defaults
when runtime refresh is unavailable. Retain retired public signing keys through
JWT expiry and restart apid after updating the configured JWKS file.
