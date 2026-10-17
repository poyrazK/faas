# ADR-1349 · Product Control complete internal pilot

- **Status:** accepted for internal preparation; deployed pilot pending
- **Date:** 2026-10-17
- **Decision:** Add an opt-in version-two pilot manifest covering the complete
  three-customer journey, including the customer Updates inbox. Keep the
  version-one ledger workflow and the existing default-off service gate.

## Complete journey contract

Version two requires three baseline customers on two plans, each with a selected
successful operation, a local downloaded CSV and reviewed SHA-256, and a journey
linked to help about that same baseline run. Empty CSV files are rejected.
All seven stages must have observed evidence: sign-in, paid feature, usage,
plan change, help request, owner response and `inbox_update`. A missing stage
leaves the journey incomplete. Blocked attempts remain recorded even if a later
observation completes the stage. Version one continues to permit ledger-only
verification and optional help/journey records.

An observed inbox stage references the actual `api.ProductNotificationPage`
JSON captured in that customer's browser. The owner verifier does not call the
private customer API or exchange the customer's credentials. Pages must match
the selected app and tenant, have canonical account/app identities, the shared
workspace scope, the existing thirty-day window and notification bounds, and
valid item references, revisions and read versions. The account ID is checked
for shape; the owner doctor response has no account identity to cross-check.

Across the journey's observed pages, the selected successful work notification
must match the retained feature/outcome/time, and the selected help notification
must match the latest observed kind/status/update time. Source timestamps use
the projection's microsecond precision. Missing or older source evidence leaves
the journey incomplete. Pages can be recorded separately for pagination. The
existing 128 KiB notification response bound applies to each captured page.

Unknown fields, duplicate JSON members, trailing JSON, foreign scope and malformed
notification records are rejected. Errors exclude raw page contents. Reported
evidence contains only paths, hashes and byte counts; it never copies inbox JSON,
CSV contents, credentials or help text. A changed artifact blocks the comparison
before parsing. Blocked inbox observations can instead reference reduced failure
artifacts within the existing four MiB journey bound.

## Evidence and execution boundary

The report version reflects the manifest version. Existing exit semantics remain:
one for mismatched expectations/artifacts, three for incomplete requested
journeys, zero for matched requested comparisons and recorded requested journeys.
All reports retain `qualification_state: unverified`, including an explicit
unverified inbox visibility/read-recovery check. Operator-provided JSON cannot
establish a real customer session, read recovery, isolation or native execution.

Run the real three-customer trial only against the explicitly selected internal
deployment and qualified native Operations lane. Record source/build provenance,
actual browser observations, downloaded output and friction before considering
another feature. Inbox drills cover explicit workspace-shared read/unread,
new-source unread, stale updates, lost acknowledgements and current authority.
Retain separate deployment and qualification evidence; do not promote local
fixtures or this verifier's exit code into live acceptance.

Preparation does not deploy, change flags, send invitations/email or mutate
customer work, billing or access. Missing target/access leaves live scenarios
pending. Frozen runtime artifacts retain their original source fingerprint;
new verifier tooling receives separate source/build evidence.

See [ADR-1347](1347-product-control-internal-pilot-evidence.md),
[ADR-1348](1348-product-control-customer-notifications.md) and the
[pilot guide](../../examples/customer-operation-export/product-control/README.md).
