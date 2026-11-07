# ADR-20261107190000: Automations become stored workflows (hard cut)

Date: 2026-11-07
Status: Accepted (owner decisions 2026-11-07); implementation in progress

## Context

Gregale has two nouns for one engine. An *automation* is a set of steps with
a manual, scheduled or event start, authored as JSON through the dashboard,
API or SDK; it already runs on the durable workflow executor
(`docs/automation-authoring.md`). A *workflow* is the same executor driven by
code or by `gregale.yaml` `workflows:`. Every feature (runs, recovery, failure
policies, tests, live verification) is explained and built for both names.

The owner decided on 2026-11-07:

1. One noun, two authoring modes: workflows written in code, and **stored
   workflows** (the former automations), which Gregale stores and versions.
2. Rename everything: CLI, API, dashboard, docs, SDKs, Go packages and
   storage.
3. API location under `/v1/apps/{slug}/workflows/`, beside the existing
   `/workflows/schedules`, `/workflows/runs` and similar paths.
4. Kind name "stored". "definition" was chosen first and replaced: the API
   already uses "workflow definition" for a code workflow defined on the live
   deployment (`workflow_definition_not_found`).
5. **Hard cut**: no aliases for old CLI nouns, API paths, SDK methods or
   table names.

## Scope

In scope: the product feature only.

| Surface | From | To |
|---|---|---|
| Tables (11) | `workflow_automation_definitions`, `_revisions`, `_failure_guards`, `_failure_history`, `_failure_policies`, `_live_baseline_promotions`, `_live_baseline_retirements`, `_live_suites`, `_live_verifications`, `_publish_policies`, `_publish_receipts` | `stored_workflows`, `stored_workflow_revisions`, `stored_workflow_failure_guards`, … (same suffixes under `stored_workflow_`) |
| API | `/v1/apps/{slug}/automations[...]` (16 path groups) | `/v1/apps/{slug}/workflows/stored[...]`; collection actions `:simulate`, `:validate`, `:publish-policy` keep their verbs |
| Event kinds | `automation.paused`, `automation.recovery_*` | `stored_workflow.paused`, `stored_workflow.recovery_*` |
| Alert rule kinds | `automation_unpublished`, `automation_paused`, `automation_failures`, `automation_backlog` | `stored_workflow_unpublished`, … |
| Error codes | `automation_invalid`, `automation_version_conflict`, `automation_ownership_conflict` | `stored_workflow_invalid`, … |
| CLI | `gregale automation …` | `gregale workflow stored …` |
| Go packages | `pkg/automationgate`, `automationchecks`, `automationtemplates`, `automationlive` | `pkg/storedworkflowgate`, `storedworkflowchecks`, `storedworkflowtemplates`, `storedworkflowlive` |
| SDKs | Node/Python/Go automation types and methods | regenerated from OpenAPI under stored-workflow names |
| Dashboard | app → Workflows → "Build and manage automations" | one Workflows section listing code and stored workflows |
| Docs | `automation-authoring.md` | `stored-workflows.md`, linked from the workflows docs |

Out of scope, a different meaning of the word: `ci_automation_*` tables and
code (GitHub pull-request automation), `hosting_automation_credentials`, and
the `webhook_automation_*` codes. They keep their names.

## Decision

### Storage

One migration renames the 11 tables (`ALTER TABLE … RENAME TO …`; indexes,
constraints and foreign keys follow the table and keep working under their
old names). In the same transaction it rewrites stored event kinds in
event rows and in webhook and event subscription filters, rewrites stored
alert-rule kinds, and replaces the CHECK constraints that list those values,
so no stored subscription or alert rule keeps a kind that no longer exists.

### Contracts

Old API paths answer 404 like any unknown path. Old error codes, event kinds
and alert kinds stop being emitted. The OpenAPI document and the generated
SDKs are regenerated and released as a breaking SDK version. The CLI's
`automation` noun is removed from the command tree.

## Consequences

- **Breaking for customers.** Scripts calling `/automations`, code matching
  `automation.*` event kinds, and SDK users of the old methods break at the
  release. Release notes must list every renamed path, kind, code and
  command.
- **Rollout window.** The migration runs before the new daemons start. While
  an old apid, schedd or alert evaluator is still running, its queries name
  tables that no longer exist, and stored-workflow endpoints and evaluations
  fail until it restarts (seconds per daemon in a rolling restart). The
  owner accepted this with the hard cut. If it is not acceptable at release
  time, views with the old table names for one release close the window
  without exposing old names to users.
- Historical rows such as audit entries keep their old text; only values
  that drive behavior (events, subscriptions, alert rules) are rewritten.

## Plan

One branch, landed as one PR, in reviewable commits:

1. Migration and the state layer (queries, row types, store methods).
2. API: handlers, paths, DTOs, error codes, OpenAPI; SDK regeneration.
3. Event and alert kinds in producers, the evaluator and docs.
4. CLI noun, help, generated reference and command-tree baselines.
5. Go package renames, dashboard section, docs.
