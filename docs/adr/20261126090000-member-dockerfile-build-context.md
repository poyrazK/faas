# ADR-20261126090000 · A workspace member's own Dockerfile picks its build context

- **Status:** accepted
- **Date:** 2026-11-26
- **Scope:** `cmd/gregale` (`deploy --path`, `deploy --dry-run --explain-files`, `start` source review)
- **Amends:** ADR-144 (zero-config workspace build context)
- **Related:** ADR-1288 (Compose-declared build context)

## Context

ADR-144 uploads the whole repository when `gregale deploy --path <dir>`
selects a workspace member: the deployment's `source_root` names the member
and the builder uses the repository as the Docker context. That suits
Turborepo-style Dockerfiles, which `COPY` the root lockfile and sibling
packages.

Many monorepo Dockerfiles are instead written for their own directory, which
is what `docker build <dir>` and Compose's `build: ./<dir>` send. ADR-1288
fixed this for Compose-declared services in project apply, but a single
`deploy --app --path` still used the repository. On rc.381 an agent deploying
`aws-containers/retail-store-sample-app` with `--path src/ui` (an Nx project,
so a workspace member) failed with `"/.mvn": not found`. The agent then
copied the directory elsewhere and ended up deploying the upstream image.

## Decision

When the selected member has its own `Dockerfile`, the CLI reads that
Dockerfile's `COPY`/`ADD` sources. It skips stage copies (`--from`), URLs,
heredocs, `.`, parent paths and paths that use build variables. For each
remaining source it checks whether the path exists in the member directory,
at the repository root, or in both.

- If at least one source exists only in the member and none exists only at
  the root, the member directory is the build context. The CLI uploads it
  exactly as it uploads a non-member `--path`, with an empty `source_root`.
- Otherwise the repository context of ADR-144 stays. This includes a member
  without a Dockerfile, one whose sources exist in both places, and one with
  no resolvable sources.

`deploy` prints the reason on stderr. `--explain-files` shows it as
`Build context: …` and in JSON as `context_reason`.

## Consequences

- Dockerfiles written for `docker build <member>` deploy unchanged from a
  monorepo, matching Docker and Compose.
- Turborepo-style Dockerfiles keep the repository context, because their
  lockfile and `packages/` sources exist only at the root.
- The check is local and read-only. It needs no API, builder or deployment
  field change, so release skew does not apply.
- A Dockerfile whose sources are all ambiguous keeps today's behavior.

## Rejected alternatives

- **Always use the member directory when it has a Dockerfile:** this breaks
  Dockerfiles that copy the root lockfile or sibling packages, which ADR-144
  exists for.
- **A `--build-context member|repo` flag only:** a cold agent would still hit
  the failure and would have to learn the flag from the build error.
