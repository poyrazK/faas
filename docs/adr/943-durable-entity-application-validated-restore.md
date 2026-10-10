# ADR-943: Application-validated durable entity restore

Status: local implementation, unqualified; default off.

## Context

ADR-942's read-only preview cannot establish application compatibility. Matching
schema numbers do not prove that the deployed handler accepts the exported data.
Restoring a candidate requires an application verdict without executing a normal
business transition or rewinding current delivery history.

## Decision

Add the operator gate `FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED=1`, requiring
the existing durable entity invocation preview. With this gate enabled, new owner
restores require an explicit `validation_deployment_id` and run application
validation under the restore's existing private claim. No operator setting is
enabled by this local work. All API writers must use consistent gate settings
before enforcing validation; an older/unconfigured writer can still accept the
previous unvalidated restore contract. Resolve outstanding unvalidated requests
before enabling the mandatory deployment-pin contract.

Add `POST /v1/apps/{slug}/entities/restore/validate` as a deploy-write/admin owner
execution surface with MFA, rate limiting, app enablement, execution-plan,
account-hold and active-tenant checks. It verifies source identity/checksum and
the current expected version before enqueueing an invocation. It returns a
boolean verdict, checked deployment ID and source/expected versions. Validation
is observational: it is not a reservation, signed permission or committed receipt.
The existing read-scoped `/restore/preview` remains metadata-only and does not
execute application code.

Use a distinct private guest path `/__gregale/entities/validate-restore`, distinct
protocol version 1 and event `validate_restore`. The guest receives verified
entity scope, request ID, pinned deployment ID, expected/source business versions
and only the candidate application JSON. No live state, alarms, outbox, claim or
bucket credentials are exposed. Response decoding accepts only
`{"protocol_version":1,"valid":true|false}` with a required boolean and central
response byte bound. Transition output, outgoing intents, missing/null verdicts,
unsupported protocols and malformed responses fail closed. Old business-event
handlers cannot silently treat this protocol as a normal invocation.

Go and Node SDK guest helpers execute synchronous pure application validators
and encode only a verdict. Application rejection exposes no private reason text.
Go errors and Node throws yield false; Node callbacks returning promises or any
non-boolean value cannot produce an affirmative verdict. Validation never rewrites
or migrates the candidate; restore commits its original bytes. A schema-aware
counter example validates the exact current schema using its existing validator.

Purity is the existing application callback contract, not independently enforced
guest isolation. The current runtime does not disable arbitrary application
network/database calls. Validators must perform no external I/O or effects. Normal
invocation rows, scheduling/wake resources, billing and execution retention still
occur. No entity transition, alarm, outbox intent or receipt is committed by the
standalone validation endpoint. A late validator result cannot publish entity state.

## Restore ordering and fences

The engine journals the validation deployment pin as part of the restore operation
fingerprint. It checks committed receipts before the expected version or validator.
An acknowledged/uncertain committed operation replays without calling the guest or
resolving a new deployment. Changing the pin under that committed request ID is a
request conflict. Legacy unvalidated fingerprints remain identical when the pin is
absent; the new field is omitted from serialization in that case.

For first execution, the engine holds existing private ownership, checks the
expected business version, and hands a copy of candidate data to validation.
Application rejection or validator failure aborts before immutable commit uploads.
Mutation of the callback's buffer cannot change the eventual restored state.
After acceptance, existing size/storage accounting, ownership expiry/takeover
checks and final manifest CAS govern publication. Current receipts, alarms,
outbox and delivery reservations survive. Expired or obsolete owners cannot commit
even if their validator accepted.

The API resolves and pins the selected live deployment before invoking it and
resolves the selection again after completion. A mismatch with the supplied pin
or a changed selection returns 409. Deployment routing in SQL and bucket state
publication are not one atomic transaction: deployment changes after the last
check are not fenced by the entity manifest CAS, and an ABA deployment selection
change is not detected. This guarantees validation by the named immutable
deployment, not that the deployment remains globally live throughout publication.
Avoid deployment changes during recovery, and retain schema-compatible readers.

False standalone verdicts return 200; rejection during restore returns 422
`durable_entity_restore_rejected`. Invalid validator responses return 502; existing
request/state conflicts, busy ownership, uncertain writes and deadlines retain
their recovery contracts. Responses are private/no-store. Acknowledged restore
audit metadata includes the validation deployment pin, never candidate data or
validator-private reasons. Audit remains best effort after bucket publication.

OpenAPI, embedded spec and Go/Node/Python clients expose the validation endpoint
and optional pin field. The pin is mandatory for restores when the operator gate
is enabled. Retry uncertain restore outcomes with the identical original request
ID, expected version, candidate and pin; changing any part starts a different
operation and cannot safely resolve the original outcome.

## Qualification

Written source cases cover verdict-only decoding, pure SDK callbacks, unsafe Node
versions, transport/pin preservation, disabled gates, first-execution revalidation,
receipt replay bypass, changed-pin conflicts, candidate buffer isolation,
application rejection and ownership takeover during validation. Tests, builds and
live provider/KVM qualification have not run here, per the testing-agent handoff.
Before release, qualify deployment drift, account/tenant execution gates, unavailable
validation routes, invocation timeout/late completion and uncertain commit replay.
No PR, deployment or production gate change is included.
