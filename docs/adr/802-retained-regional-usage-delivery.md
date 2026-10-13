# ADR-802: Retained regional usage delivery and canonical import acknowledgement

Status: accepted · 2026-10-12

## Context

ADR-798 imports a complete cell-signed minute into one global accounting root.
Lost delivery responses and regional restarts require retained transport state.
A successful HTTP response alone cannot establish which original allocation and
minute committed, and a delivery receipt must not become execution authority.

## Decision

Queue the first verified cell-signed usage assertion against its exact original
locally delivered grant, under that app's quota journal lock. Neither today's
head, app ownership, registry origin nor retirement substitutes that identity.
Retain the first envelope and immutable claims on regional app/allocation/minute.
Conflicting quantity, source digest, observation or binding is rejected. Key
rotation with identical claims replays the original envelope. Queue entries
survive app and deployment collection; direct rewrites, deletion, truncation and
populated downgrade refuse. Queueing is a trusted metering transport boundary,
not a complete-minute producer or a customer billing mutation surface.

Meterd's receiver verifies the cell signature and exact original global issuance,
then uses ADR-798's atomic canonical usage/financial import. Only after that commit
may the original directory issuer sign a purpose-separated acknowledgement of
those exact usage claims and the original import time. Lost responses and rotated
signing keys replay that import time; retries cannot create a second financial
charge. Regional delivery records the first verified acknowledgement and removes
that item from pending traversal. It preserves the imported record for audit.
Neither acknowledgement nor observation grants native admission or a drain.

HTTPS destinations are trusted operator configuration keyed by original directory
issuer. Never route by a submitted URL or today's regional target. Requests and
responses are bounded, redirects are refused, and no customer credential is
sent. Missing/untrusted/mismatched acknowledgements retain pending work. One
failed item advances a bounded page so later entries can progress; an empty page
resets traversal for retries. Concurrent workers replay the same global import.
Historical trust anchors must remain available while their retained envelopes
need delivery. Changing a directory identity requires an explicit migration.

## Integration boundary

The library receiver belongs to meterd and must be wired through operator ingress;
this change creates no public unsigned billing route or extra public listener.
The periodic sender, daemon configuration, complete minute producer and sealing
are still required. Original environment floor eligibility, admission history,
late-history protection and native-source reconciliation remain producer inputs.
Managed compute routing, global allowance/caps, shared authority and billing,
egress and dedicated native KVM acceptance remain required for activation.
Existing regional execution fences remain.
