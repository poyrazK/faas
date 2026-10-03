# ADR-423 · Scenario-scoped chaos testing

- **Status:** accepted
- **Date:** 2026-10-01
- **Decision:** Add bounded request-level fault injection to isolated `gregale test` runs through the managed internal HTTP service proxy.
- **Why:** Application resilience checks need to exercise retries, circuit breakers, and recovery against platform behavior without exposing production or unrelated workloads to faults.
- **Consequences:** Chaos plans belong to one registered scenario run, may name only its workloads, expire within five minutes, and are deleted with the namespace. The first fault kinds are added latency and synthetic HTTP 5xx responses, selected by percentage and seed. The CLI supports one-off `gregale chaos inject` runs; manifests can declare reusable plans. Only real-VM scenario runs apply plans. Reports record the installed rules and expiry; service-call metrics and traces identify injected calls.
- **Rejected alternatives:** Host-level packet loss or hypervisor/network mutation would broaden the blast radius and require VM lifecycle and networking changes. Injecting faults at the public edge would affect application ingress rather than the internal dependency path and could reach production. External services and raw TCP/UDP are outside this first version because Gregale does not terminate those connections at the service proxy.
