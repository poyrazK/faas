# ADR-202610171903: Organization application quota ownership

Status: accepted
Date: 2026-10-17

## Context

Shared application authorization and offboarding use the persisted app `org_id`
([ADR-202610171901](202610171901-organization-application-authorization.md),
[ADR-202610171902](202610171902-organization-offboarding-fences.md)). Application admission still
counted the creator's apps. Two developers could each consume an entire company
plan, while the same developer's company apps exhausted their personal quota.
Restore and project provisioning also used account-wide counts.

The resource inventory has grown beyond the original IAM-6 inventory: runtime,
metering, projects and app-bound data services still retain account references.
Reassigning `apps.account_id` alone would break child ownership, runtime lookups
and financial attribution. Synthetic human accounts would conceal those remaining
dependencies. This change makes the quota cutover explicit and protects erasure
while the remaining resource migration is incomplete.

## Decision

An application's persisted organization owns its application admission pool.
All members share the existing `api.Limits` production, developer and PR-preview
caps. Shared-org admission reads the persisted organization plan, ignoring a
caller's personal plan or supplied limits. Personal application admission excludes
shared-org apps, and unattributed legacy apps count toward their account's
personal pool. Personal callers retain the existing limits argument for current
internal workflows and compatibility; there is no new pricing or quota table.

PostgreSQL holds the legacy account lock, reads the organization with `FOR SHARE`,
then takes a transaction advisory lock keyed by organization across count and
create/restore. The shared row lock keeps the authoritative plan/status stable;
the advisory lock serializes different creators. Plan writers use the normal
conflicting organization row lock. This order also matches the existing
application-scope insert guard: a plan writer never holds the org row while
waiting for an app's quota lock. Both transaction orders and rollback have
explicit PostgreSQL tests. MemStore provides the same behavior under its mutex.
Project provisioning/reconciliation continues to target the personal pool;
project ownership is not migrated here. The personal production/developer count
methods used by project scan preflight, account responses and dashboard quotas
also exclude shared-org apps, so a read-side check cannot reject a reservation
that the personal admission pool would accept.

Deployed/evicted-cold apps count; deleted apps release their slots, and restoring
them must reserve a slot again. Developer and PR-preview pools remain independent.
Quota errors carry the authoritative plan, limit and observed count so the API
does not describe a collaborator's personal cap. Downgrades do not erase existing
apps; further admissions must fit the new cap.

Migration `20261018003100002` fills only missing personal app attribution and
adds an organization quota index. Existing shared attribution is retained. New
app insertion fills missing personal attribution and rejects a mismatched personal
organization. The backfill is forward-only, including on rollback.

Account deletion scheduling and hard purge refuse an account still referenced
by any shared-org app, including tombstones. The check holds the account lock
that serializes app creation and runs before any child deletion. REST deletion
returns 409 with the required storage/billing migration. A database trigger also
protects direct account deletion. This is a data-loss guard, not completion of
creator-independent resource lifecycle.

## Remaining cutover

Follow-up: [ADR-202610171904](202610171904-organization-resource-lifecycle.md) implements core
resource ownership, provider-qualified adoption and creator-independent erasure.
The following describes the boundary at this quota-only decision.

`apps.account_id` remains the legacy storage, runtime and metering reference.
Runtime configuration entitlements and usage billing retain their existing
account behavior. This change does not rewrite usage history, invoices,
provider customer/subscription IDs, app child account IDs or encrypted payloads.
An offboarded creator can still require migration assistance before account
erasure. The original acceptance scenario of deleting that account while all
company resources keep operating is **not yet met**.

The next ownership slice must migrate app child resources and runtime/metering
subjects together, preserve historical billing attribution, and prove company
operation after creator erasure. Jobs, projects/environments and other data
services need their own explicit migration coverage. Organization billing,
SSO/SCIM and service principals remain separate work.

## Rollout and acceptance

Apply the additive migration before the new API binary. Upgrade/drain all API
writers before relying on the pooled quota: old binaries still perform
creator-based admission. Do not perform manual `account_id` reparenting as a
rollout shortcut. A rollback retains the attribution repair and requires
reinstating the deletion guard before resuming personal-account erasure.

Acceptance covers MemStore/PostgreSQL parity, different creators racing for one
budget, create competing with restore for the last slot, committed/rolled-back
plan changes, personal/other-org isolation, separate developer/preview pools,
API limit details, and preservation of company state during blocked erasure.
No VM lifecycle implementation is changed; native runtime acceptance and a
production rollout are not implied.
