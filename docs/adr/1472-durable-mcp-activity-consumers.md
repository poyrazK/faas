# ADR-1472: Durable MCP activity consumers

Status: Accepted

## Context

The activity reader, bounded watcher and baseline recovery helper expose scoped
cursors, but each agent must preserve acknowledged progress across sessions.
Automatic advancement when a page is returned can lose work after a crash, and
two managers can overwrite each other's offsets without a shared revision guard.

## Decision

Add two statically discoverable MCP tools to get or explicitly acknowledge a
named environment activity consumer. Store only its binding, acknowledged cursor,
monotonic revision and update time in the existing encrypted single-writer
bridge journal. A consumer is scoped to the connection namespace (OAuth grant
or local profile), API endpoint, account, immutable app/project/environment and
name. Do not store events, baseline contents, downstream effects or approvals.

Getting a missing consumer returns uninitialized revision zero without choosing
or saving a cursor. Callers first process a baseline or activity page, then
explicitly acknowledge its cursor with the expected revision. Reading, watching,
cancellation and disconnect do not advance progress. Acknowledgment declares
processing; it cannot prove external side effects happened. Recovery and normal
delivery remain at least once, with possible replay after an uncertain commit.

Serialize compare-and-swap and durable commit under the StateStore mutex across
all managers using that store. Only one different update can win a revision.
An exact retry of the last acknowledged cursor with the preceding revision
returns the stored result; acknowledging an unchanged current cursor is a no-op.
Reject regressions, stale updates, foreign/future cursors and unhealthy storage.
Use the same fail-closed encrypted fsync/rename commit path as other journals.
The single-writer lease remains required; separate state files are independent.
Check the projected encrypted byte budget before modifying the journal map;
capacity rejection leaves existing records and journal health unchanged.

Revalidate current named-environment consent, immutable identity, account and
bounded native feed access on every operation. A get reports an expired saved
cursor and validated recovery metadata without changing it. Acknowledging over
an expired cursor requires explicit baseline_rebuilt confirmation after finishing
guided recovery; the proposed baseline cursor must also still validate. Never
reset to the head automatically or claim removed history was reconstructed.

These tools require durable state but remain discoverable without it, returning
durable_state_required on use. Acknowledgment mutates only bridge metadata:
advertise readOnlyHint=false, destructiveHint=false and idempotentHint=true,
track it as mutation work for graceful drain, and require read consent rather
than platform write consent. It sends no native mutation requests. Persisted
OAuth grant state is checked again under the journal lock to prevent revoked
or expired grants from reading or recreating consumer records. Revocation removes
that grant's consumer namespace alongside its plan namespaces.

## Consequences

No native API, SQL, migration, background subscription or exactly-once processing
guarantee is introduced. Limit names to 64 lowercase ASCII bytes and capacity
to 100 consumers per connection, across all targets; limits live in api/limits.go.
Existing feed retention, checkpoint compatibility and writer rollout requirements
remain in force. Journal commits are bounded but may have an uncertain result
after a filesystem failure: restore storage and retry the exact acknowledgment
after restart. Capability/preflight evidence does not reserve retention or grant
future access. Losing the independent encryption key loses checkpoint recovery.
