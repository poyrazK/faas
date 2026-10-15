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

Top-level batches contain argument arrays, labeled baseline or current. Each
repeat count is at most 20, with at most 20 batches per shared scenario/profile/side
group. Entries reference shared batch IDs; execute each top-level batch once.
Remaining deficits are reported explicitly. Run each side at its intended source
revision, use fresh report paths, and merge receipts into that side before
reevaluating coverage. Planning does not update reports.

The runner executes the full matrix even when a requirement names one case.
The cost-aware allocation below accounts for sibling cases and campaign runs.
Existing resource guards, budgets, and cleanup rules still apply to execution.

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

## Environment compatibility

Native receipts add an allowlisted `environment` snapshot from the provisioned
app responses before deployment and test traffic. It records each logical
workload's app type, runtime label, RAM, vCPU, CPU share, concurrency limits,
timeouts, minimum instances, declared Postgres use, fixture type/failure count,
and TCP ports. Workload resource values are observations at capture time, not
proof that configuration stayed fixed throughout execution.

Secrets, environment variables, credentials, endpoint URLs, app/deployment IDs,
and transient hostnames are excluded. Dependency declarations do not disclose
connection strings. Remote database versions, external dependency resources,
platform binary versions, and exact runtime binary versions are not available
from the current capture API. Platform/runtime versions are explicitly null,
rather than copied from the CLI host. The API runtime label is still recorded.

Comparison JSON exposes `environment_comparisons` for every executed
configuration/scenario/profile/engine/case context, including all-passing ones.
Each includes baseline/current snapshots, counts, supporting receipt indexes,
field-level differences, and unknown fields. Status is compatible, incompatible,
or unknown. Different or internally varying observed fields are incompatible;
missing snapshots, contexts, fingerprints, versions, or required resource fields
prevent a complete compatibility claim. Complete metadata is required for
compatible; equal recorded resources with unavailable versions remain unknown.

Snapshot equality is conservative. Multiple distinct snapshots within either
side are incompatible even if both reports contain the same mixtures. A field
with missing version evidence is reported as unknown rather than a proven
version mismatch. Differences list sorted JSON value strings for each side.

An optional policy block controls handling:

```yaml
environment:
  on_mismatch: fail
  on_unknown: ignore
```

Both actions must explicitly be fail, not_comparable, or ignore. Omitting the
block preserves existing behavior by ignoring environment compatibility.
Scoped rules can replace the entire block; they inherit it when omitted.

Fail rejects the environment condition and suppresses associated application
frequency checks. Not_comparable suppresses those checks and follows the
selected `on_not_comparable` setting. Ignore retains normal comparison
behavior while preserving compatibility diagnostics. Policy-adjusted comparison
counts and deltas reflect not-comparable groups; raw digest comparison JSON
remains descriptive and retains its original group statuses.

Whole-context checks use only context-level scoped rules, independently of
assertion-specific exceptions. An assertion rule cannot waive a broader
context-level environment requirement. JSON preview includes matched
`environment_decisions`, effective settings, and any violations with full
environment evidence. JUnit failures include the same diagnostics; accepted
policy system-out includes environment comparisons and decisions.

Legacy, local, and simulated reports remain readable with unknown metadata.
This change does not add network version probes, infer equivalence from missing
fields, or alter original application test verdicts.

## Required environment expectations

Compatibility alone cannot detect two equally incorrect setups. Add independent
expectations to a version 1 policy:

```yaml
required_environments:
  - name: checkout-resources
    match:
      scenario: checkout
      engine: real-vm
    expect:
      workloads:
        checkout:
          type: function
          runtime: go124
          ram_mb: 256
          vcpu: 1
          cpu_millicores: 500
          postgres: true
        database: {}
  - name: platform-version-evidence
    match:
      scenario: checkout
    expect:
      require_platform_version: true
```

Workload keys are the manifest's logical project/service names. An empty workload
expectation (`database: {}`) requires that workload to be present. Fixture
expectations must use the exact fixture label recorded in the snapshot; use
presence alone for application-owned database services.

Each requirement needs a unique nonempty name, a nonempty scenario selector,
and at least one expectation. Selectors support configuration SHA-256, scenario,
profile, engine, and case; step/phase/assertion selectors are invalid. At most
100 requirements and 32 workload expectations per requirement are accepted.

Expectation values use exact equality. Supported workload fields are type,
runtime, runtime_version, ram_mb, vcpu, cpu_millicores, max_concurrency,
concurrency_per_vm, min_instances, postgres, fixture, fail_first, and tcp_ports.
Resource values must be positive; minimum-instance/failure counts may be zero.
Explicit false, zero, empty fixture strings, and empty port arrays are checked.
Port order does not matter; duplicate or out-of-range expected ports are invalid.
Omitted fields are unconstrained. Additional workloads are allowed.

Use platform_version or a workload's runtime_version for an exact version,
or require_platform_version / require_runtime_version to require known metadata
without pinning its value. Current native snapshots cannot supply precise
platform/runtime binary versions, so requiring those fields intentionally fails
until evidence is available. Runtime labels such as go124 are captured separately
and can be required today. No missing version is inferred from the CLI host.

Every passed or failed executed leaf in each matching configuration/context
must satisfy the expectations, and each side must contain at least one matching
executed leaf. Skipped entries do not count. Missing scenarios in both reports,
missing snapshots, unknown configuration identities, missing required workloads,
and unavailable required versions fail explicitly. Complete version 1 snapshots
with valid basic workload resources are required before checking expectations.
These requirements cannot be waived by compatibility actions, scoped exceptions,
or sample/coverage ignore settings. They do not replace sample-count requirements.

Policy JSON and preview include all required_environments results, executed and
satisfied counts, zero-based supporting receipt indexes, and per-run expected
versus observed issues. Issues include run IDs and source receipt indexes where
available. Display is capped at 50 issues per side/context, with omitted counts;
satisfaction counts always reflect every run. No snapshot secrets or transient
identifiers are added.

Failures use rule required_environment and policy rule
required_environments/<name>. JUnit retains the full requirement result in each
failure, and accepted policy system-out includes successful requirements.
Lint with both reports emits required_environment_failed warnings and results;
without both reports it validates definitions only. Preview remains informational.
Gated comparisons return 2 for unmet requirements, even when the two environments
are compatible. Existing policies without this section retain their behavior.

## Environment-aware coverage planning

The existing read-only policy planner now includes required_environments:

```sh
gregale test policy plan --policy regression-policy.yaml --manifest gregale-test.yaml --from current.json --baseline previous.json
```

The environment_requirements array shows each matching manifest
configuration/context, manifest checks, fields needing platform observations,
version evidence requirements, and separate baseline/current actions. It is
also emitted for policies with no required_coverage entries.

Manifest checks cover workload presence, declared Postgres use, fixture labels,
fixture failure counts, and TCP ports. Checks show expected versus declared
values and whether a declaration needs changing. Explicit false/zero/empty-port
expectations retain their meaning. Port ordering does not cause differences.

Resources, app type, and runtime labels are not inferred from source paths or
scenario declarations. Matching report evidence is required for those values.
A plan records the original expected/observed evidence and receipt references.
Report evidence is an observation at capture time, not a live platform probe.

Side actions distinguish:

- manifest_change_required: fix workload/dependency declarations first;
- platform_evidence_required: confirm resources/runtime on the platform;
- platform_change_required: correct recorded mismatches and replace the bad batch;
- report_evidence_required: replace receipts with missing required evidence;
- version_evidence_required: current native capture cannot collect the required
  precise platform/runtime versions;
- collect_execution_evidence: declarations match and runs can record proof;
- ready: existing matching report evidence satisfies the expectations;
- ready_existing_evidence: recorded versions satisfy the requirement, but extra
  runs cannot currently collect new version evidence.

Unsupported engines, absent cases/fingerprints, and historical configurations
without a matching manifest remain explicit blockers. Missing matching reports
are not treated as proof that resources or versions are correct. Existing
noncompliant executed receipts set report_replacement_required: adding more
repetitions does not remove their failures from an every-run requirement.

Coverage entries include side-specific environment_blockers. Blocked command
batches are removed; an unblocked side can still receive suggestions.
environment_blocked and partially_environment_blocked explain the resulting
coverage status. Counts of already recorded coverage are retained. Existing
version evidence does not block a fully covered entry, but does block suggestions
for new runs that cannot collect required versions.

A scenario command runs its full matrix. Environment prerequisites on one case
therefore suppress commands for sibling cases in the same scenario/profile.
Prerequisites are conservative: unresolved historical environments can require
review of the original manifest before collecting additional evidence.

The planner does not modify resources, dependencies, manifests, or report files.
It makes no platform calls and provisions nothing. Readiness concerns only the
explicit requirements and available observations; it does not establish general
environment equivalence. Planning remains informational and returns 0 for
blocked entries. Existing plans without environment requirements retain their
run suggestions.

## Read-only environment preflight

```sh
gregale test policy preflight --policy regression-policy.yaml --manifest gregale-test.yaml --targets existing-workloads.yaml > environment-preflight.json
```

An explicit target mapping identifies existing apps to inspect:

```yaml
schema_version: 1
scenarios:
  checkout:
    checkout: existing-checkout-app
    database: existing-database-app
```

Keys are manifest scenario and logical project/service names; values are existing
platform app slugs. Unknown scenarios/workloads, invalid slugs, duplicate mappings
to the same app within a scenario, multiple YAML documents, and unknown fields
are rejected. Missing mappings are reported as unavailable rather than guessed.
Map the primary workload and any workloads referenced by environment requirements.
No app is selected from a hostname, execution report, or project-name guess.

The command requires a policy with required_environments and existing CLI
authentication. It reads app metadata through GetApp only, caches repeated
target reads for this invocation, and bounds the read phase to 30 seconds.
It does not create sessions, deploy, wake apps, run application commands,
change resources, or inspect secrets. Read failures retain generic diagnostics;
raw API errors, endpoint URLs, app IDs, and credential-bearing strings are not
copied into the report.

Checks show their source. Manifest declarations cover workload/dependency
configuration; live platform observations cover app type, runtime label, RAM,
CPU, concurrency limits, timeouts, and minimum instances. Runtime labels are not
precise runtime binary versions. Platform/runtime binary versions remain
unavailable. Existing app resource reads do not verify Postgres attachment,
fixture identity/failure behavior, or declared inbound TCP ports; requested
fields remain unavailable with declaration checks shown separately.

The output is a distinct versioned platform_preflight document with
execution_evidence false, observation timestamps, target-file SHA-256, logical
workload observations, and per-requirement readiness/checks. It contains no run
IDs, attempts, execution counts, or coverage credits. It cannot be used as a
merged test report. Selector context is intended scope only:
execution_context_verified stays false because reading settings cannot prove
a lifecycle profile, matrix case, configuration execution, or application
behavior. Manifest selectors that cannot be generated are flagged separately.

Ready means the requested observable fields matched, required app metadata was
readable, and no requested field was unavailable or contradicted. Declared-only
dependency settings cannot become live proof. A readable undeployed app can
have valid resource settings without a live deployment; app readiness and
campaign execution remain separate. Observations are taken at read time and do
not guarantee settings on future expiring test environments.

Output is informational: mismatches and unavailable metadata return 0 with
ready false. Invalid options/policies/manifests/mappings, authentication errors,
failed app reads, or stdout failures return 1. Partial read failures still emit
the collected report. No preflight result changes policy execution requirements
or replaces required test evidence. Planner ingestion is opt-in through --preflight, as described below; review
findings before using them to resolve planning prerequisites.

Shell redirection must target a separate file so it does not truncate a policy,
manifest, target map, or test report before the command starts.

## Planning with a saved preflight

```sh
gregale test policy plan --policy regression-policy.yaml --manifest gregale-test.yaml --preflight environment-preflight.json --targets existing-workloads.yaml --preflight-side current
```

The optional preflight input resolves platform_evidence_required prerequisites
for one side of the plan. The side defaults to current; baseline must be selected
explicitly with --preflight-side baseline and an appropriate baseline workload
mapping. One preflight/side is accepted per invocation. Review each side with
its own mapping when their environments differ.

The original target file is required. Its exact SHA-256 must match the report,
so even changing or reformatting the mapping requires a fresh preflight. Inputs
must be non-execution version 1 platform_preflight documents. Ambiguous
requirement/workload identities, verified-execution claims, invalid timestamps,
and mismatched mappings are rejected.

The default maximum age is 15 minutes. Override it with --preflight-max-age
within 1s..1h. The report must have a valid ordered read window, end no later
than planning time, and remain fresh. Every readable workload observation must
also be fresh and fall inside that window. Future-dated observations are rejected.
The JSON report size limit is 8 MiB. These checks inspect saved data locally;
the planner makes no platform calls.

A preflight requirement must match the current rule name and exact selectors.
The planner recomputes resource/runtime checks from allowlisted observed fields
against current expectations. Saved ready flags, derived check statuses, and
old expected values are not accepted as proof. Missing, unreadable, unmapped,
incomplete, or nonmatching observations leave the prerequisite blocked. Partial
preflight reports can resolve a requirement only when its necessary mapped
workload observations are complete and valid.

Only the requested API-observable platform fields are resolved. Declaration-only
fixture/database/port settings still need execution snapshots, and unavailable
precise binary versions remain blocked. Preflight cannot waive manifest changes,
historical configuration problems, existing noncompliant report replacement, or
other prerequisite types. A live correction does not remove bad execution
receipts already present in a report.

The plan includes a preflight summary with source-report and target-file hashes,
selected side, age bound, and per-context resolution status. Resolved entries list
the exact fields and oldest observation used. ready_preflight means only that
the selected platform prerequisite can guide suggestions. Baseline/current
executed coverage, sample counts, run IDs, and receipt evidence are unchanged.
The other side remains blocked unless it independently has suitable evidence.

These are planning observations, not a guarantee about future expiring
environments or an authenticated attestation of an edited report. Execution
must still meet every coverage and environment requirement. No original report
or manifest is modified. Invalid preflight inputs return 1; unresolved valid
observations remain informational blocked entries.


### Cost-aware coverage batches

Policy planning now emits canonical top-level `batches`. Execute each batch once;
entry `planned.batch_ids` references shared batches rather than duplicating
commands. Requirements for the same scenario, profile and revision side share
the maximum required repetitions. Baseline and current executions stay separate.
Each command still executes every matrix case and all campaign repetitions.

```sh
gregale test policy plan --policy campaign-policy.yaml --manifest gregale-test.yaml \
  --from current.json --baseline baseline.json \
  --plan-max-runs 100 --plan-max-deployments 200 --plan-max-workload-minutes 1000
```

The three nonnegative budgets apply to the whole plan; zero means unlimited.
Runs count native leaf executions, including unselected matrix cases.
Deployments include the primary workload and every declared service for each
leaf. Workload minutes conservatively assume every workload lives for the whole
scenario timeout (15 minutes by default), rounded up per repetition. These are
planning estimates, not measured usage or billing, and existing execution guards
remain in force.

The planner allocates groups by increasing estimated workload minutes, then
deployments, scenario, profile and side. This deterministic heuristic does not
guarantee globally optimal coverage. It shows at most twenty batches of up to
twenty repetitions per group. Per-entry planned runs are capped at the requested
deficit; remaining runs include any budget, output-cap, declaration or environment
gaps. Observed coverage counts remain unchanged. Planned coverage is conditional
on the executions actually producing qualifying assertion evidence.

Preflight and environment blockers are applied before allocation; budgeting
cannot waive them. Historical configurations without a reproducible manifest
remain gaps. Use fresh report paths, execute each side at its intended revision,
merge the resulting receipts into that side, and reevaluate the policy.


### Running and resuming a saved campaign

Generate a fresh cost-aware plan, then execute one revision side at a time.
The runner is an explicit execution command: it provisions workloads through
the existing native test runner, whose workload limits and cleanup rules apply.

```sh
gregale test policy plan --policy campaign-policy.yaml --manifest gregale-test.yaml --from current.json --baseline baseline.json --plan-max-runs 100 > campaign-plan.json

gregale test policy run --plan campaign-plan.json --policy campaign-policy.yaml --manifest gregale-test.yaml --from current.json --baseline baseline.json --side current --revision FULL_CURRENT_COMMIT --directory /tmp/gregale-current-campaign --concurrency 1
```

Use a separate directory and the intended clean checkout for the baseline side.
All source-backed workloads on a side must use the explicit full Git commit.
Tracked modifications and nonignored untracked files are rejected. Fixture
workloads retain their normal source hashes. Git checks are best effort against
checkout changes; do not edit source files or ignored deployment inputs during
execution. Actual source hashes and build inputs are also checked by report merge.

Plans bind the expanded manifest, policy and original report hashes. Older
unbound plans must be regenerated. The same original inputs are required when
resuming. Rerun the identical invocation to resume; checkpoints also bind the
plan, revision and side. Policy and manifest bindings and checkout state are
checked again before scheduling each batch. Saved argument arrays are never
executed; the runner rebuilds an allowlisted native command.

The output directory must be private (0700), initially empty, and outside source
trees (or ignored by Git). A lock prevents simultaneous runners in the same
directory. Each batch writes a private log and raw report, and the atomic
checkpoint records completion plus the exact report hash. Resume verifies saved
receipts before skipping completed work. Concurrency is 1 by default, with an
explicit range of 1..4 active batches. SIGINT/SIGTERM stops scheduling and signals
active native runners to finish cleanup; the parent drains them before returning.

Completion requires the exact planned profile, cases, configuration hashes,
execution count and source revision. Assertion failures with complete receipts
are completed work, not automatic retry requests. Skipped, partial or unverified
reports stop scheduling. Cleanup errors halt the campaign. Already active
batches may finish, so choose concurrency 1 when strict sequential stopping is
required. Budgets cover the saved batches and are checked again before execution;
the runner does not spend additional runs to fill residual gaps.

If a crash leaves a batch marked running, or interruption leaves a partial batch,
the runner cannot prove that repeating it is safe and refuses automatic replay.
Use the reconciliation command below to inspect its receipts and cleanup,
adopt verifiable evidence, and produce a continuation within the original budget. After an abnormal process exit a
stale `runner.lock` may remain: remove it only after verifying no runner is
active. Removing the lock does not bypass an uncertain checkpoint.

Completed receipts are merged with the original report for the selected side
into `combined-current.json` or `combined-baseline.json`, using the existing
duplicate/provenance/source checks without a mixed-revision override.
`policy-result.json` reevaluates both sides and shows actual remaining coverage
and environment violations. Planning projections never count as execution
evidence. The other side is unchanged; use the completed side report as input to
a new plan when preparing the next stage. Exit codes are 0 for accepted policy,
2 for policy violations, and 1 for input, execution, cleanup or evidence errors.
Raw logs, receipts and checkpoints remain available after a stop.


### Reconciling an interrupted campaign

Reconciliation inspects the existing checkpoint and batch reports, adopts verified
evidence into a new bundle, and plans only the remaining work for the same
revision side. It does not deploy, execute application commands, renew leases or
destroy resources.

```sh
gregale test policy reconcile --plan campaign-plan.json --policy campaign-policy.yaml --manifest gregale-test.yaml --from current.json --baseline baseline.json --directory /tmp/gregale-current-campaign --output-directory /tmp/gregale-current-recovery
```

Supply the exact manifest, policy and original reports used by the interrupted
plan. The source directory and checkpoint must match the plan and revision side.
The original campaign directory is locked during reconciliation. A stale
`runner.lock` must be resolved after confirming no runner is active; reconciliation
never removes another runner's lock.

The new output directory must not exist. It contains:

- `reconciliation.json`: per-batch adoption, cleanup and uncertainty diagnostics,
  original/recovered report hashes, and reserved/remaining costs.
- `recovered-batch-*.json`: verified raw evidence with `campaign_recovery`
  provenance linking the parent plan/checkpoint, original report hash, batch ID
  and original receipt path.
- `current.json` and/or `baseline.json`: safely merged evidence, including the
  original input reports where supplied.
- `policy-result.json`: actual coverage and environment policy reevaluation.
- `continuation-plan.json`: a bound, budgeted plan for the reconciled side only.

Complete reports retain their original matrix/campaign hierarchy and aggregate
verdict. A finalized partial report may contribute individually verified executed
leaves; skipped leaves never count as coverage. Partial extraction retains leaf
assertions and source metadata, but does not fabricate an aggregate verdict.
Premerged ancestor provenance that cannot safely be flattened is blocked.
Unknown raw fields and integer values are preserved. No adoption rewrites the
original checkpoint or report bytes.

Completion timestamps, exact context/configuration, stable run IDs and source
revision/build evidence are required. A saved done report must still match its
checkpoint hash. Missing, truncated, unfinished or conflicting evidence blocks
continuation. Recorded cleanup failures remain failures and require manual
reconciliation; their flags are never cleared merely because a session is absent.

By default, cleanup status describes recorded completion evidence. Add
`--check-cleanup` to also GET each known run's primary/service developer sessions
under the current credentials. Present sessions, read errors, invalid run IDs or
inspection limits block continuation. Reads do not renew leases and no response
secrets or raw platform errors are saved. A missing report cannot be resolved
through live inspection because its run identities are unknown. These reads
cannot prove that arbitrary fixture cleanup commands succeeded or that all
external effects were reversed.

Every checkpointed batch, or batch with an orphan report/log, reserves its full
original estimate even if only some receipts can be adopted. The continuation
uses only this side's original allocated cost minus those reservations.
It cannot borrow the other side's allocation, refund uncertain work, reopen an
exhausted zero budget, or grant extra runs. Actual remaining coverage may therefore
exceed the available budget. Fresh environment evidence may also still be needed.

Publication writes `campaign-reconciled.json` in the original directory last.
This seal freezes its scheduler and binds the exact continuation plan to the
parent checkpoint and reconciliation. The continuation cannot run without its
matching seal; retain the parent directory. Outputs left by an interrupted
publication are inspectable but not executable. Ready seals cannot be replaced.
A blocked reconciliation seals the original scheduler too, but can be refreshed
against the unchanged parent checkpoint into another new bundle after resolving
the diagnosed evidence. Existing blocked plans remain unexecutable.

Run the continuation using the reports present in the bundle, the same revision
and side, and a fresh checkpoint directory:

```sh
gregale test policy run --plan /tmp/gregale-current-recovery/continuation-plan.json --policy campaign-policy.yaml --manifest gregale-test.yaml --from /tmp/gregale-current-recovery/current.json --baseline /tmp/gregale-current-recovery/baseline.json --side current --revision FULL_CURRENT_COMMIT --directory /tmp/gregale-current-continuation
```

Omit a report flag when that report is absent from the bundle. Reconciliation
returns 0 when the bundle is ready, 2 when unresolved evidence blocks execution,
and 1 for invalid inputs or artifact errors. Ready means the recovery evidence
permits continuation; remaining coverage violations are reported independently.


### Unified campaign status

Use `test policy status` to inspect a saved plan, its checkpoint and optional
recovery bundle through one read-only report. It never authenticates, calls the
platform, runs application commands, takes an execution lock, merges files into
the campaign, or modifies checkpoints/seals.

```sh
gregale test policy status --plan campaign-plan.json --policy campaign-policy.yaml --manifest gregale-test.yaml --from current.json --baseline baseline.json --directory /tmp/gregale-current-campaign --json campaign-status.json --html campaign-status.html
```

JSON is also written to stdout. The optional exports must be outside campaign,
recovery-bundle and parent campaign directories and cannot alias inputs or each
other, including hard links. Use a separate file for shell redirection.

The same manifest, policy and exact original report inputs are required. Omit
report flags absent from the saved plan. With a checkpoint, status is scoped to
its revision side; an explicit `--side` must agree. Without `--directory`, it
shows the planned batches for both sides, or just the selected `--side`.
A directory without a checkpoint is also inspectable as an unstarted plan.

The report includes:

- Completed, running, pending, blocked, recovered and awaiting-reconciliation
  batch counts, context, repeat count, evidence quality and next action.
- Allocated, reserved and remaining conservative estimates for leaf runs,
  deployments and workload minutes. Any checkpointed batch or orphan report/log
  reserves its full estimate; missing evidence does not refund work.
- Actual coverage gaps, environment gaps, policy violations and next actions,
  reevaluated from verified raw receipt evidence. Saved policy-result files are
  never treated as the source of truth.
- Verified parent checkpoint/seal links, revision identity and per-parent budget
  reservations across recovery history, with cycle/depth and legacy diagnostics.

A checkpoint's running label and the presence of `runner.lock` do not prove that
a process is alive. Complete reports without a matching done checkpoint remain
awaiting reconciliation and do not count as adopted execution. Changed, missing,
duplicate or incompatible evidence cannot inflate observed coverage. Recorded
cleanup failures remain visible even when their completed execution produced
qualifying assertion evidence.

Checkpoint and seal stamps are compared before and after inspection. A change
sets `snapshot_stable: false` and asks for a fresh status snapshot. This is a
best-effort view, not a synchronization mechanism or an execution/cleanup gate.
Batch-report inspection is bounded to 128 MiB in addition to the separately
bounded original reports; reaching this limit is reported explicitly.

To include adopted partial work, point status at the original campaign and its
published recovery bundle:

```sh
gregale test policy status --plan campaign-plan.json --policy campaign-policy.yaml --manifest gregale-test.yaml --from current.json --baseline baseline.json --directory /tmp/gregale-current-campaign --recovery-bundle /tmp/gregale-current-recovery --html recovered-status.html
```

The bundle must match the original checkpoint and seal. Status verifies
reconciliation/continuation identities, per-batch adoption counts and receipt
hashes, reservation totals and merged-report hashes before using recovered
coverage. The report distinguishes a mere seal marker from verified adoption.

New execution checkpoints retain their plan's recovery link with lineage
version 1. Status traces up to 32 parents through those links. Older checkpoints
remain usable; when earlier links were not retained, the report stops and marks
the history incomplete rather than guessing that it reached the original root.

The HTML export is a standalone document with escaped report text, inline CSS,
and no JavaScript, external resources or automatic polling. The policy verdict
describes only the verified evidence subset and does not imply all planned
batches completed. Status returns 0 for an informational report, including
recorded failures/blockers, and 1 for invalid inputs, mismatched artifacts or
export errors.


### Portable campaign handoff bundles

A campaign bundle packages the saved plan, canonical policy, original receipt
inputs, checkpointed reports, known recovery parents and an optional published
reconciliation bundle. It preserves original artifact bytes and SHA-256 identities.
Parent references resolve through a portable node index; old absolute directory
strings remain immutable identity labels and are never used as filesystem paths
when an import resolver is active.

```sh
gregale test policy bundle export --plan campaign-plan.json --policy campaign-policy.yaml --manifest gregale-test.yaml --from current.json --baseline baseline.json --directory /tmp/gregale-current-campaign --output campaign-handoff.json
gregale test policy bundle inspect --from campaign-handoff.json
gregale test policy bundle import --from campaign-handoff.json --directory /tmp/gregale-inspection
```

Omit original report flags absent from the plan. A sealed source also requires
its matching `--recovery-bundle`. Export the current continuation work directory
after a continuation has claimed ownership; an older ancestor cannot represent
its current progress.

Export takes the source directory lock and writes `campaign-handoff.json` there
before publishing importable bundle bytes. This freezes its scheduler and
reconciliation. If publication fails, the source remains frozen; an identical
frozen export can be republished to a new destination after removing any partial
output file. Checkpoints and report bytes remain unchanged. Exporting an activated
recipient also revokes its old claim so it cannot start later continuations.

The format allows only enumerated metadata and evidence files, with 64 MiB total
decoded artifacts, at most 4096 files and 33 history nodes. Import verifies
artifact hashes, the bundle identity, paths, policy/plan/checkpoint bindings,
completed report availability and recovery graph/seal links before publication.
It rejects traversal, duplicate paths, missing evidence and changed artifacts.
Known legacy history boundaries remain marked incomplete.

Application manifests, source trees, credentials/configuration files and log
contents are excluded. Nonempty secret/credential configuration, configured
plaintext secret values, credential-bearing URLs, and raw request/response,
stdout/stderr, header or diagnostic captures cause export to fail. Generate
shareable evidence without these payload captures before handing it off.
The exporter preserves hash-bound evidence and does not silently rewrite it.
Recognized redaction placeholders are allowed. Continue to treat report labels
and application-supplied error text as data selected by the sender.

An orphan log transfers as an uncertainty marker without its contents.
Import creates a placeholder in the working directory so its reserved cost
cannot be mistaken for unstarted work. Missing/uncertain receipts still require
the existing reconciliation workflow; a bundle does not grant coverage or
refund potentially spent work.

Imports are inspection-only by default. The result prints the index, plan,
policy and working directory paths. To activate a recipient, all workers must
use the same private shared claim directory with atomic exclusive file creation:

```sh
gregale test policy bundle import --from campaign-handoff.json --directory /tmp/gregale-recipient --activate --claim-directory /shared/gregale-handoff-claims
```

One claim owns one recipient root for that bundle ID. Activation binds the import
to its location and host identity. The shared claim lock serializes execution,
reconciliation and subsequent export; the ownership ledger binds each plan to one
work directory. Duplicate activation, concurrent ownership, a different revision
side, or resetting a plan into another directory is rejected. Separate claim
directories do not coordinate workers. Retain claims after completion; removing
a claim or stale lock requires confirming its owner is no longer active.

`artifacts/` is immutable. `work/` contains the copied source checkpoint and
receipts plus ownership metadata. Provide the matching external source checkout,
manifest and authentication separately. Absolute application source paths remain
constraints of that manifest; relative source layouts are easier to reproduce.

```sh
gregale test policy status --handoff /tmp/gregale-recipient/bundle-index.json --plan /tmp/gregale-recipient/artifacts/plan.json --policy /tmp/gregale-recipient/artifacts/policy.yaml --manifest gregale-test.yaml --directory /tmp/gregale-recipient/work --html handoff-status.html
gregale test policy run --handoff /tmp/gregale-recipient/bundle-index.json --plan /tmp/gregale-recipient/artifacts/plan.json --policy /tmp/gregale-recipient/artifacts/policy.yaml --manifest gregale-test.yaml --directory /tmp/gregale-recipient/work --side current --revision FULL_CURRENT_COMMIT
```

Add original `--from` / `--baseline` paths under `artifacts/` when their hashes
are bound by the plan. For an already sealed source, use the plan and report
inputs under `artifacts/recovery/` with a fresh runtime directory. Pass
`--handoff` to status, run, reconcile and later bundle export so parent resolution
and ownership remain attached. Imported immutable directories cannot be used as
mutable runtime directories.

Normal native revision/configuration checks, checkpoint verification, cleanup
failures, reconciliation blockers and budget limits remain enforced. A claim
does not automatically execute work or resolve uncertainty. New native
continuations also bind their first working directory in the parent campaign,
which prevents exporting an obsolete ancestor while that continuation owns work.
