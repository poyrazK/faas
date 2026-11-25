# ADR: Expose typed pipeline receipts through MCP Task handles

Status: Accepted (SDK preview)

## Context

The Node and Python MCP starters can host queued Tasks. Typed pipelines already
have durable native execution and application-owned start receipts. Agents
need a Task handle they can save and resume across client connections.

## Decision

Offer an explicit list of pipeline starts that can return Task handles. Store a
bounded-lifetime binding from Task ID to the existing request and receipt IDs.
Scope it to the application's Task namespace and authenticated OAuth resource,
subject, and client. The original start operation validates input and performs
receipt admission. Concurrent retries converge on the same active binding.

Build `tasks/get` views from fresh authorized pipeline status and result calls.
Use current review links and scoped recovery plans for blocked starts. Publish
the original typed output on completion. The native workflow owns execution.
The starter routes queued Tasks to its existing worker runtime.

## Evidence

Shared Node and Python cases cover ownership, revocation, repeated starts,
expired handles, current review, and results. The CSV example exercises a
lost start acknowledgement and reads one handle across both authenticated
MCP hosts.
