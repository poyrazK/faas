# ADR-230 · Deployment-attached application tasks

- **Status:** accepted
- **Date:** 2026-09-23
- **Decision:** Add a first-class `app_task` intent for running one command
  against one immutable application deployment. A task pins the owning
  account, app, deployment, rootfs storage key, image digest, environment
  scope, command, execution mode, timeout, and output budget at admission.
  `schedd` will claim queued tasks with an opaque lease and boot each task in a
  fresh Firecracker VM. A task VM is destroyed after the command exits and is
  never added to serving routes, parked, pooled, or snapshotted.
- **Why:** Release commands and operator-triggered one-off commands need the
  deployed application's dependencies, environment, secrets, managed-resource
  bindings, and network policy. Gregale's existing disposable `execution`
  resource deliberately provides none of those: it evaluates caller-supplied
  source in a sanitized, networkless runtime snapshot. Expanding that contract
  would weaken a useful isolation boundary. Jobs already prove the
  run-to-completion VM lifecycle, but their durable identity is an independent
  OCI image rather than an application deployment. App tasks therefore get a
  separate intent model while reusing the jobs scheduler and VM mechanics in a
  follow-up.

## Durable contract

The lifecycle is:

```text
queued -> restoring -> running -> succeeded
   |          |           |----> failed
   |          |           |----> timed_out
   |          |           `----> cancelled
   |          |----> failed | timed_out | cancelled
   `----> cancelled
```

`schedd` claims oldest-first with `FOR UPDATE SKIP LOCKED`. `restoring` is a
pre-dispatch state: an expired restoring lease may be returned to `queued`.
`running` is the at-most-once fence: an expired running lease is failed and is
never replayed. Terminal rows are immutable. Cancellation of queued work is
immediately terminal; cancellation of restoring or running work records a
request that the scheduler must acknowledge after VM teardown.

Task kinds initially form the closed set `manual` and `release`. Manual tasks
may be created more than once for a deployment. A deployment has at most one
release task, which gives the release orchestrator an idempotency fence without
serializing unrelated manual commands.

The task stores the deployment's rootfs key, image digest, and environment
scope rather than following a mutable "current deployment" pointer later.
Environment values, secret plaintext, and credentials are not copied into the
row. The scheduler must resolve them through the existing scoped wake-time
delivery path immediately before boot. Versioned release configuration is a
separate release-ledger decision; this ADR does not pretend mutable external
database state can be rolled back.

## Delivery sequence

1. This change lands the schema and the shared PostgreSQL/MemStore lifecycle.
   No public route is mounted, so an operator cannot enqueue work before a
   scheduler can execute it.
2. A scheduler-only follow-up adds the bounded app-task coordinator, lease
   renewal, cancellation race fence, restore-to-running dispatch boundary,
   output bounds, and teardown-before-acknowledgement contract behind a runtime
   interface. It remains disabled until that interface has a production
   implementation.
3. A vmmd/guest follow-up implements that runtime interface by reusing jobs'
   admission, fresh cold boot, exit capture, metering, and teardown primitives.
   It must load the app's scoped env, secrets, bindings, and network policy
   without routing the VM as a serving instance.
4. The public `POST /v1/apps/{slug}/tasks` and `gregale app exec` surface ships
   behind a runtime gate after the dispatcher and metal isolation tests land.
5. Release phase becomes the first internal consumer: `release.command` and a
   Procfile `release:` entry enqueue a `release` task for the candidate
   deployment, and deployment activation waits for its successful terminal
   state.

## Consequences

- App tasks and source executions keep distinct security contracts and quota
  surfaces.
- A deployment artifact cannot be selected implicitly after admission; the
  task row remains sufficient to explain what code was asked to run.
- Queue recovery can replay restoration but never an already-running command,
  which avoids running non-idempotent migrations twice after a host failure.
- Release commands must themselves be safe to retry only before dispatch. A
  lost running VM fails the release and leaves the previous deployment live.
- Interactive PTYs, detached sessions, retries, schedules, and task fan-out
  are outside this ADR. They may build on the same primitive without changing
  release-phase semantics.

## Rejected alternatives

- **Add an app mode to disposable executions:** their sanitized snapshot,
  fixed environment, no-network rule, and source payload are intentional
  security properties, not missing app-task features.
- **Create a temporary public Job:** jobs own an independently materialized
  image and user-defined env. Mirroring an app into that model would duplicate
  artifacts, lose app bindings, and expose release orchestration as customer
  job clutter.
- **Run inside a serving VM:** this introduces cross-request state, lets an
  operator command contend with traffic, and makes teardown or rollback
  ambiguous.
- **Run during the build:** a release command needs runtime bindings and must
  gate activation of an already-built candidate. Builder isolation has neither
  the right identity nor the right lifecycle.
