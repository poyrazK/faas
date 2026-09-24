# ADR-237 · Provider-neutral customer billing for direct object storage

- **Status:** accepted architecture; implementation and provider qualification pending.
- **Date:** 2026-09-24
- **Decision:** keep direct provider-signed transfers from ADR-156. Customer
  charges use a versioned Gregale rate card applied to qualified, normalized
  usage evidence. Provider costs are separate operator accounting and must not
  be confused with customer quantities or charges. No gateway is required for
  billing, and no paid mode is enabled by this decision.

## Why

The S3 data protocol does not expose a portable billing feed. Each provider
has different usage, logging, cost, latency, and completeness semantics.
Gregale can provide a stable *customer price contract* by normalizing a
provider-qualified source for each billable dimension; it cannot assume that
one provider's API or invoice is portable to another provider.

Routing every signed URL and multipart part through Gregale would make
byte-level metering and immediate admission possible, but would add a new
bandwidth, latency, availability, abuse, and crash-recovery dependency on the
customer data path. That cost is not justified for the current product. Direct
URLs deliberately retain the delayed-budget and unbounded-overshoot caveat in
ADR-156. This decision must be revisited if the product promises hard
real-time transfer limits or universally exact delivered-byte billing.

## Customer meter and evidence

The first paid rate card may charge only dimensions for which the selected
backend supplies complete, attributable evidence. Candidate dimensions are
stored byte-hours and customer-attributable successful read/write operation
classes. An egress-byte price is **zero/included** unless a backend has a
qualified complete byte source covering direct URLs, multipart, range
responses, retries, and other supported paths. A missing dimension is not
zero usage: it prevents activation of a rate card that charges that dimension.
The price and meter version are fixed for a UTC billing period; old periods
are never reinterpreted when a backend, adapter, or rate card changes.

The source for each dimension must identify its coverage interval, backend,
physical bucket, immutable bucket-to-account placement, unit, classification,
and observation time. A provider adapter normalizes the source into a
monotonically cumulative UTC-month report. It must distinguish customer
traffic from operator inventory, cleanup, retries, and lifecycle work, or
explicitly exclude unsupported operations from the paid product. Signed-URL
issuance and capacity reservations are safety counters, never billable
operations or evidence of completed transfers. Provider invoices and cost
exports are not substitutes for missing per-account quantities.

Adapters must reject missing, partial, duplicated, ambiguous, regressing,
wrong-placement, and stale evidence. A report is imported atomically and
retained with source/version provenance. Late downward corrections require an
explicit adjustment rather than rewriting a closed period. A customer month
cannot close until every backend used in that month has complete evidence for
every charged dimension through the period end. The existing immutable
month-close and idempotent billing-delivery records remain the downstream
boundary. A backend migration preserves the old backend's evidence for its
part of the month; a new backend does not replace or erase it.

## Provider cost and safety budgets

Provider cost is an optional, separately sourced **operator** observation,
not a customer meter. It carries its own currency, attribution, coverage,
freshness, and reconciliation status. Unknown cost must be represented as
unknown, never as a fabricated zero. Customer charges do not depend on a
same-currency provider invoice or an EU BigQuery export. Google Cloud Billing
export can be used for operator reconciliation when the backend is GCS; it is
not a universal customer-billing prerequisite.

The current `ObjectStorageUsageReport` and PostgreSQL schema require
`cost_millicents` in EUR, and the current `MaxMonthlyCostMillicents` policy
uses it to fail closed. They remain unchanged until a later versioned
contract, migration, and tests separate customer meter evidence from provider
cost without silently weakening that safety behavior. In the new contract, a
configured operator-cost ceiling fails closed when its qualified cost source
is absent or stale. An operator may instead explicitly choose a backend with
no cost ceiling only after documenting the unbounded-overshoot risk and
enforcing independent capacity, issuance, URL-lifetime, and provider-side
controls. A cost ceiling must not silently become a customer-charge ceiling.
Usage/report freshness and capacity checks continue to fail closed.
Where provider evidence is delayed, an issued direct URL may continue to be
used before the next observation; no instantaneous monetary cap is promised.

## Activation sequence

1. Land an additive versioned customer-report schema and shadow-only store
   alongside the unchanged legacy admission/month-close reports. Then add
   separately qualified dual-read cutover logic; keep billing off until it is
   proven to preserve existing safety checks and immutable periods.
2. Implement a backend adapter and prove physical-bucket/account attribution,
   complete operation-class and storage evidence, error handling, and
   month-end coverage with real direct-provider traffic. If any priced
   dimension lacks evidence, keep that backend unqualified.
3. Run shadow billing for a complete UTC period, reconcile normalized
   quantities with independent provider observations and hand-checked
   fixtures, and bound the operator's cost/margin risk.
4. Enable live billing only at an explicit future UTC-month boundary for a
   qualified backend and published rate card. Never charge pre-activation
   usage retroactively. Provider changes require their own qualification.

Qualification exercises replayed/unused URLs, multipart, range and partial
downloads, copy, overwrite/delete, provider and client errors, retries,
lifecycle mutations, bucket migration/deletion, late evidence, month rollover,
and duplicate billing delivery. Disable or exclude any path that cannot be
measured correctly. R2, GCS, and OVH are candidates, not automatically
qualified by sharing an S3-compatible data API.

## Consequences

The data path stays direct and provider-neutral at the API surface, while
adapter implementation and evidence quality remain provider-specific. New
backend onboarding requires qualification, not only endpoint configuration.
Delayed observation means spending controls are eventually enforced, and an
abusive reusable URL can incur unbounded upstream cost before revocation or
expiry. Operator alerts, short URL lifetime, issuance limits, account
capacity reservations, and provider-side controls reduce risk but do not
turn that into a hard cap.

This ADR does **not** enable object storage, set prices, change the existing
report schema, remove the current cost safety budget, activate Polar delivery,
or deploy a new backend. Those changes require separate reviewed PRs and
production qualification.
