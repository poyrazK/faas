# ADR-936: Data API composite contracts

- **Status:** accepted
- **Date:** 2026-10-10
- **Decision:** Extend ADR-900 with generated contracts for standalone PostgreSQL
  `CREATE TYPE ... AS (...)` composites in exposed schemas, and approved invoker
  RPCs using supported composites. Catalog inspection and SQL execution remain
  inside the customer workload using its restricted binding.
- **Authorization:** Export attributes only when the binding has schema and type
  usage. Inspect standalone composite attributes separately from relation row
  types; never use this query to bypass table/column privileges. Hidden or
  unsupported dependencies become `unknown` in field contracts and disqualify
  RPC signatures. Existing opt-in, execution privileges, unique function names,
  named input arguments, invoker security and POST-only requirements remain.
  Relation row results keep ADR-900's exposed-relation-set restriction.
- **Types:** Generate named `Database[Schema]["CompositeTypes"][Name]` objects
  and a `CompositeTypes<Name, Schema>` helper. Preserve nested references,
  domains, enums and nullable array elements. Composite attributes are nullable:
  PostgreSQL has no composite attribute NOT NULL constraints, and PostgREST
  expands a NULL composite result into an object of null fields even for domain
  attributes. Direct composite RPC results are objects; SETOF results are arrays.
  Composite domains and composite arrays follow the existing domain/array rules.
  OpenAPI definitions and owner schema inspection use the same contract.
- **Compatibility:** The contract envelope stays at version 1. Newly generated
  normalized snapshots use version 2 and require a `composites` array, including
  an empty array. Readers accept legacy version 1 snapshots without this field.
  The diff command compares composite attributes independently, detecting nested
  changes while table/RPC reference strings remain unchanged. Removed types or
  fields, changed types/nullability and added required object keys are breaking;
  a new composite type is compatible. Merely upgrading an empty snapshot does
  not create a structural change. Upgrade the CLI before using new snapshots;
  regenerate types and approve baselines after reviewing differences.
- **Bounds:** Reuse existing catalog relation, column, type, query and output
  limits. Composite dependencies use a depth bound and cached support checks to
  avoid repeated graph traversal. No new database privileges or quota is added.
- **Validation:** PostgreSQL/PostgREST acceptance compiles and executes the
  generated TypeScript client for nested columns, named composite inputs,
  object/set/domain/array results, NULL results, RLS isolation and exclusions.
  Tests validate legacy/new snapshot parsing and flag an ALTER TYPE nested
  attribute change through the actual CLI diff command.
