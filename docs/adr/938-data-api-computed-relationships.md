# ADR-938: Typed computed relationships and relation-set RPC projections

- **Status:** accepted
- **Date:** 2026-10-10
- **Decision:** Extend the existing `@gregale:rpc` opt-in (ADR-900) to supported
  SQL-defined relationships. Emit relation-set metadata for SDK projections,
  nested joins and the PostgREST `ROWS 1` embed contract. All inspection remains
  bounded, fixed catalog SQL inside the customer workload (ADR-650).
- **Eligibility:** A computed relationship has one named, non-defaulted source
  relation-row argument and a `SETOF` relation result in the function's exposed
  schema. Require full SELECT access, type usage and recognized source fields,
  invoker security, STABLE/IMMUTABLE volatility, an exact annotation, EXECUTE and
  a unique function name. Exclude names shadowing exposed relations, foreign-key
  constraints or source columns; no overrides, cross-schema inference or
  materialized/foreign-table endpoints.
  Existing scalar/composite RPC policy remains unchanged.
- **Transport:** `ROWS 1` selects object-or-null embeds; other estimates select
  arrays. Direct POST RPC results remain arrays. The normalized return contract
  therefore keeps its array, while generated SDK `Returns`/`SetofOptions` account
  for the SDK's separate RPC and embed handling. Ordinary relation-set RPCs get
  a target with an empty source; they cannot become inferred computed joins.
- **Authorization:** PostgREST dispatches computed relationships inside selects,
  independently of the RPC route guard. Inspection and fresh startup reject
  currently executable engine-recognized relationship functions absent from the
  approved computed contract, including PUBLIC grants and unsupported overloads
  or definer functions. Owner permission checks report blockers and refuse apply;
  owners must revoke execution or make functions eligible. Supported grants use
  the existing atomic owner workflow. SQL grants enforce private-function denial
  in nested queries. Fresh restart is required after owner changes; startup
  inspection is not continuous ACL monitoring. Bodies remain owner-authored;
  invoker status and volatility declarations do not prove subject isolation.
- **Contract:** Normalized snapshot version 3 adds optional per-function `setof`
  metadata (`from`, `to`, `one_to_one`). The envelope stays version 1. The CLI and
  owner dashboard retain version 1/2 baseline support and strictly validate new
  fields and same-schema endpoints. Upgrade readers with the runtime, regenerate
  contracts and review fingerprints. Removing/changing existing result metadata
  is breaking; adding metadata is compatible. ROWS changes can break embedded
  shapes without changing the RPC array return contract.
- **Tooling:** Local inspector, owner dashboard and OpenAPI expose approved
  result relationships without function bodies, SQL defaults or catalog OIDs.
  Reuse existing query, function-count and output limits; no new quota or service.
- **Validation:** Compile and execute real typed table/view/self computed embeds,
  aliases, nested joins, inner joins, empty results and relation-set RPC filters.
  Verify Alice/Bob RLS with equal IDs, forged RPC row subjects, direct ROWS 1 array
  responses, PUBLIC/binding execution grants, unsupported overloads, permission
  apply and breaking cardinality diffs. Preserve legacy snapshot round trips.
