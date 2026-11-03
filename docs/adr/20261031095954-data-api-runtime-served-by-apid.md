# ADR: The control plane serves the Data API runtime

Date: 2026-10-31
Status: Accepted
Related: [ADR-650](650-schema-generated-data-api.md) (schema-generated Data API), [ADR 20261018170742](20261018170742-data-api-access-modes-and-clients.md) (single-app projects), [ADR 20261027123814](20261027123814-data-api-capability-tree.md) (Data API capability tree)

## Context

The Data API runtime (PostgREST plus the Node gateway in
`cmd/gregale/templates/data-api`) is platform code, but only the CLI carries
it. `data-api create` and `data-api deploy` upload the copy embedded in the
installed CLI binary. Data pipelines are the exception: apid deploys its own
embedded copy.

As a result:

- A customer on an old CLI release redeploys an old runtime, with whatever
  bugs it had, and nothing tells them or us.
- A runtime fix reaches an API only when its owner upgrades the CLI and then
  redeploys.
- No one can tell which runtime a running API serves.

## Decision

1. **apid serves the runtime.** `GET /v1/apps/{slug}/data-api/runtime` returns
   the runtime apid was built with: a version, a deterministic gzipped tar of
   the runtime files and the tar's SHA-256. The route sits under the Data API
   route prefix, so the `data-api` capability gates it. It needs only read
   scope, because it returns platform code and no customer data.
2. **The CLI deploys what apid serves.** `data-api create` and
   `data-api deploy` download the runtime and check its digest, size, entry
   paths and the version file inside it. They then upload it, adding the
   customer's migrations for a single-app project. The CLI falls back to its
   own copy only when apid answers "no such API route", which means an older
   control plane. In that case it says so. Any other failure stops the deploy,
   so the CLI never silently ships a runtime the platform did not choose.
3. **`data-api deploy` without `--migrations` upgrades the runtime only.**
4. **The version is a content digest.**
   `cmd/gregale/templates/data-api/runtime-version.json` holds the first 16 hex
   characters of the SHA-256 over the image's files. Those are the
   Dockerfile, the Procfile, and every file the Dockerfile copies from the
   build context, minus the version file itself. Docs and tests are excluded,
   so editing them does not mark running APIs as out of date. A unit test
   fails when the file does not match the digest.
5. **A running API reports its version.** The image copies the version file,
   and `/healthz` reports it as `runtime_version`. `data-api health` compares
   it with the served version and lists `runtime_not_current` when they
   differ, or when the runtime predates reporting. The finding does not
   change the health status, because an old runtime still serves.

## Consequences

- A runtime fix reaches an API at its next `data-api deploy`, whatever CLI the
  owner has. An older CLI release still uploads its own runtime, but
  `data-api health` reports that API as not current.
- Two concurrent changes to the runtime both rewrite the version file, so they
  conflict in review. Regenerate it with
  `UPDATE_DATA_API_RUNTIME_VERSION=1 go test ./cmd/gregale/templates -run DataAPIRuntimeVersion`.
- During a control-plane rollout, two apid releases can serve different
  runtimes. Each response is self-consistent, and the next deploy picks up
  the newer one.
- The version is a digest, not an ordered release number. It tells "same" from
  "different", not "older" from "newer". Since deploys take the served
  runtime, a version that differs from the served one is in practice an older
  one.
- The dashboard does not show the comparison yet. It can call the runtime
  route and the API's `/healthz` in the same way the CLI does.
