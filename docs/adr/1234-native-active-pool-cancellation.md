# ADR-1234: Native cancellation during active pool work

Date: 2026-10-13
Status: Accepted

## Context

The native gate cancels only after resource drain. Portable cancellation tests
alone cannot qualify application cleanup through deployed HTTP and TCP routing.

## Decision

Add `tcp-flight` as the 38th mandatory case and require verdict version 20. A
sealed pool fixture mode fixes queue and attempt deadlines at 10 and 30 seconds,
with two connection permits and two queued callers. Bind status to the run ID.
Use the existing scenario command, chaos installation and cleanup hooks.

Persist a nonce-bound active snapshot. Before sending SIGINT, independently
reinspect live application status through the local CLI proxy and verify the
same instance, full pool, queued callers, sockets and timers. CLI cancellation
kills the helper process and closes its four requests. Preserve positive TCP
fault-match evidence and clear the installed fault before workspace teardown.

A fresh cleanup command observes the same application before VM deletion and
requires all resource gauges at zero, balanced waiters, cancellation counter
deltas for four requests, including both original queued callers, and no natural timeout counter increments. Bind the final proof to the
run and nonce and hash it in the verdict. Retain independent API cleanup and
exact JSON/JUnit receipt checks. Add no runtime control endpoint.

## Consequences

The previous 37-case catalog cannot qualify. The manual workflow allows 800
minutes for all cases and independent cleanup. Portable tests cover real TCP
chaos and actual CLI command/proxy cancellation; only designated native KVM
execution can qualify deployed routing and VM cleanup. Gauges and cancellation
counters remain application-reported evidence.


A queued caller remains marked after acquisition. Require balanced retirement of
both waiters and cancellation of both queued callers, allowing an acquisition
between another caller disconnect and its own disconnect. Requiring both waiter
retirements to have the queue-cancel outcome would depend on network event order.
