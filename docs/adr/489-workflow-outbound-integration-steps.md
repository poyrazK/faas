# ADR-489: Managed outbound integration steps in workflows

Status: Accepted (preview)

## Context

Workflow action steps currently invoke app handlers. Customers need to call
external services without writing an app handler for each request. Outboundd
already owns fixed-origin routing, managed credentials, app binding permissions,
destination validation, request accounting, and provider admission policies.

## Decision

Add an `outbound` step target containing an existing customer managed integration
UUID, HTTP method, and fixed canonical relative path. Exactly one step target is
allowed. The normal workflow input templates supply a bounded JSON request body.
GET and HEAD send no body and forbid explicit input. This first slice supports
no query parameters, dynamic paths, custom headers, origins, or inline credentials.
Subsequent implementation adds whole-segment path templates and bounded query
value templates while retaining fixed-origin routing and live route policy
checks. Simulation now resolves these targets using the executor's resolver,
including loop contexts, before accepting action mocks. See
[automation authoring](../automation-authoring.md#preview-managed-integration-requests)
for the current syntax, limits, and preview contract.
The integration must be customer-owned, sealed-credential managed, enabled, and
explicitly bound to the workflow app. Authoring validation and publication check
the integration and binding route policies. Runtime authorization checks current
intent independently, so publication never creates an enduring permission grant.

Schedd sends requests only through the existing private outboundd loopback
listener. It uses its rotating Ed25519 cluster signer with the separate
`gregale:workflow:outbound:{integration_id}` audience and a workflow subject.
Assertions last 30 seconds and bind account, app, workflow run, step, numeric
attempt, fresh private attempt token, method, path, and request-body digest.
App workload, stateless Run, and ordinary internal-service assertions cannot
authorize this surface. The signer, provider credential, and attempt token never
enter customer API responses or app handlers. No VM lifecycle path changes.

Tenant-bound runs may use these steps through an integration still explicitly
bound to the app. The persisted run tenant is included in the signed identity;
schedd rechecks that the tenant is active and still linked to the app before and
during the provider call, and outboundd compares the signed tenant with the run
record and repeats the active-link check before authorizing each request. The
integration credential and route policy remain app-owned and shared across
tenants. This does not add per-tenant credentials or provider identity mapping.
At the time of this decision, tenant event waits and callbacks remained
unsupported because their external continuations did not yet carry
tenant-scoped admission. [ADR-635](635-tenant-workflow-continuations.md) later
adds authenticated, tenant-scoped event and callback continuation routes.

`workflow_steps.outbound_attempt_token` rotates whenever a step starts. Outboundd
reads the current cluster public key and checks the live run lease, step token,
attempt, immutable definition target, account/app eligibility, integration,
credential presence, and explicit app binding on each call. The gateway then
applies current integration and app route policies and shared admission limits.
Cancellation, attempt replacement, expired leases, maintenance, and account
ineligibility cancel an in-flight scheduler request at the next one-second
check; provider-side effects already performed cannot be undone. App binding
revocation denies subsequent admissions; already-admitted calls may finish.

The workflow owns durable retries. Outboundd makes at most one provider attempt
per workflow call, and subsequent workflow attempts consume its shared retry
budget when configured. Dedicated HTTP/1 transports use fresh connections on both
legs to prevent implicit transport replays outside the attempt ledger. Response
caching is disabled for this surface. Workflow
retry deadlines persist exponential/fixed backoff and bounded Retry-After values
(up to one hour), without sleeping in the dispatcher. HTTP 408/425/429/5xx and
transport failures are eligible only when the operation is safe to repeat.
GET/HEAD are repeatable. Mutating operations require explicit
`idempotency_supported: true`, asserting that the provider deduplicates the stable
`workflow/{run_id}/{step_name}` Idempotency-Key. Without that assertion, mutation
steps default to one attempt and cannot configure multiple retries. After a
crash with an uncertain mutation result, recovery marks the step and attempt
terminal rather than repeating the request. Safe outbound recovery retains the
consumed attempt count; fresh authorizations cannot reuse old private tokens.
Completion and retry writes reject obsolete attempts under the run lock. A stale
worker cannot recover or overwrite its replacement; cancellation closes active
outbound attempt records along with the run.

Successful step outputs have `{status, body}`: body is parsed JSON, a string for
non-JSON responses, or null for an empty response. HTTP status and failures use
the existing attempt ledger. Failed response bodies and authentication/cookie
headers are omitted. Raw reflected managed credentials or assertions are rejected
before response bytes are returned. Workflow audits omit output bodies for flows
with outbound steps; customer-scoped run/step inspection retains successful
bounded outputs. Request and provider-body limits are centrally defined at 1 MiB.

## Rollout

Apply the additive attempt-token migration before deploying the updated binaries.
An active `cluster_signing_keys` row must exist and be decryptable by schedd.
Outboundd follows that current public key directly, including rotation; the legacy
per-host development signer is insufficient for this feature.

Keep `FAAS_WORKFLOWS_ENABLED=1` on apid and schedd. Outbound steps additionally
require `FAAS_WORKFLOW_OUTBOUND_ENABLED=1` on schedd and outboundd; outboundd also
accepts `workflow_outbound_enabled = true` in TOML. The default stays disabled.
The existing loopback listener must remain private. No production configuration,
credentials, runtime gates, or frontend files are changed by this implementation.

Before rolling back, pause new automatic starts and drain/cancel workflow runs
containing outbound steps. Replace published and manifest definitions containing
outbound steps with versions compatible with the older binaries before downgrade.
Older binaries cannot decode the new target. Drop the
additive column only after updated workers are stopped; exporting definitions
and retaining run history remain the operator's responsibility.

## Verification

Tests cover strict JSON/YAML target parsing, unsafe retries, route permissions,
publication with missing bindings, live PostgreSQL authorization, account/app
isolation, request-body binding, token replacement, cancellation, shared gateway
policies, one provider attempt, sensitive response rejection, durable retry/input
snapshots, and crash recovery for uncertain mutations. These changes require
normal backend and contract gates; native KVM lifecycle acceptance is unaffected.
