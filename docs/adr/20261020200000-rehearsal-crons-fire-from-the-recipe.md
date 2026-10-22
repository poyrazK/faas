# ADR-20261020200000 · Rehearsal crons fire from the recipe, not the clock

- **Status:** accepted
- **Date:** 2026-10-20
- **Amends:** [ADR-20261019180000](20261019180000-rehearsal-side-effects-stay-inside-the-run.md), [ADR-20261017201008](20261017201008-contained-rehearsal-commands.md)
- **Decision:** schedd never fires an enrolled app's crons from the clock. A rehearsal exercises cron behaviour explicitly from its recipe, so both revisions see the same work.
- **Why:**
  - **Nondeterminism.** Clock-driven fires made the evidence depend on wall-clock timing. A run that happened to span one more fire produced different managed work, events or rows than its sibling run.
  - **Failure loop.** For a contained app, a command cron fire inserts an ordinary task, which the contained-command admission fence refuses. The cron then failed again on every scheduler tick.

## Decision

- schedd's cron listings, `ListOwnedCronsByNodeID` and `ListEnabledCrons`, exclude apps that have a `scenario_test_action_policies` row. HTTP and command crons are both excluded, in capture-only and contained runs alike.
- The cron definitions stay. They are still created by the candidate's manifest and are visible through the API.

A recipe exercises a cron deterministically:

| Cron type | How the recipe exercises it |
|---|---|
| HTTP cron | Send the cron's request to the app's path, from a trigger or check. Contained recipe commands reach run members on the private HTTP port. |
| Command cron | Run the same command as a contained recipe command (`command_execution`). |

## Consequences

- A rehearsal never observes a cron firing on its own. A scenario that depends on periodic work must drive it from the recipe.
- Fire-now of an enrolled app's command cron in a contained run is still refused by containment admission. Pinning it as a contained command is a separate increment.

## Rejected alternatives

- **Firing on the clock and comparing counts.** The counts depend on timing, not on the revision.
- **Running clock fires as contained commands.** The evidence would still differ between runs by timing, and a failing fire would make an unrelated comparison inconclusive.
