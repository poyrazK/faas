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

## Policy lint and preview

```sh
gregale test policy lint --policy regression-policy.yaml
gregale test policy lint --policy regression-policy.yaml --from current.json
gregale test policy preview --policy regression-policy.yaml --from current.json --baseline previous.json
```

Both commands emit versioned informational JSON to stdout. Strict policy parsing
rejects conflicting overrides, invalid settings, duplicate names, and unknown
fields. Static lint warns when an earlier rule's exact selectors contain every
combination matched by a later rule (`shadowed_rule`). It does not attempt to
prove shadowing by a union of several earlier rules.

Supplying reports adds per-rule observed match and selection counts, plus
`no_observed_match` and `never_selected` diagnostics. Lint accepts an optional
current report and optional baseline; the baseline requires the current report.
Observations are unique configuration/context pairs and comparison first-failure
groups. Rules may match both kinds, so these counts are not execution counts.
Assertion selectors do not match context-only observations.

A rule with no observed match may apply to passing assertions, other suites,
or future failures. The warning is advisory, not proof of an invalid selector.
Without reports, `observations_supplied` is false and observed counts are zero;
static lint does not emit no-match warnings.

Preview requires both raw reports. Its `prospective_result` uses the same
evaluator as policy-gated comparisons: comparison decisions show matched rules
and effective thresholds, and violations explain what would reject the run.
No policy is changed or published. Preview and lint return 0 even for warnings
or predicted policy rejections; invalid policies, inputs, options, and output
errors return 1. Use `test compare --policy` when a CI gate is intended.

JSON redirection must use a distinct destination because the shell opens it
before Gregale can inspect source files. Existing digest and gate behavior is
unchanged.

## Explicit required coverage

Observed-context checks cannot detect a critical scenario absent from both
reports. Declare independent requirements to make that absence a policy failure:

```yaml
required_coverage:
  - name: checkout-executed
    match:
      scenario: checkout
      engine: real-vm
    min_executed_runs: 5
  - name: checkout-recovery-p95
    match:
      scenario: checkout
      phase: recovery
      kind: threshold
      assertion: p95_ms
    min_executed_runs: 5
```

Add this section to a version 1 policy alongside its existing global settings.
Each requirement needs a unique nonempty name, a nonempty scenario selector,
and its own positive minimum. At most 100 requirements are supported.
Selectors use the same exact-match syntax as scoped rules. Configuration
selectors must be lowercase SHA-256 fingerprints. Assertion requirements must
specify phase, kind, and assertion; named assertion evidence also uses request.

Both reports must meet every requirement. Requirements are checked independently
for every matching configuration/scenario/profile/engine/case context in either
report. Counts are never pooled across seeds or contexts. If neither report
contains a matching context, one failed requirement is emitted using the
declared selector. A scenario-only requirement requires an executed leaf receipt
with a recorded fingerprint; status may be passed or failed. Skipped and unknown
statuses do not count. Explicit requirements cannot be waived by scoped rules,
`on_not_comparable: ignore`, or `on_insufficient_samples: ignore`.

A nonempty step selector requires exactly one executed staged step; omitting
step (or selecting an empty step) uses the top-level load. Phase evidence must
have a unique matching name, positive duration and attributed requests, and
exercised fault evidence when applicable. Recovery uses its preceding fault
phase. Assertion coverage checks the raw typed evidence, even when it passed
or was hidden by an earlier failure. Coverage does not require the assertion
to pass: regression checks and original test verdicts remain separate.

Supported assertion kind/name pairs are threshold/metric name,
http_attempts/attempts_per_request, tcp_attempts/attempts_per_request,
per_request_attempts/max_attempts, recovery/recovery,
recovery/step_recovery (with request), request_deadline/request_deadlines
(with request), circuit_breaker/circuit_breaker,
connection_leaks/connection_leaks, and pool_exhaustion/pool_exhaustion.

JSON and preview expose `required_coverage` results for passing and failing
requirements, with the minimum, executed and evidence-covered counts, and
zero-based supporting top-level receipt indexes. Failure violations use
`explicit_required_coverage` and policy rule `required_coverage/<name>`;
their nested requirement retains the selector and evidence references.
JUnit includes those diagnostics through the same policy-failure mapping.
A passing JUnit gate includes requirement results in system-out.

Lint with both reports shows requirement results and
`required_coverage_missing` warnings; static lint and single-report lint only
validate requirement definitions. Preview stays informational; a gated
comparison returns 2 when any explicit requirement fails. Existing policies
without `required_coverage` retain their behavior.

## Coverage-driven campaign planning

```sh
gregale test policy plan --policy regression-policy.yaml --manifest gregale-test.yaml
gregale test policy plan --policy regression-policy.yaml --manifest gregale-test.yaml --from current.json --baseline previous.json
```

The read-only planner emits informational JSON. It reads and expands the
manifest using the existing manifest reader, including assertion profiles,
outage cycles, seeded campaigns, and fault sweeps. It never invokes setup,
application commands, provisioning, or test execution.

Each required coverage entry is matched to native lifecycle profiles and exact
manifest case/configuration fingerprints. With no profile selector, the plan
includes warm, cold, and restored. Existing evidence-covered runs are counted
only for matching fingerprints and contexts. Output shows the minimum, covered
counts on each side, additional leaf runs, and repetitions needed after
accounting for resilience.runs. Without reports, both sides start at zero.
The baseline option requires a current report.

Suggestions are argument arrays, labeled baseline or current. A command's
repeat count never exceeds 20. Larger deficits produce separate batches; at
most 20 commands per side/context are shown, with remaining repetitions
reported explicitly. Run suggestions on the intended source revision for that
side. Use fresh report paths, then combine the receipt arrays with that side's
existing report before reevaluating coverage. Planning does not update reports.

The runner executes a full scenario matrix, even when an entry names one
case. Suggestions for different entries are independent and may duplicate work;
they are not a minimal campaign or a total cost estimate. Existing resource
guards, budgets, and cleanup rules still apply to execution. Collecting new
evidence and keeping baseline provenance are separate from reviewing this plan.

Entry statuses are covered, needs_runs, unsatisfiable, unsupported,
needs_review, or unavailable_configuration. Absent scenarios, steps, phases,
assertions, cases, and explicitly requested fingerprints are flagged.
Historical observed contexts that cannot be regenerated from this manifest
remain visible and require their original manifest. Local/simulated coverage
planning is unsupported because current digest fingerprints describe native
runs. Resource assertion placement (connection leaks and pool exhaustion)
requires manual review and receives no execution suggestion.

Declaration checks establish that evidence can be requested, not that runtime
traffic or fault matches will satisfy coverage. A required phase uses its
exact name and selected staged step; phase-only checks do not invent assertion
budgets. Existing coverage checks remain the source of truth after execution.

Planning returns 0 even with blocked entries, like preview. Invalid policies,
manifests, reports, options, or stdout errors return 1. No requirements produces
an empty plan. Shell output redirection must use a separate file.

## Merging batch reports

```sh
gregale test report merge --output combined-current.json current.json extra-batch-1.json extra-batch-2.json
gregale test policy preview --policy regression-policy.yaml --from combined-current.json --baseline previous.json
```

Merge accepts 2..100 report paths after its flags and produces the same nonempty
JSON receipt-array format used by digest, preview, and policy comparisons.
Input order and each source's top-level receipt order are retained. Campaign
hierarchies, statuses, run IDs, replay metadata, numeric precision, and unknown
evidence fields are preserved. Counters and verdicts are not recomputed.

Each output root receives `report_provenance`: a versioned list of source-report
SHA-256 hashes, source file basenames, and zero-based original receipt indexes.
Merging an already merged report appends its immediate source while retaining
earlier lineage. Metadata is descriptive, not cryptographic proof of execution.
Original evidence references remain available through provenance; an evidence
URL supplied to digest must still correspond to the report being summarized.

Duplicate run IDs anywhere in primary matrix/resilience hierarchies are errors.
Repeated report content and overlapping source receipt provenance are rejected,
including overlaps involving skipped receipts. There is no silent deduplication
or force flag. Embedded diagnostic/minimization trials remain evidence and are
not treated as additional campaign execution leaves.

Executed passed/failed leaves require stable run IDs. Older or local/simulated
reports without such identities cannot be safely merged; record distinct
identified runs instead. Skipped leaves and aggregate parents may lack IDs.
Unsupported receipt metadata, ambiguous or empty aggregate hierarchies, and
invalid provenance are rejected. Merge does not authenticate run IDs or detect
deliberately rewritten evidence.

Inputs cannot alias the output, including hard links and symlinks. All input
decoding and overlap checks complete before writing. The output is written via
a private temporary file in its destination directory and renamed only after
successful encoding. Failures leave existing destination contents intact.
Combined inputs and output are each limited to 64 MiB; receipt depth, node
count, and provenance history are also bounded.

Keep baseline and current batches separate and preserve their source revision.
Merge records input origins and checks source identities as described below.
Environment provenance is not inferred. Each successful merge is
informational and returns 0 even when receipts record failures; errors return 1.

## Source revisions and build provenance

Native real-vm runs record versioned `source_revision` metadata for the primary
app and every deployed service or fixture. Each workload records the uploaded
source SHA-256, deployment ID, runtime, shape, and handler. Git commit and tracked
working-tree changes are captured best-effort from that workload's actual source
directory with a bounded local Git lookup. No CI checkout revision is assumed
to be the tested application's revision. Unavailable Git metadata stays absent
or null. The tracked-change flag excludes untracked files; uploaded source
hashes identify the archived bytes, including included untracked changes.

The expected workload set is recorded before deployment. Missing deployment
metadata therefore cannot look like a complete multi-service identity.
Metadata is observational: a failed deployment can retain a queued deployment
ID and uploaded source hash, without claiming that deployment became ready.
Git observations and upload creation are separate moments; archive hashes are
the evidence for the uploaded bytes. Runtime build settings identify inputs,
not a reproducible binary or container image digest.

By default, report merging requires a complete source identity on every
executed leaf and one matching identity per scenario/engine across all inputs,
profiles, and matrix cases. Identity includes workload membership, source hashes,
available Git metadata, and build settings. Deployment IDs are preserved as
build-instance evidence but excluded from equality. Different scenarios may
legitimately use different sources. Skipped receipts need no identity.

Missing identities and mixed revisions/build inputs reject the merge before
the destination is changed. For intentional forensic aggregation, use:

```sh
gregale test report merge --allow-mixed-revisions --output forensic.json batch-a.json batch-b.json
```

This explicit override marks output roots with
`report_merge_revision_override: true`. It permits missing or inconsistent
source metadata, but never bypasses duplicate IDs, source provenance overlaps,
or input/output protection. Earlier override markers survive further merges.
Older reports remain readable by other commands; strict merging cannot prove
their source identity from the primary-only legacy source hash.

Comparison JSON and policy preview contain separate `baseline_sources` and
`current_sources` summaries, with source identities, representative workload
metadata, executed-run counts, supporting top-level receipt indexes, unknown
source counts, and an override marker. Each representative retains the first
supporting run's deployment IDs; full receipt evidence retains all builds.
JUnit failure details and accepted-policy system-out expose the same summaries.
Comparisons intentionally allow different source revisions across baseline and
current; provenance does not change configuration matching or policy verdicts.

These fields do not authenticate edited reports or prove environment equivalence.
Git metadata is available only when the source directory is a readable checkout;
local and simulated executions currently have unknown source identity.
