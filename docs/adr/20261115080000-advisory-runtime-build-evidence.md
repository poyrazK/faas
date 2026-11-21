# ADR-20261115080000 · Advisory running-build evidence

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-15
- **Extends:** [post-deployment verification](20261113180002-post-deployment-monitoring-timeline.md).

The requested release SHA labels deployment monitoring evidence but its health
checks do not establish which binaries produced it. Add a read-only operator
`GET /v1/admin/obs/runtime-builds` with operator read scope, MFA and allowlist
enforcement. Accept only a full lowercase release SHA, an RFC3339 evidence
boundary and an optional explicit `single-box-v1` or `control-plane-v1` role.
Unselected roles return unknown without source reads. Never infer deployment
intent from observed targets.

Use the selected critical daemon jobs already defined by input health. This is
not a complete fleet assertion: static jobs use scrape membership with minimum
one target; compute jobs use exact node name and node ID membership in the
active registry with a configured gateway endpoint, as discovery does. Missing
registry members and unexpected replacement targets remain independently
visible even when target counts match. Inactive or unconfigured nodes are out
of scope. Unavailable, duplicate or invalid inventory remains unknown.

Pin strict `up`, build-info and original timestamp reads to one evaluation
time under a three-second shared budget, with at most six job readers. Match
complete target labels after excluding the metric name and build-only labels.
Never collapse duplicate targets or build series into one chosen revision.
Require fresh up and build samples within 120 seconds, no more than 30 seconds
before the rollout boundary or in the future. Down, missing, stale, pre-boundary,
invalid and conflicting evidence never establishes a matching build. Compare
only complete lowercase commit SHAs; dev/unknown revisions are unavailable.

Return fixed codes, approved node names, full SHA-256 target fingerprints,
commit SHAs, safe bounded version labels and original observation clocks.
Exclude addresses, arbitrary labels, build-time strings and raw source errors.
Aggregate complete inspected sets before retaining a deterministic 32-item
preview. Bounds in `pkg/api/limits.go` cap samples at 4096 per vector, expected
compute nodes at 1000, version labels at 128 bytes and responses at 64 KiB.
Expose per-job source/inventory availability and full-set counts. Mixed or
mismatched revisions can coexist with missing data; they are not completeness
claims. These are sampled metrics, not cryptographic binary attestation.

Extend schema-version-1 deployment reports with optional advisory
`runtime_builds`. `--expected-role` opts into one concurrent protected build
read per poll. Without it, retain the unknown role diagnostic without another
HTTP call. Older servers, source failures or invalid/stale responses remain
advisory unknown. Preserve the three health gates, stability rules, fault flags,
wait budget and exit codes. Never send probes, deploy or roll back. Own saved
evidence, and fit advisory build preview items before evicting existing report
history or primary fault detail. Retain counts, job availability and explicit
source/envelope omission accounting.

Verify revision mixing beyond previews, registry membership despite equal
counts, missing/stale/pre-boundary/future evidence, duplicate samples, unknown
revisions, unavailable inventory and sources, closed response validation,
protected HTTP/CLI reads, older-server compatibility, private saved reports and
unchanged health qualification. Production convergence remains separate.
