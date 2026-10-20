# ADR-20261019120033: Native qualification of database-backed promotions

## Status

Acceptance implementation added. Designated native x86_64 KVM execution is pending.
Promotion and migration admission remain disabled by default.

## Context

ADR-20261019120025/1026 implement migration promotion and operator recovery, but portable
state tests cannot establish what a real guest executed against the destination
database. The existing promotion partitions do not exercise this path. A unique
SQL marker alone also hides duplicate invocations that fail before inserting it.

## Decision

Add a fifth, serialized `promotion-migrations` partition to the existing guarded
`project-environment-promotion-only` lane. Four independent native cases cover
success, application rollback, nonzero command exit after a committed expansion,
and uncertain execution followed by explicit operator resume.

Each case builds a single HTTP app through real builder guests. A paired release
establishes the destination baseline; a separate staging build declares one
immutable release command. Source and destination use separate disposable TLS
PostgreSQL databases and scoped read/write, reader and migration credentials.
Ordinary HTTP guests must lack migration credentials and fail DDL attempts.
The scratch image includes an adapter for exactly its Procfile shell invocation;
it runs the same SQL command and rejects other shell input, without depending on
utilities from the host's runtime base.

The command increments an invocation counter before attempting DDL. It expands
and backfills a column without removing legacy data, and commits an applied
marker in the same transaction. Source execution completes normally. Destination
customer SQL provides a result barrier: successful cases are released after
observing actual execution; the uncertain case holds its result until teardown.
These writes control the test application; no platform task/lease, health,
runtime-registration or publication evidence is inserted or changed by tests.

Lose the accepted HTTP response, replay its admission key, restart APID with the
task queued, disable new migration admission, and require the original task and
deadlines to persist. For held commands, restart APID while running. Successful
commands receive fresh full health deadlines, complete baseline/candidate rounds,
and survive another restart before cutover. Critical health endpoints read SQL;
an unchanged customer write counter across preflight checks that property.

A real candidate regression after expansion restores the previous application
graph and configuration. The expanded schema, invocation count and customer
data remain. A nonzero command exit preserves the baseline without publication.
Neither path executes reverse SQL or another release command.

For uncertainty, kill schedd after observing committed expansion and a running
command. Wait for its actual lease expiry before replacement. Require uncertain
status, no zero exit, unchanged baseline, and rejection of a competing migration
with migration admission temporarily enabled. Inspect real SQL before submitting
a task/generation/evidence-bound resume request. Stop schedd during acceptance;
APID restart and request replay must remain quiescing. The replacement schedd
destroys the registered lease-specific runtime through vmmd. Require its durable
acknowledgement and disappearance of the original SQL session before resumed
preflight, fresh health rounds and cutover. The task remains uncertain forever.

The partition receives a 330-minute test budget, 335-minute systemd envelope and
360-minute job. Its twelve real source builds and unshortened lifecycle clocks
fit this bound. One app per case, with at most five 512 MB VM slots including a
task and overlap, fits ordinary Scale quotas and fleet RAM admission.

The runner reserves its existing isolated SQL proxy address only for the SQL
lane and this partition, and removes it during guarded cleanup. TLS is passed
through to the isolated cluster. The five-part aggregate now requires nine
runtime scenarios plus fixture metadata, source-bound completion and final
leakchecks. Evidence parsing rejects missing partitions, duplicate command
execution, changed deadlines, invented exits, and absent teardown evidence.

## Consequences

Portable SQL/workload, parser and runner checks can run without KVM. Compiling
native scenarios or passing those checks does not qualify guest execution.
Retain the exact commit-bound native report before enabling any admission flag.

Workspace validation passed: PostgreSQL/workload tests including race mode,
native-tagged compilation and fixture metadata, focused lint, parser contracts,
partition/runner/aggregate contracts and workflow YAML parsing. All three
read-only platform observation queries prepared against the migrated schema.
Actual native runtime execution and host leakchecks remain pending.

This partition covers a single HTTP app and the resume action. Multi-service
graph migrations, resolve through a separate repair promotion, credential/input
drift, stale restore/RPC races, unavailable vmmd, migration-lane races with other
release modes, and full start/execution deadline exhaustion remain in the
[migration runbook](../ops/promotion-migration-native-acceptance.md). Passing this
partition alone does not complete those qualification requirements.
