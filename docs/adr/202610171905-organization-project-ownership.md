# ADR-202610171905: Organization-owned projects and environments

Status: accepted
Date: 2026-10-17

## Context

ADR-202610171904 separates application storage namespaces from human identities. Project
lookup, environment administration and release history still use the creator's
account, preventing a company from retaining its deployment workflow after that
person leaves. A storage namespace must not grant access to a project.

## Decision

Persist `projects.org_id` as the ownership root. Existing project, environment,
release, configuration and workload IDs and sealed bindings remain unchanged.
Project-owned control-plane rows reference resource namespaces instead of human
accounts. Environments and releases inherit ownership from their project; apps
attached to a project must have the same organization and storage namespace.

Backfill an existing project's organization from its workloads when they agree,
and use its personal organization when it has no workloads. Reject ambiguous
legacy project ownership during migration rather than expose mixed resources.
An existing shared project owner cannot be replaced by a different workload
organization during this backfill.
Organization and namespace attribution cannot be changed by ordinary updates.

Organization project APIs identify the company in the URL. Legacy project URLs
still check current membership for company projects. Explicit permissions cover
project reads, configuration, deployments, security/protection, approvals and
deletion. Owners/admins approve protected environments and manage security;
developers configure and deploy; viewers read; billing seats cannot inspect
project configuration. Scope, MFA, verification and audit actor remain human.
Unreviewed integrations do not gain shared access automatically. Shared source
uploads reject database/async-route bindings before looking up personal services.
Environment cloning and browser approval exchange remain outside this slice.

Project writes carry the existing membership/credential revision fence through
their transactions. Offboarding applies before idempotency replay. Receipts are
partitioned by caller and credential, including promotion operation keys. Audit
events retain organization/project attribution. Personal exports exclude company
project references even after departure.

Creator erasure preserves adopted company projects, environments, immutable
configuration and release/promotion history, including projects with no workloads.
Their namespace remains while a retained project or app references it. Personal
projects are removed with their personal organization. Project runtime policy
follows organization billing adoption using ADR-202610171904's permanent marker.
New deployment-rate admission uses
the organization subject after adoption, across project storage namespaces;
existing personal windows are not reassigned. Unadopted paid projects and
unsupported data-service, GitHub and GitOps bindings continue to block identity
erasure.

## Acceptance and rollout

Prove current role and credential checks, cross-organization isolation, raced
offboarding, personal export isolation and PostgreSQL creator erasure with stable
configuration/release identities. Exercise deployment, promotion and rollback as
a remaining teammate. Before applying the expansion, remediate duplicate company
project slugs, mixed workload organizations and conflicting persisted shared
owners. Upgrade API writers after the expansion and keep identity erasure
disabled until the entire fleet understands project ownership.
No old binary may purge identities after this cutover. Native runtime deployment
acceptance remains a separate release gate under ADR-202610171904.
