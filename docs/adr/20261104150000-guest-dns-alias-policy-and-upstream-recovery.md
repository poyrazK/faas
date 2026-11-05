# ADR-20261104150000 · Enforce DNS alias policy and recover upstream failures

- **Status:** accepted
- **Date:** 2026-11-04
- **Amends:** ADR-170 (bridge resolver), ADR-373 (guest DNS policy)

## Context

Checking only the requested name permits an unblocked CNAME to return blocked
addresses and authorize them for egress. Truncated UDP replies also remain
truncated when a guest retries over TCP because the resolver always contacts its
upstream over UDP. A first upstream's SERVFAIL prevents a healthy backup from
answering.

## Decision

Check answer record names and CNAME/DNAME targets against the existing blocklist
before returning records or invoking the egress hook. A blocked answer produces
NXDOMAIN, the existing blocked-query metric, and a log attributed to its caller.
Do not try another upstream after a policy denial.

Retry a truncated upstream response over TCP at that same upstream. UDP, TCP
retry, and remaining upstreams share the existing request deadline. Transport
errors, incomplete replies, and SERVFAIL permit trying the next configured
upstream. Valid negative responses such as NXDOMAIN remain terminal. Only
complete successful responses can authorize addresses. Exhaustion returns
SERVFAIL.

After inspecting a complete reply, fit it to the guest's advertised EDNS UDP
buffer, with the DNS minimum of 512 bytes. An oversized reply sets TC so the
guest can retrieve the full reply over TCP. TCP replies keep the complete answer.

## Validation

Local UDP/TCP DNS servers cover blocked aliases and record owners, allowed
aliases, successful TCP retries, oversized replies with and without EDNS,
transient upstream failure, terminal negatives, failed/incomplete TCP replies,
and the shared deadline. Existing private-service DNS and egress-hook behavior
remain covered by the gateway tests.
