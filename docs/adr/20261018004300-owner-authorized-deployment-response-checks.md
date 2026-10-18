# ADR-20261018004300: Owner-authorized deployment response checks

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
deployment-preview path when available, validating the returned app and deployment
identities in both default and named environments before using the URL. Every
launch requires response evidence for its accepted deployment, including the first
launch and recovery from older session records. A successful public response that
omits deployment identity is followed by the exact owner check;
its evidence is labeled as owner-authorized, not public URL verification. Public
denials, redirects, edge health answers and mismatched deployment headers do not
trigger this check. Edge health answers cannot verify an app response. Public
revision retention settings remain unchanged. Update the OpenAPI contract and
generated SDKs together.

New accepted-session records retain the strict-evidence flag for recovery by older
CLI versions. Current verification does not depend on this flag being present or
true, so an older first-launch record cannot bypass the identity requirement.

### Compatibility before mutation

Expose GET `/v1/deployments/response-check/capabilities` with account
authentication, deployment-write scope and rate limiting, without requiring MFA
for this metadata read. Apid queries GET
`/v1/internal/deployment-response-check-capabilities` on its configured trusted
control listener. Both hops reject redirects. The API returns its protocol version
and reports availability only when the gateway implements the same version and
has the forwarding dependencies used by the response-check handler. No app lookup,
capacity admission, deployment, secret write, source upload or guest request occurs.
This reports installed support and configuration, not capacity or future health.

The gateway query is bounded to three seconds and the existing 64 KiB envelope;
Start bounds the complete preflight to five seconds. Bounds are centralized in
`pkg/api/limits.go`. All responses use `Cache-Control: no-store`. Stable reasons
distinguish unsupported, unconfigured and unavailable gateways without exposing
internal addresses, credentials or raw proxy failures. Older APIs reject the new
path without interpreting the capability name as a deployment identity.

Start checks compatibility before deployment review and repeats the read after
reviewed account/source/target checks at the shared resolver's first-mutation
boundary. It does not cache positive results or persist them as permission to
deploy. A later service change remains a normal failure; no preflight can promise
future availability. Existing deploy commands retain their contracts.

Recovery checks compatibility before owner verification of an accepted deployment.
Unsupported installations show version/configuration guidance and the saved ID,
without app logs or futile path/retry prompts. Temporary outages offer retry and
finish; retries check the same accepted deployment. App HTTP failures keep their
existing log and alternate-path recovery. A blocked fresh launch preserves the
previous recovery record. Update the OpenAPI contract and generated SDKs together.

### Recovery before an acceptance reply

Start persists a pending source submission before upload, recording a unique
logical retry key, transport, app identity and byte count. It saves the exact
resumable upload ID before sending source bytes or committing. These synchronous
callbacks stop submission if local persistence fails. No archive, source contents,
secret values or secret-file paths are added to the session record.

A restart reconciles a pending submission before offering a fresh launch or
resuming the previous deployment. Owner-authorized upload discovery identifies
its committed deployment; app, deployment, project/environment lifetime and scope
checks still apply. A fully uploaded open session can continue its original
idempotent commit without uploading source or repeating app/secret configuration.
Already committed recovery uses reads only and needs neither local source files
nor the generic upload cache. The previous accepted and verified IDs remain
recorded until the recovered ID is durably saved; only a successful response
check advances the verified ID.

An unfinished upload can be cancelled explicitly before reviewing source again.
Cancellation must be confirmed by discovery, and an already committed upload
recovers its deployment instead. Missing, expired discovery records, outages,
identity mismatches and unknown outcomes retain the pending record. A terminal
cancelled/expired upload or a failure proven to precede commit permits another
review. A failed commit reply, including a final rejection after an earlier
attempt, is not evidence that no deployment was accepted. Legacy multipart
uploads lack this discovery contract: uncertain replies require manual history
inspection, with no guessed latest deployment or automatic resubmission.

Pending records use session format 2 so older CLIs refuse them rather than
silently resuming an earlier deployment. Records without pending work retain
format 1 compatibility. Existing deployment/upload APIs and their ownership,
idempotency and retention rules are unchanged.

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
