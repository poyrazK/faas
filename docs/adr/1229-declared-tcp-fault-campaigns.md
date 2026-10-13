# ADR-1229: Declared TCP fault campaigns

Date: 2026-10-13
Status: Accepted

## Context

Seeded campaigns can repeat a choice and omit another. Users also need a bounded
sequence that exercises latency, bandwidth, timeout and reset against one TCP
dependency with connection budgets, healthy-route checks and timed recovery.

## Decision

Add optional `load.fault_campaign.order: declared`. An unexpanded declared
campaign requires count equal to its number of choices, within existing campaign
limits. Each choice executes once in declaration order, separated by the existing
recovery template. An explicit seed remains required and controls individual
fault seeds. Both selection modes consume two SplitMix64 draws per cycle; the
existing seeded draw order remains unchanged.

Record declared campaigns as `declared-splitmix64-v1`. Fingerprints bind order and
expanded schedules. Coverage selection, previews and recovery comparisons use
the same choice-index helper. Expanded replay and minimization preserve recorded
phases without redrawing. Cycle evidence identifies its fault kind and reports
connection-budget failures with their phase and cause.

The reference fixture opts into a bounded 16 KiB echo and retains one connection
per probe, a 250 ms deadline, and bounded payload accumulation. This makes a
1 KiB/s restriction observable while ordinary probe fixtures retain their limits.

## Consequences

The sequence tests one dependency per campaign and uses phase-wide connection
ratios. It does not identify individual retries inside opaque TCP streams.
Platform cleanup and namespace ownership remain unchanged. Local real-stream
checks qualify campaign orchestration and transport behavior; deployed VM routing
still requires the designated native x86_64 KVM host.
