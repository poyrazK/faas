# Domain verification readiness and immediate checks

Date: 2026-11-06

Status: accepted

## Context

TXT ownership, DNS routing, and a usable TLS certificate are separate facts.
The ownership-only verify endpoint and generic pending labels made it difficult
to distinguish propagation from misconfiguration. A single routing observation
could revoke a working domain's verification.

## Decision

Keep `verified` and certificate fields compatible and add an independent
`readiness` summary, fresh diagnostic `checks`, automatic verification due time,
attempt count, and the legacy issuer's durable certificate retry time. A domain
is ready only with verified ownership, a fresh successful routing check, and an
unexpired issued or renewing certificate. CAA and IPv6 failures prevent readiness.
The CLI and resource conditions use the same readiness result. Old servers
without the summary retain the CLI's certificate-and-expiry fallback.

POST `/v1/domains/{domain}/verify` now checks TXT ownership through the same
claim-bound implementation as the poller, then runs the doctor's DNS and TLS
probes. DNS/TLS configuration failures return a structured non-ready response
with HTTP 200; infrastructure errors remain HTTP problems. Manual probes are
coalesced by claim, bounded to eight workers and twelve seconds, and protected
by a fifteen-second durable per-claim cooldown across API processes. Existing
account HTTP admission also applies. Cancellation ends a wait without cancelling
another caller's probe. Replacements and expired claims cannot reuse old proof.

Creation and retry send a coalesced local wake hint. The periodic durable scan
remains the recovery path for lost hints, restarts, and writes on another API
process. Existing bounded background workers and issuer cooldowns are retained. TXT
retry delays include twenty percent jitter while retaining the one-hour cap.

Revoke verification for routing drift only after two consecutive conclusive
failed observations since the current verification. An intervening healthy or
inconclusive result resets confirmation. Surface observations and superseded
claim results never count. The first failure immediately prevents a ready
result and displays routing remediation, without revoking ownership. Existing
claim-bound, ordered state mutations still protect publication.

## Consequences

Deploy the expand migration before apid. Older binaries ignore the new column
and JSON fields. Clients that depended on HTTP 422 for TLS readiness failures
must inspect readiness; the CLI still exits nonzero until HTTPS is ready.
`domain verify --wait` provides bounded, scriptable activation with one final
JSON result. Certificate retries remain owned by Caddy or the legacy issuer;
manual DNS checks do not bypass their issuance budgets.
