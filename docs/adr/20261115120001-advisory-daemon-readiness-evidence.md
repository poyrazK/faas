# ADR-20261115120001 · Advisory daemon readiness evidence

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-15
- **Extends:** [runtime builds](20261115080000-advisory-runtime-build-evidence.md),
  [collection](20261115090001-monitoring-build-evidence-transport.md),
  [history](20261115100001-observed-build-history.md), and
  [profiles](20261115110000-versioned-build-service-profiles.md).

A matching build SHA does not establish daemon readiness. Add optional readiness
alongside identity in the existing selected-role runtime-build response. Keep
identity state, the three deployment qualification checks, stability policy,
confirmed-fault flags, exits and poll scheduling independent of readiness.
Older responses that omit readiness remain valid build evidence; missing or
malformed optional readiness cannot establish readiness or discard valid build
identity. Invalid optional history is isolated from current evidence.

Read the existing daemon_ready and daemon_ready_reason families for each exact
versioned job/daemon selector. Use the same target labels, active configured
compute membership, successful up sample, pinned evaluation clock, 120-second
maximum age, 30-second skew and release boundary as identity evidence. Preserve
original readiness and active-reason timestamps. A fresh zero means not ready
only with a complete, fresh and consistent one-hot reason vector. Permit only
wire's ten fixed classes: ready, draining, database, vmmd, grpc, storage,
credentials, stale, process and other. Require all ten unique reason classes,
0/1 samples, one active reason, and agreement between ready and the active
class. Classify missing, stale, conflicting and unavailable evidence separately;
never copy raw readiness errors, targets or labels into summaries.

Each job adds four bounded instant-vector reads (readiness, its original
sample timestamp, reasons and their original timestamps). All readers retain
the existing shared three-second deadline, maximum eight jobs and strict
Prometheus response limits. Keep the 4096-target/vector limit; declare
ObsRuntimeReadinessReasonsMax=10 and ObsRuntimeReadinessReasonSamplesMax=40960
in pkg/api/limits.go to allow zero-valued reason series for every bounded target.
The registry remains capped at 1000 nodes. No new producer, probe, scrape job or
operational activation is introduced. Input-health roles and semantics remain
unchanged.

Aggregate readiness states and approved reason counts over the entire inspected
set before the existing 32-item preview. Fresh not-ready evidence takes priority;
otherwise missing/stale/conflicting/unavailable evidence, unknown inventory or
unexpected targets prevents a ready aggregate. Include per-target readiness in
the protected artifact, preserving the 64 KiB API and 1 MiB report envelopes.

Extend existing aggregate build history events with optional readiness counts
and reasons. State/count/reason changes produce events; new sample clocks and
preview churn do not. Retain the existing shared 16-event/16 KiB ring, explicit
retention metadata and report fitting priorities. Separate readiness progress
records first/last ready and not-ready, observed losses, last loss/recovery,
continuous readiness across observed polls and sticky incomplete evidence.
Unknown observations interrupt continuous readiness without fabricating a loss
or recovery; a known ready/not-ready transition can be observed across a gap.
A first not-ready observation is not a loss from previously observed readiness.
History describes aggregate sampled evidence, not fleet lifecycle transitions,
per-daemon transitions or uninterrupted readiness between polls.

Carry these optional fields through strict Go validation, SSH projection,
private artifacts, human output and aggregate-only job summaries. Regenerate
preview SDKs additively. Verify ready=0, draining/dependency reasons, conflicts,
old/missing sources, timestamp boundaries, full inventory beyond the preview,
loss/recovery/source gaps, snapshot ownership, bounded retention, version skew,
unknown-field filtering, unchanged health qualification and SDK round trips.
