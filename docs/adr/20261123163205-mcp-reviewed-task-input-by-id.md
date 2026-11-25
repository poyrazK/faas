# ADR-20261123163205: Submit reviewed MCP Task input by Task ID

Date: 2026-11-23
Status: Accepted

## Context

The Task inbox can recover a retained Task ID after losing a local receipt.
Agents also need to submit an explicitly reviewed answer from that new profile.

## Decision

- The Node PostgreSQL starter advertises `io.gregale/task-input-review` on
  modern external OAuth endpoints. It returns a signed revision of the full
  pending form set. The revision covers the Task ID, namespace, and stored
  request IDs and form bodies. Current Task ownership, expiry and tool
  access apply on each request.
- The version 2 private CLI template records the server revision in addition
  to the endpoint, Task ID and form digests. The CLI validates each live form
  before sending an answer. PostgreSQL checks the revision under the Task row
  lock before writing the responses and resuming the existing Task.
- A read-only confirmation request compares the reviewed form set and exact
  answers against the retained encrypted input state. Confirmation does not
  resend the mutation and returns status metadata only. If evidence is
  unavailable, the CLI reports an unconfirmed outcome.
- Existing form request IDs are immutable, and responses survive Task
  execution until normal retention expires. These records supply the
  evidence without adding a schema migration or a second response journal.
  Receipt-based version 1 reviews use their original protocol.
- The Node SDK preview uses the same authenticated MCP connection for the
  reviewed form snapshot and its submission. Its Streamable HTTP fetch adapter
  preserves the original JSON numeric literals from the form, and the SDK
  supports exact numeric values in submitted answers. An uncertain write
  leads to one read-only confirmation within the caller's time budget.
