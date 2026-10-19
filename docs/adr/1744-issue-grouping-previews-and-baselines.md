# ADR-1744: Offline grouping previews and reviewed regression baselines

Date: 2026-10-17

Status: Accepted

Extends: ADR-1457 (labeled grouping evaluation)

## Decision

Extend the existing offline evaluator, keeping its default aggregate output.
Explicit comparison modes report membership changes between supported strategies
or between a saved partition and the current implementation. They do not change
ingestion, grouping defaults, stored groups or manual merge/split rules. No HTTP
API, migration, SDK generation, network or database access is required.

A version-1 baseline contains a strategy, corpus SHA-256 and one assignment per
case. Name a group by its smallest case ID rather than a versioned fingerprint.
This compares equivalence classes: an identity/version change that preserves all
peers is not a merge/split. Include only structural reasons from the real verified
grouping explanation: basis, source/discriminator kind and selected frame/cause
indexes. Do not expose event strings, fingerprints, namespace values or family
labels. Case IDs are intentionally visible and must be reviewed for disclosure.

Hash the complete parsed corpus sorted by ID, including labels, namespaces and
event fields. Ignore JSON formatting and case order. Refuse a mismatched corpus,
unknown baseline version/strategy, incomplete/duplicate/foreign assignments,
cross-namespace groups, noncanonical representatives or invalid reason metadata.
Generate a new private file for explicit review; refuse overwrite. Checking never
rewrites or approves a baseline. A saved file is a review artifact, not a signed
attestation, and reviewers must assess the labeled input as well as assignments.

Compute baseline/candidate group, family and joint partition frequencies. Count
new/fixed false-merge and false-split pairs from their intersections without
enumerating pairs. Sorting snapshots is O(n log n); comparison/output is O(n).
Report each case whose peer membership changes once, including members that keep
the same representative while gaining/losing peers. Emit both decisions for those
cases; evidence-only changes with identical peers are omitted.

`-check` requires a saved baseline and fails for any newly incorrect pair. An
improvement elsewhere cannot offset it; equal aggregate error counts cannot hide
an exchanged error. Preview modes report regressions without failing. The native
command returns 2 for a regression, 1 for invalid input/output failure and 0 for
success. `go run` preserves failure but wraps the child's exit code.

Centralize the previous 10,000-case/8 MiB bounds in `pkg/api/limits.go`. Add 8 MiB
baseline, 128-byte printable non-whitespace case ID and 64-level JSON depth bounds.
Reject unknown fields, duplicate JSON keys and trailing documents. Baseline and
corpus paths must be bounded regular files, not symlinks. Output is bounded by
the number and size of case IDs, not the number of possible pairs.

Commit the existing cause-aware synthetic partition as the initial reviewed
baseline: 19 cases, 11 groups, zero false merges/splits. Run it in both the
normalizer and evaluator suites because light CI tests changed packages. Add
`make issues-grouping-check` for explicit review/CI use. A corpus change requires
new review rather than silently generating expected results during a check.

## Consequences and limits

Developers can inspect grouping changes and detect regressions across code
versions without experimenting on live groups. Independently curated, sanitized
incidents use the existing input schema; this work does not infer labels, export
incident contents or claim that sanitizing arbitrary evidence is lossless.

The initial corpus is synthetic and cannot establish production accuracy. No real
incident corpus has been supplied. Symbolication accuracy, delivery, capture
coverage, application-specific grouping rules and live rollout remain outside
this tool. Privacy of identifiers and correctness of family labels require review.

## Local validation

The complete normalizer, evaluator and command suites pass with race detection.
Comparison counts and affected cases agree with a quadratic oracle over 200
deterministic random partitions. A 10,000-case fixture verifies exact counts for
49,995,000 newly merged pairs without a quadratic report. Tests cover exchanged
errors with equal totals, namespaces, reordered input, input preservation, all
three strategy snapshots, corpus binding, strict JSON and disclosure boundaries.

The compiled command verifies unchanged-baseline exit 0, regression exit 2 and
changed-corpus/overwrite exit 1. Fresh private snapshots match the committed
snapshot deterministically. Full uncapped lint of the three packages reports
zero issues; `make issues-grouping-check`, ADR-number uniqueness and full
`make spec-check` pass. No production incident accuracy or live rollout evidence
is claimed by these offline checks.
