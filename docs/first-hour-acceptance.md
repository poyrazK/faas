# First-hour acceptance

The first-hour milestone is a verified private response, a verified source edit,
and recovery from an interruption and a failed startup. This pack extends the
existing customer journeys and coding-agent evaluation harness. It does not
create a separate test framework or change the deployment product.

## Scripted deployed CLI lane

Run against an explicitly selected API with a CLI that includes `release verify`:

```sh
python3 scripts/first-hour/acceptance.py \
  --api https://api.gregale.dev --cli /absolute/path/to/gregale \
  --cli-login --plan scale --out /tmp/gregale-first-hour
```

`--cli-login` explicitly uses the existing signed-in account. Alternatively pass
`--token-file /private/path/to/evaluation-token` for a dedicated evaluation
account. The pack checks account headroom, holds one uniquely named new app,
submits four releases, and deletes only that app. It disables the journey
sweeper. Runs consume normal build and compute allowance. Never run this
particular scenario alongside another run using the same output directory.

| Step | Required evidence |
|---|---|
| First private response | The accepted app/release/instance and HTTP 200, with the expected source version; public access remains unverified. |
| Anonymous request | The same private app refuses a credential-free GET. |
| Edit and interrupt | A second accepted release updates the same app; interrupt its read-only waiter after observed progress. |
| Resume | Follow the accepted UUID, verify the new source version, and find exactly two releases. No replay or new submission is used to resume. |
| Failed startup | Inject a known process failure, require a failed release, and find its cause in that release's logs. |
| Repair | Restore the code, deploy a fourth release, and verify the repaired response. |
| Cleanup | Delete this run's app and verify it no longer resolves, including after a failed step. |

The test is `TestFirstHourCLI`; it is skipped unless
`GREGALE_E2E_FIRST_HOUR=1`. The wrapper selects only this test, one app/deploy
slot, the full tier and no sweep. It does not silently retry a submission.
Source submission timeouts are failures with cleanup, not permission to deploy
again. A waiter that finishes before it can be interrupted does not qualify the
interruption scenario.

The disposable app uses immediate rollouts. This measures a scripted Node HTTP
fixture after authentication, not a user's installation/login time, interactive
`gregale start`, Python/Go starter coverage, or safe-rollout latency. A Scale
account run does not establish Free-plan compatibility. Those distinctions stay
visible in the scorecard.

## Published CLI qualification

Qualify the exact archive users can download, without rebuilding or replacing
your installed CLI:

```sh
python3 scripts/first-hour/qualify_release.py \
  --release v0.1.18-rc.349 --api https://api.gregale.dev \
  --cli-login --plan scale --out /tmp/gregale-first-hour-released
```

The output directory must be new. Alternatively use `--token-file` for an
explicit acceptance credential. The tool checks that the release is published,
downloads the host platform's archive and `CLI-SHA256SUMS`, verifies its exact
checksum record, and unpacks only the single regular `gregale` file into a
temporary directory. Before deploying, the binary must report the selected
version and the tag's source commit, and support `release verify`. The binary
is removed when qualification ends; the existing CLI/login is left in place.

The same seven-step journey then runs using that binary. Passing qualification
requires a zero exit, all journey milestones including checked cleanup, and
evidence bound to the verified binary checksum and selected API. Missing
configuration, unsupported commands, fixture results, skipped steps or failed
cleanup cannot qualify the released artifact. There is no automatic deployment
retry. `qualification.json`/`.md` retain release/archive/binary provenance and
the result; `baseline.json`/`.md` retain the sanitized journey scorecard.

This measures a published archive and an authenticated scripted journey on one
platform/plan. It does not qualify the published installer, its default channel
selection, browser login, interactive Start, npm, other platforms/plans, or the
rest of the release pipeline. Those lanes remain separately unmeasured.

### Release workflow

`.github/workflows/first-hour-release.yml` starts after the `release` workflow
finishes, or can be dispatched on `main` with an exact published tag. It uses
the acceptance harness from `main` and downloads the released binary separately.
The upstream tag is taken from the release workflow's `head_branch` and checked
as a release tag before network requests. An upstream pipeline failure still
starts qualification: a published CLI can be tested independently of other
publication jobs, while missing/unpublished assets cannot qualify. This does
not change release publication, channel advancement or production deploy gates.

Configure the GitHub environment `first-hour-acceptance` with:

- Variable `GREGALE_ACCEPTANCE_API_URL`: the explicitly selected API origin.
- Secret `GREGALE_ACCEPTANCE_TOKEN`: a separate key with `apps:read,deploy:write`
  scopes, for an explicitly selected account with one free app slot and
  allowance for four releases. Do not copy a developer's login into CI.
- Optional variable `GREGALE_ACCEPTANCE_PLAN`: the account's actual plan; the
  report otherwise records `unknown`.

Restrict the environment's deployment branch policy to `main`. These key scopes
are account-wide; the journey separately requires its generated app name to be
absent before creating it and deletes only that owned app. See
[authentication](auth.md) for key creation, revocation and rotation.

Missing API/token configuration produces a blocked, nonzero qualification with
artifact evidence retained. The workflow publishes its outcome in the job
summary and uploads only the four sanitized JSON/Markdown reports, even after
failure. Raw logs and Go output stay under `private/`, outside that upload list;
the temporary token file is removed on step exit. Runs serialize without
cancelling an active journey; GitHub may replace an older pending run, so a
cancelled/absent run is not evidence that a release qualified. Dispatch an exact
tag when a queued observation was missed.

The separate workflow uses `workflow_run` because releases created using
`GITHUB_TOKEN` do not start another `release: published` workflow. See
[GitHub's workflow trigger rules](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow).

## Coding-agent lane

The existing harness now has `first-hour-private` and `first-hour-edit` tasks.
Together with `fix-failed-deploy`, these cover private deployment, a source edit
of an existing private app and failure recovery:

```sh
python3 scripts/agent-eval/agent_eval.py run --agents codex --toolsets none \
  --tasks first-hour-private,first-hour-edit,fix-failed-deploy \
  --gregale /absolute/path/to/gregale --cli-login
```

This separate command starts a real coding agent and creates real apps. The
standard task budgets and attribution/cleanup rules in
[the agent-eval guide](../scripts/agent-eval/README.md) apply. The first-hour
wrapper itself never starts an agent. It can import an existing result file
with `--agents /path/to/agent-eval-results.jsonl`.

Agent records must match the journey's API origin and CLI binary checksum.
Missing or mismatched provenance is excluded and reported, rather than mixed
into the candidate baseline. The agent harness records both fields on new runs.

Private tasks are graded by the owner-authorized CLI check of the app's exact
latest live deployment, its identity, actual HTTP status and expected content.
The grader does not use the app's public URL or treat an API envelope as the
application response. Its check establishes the final outcome; it does not
claim when the agent first verified that outcome. The feature score separately
records whether the agent used `release verify`.

## Fresh install, browser login and Free Node Start

Run this separate, browser-assisted lane locally on Unix:

```sh
python3 scripts/first-hour/onboarding.py --api https://api.gregale.dev \
  --expected-email user@example.com --out /tmp/gregale-free-onboarding
```

On clean Linux with a browser on another machine, run the private starter lane:

```sh
python3 scripts/first-hour/onboarding.py --api https://api.gregale.dev \
  --template hello-node-private --device-login --file-credentials \
  --expected-email user@example.com --out /tmp/gregale-free-private-onboarding
```

In a container that doesn't export `SHELL`, set it to the path of the
container's shell before the run. The public installer uses it when printing
PATH setup instructions for the isolated destination.
Use `--file-credentials` when the Linux environment has no usable keyring.
It stores the isolated login under the run's private home and revokes it during
cleanup. Open the URL and enter the code from `private/browser-handoff.json`
in the browser to approve device login.

Select an existing Free account with no deployed apps. Its normal browser
sign-in must be available to the observer. A Scale account or an occupied Free
account blocks before Start creates anything. The runner does not change plans,
reuse an existing app, copy a credential, or accept a token argument.

Complete email verification before starting the measured run. Open the emailed
link in the browser signed in to that Free account, click **Verify email**, and
require the final **Email verified** page. Opening the link alone only renders
the confirmation form; another browser's login may belong to a different
account. CLI authorization requires the verified address even when the
dashboard already allows the new account to sign in.

For an installation-only preflight, pass `--install-only` and omit the email.
That mode performs no browser login or deployment, keeps the overall outcome
`blocked`, and exits nonzero because full onboarding has not been qualified.

The output directory must be new. The runner installs from
`https://get.gregale.dev` using the installer's default version selection and
an isolated destination. It checks the installed binary against that published
archive's checksum/version/source evidence. Its child processes use a new home,
configuration/cache/state directories and a random named connection. Named
connections have separate OS-keychain accounts and do not fall back to the
default connection's credentials. The runner confirms it is signed out before
starting normal browser login; it preserves the machine's original CLI,
selected connection and login.

The terminal driver is shared with the existing Start acceptance harness. It
uses a real PTY for login and Start and scripts these product prompts:

| Phase | Required evidence |
|---|---|
| Fresh installation | Published installer/default version resolution; installed bytes match the host archive and stamped source. |
| Browser login | Initially signed out; normal browser authorization completes; expected account identity matches. |
| Free preflight | API reports `free`; no app occupies the test slot; generated name is absent. |
| Start Node | Create the built-in Node HTTP starter, review and approve normal defaults, then require Start's verified session and an exact app/release/replica HTTP 200 with the initial greeting. |
| Edit | Enter a new greeting in Start's walkthrough, review its local change, and verify the new greeting from a distinct release of the same app. Exactly one app and two releases may exist. |
| App cleanup | Delete only the registered test name, require absence, and verify the initially empty Free account is empty again. |
| Login cleanup | Log out of the isolated connection, verify it is signed out, and remove that connection. Browser-managed CLI-key revocation must succeed. |

For `hello-node-private`, the installed CLI must list the starter before any
login. Start must show **Internal only** on both reviews. The runner edits
`app/server.js` between releases and verifies the same app and project, owner
HTTP 200 and anonymous HTTP 404 on each release. It waits for 100% traffic on
the edited release in a separate phase and verifies that both the temporary app
and its project are gone during cleanup.

Browser sign-in/authorization remains a real product interaction. Do not use
`login --token`, a copied keychain entry, or a stub callback to qualify it.
`private/browser-handoff.json` holds the current approval URL for an observer;
it is removed after successful login. Account signup, password entry and browser
session preparation are separate from the measured CLI flow. An already
authenticated browser may authorize the new CLI connection.

Only `onboarding.json` and `onboarding.md` are shareable reports. They contain
phase results, timings, installer/CLI provenance, observed plan and hashed
response/source bindings. Supplied email, login URL/code, credentials, local
paths, response bodies and failure transcripts stay out of them. Keep the
entire `private/` directory local; it contains the isolated installation,
recovery data and bounded failure logs.

The one-hour clock starts before installation and ends at the verified edit;
cleanup is separately timed and required. Timings include qualification checks
and scripted terminal input. This is controlled acceptance, not a measurement
of an unassisted new user's onboarding time. Missing/skipped/fixture evidence,
incorrect identity or plan, extra deployments, or failed cleanup cannot pass.

This lane measures the Free Node starter on its recorded platform with Start's
normal rollout defaults. Python/Go, shell startup-file integration, npm, real
coding agents, and interruption/restart of the interactive session remain
separate follow-up lanes. The older scripted CLI scorecard does not inherit
this lane's evidence or label its own installation/login fields as measured.

## Reports and local checks

The wrapper writes the existing `go-test.json`, `report.md` and `summary.json`,
plus `metadata.json`, `baseline.json` and `baseline.md`. Baseline output contains
only named step results, allowed timings, CLI checksum/version, harness revision,
account plan and agent aggregates. It omits credentials, account email, private
response bodies, agent answers and raw logs. Keep raw journey logs local.

Regenerate a scorecard without deploying:

```sh
python3 scripts/first-hour/baseline.py --journey /tmp/gregale-first-hour/go-test.json \
  --metadata /tmp/gregale-first-hour/metadata.json \
  --out /tmp/gregale-first-hour
```

Missing/skipped evidence, missing metrics and failed cleanup cannot produce a
passing scripted scorecard. Fixture evidence is labeled `fixture_only`. The
report exits nonzero unless the deployed scripted lane passes, while the other
lanes retain their own measured/unmeasured status. One run is an observation;
report sample counts before drawing reliability or latency conclusions.

`first_hour_complete_s` ends at the verified repaired response: the customer
milestone. Checked cleanup has its own required step and duration. No cleanup,
installation/login or agent-thinking time is silently counted as zero.

CI runs the report and grader tests through the existing `agent-eval-test`
target. `make first-hour-check` also compiles and runs the isolated Go evidence
contracts. Set `GREGALE_JOURNEY_TEST_CLI` to exercise its real-CLI interruption
fixture; without a binary only that fixture is skipped.

```sh
python3 -I scripts/first-hour/baseline_test.py
python3 -I scripts/first-hour/qualify_release_test.py
python3 -I scripts/first-hour/onboarding_test.py
python3 -I scripts/agent-eval/agent_eval_test.py
go test ./tests/journeys -run '^TestHarnessFirstHour' -count=1
```

The repository's normal Go development flags and shared cache apply. These local
checks require no deployment or paid agent session.
