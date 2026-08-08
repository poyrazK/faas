# ADR-087 · Runtime-version inference on `gregale deploy` (issue #740 / DEPLOY-PROV-5)

- **Status:** accepted
- **Date:** 2026-08-08
- **Issue:** #740 / DEPLOY-PROV-5
- **Supersedes:** none (extends ADR-038's `build_provenance` surface; explicitly does NOT contradict ADR-052's `runtime_version` rejection, which was scoped to `BuildManifest`)
- **Related:** ADR-038 (`build_provenance` populator pattern); ADR-052 (the `runtime_version` rejection — the rejection is for BuildManifest, not for build_provenance); ADR-083 (the function-vs-app shape distinction that constrains the function path here); `pkg/builderd/detect.go:41-95` (the existing detector this extends); `cmd/apid/handlers.go:98` (the runtime whitelist — explicitly NOT changed)

## Context

`pkg/builderd/detect.go` and `cmd/gregale/pack.go` both inspect only the **filenames** of the project root — they pick `Node | Python | Go | Docker`, never a version. Today:

- **Functions** require an explicit `--runtime node22 | node24 | python312 | python313 | go124 | go124-alpine` (apid whitelist at `cmd/apid/handlers.go:98`). The auto-detect path (`inferFunctionRuntime` at `cmd/gregale/pack.go:381-410`) emits `node22` and `python312` based on the handler extension alone. If a customer has `engines.node: ">=24"` in their `package.json`, they don't know that 22 was picked until the build log.
- **Apps** have no version at all — the `PrintOK(osStdout, "Detected: app, framework=%s", fw)` banner at `pack.go:646` renders `framework=node` and nothing else. The OCI base ref (`FAAS_DEPLOY_BASE_REF_NODE22`) is what actually pins the runtime, but the customer has no way to see what was selected without reading builderd logs.

ADR-052 (`docs/adr/052-add-a-runtime-procedure.md:114-120`) explicitly rejected adding a `runtime_version` column to `BuildManifest` because the runtime version is bound by the OCI base ref (digest-pinned via `FAAS_DEPLOY_BASE_REF_<RUNTIME>`). That rejection was correct *for the build manifest* — the build manifest is consumed by the build pipeline and an inconsistent column would break the §4.5 pipeline contract.

The version we surface here is different. It's **the version the customer's source declares they want**, not "what the build is going to run". A customer who writes `engines.node: ">=24"` is the source of truth for their intent; we surface that intent so the operator can debug "why did my app pick Python 3.11?" without reading builder logs. The build either runs on the right OCI base (by luck) or the wrong one (the existing behaviour); this change adds the observability, not the enforcement.

## Decision

A hybrid persistence model: server-side authoritative parse onto `build_provenance.framework_version`, with a client-side informational banner that renders before the multipart POST. The wire shape for customers is unchanged — `api.CreateAppRequest` / `api.DeploymentResponse` / SDK are all preserved. The new field is operator-only metadata (visible via `gregale build provenance <id>`).

### Sub-decisions

1. **Source of truth = server-side parse (`pkg/builderd`).** Mirrors `pkg/builderd/detect.go:41-95`; re-reads the canonical tarball bytes that apid already spooled. No trust boundary — the server reads its own copy. All three callers of `apidsource.Enqueue` (apid multipart, githubd bridge, post-ADR-050 provision apply) benefit without per-caller changes.
2. **Wire-shape unchanged.** `api.CreateAppRequest` / `api.DeploymentResponse` / SDK are unchanged. The persistence target is `build_provenance.framework_version`, surfaced via `gregale build provenance <id>` (already exists). No SDK regen follows.
3. **CLI banner is local + informational.** `cmd/gregale` pre-walks the cwd before the multipart POST (memory: #737's "Detected: … banner must render before the network round-trip"). The server independently re-derives for the persisted column; the banner is informational, never authoritative.
4. **Parser scope: `.nvmrc` | `.python-version` | `.tool-versions` | `package.json::engines.node` | `pyproject.toml::requires-python` | `go.mod::go X.Y`**. Dockerfile `FROM` is explicitly out of scope (multi-stage builds with different base tags would false-positive). Priority order per framework is `version-manager file → marker-embedded → ""` (e.g. `.nvmrc` wins over `engines.node`).
5. **Function path stays explicit.** No auto-version-selection for functions per the issue body and #737 / ADR-083. Only the *error message* gets a "Detected Node project — try `--runtime node22 --handler handler.handler`" suggestion when the marker is readable. The auto-detect path (`inferFunctionRuntime`) is unchanged.
6. **Version is best-effort, never an error.** Any parse failure (malformed JSON, missing fields, comments-only `pyproject.toml`) returns `""` and the build continues. The build pipeline never reads the column; a `NULL` value is the loudest signal that inference failed.
7. **Rollback is column-drop safe.** The new column is nullable; existing rows are unaffected. Reverting the migration + four source files restores the pre-#740 state.

### Persistence targets

| Layer | File | Change |
|-------|------|--------|
| Migration | `migrations/00165_build_provenance_framework_version.sql` | `ALTER TABLE build_provenance ADD COLUMN framework_version text` + partial index |
| Schema | `schema.sql` | Mirror the migration |
| State | `pkg/state/types.go` | Add `FrameworkVer string` to `BuildProvenance` |
| SQL | `pkg/state/pgstore.go` (`CreateBuildProvenance`, `BuildProvenanceByBuildID`, `scanBuildProvenance`) | Thread the new column through INSERT / SELECT / Scan |
| DTO | `pkg/api/dto.go` | Add `FrameworkVer string` to `BuildProvenanceResponse` |
| apid | `cmd/apid/handlers_ext.go` (`buildProvenanceResponse`) | 1-line mapper |
| CLI | `cmd/gregale/commands_builds.go` (`printProvenance`) | New row `framework_version` |

### Parser location

`pkg/builderd/detectversion.go` (new, ~120 lines) — independent of `detect.go` so the existing build pipeline contract is unchanged. `DetectWithVersion` is a sibling to `Detect` (existing `Detect` is unmodified). The CLI gets a parallel `detectFrameworkVersion` in `cmd/gregale/pack.go` that mirrors the priority order; both parsers return `""` on any error.

### Why this is NOT a wire-shape change

ADR-052 rejected `runtime_version` on `BuildManifest` because the build pipeline reads that column. The new `framework_version` is on `build_provenance` (the post-mortem observability table) and is **never read by the build pipeline** — it's stamped by builderd and read by apid/CLI for display. The trust boundary ADR-052 cited doesn't apply: there's no pipeline consumer that could be misled.

## Consequences

### Positive

- Customer sees the source-declared version on the banner AND in `gregale build provenance <id>`.
- Operator can debug "why did my app pick Python 3.11?" without builderd logs.
- The runtime whitelist (`cmd/apid/handlers.go:98`) is unchanged — functions still require explicit `--runtime`.
- No SDK regen. No migrations impact customer-facing tables.

### Negative

- One extra tarball walk per build (cheap — `detect.go` already walks top-level entries; the version walk is a second pass).
- The CLI banner and the server-stored value may disagree by accident (unlikely, but possible). The banner is documented as informational; the server wins on persistence.
- A future `apps.runtime_version` column would now conflict with `build_provenance.framework_version`. Mitigation: §3 explicitly names `build_provenance.framework_version` as the customer-visible version surface; the runtime version (`apps.runtime`) is operator-controlled and pipeline-binding.

## Rollback

Revert the migration (column drop is non-destructive on existing rows) + the four source files. SDK regen not required. The CLI banner falls back to the pre-#740 `Detected: app, framework=node` shape.
