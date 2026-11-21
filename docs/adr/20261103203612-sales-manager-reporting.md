# ADR: Manager reporting for sales creation cohorts

Status: Accepted 2026-11-03

## Context

Managers need date filters and salesperson comparisons across the existing CRM
and sales referral program. The stored CRM stage and owner describe the current
record; they do not establish who closed a deal at a historical point in time.
Referral credit and first-payment evidence have separate, durable owners.

## Decision

Add a private, read-only manager report under the existing sales feature flag.
Require a verified-email, MFA-completed sales-manager session. API keys, ordinary
reps and customer referral pages do not receive report access or report UI.

Select lead and sales-referral creation cohorts using inclusive local dates and
an IANA reporting time zone. The browser defaults to its local time zone, with
an optional UTC setting. PostgreSQL converts creation timestamps to local dates,
handling midnight transitions, daylight-saving changes and repeated dates.
Date ranges are bounded to 366 days, within 2000–2100.

For each salesperson, report current lead stages and ownership for the lead
creation cohort. Report signup counts and first payments for the referral signup
cohort under the original sales referrer. First payments are recorded conversion
events, including later-refunded events; outcomes may occur after the selected
creation dates. Display average signup-to-first-payment elapsed time from the
recorded timestamps. Win rate is current won/(won+lost), with no rate for an
empty denominator. These values do not invent historical close dates or stage
transitions. Deleted records are absent from later observations.

Read comparisons, collections and observation time in a single PostgreSQL
REPEATABLE READ, READ ONLY transaction. Include active configured team members
with zero activity, plus past/unavailable owners with surviving lead or sales
source records. An owner filter narrows metrics while retaining the directory.

Collections use only the captured first invoice belonging to the referred
account. Group actual invoice currencies separately. Show recorded paid amounts,
recorded refunds, pending refunds and the remaining amount after both refund
categories, clamped at zero per invoice. Amounts include tax. Missing or
wrong-account invoice snapshots do not contribute amounts and are counted as
unavailable payment snapshots. No recurring-revenue or payout balance is inferred.

Use integer cents in SQL and Go. Encode report monetary fields as decimal strings
and use BigInt when grouping and formatting them in the browser. Export comparison
and collection CSVs separately, with filter/observation metadata and spreadsheet
formula-prefix neutralization.

## Consequences

No schema migration or external analytics service is required. Refreshes are
observations, not immutable financial statements. CRM reassignment can move lead
counts while preserving referral credit and collections under the original
referrer. The existing reward terms and disabled payouts are unaffected.
