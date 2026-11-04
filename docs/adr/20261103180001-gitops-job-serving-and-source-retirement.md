# ADR-20261103180001: GitOps Job serving and source retirement

- **Status:** accepted
- **Date:** 2026-11-03
- **Related:** ADR-568, ADR-20261017190802, ADR-20261017190808

## Context

The service caller lookup still required an image Job with no command, although
reviewed schedules support source and Dockerfile artifacts and explicit argv.
It also compared the running release with the controller's current approval,
so suspension, report mode and a newer approval interrupted existing calls.
Separately, keeping qualification history prevented a source from ever being
disconnected or rebound after qualification, even when native cleanup completed.

## Decision

1. Authenticate scheduled Job callers from the claimed occurrence and exact
   live member of the active release. Preserve account, project, environment,
   original source binding, frozen intent, schedule revision, run snapshot,
   lease and service-binding checks. Controller mode, suspension, approval
   generation and intent version do not withdraw that release's authority.
2. Accept immutable images and the existing source/Dockerfile deployment
   provenance predicate. Source Jobs must name the exact qualified deployment
   and its rootfs key. Commands must equal the reviewed runtime entrypoint;
   an absent entrypoint preserves the image's default command. Unbound Jobs
   and unclaimed occurrences receive no caller identity.
3. In report mode, permit disconnect or rebind after qualification's physical
   owners have retired. Every execution journal for the original source must
   carry retirement evidence, and any associated instance must be terminal.
   Inspect restore executions and prior attempts as well as the request's
   current reserved instance. A terminal instance alone is not cleanup proof.
   Keep the existing pending-effect, graph-preparation and workload-retirement
   fences. Report mode and the source lock fence further admission.
4. Preserve qualification requests, execution journals, snapshot receipts and
   artifacts. Retirement revokes the old binding's ownership and new execution
   authority; rebinding requires fresh approval and adoption. Disconnecting
   also removes the original binding required for private service aliases.
5. Keep customer enforcement and native qualification gates closed until their
   dedicated native acceptance requirements are satisfied. Database fixtures
   and modeled receipts do not establish native acceptance.

## Validation

Exercise service-bound image, explicit-command image, source and Dockerfile
scheduled occurrences through the real PostgreSQL owner APIs. Verify caller
identity and routing survive suspension, report mode and a newer approval,
and disappear when the task is no longer claimed. Cover disconnect and rebind
with completed capture/restore history in both stores, preserve their receipts,
and reject both operations while either physical owner still needs cleanup.
