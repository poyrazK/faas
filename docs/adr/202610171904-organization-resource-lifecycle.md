# ADR-202610171904: Organization application resource lifecycle

Status: accepted
Date: 2026-10-17

## Context

ADR-202610171903 pooled admission but retained a human account as the application's
storage and execution subject. Erasing that identity could cascade company
configuration, and runtime policy and future usage still followed its plan.
Changing storage UUIDs would invalidate accepted work, runtime identities and
sealed bindings. Human authentication and resource ownership must separate.

## Decision

The persisted app organization owns application policy and lifecycle. Existing
`account_id` values on core app resources are immutable storage namespaces;
they are no longer human foreign keys. `resource_namespaces` contains only
opaque UUIDs, with no login credentials, email, plan or synthetic account.
Authentication continues to query real accounts exclusively. Core app children
retain their existing keys and encrypted bytes. Personal namespaces are erased
with their resources; a namespace used by a shared app survives human erasure.

Runtime policy reads the app's organization, independently of the creator's
membership or account status. Human MFA, email verification, actor attribution
and credential revocation continue to use the real caller. Shared app usage
created after cutover belongs to the organization billing subject. Existing
usage, delivery receipts and provider IDs keep their original attribution;
replaying an existing minute must not move it to another subject.

Admission and observation transactions lock the persisted billing owner before
reading its policy. Personal and unadopted apps retain the account row fence;
adopted apps lock the organization and recheck the app's namespace. The optional
creator join in the runtime projection is never a row-lock target. Company
log retention follows the organization plan after creator erasure. Logs whose
app is gone retain their existing account-plan fallback and one-day orphan floor.

Core lifecycle coverage includes apps, deployments, builds, snapshots, env,
sealed app secrets, signers, registry credentials, invocations and ordinary
runtime telemetry. Projects and app-bound data services require explicit
ownership coverage. Erasure fails atomically when an unsupported resource still
depends on the identity. A shared organization's sole owner must transfer
ownership or delete the organization before deleting their account, even when
no other members exist.

Internal CI pipeline, run, execution-profile and automation metadata require
separate ownership coverage. Creator erasure blocks while those unreviewed
records still reference the identity.

Rehearsal policies and fixture/runtime journals likewise remain unreviewed.
Their account references block erasure, including completed fixture evidence
that the rehearsal service could otherwise purge.

Internal Product Control revisions, customer/billing records, onboarding,
notifications and email preferences also require separate ownership coverage.
Their account references block erasure while that coverage remains unreviewed.

MCP readiness incidents, maintenance windows and operator-action receipts also
retain account references. They block identity erasure until their organization
ownership and recovery paths receive separate review.

Custom SLO definitions, synthetic checks and account notification channels also
require separate ownership coverage. Their creator account references block
identity erasure, including channels that otherwise cascade with the account.

Selective data-repair operations and their rehearsal journals retain their
existing account references and erasure blockers. Their organization ownership,
approval and recovery paths require separate review.

All writers that enter `deleted_pending`, including the personal notification
outbox, enforce the ownership barrier under the account row lock. The outbox
transition and intent remain atomic. Personal credential and notification
history follows identity erasure; it does not acquire ownership of retained
company applications. Internal mail remains behind its upstream default-off gates.

Denormalized core child organization references follow the app root. The
migration repairs legacy personal-org references without changing billing
subject UUIDs, encrypted bytes or usage quantities. New writes enforce the same
root attribution. Aggregate gateway request limits use the organization subject;
storage lookups and signed runtime bindings continue to use the namespace.

Financial subjects can be organizations without creating accounts. Provider
customer/subscription identities, invoices, delivery receipts, retained evidence
and price snapshots accept resource namespaces. Personal billing records keep
their original subject and retention rules; organization records do not cascade
with a member. Path-scoped organization usage, billing status, invoice history,
provider portal and plan changes preserve current membership, credential scopes,
human MFA, verified email and step-up requirements. Provider confirmation remains
the only way to activate a paid plan.

## Billing adoption

Existing paid organizations retain their current billing/runtime policy until
an organization-owned, provider-qualified customer/subscription binding matches
its persisted handles. A customer ID alone cannot activate ownership. Cached
handles are not sufficient evidence, and personal provider identities are never
copied to a company. Free organizations start with organization ownership.
The provider confirmation path activates `resource_ownership_enabled` inside
the same transaction as the binding. Activation is permanent: cancellation,
downgrade and provider changes never restore a former creator as owner.

Minute replay preserves the first observation's financial subject; daily storage
keeps its first subject for that day. Subsequent intervals use the organization.
Paid organizations awaiting adoption, sole organization owners, and namespaces
with unsupported bindings cannot erase their human identity. The first company
checkout requires the existing owner, scope, verified-email, MFA and step-up
posture. Existing manual subscriptions need a verified binding imported or
replayed before cutover; they must not be replaced with another subscription.

Personal exports include personal application resources and identity data.
Company configuration, deployments, builds and resource events are excluded,
including after membership removal. Historical personal billing usage retains
its recorded subject. Abuse escalation uses the organization subject after
adoption and cannot drain an unrelated company's apps through a creator UUID.

## Rollout

Apply the expansion before upgrading all API, scheduler, image, builder, gateway and
metering readers/writers. Keep account erasure disabled until the fleet is
upgraded: an old API purge still traverses creator-wide data. Do not roll back
to old binaries while erasure is enabled. Storage keys and live instance
identities do not change. No historical financial record is re-billed.

Acceptance must prove real PostgreSQL creator erasure with company resources
intact, personal-resource erasure, unchanged sealed values, current-member
access and revoked former-member access, organization runtime policy and
organization usage attribution. Native KVM evidence is required before claiming
runtime deployment acceptance.
