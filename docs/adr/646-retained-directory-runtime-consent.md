# ADR-646: Retained directory runtime consent

Date: 2026-10-06
Status: Accepted; runtime activation remains frozen.

## Context

ADR-645 supplies a regional consent receiver and explicit credential-free quota
delivery. Automatic delivery still needs durable directory consent intent and
setup recovery. Image release delegation has different authority and must remain
separate. Customer admin credentials must not enter either journal.

## Decision

Retain public `runtimequota.Consent` under its explicit original UUID, issuer,
global app/owner and complete personal regional binding, including HTTPS origin
and registry generation. Retain the original global slug, expiry, directory
creation time and first observed regional creation/revocation times. Pending
intent commits before regional PUT. Same-ID retries never extend expiry, change
scope or resurrect revoked consent; renewal requires an explicit new UUID.

The directory supports setup, inspection and revocation at
`/v1/global-apps/{slug}/runtime-consents/{id}`. Setup names an explicit region and
microsecond expiry plus a request-only regional admin key. MFA, verified email,
personal directory principal and deploy-write scope protect mutations. GET uses
the existing read scope. Shared organization consent remains unavailable.
The Go client refuses redirects. CLI setup/show/revoke uses `--consent`; setup
requires a stable `--expires-at`, and credentials come from `--regional-token-env`.

The journal serializes on the original global app. Preparation and activation
recheck current active verified account, personal owner, live app, pinned directory
issuer and exact selected enabled target. SQL checks expiry after all account,
app, registry and target lock waits, including duplicate setup and dispatch
checks. Pending, active and revoking unexpired records count against the 32-record
per-app bound in `pkg/api/limits.go`; lifetime remains ADR-645's six days.
An active observation permits future directory dispatch only while those live
checks still pass. Receipt metadata alone never proves native execution or drain.
Explicit credential-free delivery checks the retained active consent against the
allocation's complete binding before issuance and immediately before HTTP I/O.
Pending or revoking intent cannot start a new dispatch. An already in-flight
grant can race revocation; the regional journal serializes its acceptance with
the consent tombstone. Retirement delivery remains independent of consent.

Regional replies are bounded and reject duplicate/unknown/trailing fields;
their nested scope must be canonical and exactly match retained intent. The
original regional creation timestamp is immutable once observed. A lost reply
retains pending or revoking intent for retry. Transport errors omit remote bodies
and credentials. Revocation commits local revoking status before regional DELETE,
blocks new directory dispatch immediately and uses the original endpoint after
detachment, registry withdrawal or global app collection. Only an exact regional
revocation response establishes the observed tombstone. Expiry blocks grants
without asserting a remote tombstone or releasing global quota.

SQL guards preserve original identities and timestamps, enforce transitions and
capacity bounds, reject nullable JSON scope and refuse deletion, truncation or a
destructive downgrade with retained evidence. App/account/target collection does
not remove consent. Runtime heads remain frozen. The durable original-envelope
quota outbox and cancellable worker are next; this change does not enable one.

The full multi-region goal still includes native inventory/source authority,
capacity reconciliation, snapshot shape/capture, shared authorization/global
billing, independent regional provisioning/stores/replication, cached ingress
from all selected healthy eligible regions, and dedicated native Linux x86_64
KVM metal/leakcheck/recovery acceptance. The internal nested GCE node remains
diagnostics only.

## Validation

Exercise memory and real PostgreSQL contracts, concurrent bounds, immutable
retry/renewal/metadata, owner suspension, registry changes, retained restart and
collection, SQL identity/null/transition/retention/downgrade guards. Observe real
account lock waits crossing expiry for fresh and retried preparation, activation,
dispatch checks and direct SQL activation. Loopback TLS checks pending-before-I/O,
lost setup/revoke replies, exact original endpoint recovery and strict response
decoding. The actual regional apid receiver and directory API prove setup,
credential-free grant delivery and retained revocation without activating heads.
CLI tests prove scoped paths, stable IDs/expiry and absence of credentials in
output. Run race tests, platform lint, sqlc consistency and applicable policies.
