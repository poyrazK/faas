# ADR-20261019120007: Durable migration commands for atomic project releases

Date: 2026-10-13
Status: Accepted

## Context

Atomic project releases (ADR-20261019120004/1005/1006) reject release commands. Applications
with schema migrations consequently cannot use verified code/configuration
cutover. ADR-230 already provides a unique deployment-attached release task,
server-owned execution, bounded logs and the at-most-once dispatch fence. A
project release needs explicit migration admission and transactional evidence
before graph publication, rather than another execution mechanism.

## Decision

`gregale deploy --project --atomic --verify --migrate` requests
`atomic-migrate-verify`; combining `--config-file` requests
`atomic-config-migrate-verify`. The explicit opt-in asserts that the declared
command is backward-compatible with the previous release and safe while it is
serving. Gregale cannot infer schema compatibility from an arbitrary command.
Database effects are never undone by code/configuration rollback.

The first milestone admits one existing `release.command` declaration for a
single-workload project or one root Procfile `release:` process. Multi-workload
Procfiles retain reposcan's deterministic owner. Ambiguous multi-workload manifest
commands, zero commands, multiple commands and operation changes are refused.
All source manifests are inspected before candidate admission. Workload identity,
command/shell hash and compatibility intent are included in the public plan;
source bytes and the distinct migration mode are bound into the plan token and
submission fingerprint. Candidate admission checks the exact resolved command.
Older servers reject the distinct modes, and the CLI refuses missing migration
proof before submitting a candidate.

A store transaction stages command identity on the existing durable project
submission. It checks ownership and the previous production graph and refuses
other pending atomic migrations or currently active release tasks for the project.
The project row serializes these admissions. This is not a distributed database
migration lock across unrelated projects or ordinary release deployments.

Imaged admits the deployment's existing unique release app task after artifact
assembly, before snapshot priming. Schedd executes it through the existing fresh
task VM path. Candidate runtime variables use the ADR-20261019120006 deployment snapshot
when present; secrets and managed bindings use their existing delivery contract.
No VM lifecycle or dispatch transition changes. Enable imaged's release phase and
schedd's app-task dispatch before exposing this mode.

Project reconciliation durably records task ID, execution state, exit code and
failure code, without copying commands, stdout/stderr or secret values into the
saved deploy journal. The existing task read API retains bounded stdout/stderr.
Before graph publication, the same graph transaction checks task ownership,
workload/deployment, artifact key, image digest, scope, command and shell form.
Only immutable `succeeded` evidence with exit code zero permits cutover; candidate
readiness alone is insufficient. Missing, queued, restoring and running tasks
keep the release building. Failures preserve the old production graph.

A terminal task without an exit receipt after dispatch is exposed as `uncertain`,
including expired running leases, lost result transport and cancellation/timeout.
It fails the attempt and cannot be replayed by deploy resume or notification
recovery. Pre-dispatch restoring leases may retry restoration of the same task;
the running fence precedes command delivery. A new deploy is an explicit new
attempt: inspect task logs and database state before choosing to run a migration
again. This contract does not claim exactly-once external database effects.

After command success, existing candidate readiness, atomic graph/configuration
publication and critical-probe verification continue unchanged. A subsequent
probe regression restores the prior code and variables, preserving the expanded
schema. Task identities remain attached to the receipt through verification,
rollback and CLI recovery. Terminal task history is authoritative on cold reads.

## Consequences

Schema expansion can participate in verified project deploys. Migration failure
or uncertainty prevents traffic cutover, but a failed command may already have
changed the database. Destructive migrations, automatic schema reversal, ordered
multiple migration commands, worker activation and exactly-once cross-project
coordination remain outside this milestone.

Memory/PostgreSQL contracts cover missing and pending tasks, success, failure,
wrong command identity, restoration replay, lost running leases, lost execution
transport, competing admission and operator graph changes. API tests cover the
paired-configuration admission and gate. CLI tests cover mode selection, older
servers, receipt identity and recovery without source files. Native-host acceptance
must exercise actual schema expansion, guest configuration, task isolation and
lost-result recovery with both release/task runtime gates enabled before production
enablement; no native VM acceptance is claimed by control-plane unit tests.
