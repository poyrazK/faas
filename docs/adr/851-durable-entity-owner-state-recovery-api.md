# ADR-851: Owner state export and restore API

Status: local implementation, unqualified.

## Decision

Expose ADR-850 through owner-authenticated `GET /v1/apps/{slug}/entities/export`
and `POST /v1/apps/{slug}/entities/restore`. Read scopes (`apps:read` or admin)
authorize export; deploy-write scopes authorize restore. Both use existing MFA
and rate-limit wrappers, private/no-store responses and operator app enablement.
Neither uses generic HTTP response-cache idempotency.

Export resolves the same immutable scope selectors as inspection and allows
diagnosis after plan downgrade or tenant suspension. It returns sensitive
application data, with no guest dispatch, ownership acquisition or bucket writes.
Restore applies existing execution plan, workload, account-hold and active-tenant
mutation rules. The body is bounded by the central invocation byte limit and
decoded strictly. Source identity must exactly match the authorized resolved scope.
An existing-only claim acquisition prevents creating absent or version-zero state.
The lease and final commit CAS fence takeover and concurrent publication.

Restore requires a stable request ID and positive expected current version. Retry
uncertain outcomes with the identical body and ID, including the original expected
version. Receipts decide replay before the expected-version check. New stale
operations return 409 `durable_entity_restore_conflict`; changed bodies under a
committed ID return the existing request-conflict error. Missing committed state
returns 404; active/lost ownership or uncertain writes return 503; deadlines return
504. Current delivery history and exhausted retries remain intact.

An acknowledged restore emits a best-effort audit event containing scope/version
metadata and replay status, never application data, checksums or request payloads.
Audit publication is not atomic with bucket commit and can be absent after a lost
acknowledgement. Replaying may emit another audit event.

OpenAPI and its embedded copy describe both operations. The Go source-mirrored
client and generated Node/Python clients expose matching methods. Node convenience
wrappers reject unsafe business versions before sending and flag unsafe response
versions even if the restore committed; retain the original request for recovery.
Python integers and Go uint64 retain full business-version precision. Application
JSON also needs serialization fidelity: do not modify the exported envelope;
JavaScript numeric precision and reformatting numeric JSON can invalidate checksums.
Use string-encoded application numbers where exact SDK round trips matter.

## Qualification

Source cases cover owner transport, read-only export, no-store responses, stable
restore replay, stale expected versions, scope mismatch and read-key mutation
denial. SDK cases cover transport and missing/unsafe identity/version checks.
Tests/builds/live-provider checks remain delegated and have not run here. No PR,
deployment or feature-gate enablement is included. Before release the testing agent
must run these cases and qualify held/suspended scopes, missing/corrupt state,
uncertain publication retry, takeover during upload and complete storage budgets.
