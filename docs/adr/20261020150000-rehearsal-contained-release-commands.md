# ADR-20261020150000 · Contained rehearsals run the candidate's release command

- **Status:** accepted
- **Date:** 2026-10-20
- **Amends:** [ADR-20261017201008](20261017201008-contained-rehearsal-commands.md), [ADR-20261017201009](20261017201009-rehearsal-postgres-fixtures.md)
- **Decision:** When an app enrolled in a contained rehearsal deploys a candidate that declares a release command, imaged pins that release command as the run's contained command and the deployment primes once it succeeds. A release that cannot run that way fails the deployment with an actionable reason.
- **Why:** Containment admission accepted only recipe commands (`kind='manual'` on a live deployment). A candidate's own release command, typically its database migration, was refused by the admission trigger. The deployment stayed in `snapshotting` without failing, so the change most worth rehearsing could not be rehearsed in contained mode at all.

## Decision

### Admission

- **Pinning.** imaged's `ensureReleaseTask` checks whether the app has a contained run (`ScenarioRehearsalNetworkRun`). If it does, imaged creates the release task as the contained command envelope:
  - `kind='release'`, so the prime gate still waits for it.
  - `Command = [__gregale_rehearsal_command_v1__, spec]`, where `spec.Command` is the deployment's release argv. Shell releases are wrapped as `/bin/sh -lc <script>`, exactly as ordinary shell tasks run.
  - No extra env or files, and `max_output_bytes = 65536`.
- **Admission trigger.** `scenario_rehearsal_command_admission` now admits two shapes:
  - a `manual` envelope on a `live` deployment, as before;
  - a `release` envelope on a `snapshotting` deployment whose command equals that deployment's release command, checked by `scenario_rehearsal_release_matches`.
- **Execution.** `scenario_rehearsal_execution_active` and the store's command-spec check accept `kind IN ('manual','release')`. Everything downstream is unchanged:
  - the command network identity;
  - the recorder session;
  - the guest receipt;
  - retirement before success;
  - containment of the command VM.
- **Database access.** The release runs with the deployment's own environment and secrets, including an isolated PostgreSQL fixture's sealed `DATABASE_URL`. It reaches the fixture through the contained PostgreSQL proxy.

### Gating

- imaged reads the same `FAAS_REHEARSAL_COMMAND_EXECUTION=1` gate as apid's recipe commands. When the gate is off, the candidate fails with `release_phase_unavailable` and an explanation instead of stalling.
- A durable admission refusal fails the deployment the same way: for example a run without simulation fixtures, or a release that cannot be encoded within the protocol bounds. Transient errors still return to the caller.

### Prime

- `releaseAllowsPrime` and the prime-observation mirrors accept either the verbatim release or this pinned contained envelope, through the shared `ReleaseTaskRunsDeploymentRelease` rule.
- A succeeded contained command already implies a closed recorder holding its exact receipt (migration `…011`).

## Consequences

- **What can be rehearsed now.** Data migrations and additive changes to declared fixture tables. Declare the migration tool's tracking table in the PostgreSQL fixture to compare which migrations ran.
- **What still can't.** The PostgreSQL collector still requires the database to hold exactly the declared tables, with supported column types and no extensions. A migration that creates a table, type or extension therefore leaves the run inconclusive. Making that comparable through a schema inventory is the next increment.
- **Rollout order.** Apply the migration, then roll out schedd and imaged. An older schedd rejects the envelope at prime, so the deployment fails closed in `snapshotting`.
- **Unchanged.**
  - Non-contained apps: the extra cost is one policy lookup per release-task creation.
  - Interactive tasks remain refused for contained apps.
  - Command crons and other ordinary app tasks remain refused for contained apps.

## Rejected alternatives

- **Running the raw release outside containment.** It would reach production dependencies with live credentials.
- **Converting the release into a `manual` recipe command.** The prime gate would no longer wait for the release, and recipes could forge one.
