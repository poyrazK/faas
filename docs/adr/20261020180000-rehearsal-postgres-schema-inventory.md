# ADR-20261020180000 · Rehearsal PostgreSQL schema inventory

- **Status:** accepted
- **Date:** 2026-10-20
- **Amends:** [ADR-20261017201009](20261017201009-rehearsal-postgres-fixtures.md), [ADR-20261020150000](20261020150000-rehearsal-contained-release-commands.md)
- **Decision:** The PostgreSQL collector seals a schema inventory of the fixture database next to the declared tables' rows. Undeclared relations and columns no longer make capture fail. The inventory, plus a bounded row count for each undeclared table, makes them compared evidence.
- **Why:** Contained rehearsals now run the candidate's release command (ADR-20261020150000). The collector, however, refused any database that did not contain exactly the declared tables with supported column types. Every migration that created a table, type or index therefore left the run inconclusive.

## Decision

### Capture rules

- Declared tables must still exist as ordinary persistent tables. Their selected columns must have supported types, and their rows compare exactly as before.
- Other columns of a declared table may have any type; they appear in the inventory.
- Undeclared relations are allowed, up to 64 relations in total.
- The existing refusals stay:
  - extensions other than plpgsql;
  - a declared table replaced by a view;
  - forced row security;
  - unfinished SQL at sealing.

### The inventory

At seal, in the same read-only snapshot as the rows, the collector records up to 256 user-schema objects. Each object has a kind, a name and a definition that is stable across databases built from the same fixture, so it contains no OIDs or database names:

| Kind | Definition |
|---|---|
| Relations (table, view, materialized view, foreign or partitioned table, sequence) | Columns with types, nullability and defaults; view text; sequence settings |
| Index | `pg_get_indexdef` |
| Constraint | `pg_get_constraintdef` |
| User type | Enum labels, domain base type |
| Function | SHA-256 digest of its definition |
| Trigger | `pg_get_triggerdef` |

- Definitions longer than 1 KiB are kept as a SHA-256 digest.
- Every undeclared table carries its row count, saturating at 10,001.
- Objects are ordered by byte order of kind and name, not by database collation.

### Evidence and comparison

- Evidence carries `schema_inventory_version: 1` and `schema`. Both are additive; version 0 means an older collector, and validation rejects any inventory without a version.
- Both runs start from identical fixture bytes, so their final inventories differ exactly where the candidate's schema, or data written outside the declared tables, differs.
- A difference fails the comparison and is listed as `schema_changes`: each object marked added, removed or changed. The CLI prints them.
- A comparison across inventory versions is inconclusive.

## Consequences

- Migrations that create tables, types or indexes are now rehearsable. A run that differs only in an intended migration still reports "failed" with the exact schema changes. Approving intended differences is a separate increment.
- Data written to an undeclared table is visible as a row-count difference, not row by row. Declare a table to compare its rows.
- Capture issues more catalog reads and one bounded count per undeclared table, all inside the existing seal.

## Rejected alternatives

- **Persisting an initial inventory.** It would need a new column. Equality of the fixture bytes already pins the starting schema.
- **Diffing raw `pg_dump` output.** It contains OIDs, ordering and version noise, and needs a binary and privileges the collector does not have.
