# ADR-20261019120053: Defer source deployment HTTP crons until production readiness

Date: 2026-10-12
Status: Accepted

## Context

ADR-20261019120052 protects saved single-app CLI deploys. Direct source-ref, multipart
tarball, legacy multipart and resumable upload deployments still create manifest crons before
their builds finish, allowing schedules to invoke paths absent from live code.

## Decision

Source-ref, multipart source-tarball, legacy multipart deployment and resumable
upload entry points prepare durable cron intent using the
ADR-20261019120052 store and activation worker. APID generates an internal attempt identity
when the client supplies none; deployment creation stores its acceptance receipt
atomically with the deployment. Existing saved CLI intent retains its identity
and ownership. Already staged legacy CLI journals also retain ownership and
their original revision, preserving rollback compatibility. Server preparation
preserves the source API's additive behavior:
existing HTTP crons and their options survive, duplicate schedule/path pairs
are ignored, and new schedules default to UTC. Command crons are unaffected.

Source ingestion never falls back to early cron writes, including when a
concurrent deploy supersedes prepared intent. Explicit zero-traffic and preview
deploys skip production cron preparation. Other trigger kinds retain their flow.
Definite rejection cancels server-owned preparation without touching schedules;
accepted deployments leave completion to APID. The receipt binds acceptance
before build notification, so a crash after acceptance requires no CLI recovery.
Readiness, traffic, rollout, cron revision, operator edit and quota guards remain
those of ADR-20261019120052.

## Consequences and limits

Requires the ADR-20261019120052 migration and APID worker. No extra migration or public API
field is required. Activation may lag readiness by five seconds. A crash before
acceptance can leave an unbound prepared intent; it cannot activate, storage is
bounded to one intent per app, and a subsequent preparation supersedes it.
Preparation precedes deployment acceptance, so the existing one-current-intent
policy can supersede an older pending intent even if the newer request is later
rejected. The prior intent is not resurrected. Project apply remains unchanged.
