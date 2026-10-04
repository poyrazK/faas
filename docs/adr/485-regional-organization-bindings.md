# ADR-485: Regional organization ownership bindings

Date: 2026-10-04
Status: Accepted; shared runtime quota and billing admission pending.

## Context

ADR-483 provides directory organization roots and ADR-484 separates regional
consent issuers from app creators. Account-to-cell mappings cannot represent a
shared organization: two apps with different creators must share one tenant
mapping, and creator departure must not change ownership.

## Decision

Regional ownership proofs add an optional `org_id` for shared apps. The existing
`account_id` remains app creator metadata, never the authorizing member. Personal
proofs omit the organization field. Organization-bound admin-scoped keys and
purpose-scoped release grants prove a shared app only while the current actor is
an active owner or administrator. Sessions, ordinary app-scoped keys, platform
tenant keys and conflicting tenant hints are refused. Existing MFA and verified
email gates apply. Proofs attest identity, not runtime readiness.

A shared directory root maps to a regional organization UUID per cell. A personal
root maps to a regional account UUID. These typed mappings are immutable and
unique within the cell, persist after target detachment, and belong to the tenant
root rather than the requesting member. Targets retain app creator metadata and
the regional organization anchor. Different creators may attach different apps
from the same organization. A cell's app UUID remains claimable by one global
app. Registry generation changes still require a fresh ownership proof.

Shared attachment and detachment require current directory owner/admin consent.
The allocation transaction locks organization, account, membership and root
before app and registry rows, rechecking live authority after waits. SQL guards
reject tenant kind/anchor mismatches, retained identity replacement and mapping
deletion. Runtime SQL uses SQLC and schema changes are append-only.

Release snapshots and sealed delegation identities carry the optional regional
organization field. Personal ciphertext omits it and retains its previous JSON
spelling so existing credentials remain readable. Rollback refuses organization
bindings; populated personal bindings, release journals and ciphertext survive
down/up migration.

Shared image release allocation and delegation admission remain gated in both
the API and PostgreSQL until global runtime quotas and regional organization
billing are integrated. Identity attachment allocates no capacity and publishes
no ingress route. The serving policy remains `all_selected_regions`.

## Validation

Exercise memory and PostgreSQL contracts for different app creators, creator
deletion, fixed tenant mappings after detach, tenant uniqueness, current roles and
inactive organizations. Test role/removal/account/org changes while allocation
waits, raw SQL bypasses, migration round trips and shared downgrade refusal.
Authenticated HTTP and Go SDK tests attach two independent regional APIs, verify
proofs after creator departure, deny role/tenant switching and retain the shared
runtime gate. Native KVM runtime recovery remains required for full qualification.
