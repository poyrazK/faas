# ADR-086 · Nested-marker monorepo hint on `gregale deploy` (issue #744 / DEPLOY-PROV-8)

- **Status:** accepted
- **Date:** 2026-08-08
- **Issue:** #744 / DEPLOY-PROV-8
- **Supersedes:** none
- **Related:** ADR-083 (function-vs-app shape auto-detection, the `Detected: …` banner pattern this extends); ADR-144 (explicit workspace-member deploys now retain repository context); `pkg/reposcan/workspaces_extra_test.go:103` (the "monorepo" fixture proving `gregale scan` already handles nested markers); `cmd/gregale/commands_decompose.go:39-97` (the `gregale scan` command)

## Context

`pkg/builderd/detect.go:29-95` and `cmd/gregale/pack.go:130-164` both inspect only **top-level** entries of the tarball / current directory. A repo whose root has no marker but contains nested `package.json` / `go.mod` / `requirements.txt` files (a common monorepo layout — `apps/web/package.json`, `services/api/go.mod`, etc.) hits the `FrameworkUnknown` path. Today the customer sees:

```
no deployable source found in <dir>: expected package.json, requirements.txt / pyproject.toml /
Pipfile / setup.py, go.mod, or Dockerfile at the project root for an *app*, OR a single
handler.{js,ts,py,go} for a *function* — or pass --image, --tarball, --template, --repo,
--function, or --app
```

(`cmd/gregale/pack.go:482-487`, surfaced via `resolveDeployShape`'s `shapeUnknown` branch). Technically correct but unhelpful for a customer whose source IS deployable, just not in a top-level layout.

The fix for an implicit cwd deploy remains **not** to guess among nested
markers — that's a deeper architectural question. The explicit `--path`
case is now covered by ADR-144: when the operator names a recognized
workspace member, the CLI may retain the repository context and send its
source root. This ADR still governs the no-selector hint path.

## Decision

1. **CLI-side depth-2 hint.** When `resolveDeployShape`'s top-level detector returns `shapeUnknown`, do a cheap depth-2 walk and, if a nested app marker is found, append a `Hint: looks like a workspace — try: gregale scan --path .` line. Otherwise the error message is unchanged. Purely additive; the existing error contract (return path, message format, exit code) is preserved.

2. **Single source of truth for the marker set.** Extract `var appMarker = map[string]bool{...}` in `cmd/gregale/pack.go`. Used by both `detectFramework` and the new `detectNestedMarkerHint`. The set is the same closed list `detectFramework` uses today: `package.json / requirements.txt / pyproject.toml / Pipfile / setup.py / go.mod / Dockerfile`.

3. **Depth limit = 2.** A customer with `apps/web/package.json` (depth 2) gets the hint. A customer with `apps/web/services/api/package.json` (depth 3) does NOT — they get the existing bare error. Rationale: cheap; matches the customer mental model of "my top-level is one folder per service"; deeper detection belongs to `gregale scan` (which already handles it via `pkg/reposcan`).

4. **Excluded dirs at every depth.** `node_modules`, `.git`, `vendor`, `__pycache__` are skipped, so a repo with `node_modules/<pkg>/package.json` does not false-positive as a workspace. Reuses the existing `defaultExcludeDirs` map.

5. **`--json` contract preserved.** Hint NEVER appears on stdout (would corrupt the JSON envelope). The hint lives in the error message string AND in a wrapped `*NestedMarkerHintError` typed error. `printErr` uses `errors.As` to extract the hint and writes it to stderr; the JSON envelope on stdout carries only the existing `{"error": "<message>", "code": "no_deployable_source"}`. Stable error code unchanged (the hint is a hint, not a new error class).

6. **No wire-shape change for the hint path.** The explicit workspace
   source-root wire contract is defined separately by ADR-144; this ADR's
   implicit cwd hint remains a CLI-only behavior.

7. **Server-side detector remains top-level by default.** Its workspace-aware
   extension is only selected when ADR-144 supplies an explicit
   `source_root`; the legacy empty-root contract stays load-bearing for the
   §4.5 build pipeline.

### Rationale

- **Why CLI-side and not server-side hint in PlanResponse.warnings[]?** The deploy path doesn't traverse `pkg/reposcan` (it goes straight to `pkg/builderd`). Reusing the warnings list would force a wire-shape change on `DeploymentResponse` for a CLI-only UX improvement. CLI hint is purely additive.
- **Why depth 2 not depth ∞?** A nested-walk that recurses to arbitrary depth brings the CLI's detector into parity with `pkg/reposcan`, but at the cost of replicating its rules. The customer-visible benefit is small (a 3-deep workspace is rare; `gregale scan` already handles it). Depth 2 catches the common case (one folder per service) cheaply.
- **Why a typed error and not just an error-string suffix?** `--json` consumers may want to programmatically detect the hint (e.g. a dashboard wrapper that auto-runs `gregale scan` on hint). `errors.As` to a typed struct is the Go-idiomatic way to expose machine-readable metadata alongside a human-readable message.
- **Why not just expand the existing top-level detector?** Top-level-only is load-bearing for the §4.5 build pipeline. A `package.json` at `apps/web/` is a different deployment than one at root (different cwd, different builder VM, different runtime). Letting deploy silently pick the nested one would surprise customers.

## Consequences

- A customer running `gregale deploy` against a monorepo with depth-2 markers sees the hint and runs `gregale scan --path .` instead of opening an issue.
- A customer running against a deep (depth-3+) monorepo sees the existing bare error — strictly no worse than today.
- A customer running against an excluded-dir-only repo (e.g. `node_modules/x/package.json` with no other markers) sees the existing bare error — `defaultExcludeDirs` filters them.
- New code: one helper (`detectNestedMarkerHint`) + one typed error (`*NestedMarkerHintError`) + one stderr branch in `printErr`. ~50 LOC.

## Rollback

Revert `cmd/gregale/pack.go`, `cmd/gregale/pack_test.go`, `cmd/gregale/deploy_shape_e2e_test.go`, `cmd/gregale/commands2.go`. No schema migration, no SDK regen, no open PR is blocked.

## Verification

Load-bearing acceptance gate: `go test ./cmd/gregale/ -run TestResolveDeployShape_NestedMarkerHint -v`. A regression that drops the hint, suppresses it under `--json`, or false-positives on `node_modules/x/package.json` fails this gate. Plus the existing `TestResolveDeployShape_Unknown` regression guard — the bare-error case must remain unchanged for repos with zero markers at any depth.
