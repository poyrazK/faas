# gregale CLI reference

Generated from the CLI's command manifest by `gregale man --markdown`. Do not edit by hand.

| Command | What it does |
|---|---|
| [`account`](#account) | Manage the local account (account export\|delete\|restore\|status\|dpa\|slo) |
| [`add`](#add) | Provision and bind managed resources to an app |
| [`bindings`](#bindings) | List PostgreSQL, object-storage, and queue bindings for an app |
| [`capabilities`](#capabilities) | Show feature maturity and plan availability |
| [`alerts`](#alerts) | Per-app alert rules (alerts list\|add\|info\|update\|rm\|rotate-secret\|preset --app &lt;slug&gt;) |
| [`audit-events`](#audit-events) | Audit-log query (audit-events list\|get &lt;id&gt;) |
| [`events`](#events) | Publish events and inspect subscriptions and deliveries |
| [`send`](#send) | Reliably send work to another Gregale application |
| [`deliver`](#deliver) | Reliably deliver an event to a registered webhook |
| [`apps`](#apps) | List your apps |
| [`app`](#app) | Get/update one app (gregale app &lt;slug&gt; [scale\|rename &lt;new&gt;\|restart\|--profile NAME\|--ram N\|…]) |
| [`billing`](#billing) | Manage billing (portal, invoices, subscription, card on file) |
| [`canary`](#canary) | Project a canary preset against recent app traffic (canary simulate &lt;slug&gt;) |
| [`build`](#build) | Inspect builds (build status\|list\|provenance\|sbom) |
| [`connect`](#connect) | Connect a third-party service (github \| repo OWNER/NAME) |
| [`github`](#github) | Manage an app&#39;s GitHub installation and repository binding |
| [`cors`](#cors) | Configure CORS for an app (allow\|ls\|rm\|show) |
| [`crons`](#crons) | Manage scheduled requests |
| [`triggers`](#triggers) | Manage unified event triggers (broker mappings + cron-linked rows) |
| [`workers`](#workers) | Inspect and manage background worker pools |
| [`jobs`](#jobs) | Manage jobs (run-to-completion workloads) |
| [`workflows`](#workflows) | Manage durable execution workflows |
| [`dashboard`](#dashboard) | Open the account dashboard in your browser |
| [`doctor`](#doctor) | Preflight local source or OCI image metadata; runtime checks are skipped |
| [`delayed-task`](#delayed-task) | Schedule and inspect deferred invocations |
| [`deployments`](#deployments) | List deployments (--app SLUG or linked context \| --limit N \| --before C \| --all \| --wide) |
| [`deployment`](#deployment) | Get, summarize, or wait for one deployment (&lt;id&gt; \| summary &lt;id&gt; \| wait &lt;id&gt; \| set-min-instances &lt;id&gt;) |
| [`deploys`](#deploys) | Deployment drill-downs (deploys show\|status\|cancel\|reorder\|clear\|clear-obsolete\|retry) |
| [`deploy`](#deploy) | Deploy an app or project (--path DIR \| --image REF \| --tarball PATH \| --repo OWNER/NAME --ref REF \| --github \| --template NAME) |
| [`domains`](#domains) | Manage custom domains |
| [`dev`](#dev) | Sync the dirty working tree to a stable remote developer environment (name defaults to linked context) |
| [`preview`](#preview) | Manage preview environments (Mega-C PR-1 / issue #961 leaf 3) |
| [`edge-rules`](#edge-rules) | Per-app edge rules (edge-rules list\|create\|get\|update\|rm --app &lt;slug&gt;) |
| [`openapi`](#openapi) | Manage app OpenAPI docs + pre-publish schema-drift checks |
| [`env`](#env) | Pull/push .env &lt;-&gt; sealed secrets (--app &lt;slug&gt; or linked context) |
| [`init`](#init) | Scaffold a reference project from a built-in template (--template NAME --path DIR [--deploy]) |
| [`inspect`](#inspect) | Explain an app from its runtime, deployment, API, data, scaling, and release signals (slug defaults to linked context) |
| [`invoke`](#invoke) | Functional smoke test (invoke [--async] &lt;slug&gt; [--payload J\|@file\|-]; slug defaults to linked context) |
| [`run`](#run) | Run untrusted code in an isolated disposable microVM |
| [`runs`](#runs) | Inspect or cancel isolated disposable runs |
| [`invocations`](#invocations) | Per-account invocation ledger (invocations list\|get &lt;id&gt;) |
| [`debug`](#debug) | Production debugger (ADR-127) |
| [`trace`](#trace) | Look up a W3C trace through the account trace index |
| [`invitations`](#invitations) | Standalone invitation actions (invitations peek &lt;token&gt;\|accept &lt;token&gt;) |
| [`invoices`](#invoices) | List issued invoices |
| [`keys`](#keys) | Manage API keys (keys list\|add\|rm\|rotate\|grace-window) |
| [`login`](#login) | Authenticate this machine (--token for CI) |
| [`link`](#link) | Link this checkout to a Gregale project |
| [`logout`](#logout) | Revoke the managed CLI session and remove the stored token |
| [`unlink`](#unlink) | Remove the linked project from this checkout |
| [`context`](#context) | Show the linked project and default app context |
| [`signup`](#signup) | Create a new account (signup [--email-only EMAIL \| --password-stdin]) |
| [`logs`](#logs) | Query runtime logs and HTTP request events (slug defaults to linked context) |
| [`metrics`](#metrics) | Per-app or account-wide metrics (slug defaults to linked context) |
| [`analytics`](#analytics) | Historical request analytics (analytics &lt;slug&gt; [--since 24h] [--by route\|country\|referrer_host\|ua_family\|status]; slug defaults to linked context) |
| [`mfa`](#mfa) | Manage account MFA (mfa enroll\|confirm\|verify\|recover\|disable) |
| [`open`](#open) | Open the app&#39;s URL (slug defaults to linked context) |
| [`orgs`](#orgs) | Manage orgs, members, and workspace activity |
| [`overage-cap`](#overage-cap) | Set / clear the account&#39;s overage cap (--clear \| &lt;cents&gt;) |
| [`park`](#park) | Park an app cold (kill all live instances) |
| [`plan`](#plan) | Change plan (free\|hobby\|pro\|scale); paid upgrades open the provider checkout |
| [`ps`](#ps) | Show live instances + state for an app (slug defaults to linked context) |
| [`queue`](#queue) | Inspect queues and manage first-class queue bindings |
| [`dlq`](#dlq) | Inspect, replay, or purge unified dead-letter events |
| [`registry`](#registry) | Per-app private container registry credentials (registry list\|set\|rm --app &lt;slug&gt;) |
| [`realtime`](#realtime) | Manage realtime endpoints, policies, connections, channels, and auth |
| [`rollback`](#rollback) | Re-promote the previous deployment |
| [`projects`](#projects) | Inspect and recover repository projects |
| [`scan`](#scan) | Decomposition dry-run (--tarball \| --path \| --repo OWNER/NAME) |
| [`secrets`](#secrets) | Manage env secrets (secrets list\|set\|unset\|list-all\|rotate) |
| [`slo`](#slo) | Per-app SLO panel (gregale slo &lt;slug&gt; [--window 24h]; slug defaults to linked context) |
| [`status`](#status) | Personal SLO numbers (availability, wake p95, build success) |
| [`tail`](#tail) | Live tail of the unified event stream (app defaults to linked context) |
| [`trusted-publishers`](#trusted-publishers) | Per-app cosign trusted-publisher list (admin; trusted-publishers add\|remove\|list) |
| [`usage`](#usage) | Show this month&#39;s usage (gregale usage [--month YYYY-MM]\|daily [--day YYYY-MM-DD]\|storage [--day YYYY-MM-DD]\|summary) |
| [`version`](#version) | Print the CLI version |
| [`config`](#config) | Manage non-secret local CLI settings (config get\|set\|list) |
| [`wake-timeline`](#wake-timeline) | Walk the per-wake event stream (wake-timeline &lt;slug&gt; &lt;wake-id&gt; [--since RFC3339] [--limit N] [--all]; slug defaults to linked context) |
| [`throttle-suggestions`](#throttle-suggestions) | Per-route throttle recommendations + dry-run preview (gregale throttle-suggestions &lt;slug&gt; [--range 5m] [--dry-run --candidate-rps N --candidate-burst N]) |
| [`wake`](#wake) | Wake a parked app (pulls out of snapshot) |
| [`traffic`](#traffic) | Manage deployment traffic split (available on every plan) |
| [`mirror`](#mirror) | Manage traffic mirroring and sanitized replay (Pro/Scale only) |
| [`cache`](#cache) | Declare or purge response caching (cache GET /path/:id for 30s) |
| [`upload-cache`](#upload-cache) | Inspect or clean resumable source-upload recovery state |
| [`webhooks`](#webhooks) | Manage outbound webhooks (webhooks list\|add\|info\|update\|rm\|deliveries\|retry\|rotate-secret) |
| [`whoami`](#whoami) | Show the authenticated account |
| [`completion`](#completion) | Print a shell completion script (bash\|zsh\|fish\|powershell) |
| [`man`](#man) | Print the gregale(1) man page (or gregale-&lt;command&gt;(1) with one arg) |

## account

Manage the local account (account export|delete|restore|status|dpa|slo)

`gregale account [<subcommand>]`

### account export

Export account data (GDPR)

### account delete

Schedule account deletion

### account restore

Cancel a pending deletion

### account status

Show account status

### account dpa

Show DPA metadata

### account slo

Account-wide SLO panel


## add

Provision and bind managed resources to an app

`gregale add [<subcommand>]`

### add postgres

Provision or attach PostgreSQL and inject DATABASE_URL

| Flag | Meaning | |
|---|---|---|
| `--app <APP>` | app slug | required |
| `--env <SCOPE>` | environment scope (defaults to linked project environment) |  |
| `--scope <SCOPE>` | environment scope (alias for --env) |  |
| `--database <REF>` | existing database ID or name |  |
| `--region <REGION>` | provider-neutral region when creating |  |
| `--postgres-major <N>` | PostgreSQL major version |  |
| `--class <CLASS>` | service class | one of `development` · `burstable` · `production` |
| `--availability <MODE>` | availability mode | one of `single_zone` · `high_availability` |
| `--scale-to-zero` | suspend compute when idle |  |
| `--environment-key <KEY>` | connection environment variable |  |
| `--access <MODE>` | credential access | one of `read_write` · `read_only` |
| `--wait-timeout <DURATION>` | readiness timeout |  |

### add bucket

Provision or attach object storage and inject sealed S3 settings

| Flag | Meaning | |
|---|---|---|
| `--app <APP>` | app slug | required |
| `--env <SCOPE>` | environment scope (defaults to linked project environment) |  |
| `--scope <SCOPE>` | environment scope (alias for --env) |  |
| `--region <REGION>` | object-storage region |  |
| `--public` | serve objects publicly from the app host |  |
| `--serve-at <PATH>` | public mount path |  |
| `--permission <MODE>` | compute binding permission | one of `read` · `write` · `read_write` |
| `--label <LABEL>` | bucket-scoped compute credential label |  |
| `--prefix <PREFIX>` | injected storage secret prefix |  |
| `--wait-timeout <DURATION>` | readiness timeout |  |


## bindings

List PostgreSQL, object-storage, and queue bindings for an app

`gregale bindings <app>`


## capabilities

Show feature maturity and plan availability

`gregale capabilities`


## alerts

Per-app alert rules (alerts list|add|info|update|rm|rotate-secret|preset --app &lt;slug&gt;)

`gregale alerts [<subcommand>] [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug |  |

### alerts list

List alert rules

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### alerts add

Add an alert rule

| Flag | Meaning | |
|---|---|---|
| `--action <ACTION>` | alert action | one of `webhook` · `rollback` · `demote` · `promote` |
| `--webhook-secret-stdin` | read the webhook secret from stdin |  |

### alerts info

Show one alert rule

### alerts update

Update one alert rule

| Flag | Meaning | |
|---|---|---|
| `--action <ACTION>` | alert action | one of `webhook` · `rollback` · `demote` · `promote` |
| `--webhook-secret-stdin` | read the replacement webhook secret from stdin |  |

### alerts rm

Delete one alert rule

### alerts rotate-secret

Rotate the alert&#39;s webhook secret

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--from-stdin` | read the replacement secret from stdin |  |

### alerts preset

Alert preset catalog (preset list|enable --app &lt;slug&gt; [--action &lt;ACTION&gt;])


## audit-events

Audit-log query (audit-events list|get &lt;id&gt;)

`gregale audit-events [<subcommand>] [<id>]`

### audit-events list

List audit events

### audit-events get

Show one audit event


## events

Publish events and inspect subscriptions and deliveries

`gregale events [<subcommand>]`

### events publish

Publish one event (SOURCE TYPE can be positional; ID is generated by default)

| Flag | Meaning | |
|---|---|---|
| `--id <ID>` | stable event id (generated when omitted) |  |
| `--source <SOURCE>` | event source (or first positional argument) |  |
| `--type <TYPE>` | event type (or second positional argument) |  |
| `--data <J|@file|->` | JSON event data (inline \| @file \| -) | required |
| `--time <RFC3339>` | event time (RFC3339; defaults to server time) |  |

### events subscriptions

List subscriptions reconciled from the app manifest

### events deliveries

Inspect event delivery lifecycle

| Flag | Meaning | |
|---|---|---|
| `--event-id <ID>` | filter by published event id |  |
| `--state <STATE>` | filter by delivery state |  |
| `--before <ID>` | pagination cursor |  |
| `--limit <N>` | max deliveries (1..200) |  |


## send

Reliably send work to another Gregale application

`gregale send <target-app> --type <TYPE> --data <J|@file|-> [--id <ID>] [--source <SOURCE>] [--time <RFC3339>] [--queue-name <QUEUE>] [--idempotency-key <KEY>]`

| Flag | Meaning | |
|---|---|---|
| `--type <TYPE>` | event type | required |
| `--data <J|@file|->` | JSON event data (inline \| @file \| -) | required |
| `--id <ID>` | stable event id |  |
| `--source <SOURCE>` | event source |  |
| `--time <RFC3339>` | event time |  |
| `--queue-name <QUEUE>` | target logical queue name |  |
| `--idempotency-key <KEY>` | stable key for retrying an uncertain send |  |


## deliver

Reliably deliver an event to a registered webhook

`gregale deliver <source-app> <webhook-id|url> --type <TYPE> --data <J|@file|-> [--idempotency-key <KEY>]`

| Flag | Meaning | |
|---|---|---|
| `--type <TYPE>` | event type | required |
| `--data <J|@file|->` | JSON event data (inline \| @file \| -) | required |
| `--idempotency-key <KEY>` | stable key for retrying an uncertain delivery |  |


## apps

List your apps

`gregale apps [<subcommand>] [--quiet]`

| Flag | Meaning | |
|---|---|---|
| `--quiet` | delete one app without prompting (short form: -q) |  |

### apps ls

Alias for the default list action

### apps restore

Restore an app during its deletion grace window

### apps routes

List admitted per-route labels for one app (ADR-093)

### apps tcp

Manage raw TCP listeners

### apps streaming-cap

Per-app streaming classification probe (ADR-102 D6)

### apps -q

Delete one app (positional: &lt;slug&gt;)

### apps --quiet

Delete one app (positional: &lt;slug&gt;)


## app

Get/update one app (gregale app &lt;slug&gt; [scale|rename &lt;new&gt;|restart|--profile NAME|--ram N|…])

`gregale app <slug> [<subcommand>] [--profile <micro|small|medium|large|xlarge>] [--ram <MB>] [--max-concurrency <N>] [--concurrency-overflow <value>] [--max-queue-depth <N>] [--max-queue-wait <DURATION>] [--max-queue-wait-ms <N>] [--wake-max-queue-depth <N>] [--wake-max-queue-wait-seconds <N>] [--request-timeout <SEC>] [--require-signed <value>] [--security-policy <value>] [--only-declared-routes] [--no-only-declared-routes]`

| Flag | Meaning | |
|---|---|---|
| `--profile <micro|small|medium|large|xlarge>` | set a named RAM/CPU profile |  |
| `--ram <MB>` | set RAM in MB |  |
| `--max-concurrency <N>` | set max_concurrency |  |
| `--concurrency-overflow <value>` | set saturated concurrency behavior | one of `queue` · `drop` |
| `--max-queue-depth <N>` | set maximum warm-saturation waiters |  |
| `--max-queue-wait <DURATION>` | set maximum warm-saturation wait as a duration |  |
| `--max-queue-wait-ms <N>` | set maximum queued concurrency wait |  |
| `--wake-max-queue-depth <N>` | set per-app cold-wake waiter cap |  |
| `--wake-max-queue-wait-seconds <N>` | set per-app cold-wake wait budget |  |
| `--request-timeout <SEC>` | set per-app request timeout in seconds |  |
| `--require-signed <value>` | toggle require_signed | one of `true` · `false` |
| `--security-policy <value>` | deploy posture policy | one of `off` · `warn` · `enforce` |
| `--only-declared-routes` | reject undeclared paths before waking the app (OpenAPI or explicit route list) |  |
| `--no-only-declared-routes` | disable the declared-route pre-wake gate |  |

### app scale

Set max_concurrency / resource profile / RAM / CPU

### app rename

Rename an app

### app restart

Park and wake from a fresh snapshot

### app security

Show posture or configure deploy enforcement

### app egress-allowlist

Inspect or update the outbound CIDR allowlist

### app network

Inspect networking or manage private-network attachments

### app routes

List admitted per-route labels for one app (ADR-093)

### app tcp

Manage raw TCP listeners


## billing

Manage billing (portal, invoices, subscription, card on file)

`gregale billing [<subcommand>]`

### billing portal

Open the active billing provider&#39;s portal

### billing retry

Retry failed payment when supported; Polar uses the portal

### billing cancel

Cancel the subscription at period end

### billing payment-method

Show the card on file

### billing status

Show subscription status


## canary

Project a canary preset against recent app traffic (canary simulate &lt;slug&gt;)

`gregale canary [<subcommand>] <slug>`

### canary simulate

Estimate per-stage canary success from the last hour

| Flag | Meaning | |
|---|---|---|
| `--canary-preset <PRESET>` | canary ladder preset | one of `slow` · `balanced` · `aggressive` · `1-10-50-100` |


## build

Inspect builds (build status|list|provenance|sbom)

`gregale build [<subcommand>]`

### build status

Show the current status of one build

### build list

List builds and discover build IDs

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | filter to one app |  |
| `--status <STATUS>` | filter by lifecycle status | one of `queued` · `running` · `succeeded` · `failed` · `cancelled` |
| `--limit <N>` | page size (1..200) |  |
| `--before <CURSOR>` | pagination cursor |  |
| `--all` | walk every page |  |

### build provenance

Show the build provenance attestation

### build sbom

Show the build SBOM


## connect

Connect a third-party service (github | repo OWNER/NAME)

`gregale connect [<subcommand>]`

### connect github

Connect a GitHub account for repo deploys

### connect repo

Open the dashboard wizard to bind &lt;owner&gt;/&lt;name&gt; to a Gregale app


## github

Manage an app&#39;s GitHub installation and repository binding

`gregale github [<subcommand>] <slug>`

### github status

Show the GitHub connection health for &lt;slug&gt;

### github sync

Reconcile repository access with GitHub

### github repos

List repositories visible to the connected GitHub installation for &lt;slug&gt;

### github bind

Bind &lt;slug&gt; to a visible GitHub repository

| Flag | Meaning | |
|---|---|---|
| `--installation-id <ID>` | GitHub App installation id (auto-resolved when omitted) |  |
| `--repo <OWNER/NAME>` | GitHub repository OWNER/NAME | required |
| `--branch <BRANCH>` | production branch |  |
| `--deploy-branches <MAPPINGS>` | branch=scope mappings |  |

### github setup

Bind GitHub, configure previews, and write an Actions workflow

| Flag | Meaning | |
|---|---|---|
| `--repo <OWNER/NAME>` | GitHub repository OWNER/NAME (required for a dry run) |  |
| `--production-branch <BRANCH>` | production branch (default: current binding or main) |  |
| `--deploy-branches <MAPPINGS>` | comma-separated branch=scope mappings |  |
| `--workflow <PATH>` | workflow path relative to repository root |  |
| `--preview` | enable pull-request previews |  |
| `--no-preview` | disable pull-request previews |  |
| `--preview-ttl-hours <HOURS>` | preview lease in hours (1-720) |  |
| `--preview-service-policy <POLICY>` | preview-to-production service calls: deny\|allow_marked | one of `deny` · `allow_marked` |
| `--root-dir <DIR>` | repository-relative source root for the root workload |  |
| `--ignore <PATHS>` | comma-separated ignored change paths |  |
| `--rollout <MODE>` | production rollout mode: standard\|safe (safe requires Pro/Scale) | one of `standard` · `safe` |
| `--dry-run` | show the workflow without writing or changing remote state |  |
| `--force` | overwrite an existing workflow file |  |

### github disconnect

Remove the app&#39;s GitHub repository binding

| Flag | Meaning | |
|---|---|---|
| `--yes` | confirm removing the repository binding |  |


## cors

Configure CORS for an app (allow|ls|rm|show)

`gregale cors [<subcommand>]`

### cors allow

Attach a CORS rule to &lt;slug&gt;

### cors ls

List CORS rules bound to &lt;slug&gt; (defaults to linked context)

### cors rm

Delete a CORS rule by id

### cors show

Show per-app default CORS + active rules (defaults to linked context)


## crons

Manage scheduled requests

`gregale crons [<subcommand>]`

### crons list

List cron rules

### crons add

Add a cron rule

### crons info

Show one cron rule

### crons update

Update one cron rule

### crons rm

Delete one cron rule

### crons runs

Show execution history


## triggers

Manage unified event triggers (broker mappings + cron-linked rows)

`gregale triggers [<subcommand>]`

### triggers list

List triggers

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | filter to an app slug |  |
| `--kind <value>` | filter by trigger kind | one of `cron` · `kafka` · `nats` · `redis_streams` · `sqs_compat` · `queue` |

### triggers get

Show one trigger

### triggers create

Create a broker trigger

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (required) | required |
| `--kind <kind>` | trigger kind (required) | required; one of `kafka` · `nats` · `redis_streams` · `sqs_compat` · `queue` |
| `--slug <slug>` | trigger slug (required for non-cron kinds) |  |
| `--config <JSON>` | JSON config (inline \| @file \| -) |  |
| `--enabled` | enable the trigger |  |
| `--disabled` | disable the trigger |  |
| `--batch-size <N>` | maximum records per dispatch batch |  |
| `--batch-window-ms <N>` | maximum batch dwell time in milliseconds |  |
| `--max-attempts <N>` | maximum delivery attempts |  |
| `--payload-max-bytes <N>` | maximum broker payload size |  |
| `--broker-poison-strategy <commit|seek-to-offset>` | kafka poison strategy | one of `commit` · `seek-to-offset` |

### triggers update

Update one trigger

| Flag | Meaning | |
|---|---|---|
| `--enabled` | enable the trigger |  |
| `--disabled` | disable the trigger |  |
| `--config <JSON>` | replace JSON config (inline \| @file \| -) |  |
| `--schedule <EXPR>` | replace cron expression |  |
| `--path <PATH>` | replace cron request path |  |
| `--batch-size <N>` | maximum records per dispatch batch |  |
| `--batch-window-ms <N>` | maximum batch dwell time in milliseconds |  |
| `--max-attempts <N>` | maximum delivery attempts |  |
| `--payload-max-bytes <N>` | maximum broker payload size |  |
| `--broker-poison-strategy <commit|seek-to-offset>` | kafka poison strategy | one of `commit` · `seek-to-offset` |

### triggers delete

Delete one trigger

| Flag | Meaning | |
|---|---|---|
| `--quiet` | skip the typed confirmation (for scripts) |  |

### triggers pause

Disable one trigger

### triggers resume

Enable one trigger

### triggers records

List recent trigger records

| Flag | Meaning | |
|---|---|---|
| `--state <STATE>` | filter by record state | one of `pending` · `claimed` · `succeeded` · `retry` · `dead_letter` |

### triggers retry

Re-drive one trigger record

### triggers drop

Drop one trigger record

### triggers dlq

List dead-letter records

| Flag | Meaning | |
|---|---|---|
| `--reason <REASON>` | filter by dead-letter reason |  |

### triggers metrics

Show per-state trigger metrics


## workers

Inspect and manage background worker pools

`gregale workers [<subcommand>] [<slug>]`

### workers list

List background worker pools

### workers status

Show real-time status and autoscaling for a worker pool

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (optional; defaults to linked context) |  |

### workers logs

Tail logs for a background worker pool

| Flag | Meaning | |
|---|---|---|
| `--follow` | follow new log lines |  |
| `--grep <SUBSTR>` | filter log lines by substring |  |
| `--since <RFC3339>` | filter log lines after timestamp |  |
| `--level <LEVEL>` | filter log lines by level (info\|warn\|error) |  |

### workers scale

Adjust scaling bounds and graceful drain for a worker pool

| Flag | Meaning | |
|---|---|---|
| `--min <N>` | min worker replicas (0 = scale-to-zero) |  |
| `--max <N>` | max worker replicas |  |
| `--target <N>` | target messages per worker |  |
| `--metric <METRIC>` | autoscaling metric (queue_lag \| queue_depth) |  |
| `--drain-timeout <DURATION>` | shutdown grace duration (e.g. 90s, 2m) |  |
| `--stop-signal <SIG>` | stop signal (e.g. SIGTERM, SIGINT, SIGQUIT) |  |


## jobs

Manage jobs (run-to-completion workloads)

`gregale jobs [<subcommand>]`

### jobs list

List jobs in this account

### jobs add

Create a new job

### jobs info

Show one job

### jobs update

Update one job

### jobs rm

Soft-delete one job

### jobs run

Dispatch a new run (fan-out N tasks)

### jobs runs

List runs for one job

### jobs cancel

Cancel a run

### jobs tasks

List tasks for one run

### jobs retry

Retry one failed task

### jobs logs

Tail logs for one task

| Flag | Meaning | |
|---|---|---|
| `--max-bytes <N>` | maximum log payload size (1..1048576) |  |

### jobs registry

Manage private registry credentials for one job


## workflows

Manage durable execution workflows

`gregale workflows [<subcommand>]`

### workflows list

List workflow runs for an app

### workflows run

Trigger a new workflow run

### workflows status

Show details of a workflow run

### workflows steps

List steps for a workflow run

### workflows cancel

Cancel an active workflow run

### workflows events

Send external event to a workflow run


## dashboard

Open the account dashboard in your browser

`gregale dashboard`


## doctor

Preflight local source or OCI image metadata; runtime checks are skipped

`gregale doctor [--image <REF>] [--registry-user <USER>] [--registry-password-stdin] [--strict] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--image <REF>` | inspect the Linux/amd64 image without downloading layers |  |
| `--registry-user <USER>` | registry username; requires --registry-password-stdin |  |
| `--registry-password-stdin` | read registry password/token from stdin; requires --image and --registry-user |  |
| `--strict` | exit 1 on warn (default: exit 0 on warn) |  |
| `--json` | machine output (default: human prose) |  |


## delayed-task

Schedule and inspect deferred invocations

`gregale delayed-task [<subcommand>]`

### delayed-task add

Schedule a deferred invocation

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--scheduled-at <RFC3339>` | absolute dispatch time; exclusive with --delay |  |
| `--delay <DURATION>` | relative delay such as 30m; exclusive with --scheduled-at |  |
| `--payload <JSON|@FILE|->` | JSON request payload |  |
| `--method <METHOD>` | HTTP method (default POST) |  |
| `--path <PATH>` | app path (default /) |  |
| `--header <NAME:VALUE>` | request header (repeatable) |  |
| `--max-attempts <N>` | maximum delivery attempts |  |
| `--retry-base-seconds <N>` | base retry delay in seconds |  |
| `--retry-max-seconds <N>` | maximum retry delay in seconds |  |
| `--retry-jitter-seconds <N>` | retry jitter fraction (0..1) |  |
| `--retention <DURATION>` | terminal result retention |  |
| `--on-success-webhook <ID>` | success webhook subscription |  |
| `--on-failure-webhook <ID>` | failure webhook subscription |  |
| `--idempotency-key <KEY>` | stable create retry key |  |

### delayed-task list

List delayed tasks for an app

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--limit <N>` | page size (1-200) |  |
| `--before <ID>` | pagination cursor |  |

### delayed-task get

Show one delayed task

### delayed-task info

Alias for get

### delayed-task cancel

Cancel a delayed task


## deployments

List deployments (--app SLUG or linked context | --limit N | --before C | --all | --wide)

`gregale deployments [--app <slug>] [--limit <N>] [--before <cursor>] [--all] [--wide]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (app-scoped deployment history) |  |
| `--limit <N>` | page size (1-200) |  |
| `--before <cursor>` | pagination cursor (RFC3339Nano) |  |
| `--all` | walk every page |  |
| `--wide` | include annotation columns (by / pr / tag / reason) |  |


## deployment

Get, summarize, or wait for one deployment (&lt;id&gt; | summary &lt;id&gt; | wait &lt;id&gt; | set-min-instances &lt;id&gt;)

`gregale deployment [<subcommand>] <id|vN> [--app <SLUG>] [--show-scan] [--min <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--show-scan` | include the per-deploy grype scan payload |  |
| `--min <N>` | min_instances floor (&gt;= 0) |  |

### deployment summary

Show the release diff and rollback target

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |

### deployment wait

Wait until a deployment is live (or safe rollout completes)

| Flag | Meaning | |
|---|---|---|
| `--rollout` | wait for safe rollout to reach 100% traffic |  |
| `--progress` | print rollout transitions while waiting (human output only) |  |
| `--timeout <SECONDS>` | maximum seconds to wait |  |

### deployment set-min-instances

Set the per-deployment cold-wake floor


## deploys

Deployment drill-downs (deploys show|status|cancel|reorder|clear|clear-obsolete|retry)

`gregale deploys [<subcommand>] <id|vN> [--app <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |

### deploys show

Print the closed 6-stage post-stream summary

### deploys status

Print stages, terminal status, and failure guidance

### deploys cancel

Cancel one pending deployment

### deploys reorder

Change one pending deployment&#39;s queue priority

### deploys clear

Hide one deployment from the list

### deploys clear-obsolete

Hide obsolete deployments older than a cutoff

### deploys retry

Retry a failed deployment from a specific stage (--from=&lt;stage&gt;)


## deploy

Deploy an app or project (--path DIR | --image REF | --tarball PATH | --repo OWNER/NAME --ref REF | --github | --template NAME)

`gregale deploy [--image <REF>] [--tarball <PATH>] [--path <DIR>] [--worktree] [--repo <OWNER/NAME>] [--repository <OWNER/NAME>] [--install-id <N>] [--production-branch <BRANCH>] [--ref <REF>] [--github] [--template <NAME>] [--dockerfile] [--runtime <RUNTIME>] [--handler <HANDLER>] [--name <SLUG>] [--profile <PROFILE>] [--vcpu <N>] [--function] [--app] [--yes] [--only <SLUGS>] [--project] [--environment <SLUG>] [--reason <text>] [--tag <TAG>] [--deployed-by <NAME>] [--pr-number <N>] [--exclude <SLUGS>] [--show-affected] [--persist-exclude] [--project-slug <SLUG>] [--canary-preset <PRESET>] [--canary-stages <STAGES>] [--safe] [--require-authn] [--no-require-authn] [--app-protocol <PROTOCOL>] [--traffic-percent <PERCENT>] [--no-traffic] [--no-triggers] [--wait] [--no-wait] [--create-only] [--timeout <SECONDS>] [--idempotency-key <KEY>] [--secrets-file <PATH>] [--secret-scan <on|off>] [--diff] [--dry-run] [--plan] [--strict] [--lenient] [--server-diff] [--doctor-strict] [--no-doctor]`

| Flag | Meaning | |
|---|---|---|
| `--image <REF>` | deploy from a container image reference |  |
| `--tarball <PATH>` | deploy from a source tarball |  |
| `--path <DIR>` | deploy a selected local source directory (relative to the current directory) |  |
| `--worktree` | deploy the selected source directory from the working tree, including local changes |  |
| `--repo <OWNER/NAME>` | deploy from a GitHub repo |  |
| `--repository <OWNER/NAME>` | GitHub owner/name to bind to a project |  |
| `--install-id <N>` | GitHub installation id for a project binding |  |
| `--production-branch <BRANCH>` | production branch for a project binding |  |
| `--ref <REF>` | git ref for --repo (branch, tag, or 40-char SHA) |  |
| `--github` | emit a GitHub Actions workflow snippet for the Gregale deploy action |  |
| `--template <NAME>` | scaffold from a built-in template | one of `hello-node` · `hello-python` · `hello-go` · `cron-example` · `function-node` · `function-python` · `function-go` · `function-node24` · `function-python313` · `event-worker` · `queue-worker` · `s3-uploader` · `slack-bot` · `rest-api-postgres` · `cron-worker` · `webhook-receiver` · `ai-chat` |
| `--dockerfile` | build with the supplied Dockerfile inside --tarball |  |
| `--runtime <RUNTIME>` | function runtime | one of `node22` · `python312` · `go124` · `go124-alpine` · `node24` · `python313` |
| `--handler <HANDLER>` | function handler |  |
| `--name <SLUG>` | app name (default: selected source directory, or current directory) |  |
| `--profile <PROFILE>` | named app resource profile | one of `micro` · `small` · `medium` · `large` · `xlarge` |
| `--vcpu <N>` | assert the plan guest vCPU shape (omit to use the plan default) |  |
| `--function` | deploy as a function; skip shape auto-detection |  |
| `--app` | deploy as an app; skip shape auto-detection |  |
| `--yes` | skip the apply confirmation prompt |  |
| `--only <SLUGS>` | workloads to apply; retain unselected project workloads (comma-separated) |  |
| `--project` | deploy all detected workloads as one project (slug defaults from --name or source) |  |
| `--environment <SLUG>` | deploy to a registered project environment (defaults to linked context) |  |
| `--reason <text>` | free-text deploy reason (≤280 chars) |  |
| `--tag <TAG>` | annotation tag (incident_recovery\|hotfix\|scheduled_maintenance\|compliance_hold\|partner_request) | one of `incident_recovery` · `hotfix` · `scheduled_maintenance` · `compliance_hold` · `partner_request` |
| `--deployed-by <NAME>` | operator label (auto-resolved from git config user.name) |  |
| `--pr-number <N>` | GitHub PR number (positive int; 0 = absent). CI paths stamp via the GitHub Action. |  |
| `--exclude <SLUGS>` | omit workloads (slug, comma-separated; mutex with --only; ADR-124) |  |
| `--show-affected` | render the WillDeploy + Skipped + Unaffected + Removed partition (ADR-124) |  |
| `--persist-exclude` | record --exclude slugs into deployment_scope_exclusions (apply path only; ADR-124 follow-up #3) |  |
| `--project-slug <SLUG>` | kebab slug for the project (one-key provision) |  |
| `--canary-preset <PRESET>` | canary ladder preset | one of `none` · `slow` · `balanced` · `aggressive` · `1-10-50-100` · `custom` |
| `--canary-stages <STAGES>` | custom percent@duration canary stages |  |
| `--safe` | deploy with the balanced health-gated rollout and first-wake 5xx rollback |  |
| `--require-authn` | require bearer auth on every request |  |
| `--no-require-authn` | drop the token requirement |  |
| `--app-protocol <PROTOCOL>` | wire protocol selector | one of `http1` · `http2` · `grpc` |
| `--traffic-percent <PERCENT>` | deployment traffic split weight (0-100) |  |
| `--no-traffic` | stage with 0% production traffic and print the preview URL |  |
| `--no-triggers` | skip gregale.yaml trigger fan-out |  |
| `--wait` | wait for deployment to become live (default) |  |
| `--no-wait` | return after deployment is queued |  |
| `--create-only` | create or reserve the app without uploading a deployment |  |
| `--timeout <SECONDS>` | maximum wait seconds for deploy (default 1200) |  |
| `--idempotency-key <KEY>` | stable logical retry key for this deployment |  |
| `--secrets-file <PATH>` | seal KEY=VALUE pairs before the first deployment |  |
| `--secret-scan <on|off>` | scan .env files before packing | one of `on` · `off` |
| `--diff` | preview what would change without deploying |  |
| `--dry-run` | run deploy preflight without uploading or changing remote state |  |
| `--plan` | show local stateless app defaults without login or remote changes |  |
| `--strict` | fail on diff schema/quota/env breaks |  |
| `--lenient` | return success even when diff has breaks |  |
| `--server-diff` | compute deploy diff on apid |  |
| `--doctor-strict` | run doctor before deploy and abort on errors |  |
| `--no-doctor` | skip the automatic local doctor preflight |  |


## domains

Manage custom domains

`gregale domains [<subcommand>]`

### domains list

List custom domain bindings

### domains add

Bind a custom domain to an app

### domains rm

Remove a custom domain binding

### domains set-default

Set a verified domain as the app default

### domains verify

Re-verify DNS + cert for a domain

### domains show

Show a domain&#39;s cert details

### domains status

Show durable TLS status for all domains

### domains doctor

5-check doctor report (DNS / CNAME / TLS / CAA / IPv6)


## dev

Sync the dirty working tree to a stable remote developer environment (name defaults to linked context)

`gregale dev [<subcommand>] [--path <DIR>] [--name <PROJECT>] [--env-file <PATH>] [--service-override-file <PATH>] [--once] [--stop] [--no-logs] [--open]`

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | source directory |  |
| `--name <PROJECT>` | developer-session project name |  |
| `--env-file <PATH>` | sync KEY=VALUE entries as developer secrets |  |
| `--service-override-file <PATH>` | sync validated service URLs as developer secrets |  |
| `--once` | deploy once and exit |  |
| `--stop` | tear down the developer environment |  |
| `--no-logs` | do not attach the live runtime log stream |  |
| `--open` | open the developer environment URL after the first live sync |  |

### dev status

show developer-environment quota usage

### dev history

show edit-to-live timings and SLO guidance

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | source directory |  |
| `--name <PROJECT>` | developer-session project name |  |
| `--limit <N>` | number of recent syncs to show |  |

### dev setup

preflight a project and prepare the first developer environment

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | source directory |  |
| `--name <PROJECT>` | developer-session project name |  |
| `--env-file <PATH>` | validate and sync developer secrets |  |
| `--service-override-file <PATH>` | validate and sync service URLs |  |
| `--start` | start after preflight |  |
| `--once` | sync once and exit |  |
| `--no-logs` | do not attach runtime logs |  |
| `--open` | open the verified URL |  |
| `--postgres` | provision an isolated PostgreSQL database |  |
| `--postgres-region <REGION>` | choose managed database placement |  |


## preview

Manage preview environments (Mega-C PR-1 / issue #961 leaf 3)

`gregale preview [<subcommand>]`

### preview create

Create and deploy a pull-request preview from a GitHub ref

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | parent app slug (defaults to the linked app) |  |
| `--repo <OWNER/NAME>` | GitHub repository OWNER/NAME | required |
| `--ref <REF>` | branch, tag, or commit SHA | required |
| `--pr-number <N>` | pull-request number | required |
| `--ttl-hours <HOURS>` | preview lease in hours (default 168) |  |
| `--wait` | wait for the deployment to become live (default) |  |
| `--no-wait` | return after the deployment is queued |  |
| `--timeout <SECONDS>` | deployment wait timeout in seconds |  |
| `--idempotency-key <KEY>` | stable retry key |  |
| `--open` | open the preview URL after a successful create |  |

### preview list

List pull-request and developer previews (defaults to the linked app)

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | parent app slug |  |

### preview show

Inspect a preview and its latest deployment

### preview wait

Wait for a preview deployment to become ready

| Flag | Meaning | |
|---|---|---|
| `--progress` | print deployment transitions while waiting |  |
| `--open` | open the preview URL after it becomes ready |  |
| `--timeout <SECONDS>` | maximum seconds to wait |  |

### preview destroy

Tear down a preview app (POST /v1/preview/{slug}/destroy)


## edge-rules

Per-app edge rules (edge-rules list|create|get|update|rm --app &lt;slug&gt;)

`gregale edge-rules [<subcommand>] --app <slug> [--kind <value>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--kind <value>` | rule kind | one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` · `respond` · `retry` · `circuit_breaker` · `async` |

### edge-rules list

List edge rules

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | filter to a single app slug |  |
| `--kind <value>` | filter to a single kind | one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` · `respond` · `retry` · `circuit_breaker` · `async` |

### edge-rules create

Add an edge rule

### edge-rules get

Show one edge rule

### edge-rules update

Update one edge rule

### edge-rules rm

Delete one edge rule


## openapi

Manage app OpenAPI docs + pre-publish schema-drift checks

`gregale openapi [<subcommand>]`

### openapi diff

Diff two openapi.yaml files; exit 2 on any BREAKING row

### openapi get

Fetch an app OpenAPI document (manual_import|auto; slug defaults to linked context)

| Flag | Meaning | |
|---|---|---|
| `--source <manual_import|auto>` | document source | one of `manual_import` · `auto` |

### openapi import

Import an app OpenAPI document from a JSON file or stdin

### openapi dry-run

Preview uncovered routes without importing the document (slug defaults to linked context)

### openapi preview

Preview routes, edge policies, and the read-only OpenAPI contract diff (slug defaults to linked context)

| Flag | Meaning | |
|---|---|---|
| `--scope <scope>` | deployment scope to compare (defaults to linked environment, otherwise prod) |  |
| `--fail-on-unavailable` | fail when the contract-diff backend is unavailable |  |

### openapi apply

Plan or apply generated validation edge rules

| Flag | Meaning | |
|---|---|---|
| `--confirm` | apply the reviewed plan |  |
| `--preview-sha256 <SHA256>` | approval hash from the plan |  |
| `--match-host <HOST>` | hostname for generated rules |  |

### openapi rm

Remove the imported app OpenAPI document


## env

Pull/push .env &lt;-&gt; sealed secrets (--app &lt;slug&gt; or linked context)

`gregale env [<subcommand>] [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked context) |  |

### env pull

Pull sealed-secret keys to a .env skeleton (values blank)

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | env scope (defaults to linked project environment) |  |

### env push

Push KEY=VALUE pairs to sealed secrets (use --restart to apply now)

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | env scope (defaults to linked project environment) |  |
| `--restart` | restart app after applying changes (otherwise changes apply on next cold wake) |  |

### env diff

Render the env-diff matrix (presence / value-equality across scopes)


## init

Scaffold a reference project from a built-in template (--template NAME --path DIR [--deploy])

`gregale init --template <NAME> --path <DIR> [--deploy] [--name <SLUG>] [--secrets-file <PATH>] [--list]`

| Flag | Meaning | |
|---|---|---|
| `--template <NAME>` | template name | required; one of `hello-node` · `hello-python` · `hello-go` · `cron-example` · `function-node` · `function-python` · `function-go` · `function-node24` · `function-python313` · `event-worker` · `queue-worker` · `s3-uploader` · `slack-bot` · `rest-api-postgres` · `cron-worker` · `webhook-receiver` · `ai-chat` |
| `--path <DIR>` | target directory | required |
| `--deploy` | deploy after scaffolding |  |
| `--name <SLUG>` | app slug used with --deploy |  |
| `--secrets-file <PATH>` | seal KEY=VALUE pairs before the first deployment (requires --deploy) |  |
| `--list` | list available templates |  |


## inspect

Explain an app from its runtime, deployment, API, data, scaling, and release signals (slug defaults to linked context)

`gregale inspect [<slug>] [--upstreams] [--scope <scope>] [--errors]`

| Flag | Meaning | |
|---|---|---|
| `--upstreams` | List data upstreams captured for this app (ADR-098 §9.A) |  |
| `--scope <scope>` | filter by scope (defaults to linked project environment; used with --upstreams) |  |
| `--errors` | show the latest failed deployment&#39;s persisted error explanation |  |


## invoke

Functional smoke test (invoke [--async] &lt;slug&gt; [--payload J|@file|-]; slug defaults to linked context)

`gregale invoke [<slug>] [--async] [--payload <J|@file|->] [--on-success-webhook <ID>] [--on-failure-webhook <ID>]`

| Flag | Meaning | |
|---|---|---|
| `--async` | return immediately with status_url |  |
| `--payload <J|@file|->` | JSON payload (inline \| @file \| -) |  |
| `--on-success-webhook <ID>` | app webhook id for completed invocation callbacks |  |
| `--on-failure-webhook <ID>` | app webhook id for failed or dead-lettered callbacks |  |


## run

Run untrusted code in an isolated disposable microVM

`gregale run [--runtime <R>] [--source <CODE>] [--file <PATH>] [--input <J|@file|->] [--timeout-ms <N>] [--memory-mb <N>] [--cpu-millicores <N>] [--ephemeral-disk-mb <N>] [--max-output-bytes <N>] [--wait] [--watch] [--poll-interval <D>] [--wait-timeout <D>]`

| Flag | Meaning | |
|---|---|---|
| `--runtime <R>` | runtime (node22\|node24\|python312\|python313) | one of `node22` · `node24` · `python312` · `python313` |
| `--source <CODE>` | inline source code |  |
| `--file <PATH>` | source file (regular file only) |  |
| `--input <J|@file|->` | JSON input (inline \| @file \| -) |  |
| `--timeout-ms <N>` | execution timeout |  |
| `--memory-mb <N>` | memory limit |  |
| `--cpu-millicores <N>` | CPU limit |  |
| `--ephemeral-disk-mb <N>` | ephemeral scratch size |  |
| `--max-output-bytes <N>` | combined output cap |  |
| `--wait` | wait for terminal result |  |
| `--watch` | stream live output while waiting |  |
| `--poll-interval <D>` | status polling interval with --wait |  |
| `--wait-timeout <D>` | maximum client wait duration |  |


## runs

Inspect or cancel isolated disposable runs

`gregale runs [<subcommand>] <id>`

### runs list

List runs

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | maximum number of runs (1..200) |  |
| `--offset <N>` | number of matching runs to skip |  |
| `--status <STATUS>` | filter by lifecycle status | one of `queued` · `restoring` · `running` · `succeeded` · `failed` · `timed_out` · `out_of_memory` · `cancelled` |

### runs get

Show one run

### runs status

Show one run (alias for get)

### runs cancel

Cancel one run


## invocations

Per-account invocation ledger (invocations list|get &lt;id&gt;)

`gregale invocations [<subcommand>] <id>`

### invocations list

List invocations

### invocations get

Show one invocation


## debug

Production debugger (ADR-127)

`gregale debug [<subcommand>] [flags] <slug> [<request-id>]`

### debug requests

Per-request telemetry and root-cause synthesis (list/export/watch/get/show/evidence/explain/trace/inspect/replay)

### debug coverage

Observed debugger signal coverage (coverage &lt;slug&gt; [--since D])

### debug running

Explain why an app is still running, with request evidence when available (running &lt;slug&gt; [--since D] [--limit N])

### debug regressions

Regressions (live watch, lifecycle actions, per-app/--all, rollback)

### debug compare

Per-route deployment-vs-deployment compare

### debug bundle

Export a redacted incident bundle with coverage (bundle &lt;slug&gt; &lt;request-id-or-row-id&gt; [--output PATH])


## trace

Look up a W3C trace through the account trace index

`gregale trace <trace-id> [--watch] [--interval <DURATION>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--watch` | poll until linked invocations reach a terminal state |  |
| `--interval <DURATION>` | poll interval (default 1s) |  |
| `--timeout <DURATION>` | maximum watch duration (default 5m) |  |


## invitations

Standalone invitation actions (invitations peek &lt;token&gt;|accept &lt;token&gt;)

`gregale invitations [<subcommand>] <token>`

### invitations peek

Look up an invitation by token

### invitations accept

Accept an invitation


## invoices

List issued invoices

`gregale invoices`


## keys

Manage API keys (keys list|add|rm|rotate|grace-window)

`gregale keys [<subcommand>]`

### keys list

List API keys

### keys add

Mint a new API key

### keys rm

Revoke an API key

### keys rotate

Rotate an API key

### keys grace-window

Set the rotation grace window


## login

Authenticate this machine (--token for CI)

`gregale login [--token <TOKEN>]`

| Flag | Meaning | |
|---|---|---|
| `--token <TOKEN>` | use a pre-minted token (CI) |  |


## link

Link this checkout to a Gregale project

`gregale link <project-slug> [--app <slug>] [--environment <environment>] [--no-gitignore]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | workload/app slug for app-scoped commands |  |
| `--environment <environment>` | default project environment scope |  |
| `--no-gitignore` | do not add .gregale/ to .gitignore |  |


## logout

Revoke the managed CLI session and remove the stored token

`gregale logout`


## unlink

Remove the linked project from this checkout

`gregale unlink`


## context

Show the linked project and default app context

`gregale context`


## signup

Create a new account (signup [--email-only EMAIL | --password-stdin])

`gregale signup [--email-only <EMAIL>] [--password-stdin]`

| Flag | Meaning | |
|---|---|---|
| `--email-only <EMAIL>` | send a one-time signup link to this email (no password prompt) |  |
| `--password-stdin` | read password from stdin (CI; mutually exclusive with --email-only) |  |


## logs

Query runtime logs and HTTP request events (slug defaults to linked context)

`gregale logs [<slug>] [--follow] [--deployment <ID>] [--release <ID|vN>] [--source <SOURCE>] [--grep <SUBSTR>] [--since <15m|3d|RFC3339>] [--level <LEVEL>] [--status <100..599>] [--route <PATH>] [--request <ID>] [--limit <N>] [--all] [--explain] [--archive] [--instance <ID>] [--date <YYYY-MM-DD>]`

| Flag | Meaning | |
|---|---|---|
| `--follow` | stream logs until interrupted |  |
| `--deployment <ID>` | deployment id or vN revision (default: latest) |  |
| `--release <ID|vN>` | release id or revision (alias for --deployment) |  |
| `--source <SOURCE>` | log source | one of `runtime` · `http` |
| `--grep <SUBSTR>` | only show lines containing this substring |  |
| `--since <15m|3d|RFC3339>` | lookback duration or RFC3339 timestamp |  |
| `--level <LEVEL>` | only show lines at this level | one of `info` · `warn` · `error` |
| `--status <100..599>` | only show HTTP requests with this status |  |
| `--route <PATH>` | only show HTTP requests for this route |  |
| `--request <ID>` | show one HTTP request by public request id or row id |  |
| `--limit <N>` | HTTP request page size (1..200) |  |
| `--all` | read every retained HTTP request page |  |
| `--explain` | summarize the last failure and common error patterns |  |
| `--archive` | read durable logs for one instance and UTC day |  |
| `--instance <ID>` | instance id for --archive |  |
| `--date <YYYY-MM-DD>` | UTC day for --archive |  |


## metrics

Per-app or account-wide metrics (slug defaults to linked context)

`gregale metrics [<slug>] [--range <WINDOW>] [--account]`

| Flag | Meaning | |
|---|---|---|
| `--range <WINDOW>` | window (5m\|15m\|1h\|6h\|24h\|7d) | one of `5m` · `15m` · `1h` · `6h` · `24h` · `7d` |
| `--account` | account-wide roll-up |  |


## analytics

Historical request analytics (analytics &lt;slug&gt; [--since 24h] [--by route|country|referrer_host|ua_family|status]; slug defaults to linked context)

`gregale analytics [<slug>] [--since <WINDOW>] [--until <TIMESTAMP>] [--by <DIMENSION>]`

| Flag | Meaning | |
|---|---|---|
| `--since <WINDOW>` | lookback window |  |
| `--until <TIMESTAMP>` | exclusive RFC3339 end |  |
| `--by <DIMENSION>` | grouping dimension | one of `route` · `country` · `referrer_host` · `ua_family` · `status` |


## mfa

Manage account MFA (mfa enroll|confirm|verify|recover|disable)

`gregale mfa [<subcommand>]`

### mfa enroll

Begin TOTP enrolment

### mfa confirm

Confirm an enrolment code

### mfa verify

Verify a TOTP code (step-up)

### mfa recover

Use a recovery code

### mfa disable

Disable MFA


## open

Open the app&#39;s URL (slug defaults to linked context)

`gregale open [<subcommand>] [<slug>]`

### open docs

Open a CLI docs page (open docs [&lt;slug&gt;])


## orgs

Manage orgs, members, and workspace activity

`gregale orgs [<subcommand>]`

### orgs ls

List orgs

### orgs create

Create an org

### orgs info

Show one org

### orgs activity

Show the global infrastructure timeline

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--before <CURSOR>` | pagination cursor |  |
| `--kind-prefix <PREFIX>` | filter by activity kind prefix |  |
| `--actor-type <TYPE>` | filter by actor category | one of `user` · `api_key` · `github` · `system` · `operator` |
| `--app-id <UUID>` | filter by application UUID |  |
| `--limit <N>` | page size (1..100) |  |

### orgs rm

Delete one org

### orgs members

Manage org members

### orgs keys

Manage org API keys

### orgs transfer-ownership

Transfer org ownership

### orgs seat-usage

Show seat usage

### orgs invitations

Manage org invitations

### orgs me

Show current org membership

### orgs update

Update org metadata


## overage-cap

Set / clear the account&#39;s overage cap (--clear | &lt;cents&gt;)

`gregale overage-cap <cents> [--clear]`

| Flag | Meaning | |
|---|---|---|
| `--clear` | remove the overage cap |  |


## park

Park an app cold (kill all live instances)

`gregale park`


## plan

Change plan (free|hobby|pro|scale); paid upgrades open the provider checkout

`gregale plan`


## ps

Show live instances + state for an app (slug defaults to linked context)

`gregale ps [<slug>] [--all]`

| Flag | Meaning | |
|---|---|---|
| `--all` | include the newest 100 retained history rows (parked rows expire after 30d by default) |  |


## queue

Inspect queues and manage first-class queue bindings

`gregale queue [<subcommand>]`

### queue tail

Tail the wake queue

### queue send

Enqueue a wake request

| Flag | Meaning | |
|---|---|---|
| `--payload <J>` | JSON payload (inline \| @file \| -) |  |
| `--queue-name <QUEUE>` | logical queue name |  |

### queue receive

Receive a wake request

### queue state

Show queue state

### queue status

Show queue depth, scaling, bindings, and liveness

### queue peek

Peek at the next wake

### queue dead-letter

Inspect the dead-letter queue

### queue ack

Ack a wake

### queue setup

Configure a simple push workload with queue-depth scaling

| Flag | Meaning | |
|---|---|---|
| `--queue-name <QUEUE>` | logical queue name |  |
| `--target-depth <N>` | messages per worker before scaling out |  |
| `--max-concurrency <N>` | maximum concurrent deliveries per worker |  |
| `--max-attempts <N>` | maximum delivery attempts (0 uses the plan default) |  |
| `--retry-base-seconds <N>` | base retry delay in seconds |  |
| `--retry-max-seconds <N>` | maximum retry delay in seconds |  |
| `--retry-jitter-seconds <N>` | retry jitter in seconds (0..1) |  |
| `--force` | replace an existing default binding on another queue |  |

### queue bindings

Manage queue bindings


## dlq

Inspect, replay, or purge unified dead-letter events

`gregale dlq [<subcommand>] <app> [<event-id>]`

### dlq list

List app dead-letter events

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | max events (1..200) |  |
| `--before <ID>` | pagination cursor |  |

### dlq inspect

Inspect one dead-letter event

### dlq replay

Replay one event or --all

| Flag | Meaning | |
|---|---|---|
| `--all` | replay pending events |  |
| `--limit <N>` | maximum events (1..200) |  |

### dlq purge

Purge one event or --all

| Flag | Meaning | |
|---|---|---|
| `--all` | purge all events |  |
| `--limit <N>` | page size (1..200) |  |


## registry

Per-app private container registry credentials (registry list|set|rm --app &lt;slug&gt;)

`gregale registry [<subcommand>] --app <value>`

| Flag | Meaning | |
|---|---|---|
| `--app <value>` | app slug | required |

### registry list

List registry credentials

### registry set

Set a registry credential

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--registry <host>` | registry host | required |
| `--user <user>` | registry username | required |
| `--password-stdin` | read the registry password/token from stdin |  |

### registry rm

Remove a registry credential


## realtime

Manage realtime endpoints, policies, connections, channels, and auth

`gregale realtime [<subcommand>]`

### realtime list

List managed realtime endpoints

### realtime get

Show one endpoint and safe auth-rotation status

### realtime create

Create a managed realtime endpoint

### realtime update

Update endpoint callback, auth, or connection policy

### realtime delete

Delete a managed realtime endpoint

### realtime connections

List live connections for an endpoint

| Flag | Meaning | |
|---|---|---|
| `--channel <CHANNEL>` | only connections subscribed to this channel |  |
| `--principal <PRINCIPAL>` | only connections for this authenticated principal |  |
| `--limit <N>` | maximum connections to return (1-1000) |  |
| `--cursor <TOKEN>` | continue from a previous response&#39;s next_cursor |  |

### realtime drain

Close a bounded, filtered set of live connections

| Flag | Meaning | |
|---|---|---|
| `--reason <TEXT>` | required audit reason | required |
| `--channel <CHANNEL>` | only connections subscribed to this channel |  |
| `--principal <PRINCIPAL>` | only connections for this principal |  |
| `--connection-id <ID>` | select a specific connection; repeat up to 100 times |  |
| `--limit <N>` | maximum connections to select (1-1000) |  |
| `--dry-run` | preview without closing connections |  |
| `--allow-partial` | allow the reachable subset when nodes are unavailable |  |
| `--wait` | wait for the drain to reach a terminal state |  |
| `--timeout <DURATION>` | maximum time to wait with --wait |  |

### realtime drain-status

Show or wait for a durable realtime drain

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the drain to reach a terminal state |  |
| `--timeout <DURATION>` | maximum time to wait with --wait |  |

### realtime send

Send a message to one live connection

### realtime close

Close one live connection

### realtime subscribe

Subscribe one live connection to a channel

### realtime unsubscribe

Remove one live connection from a channel

### realtime publish

Publish a message to a channel

### realtime auth

Rotate, finalize, or inspect static bearer auth (auth rotate|finalize|status)


## rollback

Re-promote the previous deployment

`gregale rollback <slug> [--to <deployment_id|vN>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--to <deployment_id|vN>` | target deployment id or vN revision (e.g. v41) |  |
| `--json` | machine-readable output |  |


## projects

Inspect and recover repository projects

`gregale projects [<subcommand>] <project-slug>`

### projects list

List projects in this account

### projects info

Show a project and its workloads

### projects environments

Manage project environments (list|create|protect|unprotect|releases|history|config [set]|diff|preview|promote|status|rollback); promote supports --wait [--progress] [--timeout SECONDS]

### projects update

Update repository or production branch

| Flag | Meaning | |
|---|---|---|
| `--repo <OWNER/NAME>` | GitHub repository owner/name; empty unbinds |  |
| `--branch <BRANCH>` | production branch |  |

### projects rm

Preview or delete a project

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview affected state |  |
| `--yes` | confirm project deletion |  |


## scan

Decomposition dry-run (--tarball | --path | --repo OWNER/NAME)

`gregale scan [--tarball <PATH>] [--path <DIR>] [--repo <OWNER/NAME>] [--repository <OWNER/NAME>] [--install-id <N>] [--production-branch <BRANCH>] [--project-slug <SLUG>] [--environment <SLUG>] [--exclude <SLUGS>] [--show-affected] [--explain] [--persist-exclude]`

| Flag | Meaning | |
|---|---|---|
| `--tarball <PATH>` | scan a source tarball |  |
| `--path <DIR>` | scan a local directory |  |
| `--repo <OWNER/NAME>` | scan a GitHub repo after gregale connect |  |
| `--repository <OWNER/NAME>` | GitHub owner/name to bind to the project (defaults to --repo) |  |
| `--install-id <N>` | optional GitHub installation id; normally resolved from the connected account |  |
| `--production-branch <BRANCH>` | production branch for the project |  |
| `--project-slug <SLUG>` | kebab slug; default = repo dir basename |  |
| `--environment <SLUG>` | registered project environment to scan |  |
| `--exclude <SLUGS>` | omit workloads (slug, comma-separated; mutex with --only; ADR-124) |  |
| `--show-affected` | render the WillDeploy + Unaffected tables (ADR-124) |  |
| `--explain` | show detector provenance and skipped/merged decisions |  |
| `--persist-exclude` | record --exclude slugs into deployment_scope_exclusions (apply path only; ADR-124 follow-up #3) |  |


## secrets

Manage env secrets (secrets list|set|unset|list-all|rotate)

`gregale secrets [<subcommand>]`

### secrets list

List sealed secrets

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE|__all__>` | env scope filter (defaults to linked project environment) |  |

### secrets set

Set a sealed secret

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | env scope to write (defaults to linked project environment) |  |
| `--restart` | restart the app and apply updated secrets now |  |

### secrets unset

Remove a sealed secret

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | env scope to delete from (defaults to linked project environment) |  |

### secrets list-all

List every secret across apps

### secrets rotate

Re-seal one secret under the current host key

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | env scope to rotate (defaults to linked project environment) |  |
| `--restart` | restart the app and apply the rotated secret now |  |


## slo

Per-app SLO panel (gregale slo &lt;slug&gt; [--window 24h]; slug defaults to linked context)

`gregale slo [<slug>] [--window <WINDOW>]`

| Flag | Meaning | |
|---|---|---|
| `--window <WINDOW>` | window (1h\|24h\|7d) | one of `1h` · `24h` · `7d` |


## status

Personal SLO numbers (availability, wake p95, build success)

`gregale status`


## tail

Live tail of the unified event stream (app defaults to linked context)

`gregale tail [--app <slug>] [--include-stateless]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | filter to a single app slug (optional) |  |
| `--include-stateless` | also print stateless.advisory frames (default: hide) |  |


## trusted-publishers

Per-app cosign trusted-publisher list (admin; trusted-publishers add|remove|list)

`gregale trusted-publishers [<subcommand>]`

### trusted-publishers add

Add a trusted publisher

### trusted-publishers remove

Remove a trusted publisher

### trusted-publishers list

List trusted publishers


## usage

Show this month&#39;s usage (gregale usage [--month YYYY-MM]|daily [--day YYYY-MM-DD]|storage [--day YYYY-MM-DD]|summary)

`gregale usage [<subcommand>] [--month <YYYY-MM>] [--day <YYYY-MM-DD>]`

| Flag | Meaning | |
|---|---|---|
| `--month <YYYY-MM>` | month (YYYY-MM) |  |
| `--day <YYYY-MM-DD>` | day (YYYY-MM-DD) |  |

### usage daily

Per-day breakdown

### usage storage

Per-app storage bytes

### usage summary

Account roll-up


## version

Print the CLI version

`gregale version`


## config

Manage non-secret local CLI settings (config get|set|list)

`gregale config [<subcommand>]`

### config get

Show one effective setting

### config set

Persist one non-secret setting

### config list

Show all effective settings


## wake-timeline

Walk the per-wake event stream (wake-timeline &lt;slug&gt; &lt;wake-id&gt; [--since RFC3339] [--limit N] [--all]; slug defaults to linked context)

`gregale wake-timeline [<slug>] <wake-id> [--since <RFC3339>] [--limit <N>] [--all]`

| Flag | Meaning | |
|---|---|---|
| `--since <RFC3339>` | RFC3339 timestamp |  |
| `--limit <N>` | page size (1..1000) |  |
| `--all` | walk every page |  |


## throttle-suggestions

Per-route throttle recommendations + dry-run preview (gregale throttle-suggestions &lt;slug&gt; [--range 5m] [--dry-run --candidate-rps N --candidate-burst N])

`gregale throttle-suggestions <slug> [--range <WINDOW>] [--dry-run] [--candidate-rps <N>] [--candidate-burst <N>]`

| Flag | Meaning | |
|---|---|---|
| `--range <WINDOW>` | observation window (5m\|15m\|1h\|6h\|24h\|7d\|15d) | one of `5m` · `15m` · `1h` · `6h` · `24h` · `7d` · `15d` |
| `--dry-run` | enable the dry-run preview pass (requires --candidate-rps) |  |
| `--candidate-rps <N>` | candidate rate-limit rps for the dry-run preview |  |
| `--candidate-burst <N>` | candidate burst for the dry-run preview |  |


## wake

Wake a parked app (pulls out of snapshot)

`gregale wake`


## traffic

Manage deployment traffic split (available on every plan)

`gregale traffic [<subcommand>]`

### traffic set

Set the traffic split for a deployment

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--deployment <ID>` | deployment id or vN revision to set the traffic split on | required |
| `--percent <N>` | traffic weight in [0, 100]; -1 = unset (server default 100) | required |

### traffic promote

Promote a live deployment to 100% production traffic

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--deployment <ID>` | deployment id or vN revision to promote | required |
| `--if-serving <ID>` | require this deployment id or vN revision to remain at 100% traffic |  |

### traffic status

Show live deployment traffic weights for an app


## mirror

Manage traffic mirroring and sanitized replay (Pro/Scale only)

`gregale mirror [<subcommand>]`

### mirror list

List mirror rules

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### mirror create

Create a mirror rule

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--source <ID>` | source deployment id or vN revision (live) | required |
| `--mirror <ID>` | mirror deployment id or vN revision (live; same app) | required |
| `--percent <N>` | fan-out percent in [0, 100]; 100 = every request |  |
| `--include-body` | include request/response body hashes in the comparison ledger |  |
| `--redact-header <NAME>` | extra header name to redact (repeatable) |  |

### mirror info

Show one mirror rule

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--id <ID>` | mirror rule id | required |

### mirror update

Patch a mirror rule (patch semantics)

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--id <ID>` | mirror rule id | required |
| `--percent <N>` | new percent in [0, 100] |  |
| `--enable` | enable the rule (mutually exclusive with --disable) |  |
| `--disable` | disable the rule (mutually exclusive with --enable) |  |
| `--include-body` | enable body-hash comparison (mutually exclusive with --no-include-body) |  |
| `--no-include-body` | disable body-hash comparison |  |
| `--redact-header <NAME>` | extra header name to redact (repeatable) |  |
| `--clear-redact` | clear the customer&#39;s redact_headers list (drop to always-stripped only) |  |

### mirror rm

Delete a mirror rule

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--id <ID>` | mirror rule id | required |

### mirror summary

Aggregate mirror drift counts over a window

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--id <ID>` | mirror rule id | required |
| `--window <WINDOW>` | summary window: 1h \| 24h \| 7d (default 1h) | one of `1h` · `24h` · `7d` |

### mirror replay

Replay a sanitized historical request corpus

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--id <ID>` | mirror rule id | required |
| `--file <PATH>` | corpus JSON file, or - for stdin | required |
| `--allow-unsafe-methods` | allow POST, PUT, PATCH, and DELETE |  |


## cache

Declare or purge response caching (cache GET /path/:id for 30s)

`gregale cache [<subcommand>]`

### cache GET

Cache GET responses for a route

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (defaults to linked project context) |  |
| `--host <HOST>` | hostname override |  |
| `--stale-while-revalidate <DURATION>` | serve stale while refreshing |  |
| `--stale-if-error <DURATION>` | serve stale when the origin fails |  |
| `--vary-on <HEADER>` | header included in the cache key | one of `Accept-Language` · `Accept-Encoding` |
| `--priority <N>` | match priority (lower wins) |  |

### cache HEAD

Cache HEAD responses for a route

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (defaults to linked project context) |  |
| `--host <HOST>` | hostname override |  |
| `--stale-while-revalidate <DURATION>` | serve stale while refreshing |  |
| `--stale-if-error <DURATION>` | serve stale when the origin fails |  |
| `--vary-on <HEADER>` | header included in the cache key | one of `Accept-Language` · `Accept-Encoding` |
| `--priority <N>` | match priority (lower wins) |  |

### cache purge

Purge cached responses: cache purge &lt;slug&gt; [--path GLOB]

| Flag | Meaning | |
|---|---|---|
| `--path <GLOB>` | optional normalized request path glob |  |


## upload-cache

Inspect or clean resumable source-upload recovery state

`gregale upload-cache [<subcommand>]`

### upload-cache list

List resumable, stale, and orphaned cache entries

### upload-cache cleanup

Remove stale and excess state safely

| Flag | Meaning | |
|---|---|---|
| `--older-than <D>` | maximum recovery-state age |  |
| `--max-entries <N>` | maximum recovery records to retain |  |
| `--dry-run` | show actions without deleting files |  |


## webhooks

Manage outbound webhooks (webhooks list|add|info|update|rm|deliveries|retry|rotate-secret)

`gregale webhooks [<subcommand>]`

### webhooks list

List webhooks

### webhooks add

Add a webhook

### webhooks info

Show one webhook

### webhooks update

Update one webhook

### webhooks rm

Delete one webhook

### webhooks deliveries

Show the delivery ledger

### webhooks retry

Retry a failed delivery

### webhooks rotate-secret

Rotate the webhook signing secret

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--secret <VALUE>` | replacement HMAC-SHA256 secret |  |
| `--from-stdin` | read the replacement secret from stdin |  |


## whoami

Show the authenticated account

`gregale whoami`


## completion

Print a shell completion script (bash|zsh|fish|powershell)

`gregale completion [<subcommand>]`

### completion bash

Print the bash completion script

### completion zsh

Print the zsh completion script

### completion fish

Print the fish completion script

### completion powershell

Print the powershell completion snippet


## man

Print the gregale(1) man page (or gregale-&lt;command&gt;(1) with one arg)

`gregale man <command>`
