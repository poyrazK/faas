# Observed command-cron admission timing

Date: 2026-11-16

## Context

Cron outcomes distinguish scheduling decisions from admission returns, but do not
show their nominal schedule delay. The scheduler already exports admission-lag
histogram buckets and a separate counter for untrustworthy timing. These are
process-local observations of returned committed occurrences and policy
dispositions, including catch-up history. They do not measure database commit
time, execution-start latency or continuous eligibility delay.

## Decision

Add optional advisory `command_cron_timing` evidence to the existing protected
runtime-build read for explicit single-box-v2 and control-plane-v2 profiles.
Observe schedd and, on the control-plane profile, schedd-compute. Read the eleven
fixed cumulative buckets (1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600 seconds,
plus infinity), the histogram count, and the independent unknown-timestamp
counter. Do not read the histogram sum or estimate percentiles.

Require coherent bucket snapshots: fresh exact nonnegative counters, identical
original scrape clocks, nondecreasing cumulative buckets, and infinity equal to
count. Comparisons reuse the existing unchanged-process and uptime-window proof.
Every bucket difference must form a possible cumulative distribution. Incomplete
histograms, resets or impossible windows establish no timing delta. Preserve the
independent unknown-timestamp counter when the histogram is unavailable. A first
sample is a baseline; zero timed observations do not imply zero lag.

Expose 13 closed ordered signal classifications and bounded target details.
Summary counts classify signals, not observations or unique tasks; target deltas
count process-local observations. Buckets overlap and must not be summed. Keep
full-set coverage when preview details yield to the existing shared response
limit. New queries yield to existing loop evidence and share its deadline. No
additional protected HTTP poll, scheduler policy, alert threshold or deployment
gate is introduced.

Carry evidence through CLI JSON/human output, projected reports and bounded
compact history. History records category changes, preserves sticky increased
signal categories and uncertainty, and does not sum overlapping poll deltas.

## Consequences

Operators can distinguish timed observations, delay bucket changes and returns
with unknown timing without inferring execution success. Sampled process
continuity cannot exclude every intervening restart. Older profiles and readers
retain compatibility; invalid optional evidence is isolated from core results.
