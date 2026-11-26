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

## Amendment 2026-11-14: platform events and clone copies of subscriptions

The follow-up the previous amendment named.

- **Storage events.** An object event from a bucket a stage owns (an
  environment branch's copy bucket, a clone's target bucket) carries that
  stage's environment, so only the stage's subscriptions receive it. When the
  owning stage no longer exists the event reaches no one rather than
  production. Production's buckets stamp none, as before.
- **Route discovery.** The gateway reports the deployment that served each
  discovered route (`ConsumerUsageEvent.discovered_deployment_id`); an N-1
  gateway omits it and apid falls back to the audit record's deployment.
  Routes first served by a stage go into `environment_api_routes`, keyed by
  environment, and are deleted with it. Their first-seen event carries the
  stage and has its own event ID, so it never collides with production's.
  Production's `app_api_routes`, its event and the dashboard's route list are
  unchanged.
- **Clones copy event subscriptions paused.** As with crons (amendment
  2026-11-12), a full clone copies every event subscription of a captured
  workload in the source environment (source, type, filter, retry policy,
  schema versions, acknowledgment lease) into the stage disabled.
  `event_subscriptions.clone_paused` marks the copies the source ran; any
  explicit change of `enabled` clears it (trigger
  `clear_event_subscription_clone_pause`), including a stage deploy that
  reinstalls its manifest's triggers. The clone refuses a source with
  subscriptions on an app that has no workload in it, and publication checks
  that the stage holds exactly the captured subscriptions, each disabled.
- **Resume and limits.** `automation/resume` (`gregale env resume-automation`)
  also enables the clone-paused subscriptions and reports them
  (`event_subscriptions_resumed`, `event_subscription_ids`). Only enabled
  subscriptions count against the plan's event subscription caps
  (ADR-20261031130000), so paused copies are free and the resume is where the
  caps apply: it resumes subscriptions first, counts after the write in one
  transaction, and refuses with `plan_event_subscription_quota` changing
  nothing (crons included) when the stage would pass a cap.
- **Not yet.** Bucket notification rules still block a full clone
  (`object_bucket_setting_unavailable`): their queue destinations resolve
  through environment-bound queue bindings, so a paused copy needs its own
  design. Deployment and database events stay production's.

## Amendment 2026-11-16: bucket notification copies

Status: accepted (owner's choices of 2026-11-16). Closes the "not yet" above
for bucket notification rules.

### Problem

A rule's destination is an ARN that names an app and, for a queue, a queue
name (`arn:gregale:sqs:REGION:ACCOUNT:APP/QUEUE`,
`arn:gregale:lambda:REGION:ACCOUNT:function:APP`). Neither names an
environment.

- **Queues.** Delivery resolves the ARN to the one enabled queue binding of
  that app and name, across every environment. Full clones copy queue
  bindings into the stage, so once a stage holds a queue of the same name the
  lookup finds two and resolves to none: production's own bucket
  notifications to that queue stop resolving. This is latent while full
  clones are dark and is fixed by the first decision below.
- **Functions.** A notification always invokes the app's production
  deployment, even when the event came from a stage bucket.

That is why `object_bucket_notifications` is `unavailable` in the clone
registry (`object_bucket_setting_unavailable`): a copied rule would deliver a
stage's object events to production.

### Decision

1. **Destinations resolve in the bucket's environment.** The bucket's
   environment is the one its events carry (amendment 2026-11-14): a branch's
   copy bucket or a clone's target bucket belongs to its stage, every other
   bucket to production. An ARN is resolved there:
   - a queue ARN to the enabled binding of that app and queue name **in that
     environment** (production: `deployment_scope = ''`), never another
     environment's;
   - a function ARN to that app's work in that environment: the recipient
     carries the environment and is delivered as stage work, pinned to the
     stage's live release, as event-subscription recipients are (amendment
     2026-11-12).
   The ARN format does not change, and a rule copied verbatim reaches the
   stage's own queue or deployment. Rule writes (`PutBucketNotification`,
   compute-trigger reconcile) validate destinations in the bucket's
   environment the same way. A stage bucket whose environment is gone
   resolves nothing.
2. **Full clones copy the configuration, paused as a whole.** The clone
   copies each captured bucket's `rules` and `managed_rules` (manifest compute
   triggers stay managed) into its target bucket with the source revision,
   and sets `object_bucket_notifications.clone_paused`. While it is set the
   rules are stored and readable but match no event, so nothing is delivered.
   It is cleared by `automation/resume` (`gregale env resume-automation`),
   which then reports `bucket_notifications_resumed` and the bucket IDs, and
   by any explicit write of the rules (PUT, delete, or a stage manifest deploy
   reconciling its compute triggers): the customer has decided. A source
   bucket that is itself clone-paused copies paused.
3. **Missing destinations refuse the clone.** Capture checks every rule's
   destination against the stage-to-be: a queue must be one of the captured
   workloads' queues and a function one of the captured workloads' apps.
   Anything else refuses admission with a named blocker
   (`object_notification_destination_unavailable`), as crons and subscriptions
   refuse apps outside the captured workloads. Destinations in another
   account or region are already invalid.
4. **Proof.** Publication checks that each target bucket holds exactly the
   captured rules and managed rule IDs and is still `clone_paused`.
5. **Registry.** `object_bucket_notifications` gains `clone_paused` and moves
   from `unavailable` to `capture` + `materialize`. CORS, lifecycle,
   encryption, Object Lock, upload routes and per-version protection keep
   blocking.

### Consequences

- Environment branches benefit without copying anything: a branch's copy
  bucket created with rules resolves them in the branch. (Branches do not copy
  notification rules today; that stays a separate choice.)
- Plan entitlement is unchanged: queue targets still need queue depth and
  function targets async invoke on the bucket's plan.
- No resume quota: notification rules have no plan cap of their own, and the
  queues and functions they reach are already counted.
- A production bucket no longer reaches a queue that exists only in a stage:
  before, the environment-blind lookup resolved it there. Stage queues are
  dark with full clones, so no production rule is expected to rely on it.
- Release compatibility. The environment-scoped lookup and the pause are read
  by every writer that captures notification work (apid and the object
  gateway). An N-1 writer resolves environment-blind and ignores
  `clone_paused`, so it would deliver a paused copy: full clones must stay off
  until every such writer runs N. The migration adds `clone_paused boolean
  NOT NULL DEFAULT false`, expand-only; the writers clear it on any explicit
  rules write, identical rules included.

### Slices

1. Environment-scoped destination resolution and validation (fixes the
   production ambiguity on its own; shippable first).
2. `clone_paused` column, capture, copy, proof, registry, admission blocker.
3. `automation/resume` and the CLI resume bucket notifications; docs and SDK
   models.

## Amendment 2026-11-21: what production projects need from a full clone

Status: accepted.

### Problem

A read-only run of the clone admission inventory over production (22
projects; 6 hold apps, 56 of 89 live apps belong to no project) found that no
project with apps could be cloned. The tables the sources hold rows in were,
by projects hit: `deployment_public_build_urls` 6; `edge_rules` and
`edge_rule_set_versions` 3; `app_service_wake_ahead` 3; `app_release_gates`,
`app_slos` and `synthetic_checks` 2; and once each `project_environment_secrets`,
`route_health_gates`, `alert_rules`, `api_consumers`, `consumer_keys`,
`app_openapi_docs` and `stored_workflow_revisions`.

### Decision

1. **App-wide settings stay with production.** `edge_rules`,
   `edge_rule_set_versions`, `app_service_wake_ahead`, `app_slos`,
   `synthetic_checks`, `app_release_gates` and `route_health_gates` have no
   environment column. A new registry strategy, `production_only`, declares
   that a clone neither copies nor refuses them: the stage starts without
   them. Stage hosts already take their edge rules from the environment edge
   policy, which a clone copies. The strategy is valid only for tables owned
   below the account (app, deployment, environment, project).
2. **A target deployment carries its source's build URLs.** The workload
   snapshot captures the source deployment's `deployment_public_build_urls`
   row; attaching the target deployment writes it. The table gains `capture`
   and `materialize`.
3. **Shared secrets are captured sealed.** The capture records the source
   environment's `project_environment_secrets` envelopes (ciphertext, key ID,
   value hash, version) and every project app's
   `app_environment_secret_refs` and `app_environment_secret_ref_suppressions`
   in the project-level part of the snapshot, beside the project
   configuration. The envelope is not bound to an environment, so the stage
   gets the same ciphertext under the same key ID, as a clone without a
   checkpoint already does; no value is opened. Materialization writes the
   secrets and the cloned apps' references and suppressions; publication
   proves the stage holds exactly them. The refusal
   `source.project_secrets` (`ErrProjectSecretCloneTransferUnsupported`) is
   removed.

### Consequences

- Five of the six production projects with apps pass admission; the sixth
  still needs `alert_rules`, `api_consumers`, `consumer_keys`,
  `app_openapi_docs` and `stored_workflow_revisions`.
- A secret rotated in production after the clone does not reach the stage:
  the stage holds its own copy from the clone's capture.
- Captures made before this release carry no shared secrets and still
  publish; N-1 capture refuses a source with shared secrets, so admission
  stays off until every apid runs N.

## Amendment 2026-11-22: per-environment API consumers, alert rules and stored workflows

Status: accepted (consumers designed here; alert rules and stored workflows
get their detail in their own slices).

### Problem

The last production project that a full clone refuses holds app-wide
`api_consumers`, `consumer_keys`, `alert_rules` and stored workflows. None has
an environment column, so a clone can neither copy them into the stage nor
leave them behind without changing what the stage serves. `app_openapi_docs`
is also app-wide; stage deployments already record their own OpenAPI snapshot.

### Decision

1. **`app_openapi_docs` is derived**: the stage's deployments rebuild it.
2. **The owner chose per-environment configuration for the other three, in
   minimal form first.** Each gets an environment scope (NULL is production,
   as for queue bindings and crons). A full clone copies the source
   environment's rows into the stage; side effects are copied paused. A stage
   row works only where its source is environment-aware today; elsewhere the
   stage refuses it with a clear message, and later slices widen that.
3. **API consumers.**
   - `api_consumers` and `consumer_keys` gain `environment_id` (NULL is
     production). A key belongs to its consumer's environment; a trigger
     fills and checks it.
   - The unique identities become per-environment: key prefix and key name
     per app and environment, consumer external reference per app and
     environment. A stage copy of a key keeps its prefix and keyed hash, so
     the same plaintext authenticates in both environments.
   - **Stage hosts:** a stage that has consumers of its own accepts only
     them; a stage with none keeps accepting production's (owner's choice),
     so existing stages keep working. Production hosts accept only production
     consumers.
   - Consumer plans stay app-wide and are shared; plan assignments follow
     their consumer.
   - Stage consumer usage is not billed or metered against consumer plans.
   - A full clone copies the source environment's active consumers, their
     active keys and plan assignments into the stage, and publication proves
     the copy.
   - API and CLI take `environment` on consumer and key routes; omitted is
     production.
4. **Alert rules** (later slice): copied paused; a stage rule may be resumed
   only on a metric whose source carries the environment (requests, errors,
   latency and invocations first).
5. **Stored workflows** (later slice): copied disabled; a stage workflow may
   be enabled only for HTTP and cron triggers first.

### Release

The per-environment identity indexes are added beside the app-wide ones, and
a later contract release drops the app-wide ones. They cannot go in the same
release: platform-tenant consumer creation writes `ON CONFLICT (app_id,
external_ref)`, which needs the app-wide index, and N-1 runs that path. Until
the contract release a stage consumer's external reference and key names must
differ from production's, so a full clone, which copies them, ships with the
contract release; full clone admission stays off until every apid and gateway
runs it.

An N-1 gateway reads keys by app and prefix with no environment, so it would
accept a stage's key on a production host. Creating a stage's consumers waits
for the apid runtime key `stage_api_consumers_enabled` (default off), which
operators turn on once every gatewayd-internal runs this release. A stage's
consumers are not billed: the gateway records their usage without a consumer
and skips their plan admission, and billing runs read production's consumers
only.

Release order: (1) the per-environment columns, indexes and stage
consumers; (2) platform-tenant consumer creation writes `ON CONFLICT (app_id,
COALESCE(environment_id, zero uuid), external_ref)`, the per-environment
index; (3) once every apid runs (2), the contract migration drops the
app-wide indexes and full clones copy consumers, keys and plan assignments.
