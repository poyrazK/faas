# ADR-1542: Reviewed source-backed scheduled Jobs

- **Status:** accepted
- **Date:** 2026-10-16
- **Related:** ADR-568, ADR-099, ADR-1541

## Context

GitOps prepares held source and Dockerfile deployments, but schedules accept
only immutable OCI images. Customers must build and publish an image separately
before a reviewed cron workload can use it.

## Decision

1. Support `source` and `dockerfile` schedules through the existing verified
   provider archive and ephemeral builder microVM path. Function sources and
   Job queue bindings remain unsupported. Preserve all plan, scoped variable,
   secret-reference and service-binding restrictions. Require explicit reviewed
   `runtime.entrypoint` argv for source schedules, separate from `job_smoke`.
2. Create a durable paused Job during intent reconciliation. Its internal
   `gregale-source://<environment>/<app>/<commit>` reference identifies scoped
   reviewed source, never an OCI image. The source descriptor is additionally
   checked against the deployment's immutable frozen contract. OCI materializer
   discovery, direct claims and generic artifact publication cannot handle it.
3. After qualification succeeds, bind the Job to that graph member's exact
   deployment layer in the release activation transaction. Record an explicit
   `gregale-deployment:<UUID>` resolved identity in the existing artifact identity
   field; do not invent an OCI/content digest. The layer remains owned and
   retained by the deployment. A changed layer increments the schedule revision
   and resets its cursor. Failed publication rolls back binding and activation.
4. Occurrences freeze source reference, resolved deployment identity and storage
   key. Schedd checks the current scoped source, frozen build inputs and active
   release before cold boot and again before releasing the held guest command.
   A changed commit, source descriptor, artifact or release fails closed. Runs
   never pull source or rebuild. Serving still requires a matching successful
   post-activation occurrence. Rollback follows fresh review and qualification.
5. Keep production qualification dispatch and enforcement gated. State and fake
   VMM regressions are not evidence of exclusive native acceptance.

The migration also aligns two obsolete SQL constraints with the existing native
Job dispatcher: new claimed attempts require a reserved instance, and current
v3 smoke policies are accepted while historical v2 receipts remain stored.
Legacy claims without a VM identity survive migration but cannot satisfy current
execution evidence or renew without acquiring that identity. Old receipts
cannot satisfy current Go evidence checks. Release retention remains an
explicit app opt-in; this feature does not invent a retention default.

## Validation

Cover source/Dockerfile preparation for GitHub and GitLab, durable paused Job
identity, archive/commit and build fencing, materializer isolation, qualified
artifact binding, failed/superseded builds, release publication, occurrence
snapshots, restart checks, rollback and SQL guard/replay safety in both stores.
Native execution remains pending on an exclusive native x86_64 KVM host.

The guarded native runner now includes both providers and both source kinds,
with real builder execution, guest commit/run output, repeated occurrences
without rebuilding, restart between completed occurrences, held-build
supersession and fresh reviewed rollback. Its v2 report fingerprints the real
kernel and guest-init and binds all three pinned base-image references,
requiring every scenario's structured pass event. Implementation
and local validation of this harness are not a native acceptance pass; execution
and leak checks still require the exclusive native host.
