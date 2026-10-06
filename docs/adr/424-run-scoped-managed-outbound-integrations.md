# ADR-424 · Managed outbound integrations for stateless Runs

- **Status:** proposed
- **Date:** 2026-10-01
- **Relates to:** ADR-171 (disposable one-shot executions), ADR-249 (platform-held outbound credentials), ADR-251 (managed outbound route policy), ADR-255 (public destination dialing), ADR-372 (tenant egress gateway)
- **Decision:** Let a Run call explicitly bound managed outbound integrations through a host-mediated capability broker. Keep the execution VM networkless; do not enable general guest egress.
- **Why:** Agents need to read and act on approved external services, while stateless Runs must not receive durable disks, provider secrets, DNS, or arbitrary network access.
- **Consequences:** Add Run-scoped integration grants, a bounded guest-to-host request protocol, an execution workload identity, outboundd authorization and accounting, and JavaScript/Python runtime helpers. Existing app bindings and `network.mode=none` retain their current meaning.
- **Rejected alternatives:** Give Runs a normal tenant network and DNS; pass provider credentials or gateway bearer tokens into caller code; treat an account's app binding as an implicit Run grant; accept arbitrary URLs and attempt to filter them after DNS resolution.

## Decision details

The public Run request adds an optional list of managed integration IDs. Admission
resolves each ID against explicit account-owned **Runs bindings** before it
persists the execution. The IDs are immutable execution intent and are covered
by the same account/principal checks as other Run metadata. Omission means the
Run receives no integration capabilities. A caller cannot bind an integration
by naming an app, and existing app-to-integration bindings do not grant Runs
access.

The scheduler receives this allowlist alongside the encrypted request, never
inside the guest payload. It is a ceiling on a Run's possible integrations,
not a bearer capability: because an account can revoke a grant after admission,
the broker must check the current grant and integration state on every call.

An authorized Run can call only the Gregale integration helper from its runtime
context. Its request contains an integration ID, an HTTP method, a relative
provider path, and an optional bounded body. It cannot supply an origin,
redirect target, proxy, credential, or arbitrary URL. JavaScript and Python
receive the same helper contract. Runtime profile packages remain immutable;
this helper is part of the trusted executor image and does not require a
per-Run dependency install.

The helper uses a loopback-only guest endpoint backed by a dedicated AF_VSOCK
request/response channel. Guest-init forwards each call over its existing
per-instance vsock bridge to vmmd. vmmd verifies that the live instance is an
execution VM, mints a short-lived assertion scoped to that execution and one
integration, then emits a typed call event over a new bidirectional execution
gRPC stream to schedd. schedd calls outboundd over its loopback listener and
returns the bounded response on the same stream. Cancellation closes the
stream and releases the upstream request. This keeps outboundd's listener
private and leaves outbound policy with outboundd, which already owns the
fixed-origin dialer and integration admission state.

The execution VM still has no Firecracker network interface, tenant route,
DNS, service discovery, or metadata route. The new broker does not change
`network.mode=none`; it is a separate capability. The signed assertion and
provider authorization are visible only to vmmd, schedd, and outboundd, never
to the guest process or runtime helper.

vmmd derives the caller identity from the live execution instance, never from
guest-provided account or execution IDs. It mints a short-lived assertion for
one integration and one execution. outboundd verifies the assertion and
requires an active Runs binding for the same account and integration before it
forwards the request. Existing fixed-origin dialing, method/path policy,
managed credential resolution, request budgets, concurrency limits, retries,
and redirect rejection remain authoritative. Only managed-credential
integrations are eligible; legacy application-supplied gateway tokens are not
available to Run code. Credentials and workload assertions never enter the
execution result.

Bindings are explicit, account-owned policy objects with an optional narrower
method/path policy and daily request budget. The integration-wide policy is
the upper bound. Each admitted request is attributed to the execution ID for
audit, while metric labels remain bounded and never include execution IDs.
Audit records include integration ID, execution ID, method, bounded route
class, result status, and byte counts; they exclude request/response bodies,
authorization values, and caller headers.

The helper accepts only bounded JSON requests and returns status, a small
allowlist of safe response headers, and a bounded body. It strips hop-by-hop,
cookie, and authentication response headers. The total integration request
and response bytes, including framing overhead, are capped independently of
the Run's normal stdout/result/artifact cap. A call's deadline is the minimum
of the Run's remaining deadline and the integration request timeout. Run
teardown closes outstanding broker calls; the gateway still owns upstream
timeouts and admission leases.

## Rollout and acceptance

1. Add account-scoped Runs binding state and management APIs. Bind and unbind
   require the same MFA and deploy-write controls as managed integration
   configuration. Reads are account-scoped and return policy, usage, and
   integration metadata, never credentials.
2. Add an execution-specific identity claim and outboundd admission path. The
   assertion is tied to the live execution and one requested integration;
   outboundd rejects expired, cross-account, unbound, disabled, and
   app-identity assertions on this path.
3. Add a bidirectional vmmd execution stream and the bounded guest/vsock
   broker. Calls cannot outlive the execution lease, and all success and
   failure paths close their stream and release gateway admission state.
4. Add the matching JavaScript/Python helpers, API/SDK/CLI support, and agent
   examples after the production path passes the isolation and gateway tests.

Ship gates cover: a guest still cannot resolve or connect to any address;
only explicitly bound IDs work; a forged ID/account, another Run's assertion,
and a revoked/disabled binding fail closed; integration route and daily
budgets apply; managed credentials never appear in guest memory, environment,
logs, artifacts, or receipts; cancellation/timeout tears down both the VM and
in-flight broker call; and response bodies, headers, and framing stay within
the configured caps. Native Linux KVM tests and `make leakcheck` are required
before enabling this for customer Runs.

The API and user-facing docs must continue to describe Runs as stateless:
source bundles, helper scratch, request/response buffers, and output artifacts
exist only for one execution. This decision introduces no persistent disk,
workspace, dependency installation, or cross-run filesystem state.
