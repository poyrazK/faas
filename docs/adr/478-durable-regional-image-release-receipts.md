# ADR-478: Durable regional image release receipts

Date: 2026-10-04
Status: Accepted; implementation in progress.

## Context

Spec §4.2 assigns deployment intent to apid and retains ordinary request replay
for 24 hours. ADR-476 supplies verified targets for all selected regions, and
ADR-477 fences deployment mutations by cell and app UUID. A coordinator can
lose the response after a region commits its deployment. Retrying after the
ordinary replay window must not create a second deployment or supersede a
newer release.

## Decision

Regional apid adds `PUT` and `GET` at
`/v1/cells/{cell_id}/apps/{app_id}/image-releases/{release_id}`. The release ID
is a canonical nonzero UUID chosen once by the global coordinator. PUT accepts
one digest-pinned image reference. It reuses the regional image deployment
pipeline and the app's regional configuration. Source builds, configuration
replication and global publication remain separate protocols.

A regional receipt permanently identifies `(app UUID, release UUID)`, its
cell, authorizing local account, image reference and accepted deployment UUID.
The app lock, deployment insertion, activity outbox and receipt insertion are
one transaction. The lock also checks the expected account and organization,
so a change of app ownership between preflight and insertion rejects the
mutation. The existing VM admission and plan limits are unchanged.

A matching retry returns the recorded deployment. A different image conflicts
and creates no work. This endpoint bypasses the ordinary 24-hour response cache; its durable ledger
is authoritative for matching retries, body conflicts and collected deployments.
Deployment garbage collection clears their deployment pointer, preserving a
tombstone: subsequent PUT returns 410 instead of repeating the operation.
Receipts cannot be rewritten or deleted while their app identity exists.
Hard app deletion removes its receipts; public app creation never reuses UUIDs.
Downgrade is refused while receipts remain populated.

Authentication, scopes, MFA, verified email, cell identity and current personal
ownership are checked before replay. PUT accepts personal regional account keys
with admin or deploy-write scope. GET also accepts apps-read scope. Neither
accepts sessions, shared organization, app-scoped or platform-tenant principals
in this adoption phase. A receipt from a previous owner is hidden after transfer. Its authorizing owner
is historical metadata and survives deletion of that account while the app exists.
All SDK receipt requests refuse redirects, including custom HTTP clients.

GET reports the committed receipt and the deployment's current regional status.
A receipt proves acceptance, not global readiness, capacity allocation, serving
eligibility or ingress publication. It stores no credential or inline secret.
The global coordinator will persist selected target snapshots and use these
receipts to reconcile uncertain submissions after process or network failure.
The default remains serving from all selected healthy, eligible regions.

## Validation

Exercise concurrent submissions, lost-response retries beyond ordinary replay,
conflicting input, failed deployment retention, deployment collection, immutable
SQL identities, cell/database scope and downgrade guards. Verify atomic rollback
of deployment, receipt and activity records. Pause after app lookup, rename or
transfer/delete the app, and prove only the authorized UUID can receive work.
Test two independently authenticated regional HTTP APIs and all SDK transports.
Native VM, quota allocation, regional outage and global ingress acceptance remain
required for the full multi-region feature.
