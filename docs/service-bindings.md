# Service bindings

A binding connects an app to a service, database, bucket, queue or outbound integration. Read the binding inventory, check readiness in CI, verify a binding with a canary, and make a release wait until its bindings are ready.

## The binding inventory

`GET /v1/apps/{slug}/bindings` provides the shared binding inventory used by
the CLI and SDKs. It batches app-scoped catalog reads for services, PostgreSQL,
object storage, queues, and outbound integrations. Omit `scope` to inspect all
resource scopes, or use `?scope=staging` / `gregale app binding public-api --env
staging` to filter database, bucket, and environment queue bindings. Shared
legacy queues and app-wide service and outbound bindings remain included; the filter does not create or isolate
resources for that environment.

The binding inventory separates configuration from queue consumer state and
liveness. An enabled queue binding is shown as `enabled`; its consumer can
still be `not_configured`, `not_observed`, `degraded`, or `stale`. Service
`declared`/`enforced`, database `ready`, and storage credential `active` describe
their configuration or provisioning state, not verified application
connectivity. JSON `runtime_status` is `unknown` unless a queue consumer has a
scheduler observation; `observed_at` identifies that observation, while
`generated_at` is the inventory collection time. `verification_status` is
`passed`, `failed`, `unknown`, or `stale` for service, runtime PostgreSQL,
object-storage, and configured outbound entries.
The optional `verification` contains sanitized stage statuses, the actual task
scope and deployment, completion time, and PostgreSQL credential generation.
Queues and release-only migration bindings remain `unknown`. Queue entries
publish `queue_environment`: an empty string means shared legacy configuration;
otherwise it names the binding's environment. This also distinguishes a project
environment named `app` from the app-wide inventory label. Outbound entries show integration enablement, credential
configuration, and the app binding's allowed methods and path prefixes. They
do not expose provider credentials, resource IDs, or sealed secret names.

`runtime_freshness` separately compares resident app instance start timestamps
with the latest app-wide environment or secret change. It groups live
deployments and deployments with resident instances by deployment and scope.
`serving` counts running instances; `resident` also includes starting, warm,
snapshotting, draining and migrating instances. Each count is `current`
(started after the stamp), `stale` (at or before it), or `unknown` (missing
stamp or timestamp). `starting` is a subset of resident counts. Task guests,
jobs, mirrors, parked and terminal instances are excluded. A deployment is
`stale` while any stale resident remains, then `unknown` if any resident is
unknown, `updating` while a replacement starts, `current` when its remaining
residents are current, and `inactive` when it has none. Scope filtering also
filters this projection. The app-wide stamp conservatively applies to every
binding; these timestamps do not prove guest acknowledgement, readiness,
credential use or connectivity. A passing canary never changes these counts.

Pending PostgreSQL and object-storage rotations may include `refresh`, the
durable rolling restart's correlation `wake_id`, status, attempts and sanitized
failure reason. `not_queued` means no retained outbox record was found, including
rotations staged before handoff or waiting for a later wake. It is not proof
that a restart never happened. `unknown` means status could not be read.
`completed` describes that handoff; it does not override stale runtime counts
or verification. The CLI prints freshness alongside verification and warns
when serving instances predate the configuration change. Freshness or refresh
read failures preserve other results and make inventory incomplete.

The endpoint requires app read access and completed MFA for browser sessions.
PostgreSQL metadata additionally requires `managed-postgres:read`; storage
metadata requires `storage:read` or `storage:manage`, as does listing compute
bindings; neither returns a credential, so a least-privilege CI key can run
`gregale app binding check`. An admin key or authenticated session has the
existing full access.
Insufficient resource permissions omit that section without reading it.

If a section or consumer status cannot be read, the API returns HTTP 200 with
the available results, `complete: false`, sanitized `warnings`, and structured
`issues` (`type`, `code`, `severity`, `message`). The CLI prints the available
results and warnings in the same document; failed queue status reads remain
`unknown`. Error issues, including denied resource permissions, return CLI exit status 1 after printing the partial
inventory. A gated, unavailable PostgreSQL preview remains a warning by
default; use `gregale app binding public-api --require-complete` to return status 1 for
any incomplete inventory, including that preview. `complete: true` means all
inventory and status reads succeeded, not that every dependency is healthy.
The response is a best-effort projection, not an atomic snapshot, and uses
`Cache-Control: no-store`. An unobserved consumer is not a failed read: its
configuration can be known while its runtime status remains `unknown`.
The command only reads metadata; it does not invoke application handlers or
providers. Use `bindings verify` for service, PostgreSQL or object-storage connection probes.

## Check bindings in CI

Use `bindings check` to evaluate those recorded observations in CI:

```sh
gregale app binding verify public-api --all
gregale app binding check public-api --scope production --max-verification-age 10m --json
```

The default check makes one inventory GET and returns exit status 0 when its policy
passes, or 1 for blockers, invalid options or failed reads. It does not create
tasks, invoke providers or restart instances. Omit `--deployment` to use the
current manual-task deployment. Use `--deployment ID|vN` to select an exact live,
materialized deployment, including a candidate receiving zero traffic. Revision
handles resolve within the named app. An explicit `--scope` asserts that the
selected deployment uses that scope; omit it to use the deployment's scope.
App-wide service and outbound bindings and shared legacy queues remain included.
PostgreSQL, object-storage and environment queue bindings in other scopes are
explicitly skipped. PostgreSQL bindings with `access: migration` are reported as
`skipped` with reason `release_only`: their credentials are confined to the
deployment's release task. The existing release-command gate must succeed before
activation; runtime probes, application ACKs and refresh requirements do not
validate migration credentials. `bindings verify --all` skips these bindings and
creates no migration probes.

### Wait for pending probes

Use `--wait --timeout 5m --poll-interval 1s` to observe already pending probes,
credential refreshes, runtime replacement or application acknowledgements. Waiting
pins the first selected deployment and scope and prints one final report. It
creates no tasks or refreshes. Terminal failures and missing probe evidence stop
immediately; timeout returns the last blocked report and exit status 1. Ctrl-C
returns 130. Failed reads stop the wait with a nonzero result.

### Check a staged rollout before promotion

For a staged rollout, verify and check the same deployment before promotion:

```sh
gregale app binding verify public-api --release v12 --all
gregale app binding check public-api --release v12 --max-verification-age 10m --json
gregale release traffic promote --app public-api --release v12 --if-serving v11 --require-bindings --max-verification-age 10m
```

`--require-bindings` makes the server repeat the exact deployment check and
compare binding/configuration, probe and runtime facts inside the traffic
transaction. Blockers, changed facts or evidence that expires while waiting
leave traffic unchanged. The gated promotion API is
`POST /v1/deployments/{id}/promote`; older servers return 404 before mutation.
JSON receipts include `bindings_check`, the applied policy and coverage. Check
failures return structured blockers in the problem envelope. An already
promoted target still requires a current passed check. The default maximum
verification age is ten minutes; `--allow-unsupported` explicitly waives
queue/outbound probe coverage and keeps it partial. These policy flags require
`--require-bindings` on promotion. Promotion creates no probes or restarts.
The gate protects committed platform observations at promotion; later changes
and upstream outages still require monitoring.

Evidence stays separate per deployment, binding and scope. A passing serving
revision cannot hide a failed or unprobed candidate. The CLI requires the server
to confirm explicit selection and rejects admission or polling receipts from
another deployment or scope. Older servers that ignore the selector cannot
produce a passing result. UUID selection uses one inventory read; revision
selection first resolves the app's deployment ladder. Probes use the selected
artifact with current scoped configuration. A check does not lock configuration
or traffic promotion against subsequent changes.

### What the default policy requires

The default policy requires a complete inventory, ready managed bindings and
passed service, PostgreSQL and object-storage evidence matching the selected
deployment and scope. Evidence must have a completion timestamp no more than
ten minutes old, including the boundary, and must not be in the future. Use a
positive `--max-verification-age` duration to change that window. Missing,
pending, failed, stale or contradictory evidence blocks the check. PostgreSQL
credential generations must also match.

In the selected scope, stale or unknown residents and starting replacements
block the check, including residents from superseded deployments. A fresh
passing task probe cannot hide an old serving instance. An inactive deployment
is allowed with a warning that no resident adoption can be assessed. Pending
credential retirement or an unfinished, failed or unknown refresh handoff also
blocks; a completed handoff does not override pending retirement or runtime
freshness. Inventory read issues block even when their severity is `warning`.

### Bindings without a connectivity probe

Active queue bindings and outbound bindings without a supported configured probe have unsupported connectivity coverage and
block by default. `--allow-unsupported` explicitly waives only this coverage gap;
the report retains `coverage: partial`, `status: unsupported` and a warning when
consumer readiness has no blockers.
Disabled queue and outbound bindings are skipped. Unknown binding types always
block and require a CLI update. A selected scope with no active bindings reports
`coverage: none` with a warning rather than claiming connectivity coverage.

### Push queue consumers

Enabled push queue bindings also require an active, healthy scheduler consumer
with a nonzero poll timestamp at most thirty seconds old and no later than the
check clock. Missing, paused, degraded, unobserved, unknown or stale consumers
block with `queue_consumer_*` findings, including under `--allow-unsupported`;
their per-binding status is `blocked`. `--max-verification-age` affects probe
evidence only. Gated promotion checks poll expiry again at the traffic write,
and a concurrent consumer change also rejects promotion. Inspect
`gregale queue status APP` and binding status, restore or resume the consumer,
resolve its errors or scheduler clock/polling problems, and retry after a fresh
healthy poll. A healthy idle queue needs no delivered message. External pull
consumers retain partial coverage because their liveness is not observed by the
scheduler. Polling health establishes control-plane readiness; it does not
prove guest queue access or message delivery.

### Require consumer readiness per queue

A stored release policy can explicitly require scheduler readiness for individual
push queue bindings while retaining strict verification for other dependencies:

```sh
gregale app binding release-policy set public-api --scope production --mode enforce --expected-revision 0 --queue-require orders=consumer_readiness
gregale app binding check public-api --release v12 --release-policy --json
```

`orders` is the binding name. The requirement needs exactly one shared or
selected-environment binding, enabled push delivery, and a healthy consumer poll
no older than thirty seconds. Missing, ambiguous, disabled, pull, unobserved,
stale and unhealthy consumers block. The queue environment must be confirmed by
the server; older inventories cannot satisfy this requirement. JSON exposes
`queue_requirements` and `required_queue_evidence`. A satisfied readiness policy
returns `passed` with `coverage: partial` and a `queue_connectivity_unverified`
warning. It establishes scheduler readiness only; it sends no messages and does
not establish guest connectivity or end-to-end delivery. Unlisted queues and
unsupported outbound bindings still block stored enforcement. Probe age never
extends the separate queue-poll deadline, which promotion checks under its
transaction locks.

Use `--wait` to observe the next scheduler poll for an active consumer whose
required readiness is stale or unobserved. Waiting sends no messages and does
not resume paused consumers or repair missing bindings.

Use `--queue-require NAME=consumer_readiness` repeatedly (up to 32 bindings) to replace the complete
queue requirement map. Updates require the observed policy revision; use
`--clear-queue-requirements` to explicitly remove all queue requirements.
Existing storage requirements must be retained with `--object-storage-require`
or explicitly cleared. CLI and SDK writes use the dedicated
`PUT .../bindings/release-policy-with-queues` route, so older servers return 404
before mutation. Older write routes reject implicit removal of these
requirements. A local `bindings check --queue-require ...` reads the same
readiness evidence without changing the stored policy.

## Prepare dependencies for an enforced release policy

To prepare all dependencies for an enforced release policy with one command,
preview the plan and then run it for the same candidate:

```sh
gregale app binding verify public-api --release v12 --release-policy --dry-run --json
gregale app binding verify public-api --release v12 --release-policy --wait-timeout 5m --json
```

The policy need not contain storage requirements. The plan covers services,
runtime PostgreSQL, object storage, configured outbound integrations and required
queue readiness. Each entry exposes an `action`: `probe` for missing, failed,
stale or insufficient evidence; `reuse` for current evidence; `wait` for an
already-pending compatible probe; `observe` for queue readiness; `skip` for disabled,
release-only or other-scope bindings; and `blocked` for unsupported or invalid
configuration. `observed_check` contains initial findings, while `plan_blockers`
explain conditions that stop the whole batch before tasks. A dry run reports
`status: planned` and `passed: false`; it performs no probes or provider calls.

If any scheduled storage operation writes, add `--object-storage-write` after
reviewing the plan. Without that consent, no part of the batch runs. Current
write evidence can be reused without writing again. The storage requirement and
binding permission select listing or upload/cleanup, with download for a
read/write canary. Unlisted write-only storage bindings also need an explicit
write canary because their credentials cannot perform a listing.

The command pins the deployment, scope and original policy; reconfirms selected
binding configuration before each task and after each successful probe; and
stops on failures or observed configuration changes. It finishes with fresh
policy-aware preflight, observing pending probes, required queue polls and
supported runtime/ACK progress under one overall `--wait-timeout`. Paused or
missing consumers and other terminal failures stop immediately. Reused evidence
that expires before the final check cannot pass. Queue coverage remains partial,
and canaries do not establish resident application adoption. JSON retains the
plan and completed results alongside `preflight` when later work fails. Deadline
errors use `error_code: verification_timeout` and retain any admitted task ID.

The final result is an observation; traffic promotion rechecks policy and
evidence under its transaction locks.

Human output identifies blockers and remediation. JSON includes `passed`, the
applied policy, scope and deployment, per-binding coverage, runtime counts,
inventory issues and stable blocker codes such as `verification_expired`,
`verification_scope_mismatch`, `runtime_stale`, `rotation_pending` and
`verification_unsupported`. On a control-plane host that does not run binding
probes (deployment-attached app tasks disabled), missing or stale evidence is
reported as `verification_unavailable` instead of asking for `bindings verify`;
`--allow-unsupported` waives it like an unsupported probe, and a recorded
failure still blocks. This is a point-in-time policy result from a
best-effort projection. It does not prove application readiness or resident
credential use; object-storage evidence covers bucket-list read access only.

## Verify outbound integrations

Customer-sealed outbound integrations can opt into verification by declaring a
provider endpoint safe to call:

```sh
gregale app binding probe-policy set INTEGRATION_ID --path /health --method GET --expect-status 200
gregale app binding verify public-api --release v12 --outbound INTEGRATION_ID
gregale app binding check public-api --release v12
gregale release traffic promote --app public-api --release v12 --require-bindings
```

The method must be GET or HEAD, the canonical path must fit the integration's
route policy, and the expected status must be 2xx. This declaration sends no
provider requests. `bindings verify --all` includes configured outbound probes;
use `bindings probe-policy INTEGRATION_ID --delete` to remove a policy. Calls
use the candidate task's workload identity through Gregale's outbound gateway, with normal
credential injection, route restrictions and admission budgets. Probes bypass
response caching and never include provider bodies, headers or credentials in
reports. Evidence becomes stale when credentials, probe policy, binding policy
or integration configuration change. A failed configured probe remains a
blocker even with `--allow-unsupported`.

Probes use normal TLS verification. Operator-environment credentials, missing
probe policies and installations without an outbound probe gateway keep
unsupported coverage. A successful probe establishes only the configured
endpoint.

## Verify a binding with a canary

`gregale app binding verify <app> <service>` runs a platform-owned HTTPS canary
inside a disposable task guest attached to the caller's deployment. Use
`gregale app binding verify <app> --all` to check every declared service before
switching the caller to HTTPS-first transport. The all-bindings form also
checks managed PostgreSQL and object-storage bindings in the scope selected for manual app tasks,
reported as `verification_scope` in the inventory. Other resource scopes and
unsupported binding families are not probed by this command. The form reports
each result and exits nonzero if any check fails. Both forms check the
`.internal` DNS alias, verify the gateway certificate against the
workload-scoped CA bundle, and exercise the same binding and target
authorization path as a real request. The final routing check only consults
the healthy endpoint registry: it does not wake an idle target or invoke its
handler. The probe has no HTTP fallback.
By default the generated URL is the HTTP endpoint. The `_HTTPS_URL` companion
stays available, and a caller can opt into HTTPS as the canonical `_URL`. The alias is never a public ingress
hostname.

To test an app's managed PostgreSQL binding, run
`gregale app binding verify <app> --postgres DATABASE_URL` with the binding's
environment key. The CLI confirms that the key belongs to a managed database
binding, then starts a bounded platform task in the app's live deployment. The
task reads the injected URL inside the guest, checks URL configuration and
database connection, and runs only `SELECT 1`. It reports the environment,
configuration, connection, and query stages; the URL, host, username, password,
and database response are never included in the report. This confirms basic
connectivity and query permission, not application-specific schema readiness.

Platform service, managed PostgreSQL and object-storage canaries admitted through the app-task
API carry a server-selected binding revision pin stored atomically with the
task. Inventory derives typed outcomes from the durable task result, without
returning its raw output, free-form details, or errors. A malformed, truncated,
pending, cancelled, or unmanaged task cannot supply a passing result. The
latest admission wins even when an older task completes later. A caller
deployment change, binding replacement, credential generation change, or app
runtime-configuration change marks prior evidence `stale`. Unsupported or
unrecorded results remain `unknown`.

`gregale app binding verify <app> --object-storage GREGALE_S3_ASSETS` checks the
six injected S3 environment variables and makes one signed, path-style
`ListObjectsV2` request with `MaxKeys=1`. It has a five-second deadline, does
not retry or follow redirects, and caps provider response XML at 64 KiB.
Empty buckets pass. The report separates environment, configuration,
connection, authorization and bucket access, and excludes endpoint URLs,
credentials, object names and raw provider errors. It never writes objects;
a pass establishes bucket-list read access, not write permission or successful
object downloads. `HeadBucket` alone would only check Gregale gateway access,
without testing the storage provider.

Object-storage evidence uses internal binding and latest rotation identities,
plus the app configuration stamp. Credential cutover invalidates older evidence
even before rotation stamps the app configuration. Rotation cleanup preserves
that identity. Object-storage metadata and evidence require `storage:manage`
or admin, matching the existing compute-binding inventory permission boundary.

The CLI rejects a passing result when task admission selected a different
deployment or scope from the inventory selection. Batch results include the
actual task scope so a deployment change cannot silently mix environments.

`bindings verify --all` reports every inventory binding. Unsupported queue probes
and outbound bindings without probe configuration have explicit reasons;
disabled bindings and resources outside the deployment scope are marked skipped.
JSON includes `total`, `checked`, `passed`, `failed`, `unsupported`, `skipped`,
`not_checked` and `coverage` (`complete`, `partial` or `none`). A supported probe
failure, incomplete inventory, unknown binding type, unfinished probe or absence
of supported probes returns nonzero. Known unsupported bindings retain partial
coverage even when supported probes succeed with exit status 0. Use `bindings
check` to enforce coverage and its explicit unsupported waiver policy.

Service probe evidence includes private dependency revisions for target identity,
service authorization, project preview policy and test-run membership. Changes
invalidate service evidence and binding-gated promotion approvals together. This
conservatively invalidates service probes across the account; target metadata and
revision tokens stay private. Run verification again after such changes.

Verification is point-in-time connectivity from a fresh task guest, not an
acknowledgement that resident application VMs adopted current credentials.
The GET inventory remains read-only. Explicit `bindings verify` tasks can
consume compute, establish database connections and issue bucket-list requests; service canaries do not
wake targets or call customer handlers.

## Managed binding application adoption

Bindings inventory reports `application_adoption` for PostgreSQL and object
storage. It maps the binding's managed secret versions to authorized resident
main/sidecar workloads and shows separate guest reload and application counts
(`current`, `failed`, `stale`, `unknown`). Counts represent workload/secret
pairs. PostgreSQL expects its connection secret; object storage expects the
endpoint, region, bucket, access key, secret key and addressing style metadata.
Workload grants determine which of those secrets require acknowledgement.
Task guests, jobs, mirrors and stopped/parked instances are excluded. JSON
includes each target's deployment, runtime, workload, key and observed versions.
It contains no credential values, hashes or private binding owner IDs.

To require application acknowledgements during a rollout:

```sh
gregale app binding check public-api --release v12 --require-application-ack --json
gregale release traffic promote --app public-api --release v12 --if-serving v11 --require-bindings --require-application-ack
```

Strict mode requires current version-bound acknowledgements from every
eligible authorized resident workload of each PostgreSQL/object-storage
binding in the candidate scope, including a resident candidate target for
each binding. Enable the workload's existing secret reload/application
acknowledgement contract, start the candidate and wait for it to apply and
acknowledge the current secrets. Missing, stale, failed, unsupported and
invalid observations block. Current-version reload failures also block.
Main workloads and long-running sidecars can independently opt into reload
and application acknowledgements in the same deployment. Each workload is
evaluated against its own secret grants and image reload opt-in. Main receives
no implicit secrets when companions are declared; explicitly grant its binding
secrets through `env_secrets`. Main acknowledgements use the standard endpoint;
sidecar acknowledgements include their stamped `?workload=<name>` identity.
An older valid guest projection remains separate from a newer application
receipt. Application receipts have no probe-age expiry; a changed secret
version makes an old acknowledgement stale. The existing probe, rotation and
instance timestamp checks still apply. `--allow-unsupported` cannot waive
application adoption. The default policy remains unchanged.

Strict promotion uses
`POST /v1/deployments/{id}/promote-with-application-ack`, which always enforces
this policy even if its request field is omitted or false. CLI and Go clients
select it automatically for `require_application_ack=true`; Node/Python
clients expose `promoteDeploymentWithApplicationAck` /
`promote_deployment_with_application_ack`. Older servers return 404 before
changing traffic; do not fall back to ordinary promotion. Receipts echo
`require_application_ack`, and changes to secret versions, workload grants,
reload support or acknowledgements invalidate the check at the traffic write.

An optional adoption read failure produces incomplete/unknown adoption and a
sanitized warning; it blocks strict checks. Application acknowledgements are
self-attestations and do not independently prove readiness, ongoing health or
actual credential use.

## Stored binding release enforcement

Enable verification enforcement for an app's deployment scope after establishing
its stable serving deployment:

```sh
gregale app binding release-policy get public-api --scope production
gregale app binding release-policy set public-api --scope production \
  --require-verification --max-age 10m --expected-revision 0
```

Add `--require-application-ack` to require current managed-secret application
acknowledgements. Use the current revision from `get` for subsequent updates.
Unconfigured scopes remain off. Enabling the policy does not change serving
traffic or run probes. Verification age must be a whole number of seconds from
1s through 24h.

Admit new candidates with explicit zero traffic, verify the exact candidate,
then promote it or advance its existing canary. The server enforces the saved
policy on ordinary traffic PATCH, promotion and manual/worker canary advances,
including any deployment gaining redistributed traffic. Request flags cannot
relax the saved policy. Missing, stale or changed evidence returns a structured
conflict with binding blockers. Smoke tests remain diagnostic and do not count
as binding verification.

To admit a new canary under enforcement, use a custom ladder whose first stage
is 0%. The built-in presets start with positive traffic and are blocked at
admission. The zero stage keeps the stable deployment serving while you verify
the candidate; subsequent advances apply the stored binding policy.

To abort an active canary while enforcement stays enabled, first verify the
exact retained predecessor, then pin both deployments:

```sh
gregale app binding verify public-api --release v41 --all
gregale rollouts recover public-api --action abort --deployment v42 \
  --expected-predecessor v41 --reason 'Restore verified predecessor'
```

The predecessor must remain live and serving in the same deployment scope.
Recovery checks its current bindings, policy revision and evidence expiry in
the traffic transaction, restores it to 100%, and aborts the selected canary.
The JSON receipt includes both IDs, the committed restored percentage, binding
check reports and the audit ID. Automatic canary aborts from the circuit breaker,
alert demotion, stuck-rollout recovery, critical route health and an expired
progression-worker lease also check the restored recipient. Health and lease
recovery retain their existing safety conditions. Missing or changed evidence
leaves traffic unchanged; recovery does not run verification tasks implicitly.
Binding refusals can be retried with the same idempotency key after supplying
fresh evidence, while successful recovery responses are replayed.

Positive initial canary admission, legacy canary recovery,
unchecked historical rollback and project release graph switches currently fail closed in
enforced scopes when they would increase protected traffic. Graph-wide checked
activation is not yet supported. During an incident, explicitly disable the
policy with a reason before using an unguarded recovery path:

```sh
gregale app binding release-policy set public-api --scope production \
  --mode off --expected-revision 1 --reason 'Restore stable deployment during incident'
```

Recover, then re-enable with the new revision. Every policy update has durable
history; disabling does not require binding/provider availability. Policy
changes and evidence are rechecked inside the traffic transaction, so a
concurrent update or expired observation cannot carry an old approval into a
traffic increase.

## Binding-checked service handoffs

A service rollout under enforcement keeps its predecessor serving while Gregale
checks the exact ready candidate. Missing evidence appears as bounded blockers
in `service_rollout_handoff.bindings_check`; supplying current verification lets
the worker retry without changing the policy. The routing transaction rechecks
policy, evidence and ready service capacity before publishing weights. Gateway
acknowledgement and request drain still determine handoff completion.

An exact abort uses the same pinned pair as canary recovery, including a retained
live predecessor at zero weight after service cutover. Verify that predecessor,
then request the abort and wait for the selected rollout:

```sh
gregale app binding verify public-api --release v41 --all
gregale rollouts recover public-api --action abort --deployment v42 \
  --expected-predecessor v41 --reason 'Restore verified predecessor'
gregale rollouts status public-api --release v42 --wait --timeout 10m
```

Service recovery returns 202 and `service_recovery` with the request UUID and
both IDs. This confirms acceptance only. `rollouts status` shows binding checks,
routing, missing gateway acknowledgements and drain progress. With `--wait`, a
service abort succeeds only at `rollout_state=aborted` and handoff phase
`complete`; promotion waits for its corresponding complete handoff. Status and
wait use GETs only, never run verification, and preserve the last status on a
timeout. Add `--json` for the exact deployment response. A restart of the
Gregale API resumes the durable request; a missing pinned predecessor cannot be replaced silently.

Completed deployments can be restored through an exact binding-checked rollback:

```sh
gregale rollback api --to v41 --expected-current v42 --reason "restore previous release" --wait
```

The accepted operation retains both deployments through readiness and binding
checks. The current release keeps serving while the target prepares at zero
traffic. If bindings are blocked, verify the exact target with the existing
`gregale app binding verify-all api --release v41` command, then inspect or resume
waiting for the accepted operation:

```sh
gregale rollback status api --operation OPERATION_UUID --wait
```

Both waits use read-only operation polling. A changed current release fails the
operation rather than selecting another deployment. Service completion includes
gateway acknowledgement and draining. A timeout keeps the last status and binding
blockers visible; an interrupt exits 130. Add `--json` for the operation receipt.

## Push queue binding completion canaries

A healthy poll proves consumer readiness. Select `handler_completion` when a release must also prove that a safe message reached the selected deployment and its handler completed successfully.

The deployment declares supported logical binding names in its `override_env`:

```json
{"GREGALE_QUEUE_CANARY_BINDINGS":"[\"orders\"]"}
```

The consumer must recognize `{"gregale_queue_canary":"v1","canary_id":"<unique UUID>","binding":"orders"}` and handle it safely. Return success only after the safe test handler finishes. Successful HTTP handling does not certify application-specific business effects. Pull consumers are unsupported. Shared legacy bindings support canaries in production/default only; other environments require an explicitly scoped queue binding.

```sh
gregale app binding release-policy set my-api --scope production --require-verification --queue-require orders=handler_completion --expected-revision 0
gregale app binding verify my-api --release v12 --release-policy --dry-run --json
gregale app binding verify my-api --release v12 --release-policy --queue-publish --json
gregale app binding verify my-api --release v12 --queue orders --queue-publish --json
```

Checked graph verification also accepts `--queue-publish`. It reviews every member and publication consent requirement before admitting messages. Dry runs never publish; compatible pending canaries are observed and fresh successful evidence is reused.

The API exposes `POST /v1/apps/{slug}/bindings/queue-canaries` with `deployment_id`, `binding`, `ttl_seconds` (1–600) and `publish_consent: true`; `GET` and `DELETE /v1/apps/{slug}/bindings/queue-canaries/{canary}` observe and cancel. `published` and `dispatching` are not passes. Confirmed success reports delivery and completion timestamps. Raw handler output and credentials are not returned. Cancellation revokes evidence even when an in-flight handler is finishing; CLI interruption or timeout requests cancellation.

Canaries use normal queue/runtime limits, retained revision pins and one execution attempt. They do not change release routing. Evidence expires at the original deadline or sooner under the policy's maximum age. Consumer or deployment changes invalidate it. Replay and stale acknowledgements cannot qualify a release.

The shipped `queue-worker` starter handles the reserved canary before business
jobs and rejects malformed or undeclared probes. Opt-in remains an exact
deployment override plus explicit publication consent; its README includes the
API setup and durable verification commands.

Ordinary durable dispatch does not perform a separate canary lookup. The existing
ledger ownership read also identifies registered canaries, and only those
invocations undergo full completion-canary validation. Caller-controlled source,
payload and headers cannot skip these checks; cancelled and expired canaries
remain registered until ordinary retention removes them.

## Adopt rotated PostgreSQL pools in application code

The Go, Node and synchronous Python SDKs provide driver-independent PostgreSQL
pool bindings with required readiness validation and transaction leases. Applied
secret ACKs follow validation; old pools drain after leased transactions release
their driver connections. These application checks do not replace platform binding
verification or release qualification. See the [runtime guide](postgres-pool-runtime.md)
and [pg.Pool example](../examples/postgres-pool-rotation/README.md).
