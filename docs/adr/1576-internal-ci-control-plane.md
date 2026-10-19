# ADR-1576: Internal CI control plane with compute admission closed

- Status: accepted for internal metadata qualification; not launched
- Date: 2026-10-15
- Extends: ADR-099 (Jobs), ADR-982 (apid domain ownership)

## Decision

Introduce a native CI control plane in `pkg/ci` and its internal HTTP surface in
`pkg/apid/ciapi`. apid owns definition and run intent. This release installs no
executor, background dispatcher, scheduler notification, GitHub trigger, or VM
integration. It does not change Job resources or VM lifecycle behavior.

`FAAS_CI_INTERNAL_ENABLED` defaults to `0`. Access additionally requires a
boot-time `FAAS_CI_INTERNAL_ACCOUNTS` UUID allowlist, an authenticated active
account, admin authority, and the existing MFA middleware for POST/PUT routes.
An empty or malformed allowlist fails closed. Unlisted or disabled requests
return 404 before reaching CI storage. There is no customer-plan entitlement.

Definitions use strict version-1 YAML/JSON with a repository descriptor,
digest-pinned OCI images, named shell steps, bounded timeouts, explicit job
dependencies, and workspace-relative artifact selectors. Anchors, aliases,
merge keys, duplicate keys, unknown fields, cyclic dependencies, inline secrets
configuration and remote actions are unsupported. Commands are stored as
definition content and are never run on the control plane or in the CLI.

Normalization supplies explicit timeouts, sorts unordered dependencies and
artifact selectors, and produces canonical JSON and a SHA-256 digest. Revisions
are immutable and deduplicated by account, pipeline name and content digest.
Each run selects an exact revision UUID and a full lowercase commit SHA. Its
execution plan is copied into the run receipt independently of later revisions.

Admission records `blocked` with `compute_unavailable`. This is retained intent,
not a queued Job or a promise of future execution. The only subsequent state
is `cancelled`. Database CHECK constraints prohibit `running` or `succeeded`.
Blocked runs must not execute automatically when future compute support lands.
A future execution release must define fresh source verification, compute
admission, trust, secret, resource, and accounting contracts.

## Persistence and recovery

Three account-owned tables retain revisions, runs, and ordered lifecycle events.
All foreign keys include account ownership and cascade on account deletion.
Queries are generated through the dedicated SQLC package; its compile-time
schema projection mirrors migration `20261016175839285`.

Run submission requires a stable account-scoped Idempotency-Key. Identical
retries return the original receipt, including after cancellation; conflicting
revision/commit inputs return 409. Account row locks serialize PostgreSQL
admission and metadata ceilings. Run row locks serialize cancellation, which
commits its event in the same transaction. Receipt recovery precedes quota
admission so a full allowance cannot prevent a retry returning its original run.

All bounds live in `pkg/api/limits.go`. There are at most 64 revisions and 256
retained runs per account in this qualification release. Cancelled runs retain
their slots and history; no automatic pruning or retention promise is added.
Lists omit command bodies and full execution plans. Audit events include only
resource identifiers, content digests, and lifecycle reasons.

## Internal tooling and future integration

`gregalectl ci validate` and `plan` work offline. Definition and run operations
use the authenticated operator session and the same internal API gates. No
customer CLI command, public SDK method, or public OpenAPI contract is added.

Future execution should map a CI job to a leased Gregale Job task through the
scheduler owner. apid must continue to record intent rather than call vmmd or
run shell commands. GitHub Actions runner support is a separate adapter and
does not change the native pipeline format into Actions YAML.

## Evidence

Portable tests cover strict parsing, canonical plans, internal gates and offline
planning without command execution. Memory/PostgreSQL parity tests cover
immutable revisions, concurrent idempotency, cancellation/event atomicity,
account isolation, metadata ceilings, restart reads and account deletion.
HTTP acceptance verifies admin-only internal admission, default-off hiding,
blocked receipts and absence of created Jobs. No KVM or live execution claim
is made by this control-plane release.

ADR-1571 adds a separate durable execution ledger for synthetic engine
qualification. It preserves the metadata-only API and the inert receipt
contract described here; no production compute integration is installed.

ADR-1572 adds separately gated account-owned GitHub source admission and
blocked-admission Checks. Its default-off triggers create blocked metadata
receipts and preserve the compute boundary.
