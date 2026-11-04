# ADR-20261103224500: Repair catalog contracts across schema changes

- Status: accepted for internal operator qualification
- Date: 2026-11-03
- Extends: [domain qualification](20261103201136-repair-timezones-and-domain-qualification.md) and [scalar fidelity](20261103205518-repair-scalar-fidelity-and-checks.md)

## Problem

An incoming cascading foreign key created after enrollment could update child
rows outside a reviewed selection. Recreating a domain under the same name
could reinterpret recorded text as numeric without changing the existing table
fingerprint. Index operator classes could invoke custom support functions
without a direct expression dependency, leaving session effects after rollback.

## Decision

Repeat incoming foreign-key qualification at every capture and repair entry
point, including advisory validation, verification and catalog-only readiness.
Reject UPDATE CASCADE, SET NULL and SET DEFAULT when the referenced key includes
a selected field. The existing parent-table lock fences concurrent additions
for the transaction.

Index qualification also inspects operator classes, families, support functions
and same-type operator implementations. Only original PostgreSQL catalog classes
and implementations are supported. Builtin support functions need not be
immutable: sortsupport functions legitimately have other volatility markings.
Qualification never executes those functions.

Keep the historical table fingerprint and plan documents unchanged. Add a
separate logical named-type signature to enrollment and new operations. Follow
all columns through domains, arrays, composites, ranges and multiranges, recording
definitions, members, enum labels, constraints and I/O implementations. Canonical
names and pinned rendering settings make this signature portable across
pg_dump/restore. Local catalog tuple identities also fence type changes and
changes reverted within the capture transaction, at finalization and commit.
Disable JIT for these bounded catalog inspections to avoid compiling large
catalog plans on the readiness request path.

An explicit owner upgrade establishes the current type baseline for future
captures. It never fills historical operation signatures or rewrites evidence,
digests or approvals. Old captures involving named types cannot authorize a new
repair without contemporaneous type proof. Original builtin-only histories
retain their existing value-fidelity requirements. A changed baseline does not
recertify previous captures. Committed receipts remain recoverable.

Writers, executors, freshness readers and verification readers need EXECUTE on
`gregale_repair.type_fingerprint(regclass,boolean)`. Readiness uses embedded
catalog SQL rather than calling a possibly altered installed function. Apply
the owner upgrade and grants before matching executor and SDK versions.

Owners must coordinate schema DDL with repair operations. The signatures detect
catalog drift; they do not provide a general PostgreSQL type-DDL lock or support
arbitrary application-defined index behavior.

## Validation

PostgreSQL regressions cover late cascading keys, domain recreation, enum and
composite changes, custom classes with custom and builtin comparators, missing
historical proof, changed-and-restored definitions, blocked callbacks, unchanged
approvals and receipt recovery. Docker rehearsal checks named types through a
real dump/restore and apply. Go, Node and Python share regenerated readiness SQL.
