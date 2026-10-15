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
