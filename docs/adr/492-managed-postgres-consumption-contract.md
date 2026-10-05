# ADR-492: Validate the Neon consumption contract

- **Status:** Accepted
- **Date:** 2026-10-03
- **Decision:** Normalize v2 byte-months using Neon's fixed 744-hour month and
  require complete, nonduplicated time coverage before recording usage.
- **Supersedes:** ADR-155's Neon storage-unit interpretation.

## Context

The adapter treated `*_bytes_month` as byte-hours and multiplied by 3,600.
Neon's [current unit definitions](https://neon.com/docs/introduction/usage-calculations)
describe v2 storage/history values as byte-months, already divided by 744.
Recorded byte-seconds were therefore 744 times too small.

The [consumption guide](https://neon.com/docs/guides/consumption-metrics) also
allows omitted zero-valued metrics and returns a project pagination cursor.
The adapter rejected that cursor but ignored timeframe boundaries. An empty or
partial response without a cursor could consequently advance accounting with
zero or incomplete quantities.

## Decision

Convert the summed root/child storage and instant-restore/snapshot history
values to byte-seconds using `744 * 3,600`, with checked addition and
multiplication. This constant is independent of calendar month length.
Compute and network units retain their existing conversions.

Require exactly the requested project. Pagination covers projects; a returned
cursor does not make that project's time series incomplete. Parse period IDs,
period bounds, and timeframe bounds. Sort the reported intervals and require
contiguous coverage of the exact requested window, rejecting duplicate periods,
overlaps, missing bounds, and out-of-window data. Clip a bucket at billing-period
boundaries so a plan change within a bucket can contribute both period shares.
Reject duplicate metrics, absent/null values, negatives, unrequested metric
names, and arithmetic overflow. Omitted zero-valued metrics remain valid when
the response explicitly covers the entire window.

The collector and durable ledger retain their existing checkpoint, correction,
and admission semantics. Invalid provider evidence defers accounting; it does
not advance coverage or synthesize zero consumption.

## Existing installations

Do not infer historical correctness from an existing coverage checkpoint.
Operators must audit any Neon ledger written by the prior adapter before
re-enabling new-resource admission. Replay provider windows that remain
available through the existing ledger correction path, preserving window keys
and using a newer observation time. Reconcile older windows against retained
provider exports or invoices before restoring coverage. Do not skip missing
history or add a second overlapping window size.

This change does not automatically multiply historical rows: a row may have
come from incomplete evidence, or may already have been corrected. Retained
deleted-database totals also need explicit review. Customer charges are not
recalculated by this change. The preview remains qualification-gated.

## Validation

HTTP regressions reproduce the cursor rejection, missing/duplicate coverage,
malformed metrics, and the 744-fold normalization error against the merged
implementation. Positive cases cover omitted zero metrics, unordered billing
periods, a mid-bucket plan change, and maximum safe integer conversions.
Restore idempotency and PostgreSQL-backed accounting tests continue to exercise
durable retry and checkpoint behavior.
