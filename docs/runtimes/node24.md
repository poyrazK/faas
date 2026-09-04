# `node24` runtime

Node.js 24 LTS on the function surface (prewarmed persistent adapter with a
legacy per-request subprocess fallback, §4.9
envelope contract). Built by Railpack v0.31.1 with `--plan node`; the
underlying Node version is bound by the OCI base image
(`node:24-bookworm-slim` from `images/runner-node24.Dockerfile`). No
new dispatch logic — Railpack's `--plan node` is version-agnostic, so
detection priority (`docker > node > python > go` in
`pkg/builderd/detect.go`) treats `node22` and `node24` identically.

`node24` is **additive on top of `node22`**: existing `node22` apps are
unaffected. There is no default-flip in this PR — `node22` stays the
default for new function apps. A future PR may flip once fleet-wide
`snapshot_fleet_avg_mb` is measured with both bases co-resident
(`pkg/api/limits.go::FleetSnapshotAvgTargetMB = 130`, alarm 160).

## Function contract

The customer's source is a Node module exporting `default async
function handler(req)`, served at `/app/node24.js`. Generated adapters are
prewarmed during runner startup and process newline-framed §4.9 envelopes in
one long-lived subprocess; legacy protocol handlers retain one subprocess
per request. The runner reads the envelope from stdin and the handler writes
the response envelope to stdout.
This is identical to the `node22` contract; the only differences are
the **handler filename** (`/app/node24.js` vs `/app/node22.js`) and
the runtime id (`node24` vs `node22`).

The runner shim sets `FAAS_RUNTIME=node24` in the handler's environment
so customers can branch on runtime if they want.

### Minimal handler

```js
// /app/node24.js
export default async function handler(req) {
  return {
    status: 200,
    headers: { "content-type": "application/json" },
    body_b64: Buffer.from(JSON.stringify({ hello: "world" })).toString("base64"),
  };
}
```

### Local smoke test

The §4.9 envelope round-trips with bash and `base64` — the runner
starts `node /app/node24.js` and pipes newline-framed envelope JSON over stdin:

```
echo '{"method":"GET","path":"/hello","headers":{},"query":"","body_b64":""}' \
  | node /app/node24.js
```

prints a JSON response envelope on stdout. The platform runs the same
binary in production; nothing else differs.

### CGO

CGO is not applicable to JS runtimes.

## App contract

`node24` is a function-only runtime in this PR — there is no App
deployment path. Customers wanting a Node HTTP server use `type: app`
and `image: registry.gregale.dev/<digest>` to deploy a regular OCI
image (no runner shim). Adding a Node app path is out of scope; if
the workaround becomes a common ask, file an issue and we'll design
the contract separately.

## Base image

- Base ref: `ghcr.io/onebox-faas/runner-node24:latest`
- Source: `images/runner-node24.Dockerfile`
  (`FROM node:24-bookworm-slim@sha256:REPLACE_ME_AT_MERGE_TIME`)
- Disk: ~150 MB uncompressed, amortized across all `node24` apps via
  the two-drive scheme (drive0 = shared base, drive1 = per-app layer).
  Per-app cost is just the customer's `node_modules` + handler.

### Operational configuration

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
