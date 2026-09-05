# ADR-155 · Latest-save-wins developer deployments

- **Status:** accepted
- **Date:** 2026-09-05
- **Decision:** keep watching local source while a developer deployment is in
  flight, cancel an accepted deployment when a newer settled snapshot exists,
  and deploy only the latest pending source.

## Context

`gregale dev` originally resumed file watching only after the current build
became live or failed. An edit made during a build was eventually detected, but
the obsolete build still consumed capacity and could briefly become live before
the newer source entered the queue. Repeated saves serialized into repeated
builds, making the remote loop feel slower precisely while a developer was
iterating quickly.

## Decision

The CLI watcher and deployment now run concurrently. The watcher retains one
pending intent rather than one deployment per filesystem event; the existing
settle interval still collapses editor write bursts. Once the current upload is
accepted and its deployment ID is known, a newer intent calls the public
deployment-cancel endpoint and then cancels the local log stream. Source events
that accumulated during cancellation are drained before resolving the next
application shape, refreshing the developer lease, and packaging the current
tree.

Cancellation is deliberately delayed until the create response supplies a
deployment ID. Cancelling only the HTTP request could leave an accepted server
deployment whose response raced with local context cancellation and whose ID
the CLI no longer knows. A deployment that became terminal before cancellation
is not an error: the next developer deployment still supersedes it normally.

Ordinary `gregale deploy` retains its signal-owned context and blocking
behavior. The shared deploy implementation exposes context and queued-ID hooks
only to long-lived workflows such as `gregale dev`.

## Consequences

Rapid saves no longer form a serial build backlog. At most one accepted
developer deployment is active from this CLI loop, and one newest pending
source intent is retained. A very fast obsolete build can still reach a
terminal state before the cancel request wins the race; correctness is
preserved because the newest source is deployed immediately afterward.

Stopping the watcher cancels local work without tearing down the stable
developer environment. Failed cancellation is reported, but does not strand
the watch loop or prevent the latest source from being submitted.

## Validation

CLI state-machine tests cover retry after a failed build, invalid intermediate
source, and superseding an accepted in-flight deployment without rendering it
as a developer-sync failure.
