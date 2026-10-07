# ADR-627 · Prospective gateway object-storage safety accounting

- **Status:** accepted for operator preview; production qualification pending
- **Date:** 2026-10-06
- **Decision:** add explicit `gateway_safety_v1` admission using Gregale's durable counters, without activating customer billing.
- **Why:** native GCS and the branded gateway can serve traffic, but the legacy cost-bearing provider-report requirement prevents qualification when no provider billing adapter is configured.

## Contract

The default policy continues to require qualified cumulative provider reports,
including a positive provider-cost ceiling. An operator may explicitly select
`gateway_safety_v1` with `gateway_metering_since`, no cost ceiling (zero),
positive finite capacity/request/egress/authorization budgets, and bounded
inventory freshness. A nonzero cost ceiling is rejected in this mode rather
than ignored. This implements the explicit no-cost-ceiling choice in ADR-237.

Gateway mode requires a public gateway, proxied transfers and no pricing.
Startup rejects any enabled object-storage billing delivery policy. This mode
cannot finalize a customer bill or estimate charges. The v2 customer meter
remains shadow-only; this decision does not qualify operation classes or
stored byte-hours for billing.

Only buckets created after the configured coverage start can qualify. Earlier
buckets may have unmeasured traffic or old direct URLs and fail closed. The
timestamp is fixed when all serving components have the metering implementation;
operators must not backdate it to bless earlier buckets. Restarting components
does not change that timestamp. Backend fingerprints still fence placements.

## Safety measurements

Admission reads provider-request attempts and egress counters from the same
PostgreSQL ledger used by the data plane. Counters are joined to immutable
bucket/account placement; a missing row means zero only for a newly eligible
bucket. Deleted buckets retain current-month request/egress consumption.
Capacity still uses complete inventories and conservative write/multipart
reservations. Missing, future or stale inventories fail closed, including
after UTC-month rollover. Legacy reports cannot override gateway counters.
Customer listing/read requests and public upload dispatch reserve attempts
atomically; inventory scans,
provisioning and cleanup record their attempts without blocking maintenance at
a reached customer request budget. These counters include hooked maintenance
and retries and are not an exact provider invoice request total.

Successful object GETs on both the branded S3 gateway and public asset origin
reserve the full response length before the first downstream byte. Reservations
serialize on the account lock shared with URL admission, preventing concurrent
downloads from spending the same remaining egress budget. Unknown lengths or
failed persistence block the body. HEAD, 304 and 412 responses reserve no body
bytes; range requests reserve the range response length. Forwarding is bounded
to the reserved length. Cancellation or a crash does not refund a reservation,
so this is a conservative safety counter, not a delivered-byte billing meter.

Legacy mode additionally records ordinary S3 delivered bytes, closing the
previous GET gap. Its bounded write survives request cancellation but cannot
promise crash-safe exact accounting. Provider reports remain authoritative
for legacy admission and billing.

The response exposes `unavailable_meters` for stored byte-hours and provider
cost; existing numeric fields are compatibility placeholders, not observed
zeros. Request attempts include retries and hooked operator work, rather than
qualified customer read/write classes. No provider invoice cost is inferred
from customer prices. Actual provider spend remains unbounded by money in this
explicit mode: an upstream request may already have begun before egress
reservation, and provider-internal operations, storage charges and traffic
outside Gregale are not captured by these meters.
Managed job object reads/download URLs and project-environment full-copy clones
are unavailable in this preview mode because they bypass gateway measurements.
Supporting those paths requires separately bounded meters; sharing a provider
interface alone does not qualify them.

## Qualification and rollout

Memory/PostgreSQL parity, concurrent reservations, process replacement,
historical-bucket rejection, range/partial reads, persistence failure, disabled
billing and unchanged legacy report gates must pass before rollout. Use the
actual Gregale CLI against a disposable API fixture, then qualify native GCS
in production separately. Local fixtures do not prove production readiness.

Keep `s3_enabled` false during binary/config rollout. Set conservative finite
budgets, restrict provider IAM to the dedicated storage project, keep public
URL lifetimes bounded, verify all serving components, then choose the coverage
timestamp and enable through the MFA-protected operator API. The flag is global.
Restore the flag to false on failed qualification; metadata and cleanup remain
available. Rollback to legacy accounting again requires qualified provider
reports and cannot synthesize them from these safety counters.

ADR-557/558 broker current customer signed object and multipart URLs through
Gregale's gateway. The older direct-path descriptions in ADR-156/237 are
historical; old issued native URLs keep their original expiry semantics.
