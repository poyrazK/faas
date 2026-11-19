# ADR-20261115140001 · Advisory work-loop health

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-15
- **Extends:** [runtime build evidence](20261115080000-advisory-runtime-build-evidence.md),
  [observed history](20261115100001-observed-build-history.md),
  [readiness](20261115120001-advisory-daemon-readiness-evidence.md), and
  [process continuity](20261115130000-sampled-daemon-process-continuity.md).

A ready daemon running the expected build can retain uptime while its working
loop is stuck. Reuse ADR-190's existing loop_stalled and
loop_last_beat_age_seconds metrics. Surface their reports in the selected-profile
operator response, deployment artifacts and summaries. Do not add a probe,
metric producer, alert, scrape job, watchdog action or deployment health gate.

Freeze the expected loop names independently of build-job membership: runtime
for every selected daemon, main additionally for schedd (control and compute),
and sweep additionally for vmmd. Other work loops remain outside this evidence.
The runtime loop alone does not establish progress of a daemon's actual work.
Read only the exact prefix/job/loop selectors; loop metrics do not have the
identity metrics' daemon label. Match the same complete scrape labels and active
configured compute registry used by identity evidence, retaining explicit
unknown inventory, missing members, conflicting targets and unexpected targets.

Add an optional loop_health envelope around the existing identity DTO. Keep
private read DTOs and their validation in pkg/apid/operatorobs; preserve
RuntimeBuilds as the original identity read and use RuntimeBuildsWithLoopHealth
for the protected route. Both share the same collector. The new envelope holds
full counts and its own bounded target-loop preview; the original typed identity
DTO remains compatible with older readers. The CLI saves loop_health alongside
runtime_builds.evidence. Source release, role, boundary and generation clock come
from the validated identity response, never from arbitrary loop properties.

After the original reads, at most eight fixed loop readers use the same pinned
evaluation time and shared three-second deadline. Each expected loop adds four
instant queries: stalled, its original timestamp, age and its original timestamp.
Retain the existing 4096-row/vector bound from pkg/api/limits.go. Skip reads when
there is no known up target; synthetic missing expectations and unknown inventory
still appear in coverage. Loop lists are closed policy, not a configurable quota.
Reuse the existing 32-item preview, 1000-node inventory, 120-second freshness,
30-second boundary/future skew, and the central ten-year finite nonnegative
uptime bound as a sanity bound for heartbeat age. No new limits are introduced.

A fresh reported bit requires a fresh successful up observation and a valid
0/1 loop_stalled sample. A fresh reported one remains visible even when age is
missing, stale, conflicting, invalid or unavailable. Item state describes the
complete evidence; reported_stalled records the independently valid bit.
reported_stalls overlaps state counts, and takes aggregate priority over
incompleteness. A stale reported one is not a current stall. Both fresh signals
are required for stalled/no_reported_stall item states. The producer owns its
configured stall budget; do not invent a common age threshold for vmmd's
configurable sweep. no_reported_stall means a complete fresh set of sampled
reports, not proven progress, uptime or uninterrupted absence of stalls. A frozen
sampler can keep exporting values; metric presence is not independent attestation
of the sampler. Missing or conflicting evidence never defaults to a clear bit.

Aggregate every inspected target-loop pair before previewing. Prioritize fresh
reported stalls, then incomplete observations, then complete clear reports, with
stable ordering. Exclude raw labels, endpoints and provider errors; expose only
approved loop/job/daemon/node names, complete target digests, bounded ages and
original clocks. Keep one conservative 64 KiB API envelope, dropping loop detail
before identity detail when needed. Counts always cover the full inspected set.

Retain only state/count/incompleteness summaries in the existing shared
16-event/16 KiB history. Routine age/clock/preview changes do not add events.
Separate loop progress tracks first/last reported stall and complete clear
observation, last recovery/regression, stall regressions, clear reports across
all observed polls, and sticky incomplete evidence. Source gaps break the
throughout flag without inventing a fault or clearing pending stall evidence.
Recovery means a later complete clear aggregate for the selected scope; it does
not prove recovery of the same daemon or reconstruct events between polls.
Coordinator clock regressions retain the existing monotonic observation order.

Decode optional loop metadata independently, including JSON type errors. Older
APIs omitting it and malformed loop metadata preserve valid identity, readiness,
process evidence, deployment qualification, exits, stability and poll scheduling.
Closed Python projection drops unreviewed fields before private artifact writes
or aggregate-only Markdown summaries. Invalid loop history is isolated from
valid current evidence and existing build history. Preserve the existing 1 MiB
report bound: loop detail yields before identity previews and older advisory
events, with explicit omissions and retained aggregate progress.

Regenerate optional preview SDK models. Verify all four profiles and exact loop
selectors, ready-but-stalled daemons, partial positive reports, missing loops,
conflicts, invalid values, stale/future/pre-boundary samples, source failures,
unknown/empty inventory, full counts beyond previews, byte bounds, recovery
after gaps, owned snapshots, clock-churn deduplication, retention, older APIs,
malformed optional JSON, unchanged health qualification and client round trips.
