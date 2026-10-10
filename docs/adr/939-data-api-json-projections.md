# ADR-939: Explicit nullable JSON projections in the Data API SDK

- **Status:** accepted
- **Date:** 2026-10-10
- **Decision:** Add `selectJson`, `jsonText` and `jsonValue` to the existing SDK.
  PostgreSQL JSON path extraction can produce SQL NULL for missing keys even
  when the source JSON column is NOT NULL. The pinned upstream SDK's native
  select inference can omit that nullability. Explicit aliases return
  `string | null` for text extraction and arbitrary `Json` for JSON extraction.
- **Integration:** Keep the native SDK builders and select parser. Infer base
  projections with its public, unstable `UnstableGetResult` export, covered by
  strict compilation against the pinned dependency. No runtime proxy, schema
  format change, catalog annotation, new service or quota. Preserve table/view,
  relation-set RPC and write representation chains, tokens, errors and filters.
- **Scope:** Alias-map paths apply at the selected resource. JSON expressions
  inside the base select retain upstream types. Check source columns and alias
  collisions against literal base projections. Reject names the helper does not
  safely encode, and require nonempty paths and safe integer indexes. Quote keys
  and leave URL encoding to the existing builder. Numbers mean array positions;
  numeric strings mean object keys. Do not infer arbitrary JSON shapes or enums.
- **Nulls:** SQL NULL and JSON null share the HTTP JSON `null` representation.
  Preserve that uncertainty in result types and document database predicates
  when the distinction matters. Text extraction also maps JSON null to SQL NULL.
  Database grants, invoker views/functions and RLS retain their existing roles.
- **Validation:** Strict compile-time checks for nullable aliases, generated
  columns, ordinary/nested projections and builder chains. Real PostgREST 14.3
  acceptance for JSON and JSONB, quoted keys, positive/negative array indexes,
  scalar text coercion, missing and null values, views, relation-set RPCs, write
  representations, hidden columns and Alice/Bob isolation. Browser inspector
  acceptance executes actual JSON paths under both identities.
