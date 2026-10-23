# ADR-20261022194510 · Contained cron fire-now and captured cron requests

- **Status:** accepted
- **Date:** 2026-10-22
- **Amends:** [ADR-20261020150000](20261020150000-rehearsal-contained-release-commands.md), [ADR-20261020200000](20261020200000-rehearsal-crons-fire-from-the-recipe.md)
- **Decision:**
  - A contained rehearsal app's command-cron fire-now runs as the run's pinned contained command, and a recipe can fire one with `trigger_cron`.
  - A guest's `POST /v1/crons/{id}/run` is captured, never fired.
  - Job runs are refused in a contained run.
- **Why:**
  - A command-cron fire-now on a contained app failed: the admission trigger refused the raw `cron` task, so the request ended `failed`. ADR-20261020200000 left this as a separate increment.
  - A guest that fires a cron or starts a job through `api.svc.gregale` was blocked. Firing a production cron from a rehearsal would run production work.

## Decision

### The platform pins a contained command cron

When schedd turns a fire-now into a task for an app with a contained policy, the task becomes that run's contained command envelope:

- the cron's own argv (`/bin/sh -lc` for a shell cron), with no env or files;
- on the live deployment;
- `max_output_bytes` at the receipt budget, no shell, and `retry_max = 0`.

A cron retry reuses its task row, and a recorder receipt is one-shot, so a contained cron never retries.

The task keeps kind `cron` and its `cron_id`, so the cron's run history shows it.

**Admission (migration `20261023105713408`)** accepts this third shape: kind `cron`, no retries, a live deployment, and an envelope that equals the cron's own command. It reuses `scenario_rehearsal_release_matches`, which compares any pinned argv. `scenario_rehearsal_execution_active` also accepts kind `cron`. The memory store applies the same admission.

**apid** refuses a contained app's command-cron fire-now with `503` while rehearsal command execution (`FAAS_REHEARSAL_COMMAND_EXECUTION`) is off. Otherwise the request would be accepted and then fail in schedd.

### Recipes fire a cron with `trigger_cron`

Rehearsal apps receive no crons from the manifest: manifest crons name the production app, and command crons come only from the API. So the recipe declares the cron:

```yaml
command_execution: {version: 1, runtime: contained}
trigger_cron:
  command: [bin/sync, --once]   # or [script] with shell: true
  timeout_seconds: 300
```

In the trigger phase, after `trigger`, the CLI runs these steps:

1. It creates the command cron on the run's primary app. The app is enrolled, so the clock never fires it.
2. It fires the cron now and waits for the fire-now request to name its task.
3. It follows that task with the same checks as a contained command: identity, pinned intent, recorder receipt and exit status.
4. It records the result as stage `cron` and deletes the cron.

`trigger_cron` requires `command_execution`.

### A guest's fire-now is captured

`POST /v1/crons/{id}/run` on the run-local API is checked like apid: a UUID, the plan's cron gate, and a cron of the run's account. It is then captured as platform call kind `cron_run`, target `cron:<id>`. A guest names a cron from its configuration, usually a production one, so the ID is the same in both rehearsal runs.

| Request | Answer |
|---|---|
| The fire-now | `202` with the capture as `request_id` and status `pending`, as apid answers |
| `GET /v1/cron-fire-now-requests/{id}` | The run's own captures only, always `pending`, since they never fire |
| A repeated `Idempotency-Key` | The first response, as apid scopes the key to method and path |

The kind is added to the platform call CHECKs by the not-yet-released migration `20261022170348390`.

### Jobs are refused

`POST /v1/jobs/{name}/runs` and `/operations` answer `501`, "Jobs unavailable in a rehearsal". A Job runs its own OCI image in a job VM, with no app deployment. That is outside the containment boundary, so it can be neither pinned to the run nor performed. Its work can run as a contained command from the recipe instead.

## Consequences

- An HTTP cron's fire-now still dispatches as an invocation. A recipe drives an HTTP cron with an ordinary request (ADR-20261020200000).
- A contained command cron bound to an exclusive operation is not pinned; that path is unchanged.
- The migration's down step refuses while contained cron tasks exist; they are retained run evidence.
