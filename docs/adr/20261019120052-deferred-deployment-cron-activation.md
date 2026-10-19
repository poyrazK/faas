# ADR-20261019120052: Activate manifest HTTP crons after deployment readiness

Date: 2026-10-12
Status: Accepted

## Context

ADR-20261019120051 makes cron staging recoverable, but activates new schedules before a
build succeeds. Those paths may not exist in the currently serving release.
Waiting for a CLI to apply schedules also fails when its process exits.

## Decision

New saved single-app deploys use `prepare` and `cancel` on the existing guarded
cron endpoint. Preparation retains the immutable before/desired intent, advances
the HTTP cron epoch, and leaves active schedules unchanged. One durable intent
per app bounds storage; a newer preparation supersedes older pending intent.
Cancellation seals a late preparation and preserves concurrent schedule edits.
An accepted submission cannot be canceled through this endpoint.

APID polls accepted prepared intents every five seconds, including immediately
at startup. The durable submission receipt binds an intent to its deployment.
The CLI registers an owned upload before discovery or commit. A committed upload
can bind the intent even when its original deploy used another attempt identity.
Before registering that proof, the CLI durably marks upload acceptance uncertain.
This fences premature policy cleanup if it crashes before receipt discovery;
definite rejection can still clean up. The actual deployment send checkpoint
remains separate from configuration RPCs.
Upload commits now carry the saved attempt identity into deployment creation;
replaying a committed upload attaches an immutable receipt for that attempt.
Source-manifest ingestion skips HTTP cron staging when this exact saved attempt
already owns a prepared intent. Other trigger types retain their existing flow.

Activation requires the newest production candidate to be live, serving 100% of
production traffic, with no competing live release or unfinished rollout.
Dark and nondefault-scope releases do not activate; later dark/preview releases
are excluded from production ordering. Failed, canceled, superseded or aborted
releases discard the intent. The account, app, intent and deployment guards are
locked for the entire transaction. The current cron epoch and complete before
set must still match. Quotas are checked against the current account plan, including unchanged desired sets.
A savepoint rolls back all row changes on a quota or identity conflict.
Schedules and activation status commit atomically, so competing workers and
restarts activate once. Scheduler firing bookkeeping remains independent.

The CLI saves an opt-in deferred journal bit. Previously saved journals continue
using stage/restore, preserving ADR-20261019120051 recovery. New JSON receipts expose
`cron_activation`; configuration is incomplete while activation remains pending.
Resume reads the durable status without creating another build. For tracked uploads,
it prefers the atomic submission receipt before upload bookkeeping, then falls
back to an owned committed-upload receipt when needed. Conflicts and
discarded intents require inspection and a new deploy; resume never overwrites
operator edits or silently refreshes the captured before set.

## Consequences and limits

Preparation needs the additive database migration and an APID version supporting
these operations before rolling out the CLI. Activation can lag readiness by one
poll interval. Current schedules stay active throughout builds and partial
canaries. `--no-wait` and disconnected CLI processes need no further action for
an eligible release. Product notifications/audit follow successful commits.

This slice covers HTTP cron changes managed by saved single-app CLI deploys.
Project apply and standalone server-side source-ref manifests retain their prior
staging behavior. Explicit zero-traffic CLI deploys skip preparation, including --no-traffic and --traffic-percent=0. A prepared intent
for another environment is discarded rather than changing production schedules.
An intent with unknown acceptance remains prepared and cannot activate without
a receipt. Pending storage cascades away when the app row is deleted.

## Validation

Shared memory/PostgreSQL tests cover unchanged schedules during preparation and
build, failures/cancellation, partial canaries, dark releases, supersession,
operator edits and concurrent activation. Process-kill CLI recovery covers lost
prepare/cancel replies and delayed requests without the original source tree.
Legacy stage/restore guards remain covered by ADR-20261019120051 tests.
