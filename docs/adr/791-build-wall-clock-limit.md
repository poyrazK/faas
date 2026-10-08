# ADR-791 — The build wall-clock limit is 15 minutes

- **Status:** accepted
- **Date:** 2026-10-08
- **Milestone:** production hunt #7 (H5-71)
- **Amends:** spec §4.5 ("Timeouts: 10 min build, 15 min end-to-end")

## Context

Spec §4.5 and CLAUDE.md state a 10-minute build limit and a 15-minute
end-to-end deploy limit. `pkg/api/limits.go` has carried
`BuildTimeoutSeconds = 900` since 2026-08-18 ("cold rootless Railpack export
needs headroom"), without an ADR. On production-us (rc.247):

- a Node build whose build script slept 11 minutes succeeded after 883 s and
  went live 16 m 44 s after the deploy started;
- a build script sleeping 17 minutes was killed after 911 s, classified
  `timeout`; the previous release stayed live.

No server-side deadline enforces the 15-minute end-to-end figure.
`BuildE2ETimeoutSeconds` (also 900) only sizes the CLI's wait
(`defaultDeployWaitTimeout` = e2e + 5 minutes).

## Decision

- The guest build wall-clock limit is 15 minutes (`api.BuildTimeoutSeconds`,
  overridable per host with `build_timeout_seconds` in `builderd.toml`; unset
  on production-us). A build over the limit is killed and recorded as
  `failure_class=timeout`; the previous release keeps serving.
- "15 min end-to-end" is not a platform guarantee. A deploy spends the build
  time plus scan, snapshot, readiness and hosting verification; the CLI waits
  `BuildE2ETimeoutSeconds` + 5 minutes before reporting a timeout and the
  server keeps working.

## Consequences

- Spec §4.5 and CLAUDE.md now state 15 minutes.
- A builder slot can be held for up to 15 minutes by one build, which bounds
  queueing behind it (one guaranteed slot per node).
- Customers see `build exited 124` for a timed-out build until timeouts carry
  a typed error code (tracked as H5-66).
