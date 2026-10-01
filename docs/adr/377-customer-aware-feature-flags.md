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
`FAAS_FLAGS_ENABLED=1` enables owner access for operator qualification. The
native e2e gate requires `TestFeatureFlagsNativeParkRestoreMetal`, which checks
configuration refresh and verified customer evidence after a real VM restore.
The catalog points to this test as the capability qualification evidence; the
database-backed `make test-flags` gate continues to cover management and
application-level acceptance.
The first runtime client was the server-only Node SDK. Runtime clients retrieve
configuration with the existing loopback workload identity endpoint and an
RS256 assertion for `gregale:flags`. apid derives account, project and environment
from the live instance and its deployment. Account API keys cannot use the runtime
endpoint; app-scoped deploy keys cannot use project management endpoints. Preview
apps are excluded until their independent configuration scope is defined.
Operators install public JWKS on apid; no remote key discovery occurs on the
request path. The Python SDK exposes the same runtime contract through an async
client, ASGI request middleware, and an HTTPX transport. Its evaluator and bounded
evidence/context envelopes use the same cross-language allocation vectors.

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
This is application/configuration acceptance; the native KVM park/restore proof
is a separate required test in the hardware gate. No VM lifecycle behavior is
changed by this decision. SDK allocation vectors and
rollout monotonicity, optimistic concurrency, tenant ownership, history, rollback,
workload-token audience and scope, and evidence stripping are separate gates.

Arbitrary user attributes remain a future extension. Staged rollout plans allow
explicit, health-gated promotion, with opt-in automatic advancement after a full
healthy evidence window. For managed service calls, the Node fetch helper and
Python HTTPX transport propagate only decisions marked used. The Python ASGI
middleware pins the verified request context and writes evidence before response
headers are sent. The service proxy
forwards a bounded, canonical envelope only after its existing caller identity
and binding checks; public ingress removes caller-supplied copies. Downstream
SDK evidence records the original app, environment, config version and rule
with source `inherited`. For `queues/send` and the app inbox, producers may
attach that same bounded envelope. apid binds its customer to the durable
invocation, and synthetic delivery restores the tenant and canonical flag
context on every attempt. This envelope carries application behavior context
and never grants access or entitlements. Other asynchronous work continues to
evaluate its own configuration using its existing verified tenant identity.

Operational owner: apid and SDK maintainers. Recover by publishing a known good
version with the current expected version, or using explicit application defaults
when runtime refresh is unavailable. Retain retired public signing keys through
JWT expiry and restart apid after updating the configured JWKS file.
