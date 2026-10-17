# ADR-1470: Durable Linear Issues connections

Date: 2026-10-14

Status: Accepted

## Context

ADR-1469 verifies Linear personal API keys and seals them with account, app and
provider identity. Native operations need a durable owner-managed connection.

## Decision

Store one Linear connection per app in PostgreSQL, with a monotonically increasing
revision. Serialize changes on the owned, live app row and require the caller's
observed revision. Store only age ciphertext and verified workspace/team/viewer
UUIDs. Return metadata only; never return ciphertext or keys. App/account deletion
cascades the record. Disconnect erases the ciphertext and provider identity while
retaining a revision tombstone, preventing stale reattachment.

Expose GET/PUT `/v1/apps/{slug}/issue-tracker-linear-connection` to the app owner.
Changes require deploy-write and session MFA. Connecting requires Issues
entitlement and deploy eligibility, performs bounded provider verification, and
fails closed if sealing is unavailable. Inspection and disconnection remain
available after an Issues plan downgrade. Disconnect never contacts Linear or
requires a sealing key. Audit changes with app, decision and revision only.

The CLI command `issues tickets linear-connection` inspects metadata by default;
`--connect --team UUID` reads a personal API key from `GREGALE_LINEAR_TOKEN` or the
environment variable named by `--token-env`. `--disconnect` erases the stored
credential. Both read the current revision before submitting a change.

## Consequences

The connection does not enable automatic synchronization or create/update Linear
tickets. Those native operations will use the connection revision and reverify
identity before dispatch. OAuth callback, refresh and revocation support remains
a separate increment. Existing explicit CLI token operations continue to work.

## Validation

PostgreSQL-backed API tests verify default metadata, ingest-token denial,
encrypted identity binding, stale revisions, one winning concurrent update,
sanitized provider failures, plan-gated connection and offline post-downgrade
credential erasure. CLI and SDK contracts verify revision and decision preservation.
