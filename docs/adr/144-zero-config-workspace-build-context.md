# ADR-144 · Zero-config deploys retain the workspace build context

- **Status:** accepted
- **Date:** 2026-09-05
- **Scope:** `cmd/gregale`, multipart source deploys, `builderd`, guest-init, deployment state
- **Related:** ADR-086 (nested-marker deploy hint), ADR-088 (shared marker detection), ADR-090 (repo decomposition)

## Context

`gregale deploy --path apps/api` previously uploaded only the selected
directory. That is sufficient for a self-contained service, but it drops the
workspace lockfile, root build configuration, and sibling packages needed by
common monorepo builds. The CLI also had no way to tell the server or builder
which nested directory should be detected and built after uploading a larger
repository context.

## Decision

When an explicit `--path` points at a deployable member of a workspace graph
recognized by `pkg/reposcan`, the CLI uploads the repository as a flat,
repository-relative source archive and sends `source_root` with the selected
member path. The server validates that the path exists in the archive and
persists it on the deployment row. Builderd carries the value into the build
manifest; guest-init extracts the repository at `/build/src`, runs the build
from `/build/src/<source_root>`, and gives BuildKit the full repository as its
context.

Self-contained `--path` deployments and existing tarball/source-ref callers
keep the empty-root contract. Their historical single-directory archive
wrapper and resumable-upload behavior remain unchanged. Workspace-context
uploads stay on multipart until the resumable commit protocol can carry the
same metadata.

## Consequences

- Workspace lockfiles and sibling packages are available to Railpack and
  Dockerfile builds without requiring a project-plan deployment.
- Framework/version detection and stateless-shape checks are scoped to the
  selected member, while the archive remains subject to the existing source
  exclusions, secret scan, file/size caps, and tar safety validation.
- Deployment rows and API responses expose `source_root` for inspection and
  retry continuity; empty values remain omitted on the wire for compatibility.
- The upload is repository-context wide, not dependency-closure aware. A
  future optimization may compute a smaller safe closure, but it must preserve
  the same explicit root contract.

## Rejected alternatives

- **Upload only `--path`:** breaks workspace package-manager resolution and
  root-level build configuration.
- **Infer a nested root from the cwd:** changes the established zero-config
  behavior and can select the wrong workload; explicit `--path` is required.
- **Enable resumable uploads without extending their metadata:** would lose
  `source_root` at commit time and build the wrong directory.
