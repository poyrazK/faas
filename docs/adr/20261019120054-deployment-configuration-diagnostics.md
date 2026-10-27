# ADR-20261019120054: Expose retained deployment configuration diagnostics

Date: 2026-10-12
Status: Accepted

## Context

A live release can still have deferred cron changes pending or blocked by an
operator edit or quota change. ADR-20261019120052/997 keep production schedules safe, but
the app's one-current-intent row is replaced by later attempts. Direct API
clients need a read-only diagnostic tied to the affected deployment, and saved
CLI recovery must not recreate preparation just to inspect an old result.

## Decision

GET /v1/deployments/{id} adds an optional `configuration` object. Its `complete`
field describes server-managed cron activation; CLI receipts additionally
include unfinished lifecycle/scaling steps in `configuration_complete`.
`configuration.crons` reports `not_managed`, `pending`, `activated`, `conflicted`
or `discarded`, with a stable reason, customer-facing message and recovery hint.
The existing account ownership, deployment read scopes and MFA protect reads.
Reads do not reconcile, mutate cron intent or create builds.

A deployment-owned `deployment_cron_outcomes` row retains the latest cron intent
revision, session identity, internal activation status and diagnostic reason.
The worker records outcomes atomically with intent status and schedule changes.
Preparation archives a replaced accepted intent before replacing the app row;
prepared intent becomes discarded with `intent_superseded`. Terminal results
retain their original explanation. Rows cascade with deployment deletion;
there is no independent unbounded event log. Reusing a committed upload can
bind a newer intent revision to the same deployment, replacing its latest result.

Reads also resolve the current app intent using the atomic submission receipt
or an owned committed upload, so pending status is visible immediately after
acceptance, before the worker's first poll. Pending reasons reflect the current
release status, production traffic and rollout. A readiness guard becoming
eligible remains pending until the worker commits activation. Operator edits,
quota changes and identity conflicts have distinct retained reasons.

CLI deployment inspection renders release status separately from configuration.
Saved deploy resume reads the deployment diagnostic, retains it in recovery
JSON and reports why activation is blocked. It never submits another build or
refreshes the captured before set. Older APID versions without this field keep
the prior staging-receipt compatibility path. Untracked historical deployments
report `not_managed`; the migration can backfill only still-retained bound intents.

## Consequences

Apply the additive migration before APID, then roll out CLI/SDK updates. Existing
response consumers remain compatible because the field is optional. The status
is a read snapshot, not a guarantee against a later operator edit. `activated`
records that the deployment's intent was activated; it is not reclassified when
a newer deployment subsequently replaces schedules. Terminal conflict recovery
requires inspection and a new deployment attempt; resume does not overwrite
operator configuration. Project reconciliation remains outside this slice.
