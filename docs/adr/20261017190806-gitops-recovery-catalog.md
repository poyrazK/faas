# ADR-20261017190806: GitOps recovery catalog

- **Status:** accepted (implementation preview)
- **Date:** 2026-10-17
- **Related:** ADR-20261017190803, ADR-20261017190804, ADR-20261017190805

## Context

Reviewed rollback plans require operators to discover release UUIDs and check
historical evidence and artifact eligibility individually. Generic release history
also contains releases that were never proven serving by the current Git binding.

## Decision

Provide a read-scoped GET recovery catalog and `gitops releases`, plus an explicit
read-only dashboard check. Require an immutable target manifest commit verified
through the bound provider. List only retained release sets with completed serving
receipts in the current source/account/project/environment. Use descending creation
time and UUID ordering with a scoped release UUID cursor and a bounded page size.
Expired compatibility windows remain visible. A detached/rebound source cannot
borrow a different binding's history, including through a page cursor.

For every row reuse the exact ADR-20261017190805 rollback planner, on one memory lock or
PostgreSQL repeatable-read transaction for the whole page. Expose eligibility and
stable blockers, public image/build identities, original manifest commit and
serving time, active selection, and the release routing compatibility deadline.
The catalog never claims that deadline guarantees artifact retention. It does not
pin, extend, rebuild, approve, activate, restore data or initiate a rollback.
Unknown storage/provider failures fail the assessment; they never become eligible.

Show a command to request a fresh individual rollback plan. Listing does not
replace hash revalidation before export, ordinary Git review, or new target
qualification, activation and serving. Preserve value redaction and no-store.

## Consequences and validation boundary

Eligibility can change immediately after observation when trust, intent or
retention changes. Paging produces fresh independent observations; it is not a
cross-page frozen snapshot. Portable and PostgreSQL checks cover scoped history,
planner parity, pagination and read-only behavior. No native VM lifecycle changes
or physical KVM/leak acceptance are introduced. Production execution stays gated.
