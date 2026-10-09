# ADR-834: Lifecycle review at the production traffic boundary

Status: Accepted

## Context

ADR-832 and ADR-833 enforce lifecycle declarations and successor receipts during
canary advances. Initial activation, ordinary cutovers, manual traffic changes,
service promotions and checked rollback can change production traffic without
using that endpoint. Lifecycle promises must survive those transitions too.

## Decision

Use the existing app route gate mode for lifecycle declarations on every
production traffic increase. Run the shared declaration comparator against the
candidate, every positive serving baseline and the retained removal-policy
baseline before traffic mutations. Dark activation and traffic decreases remain
available; any sibling gaining the redistributed traffic needs its own review.
The saved route-requirements evidence gate continues to apply to canary advances.
This decision extends lifecycle enforcement, not automatic requirements gating.

Create internal, transaction-local authorizations after locking the policy and
captures. A deployment database trigger rejects production increases without a
current authorization in enforce mode, including direct SQL and unintegrated
worker paths. Pin scope, database configuration, gate and intent revisions,
removal policy and capture identities, including replacement timestamps. Recheck
accepted receipt expiry/invalidation at the traffic write. A stale authorization
cannot be refreshed merely by copying a later input snapshot.

Reuse successor receipts only when their additional database configuration
snapshot matches current app/account settings and edge rules. The database
binding includes all inputs to effective app limits and conservatively includes
full manifest and rule rows. Existing receipts without this binding are
invalidated during migration. Canonical host configuration remains an operator
setting; changing that setting requires fresh successor review. Receipt creation
and canary evaluation still use the configured host fingerprint.

Record successful production traffic reviews in `production_lifecycle_reviews`,
including report-mode findings, consumed receipt IDs and recovery status. Direct
SQL in report mode records unavailable review rather than claiming a passing
comparison. Rejected transactions do not commit traffic or review rows.

Permit lifecycle recovery authorizations only from validated canary/service
abort and automatic incident rollback code. These restore a selected predecessor
and preserve the existing ownership, readiness, lease, binding and route-removal
checks. Ordinary and checked rollback still require lifecycle review. No HTTP
field grants recovery authority.

## Consequences

Choosing enforce mode now requires complete captures before initial positive
activation. Owners can stage at zero traffic, capture and approve, then promote.
Missing captures, earlier sunsets and premature removal block production
increases across rollout paths. Unsupported database mutation paths fail closed.
The append-only review history adds a retention concern consistent with other
traffic audit histories; it is removed with the app/deployment. Cross-app and
custom-domain successor compatibility remain separate work.
