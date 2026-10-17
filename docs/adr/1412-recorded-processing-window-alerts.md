# ADR-1412 · Local alerts over recorded processing windows

Status: accepted

## Context

Minute counters support recorded renewal rejection fractions and expiry frequency,
but do not prove that all writers recorded throughout a mixed-version rollout.
Repeated health polls of the same completed window must not create new window
samples. Coalesced counts and retained-invocation completeness cannot establish
recording continuity.

## Decision

Add opt-in CLI and Go SDK watch conditions for recorded renewal rejection fraction
and expiry events per second. Require a past operator assertion of continuous
recording by every relevant writer. Reject windows crossing that boundary and
bind the assertion into condition, alert and checkpoint metadata. This is not
automatic deployment/rollout verification. Operators must restart with a new
boundary after recording gaps, rollback, restore or data loss.

Validate effective window bounds, scope, bucket metadata, count bounds, saturation,
sample denominator and rates. Preserve health observation freshness checks while
counting only distinct consecutive completed-minute windows. Both breach and
recovery need at least two windows and a configurable duration of at least one
minute. Low samples, stale or incomplete evidence lose confirmation rather than
resolve an incident. Same-window classification changes reset the opposite proof.

Use the existing local alert lifecycle and durable outbox. Checkpoint version 2
stores historical incident state and matching policy, including recording-since;
live confirmation always starts over on resume. Keep all prior conditions and
server health semantics unchanged. No table, route or deployment change is needed.

## Consequences

An explicit continuity assumption is required because the server cannot verify
all-writer upgrade state. Defaults cannot mistake pre-upgrade zeros for health.
No-sample renewal windows cannot recover incidents; covered zero-expiry windows
can, after fresh sustained confirmation. Repeated polls provide liveness rather
than fabricated samples, and overlapping windows are not independent statistical
observations. The feature emits local notifications only. Automatic recording
coverage and hosted notifications remain separate future work.
