# ADR-1304: Discover automation baselines and observe retained evidence health

## Status

Accepted

## Context

ADR-1303 exposes a known named baseline in the dashboard, but users must know its
name. Promotion receipts are immutable history and do not describe present
report availability. Operators need to discover names and inspect evidence gaps
without accidentally running workflows or changing a trust decision.

## Decision

Provide a read-scoped GET collection at the automation's `live-baselines` path.
Return one current receipt per name, sorted by name and bounded by the existing
20-name quota, including a shared observation timestamp. Memory observes heads
and linked suite/report metadata under one mutex; PostgreSQL uses a read-only
repeatable-read transaction and SQLC queries. Missing evidence preserves the
baseline head and audit receipt. An unknown app/automation returns not found;
an existing automation with no promotions returns an empty array.

Separate evidence status (`usable`, `failed`, `unavailable`) from renewal
readiness (`passed`, `failed`, `inconclusive`). Evidence status describes current
retained assertion reports. Renewal uses the same pure promotion evaluator and
reviewed version 2 policy, checking original run age and current target. Old age
and a historical target alone do not invalidate a baseline for CI: the gate has
always allowed historical baseline evidence and applies age limits to candidates.
These observations are not CI gate verdicts or automatic revocation decisions.

Return safe case names, run IDs, result categories, blocking reason codes, oldest
original run age and policy age budget. Never return run inputs, outputs,
assertions or private errors. Never check, resume, start or repair runs, mutate
suite timestamps, promote replacements or renew original evidence age.

Expose one bounded GET through Go, Node and Python clients and
`gregale automations live baseline list`. Show the catalog in the dashboard,
including policy/target, readable reasons and links to inspect retained runs or
prepare a replacement suite. Preparing a replacement is navigation only; real
execution and baseline promotion retain their separate explicit confirmations.

## Consequences

No new schema or retention policy is required. Head receipts and linked reports
are coherent for each listing, but observations can change after the read. The
API and dashboard remain useful with execution disabled. API authorization,
MFA, no-store responses and app/automation isolation match existing baseline reads.
