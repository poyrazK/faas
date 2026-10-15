# ADR-1331: Owner-authorized deployment response checks

Date: 2026-10-15
Status: Accepted

## Context

Start must verify the deployment it accepted, including private apps in named
environments. Ordinary sync invocation is default ingress and deliberately
rejects stage pins. A completed invocation also does not establish an HTTP
status. Public revision retention settings must not be changed to run a check.

## Decision

Add POST `/v1/apps/{slug}/deployments/{deployment}/response-check` with the
existing account authentication, MFA, deployment-write scopes and API rate
limiter. The body pins the app UUID, explicit runtime scope, an app-relative GET
path, and project/environment UUIDs for named scopes. Reject unsupported fields,
ambiguous identities, cross-account targets, URL fragments and external URLs.

Apid validates the current owned app, live deployment and catalog lifetime,
then calls a dedicated route on gatewayd-internal's existing trusted control
listener. New apid logic lives in `pkg/apid/responsecheck` under ADR-982.
Gatewayd resolves the deployment's immutable workload settings, enforces account
and security holds, and uses its existing selected-deployment capacity path to
wake a parked replica through schedd. It selects only that deployment and
validates the running replica's app, deployment, node and wake identities.

Forward one fresh GET through the existing per-node VMMD forwarding transport,
stamping the validated replica identity and selected deployment's wire protocol.
The host comes from the app slug and configured app domain, or the pinned
environment's stable hostname for named scopes. No account token,
cookies, request body, arbitrary headers, redirects, public cache, revision
retention override or durable invocation is involved. Application-owned
authentication remains effective and its HTTP denial is returned honestly.
The check authorizes owner access to the workload; it does not prove public
ingress, end-customer authentication or a public hostname's routing policy.

Recheck the deployment, environment lifetime and replica after forwarding;
apid independently rechecks the deployment/environment before returning evidence.
Return selected identities, the actual HTTP status and a bounded UTF-8 response
preview. HTTP application errors are evidence in a 200 API envelope, not success
verdicts. A changed or unavailable target returns an error without a response
body preview. All API/control responses use `Cache-Control: no-store`.

Bounds live in `pkg/api/limits.go`: 20 seconds, a 2048-byte relative path,
a 4096-byte response preview and a 64 KiB control envelope. Larger response
bodies cancel the local forwarding context and are explicitly marked truncated.
The forwarding context preserves the hard deadline and cancellation after
response headers; it cannot detach them as an ordinary streaming budget.
The exact POST route receives a 20-second apid request budget instead of the
five-second ordinary API default; caller-requested tighter budgets still apply.
Path queries and response content remain ephemeral; neither is onboarding
metadata. Existing plan admission and scheduler ownership are unchanged.

Start uses the new Go SDK method for private checks in every scope, validates
returned identities, and reports the actual HTTP status as a private check.
Retries retain the accepted deployment. Older servers fail visibly without
falling back to an unpinned/default invocation. Public checks first use their
deployment-preview path. During an update or resumed update, a successful public
response that omits deployment identity is followed by the exact owner check;
its evidence is labeled as owner-authorized, not public URL verification. Public
denials, redirects, edge health answers and mismatched deployment headers do not
trigger this check. Edge health answers cannot verify an app response. Public
revision retention settings remain unchanged. Update the OpenAPI contract and
generated SDKs together.

## Consequences

Private first-response checks work without changing public access, active release
graphs, secret configuration or revision pin TTLs. Gatewayd must receive the new
control handler before apid/Start can complete these checks. Mixed versions fail
closed. Unavailable capacity, expired deployment state and changing environment
lifetimes remain explicit failures. No schema migration, VM lifecycle change or
new scheduler protocol is introduced.

Portable tests cover owner/target isolation, changed environment lifetimes,
selected-deployment wake, credential isolation, response bounds, redirects,
application errors and CLI retry behavior. Native deployment validation remains
required before production rollout.
