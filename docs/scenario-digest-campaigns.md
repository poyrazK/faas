# Comparing campaign digests

The offline digest command compares each exact configuration fingerprint independently:

```sh
gregale test digest --from current.json --baseline previous.json --markdown digest.md
```

A repeated or seeded campaign can contain several configurations. Each matching
configuration gets its own failure counts, executed-run denominator, frequency
change, worst observation, and supporting receipt links. Different repeat counts
are allowed; changing the mixture of configurations does not blend their rates.

Configurations executed on only one side are not comparable. Missing fingerprints,
incomplete phase/assertion coverage, and faults without matching evidence remain
not comparable. A group is no longer observed only when the matching assertion
has passing evidence in every executed run for that configuration.

Configuration identity comes from recorded SHA-256 fingerprints, including reduced
replay configurations. This is exact configuration matching, not seed inference:
reports without fingerprints cannot establish equivalence. Older reports with
replay fingerprints remain supported.

Comparison sections display the configuration fingerprint. Counts of newly
observed, persisting, no longer observed, and not comparable entries count
configuration/group pairs. The ordinary current-report digest still groups
recurring failures across configurations. Comparison ordering is deterministic
by fingerprint, then by change category and frequency change.

Evidence links retain the top-level receipt index from the original report.
The command remains informational and does not alter test verdicts or gate CI.

## Machine-readable comparison

```sh
gregale test digest --from current.json --baseline previous.json \
  --markdown digest.md --comparison-json comparison.json
```

The optional JSON output requires a baseline and a distinct output path.
Both inputs are protected against output aliases. The JSON contains every
configuration/group comparison; Markdown's `--limit` does not truncate it.

Schema version 1 contains `schema_version`, `informational: true`, status
`counts`, and a `comparisons` array (empty when no groups exist). Each comparison
contains its configuration SHA-256, structured group key, status, optional
`not_comparable_reason`, nullable `frequency_delta_percentage_points`, and
`baseline` / `current` observations.

Each observation contains failures, executed runs, nullable worst value and
associated limit, comparison operator when available, and all recorded
occurrences with run ID, attempt, failure, and optional replay command.
`first_supporting_receipt_index` is zero-based and null without a supporting
failure. Optional evidence URLs use the original report's receipt anchor when
a supporting failure exists; otherwise they point to the full report.
Unavailable numbers are null, never fabricated zeroes. Missing configuration
fingerprints are omitted and cannot establish comparability.

Consumers should check the schema version and tolerate additional fields.
Replay commands retain original report/manifest placeholders; consumers must
supply those paths explicitly. JSON strings are data, not shell or HTML source.
The output is descriptive and never changes a test verdict or gates CI.

Outputs are written sequentially, Markdown first. A write failure returns a
nonzero exit status; an earlier successfully written output may remain.
