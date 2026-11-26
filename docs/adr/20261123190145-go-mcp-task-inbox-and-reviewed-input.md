# ADR-20261123190145: Serve the Task inbox and reviewed input in the Go starter

Date: 2026-11-23
Status: Accepted

## Context

The Task inbox and reviewed form input let a fresh CLI resume a paused
Node Task with its OAuth credential and Task ID. Go Tasks use the same
PostgreSQL model and can serve that workflow.

## Decision

- Go advertises the existing inbox and review extensions on modern external
  OAuth endpoints. The inbox requires the same additive index as Node.
  Each page checks the namespace, owner hashes, current tool policy and
  retention. Its keyset cursor preserves PostgreSQL microseconds.
- Go reuses the encrypted input state and ordinary response persistence.
  It checks the exact pending form revision while holding the row lock and
  checks expiry after that lock is acquired. Confirmation reads recorded
  answers without mutating the Task. The reviewed path preserves JSON number
  precision when preparing revisions and comparing answers.
- Cursors and review revisions are authenticated by the issuing runtime.
  Go replicas with the same owner key share them. Clients start a fresh scan
  or export a new review after a runtime or owner-key change.
