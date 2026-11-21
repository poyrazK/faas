# ADR-20261115130000 · Sampled daemon process continuity

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-15
- **Extends:** [runtime builds](20261115080000-advisory-runtime-build-evidence.md),
  [history](20261115100001-observed-build-history.md), and
  [readiness](20261115120001-advisory-daemon-readiness-evidence.md).

Ready snapshots and matching SHAs can hide a process restart between polls.
Read the existing daemon_uptime_seconds metric for each exact selected job and
daemon. Do not interpret the optional launcher restart counter's zero as proof
of no restarts, add a metric producer, infer a crash cause, or change deployment
qualification, readiness, confirmed faults, exits, stability policy or polling.

Add optional process aggregates and per-preview-item uptime evidence to the
existing runtime-build response. Fresh evidence requires a successful fresh up
sample, matching complete target labels, finite nonnegative bounded uptime and
an original uptime sample timestamp within the existing 120-second freshness,
30-second skew and release-boundary policy. Preserve original clocks; missing,
stale, conflicting, invalid and unavailable evidence cannot establish continuity.

The API remains stateless. Add optional compare_at, supplied from the previous
valid runtime response's generated_at during polls already needed by the
verifier. Compare current and historical instant queries over the complete
inspected target sets before the existing 32-item preview. Comparing previews
alone would hide process changes elsewhere in the fleet. Historical telemetry
uses the current selected registry scope; it is not a historical registry audit.
Exact full target identity includes node_id, so node replacement is a membership
change rather than a restart of the prior target. New and removed targets,
repeated samples, unavailable comparisons and coverage gaps remain explicit.

Each job reads current identity/up (four queries), uptime (two), readiness (four)
and, only when comparison is selected, historical identity/up and uptime (six).
The eight fixed readers retain one shared three-second query deadline, strict
Prometheus responses and the existing 4096-sample/vector and 1000-node bounds.
Declare these limits in pkg/api/limits.go: comparison lookback ten minutes;
maximum plausible uptime ten years (365-day years); estimated-start tolerance
31 seconds (existing clock skew plus the one-second producer tick); uptime-reset
tolerance one second. Reject a comparison at/after generation, outside the
lookback, before the tolerated evidence boundary, without a selected role, or
with duplicate/empty/unknown query parameters.

For advancing sample clocks, an uptime drop exceeding one second or a forward
shift exceeding 31 seconds in sample_time minus uptime is an observed process
change. The latter detects replacements even when the new uptime exceeds the
previous uptime. A backward estimated-start shift beyond tolerance, a regressed
sample clock, or unavailable historical evidence yields an unavailable
comparison. The same timestamp and uptime is repeated evidence. Initial fresh
reads establish only a baseline. Unchanged means no change was detected in the
sample pair; short restarts, multiple restarts, clock changes and missing
telemetry prevent any claim of uninterrupted process survival or a complete
restart count. A process change can be classified as same revision, different
revision or unknown revision from fresh build identities. A changed revision
does not itself prove a planned rollout; an unchanged revision does not prove a
crash. Observed revision changes without uptime evidence do not invent a restart.

The CLI binds comparison responses to the requested prior clock and advances
accepted intervals, including partial responses, so intervals do not overlap.
Transient read errors can preserve the last point within the lookback; expiry
resets to a baseline. An older API omitting process evidence keeps valid build
and readiness evidence. If a rolling cutover returns 400 for compare_at, the
next existing poll omits that key; do not retry or add another poll. Invalid
optional process data is dropped independently of identity and readiness.

Extend aggregate history with process summaries and their original comparison
clock. Routine clock changes with equal counts do not add events. Distinct
process-change or membership-change intervals do, even with identical counts.
Retain the existing shared 16-event/16 KiB ring and 1 MiB report envelope, with
explicit truncation. Progress preserves compared-pair and observed-change
counts, revision classifications, new/removed targets, first/last observed
change and sticky incomplete evidence. Replayed or overlapping evaluation
windows do not increment totals; they mark incomplete evidence. Histories carry
aggregates, not raw uptime series, target labels or per-target lifecycle claims.

Carry optional evidence through strict Go validation, SSH projection, private
artifacts, human output and safe aggregate job summaries. Keep the 64 KiB API
bound and regenerate preview SDKs additively. Verify normal growth, same-SHA and
changed/unknown-SHA replacements, uptime growth after a replacement, timestamp
precision/skew/regressions, stale/missing/conflicting data, source failures,
node replacement, evidence beyond the preview, bounded retention, owned
snapshots, repeated responses, compatibility, unchanged qualification and SDK
round trips. Operational activation remains with the deployment owner.
