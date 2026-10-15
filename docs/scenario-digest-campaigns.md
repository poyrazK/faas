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

## Opt-in regression policy

The existing comparison command supports an explicit campaign policy mode:

```sh
gregale test compare --from current.json --baseline previous.json --policy regression-policy.yaml > policy-result.json
```

Example policy:

```yaml
schema_version: 1
fail_new_failures: true
max_frequency_increase_percentage_points: 10
min_executed_runs: 5
on_not_comparable: fail
on_insufficient_samples: fail
```

Policies use strict YAML: unknown fields, duplicate fields, multiple documents,
unsupported versions, and nonfinite or out-of-range limits are errors.
The version, positive sample minimum, and both handling actions are required.
Actions are `fail` or `ignore`. The new-failure check defaults to false;
omitting the frequency limit disables that check.

Exit status is 0 when the policy passes, 2 for policy violations, and 1 for
invalid options, inputs, or output errors. A versioned JSON result goes to stdout
and includes all comparisons and violations, with rule names, context,
measurements, coverage reasons, supporting receipt references, and replay
commands where available. Optional `--evidence-url` and
`--baseline-evidence-url` attach HTML report links. Redirect stdout to a
distinct file: shell redirection happens before Gregale can protect input files.

Frequency limits use percentage points and fail only when the increase is
strictly greater than the configured limit. New failures and frequency limits
are evaluated only for comparable groups with sufficient samples on both sides.
If insufficient samples are ignored, those groups do not trigger regression
checks. Counts describe first recorded failures, not statistical confidence.

Coverage includes every configuration/scenario/profile/engine/case context in
either report, even when every run passed. Every context therefore requires
matching executed coverage on both sides. Missing fingerprints, missing
configurations, skipped-only contexts, and incomplete assertion/fault coverage
follow `on_not_comparable`; inadequate nonzero context counts follow
`on_insufficient_samples`. Coverage and group violations may both describe
the same missing context. Both reports can contain no failure groups and still
fail required coverage checks.

Policy mode uses named `--from` and `--baseline` inputs. It does not combine
with legacy positional comparisons, `--budget`, or legacy output flags.
Without `--policy`, existing comparison behavior is unchanged. Digest output
remains informational and continues to return success for recorded test failures.

## JUnit policy results

Add `--junit` in policy mode to publish decisions in CI test reports:

```sh
gregale test compare --from current.json --baseline previous.json --policy regression-policy.yaml --junit policy-results.xml > policy-result.json
```

The optional XML output contains a `gregale.campaign-policy` testsuite.
Each violation becomes one failed testcase. Names include the rule,
configuration fingerprint, and structured context; an ordinal distinguishes
multiple violations. Failure details contain JSON with policy thresholds,
baseline/current sample counts, measurements, coverage explanations, report
links, and recorded replay commands where available. XML metacharacters are
escaped by the standard encoder.

A passing policy produces one passing `campaign policy accepted` testcase.
This means the configured policy accepted the comparison, including explicitly
ignored coverage/sample conditions; it does not assert that all application
tests passed. Policy and application JUnit reports can be uploaded separately.

The policy's JSON stdout and exit statuses are unchanged: 0 accepted,
2 rejected, 1 invalid input or report write error. XML is written before JSON
stdout, including for policy rejections. A later stdout failure can leave a
complete XML report. Invalid inputs do not generate a new report; existing
outputs may remain, so CI should use a fresh artifact path per job.

The JUnit path cannot alias either input report, the policy file, or an existing
file used for JSON stdout, including hard links. Shell redirection can truncate
an input before Gregale starts; always redirect JSON to a separate destination.
JUnit output is available only in explicit `--policy` mode.

## Scoped policy rules

Optional ordered `rules` override the global settings:

```yaml
schema_version: 1
fail_new_failures: true
max_frequency_increase_percentage_points: 10
min_executed_runs: 5
on_not_comparable: fail
on_insufficient_samples: fail
rules:
  - name: checkout-recovery
    match:
      scenario: checkout
      phase: recovery
    fail_new_failures: true
    max_frequency_increase_percentage_points: 0
  - name: search-latency
    match:
      scenario: search
      assertion: p95_ms
    fail_new_failures: false
    max_frequency_increase_percentage_points: 5
  - name: reset-samples
    match:
      scenario: dependency-reset
    min_executed_runs: 20
```

All specified selectors must match exactly. Supported selectors are
`configuration_sha256`, `scenario`, `profile`, `engine`, `case`, `step`,
`phase`, `kind` (assertion kind), `assertion`, and `request`. There are no
globs, regular expressions, or implicit phase-role matching. An omitted selector
matches any value; an explicit empty string matches only an empty value.

The first matching rule wins. Omitted settings inherit global defaults, never
later rules. Put specific exceptions before broader rules. Set
`disable_frequency_check: true` to disable an inherited frequency limit;
combining that setting with a limit is invalid. Boolean false and numeric zero
are explicit overrides. Each rule needs a unique nonempty name other than
`default`, at least one selector, and at least one override. At most 100 rules
are accepted. Existing policies without rules retain their global behavior.

Assertion comparisons use all selectors. Whole-context coverage checks only
consider rules whose selectors are limited to configuration, scenario, profile,
engine, and case. Assertion-specific exceptions cannot waive a missing context
or a global context sample minimum. Context requirements remain independent;
use a context-level rule to change them. Matching coverage and assertion sample
violations for the same rule/context are reported once.

JSON `decisions` records the zero-based comparison index, `policy_rule`
(`default` when no rule matched), and effective settings for every comparison,
including accepted or ignored comparisons. Violations also carry the matched
rule and effective settings. JUnit failure names include the rule name and
failure details use the effective thresholds; accepted JUnit output includes
the comparison decisions. These are additive fields in result schema version 1.
