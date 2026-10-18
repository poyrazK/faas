# Gregale CLI simplicity and developer experience roadmap

**Status:** proposed feature goals

**Date:** 2026-10-18

Make Gregale require fewer repeated choices, explain what each operation will
do, and give developers a clear next step when something fails. Prioritize
consistent project and app selection first, then build four connected workflows
on that foundation.

The current checkout already improves onboarding, concise help, and local error
handling. Gregale also has project links, development sessions, deployment
previews, recovery commands, and GitHub workflow generation. These five goals
extend those capabilities into a consistent daily experience.

This roadmap complements the [API hosting roadmap](api-hosting-roadmap.md) and
the [UX specification](faas_ux_spec.md). It defines proposed CLI work; it does
not mark the features below as implemented.

## Feature goals

| Priority | Feature goal | Developer outcome | First useful release |
| --- | --- | --- | --- |
| 1 | Remember the project and app | Run everyday commands without repeating targets | Correct source-directory targeting and remember the app selected during `start` |
| 2 | Make daily development one continuous workflow | Start developing with the choices already made during setup | Connect source selection and `dev setup` checks to the normal `dev` entry point |
| 3 | Make changes easy to preview | Understand the target, source, and effects before applying a change | Consistent summaries across existing deployment preview modes |
| 4 | Guide failure recovery | Understand the failure and take one concrete next step | Shared interruption and build-failure guidance in deployment and inspection output |
| 5 | Make setup reusable by teammates and CI | Reproduce the intended workflow from a fresh checkout | Review and save supported setup choices in the existing manifest |

## 1 Remember the project and app

**Current gap.** `start` knows the app it deployed but does not save an app
default for everyday commands. `logs` offers an interactive app picker, while
`metrics` requires an explicit or linked app. `deploy --path` can replace a
linked app with the source directory name while resolving the environment from
the calling directory.

**Build.** Use consistent target resolution across source commands and app
reads. Resolve source-command defaults from the selected source directory;
explicit options take precedence. Remember the app confirmed during `start`,
including standalone apps, and show where it was saved. Extend the existing
read-command picker to `metrics` and use consistent missing-target errors in
scripts. Keep `context` as the place to explain and change saved defaults.

**Done when.** After setup, `logs --follow`, `metrics`, `inspect`, and `deploy`
work without repeating the app. Tests cover repository roots, nested sources,
standalone apps, explicit overrides, and invalid links. Selecting another source
directory cannot accidentally combine its app with the caller's environment.
App-scoped reads remain app-scoped; a saved environment only affects commands
that support that scope.

**First slice.** Fix `deploy --path` resolution, then add `start` persistence
and picker consistency. Extend the local context format with compatibility for
existing links; preserve a conflicting saved link until explicitly changed.

## 2 Make daily development one continuous workflow

**Current gap.** Source detection, `dev setup`, manifest defaults, and the dev
loop exist, but their entry points do not consistently carry the developer's
selected source and setup through the whole journey.

**Build.** Reuse the selected workload and compatible setup choices when entering
`dev`. Integrate the existing setup checks into that entry point and present one
compact overview of the source, runtime, developer resources, and live URL.
Connect existing progress and diagnostics into a clear sequence from change
detection to readiness. On exit, explain the session's remaining lease and give
the exact command to stop it.

**Done when.** A supported single-app fixture can go from setup to `dev`, edit,
live response, exit, and restart without repeating a source selection or saved
setup options. The output distinguishes the developer session from the deployed
app, and its stop command targets that session. Context reuse preserves developer
isolation. Existing multi-workload and explicit-option behavior remains usable.

**First slice.** Share source resolution and setup checks between `dev setup`
and `dev`, then add a coherent start and exit summary. Measure edit-to-live time
against the existing API hosting roadmap targets; establish a baseline before
claiming an improvement.

## 3 Make changes easy to preview

**Current gap.** Deployment plans, remote diffs, scale plans, and secret-update
previews already exist, but users must learn their different presentations and
verification guarantees.

**Build.** Present a common summary of the effective connection, app, supported
environment scope, source, and proposed changes. Explain where defaults came
from, whether uncommitted files are included, and what remains unknown. Start
with deployment previews, then extend the presentation to existing scale and
secret-update previews.

**Done when.** Preview fixtures cover dirty checkouts, nested sources, existing
apps, and explicit overrides. A developer can identify the target and included
source from the summary. Human and structured output agree on those facts, and
preview commands perform no remote mutations. Unsupported combinations produce
an actionable explanation.

**First slice.** Align the summaries in `deploy --plan` and `deploy --dry-run`.
Keep their guarantees explicit: `--plan` is local inference for supported
sources; `--dry-run` compares supported changes with authenticated remote state.
Keep existing source defaults and saved scale-plan checks. Preview consistency
does not imply that every command supports applying a saved plan.

## 4 Guide failure recovery

**Current gap.** `doctor`, `inspect --errors`, deployment recovery data, and
resumable onboarding provide useful pieces, but recovery guidance varies by
entry point.

**Build.** Connect those pieces around a shared failure presentation: what
failed, the relevant evidence, and one recommended next action. Carry the exact
app, connection, source options, and accepted operation identifiers into recovery
commands. Begin with interrupted deployment waits and build failures, then cover
configuration, authentication, and readiness failures.

**Done when.** Every failure in the initial catalog has evidence and a runnable
next command in human output, plus structured recovery data for automation.
Interruption tests demonstrate that an accepted deployment resumes observation
without submitting another deployment. Unknown causes remain explicitly unknown;
the next action gathers evidence instead of inventing a fix.

**First slice.** Align deployment and `inspect --errors` guidance using the
existing recovery data and error contracts. Any future local-file repair shows
the proposed edit for review before applying it.

## 5 Make setup reusable by teammates and CI

**Current gap.** The manifest already supports hosting and development defaults,
and `deploy --github` generates a workflow. Carrying a successful interactive
setup into those shareable inputs still requires developers to reconcile the
choices themselves.

**Build.** Offer a reviewable way to save supported, non-secret setup choices in
`gregale.yaml`. Extend existing workflow generation to carry supported source
and deployment choices explicitly. Validate which choices are portable and name
anything requiring additional configuration. Use the same resolution and
preview rules locally and in automation.

**Done when.** A fresh checkout with its own credentials reproduces the intended
source, runtime, and supported development settings without the original user's
local context file. Generated CI configuration declares its deployment target
and source intentionally, handles missing inputs without prompting, and retains
backward-compatible structured output and exit behavior. Shareable files contain
no tokens, secret values, or machine-specific absolute paths.

**First slice.** Preview and save a bounded set of setup choices already supported
by the manifest, preserving unrelated configuration. Follow with generation and
validation of CI configuration for the same supported source shapes.

## Delivery order and measurement

Deliver goal 1 first because the other goals need reliable source and target
resolution. Goals 2 and 3 follow; the common recovery presentation can develop
alongside them. Complete the reusable setup and CI path once those choices are
stable.

Use a small acceptance matrix spanning standalone and linked apps, nested
sources, terminal and non-interactive runs, and clean and dirty checkouts.
Record repeated flags, prompts, successful workflow completion, and recovery
steps before and after each change. Use the existing API hosting scorecard for
latency and reliability targets. Each release should improve a complete user
task and retain coverage for advanced workflows.

## Implementation starting points

- Context: [target resolution](../cmd/gregale/project_context.go),
  [read-command picker](../cmd/gregale/app_picker.go),
  [onboarding](../cmd/gregale/cmd_start.go).
- Development: [setup checks](../cmd/gregale/commands_dev_setup.go),
  [development loop](../cmd/gregale/cmd_dev.go).
- Previews: [deployment behavior](cli/deploy.md),
  [CLI configuration and automation](cli-config.md).
- Recovery: [deployment recovery](../cmd/gregale/deploy_recovery.go),
  [app inspection](../cmd/gregale/commands_inspect.go).
- Team setup: [GitHub workflow generation](../cmd/gregale/cmd_deploy_github.go),
  [CLI setup guide](cli-setup.md).
