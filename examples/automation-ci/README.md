# Live automation evidence gate

This optional workflow evaluates two **already retained** live verification
suites. It reads the comparison API once; it never starts cases, checks
assertions, continues suites, resumes runs or changes the deployment.

Use a shared CLI suite (`live suite --share`) or a dashboard suite for each side.
Complete the candidate's live assertion checks explicitly before gating it.
The comparison API and the CLI version must include this feature. For an
unreleased checkout, build `go build -o gregale ./cmd/gregale` and use that binary
in your existing CI job. Do not assume an older published CLI supports `gate`.

To use the standalone GitHub Actions example:

1. Copy [github-actions.yml](github-actions.yml) to
   `.github/workflows/gregale-automation-gate.yml` in your repository.
2. Set `GREGALE_CLI_VERSION` to an exact release tag containing the gate command,
   and `GREGALE_API` to your API base URL, as repository variables.
3. Set `GREGALE_AUTOMATION_READ_TOKEN` as a repository secret. The key needs
   `apps:read` access to the app. It is supplied only to the evidence-read step.
4. Dispatch the workflow with the app/name, distinct baseline and candidate suite
   UUIDs, and the candidate's expected saved version, published revision and
   deployment UUID. These identities come from your deployment/verification job;
   the gate never chooses a newer target or baseline automatically.

The `strict_v1` policy requires every selected candidate case to pass. Regressions
and unchanged failures exit **1**. Passed recoveries and passed added cases are
allowed. Missing, unstarted, pending, unchecked, stale or incompatible evidence
and removed baseline cases block a pass with exit **7**. Wrong or outdated
candidate targets are also inconclusive (7), even if their old reports passed.
A known failure in the intended current candidate takes precedence over other
evidence gaps, and all blocking reasons remain in the report. A historical
baseline is allowed when its recorded results remain comparable and current for
their original runs.

Both 1 and 7 fail the GitHub step. There is no `continue-on-error`. The result
summary and JSON artifact are published even when the gate fails, preserving the
CLI exit status. JSON includes `schema_version`, `policy`, selected identities,
`expected_target`, `outcome`, `exit_code`, `reasons`, candidate pass/fail counts
and the safe comparison. Raw inputs, outputs, assertion values, tokens and API
error details are excluded.

Policy reason codes are `candidate_target_mismatch`, `candidate_outdated`,
`candidate_failed`, `regressions`, `candidate_incomplete`, `unknown_results`,
`incompatible_cases`, `removed_cases` and (with a coverage policy)
`required_cases_missing`. Version 2 also reports `candidate_evidence_stale`,
`candidate_evidence_future` and `candidate_evidence_age_unavailable`. Request/protocol errors use
`authentication_required`, `request_failed`, `request_timed_out`,
`invalid_response` or `interrupted`. The report retains every applicable policy
reason; use `outcome` and `exit_code` for the final decision.

Usage errors exit 1 before contacting the API. Request errors use normal CLI
codes: 2 authentication, 3 infrastructure/protocol/10-second read timeout,
4 missing or expired records, 5 conflict and 6 permission denied. Interruption
exits 130. After valid options have been parsed, errors also produce a safe
JSON result on stdout with `--json`, plus an RFC 7807 diagnostic on stderr.
Missing suite groups produce request exit 4; a retained suite with missing linked
run evidence is inconclusive (7).

The gate is an observation of the selected evidence, not a publication receipt
or an automatic deployment action. Without a version 2 policy it does not impose a maximum age or
prove that omitted, never-selected cases were tested. Both suites and their run
records remain subject to normal retention. Rerun the read-only gate after
explicitly completing checks; intentional coverage or assertion changes require
an explicitly selected compatible baseline and candidate.

For required coverage, keep a policy JSON file in version control and pass
`--policy path/to/policy.json` to the gate. For example:

```json
{"version":1,"required_cases":["paid-invoice","declined-payment"]}
```

The candidate must select every required case. Missing names return inconclusive
(7), including names omitted by both suites. Reports include the policy content,
its canonical SHA-256 hash and missing names. CI must make the policy file
available locally (for example by checking out the reviewed commit first).
Without `--policy`, the sample workflow only checks selected-suite coverage.

To require recent evidence, upgrade the same file to version 2:

```json
{"version":2,"required_cases":["paid-invoice","declined-payment"],"max_candidate_age_seconds":3600}
```

Age runs from the oldest candidate run creation to the CLI clock. It covers the entire
suite conservatively; fresh assertion checks or comparison reads cannot refresh
old evidence. Stale or future-dated evidence returns inconclusive (7), taking
precedence over old failures. Exactly the limit is allowed. Reports include
`evidence_age` with the evaluation time, oldest run creation time, age in seconds and
maximum age. The age limit participates in the policy hash. Version 1 hashes
and behavior are unchanged. Keep the CI clock synchronized. Baseline age is not
restricted. Missing timestamps (including an older server) return inconclusive
with `candidate_evidence_age_unavailable`. These checks remain read-only.

For a stable reviewed comparison point, explicitly promote a passing suite with
`gregale automation live baseline promote` and a version 2 policy. Use
`--expected-revision 0` for a new name and the current observed revision for later
promotions. Keep a distinct `--promotion-id UUID` per deliberate promotion and
preserve it for uncertain retries; replay returns its historical receipt without
resetting a newer head.

Then replace `--baseline UUID` in the gate invocation with
`--baseline-name production`. The gate reads the head once, inherits its reviewed
policy, and compares the exact resolved suite UUID. A supplied `--policy` must
have the same canonical hash or the result is inconclusive (7). Reports include
the full resolved receipt, its revision and promotion identity. These two reads
share a ten-second budget and never promote, execute or check runs. Names are
scoped to app and automation; they do not switch execution environments.

Inspect `automations live baseline show` or `history` for the attributable audit
trail. Promotion requires deploy-write scope; CI gate reads require only app-read
scope. Baseline groups are pinned within normal suite capacity, but linked run
retention and report invalidation still apply. The sample workflow continues to
use explicit UUIDs; adapt its selector inputs and command together when adopting
named baselines.
