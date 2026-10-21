# ADR-20261019120016: Verify production compatibility after schema expansion

## Status

Implemented for newly planned `--migrate` atomic project deployments. Native
migration qualification remains pending. Destructive schema cleanup is a separate
implementation target and is not admitted by this change.

## Context

A successful migration task proves command completion, not compatibility with
currently serving code. Publishing a candidate immediately after expansion can
hide damage to the baseline that would later be restored during rollback.

## Decision

New migration plans require `release_command.verify_baseline`. That immutable
requirement participates in the command policy fingerprint shared by scan, apply,
and the CLI journal. New CLI submissions reject a server plan that omits this
requirement before applying. Existing saved plans without this requirement retain their
original contract; their identity is not rewritten during recovery.

After observing the unique expansion task succeed with exit zero and a terminal
completion timestamp, the deployment controller checks the active baseline's
artifacts and runs the existing private, deployment-attributed, read-only critical
HTTP probes. Evidence begins after task completion. The checkpoint binds the
account, attempt, command/artifact-validated task, production graph, route list,
start time and server completion time. It is an internal store operation; no
customer endpoint accepts probe evidence. No task output, database contents or
configuration values enter the public receipt.

Checkpoint writes and publication repeat their guards under the existing project
and submission locks. PostgreSQL uses SQLC and the existing receipt JSON; no new
schema or VM/task execution path is required. Checkpoint retries reuse the same
task and reject older overlapping rounds. A reconstructed store adapter recovers
proof from the receipt. Publication verifies the proof again, including exact
route attribution and freshness, in the graph transaction. A changed graph or
configuration cannot be overwritten by this attempt.

Every critical baseline route needs a complete attributed sample with zero server
errors. Unauthenticated, unexpected, incomplete or unavailable evidence remains
unknown and prevents cutover. A complete failed baseline sample terminates the
attempt while preserving production intent and the task identity for operator
investigation. The platform neither replays the migration nor reverses schema
changes. Candidate errors remain the responsibility of the subsequent rollout
verification; they do not invalidate a healthy baseline measurement.

Unknown rounds are retried at the existing verification interval. Evidence must
be at most `ProjectMigrationBaselineEvidenceMaxAge` (three intervals) old at
publication. The original deploy deadline bounds waiting; the gate does not
extend migration deadlines or graph retention. The receipt and `deploy status`
report the post-expansion baseline checkpoint separately from task completion.

## Consequences

Operators must configure critical probes that exercise database-dependent paths.
Passing those probes is practical evidence for those paths, not a static proof of
SQL compatibility. The backward-compatible migration declaration is still
required. Worker-specific schema compatibility and destructive contract admission
need further work: stabilization alone cannot authorize dropping old schema while
retained graphs, pinned jobs or old instances can still execute.

Memory and real PostgreSQL contracts cover missing proof after task success,
healthy/failed/unknown routes, wrong task and route identity, pre-completion proof,
stale/future proof, competing production intent, replay and store reconstruction.
Controller tests cover unavailable probes/artifacts, cancellation and throttling.
API/CLI checks cover plan identity and journal copying. Control-plane tests do not
qualify actual SQL migrations or native guest execution. No production gate is
enabled by this change.
