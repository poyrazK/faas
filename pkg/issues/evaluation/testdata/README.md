# Grouping corpus

Run `make issues-grouping-eval` from the repository root. The JSON output compares
legacy, failure-site, and cause-aware grouping on the same labeled corpus.
Use `go run ./cmd/issues-grouping-eval -corpus PATH -strategy cause-chain-v1` for
an independently curated corpus.

Each case has a unique `id`, a ground-truth `family`, an event, and optional app
and environment namespaces. A false merge is a pair from different families
placed in one group. A false split is a pair from one family placed in different
groups. Precision measures same-family correctness among grouped pairs; recall
measures recovered same-family pairs. Missing denominators remain unknown.

The checked-in examples are synthetic and cover changing callers, moved lines,
stable codes, wrappers, Python stack order, Go causes without creation stacks,
manual overrides, namespaces, and HTTP fallbacks. They do not establish real-world
accuracy. Extend them with reviewed, sanitized incident labels and preserve
differences that matter operationally. Never use source contents, bodies, secrets,
or customer identifiers in a shared corpus.

The tool evaluates grouping identity, not capture delivery or symbolication
accuracy. It synthesizes valid event IDs/timestamps and removes read-only mapping
stamps while preserving reported frames. At most 10,000 cases and 8 MiB of input
are accepted. Aggregate reports omit event content and family labels.

## Membership previews and reviewed baselines

`-strategy CANDIDATE -compare-with BASELINE_STRATEGY` reports changed membership
using the same corpus and both real normalizers. It lists each affected case once,
including cases that retain their group name but gain or lose peers. A group is
named by its lexicographically smallest member ID; the name is an offline
representative, not a live fingerprint or issue ID. `merged`, `split` and
`merged-and-split` describe peers joining and/or leaving each case's group.

Reasons report only structural inputs: evidence basis (`application-frames`,
`failure-site`, `fallback`, `override`), source kind, discriminator kind
(`none`, `message`, `error-code`, `override`), frame indexes and cause index.
Indexes refer to normalized evidence, including parsed stack frames when no
frames were supplied. Selected frame paths/functions, messages, exception types,
error-code/override values, family labels and app/environment names are omitted.
Changing evidence selection without changing peers does not create a case change.

Case IDs and group representative IDs are intentionally included in previews
and snapshots. Give each case a non-sensitive, stable identifier of at most 128
UTF-8 bytes, with printable non-whitespace characters. Family, app and environment
labels stay local to the input and its opaque corpus digest. Sanitize an incident
before labeling it; if sanitation changes meaningful grouping evidence, preserve
that distinction explicitly rather than inventing a production accuracy result.

Create a version-1 snapshot for review with:

```sh
go run ./cmd/issues-grouping-eval -corpus PATH -strategy cause-chain-v1 -write-baseline new.baseline.json
```

The file is created with mode 0600; existing files and symlinks are never
overwritten. Review the input labels and all assignments before committing a
baseline. Generation is not approval. The checked-in `cause-chain-v1.baseline.json`
records the original 19 synthetic examples (11 groups, no false merges/splits).
It adds no production evidence. No real incident corpus has been supplied.

```sh
go run ./cmd/issues-grouping-eval -corpus PATH -strategy cause-chain-v1 -baseline new.baseline.json -check
make issues-grouping-check
```

Without `-check`, comparison is a preview. With `-check`, the compiled command
returns 2 if any newly incorrect pair appears, 1 for invalid inputs/output errors
and 0 otherwise. `go run` itself wraps a child exit 2 as a nonzero tool exit.
False merges and splits cannot offset one another, and replacing one wrong pair
with another still fails despite equal aggregate metrics. Reports include exact
new/fixed pair counts; they never enumerate the potentially quadratic pair list.
Case details are bounded by the 10,000-case input limit.

A baseline must assign every case exactly once, keep app/environment namespaces
separate and use the canonical representative IDs. A SHA-256 digest binds the
full parsed input (sorted by ID), not just labels or counts. Formatting and case
order do not matter; changes to event fields, labels, IDs or namespaces require
a new snapshot and review. Both files are bounded to 8 MiB, reject unknown fields,
duplicate keys and trailing documents, and limit JSON nesting to 64. All limits
live in `pkg/api/limits.go`. Checks do not rewrite files, change live strategies,
apply manual rules or contact Gregale. The reviewed baseline gate runs in both
the normalizer and evaluator suites, so light CI checks changes in either package.
