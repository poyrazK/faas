# ADR-1288 · Project apply honors a Compose service's declared build context

- **Status:** accepted
- **Date:** 2026-10-14
- **Scope:** `pkg/apid/apidserver` project apply staging, `pkg/reposcan`
- **Related:** ADR-144 (workspace build context), ADR-090 (repo decomposition), hunt #10 H10-25

## Context

Project apply stages one source archive per workload. Since #2319 every
workload's archive is the whole repository, and the deployment's
`source_root` tells the builder which directory to build. That serves
workspace monorepos (ADR-144): a Dockerfile in `apps/api` can `COPY` the root
lockfile and sibling packages.

Docker Compose declares the build context explicitly. For
`build: ./web` (or `build: { context: services/backend }`), Docker sends only
that directory, and a Dockerfile written for it says `COPY server.js .`. On
production-us every such service failed with `/server.js: not found`, because
the builder used the repository as the context (H10-25).

The first fix (#4696) applied the Compose rule to every Dockerfile workload in
a project whose scan source is compose. That re-rooted workloads that the
scanner found beside the Compose file but that the file does not declare. The
release-gate e2e test `TestApplyProject_Builds_StagedTarballPreservesRepository`
caught it: a Dockerfile worker next to a one-service Compose file lost its
repository workspace.

## Decision

A workload whose scanner detection is Compose (`reposcan.DetectorCompose`),
which builds from a Dockerfile in a non-root directory, and which is not an
image service has that directory staged as its archive root with an empty
`source_root`. This is the context Docker Compose would use.

Every other workload, including Dockerfile workloads in a compose-sourced
project that the Compose file does not declare, keeps the whole-repository
archive and its `source_root` (ADR-144).

## Consequences

- Compose Dockerfile services build as they do under `docker compose build`.
  They cannot reach files outside their context, and Docker Compose cannot
  either.
- A workload's archive depends on how it was detected, not on the project's
  aggregate scan source.
- The e2e test now pins both shapes: the Compose-declared service's archive is
  its context, and the undeclared worker's archive is the repository.

## Rejected alternatives

- **Keep the repository archive and pass the context as a separate build
  setting:** it needs a new deployment field carried through builderd and
  guest-init for the same observable result, and the archive would still ship
  files Compose never sends.
- **Key on the project's scan source:** that was the #4696 behavior. It
  re-roots undeclared workloads, as described above.
