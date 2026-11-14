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

## Amendment 2026-11-11: manifest crons and environment work

- **Manifest staging state per environment.** Production keeps its staging
  revision on `apps` and its pending activation in
  `deployment_cron_activations`, which N-1 writes with `ON CONFLICT(app_id)`.
  Every other environment has its own revision and activation in
  `environment_deployment_cron_states` and
  `environment_deployment_cron_activations`, keyed by app and scope, so a
  stage deploy and a production deploy never supersede each other's intent.
  Only the deferred protocol (prepare, cancel, track_upload) takes an
  environment; the legacy immediate journal stays production's.
- **Activation lane.** An environment's intent activates only from a
  deployment of that environment, under the production rules within the lane
  (live, all of the lane's traffic, no competing deployment, rollout done).
- **Environment HTTP crons are environment work.** Their invocations carry the
  environment and are owned by the cron row, whose environment never changes;
  claims, version resolution and environment deletion check that ownership.
- **Decided next (user, 2026-11-11):** an environment's event subscriptions
  receive only that environment's events (its buckets, deployments and
  databases, and custom events its code publishes); environment broker
  triggers get a platform-derived consumer group or durable name per
  environment, so a stage never competes with production for messages.

## Amendment 2026-11-12: full clones copy crons paused

Decision 4 for crons, with the owner's choices of 2026-11-12:

- **Scope.** Only a full clone (`gregale env create STAGE --from ENV --full`)
  copies crons. A plain clone and an environment branch still create none.
- **What is copied.** Every cron of a captured workload in the source
  environment, manual and manifest alike, with its configuration: schedule,
  path or command, timezone, overlap, retry, schedule policy, failure rules
  and labels. The scheduler's own columns (last fire, schedule anchor,
  revision, suspension) are not part of the capture, so a source that fires
  during the clone has not changed. The clone refuses a source with crons on
  an app that has no workload in it (`source.crons`).
- **Paused.** Each copy is disabled. `crons.clone_paused` marks the copies the
  source ran (a source cron that is itself clone-paused counts as running). A
  manifest deploy into the stage replaces its HTTP crons as usual.
- **Resume.** Per cron with the existing enable, or all at once with
  `POST /v1/projects/{slug}/environments/{environment}/automation/resume`
  (`gregale env resume-automation STAGE`), which enables exactly the
  clone-paused crons. Any explicit change of `enabled`, by any writer, clears
  the mark (trigger `clear_cron_clone_pause`), so the bulk resume never undoes
  a customer's decision or turns on a cron the source had off.
- **Limits.** Copies count against the plan's cron limits like any other cron,
  disabled or not. Admission refuses a clone whose copy would pass a per-app
  or per-account limit (`plan_cron_quota`); materialization checks again under
  the cron quota lock and ends a clone that crossed it with
  `cron_limit_exceeded`.
- **Proof.** Publication checks that the stage holds exactly the captured
  crons, each still disabled.

## Amendment 2026-11-12: event subscriptions per environment

Decision 3 for event subscriptions and custom events. Platform events
(buckets, deployments, databases) and clone copies follow in the next PR.

- **Subscriptions.** `event_subscriptions` gains `deployment_scope` (`''` is
  production) and `environment_id`, which a trigger resolves from the app's
  project and keeps immutable. The identity is per environment
  (`app_id, deployment_scope, source, type, filter`), so a stage may declare
  the same trigger as production. Deleting an environment deletes its
  subscriptions.
- **Deploys.** A manifest deploy installs `triggers.event` into its own
  environment. Before this, a stage deploy installed them as production
  subscriptions. Only production records the declared set that activation
  reconciles. A stage trigger that names `work_policy` or `ordered` is refused
  (422) until work policies are per environment.
- **Events carry their environment.** The envelope's server-stamped
  `environmentid` extension names it; none is production.
  - `POST /v1/events:publish` and the batch route take `environmentid`, an
    environment of the account.
  - `POST /v1/apps/{slug}/events:publish` takes `environment`, a slug of the
    app's project.
  - An in-guest publish always gets the publishing deployment's environment
    from vmmd (the platform's runtime default). A guest that names an
    environment must name its own.
  - Naming production stamps none.
- **Fan-out.** `enqueue_event_fanout` validates the environment against the
  account, gives the event only to subscriptions of the same environment, and
  records each recipient's environment. Workflows are production's: only
  production events start them. Replay previews and backfills offer a
  subscription only its environment's events.
- **Delivery.** A stage recipient's invocation is stage work. It carries the
  environment and is pinned to the stage's live release through the same
  ingress resolution as the stage's routes.
- **Release compatibility.** N-1 apid and vmmd stamp no environment, so their
  events stay production's, as before. N-1 schedd delivers a stage recipient
  as production work. Stage subscriptions exist only once N apid installs
  them, and the deploy order (data, control plane, edge/node, runtime)
  upgrades schedd with apid.
