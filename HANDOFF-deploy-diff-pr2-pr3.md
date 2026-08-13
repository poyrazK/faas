# Handoff — `gregale deploy --diff` cluster (PR-2 + PR-3 remaining)

> **Status:** PR-0 (#860) and PR-1 (#869, merged 2026-08-13T09:49:13Z) are on
> `main`. PR-2 and PR-3 from the original cluster plan are **not yet started**.
> Pick up from here.
>
> **Working branch for the next agent:** `worktree-feat-deploy-diff`
> (already exists in `.claude/worktrees/feat-deploy-diff/` and is the same
> branch the PR-0 / PR-1 work landed from).

## What landed

### PR-0 — `feat(cli): gregale deploy --diff` — #860, merged

CLI local diff against the SDK baseline. Five SDK GETs (apps,
deployments, envs, crons, edge rules) feed `pkg/deploydiff.Compute`,
which produces `Diff{ Changes, Breaks }` that the gate consumes.

- `cmd/gregale/commands_diff.go` (PR-0 file, since heavily edited in PR-1)
- `pkg/deploydiff/{diff.go,quota.go,render_text.go,render_json.go}` — engine
- `pkg/deploydiff/{diff_test.go,quota_test.go}` — engine tests

### PR-1 — `feat(deploydiff): POST /v1/apps/{slug}/diff server endpoint` — #869, merged 2026-08-13

Read-only server endpoint so CI doesn't have to ship the CLI binary.

Files:

| File | Purpose |
|---|---|
| `pkg/api/diff.go` | Wire DTOs (`DiffRequest` / `DiffAppConfigPatch` / `DiffEnvRow` / `DiffChange` / `DiffBreak` / `DiffPayload` / `DiffResponse`). Polymorphic Before/After/Observed/Limit use `json.RawMessage` so the wire is byte-stable against the engine's `anyJSON`. |
| `pkg/api/diff_test.go` | Round-trip tests. |
| `pkg/api/client.go` | `Client.Diff(ctx, slug, req)` — added at the bottom of the existing client. |
| `pkg/deploydiff/diff.go` | `Compute(slug, plan api.Plan, baseline, pending)` — the plan parameter replaced the PR-0 `inferPlan` stub. `ToWire()` adapter converts engine `Diff` → `api.DiffResponse`. The engine's `anyJSON` stays private; `anyJSONToRaw(v anyJSON) json.RawMessage` re-encodes via `json.Marshal`. |
| `pkg/deploydiff/engine.go` | 647-line diff engine (predates PR-1; see review fixes section below for the four cleanups PR-1 made). |
| `pkg/deploydiff/engine_test.go` | Tests, all callsites updated to the new `Compute` signature. |
| `cmd/apid/handlers_diff.go` | `s.diffApp` handler (280 LOC). Auth chain: `authLimited` + `requireScope(ScopesReadSurface)` — read-only, no MFA, mirrors `GET /v1/apps/{slug}/metrics` at `server.go:785`. **Reads the store via `s.store.AppBySlug` (NOT `s.loadApp`)** so a missing slug returns 200 with would-create-app Change and the customer's preview isn't lost. |
| `cmd/apid/handlers_diff_test.go` | New file — three table-driven tests pin missing-slug-200, cross-account-404, existing-slug-200 boundaries. **Crucial regression coverage for review finding #1.** |
| `cmd/apid/server.go` | Route: `POST /v1/apps/{slug}/diff`. |
| `cmd/apid/spec_compliance_test.go` | `diffFile = "diff.go"` constant + included in `testSchemasParity`'s files slice. |
| `api/openapi.yaml` + `pkg/apid/openapi.yaml` | New path + 7 schemas + `deploydiff` tag. Byte-identical mirror (Makefile `spec-sync`). OpenAPI 3.1 `[T, 'null']` for nullable per `pr-819-openapi-nullable-3-1`. |
| `cmd/gregale/commands_diff.go` | PR-0's `--server-diff` flag (was wired but not routed) now calls `client.Diff`. `syntheticDiffFromResponse` re-projects the wire `DiffResponse` back onto the engine's `Diff` so the same renderers work. `diffRequestFromCLI` projects CLI flags onto the wire `DiffRequest`. |
| `cmd/sdk-coverage/main.go` | One row in `methodRouteMap`. |
| `sdk/node/src/generated/` | 7 new models + `DeploydiffService`. Regenerated via `npm ci && npm run gen`. |

### Review fixes merged with PR-1

Code review (low effort, agent) caught four findings; all closed on `main`:

1. **`handlers_diff.go:82-86` `loadApp` 404 vs preview-200** — handler now
   uses `s.store.AppBySlug` with `errors.Is(err, state.ErrNotFound)` branch
   for the preview path. Cross-account slug still 404s. Pinned by
   `cmd/apid/handlers_diff_test.go`.
2. **`render_text.go:269` `splitBreaksBySeverity` duplicated by
   `render_json.go:63` `sortedBreaks`** — `sortedBreaks` now delegates to
   `splitBreaksBySeverity`. Both renderers share one severity-partition
   helper.
3. **`engine.go:389,407` dead `seen := map[erKey]int{}` variable** —
   removed. Comment rewritten to match actual behaviour (current-occurrence
   index, not first-seen — the duplicate `Observed` reports `i`).
4. **`engine.go:580` `len(p.Manifest.EnvSecrets) > 0` falsy-zero guard** —
   removed. Mirror of the comment-cited Env-path bug at lines 562-564.
   Now both Env and EnvSecrets branches emit `schema_env_changed`
   "whenever the manifest is present".

PR-1 head on main: **5a34a0c2** (gofmt cleanup is the final commit).

### CI gate profile observed on PR #869

Run `bash ./scripts/check-gofmt.sh` or `gofmt -l` over modified `.go` files
**before** pushing. Local `make lint` does NOT include the gofmt gate; CI
does (lint+build job: `gofmt` step fails the entire check). This bit PR-1
twice — three files needed reformatting after the initial commit.

## What's outstanding

### PR-2 — structural OpenAPI schema-break detection (`pkg/openapidiff/`)

**Current state:** the engine emits a single text-only
`schema_env_changed` Break via the Env/EnvSecrets diff. The full structural
detection (response-schema changes by path/method/status) is not yet
wired.

Per the cluster plan (`/Users/poyrazk/.claude/plans/cozy-wishing-church.md`):

- New package `pkg/openapidiff/`:
  - `Generator.FromManifest(appSlug, AppManifest) (openapi3.T, error)` —
    load the current OpenAPI (already embedded at
    `pkg/apid/openapi.yaml`), apply manifest-driven mutations: new
    route paths (via edge_rules with `kind=route`), per-path response
    shape changes (when handler changes for a path already in the
    spec), schema additions for any `+POST /payments` example. Narrow
    generator that only projects the *manifest's deltas*, not the full
    OpenAPI. Keeps it deterministic.
  - `Differ.Compare(current, proposed) []SchemaBreak`. Walks
    `Paths` → `Operations` → `Responses` → `Content` → `Schema` for
    both. Ignores property order, description whitespace, and
    `[T, 'null']` ≡ `nullable: true` (per the OpenAPI 3.1 noise rule).
    Emits one `SchemaBreak{ Path, Method, Status,
    Kind: type_change|field_removed|required_added|nullability_change }`
    per change.
- Wire in `pkg/deploydiff/diff.go`: call generator + differ on the same
  `(Baseline, Pending)` pair. Any `SchemaBreak` becomes a
  `Break{ Code: "schema_response_changed", Severity: error }`.
- Server endpoint (PR-1): the same call, server-side, so CI callers
  get the full gate.
- Tests: `pkg/openapidiff/differ_test.go` golden tests:
  property reorder (no break), `[T, 'null']` ≡ `nullable` (no
  break), type change (break), required added (break).

**OpenAPI 3.1 noise rule (memory `pr-819-openapi-nullable-3-1`)** —
`[T, 'null']` ≡ `nullable: true` ≡ same schema semantically; the
differ MUST skip these so the schema-break signal stays
high-precision.

**Decoder/loader:** `pkg/openapidiff/loader.go` should reuse the
already-embedded current OpenAPI at `pkg/apid/openapi.yaml`
(`//go:embed`). Don't add a second embed source.

### PR-3 — `cmd/e2e/diff_test.go` (cluster envelope)

PR-3 closes the cluster with an end-to-end test (per
`tier-a-e2e-shape`, `provision-real-app-e2e-pattern` memories):

- `cmd/e2e/diff_test.go` (new):
  - Provision a real Hobby app via the `e2e harness` (per memory
    `provision-real-app-e2e-pattern`).
  - Run `gregale deploy --diff` proposing `ram_mb=2048` — expect
    gate to fire with `code = plan_ram_mb_exceeded`.
  - Run `gregale deploy --diff` proposing an extra cron (would push
    Hobby app from 5 to 6) — expect `code = cron_limit_per_app`.
  - Run `gregale deploy --diff --json` against the same payload
    twice — assert the wire shape is stable (byte-identical
    `DiffResponse`).
- Mirror the same shape against `--server-diff` once PR-2 is in, so
  the schema-break signal is asserted end-to-end.

## Out-of-cluster follow-ups (per the plan, low priority)

- **Per-scope env diff** — `gregale app env <scope> --diff`. Same
  `pkg/deploydiff` engine, new CLI surface.
- **Diff against a target environment other than the live one**
  (pre-production preview deploys). Needs an `env=` parameter.
- **Per-deployment manifest diff** — extends `pkg/deploydiff.Baseline`
  to load `(*Deployment).AppManifest` and diffs against a proposed
  manifest.

## Files the next agent should touch (in rough order)

1. `pkg/openapidiff/loader.go` (new) — load embedded OpenAPI.
2. `pkg/openapidiff/generator.go` (new) — manifest → proposed OpenAPI.
3. `pkg/openapidiff/differ.go` (new) — structural walk + noise filter.
4. `pkg/openapidiff/differ_test.go` (new) — golden tests as above.
5. `pkg/deploydiff/diff.go` — wire the differ in. Append
   `SchemaBreak` to `Diff.Breaks` with `Code: "schema_response_changed"`,
   `Severity: error`.
6. `cmd/e2e/diff_test.go` (new) — provision + assert envelope.

## Things to watch for

- The engine private type `anyJSON` and the wire's `json.RawMessage`
  are kept in lock-step. The ToWire adapter (`anyJSONToRaw` in
  `pkg/deploydiff/diff.go`) is the only conversion seam — extending
  the wire DTOs without updating the adapter will fail JSON
  round-trip tests in `pkg/api/diff_test.go`.
- `deploydiff.Compute(slug string, plan api.Plan, baseline Baseline,
  pending Pending) Diff` — the **plan** parameter is mandatory.
  Existing PR-1 callsites must not regress to the PR-0 signature.
- `s.store.AppBySlug` returns `state.ErrNotFound` for missing slugs.
  The handler `errors.Is` check is the seam — keep it. Using
  `s.loadApp` instead will silently break the fresh-app preview path.
- `state.AppEnv` does NOT have `HasValue` or `Scope` fields — those
  are derived. Use `state.AppEnv.AccountID / AppID / Scope / Key /
  Value / CreatedAt / UpdatedAt` only.
- `pkg/state/pgstore.go` has the IO store; `pkg/state/memstore.go`
  has the in-process test mirror. Most new server-side reads have
  to exist on both. The `cmd/apid/handlers_diff.go` tests use the
  MemStore (see `cmd/apid/handlers_diff_test.go` for the testEnv seam).
- Slot fence discipline (per `cross-pr-slot-gate-races-with-active-pr`
  memory): if any migration is added, check
  `gh api repos/poyrazK/faas/contents/migrations?ref=main | jq` right
  before `gh pr create` to avoid slot collisions.

## CI profile + reminders for the next agent

- `make lint` does **not** include gofmt. Run `gofmt -l` over modified
  `.go` files before pushing, or the `lint+build` CI job will go red.
  (Per `gofmt-repo-wide-gate` memory.)
- Local `make sdk-check` won't catch the Node SDK regen drift that
  CI's `sdk-node (gen-check + smoke + unit)` does. Run
  `cd sdk/node && npm ci && npm run gen && npm run gen:check` after
  any `api/openapi.yaml` change.
- Pre-existing flake: `TestV1AuthLogin_TimingPadEqualisesTwoFailurePaths`
  reproduces under load but passes in isolation. Unrelated to deploy-diff.
- `pkg/api/limits.go` is the ONE table for quotas/limits. Adding new
  limits requires editing that file + the relevant docs —
  never inline a limit. (`cli-plan-creation-cmd-pr-849` and several
  other memories reinforce this.)

## Anchors for the next agent

- Plan file with full PR-2 details: `/Users/poyrazk/.claude/plans/cozy-wishing-church.md`
- Source-of-truth spec: `docs/faas_implementation_spec.md`
- ADR directory: `docs/adr/` (ADR-091 / 092 / 096 in scope for the
  diff feature's neighbour concerns)
- Merged PRs: `#860` (PR-0), `#869` (PR-1)
- Working branch (already exists for the next agent to use):
  `worktree-feat-deploy-diff`

## Done checklist for the next agent

- [ ] PR-2: open new feature branch from main, ship `pkg/openapidiff/`
- [ ] PR-2: wire differ into `pkg/deploydiff/diff.go`
- [ ] PR-2: CI green on PR (sdk-node regen included)
- [ ] PR-2 merged
- [ ] PR-3: write `cmd/e2e/diff_test.go`, get it green on the e2e
      harness
- [ ] Cluster complete — close the issue / memory pointer
