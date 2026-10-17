# ADR-1346 · Pinned revision rehearsals

- **Status:** accepted
- **Date:** 2026-10-16
- **Decision:** Add `gregale test rehearse` to orchestrate two local Git commits through existing scenario runners and the ADR-1345 behavior comparator. Capture a single trusted manifest and declared fixture/observer files from the invoking checkout; overlay those files into both revisions. Use fresh source directories for every revision/profile/attempt and write one versioned combined receipt.
- **Why:** Manually running two variants does not ensure immutable revision selection, identical declared comparison inputs, fresh source state, or a reviewable association between revisions and results.
- **Consequences:** Both refs resolve before execution. The receipt links commit/tree identities, archive and materialized-source digests, trusted input/recipe digests, complete run receipts, comparison and cleanup outcomes. Existing app/service/database/bucket ownership and lifecycle evidence stay with Gregale Test. No worktrees, provider clone path, daemon or deployment promotion is introduced.
- **Rejected alternatives:** Switching the user's checkout risks local work and moving refs. Letting each revision select its own observer can conceal a regression. Reusing an extracted source directory lets builds and setup contaminate repeats. Claiming all external actions are captured without dispatcher interception and egress enforcement would exceed the evidence.

## Supported execution and trust

Version 1 accepts one behavior-enabled scenario and locally available commits.
`--repo` selects the checkout; the manifest path is relative to the invoking
directory. The user or CI supplies reviewed manifest, observer and fixture bytes;
working-tree changes to those declared inputs are intentional and fingerprinted.
Application source comes from Git archives, with Git replacement objects disabled.
Submodules, symlinks, non-regular files, out-of-repository sources and snapshots
outside the existing source archive envelope fail preparation. Archive attributes
apply; archive and materialized-source hashes identify the exact resulting bytes.

The selected recipe is parsed once. Declared fixture and observer files are
captured once before either run and copied into each fresh revision. Shared
harness scripts and their transitive imports must be declared as observers when
they are part of the trusted comparison. Other application code and configuration
come from their respective commits. Fixture fingerprints prove selected input
identity, not that an application loaded or correctly applied every fixture.

Real-VM runs reuse isolated, expiring scenario resources and require a total
workload-minute guard covering both revisions. Local runs require a managed
`local.command`; a shared pre-existing server cannot represent two revisions.
Simulation owns its fixture lifecycle. Host commands execute with the user's
existing permissions and supported environment; source directories are fresh
state, not a security boundary for hostile code. Cancellation stops active Unix
command process groups and follows existing runner cleanup; skipped slots remain
explicit and inconclusive. Process-group handling is also used by ordinary test
commands. Forced termination still relies on existing hosted resource expiry.

## Reporting and limits

Validate both revision snapshots before provisioning or executing a scenario.
Output files must be new and distinct, reserved before execution. The combined
JSON receipt embeds both sets of runs and the comparison; HTML and Markdown reuse
the existing comparison renderers with revision and input identities. Source
hashes cover configuration and migration files present in the snapshot; they do
not attest migration execution or database state. Failures, missing evidence,
interruption and cleanup errors cannot establish equivalence. Selected behavior
changes fail, and incomplete comparisons are inconclusive; both exit nonzero.

Portable acceptance compares two actual Git commits of the export fixture.
Only the candidate implementation changes; both executions pass their ordinary
checks, while the unchanged observer detects successful notifications increasing
from one to two. Reports retain `simulated` and `application_reported` provenance.

Managed-webhook interception, egress isolation, platform-observed collectors,
intentional-difference approval, nested resilience comparisons, production
snapshot consistency and migration contention remain separate increments.
Hosted/native qualification requires the existing supported acceptance hosts.
