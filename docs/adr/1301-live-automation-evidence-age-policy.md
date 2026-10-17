# ADR-1301: Candidate evidence age in live automation gates

Status: Accepted

## Context

ADR-1300 requires selected case coverage but retained passing evidence can remain
acceptable until retention expires. CI needs a reviewable freshness budget.

## Decision

Add policy version 2 with required positive integer `max_candidate_age_seconds`.
Retain the version 1 coverage requirements. Bound the value by the representable
Go duration range, rather than introducing a separate product quota. Version 1
rejects the new field and keeps its original canonical hash; version 2 includes
its age limit in the hash.

Use the oldest original linked candidate run creation as the start of the
evidence window. The comparison includes retained verification creation
timestamps from the same snapshot. Grouping old runs cannot refresh evidence.
Evaluate against the CLI clock, never the comparison observation time or mutable
suite update time. Age strictly greater than the budget or a future creation time
blocks evaluation as inconclusive (7), including old failed evidence. Preserve all
other policy reasons. Exactly the limit is allowed. Baseline age is unrestricted.

Missing creation timestamps return inconclusive, including older servers.
Report evaluation time, oldest available run creation time, observed seconds and
the limit for version 2 comparisons. Request errors retain policy provenance but no
fabricated age. Version 1 and omitted policies preserve existing behavior.

## Consequences

Long-running suites may need a larger budget or a newly executed suite. CI hosts
must have synchronized clocks; no clock-skew allowance is implicit. The feature
adds optional per-run timestamps to the comparison API without extra HTTP
requests or database changes, and cannot start or check real executions. Baseline promotion remains separate work.
