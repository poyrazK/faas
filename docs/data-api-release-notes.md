# Data API release review

This change completes the current core Data API development slice. It does not
publish artifacts or establish hosted staging acceptance.

## Included

- Generic local projects, project-owned authorization scenarios, and owner tooling.
- PostgreSQL composite types, view relationships, computed relationships, and
  relation-set RPC projections in generated contracts.
- Nullable JSON projections and application-owned runtime parsers.
- Typed composite cursor pagination, adopted by the shipped notes starter.
- Local CI type drift checks without overwriting committed types.
- Reproducible CLI/SDK bundles and packaged custom-project verification.

## Upgrade

1. Install a matching CLI/SDK bundle using the verified artifact workflow in
   [Data API packaging](data-api-packaging.md). The SDK package version remains
   0.1.0; pin artifact integrity rather than relying on the version alone.
2. Regenerate application types. New snapshots use format version 3; readers
   accept earlier baselines. Review generated contract changes and compile the
   application before adopting the bundle.
3. Approve computed relationships explicitly using `@gregale:rpc`; executable
   unapproved computed relationships block inspection. Review permissions and
   refresh the serving contract after owner DDL or ACL changes.
4. Run `gregale data-api dev --port 0 --once --check-types --check` in local CI
   with Docker, Node 22+, client dependencies, and committed current types.
5. Upgrade the starter alongside the SDK: its cursor implementation now imports
   SDK helpers while retaining its existing cursor interface and validation.

`validateJson` uses synchronous application parsers. Cursor pagination requires
a unique, non-null final key and consistent filters/identity between requests;
it does not provide a snapshot across pages. The full guide covers these bounds.

## Comparison with Supabase's core Data API

| Capability | Gregale in this slice | Remaining gap or boundary |
| --- | --- | --- |
| Schema-generated REST and TypeScript | Supported, with CRUD, joins and approved RPCs | SDK is packaged through verified bundles; no public registry release |
| Local database/API development | Disposable migrations, watch reloads, inspector and authorization scenarios | Requires Docker; no hosted development acceptance in this environment |
| Query typing | Tables, views, composites, computed relationships and RPC projections | Restricted eligible relationships; arbitrary JSON shape stays application-owned |
| Runtime validation | Optional parser hook, including Zod-compatible parsers | Synchronous parsers only |
| Pagination | Typed keyset helper and native offset pagination | Unique keys and stable ordering are caller-owned |
| Schema drift | Hosted type checks and local migration-driven checks | Checks establish drift, not migration backward compatibility |
| Hosted tooling/distribution | Owner tooling, contract refresh and artifact pin/restore | Public distribution and staging canary still need a configured destination |

GraphQL and Realtime remain outside the agreed core Data API scope. This is a
capability comparison, not a claim of complete Supabase/PostgREST parity.

## Validation

Run `scripts/test-data-api.sh --integration` with PostgreSQL, pinned PostgREST
14.3, Chromium and Docker, then `scripts/test-data-api-packaging.sh` sequentially.
The packaging gate rebuilds the committed source twice, compares checksums, and
verifies installation, restoration, generated starters, browser builds and replay.
A hosted canary remains unverified because no staging endpoint or credentials
are configured. GitHub CI results belong to the PR and must be checked separately.
