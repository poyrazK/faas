# ADR-404: Bound UDP listener reservations per app

## Status

Proposed; public UDP rollout and native qualification remain pending.

## Context

The existing manifest contract permits at most 16 workload ports. Public UDP
reservations outlive manifest changes and disabled reservations retain their
public ports. Checking only the current manifest therefore allows unbounded
reservation accumulation within an app, consuming a finite shared port range.
A read/count in the HTTP handler cannot prevent concurrent creators exceeding a cap.

## Decision

Use the existing 16-port technical contract as the per-app durable UDP
reservation ceiling. Declare both the workload cap and its derived reservation
cap in pkg/api/limits.go. All reservations count, including disabled ones.
Deletion frees capacity; disabling does not. No plan price or financial-model
quota changes are introduced.

Enforce the count inside CreateUDPListener. PostgreSQL holds the existing app
ownership row lock across a generated count query and insertion. MemStore holds
its mutex for the same operation. Reject unauthorized/deleted-app creation
before exposing quota; retain duplicate-name conflict behavior at capacity.
Return a typed reservation-limit error carrying the ceiling and attempted count.
The integrated API maps it to HTTP 409, stable udp_listener_limit, limit/observed
fields and documentation guidance to delete an existing reservation.

## Consequences

Concurrent creation cannot exceed this per-app ceiling through either store.
The ceiling does not replace account/session/rate limits, public-port global
uniqueness, source-CIDR filtering or disabled-by-default public rollout. App
retirement and reservation reclamation still need lifecycle review. PostgreSQL
and memory race tests cover final-slot contention, disabled retention, deletion,
name conflicts and app isolation. No native VM execution is supplied by these
store tests, and guest storage remains stateless on Linux/amd64.
