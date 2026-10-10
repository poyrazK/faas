# ADR-925: Recovery notification retry history status filters

Date: 2026-10-09
Status: Accepted

## Context

Per-request outcome summaries expose failed, pending, and inconclusive recovery notification retries, but finding unresolved requests still requires scanning the entire retained history.

## Decision

Allow one optional `status` query parameter on the existing history list endpoint. It is a comma-separated union of distinct `succeeded`, `failed`, `pending`, and `inconclusive` request statuses. Reject empty selections, duplicate/unknown status names, repeated status parameters, malformed query encoding, and other query parameters. Omission returns the complete retained list. Request detail still rejects query parameters.

Compute the existing full retained original-generation evidence snapshot under its read-only repeatable-read transaction or memory lock, then filter summaries without changing decision-time ordering. Always return `matched_count` and `totals`. Totals count all retained requests before filtering: `request_count`, `succeeded_count`, `failed_count`, `pending_count`, `inconclusive_count`, and `incomplete_evidence_count`. Status counts sum to request_count; incomplete evidence counts overlap statuses. An empty retained history or no matches returns an empty array and matched_count zero; no-match responses preserve full retained totals. All counts share the existing observed_at timestamp and expire with the job.

Add `--status STATUS,...` to CLI history lists, rejecting it with `--request-id` or waiting. Text output prints full retained totals and matched count before selected rows. JSON returns the API response. Keep existing two-argument Go list calls valid using one optional query object, validated and canonically encoded. Generated Node and Python clients expose the optional status parameter and totals model.

## Consequences

Operators can find unresolved requests without confusing failure with missing evidence or mistaking filtered counts for complete history counts. Authorization, MFA, retention, original-generation outcome classification, bounded evidence queries, and dispatch behavior remain unchanged. No new endpoint, migration, storage, or delivery mutation is introduced.
