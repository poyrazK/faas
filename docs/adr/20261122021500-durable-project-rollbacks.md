# ADR-20261122021500 · Persist and advance project rollbacks on the server

Project rollback admission computes the exact caller-first plan and checks all
targeted services before saving it. The receipt stores exact revision pairs,
serving periods, reviewed release runs, and a stable rollback attempt for each
service.

The API worker claims the parent receipt with a fenced lease. Service rollback
admission checks that lease and the pinned pair in the same transaction that
creates the child operation. If the worker stops after child acceptance, the
next worker reads the same child receipt using the saved attempt ID.

Before starting another service, the worker checks that completed services still
serve their restored revisions and that the next service still serves the pinned
revision. A blocker pauses the project receipt and records the service involved.
An explicit retry retains the plan. A failed child gets a fresh attempt ID.

The CLI can wait, inspect, or retry the operation by ID. Declining to wait or
closing the CLI leaves the worker running. Earlier local plans remain resumable
from the original CLI state directory.
