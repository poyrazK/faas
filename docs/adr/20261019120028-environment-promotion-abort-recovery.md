# ADR-20261019120028: Abort and guarded recovery for environment promotions

## Status

Implemented for health-gated HTTP promotions, including the single production push
worker supported by ADR-20261019120027. Promotion admission remains disabled by default until
native qualification, including these controls. Accepted promotions continue to
reconcile when admission is disabled.

## Context

A running promotion cannot use the completed-promotion rollback command. A health
window can also expire while a baseline or its worker is unavailable, leaving no
operator command to retry restoration after the dependency recovers.

## Decision

`projects environments abort PROJECT PROMOTION --to ENV --generation N` records a
durable operator decision. Before cutover it atomically fails the promotion in an
`aborted` health phase, preserves destination graph/configuration, retires owned dark
candidates and drops candidate worker startup holds. Schedd owns instance teardown.
A preparation transaction and abort serialize on the promotion owner; late probes
and publication cannot resurrect an aborted candidate.

After cutover, abort enters `restoring`, resets previous health evidence, and requests
fresh baseline-only HTTP probe rounds. At least two minute-spaced successful rounds
are required. Worker promotions additionally require current retained-worker startup
and configuration proof and healthy real delivery samples from a fresh rolling
five-minute window. Low traffic remains unknown; controls do not manufacture jobs.
The candidate can fail independently of the baseline. Existing durable first-admission
job pins remain authoritative throughout restoration.

Restoration has one ten-minute deadline covering probes and publication. A derived,
bounded hold lets the scheduler recover the retained worker. Current destination
ownership, captured configuration, credentials, bindings, workload settings, baseline
retention and worker readiness are checked at publication. Graph, configuration,
workload settings and new-job selection restore together. A crash after publication
can finish recording the same restoration without switching the graph again.

A failed qualification or publication records `blocked` and drops the control hold.
`projects environments recover ... --generation N` admits a new bounded attempt from
`rollback_failed`, using the original artifacts and baseline retention. It never
rebuilds, extends retention, bypasses health or overwrites newer destination intent.
Changed graph ownership closes the decision as conflicted and requires inspecting
that newer intent. Migration promotions keep ADR-20261019120026's separate workflow; general
abort/recover is refused because a process outcome cannot establish database state.

Request IDs are 32 lowercase hexadecimal characters. The inspected generation is
mandatory. A per-promotion receipt history keeps immutable request identity, operator,
action, timestamps and deadline, and a terminal outcome. Identical replay returns that
request's receipt, including after a newer recovery; changed semantics or stale
concurrent requests conflict. PostgreSQL triggers prevent older binaries from erasing
a control or changing its identity. A transaction-local publication authorization, bound to the exact request and
generation, rejects older publishers already in flight when abort was admitted.
Generation CAS and the publication transaction
fence observations and actions already in flight. API routes use deployment write
scope, MFA and existing idempotency middleware. Status shows the current receipt,
generation and available action. CLI waits do not cancel server work.

## Validation

Portable Memory/PostgreSQL contracts cover early abort, candidate retirement, command
races/replay, blocked recovery, baseline health, stale observations, newer intent,
configuration drift, old-controller writes, retained worker readiness, expired
retention and job pins across restoration. API, CLI and generated SDK checks cover
the public contract. Native x86_64 KVM qualification must additionally exercise abort
at preparation/cutover boundaries, controller restart during restoration, retained
worker loss/recovery, blocked retry and guest cleanup; see the promotion acceptance
runbook. External application or job effects are never reversed by graph restoration.
