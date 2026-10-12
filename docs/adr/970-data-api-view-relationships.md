# ADR-970: Data API relationships through views

- **Status:** accepted
- **Date:** 2026-10-10
- **Decision:** Extend ADR-650's generated foreign-key metadata to readable
  ordinary views that project every column of a declared foreign key or its
  referenced key. Use PostgreSQL's root `pg_rewrite` target origins, matching
  PostgREST 14.3's lineage model, rather than interpreting SQL text.
- **Contract:** Expand complete keys into view/table, table/view and view/view
  relationships. Preserve constraint names, composite column order, renamed
  projection columns, repeated aliases and the underlying constraint's
  one-to-one cardinality. Exclude cloned partition constraints, matching
  PostgREST's canonical foreign-key catalog. Resolve view chains only through
  readable objects in exposed schemas, with a cycle/depth bound. Expressions,
  partial keys, unions, inaccessible origins and unsupported node structures
  yield no inferred path.
  Materialized views and computed relationships remain outside this slice.
- **Authorization:** Inspection uses fixed, parameterized catalog reads inside
  the customer workload's existing read-only transaction. Readable columns and
  existing table SELECT grants filter every origin and join endpoint; relations
  must stay in the same exposed schema. No SQL executes in the control plane.
  View definitions, internal OIDs and node trees are not exported. View grants
  and owner-authored security modes remain unchanged. Owners must use
  `security_invoker=true` and underlying RLS for subject isolation.
- **Compatibility:** Add metadata to the existing normalized version 2
  `relationships` arrays; no new snapshot field/version is needed. Existing
  fingerprints can change as views gain relationships. Existing structural diff
  checks flag changed relationships even when column names/types stay identical.
  Regenerate contracts after refreshing the runtime and review baselines. Views
  retain their read-only generated helpers and conservative column nullability.
- **Ambiguity:** PostgREST remains the dispatch authority. Multiple foreign keys
  can be selected by constraint hints; repeated projections of one composite key
  can remain ambiguous even with a constraint hint. Generated metadata preserves
  those alternatives and does not invent a dispatch rule or validate hints.
- **Bounds:** Budget the combined view-definition input under the existing output
  limit before returning it from PostgreSQL. Reuse catalog relation/query bounds
  and cap expanded relationship metadata under the existing relation limit.
  Fail generation on oversized input or excessive alias combinations.
- **Validation:** Pure tests cover escaped delimiters, root/nested targets,
  malformed definitions, aliases, chains, incomplete keys, cycles and bounds.
  PostgreSQL/PostgREST acceptance compiles and runs typed aliased joins in both
  directions, one-to-one results, inner joins, partitioned tables, two-subject
  RLS, inaccessible columns/tables and ambiguous paths. A view expression change
  with an unchanged column contract triggers a breaking CLI relationship diff.
