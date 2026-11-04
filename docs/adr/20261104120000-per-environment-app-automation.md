# ADR-20261104120000: Per-environment app automation

Date: 2026-11-04
Status: Accepted
Amends: ADR-590 (complete project-environment clones), ADR-20261103160000 (slice 3)

## Context

A project environment (production, a stage, a preview) gives each workload its
own deployments, variables, secrets, settings, route and edge policies,
databases and buckets. Several kinds of app automation do not have that
property. Their tables hold one row set per app, with no environment column:

| Table | Today |
|---|---|
| `crons` | The scheduler sweep, fire-now, the app task and the exclusive command-cron admission all pick the app's live deployment in the default scope, so every cron runs in production. |
| `triggers`, `event_subscriptions` | One set per app; deliveries reach the default scope. |
| `edge_rules`, `cors_presets` | One set per app. |
| `app_work_policies` | One set per app. |

This has two consequences:

- **No stage-local automation.** A stage cannot have crons, triggers or
  subscriptions of its own.
- **Clones are refused.** A full clone of a source holding any of these rows
  is refused (`clone_strategy_unavailable`), because there is nowhere in the
  target to put an isolated copy.

**Manifest crons can cross environments.** Crons declared in `gregale.yaml` are
staged by `gregale deploy` through `ApplyDeploymentCronStaging`. Neither the
request nor the CLI carries an environment, so a deploy to a stage can replace
the app's production HTTP crons with the stage manifest's.

## Decision

1. **App automation is per environment.** Each of the tables above gains an
   environment scope with the deployment-scope convention (`''` is the default,
   production scope).
   - **Existing rows:** keep `''`, so production behaviour does not change.
   - **New environments:** a new environment starts with none.
   - **Plan limits:** `cron_limit_per_app` and `cron_limit_per_account` count
     every environment's crons, so a stage cannot exceed a plan's limits.

2. **Crons first.**
   - **Storage:** `crons.environment` is added (expand migration, CHECK
     against the scope syntax).
   - **Firing:** the scheduler, fire-now, command-cron app tasks and exclusive
     command-cron admissions fire into the live deployment *of the cron's
     environment*, carrying it as `DeploymentScope`.
   - **Suspension:** "no live deployment" suspends only crons of the
     environment that lost its deployment.
   - **API and CLI:** cron endpoints take `environment`, and the CLI
     `--environment`. An omitted environment means production, as today.
   - **Manifest crons:** `gregale deploy --environment X` stages manifest
     crons into X only. The staging request names its environment, and the
     server replaces only that environment's HTTP crons.

3. **Then triggers and event subscriptions, edge rules and CORS presets, and
   work policies.** Each follows the same shape in its own PR: a column, owners
   reading their environment, API/CLI `environment`, and production as the
   default.

4. **Clones copy automation paused.** Once a table is per environment, its
   clone strategy copies the source environment's rows into the target. Crons,
   triggers, event subscriptions and outbound webhooks start paused or
   disabled in the target, and alert notifications do not fire. A clone never
   double-fires a schedule or pages anyone until the customer turns the copy
   on. Edge rules, CORS presets and work policies are copied active: they
   shape the stage's own traffic and have no outside effect.

## Consequences

- **Defaults:** production keeps its automation. Nothing moves without an
  explicit environment.
- **Manifest staging:** stages can deploy manifests with crons without
  touching production's.
- **Release compatibility (docs/release-compatibility.md):**
  - N-1 apid writes `''` (production) through the column default.
  - N-1 schedd keeps firing only default-scope crons, ignoring stage crons
    until it is upgraded. That is the old behaviour, and stage crons do not
    exist before N.
  - Each schema change is expand-only.
- **Admission until a table's PR lands:** clone admission keeps refusing that
  table, then accepts it through the strategy in decision 4.
