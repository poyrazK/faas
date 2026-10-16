# ADR-1481: Final consumer read validation

Status: Accepted

## Context

Consumer handlers check consent and saved state while collecting observations.
The shared tool wrapper performs additional native authorization reads after a
handler returns. ACK, retirement or grant changes during those final reads can
invalidate the already collected result. The health overview introduced a final
read guard, while other consumer read tools still check earlier. Exact get lag
metrics are currently sampled before these final checks and output serialization.

## Decision

Extend the existing final tool-read guard to consumer get, watch, baseline
recovery and list. Wrap only those successful read results with private guard
context. Marshal the wrapped payload directly so no new JSON envelope or context
fields appear. Preserve public response schemas, token formats and catalog counts.

Single-consumer guards recheck current durable grant/storage health, exact binding,
connection generation and consumer revision after the wrapper's final native
authorization and before serialization. Cover initialized and uninitialized reads,
expiry guidance, empty watch timeouts, event pages, partial recovery continuations
and completed recovery ACK drafts. Keep existing handler-side checks, including
checks on failed native reads. Legitimate recovery-restart errors retain only the
already validated scoped recovery guidance; they introduce no later authorization
read gap.

Share the bounded scoped-record snapshot/fingerprint helper between health and
list. Capture the list guard from the same locked record set used to build the
page, then recheck the entire selected scope before releasing the result. Changes
outside the returned page must also withhold it. Keep health continuations bound
to their existing fingerprint, while list continuations remain generation-bound
live pagination between requests. Revisions change only for real progress, so
exact no-op ACK retries do not falsely conflict. Other-scope ACKs do not affect the
scoped set; single-consumer guards allow unrelated consumers' progress. Retirement
anywhere in the connection invalidates its generation first.

Move exact get lag sampling to an optional successful-read observer in the shared
wrapper. Invoke it only after final guards and bounded/redacted serialization
succeed. Get handlers and serialization have no sampling side effects themselves.
Failed final authorization, state validation or serialization must not contribute
get lag samples. Other consumer reads, health, resources and prompts remain quiet.
These samples describe locally accepted responses rather than network delivery.

Apply no observation guard to ACK or retirement outputs. A later progress change
alone must not discard the receipt of an already committed metadata mutation.
Keep their existing authorization, CAS, retry and mutation-drain semantics.

## Consequences

Conflicts or lost access during final authorization withhold the whole observation:
no stale cursor, page, continuation, lag, generation, recovery extension or ACK draft.
Generation conflicts retain priority over revision conflicts. Cancellation and
drain continue to stop observations; no journal lock spans native I/O and final
checks add no native calls or durable writes.

No native endpoints, migrations, quotas, journal-format changes, new tools or
implicit processing/acknowledgment are introduced. Observations remain point-in-time
checks rather than leases; feed activity and retention can change after a read.
Clients still process explicitly and ACK against current native retention and
saved guards.

SDK tests pause real requests after the actual handler returns, during the shared
wrapper's final native authorization. Both supported protocols and session/HTTP
transports cover concurrent ACK, retirement and revocation. Additional cases cover
uninitialized/expired states, timeout and recovery branches, out-of-page changes,
storage/native permission/identity loss, cancellation/drain, no-op and unrelated
changes, live list pagination and unchanged wire payloads. Receipt tests ensure
read guards do not suppress committed ACK/retirement replies. Serialization tests
verify that rejected oversized or unencodable output adds no get lag samples.
