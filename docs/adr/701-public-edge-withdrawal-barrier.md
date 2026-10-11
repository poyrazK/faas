# ADR-701: Public edge withdrawal barrier

Status: accepted · 2026-10-07

## Context

ADR-700 retains admission generations on current public processes. Removing or
replacing a process in the reviewed roster does not prove its old streams have
finished, or that a late authorization cannot still start forwarding. Missing
heartbeats are not proof of process death.

## Decision

Capture every removed public startup session as immutable platform withdrawal
intent when the public head changes, including direct SQL head updates. Preserve
its exact slot, session, configuration and last roster revision. Backfill all
historical sessions absent from the current roster during migration; never
truncate legacy history. Keep intents and sealed receipts through later reviews.
Every changed head also clears current guard/activity facts in the database,
including direct SQL publication and switching back to an earlier revision.
Never re-enroll a withdrawn session. A retained startup session cannot change its
slot or configuration without a new session. Bound unresolved withdrawals at 64
in `pkg/api/limits.go`; reject transitions adding withdrawals above that bound.
Legacy overflow remains pending while topology-only refreshes remain possible.

Add default-off `FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_WITHDRAWAL=1`, requiring ADR-700
activity, ADR-699 confirmation, the installed ADR-698 connection guard and a
PostgreSQL withdrawal store. Bind the installed protocol to the startup config
fingerprint. Repair withdrawal before attempting current guard/activity facts:
removed processes cannot publish those facts, but still need to drain.

Fence internal and public heads, then lock this exact process's withdrawal intent.
A synchronous local callback atomically closes admission before returning its
snapshot. Reject both begin and late bind after closure. Preserve existing HTTP,
H2C and raw upgrade scopes until normal completion. The fence is permanent for
that startup session, even if database publication fails. Do not advance the
activity version for rejected admission after closure. Retry the same fence and
intent; never reset local coverage or reopen admission. Hold no database lock for
the duration of a stream.

Seal an immutable receipt only for closed admission, known coverage and zero
total forwards across ALL generations, including pending authorization. Record
intent ID, local fence ID, positive monotonic activity version and a database
clock sampled after lock waits. Exact retries preserve the original timestamp
and receipt; conflicting fence/version retries fail. Receipts have no lease:
admission stays closed and the process session can never re-enroll. Unknown
coverage cannot recover to known zero. An absent, stopped, unresponsive or
default-off process stays pending; there is no automatic expiry or force close.

Add private `PublicEdgeControls.ObserveCoverage`, without an endpoint or CLI.
Combine ADR-700 current activity with ALL unresolved historical withdrawals
under the same internal/public head fences. Read bounded withdrawal diagnostics
before current activity and its database clock, so withdrawal lock waits cannot
extend a current fact lease. Return `coverage_observed` only if current activity
is confirmed and no withdrawal is pending; validity is still the shortest current
fact lease. Register intent as platform configuration and receipts as operational
in the clone inventory. Gateway writes receipts only; apid review owns intent.

## Consequences

Public roster changes retain unfinished removed sessions instead of making them
disappear. A drained live process can provide permanent local evidence. This
does not establish actual DNS/Caddy topology or exclude undeclared/bypass edges,
and it provides no dead-process fencing mechanism. Current edges may still admit
current-generation traffic. The combined observation grants no retirement lease
or artifact-deletion authority; scheduler/VM quiescence and native topology
acceptance remain required.

Private flags remain disabled in deployment units, and public execution remains
unavailable. No PR, push, deployment, production daemon, forced disconnect,
retirement or artifact deletion is authorized. Native Linux amd64 KVM
`test-metal` and final `leakcheck` remain pending before enablement. Local
PostgreSQL/TCP/H2C fixtures establish synthetic contracts only.

## Validation

Contracts cover irreversible closure, late authorization, independent completion,
concurrent admission, unknown coverage, live streams/upgrades, lost receipt
publication and stable retries. Real migrated PostgreSQL checks capture/backfill,
immutable intent/receipts, permanent non-reenrollment, exact process binding,
capacity, all historical withdrawals, head fences and clock ordering.

Local validation passed 84 focused roots: 28 state, 21 public proxy, 19 ingress
and 16 private runtime-upgrade roots, with PostgreSQL subcases enabled and no
skips. All 40 ingress/public-proxy roots passed under the race detector. The
direct-head round-trip regression retains an intermediate generation's live
scope and requires fresh facts before coverage can recover. Both host gateway
binaries compiled without being launched; production lint reported zero issues.
SQLC regeneration, the migrated clone inventory, eight static migration contracts
and documentation/text/shell gates passed. Three unrelated historical PostgreSQL
migration tests were deliberately excluded by `no_pg`; the final feature schema
was applied in all migrated state fixtures. A read-only check found no version
collision among all 136 open pull requests. These are local contracts, not native
acceptance.
