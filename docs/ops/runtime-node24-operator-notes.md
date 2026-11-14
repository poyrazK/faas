# `node24` runtime: operator notes

Moved out of the customer page `docs/runtimes/node24.md` (ADR-20261028200304).

## Original introduction

Node.js 24 LTS on the function surface (prewarmed persistent adapter with a
legacy per-request subprocess fallback, §4.9
envelope contract). Built by Railpack v0.31.1 with `--plan node`; the
underlying Node version is bound by the OCI base image
(`node:24-trixie-slim` from `images/runner-node24.Dockerfile`). No
new dispatch logic — Railpack's `--plan node` is version-agnostic, so
detection priority (`docker > node > python > go` in
`pkg/builderd/detect.go`) treats `node22` and `node24` identically.

`node24` is **additive on top of `node22`**: existing `node22` apps are
unaffected. There is no default-flip in this PR — `node22` stays the
default for new function apps. A future PR may flip once fleet-wide
`snapshot_fleet_avg_mb` is measured with both bases co-resident
(`pkg/api/limits.go::FleetSnapshotAvgTargetMB = 130`, alarm 160).


## Operational configuration

The runtime base is **auto-staged** by `imaged` through
`pkg/imaged/base_stage.go::EnsureRuntimeBase`. The deployment pipeline
must write `FAAS_DEPLOY_BASE_REF_NODE24` as an immutable OCI digest in
`/etc/faas/runtime-bases.env`; the default `:latest` is for unnamed
development daemons only.

The production workflow is:
1. Publish the `images/runner-node24.Dockerfile` image to
   `ghcr.io/onebox-faas/runner-node24` and record its config digest.
2. Let the deployment pipeline render that digest into
   `FAAS_DEPLOY_BASE_REF_NODE24`.
3. Start or restart `imaged`. It pulls, validates, and stages the ext4
   automatically; subsequent boots short-circuit on the digest sidecar.

Operators must not build, copy, or manually place a runtime `.ext4` on a
compute node. A missing or invalid base is a deployment error, not a reason
to fall back to a hand-staged artifact.

If a non-digest-pinned `FAAS_DEPLOY_BASE_REF_NODE24` is set (e.g.
`:latest`), imaged aborts startup loud with a one-line error naming
the offending env var. The retired global `FAAS_DEPLOY_BASE_REF` is rejected.

## Detection priority

`pkg/builderd/detect.go` priority order is
`docker > node > python > go`. The `node` branch matches on
`package.json` — it does NOT branch on the `engines.node` field, so a
tarball with `engines.node: ">=24"` still builds against the `node22`
or `node24` base depending on which runtime the customer chose
elsewhere. Version selection is operator-controlled via
`FAAS_DEPLOY_BASE_REF_NODE24`.

## Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| `unsupported function runtime "node24"` | migration `00075` not applied, OR imaged built before PR 1 merge | `goose up`; rebuild + restart imaged |
| runner exec fails with `code: 'MODULE_NOT_FOUND'` | customer handler uses an npm dep not vendored into the tarball | bind via Railpack `package.json` lockfile, redeploy |
| `exec format error` on first wake | arm64 base deployed on amd64 host (or vice versa) | set `FAAS_DEPLOY_BASE_REF_NODE24` to the matching arch digest |

## See also

- `docs/STATUS.md` — runtime roster
- `pkg/api/build.go::FrameworkRailpackNode` — wire contract
- `pkg/builderd/dispatch.go::MapFramework` — detection → wire
- `guest/runners/node24/main.go` — function runner shim
- `guest/runners/node24/main_test.go::TestNode24RunnerHandlerDefault` —
  pins `/app/node24.js` against imaged argv
- `images/runner-node24.Dockerfile` — base image
- `migrations/00075_app_runtime_node24_python313.sql` — runtime enum widening

<!-- CI status: runtime migration, handler matrix, OCI auto-staging, and
runtime-image smoke coverage are implemented and enforced by CI. -->
