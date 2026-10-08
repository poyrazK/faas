# ADR-801: Opt-in profiling gates for canary deployment stages

Status: Accepted

## Context

ADR-800 associates route CPU/request regressions with sampled code. Automatic
checks previously remained advisory. Customers need to require qualified profile
evidence before increasing canary traffic and optionally recover confirmed
regressions. Aggregate CPU increases cannot safely substitute for route evidence.

## Decision

An optional `canary_gate` in the app's automatic profiling policy configures
1–5 confirmations, an evidence timeout up to 86400 seconds, `hold` or explicit
`continue` on timeout, and optional automatic rollback. The timeout must cover
warmup, all confirmation windows and ingestion grace. Default dashboard choices
are two confirmations, 1800 seconds and hold; rollback remains unchecked.

Each stage instance and policy revision collects nonoverlapping equal stable and
candidate windows. The stable predecessor is fixed for that receipt. The durable
JSON receipt keeps only the latest bounded comparison, one next capture selection
and capped route streaks. Retrying a window cannot create another confirmation.
Unknown evidence resets the affected route's streak. A confirmed regression on
any configured route holds; every configured route must accumulate healthy
confirmations to pass. Passed certificates apply only to that exact stage,
policy revision and current stable predecessor, not to later deployment stages
or to ongoing performance. Existing capture, attribution, labeled-request and
minimum-request gates remain prerequisites. Code evidence is explanatory and
cannot prove causality.

APID rechecks the policy and receipt while holding the application/deployment
locks in the existing traffic transition. A customer override requires the
current policy revision and a bounded nonblank reason, recorded with the actor
and gate decision in the traffic audit. Workers cannot override. Existing route,
health and binding checks still apply. Legacy recovery advance/promote cannot
bypass a configured profiling gate; generic traffic edits already reject active
canaries. Manual abort remains available.

Meterd requests explicitly tagged rollback for a confirmed opted-in regression
before stage dwell expires. APID rechecks the durable worker lease, current stage,
policy, route confirmation and exact unique stable predecessor. Binding release
authorization covers the zero-candidate/100-percent-stable traffic intent. One
transaction aborts the request-mode candidate, restores that stable predecessor
and writes the audit. A stale rollback intent never falls back to promotion.
Service workloads require their existing checked handoff recovery and therefore
remain held rather than receiving this direct traffic rollback.

## Consequences

Default checks remain advisory. Sparse, missing, expired or interrupted evidence
cannot silently pass a gate; `continue` is an explicit timeout choice and never
bypasses a confirmed regression. Revision changes require fresh stage evidence.
This protects observed CPU/request behavior; traffic mix, sampling bias and
unobserved code can still limit conclusions. Stored JSON and existing worker
leases provide restart durability without another scheduling owner or table.
