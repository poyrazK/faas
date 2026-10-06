# ADR-402: Bound namespace bridge readiness

## Status

Proposed; native Linux/amd64 acceptance pending.

## Context

VMMD launches namespace helpers for public TCP and UDP sessions. The helper's
readiness pipe previously allowed an unlimited record and an indefinite wait.
A helper that stalled or wrote without a newline could retain a session or grow
VMMD memory. An error record was followed by an unbounded process wait.

## Decision

Bound readiness to 4,096 bytes including its newline and 35 seconds, declared in
`pkg/api/limits.go`. The deadline accommodates the TCP helper's existing
30-second guest dial plus launcher overhead. An earlier caller deadline or
cancellation closes the readiness descriptor to interrupt its read. The record
is read through a limited reader and the descriptor closes on every return.
On an error record VMMD kills and reaps the helper before releasing the pipes.
Successful readiness still means bridge socket setup, not application readiness.

## Consequences

Stalled and malformed helpers fail their session without changing guest storage,
architecture, or scheduler ownership. Real pipe tests cover record boundaries,
truncation, cancellation and deadlines. Native namespace/process cleanup and
leak acceptance must still run on a designated Linux/amd64 KVM host.
