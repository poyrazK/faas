# ADR-1233: Native TCP resource and request-deadline qualification

Date: 2026-10-13
Status: Accepted

## Context

Declared TCP campaigns, connection observations, pool exhaustion and complete
response deadlines have portable coverage but were absent from the native gate.
A failing command alone cannot prove that a configured resilience assertion works.

## Decision

Extend the catalog from 30 to 37 cases and require verdict version 19. Add passing
campaign, pool and deadline cases, controlled queue-budget, response-contract and
missing-gauge failures, and nonce-driven cancellation after verified resource
drain. Require real phase timing, positive fault matches, bounded connection
attempts, restored dependency behavior, application resource evidence, and exact
JSON/JUnit agreement. Keep independent API cleanup and installed release identity
checks mandatory for every case, including receipt rejection.

Use existing sealed fixtures and declarative manifest mutations; add no runtime
control endpoints. Keep high-water queue violations failed after current counts
drain. Native cancellation here occurs after drain; portable tests continue to
exercise active socket, queued-waiter and timer cancellation. No native in-flight
resource cancellation claim is made by this catalog.

## Consequences

Older and partial catalogs fail closed. The manual workflow allows 780 minutes
for the expanded catalog and independent cleanup deadlines. Portable tests
validate generated manifests and evaluate real local TCP reports through the
native receipt validator. Only designated native x86_64 KVM execution can produce
a qualifying verdict; application gauges remain application-reported evidence.
