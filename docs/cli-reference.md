# gregale CLI reference

Generated from the CLI's command manifest by `gregale man --markdown`. Do not edit by hand.

| Command | What it does |
|---|---|
| [`account`](#account) | Manage the local account (account export\|delete\|restore\|status\|dpa\|slo) |
| [`add`](#add) | Provision and bind managed resources to an app |
| [`bindings`](#bindings) | Inspect app bindings and rotation status, manage storage credentials, or verify service and PostgreSQL connections |
| [`capabilities`](#capabilities) | Show feature maturity and plan availability |
| [`alerts`](#alerts) | Per-app alert rules (alerts list\|add\|info\|update\|rm\|rotate-secret\|preset --app &lt;slug&gt;) |
| [`audit-events`](#audit-events) | Audit-log query (audit-events list\|get &lt;id&gt;) |
| [`events`](#events) | Preview routing, publish events, inspect deliveries and routing history, and replay failures |
| [`send`](#send) | Reliably send work to another Gregale application |
| [`deliver`](#deliver) | Reliably deliver an event to a registered webhook |
| [`apps`](#apps) | List your apps |
| [`app`](#app) | Get/update one app or run a deployment-attached command |
| [`billing`](#billing) | Manage billing (portal, invoices, subscription, card on file) |
| [`canary`](#canary) | Project a canary preset against recent app traffic (canary simulate &lt;slug&gt;) |
| [`build`](#build) | Inspect builds (build status\|list\|provenance\|sbom) |
| [`connect`](#connect) | Connect a third-party service (github \| repo OWNER/NAME) |
| [`github`](#github) | Manage an app&#39;s GitHub installation and repository binding |
| [`cors`](#cors) | Configure CORS for an app (allow\|ls\|rm\|show) |
| [`crons`](#crons) | Manage scheduled HTTP requests and deployment commands |
| [`triggers`](#triggers) | Manage unified event triggers (broker mappings + cron-linked rows) |
| [`workers`](#workers) | Inspect and manage background worker pools |
| [`jobs`](#jobs) | Manage jobs (run-to-completion workloads) |
| [`workflows`](#workflows) | Manage durable execution workflows |
| [`dashboard`](#dashboard) | Open the account dashboard in your browser |
| [`doctor`](#doctor) | Preflight local source or OCI image metadata; runtime checks are skipped |
| [`delayed-task`](#delayed-task) | Schedule and inspect deferred invocations |
| [`deployments`](#deployments) | List deployments or manage stable named URLs for immutable revisions |
| [`deployment`](#deployment) | Get, summarize, or wait for one deployment (&lt;id&gt; \| summary &lt;id&gt; \| wait &lt;id&gt; \| set-min-instances &lt;id&gt;) |
| [`deploys`](#deploys) | Deployment drill-downs (deploys show\|status\|cancel\|reorder\|clear\|clear-obsolete\|retry) |
| [`deploy`](#deploy) | Deploy an app, function, or project |
| [`domains`](#domains) | Manage custom domains |
| [`dev`](#dev) | Sync local changes to a developer environment |
| [`diff`](#diff) | Compare two named environments in the linked project |
| [`test`](#test) | Run scenario suites and bounded local HTTP load tests |
| [`preview`](#preview) | Manage preview environments for pull requests |
| [`flags`](#flags) | Release application behavior to selected customers |
| [`platform-tenants`](#platform-tenants) | Manage one customer across app consumers and tenant hostnames |
| [`edge-rules`](#edge-rules) | Per-app edge rules (edge-rules list\|trace\|create\|get\|update\|rm --app &lt;slug&gt;) |
| [`openapi`](#openapi) | Manage app OpenAPI docs + pre-publish schema-drift checks |
| [`env`](#env) | Clone project environments or manage app runtime env/secrets |
| [`init`](#init) | Scaffold a project from a built-in template |
| [`inspect`](#inspect) | Explain an app from its runtime, deployment, API, data, scaling, and release signals (slug defaults to linked context) |
| [`invoke`](#invoke) | Functional smoke test (invoke [--async] &lt;slug&gt; [--payload J\|@file\|-]; slug defaults to linked context) |
| [`run`](#run) | Run untrusted code in an isolated disposable microVM |
| [`runs`](#runs) | Inspect or cancel isolated disposable runs |
| [`invocations`](#invocations) | Per-account invocation ledger (invocations list\|get\|wait &lt;id&gt;) |
| [`issues`](#issues) | Group failures and track ownership and release-aware resolution |
| [`debug`](#debug) | Inspect production requests and regressions |
| [`trace`](#trace) | Look up a W3C trace through the account trace index |
| [`invitations`](#invitations) | Standalone invitation actions (invitations peek &lt;token&gt;\|accept &lt;token&gt;) |
| [`invoices`](#invoices) | List issued invoices |
| [`keys`](#keys) | Manage API keys (keys list\|add\|rm\|rotate\|grace-window) |
| [`login`](#login) | Authenticate this machine |
| [`link`](#link) | Link this checkout to a Gregale project |
| [`logout`](#logout) | Revoke the managed CLI session and remove the stored token |
| [`unlink`](#unlink) | Remove the linked project from this checkout |
| [`context`](#context) | Show the linked project and default app context |
| [`signup`](#signup) | Create a new account (signup [--email-only EMAIL \| --password-stdin]) |
| [`logs`](#logs) | Query runtime logs and HTTP request events |
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
| [`secrets`](#secrets) | Manage env secrets (secrets list\|set\|unset\|list-all\|audit\|rotate) |
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
| [`mirror`](#mirror) | Manage traffic mirroring and sanitized replay (Pro/Scale only). Rules default to 5% and mirror only safe methods; bodies over 64 KiB are skipped, and raw bodies are never retained. |
| [`cache`](#cache) | Declare or purge response caching (cache GET /path/:id for 30s) |
| [`upload-cache`](#upload-cache) | Inspect or clean resumable source-upload recovery state |
| [`webhooks`](#webhooks) | Manage app and account release webhooks (webhooks account &lt;verb&gt;) |
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

Inspect app bindings and rotation status, manage storage credentials, or verify service and PostgreSQL connections

`gregale bindings [<subcommand>] <app>`

### bindings object-storage

Manage app-to-bucket compute bindings

#### bindings object-storage list

List safe binding metadata

`gregale bindings object-storage list <app> <bucket>`

Examples:

```sh
gregale bindings object-storage list my-api assets
```

#### bindings object-storage rotate

Rotate a binding credential and optionally wait for retirement

`gregale bindings object-storage rotate <app> <bucket> <binding-id> [--wait] [--wait-timeout <DURATION>] [--poll-interval <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the previous credential to retire |  |
| `--wait-timeout <DURATION>` | maximum time to wait for rotation (default 5m) |  |
| `--poll-interval <DURATION>` | status polling interval while waiting (default 1s) |  |

Examples:

```sh
gregale bindings object-storage rotate my-api assets BINDING_ID --wait
```

#### bindings object-storage revoke

Revoke a binding credential

`gregale bindings object-storage revoke <app> <bucket> <binding-id>`

Examples:

```sh
gregale bindings object-storage revoke my-api assets BINDING_ID
```

### bindings verify

Check a private service route or test one managed PostgreSQL binding

`gregale bindings verify <app> [<service>] [--all] [--postgres <ENVIRONMENT_KEY>] [--poll-interval <D>] [--wait-timeout <D>]`

| Flag | Meaning | |
|---|---|---|
| `--all` | verify every declared service binding |  |
| `--postgres <ENVIRONMENT_KEY>` | verify one managed PostgreSQL binding by environment key |  |
| `--poll-interval <D>` | status polling interval while the canary runs |  |
| `--wait-timeout <D>` | maximum time to wait for the canary task |  |

Examples:

```sh
gregale bindings verify my-api billing
gregale bindings verify my-api --all
gregale bindings verify my-api --postgres DATABASE_URL
```

### bindings smoke

Invoke a path on one exact live target deployment over the private HTTPS binding

`gregale bindings smoke <app> <service> --deployment <ID> --path <PATH> [--expect-status <CODE>] [--poll-interval <D>] [--wait-timeout <D>]`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | exact live target deployment to invoke | required |
| `--path <PATH>` | absolute path on the target service | required |
| `--expect-status <CODE>` | require this exact HTTP status; default accepts any 2xx response |  |
| `--poll-interval <D>` | status polling interval while the smoke task runs |  |
| `--wait-timeout <D>` | maximum time to wait for the smoke task |  |


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

Preview routing, publish events, inspect deliveries and routing history, and replay failures

`gregale events [<subcommand>]`

### events preview

Preview account-wide event routing without publishing

| Flag | Meaning | |
|---|---|---|
| `--id <ID>` | event id to use when filters inspect the CloudEvents id |  |
| `--source <SOURCE>` | event source (or first positional argument) |  |
| `--type <TYPE>` | event type (or second positional argument) |  |
| `--data <J|@file|->` | JSON event data (inline \| @file \| -) | required |
| `--time <RFC3339>` | event time (RFC3339; defaults to server time) |  |

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

Inspect event deliveries, replays, and pre-invocation fanout failures

| Flag | Meaning | |
|---|---|---|
| `--event-source <SOURCE>` | narrow event filter to one published source; requires --event-id |  |
| `--event-id <ID>` | filter by published event id |  |
| `--state <STATE>` | filter by delivery state; failed includes recipient fanout failures |  |
| `--before <CURSOR>` | pagination cursor |  |
| `--fanout-before <CURSOR>` | pre-invocation failure pagination cursor |  |
| `--limit <N>` | max deliveries (1..200) |  |

### events fanout-history

Inspect immutable routing outcomes and replay history for one event

`gregale events fanout-history <app> --event-source <SOURCE> --event-id <ID> [--subscription-id <ID>] [--before <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--event-source <SOURCE>` | published event source | required |
| `--event-id <ID>` | published event id | required |
| `--subscription-id <ID>` | narrow history to one recipient |  |
| `--before <CURSOR>` | pagination cursor |  |
| `--limit <N>` | max history rows (1..200) |  |

### events replay

Retry one terminal pre-invocation recipient failure using its event identity and subscription ID from events deliveries

`gregale events replay <app> --event-id <ID> --event-source <SOURCE> --subscription-id <ID>`

| Flag | Meaning | |
|---|---|---|
| `--event-id <ID>` | published event id | required |
| `--event-source <SOURCE>` | published event source | required |
| `--subscription-id <ID>` | failed subscription id | required |

### events replay-retryable

Retry a bounded batch of terminal failures classified as retryable; pass --event-source and --event-id together to filter

`gregale events replay-retryable <app> [--event-source <SOURCE>] [--event-id <ID>] [--limit <N>] --yes <value>`

| Flag | Meaning | |
|---|---|---|
| `--event-source <SOURCE>` | limit replay to one published event source |  |
| `--event-id <ID>` | limit replay to one published event |  |
| `--limit <N>` | max recipients to requeue (1..100) |  |
| `--yes <value>` | confirm requeueing retryable event recipients | required |


## send

Reliably send work to another Gregale application

`gregale send <target-app> --type <TYPE> --data <J|@file|-> [--id <ID>] [--source <SOURCE>] [--time <RFC3339>] [--queue-name <QUEUE>] [--work-policy <NAME>] [--work-key <JSON>] [--work-fairness-key <JSON>] [--idempotency-key <KEY>]`

| Flag | Meaning | |
|---|---|---|
| `--type <TYPE>` | event type | required |
| `--data <J|@file|->` | JSON event data (inline \| @file \| -) | required |
| `--id <ID>` | stable event id |  |
| `--source <SOURCE>` | event source |  |
| `--time <RFC3339>` | event time |  |
| `--queue-name <QUEUE>` | target logical queue name |  |
| `--work-policy <NAME>` | named work policy for an unnamed queue |  |
| `--work-key <JSON>` | JSON scalar identifying related work |  |
| `--work-fairness-key <JSON>` | JSON scalar shared by related work keys |  |
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

Examples:

```sh
gregale apps
gregale apps --json
```

### apps ls

Alias for the default list action

### apps restore

Restore an app during its deletion grace window

### apps routes

List admitted per-route labels for one app

### apps tcp

Manage raw TCP listeners

### apps streaming-cap

Show app streaming classification

### apps -q

Delete one app (positional: &lt;slug&gt;)

### apps --quiet

Delete one app (positional: &lt;slug&gt;)


## app

Get/update one app or run a deployment-attached command

`gregale app <slug> [<subcommand>] [--visibility <public|internal>] [--profile <micro|small|medium|large|xlarge>] [--ram <MB>] [--cpu-millicores <250|500|1000>] [--max-concurrency <N>] [--concurrency-overflow <value>] [--max-queue-depth <N>] [--max-queue-wait <DURATION>] [--max-queue-wait-ms <N>] [--wake-max-queue-depth <N>] [--wake-max-queue-wait-seconds <N>] [--idle <SEC>] [--request-timeout <SEC>] [--require-signed <value>] [--security-policy <value>] [--basic-user <USER>] [--basic-pass <PASS>] [--min <N>] [--autoscale-target-rps <N>] [--autoscale-target-cpu-pct <1..100>] [--warm-snapshot] [--no-warm-snapshot] [--warm-snapshot-min-requests <N>] [--warm-snapshot-min-ms <MS>] [--warm-pool-size <N>] [--eviction-priority <best_effort|reserved>] [--require-authn] [--no-require-authn] [--platform-tenant-required] [--no-platform-tenant-required] [--maintenance] [--no-maintenance] [--streaming-enabled] [--no-streaming-enabled] [--websocket-enabled] [--no-websocket] [--route-metrics] [--no-route-metrics] [--consumer-auth-mode <optional|required>] [--only-declared-routes] [--no-only-declared-routes] [--head-wakes] [--crawler-policy <wake|cached|block>] [--health-path <PATH>] [--health-path-wakes] [--no-health-path-wakes] [--app-protocol <http1|http2|grpc>] [--public-auth <open|bearer|basic|ip_allowlist|internal_only>] [--ip-allowlist <CIDR>]... [--overflow-node <NAME>]`

| Flag | Meaning | |
|---|---|---|
| `--visibility <public|internal>` | set public edge exposure | one of `public` · `internal` |
| `--profile <micro|small|medium|large|xlarge>` | set a named RAM/CPU profile |  |
| `--ram <MB>` | set RAM in MB |  |
| `--cpu-millicores <250|500|1000>` | set sustained CPU allowance |  |
| `--max-concurrency <N>` | set max_concurrency |  |
| `--concurrency-overflow <value>` | set saturated concurrency behavior | one of `queue` · `drop` |
| `--max-queue-depth <N>` | set maximum warm-saturation waiters |  |
| `--max-queue-wait <DURATION>` | set maximum warm-saturation wait as a duration |  |
| `--max-queue-wait-ms <N>` | set maximum queued concurrency wait |  |
| `--wake-max-queue-depth <N>` | set per-app cold-wake waiter cap |  |
| `--wake-max-queue-wait-seconds <N>` | set per-app cold-wake wait budget |  |
| `--idle <SEC>` | set idle timeout in seconds |  |
| `--request-timeout <SEC>` | set per-app request timeout in seconds |  |
| `--require-signed <value>` | toggle require_signed | one of `true` · `false` |
| `--security-policy <value>` | deploy posture policy | one of `off` · `warn` · `enforce` |
| `--basic-user <USER>` | basic-auth username (required with --public-auth=basic) |  |
| `--basic-pass <PASS>` | basic-auth password (required with --public-auth=basic) |  |
| `--min <N>` | set minimum warm instances (Pro/Scale only) |  |
| `--autoscale-target-rps <N>` | set per-instance RPS scale-up target; 0 disables |  |
| `--autoscale-target-cpu-pct <1..100>` | set per-instance CPU scale-up target; 0 disables |  |
| `--warm-snapshot` | enable the warm-snapshot tier |  |
| `--no-warm-snapshot` | disable the warm-snapshot tier |  |
| `--warm-snapshot-min-requests <N>` | set the warm-snapshot request threshold |  |
| `--warm-snapshot-min-ms <MS>` | set the warm-snapshot ready-time threshold |  |
| `--warm-pool-size <N>` | set the paused warm-pool size |  |
| `--eviction-priority <best_effort|reserved>` | set the app eviction tier | one of `best_effort` · `reserved` |
| `--require-authn` | require a Gregale bearer token on every request (Pro/Scale only) |  |
| `--no-require-authn` | disable the per-deployment token requirement |  |
| `--platform-tenant-required` | require verified customer identity on app traffic (Hobby and above) |  |
| `--no-platform-tenant-required` | allow app traffic without verified customer identity |  |
| `--maintenance` | put every request into 503 maintenance mode |  |
| `--no-maintenance` | resume normal request handling |  |
| `--streaming-enabled` | enable streamed responses (plan eligibility is checked by the API) |  |
| `--no-streaming-enabled` | use buffered responses |  |
| `--websocket-enabled` | allow WebSocket upgrade forwarding (plan eligibility is checked by the API) |  |
| `--no-websocket` | disable WebSocket upgrade forwarding |  |
| `--route-metrics` | enable per-route gateway metrics (plan eligibility is checked by the API) |  |
| `--no-route-metrics` | disable per-route gateway metrics |  |
| `--consumer-auth-mode <optional|required>` | end-customer API-key policy: optional\|required | one of `optional` · `required` |
| `--only-declared-routes` | reject undeclared paths before waking the app (OpenAPI or explicit route list) |  |
| `--no-only-declared-routes` | disable the declared-route pre-wake gate |  |
| `--head-wakes` | wake a parked app for HEAD / |  |
| `--crawler-policy <wake|cached|block>` | monitor/crawler wake policy | one of `wake` · `cached` · `block` |
| `--health-path <PATH>` | set the monitor-facing health path |  |
| `--health-path-wakes` | allow health probes to wake the app |  |
| `--no-health-path-wakes` | answer health probes without waking the app |  |
| `--app-protocol <http1|http2|grpc>` | set the wire-protocol selector | one of `http1` · `http2` · `grpc` |
| `--public-auth <open|bearer|basic|ip_allowlist|internal_only>` | set public URL authentication; internal_only admits Gregale internal services, ip_allowlist is Pro+ | one of `open` · `bearer` · `basic` · `ip_allowlist` · `internal_only` |
| `--ip-allowlist <CIDR>` | allow a CIDR through the public URL; repeat for multiple ranges; requires --public-auth ip_allowlist |  |
| `--overflow-node <NAME>` | set or clear the preferred overflow compute node |  |

Examples:

```sh
gregale app my-api --maintenance
gregale app my-api --no-maintenance --streaming-enabled --websocket-enabled --route-metrics
gregale app my-api --consumer-auth-mode required --json
```

### app scale

Set max_concurrency / resource profile / RAM / CPU

### app rename

Rename an app

### app restart

Park and wake from a fresh snapshot

### app exec

Run a one-off command against the live deployment

| Flag | Meaning | |
|---|---|---|
| `--shell` | interpret one command string through the app shell |  |
| `--detach` | return after the task is queued |  |
| `--timeout-seconds <N>` | server-side command timeout |  |
| `--max-output-bytes <N>` | combined stdout/stderr tail cap |  |
| `--poll-interval <D>` | status polling interval while attached |  |
| `--wait-timeout <D>` | maximum attached wait |  |

### app security

Show posture or configure deploy enforcement

| Flag | Meaning | |
|---|---|---|
| `--posture` | show the read-only security posture |  |
| `--require-signed <true|false>` | require signed images on deploy | one of `true` · `false` |
| `--security-policy <off|warn|enforce>` | deploy posture policy | one of `off` · `warn` · `enforce` |

### app egress-allowlist

Inspect or update the outbound CIDR allowlist

### app egress-ports

Inspect or update the extra outbound TCP ports (Pro/Scale)

### app network

Inspect networking or manage private-network attachments

### app routes

List admitted per-route labels for one app

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

Examples:

```sh
gregale connect github
```

### connect repo

Open the dashboard wizard to bind &lt;owner&gt;/&lt;name&gt; to a Gregale app

`gregale connect repo <owner>/<name>`

Examples:

```sh
gregale connect repo acme/my-api
```


## github

Manage an app&#39;s GitHub installation and repository binding

`gregale github [<subcommand>] <slug>`

### github status

Show the GitHub connection health for &lt;slug&gt;

`gregale github status <slug>`

Examples:

```sh
gregale github status my-api
```

### github sync

Reconcile repository access with GitHub

`gregale github sync <slug>`

Examples:

```sh
gregale github sync my-api
```

### github repos

List repositories visible to the connected GitHub installation for &lt;slug&gt;

`gregale github repos <slug>`

Examples:

```sh
gregale github repos my-api
```

### github bind

Bind &lt;slug&gt; to a visible GitHub repository

`gregale github bind <slug> [--installation-id <ID>] --repo <OWNER/NAME> [--branch <BRANCH>] [--deploy-branches <MAPPINGS>]`

| Flag | Meaning | |
|---|---|---|
| `--installation-id <ID>` | GitHub App installation id (auto-resolved when omitted) |  |
| `--repo <OWNER/NAME>` | GitHub repository OWNER/NAME | required |
| `--branch <BRANCH>` | production branch |  |
| `--deploy-branches <MAPPINGS>` | branch=scope mappings |  |

Examples:

```sh
gregale github bind my-api --repo acme/my-api --branch main
```

### github setup

Bind GitHub, configure previews, and write an Actions workflow

`gregale github setup <slug> [--repo <OWNER/NAME>] [--production-branch <BRANCH>] [--deploy-branches <MAPPINGS>] [--pinned-sha <SHA>] [--pin-action] [--enable-action-updates] [--workflow <PATH>] [--preview] [--no-preview] [--preview-ttl-hours <HOURS>] [--preview-service-policy <POLICY>] [--root-dir <DIR>] [--ignore <PATHS>] [--rollout <MODE>] [--dry-run] [--force]`

| Flag | Meaning | |
|---|---|---|
| `--repo <OWNER/NAME>` | GitHub repository OWNER/NAME (required for a dry run) |  |
| `--production-branch <BRANCH>` | production branch (default: current binding or main) |  |
| `--deploy-branches <MAPPINGS>` | comma-separated branch=environment mappings (default or registered environment) |  |
| `--pinned-sha <SHA>` | pin the generated deploy Action to this full 40-character commit SHA (default: immutable SHA embedded in the CLI release) |  |
| `--pin-action` | resolve the current v0 deploy Action tag to its commit SHA |  |
| `--enable-action-updates` | add a weekly GitHub Actions Dependabot updater |  |
| `--workflow <PATH>` | workflow path relative to repository root |  |
| `--preview` | enable pull-request previews |  |
| `--no-preview` | disable pull-request previews |  |
| `--preview-ttl-hours <HOURS>` | preview lease in hours (1-720) |  |
| `--preview-service-policy <POLICY>` | preview-to-production service calls: deny\|allow_marked | one of `deny` · `allow_marked` |
| `--root-dir <DIR>` | repository-relative source root for the root workload |  |
| `--ignore <PATHS>` | comma-separated ignored change paths |  |
| `--rollout <MODE>` | production rollout mode: standard\|safe (safe requires Pro/Scale) | one of `standard` · `safe` |
| `--dry-run` | show generated files without writing or changing remote state |  |
| `--force` | overwrite an existing workflow file |  |

Examples:

```sh
gregale github setup my-api --repo acme/my-api --dry-run
gregale github setup my-api --repo acme/my-api --preview --preview-ttl-hours 72
```

### github disconnect

Remove the app&#39;s GitHub repository binding

`gregale github disconnect <slug> [--yes]`

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

Manage scheduled HTTP requests and deployment commands

`gregale crons [<subcommand>]`

### crons list

List cron rules

### crons add

Schedule an HTTP request or deployment command

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (required) | required |
| `--schedule <EXPR>` | five-field cron expression (required) | required |
| `--path <PATH>` | HTTP request path (mutually exclusive with --command) |  |
| `--command <EXEC>` | executable for a deployment command cron |  |
| `--arg <ARG>` | append one command argument (repeatable) |  |
| `--shell` | run --command as one shell string |  |
| `--timeout-seconds <N>` | command timeout (default 600 seconds) |  |
| `--max-output-bytes <N>` | captured output limit (default 1048576 bytes) |  |
| `--timezone <TZ>` | IANA timezone (default UTC) |  |
| `--skip-if-running` | skip fires while the previous run is active |  |
| `--retry-max` | additional command attempts after failure or timeout |  |
| `--retry-backoff-seconds` | base retry delay; doubles per attempt |  |
| `--schedule-policy <JSON>` | versioned schedule policy JSON |  |
| `--failure-rules <JSON>` | versioned retry rules JSON (command crons only) |  |

### crons info

Show one cron rule

### crons update

Update one cron rule

| Flag | Meaning | |
|---|---|---|
| `--schedule <EXPR>` | new five-field cron expression |  |
| `--path <PATH>` | HTTP request path |  |
| `--timezone <TZ>` | IANA timezone |  |
| `--enable` | enable the cron |  |
| `--disable` | disable the cron |  |
| `--skip-if-running` | skip fires while a previous run is active |  |
| `--allow-overlap` | allow scheduled fires to overlap |  |
| `--retry-max` | additional command attempts after failure or timeout |  |
| `--retry-backoff-seconds <N>` | base retry delay; doubles per attempt |  |
| `--schedule-policy <JSON>` | replace versioned schedule policy JSON |  |
| `--failure-rules <JSON>` | replace versioned retry rules JSON (command crons only) |  |

### crons rm

Delete one cron rule

### crons run

Fire one cron immediately

### crons fire-now

Show the status of a manual fire request

### crons runs

Show execution history

| Flag | Meaning | |
|---|---|---|
| `--before <CURSOR>` | pagination cursor for older runs |  |
| `--limit <N>` | max runs to show (1..100) |  |
| `--run <TASK-ID>` | show details and captured output for one command run |  |

### crons occurrences

Inspect scheduled occurrence decisions

| Flag | Meaning | |
|---|---|---|
| `--before <ID>` | occurrence id cursor from the previous page |  |
| `--limit <N>` | max occurrence decisions (1..200) |  |

### crons cancel

Request cancellation of one command-cron run


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

| Flag | Meaning | |
|---|---|---|
| `--image <REF>` | OCI image (required) | required |
| `--schedule <EXPR>` | recurring five-field cron schedule |  |
| `--timezone <TZ>` | IANA timezone for the recurring schedule |  |
| `--schedule-policy <JSON>` | versioned recurring schedule policy JSON |  |
| `--failure-rules <JSON>` | versioned exit-code and outcome retry rules JSON |  |

### jobs info

Show one job

### jobs update

Update one job

| Flag | Meaning | |
|---|---|---|
| `--schedule <EXPR>` | replace recurring cron schedule |  |
| `--timezone <TZ>` | replace schedule IANA timezone |  |
| `--unschedule` | remove recurring schedule |  |
| `--schedule-policy <JSON>` | replace versioned recurring schedule policy JSON |  |
| `--failure-rules <JSON>` | replace versioned exit-code and outcome retry rules JSON |  |

### jobs rm

Soft-delete one job

### jobs run

Dispatch a new run (fan-out N tasks)

| Flag | Meaning | |
|---|---|---|
| `--input <ID=REF>` | repeatable input binding |  |
| `--input-manifest-uri <URI>` | account-readable input manifest object |  |
| `--input-manifest-sha256 <DIGEST>` | SHA-256 of exact manifest bytes |  |
| `--parallelism <N>` | maximum concurrent tasks |  |
| `--flexible` | use spare capacity within a start window |  |
| `--eligible-at <RFC3339>` | earliest task start |  |
| `--latest-start-at <RFC3339>` | latest task start |  |
| `--fail-fast` | cancel unstarted tasks after permanent failure |  |
| `--failure-rules <JSON>` | override versioned exit-code and outcome retry rules for this run |  |

### jobs runs

List runs for one job

### jobs occurrences

Inspect recurring schedule decisions

| Flag | Meaning | |
|---|---|---|
| `--before <ID>` | occurrence id cursor from the previous page |  |
| `--limit <N>` | max occurrence decisions (1..200) |  |

### jobs cancel

Cancel a run

### jobs tasks

List tasks for one run

### jobs attempts

List retained attempts for one task

### jobs retry

Retry one failed task

### jobs replay-failed

Replay unsuccessful tasks in a linked run

### jobs artifact-url

Verify a managed result and get a signed URL

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

Examples:

```sh
gregale doctor
gregale doctor --strict
```


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
| `--work-policy <NAME>` | named app work policy |  |
| `--work-key <JSON>` | JSON scalar identifying related work |  |
| `--work-fairness-key <JSON>` | JSON scalar shared by related work keys |  |
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

List deployments or manage stable named URLs for immutable revisions

`gregale deployments [<subcommand>] [--app <slug>] [--limit <N>] [--before <cursor>] [--all] [--wide]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (app-scoped deployment history) |  |
| `--limit <N>` | page size (1-200) |  |
| `--before <cursor>` | pagination cursor (RFC3339Nano) |  |
| `--all` | walk every page |  |
| `--wide` | include annotation columns (by / pr / tag / reason) |  |

Examples:

```sh
gregale deployments --app my-api --limit 10
gregale deployments --app my-api --wide
```

### deployments alias

Manage stable named URLs for immutable deployments

#### deployments alias list

List deployment aliases for an app

`gregale deployments alias list [--app <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; defaults to the linked project |  |

#### deployments alias set

Point an alias at an exact deployment revision

`gregale deployments alias set [--app <SLUG>] --name <NAME> --deployment <ID|vN>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; defaults to the linked project |  |
| `--name <NAME>` | lowercase DNS-label alias name | required |
| `--deployment <ID|vN>` | deployment ID or app revision (vN) | required |

#### deployments alias delete

Remove an alias without deleting its deployment

`gregale deployments alias delete [--app <SLUG>] --name <NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; defaults to the linked project |  |
| `--name <NAME>` | lowercase DNS-label alias name | required |


## deployment

Get, summarize, or wait for one deployment (&lt;id&gt; | summary &lt;id&gt; | wait &lt;id&gt; | set-min-instances &lt;id&gt;)

`gregale deployment [<subcommand>] <id|vN> [--app <SLUG>] [--show-scan] [--min <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--show-scan` | include the per-deploy grype scan payload |  |
| `--min <N>` | min_instances floor (&gt;= 0) |  |

Examples:

```sh
gregale deployment summary v42 --app my-api
gregale deployment wait v42 --app my-api
```

### deployment summary

Show the release diff and rollback target

`gregale deployment summary <id|vN> --app <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |

Examples:

```sh
gregale deployment summary v42 --app my-api
gregale deployment summary v42 --app my-api --json
```

### deployment wait

Wait until a deployment is live (or safe rollout completes)

`gregale deployment wait <id|vN> [--app <SLUG>] [--rollout] [--progress] [--timeout <SECONDS>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug for a vN revision outside a linked project |  |
| `--rollout` | wait for safe rollout to reach 100% traffic |  |
| `--progress` | print rollout transitions while waiting (human output only) |  |
| `--timeout <SECONDS>` | maximum seconds to wait |  |

Examples:

```sh
gregale deployment wait 00000000000000000000000000000001
gregale deployment wait 00000000000000000000000000000001 --rollout --progress
```

### deployment set-min-instances

Set the per-deployment cold-wake floor


## deploys

Deployment drill-downs (deploys show|status|cancel|reorder|clear|clear-obsolete|retry)

`gregale deploys [<subcommand>] <id|vN> [--app <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |

Examples:

```sh
gregale deploys status 00000000000000000000000000000001
gregale deploys show v42 --app my-api --status
```

### deploys show

Print the closed 6-stage post-stream summary

`gregale deploys show <id|vN> [--app <SLUG>] [--status] [--url]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--status` | include terminal status and timing |  |
| `--url` | print only the deployment preview URL |  |

Examples:

```sh
gregale deploys show 00000000000000000000000000000001
gregale deploys show v42 --app my-api --status
```

### deploys status

Print stages, terminal status, and failure guidance

`gregale deploys status <id|vN> [--app <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |

Examples:

```sh
gregale deploys status 00000000000000000000000000000001
gregale deploys status v42 --app my-api --json
```

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

Deploy an app, function, or project

`gregale deploy [--image <REF>] [--tarball <PATH>] [--path <DIR>] [--source <auto|head|worktree>] [--worktree] [--repo <OWNER/NAME>] [--repository <OWNER/NAME>] [--install-id <N>] [--production-branch <BRANCH>] [--ref <REF>] [--source-branch <BRANCH>] [--github] [--pinned-sha <SHA>] [--pin-action] [--template <NAME>] [--dockerfile] [--runtime <RUNTIME>] [--handler <HANDLER>] [--name <SLUG>] [--profile <PROFILE>] [--vcpu <N>] [--execution-mode <request|service|worker|job>] [--restart-policy <no|on-failure|always|unless-stopped>] [--startup-deadline-s <SECONDS>] [--max-retries <N>] [--function] [--app] [--yes] [--only <SLUGS>] [--project] [--environment <SLUG>] [--reason <text>] [--tag <TAG>] [--deployed-by <NAME>] [--pr-number <N>] [--exclude <SLUGS>] [--show-affected] [--persist-exclude] [--project-slug <SLUG>] [--canary-preset <PRESET>] [--canary-stages <STAGES>] [--safe] [--require-authn] [--no-require-authn] [--platform-tenant-required] [--no-platform-tenant-required] [--app-protocol <PROTOCOL>] [--traffic-percent <PERCENT>] [--no-traffic] [--rollback-on-5xx] [--healthcheck-path <PATH>] [--healthcheck-grpc] [--healthcheck-grpc-service <SERVICE>] [--disable-startup-cpu-boost] [--no-triggers] [--wait] [--no-wait] [--create-only] [--timeout <SECONDS>] [--idempotency-key <KEY>] [--secrets-file <PATH>] [--secret-scan <on|off>] [--diff] [--dry-run] [--plan] [--strict] [--lenient] [--server-diff] [--doctor-strict] [--no-doctor]`

| Flag | Meaning | |
|---|---|---|
| `--image <REF>` | deploy from a container image reference |  |
| `--tarball <PATH>` | deploy from a source tarball |  |
| `--path <DIR>` | deploy a selected local source directory (relative to the current directory) |  |
| `--source <auto|head|worktree>` | local source policy (default: auto) | one of `auto` · `head` · `worktree` |
| `--worktree` | deploy the selected source directory from the working tree, including local changes |  |
| `--repo <OWNER/NAME>` | deploy from a GitHub repo |  |
| `--repository <OWNER/NAME>` | GitHub owner/name to bind to a project |  |
| `--install-id <N>` | GitHub installation id for a project binding |  |
| `--production-branch <BRANCH>` | production branch for a project binding |  |
| `--ref <REF>` | git ref for --repo (branch, tag, or 40-char SHA) |  |
| `--source-branch <BRANCH>` | reject promotion if the branch for a pinned --ref moves |  |
| `--github` | emit a GitHub Actions workflow snippet for the Gregale deploy action |  |
| `--pinned-sha <SHA>` | with --github only, pin the generated Action to this full 40-character commit SHA |  |
| `--pin-action` | with --github only, resolve the current v0 Action tag to its commit SHA |  |
| `--template <NAME>` | scaffold from a built-in template | one of `hello-node` · `hello-python` · `hello-go` · `cron-example` · `function-node` · `function-python` · `function-go` · `function-node24` · `function-python313` · `event-worker` · `queue-worker` · `s3-uploader` · `slack-bot` · `rest-api-postgres` · `cron-worker` · `webhook-receiver` · `ai-chat` · `secret-reload-node` · `customer-platform` |
| `--dockerfile` | build with the supplied Dockerfile inside --tarball |  |
| `--runtime <RUNTIME>` | function runtime | one of `node22` · `python312` · `go124` · `go124-alpine` · `node24` · `python313` |
| `--handler <HANDLER>` | function handler |  |
| `--name <SLUG>` | app name (default: selected source directory, or current directory) |  |
| `--profile <PROFILE>` | named app resource profile | one of `micro` · `small` · `medium` · `large` · `xlarge` |
| `--vcpu <N>` | assert the plan guest vCPU shape (omit to use the plan default) |  |
| `--execution-mode <request|service|worker|job>` | app lifecycle mode |  |
| `--restart-policy <no|on-failure|always|unless-stopped>` | app restart policy |  |
| `--startup-deadline-s <SECONDS>` | maximum startup seconds (0 uses plan default) |  |
| `--max-retries <N>` | maximum restart attempts (0 uses plan default) |  |
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
| `--exclude <SLUGS>` | omit workloads (comma-separated slugs; cannot combine with --only) |  |
| `--show-affected` | show workloads that deploy, stay unchanged, or are removed |  |
| `--persist-exclude` | save --exclude slugs for future project deploys |  |
| `--project-slug <SLUG>` | kebab slug for the project (one-key provision) |  |
| `--canary-preset <PRESET>` | canary ladder preset | one of `none` · `slow` · `balanced` · `aggressive` · `1-10-50-100` · `custom` |
| `--canary-stages <STAGES>` | custom percent@duration canary stages |  |
| `--safe` | deploy with the balanced health-gated rollout and first-wake 5xx rollback |  |
| `--require-authn` | require bearer auth on every request |  |
| `--no-require-authn` | drop the token requirement |  |
| `--platform-tenant-required` | require verified customer identity on selected apps (Hobby and above) |  |
| `--no-platform-tenant-required` | allow selected apps to receive traffic without customer identity |  |
| `--app-protocol <PROTOCOL>` | wire protocol selector | one of `http1` · `http2` · `grpc` |
| `--traffic-percent <PERCENT>` | deployment traffic split weight (0-100) |  |
| `--no-traffic` | stage with 0% production traffic and print the preview URL |  |
| `--rollback-on-5xx` | roll back after repeated first-wake 5xx responses |  |
| `--healthcheck-path <PATH>` | startup HTTP readiness path |  |
| `--healthcheck-grpc` | use standard gRPC health for startup readiness |  |
| `--healthcheck-grpc-service <SERVICE>` | service name for --healthcheck-grpc; empty checks overall health |  |
| `--disable-startup-cpu-boost` | disable temporary CPU boost during VM startup |  |
| `--no-triggers` | skip gregale.yaml trigger and async-route changes |  |
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

Examples:

```sh
gregale deploy --plan
gregale deploy --source=head --name my-api
gregale deploy --path packages/api --source=worktree
```


## domains

Manage custom domains

`gregale domains [<subcommand>]`

### domains list

List custom domain bindings

### domains add

Bind a custom domain to an app or project environment

| Flag | Meaning | |
|---|---|---|
| `--domain <DOMAIN>` | domain to attach (required) | required |
| `--app <SLUG>` | app slug to attach to (required) | required |
| `--environment <SLUG>` | project environment to route this domain to |  |

### domains rm

Remove a custom domain binding

### domains set-default

Set a verified domain as the app default

### domains verify

Check DNS and certificate verification status; exits nonzero while pending

### domains show

Show a domain&#39;s cert details

### domains status

Show durable TLS status for all domains

### domains doctor

5-check readiness report; exits nonzero when unhealthy, including in JSON mode


## dev

Sync local changes to a developer environment

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

Examples:

```sh
gregale dev --once
gregale dev --path ./api --once
```

### dev status

show developer-environment quota usage

### dev bridge

run an HTTP service locally in a remote development environment

| Flag | Meaning | |
|---|---|---|
| `--environment <ENV>` | named development environment |  |
| `--local-port <PORT>` | local HTTP service port |  |
| `--dependencies <APPS>` | comma-separated remote dependency apps |  |
| `--entrypoint <APP>` | remote frontend for the session URL |  |
| `--inspect` | inspect recent requests in a local browser |  |
| `--replay-webhook <INVOCATION>` | copy one provider-verified webhook receipt locally |  |
| `--ready-path <PATH>` | check a local HTTP readiness path before attaching |  |
| `--bind-env <BINDING>` | map ENV_KEY=dependency to its local proxy URL; repeatable |  |

#### dev bridge list

list active account development bridges

`gregale dev bridge list`

#### dev bridge status

inspect a session and recent request activity

`gregale dev bridge status SESSION`

#### dev bridge revoke

revoke a session and disconnect its laptop

`gregale dev bridge revoke SESSION`

#### dev bridge doctor

check admission, relay and local listener using a temporary session

`gregale dev bridge doctor APP [--environment <ENV>] [--local-port <PORT>] [--entrypoint <APP>]`

| Flag | Meaning | |
|---|---|---|
| `--environment <ENV>` | named development environment |  |
| `--local-port <PORT>` | local HTTP service port |  |
| `--entrypoint <APP>` | remote frontend |  |

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


## diff

Compare two named environments in the linked project

`gregale diff <from-environment> <to-environment> [--project <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--project <SLUG>` | project slug (defaults to linked project) |  |


## test

Run scenario suites and bounded local HTTP load tests

`gregale test [<subcommand>] [--scenario <NAME>] [--suite <NAME>] [--fail-fast] [--validate] [--preflight] [--engine <ENGINE>] [--base-url <URL>] [--data <PATH>] [--load] [--vus <N>] [--rate <N>] [--iterations <N>] [--duration <DURATION>] [--pacing <DURATION>] [--progress] [--baseline <PATH>] [--profile <PROFILE>] [--repeat <N>] [--max-workload-minutes <N>] [--manifest <PATH>] [--report <PATH>] [--junit <PATH>] [--html <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--scenario <NAME>` | scenario declared in gregale-test.yaml |  |
| `--suite <NAME>` | named suite; run members sequentially in declaration order |  |
| `--fail-fast` | stop after the first failed run and cleanup; report remaining runs as skipped |  |
| `--validate` | validate local scenario sources without a platform login |  |
| `--preflight` | check account entitlements and developer app capacity |  |
| `--engine <ENGINE>` | execution engine (default real-vm) | one of `real-vm` · `local` · `simulated` |
| `--base-url <URL>` | HTTP loopback origin (optional with local.command) |  |
| `--data <PATH>` | JSON or CSV case data for the local engine |  |
| `--load` | repeat native HTTP journeys concurrently with the local engine |  |
| `--vus <N>` | concurrent users or arrival-rate concurrency cap (1..50, default 1) |  |
| `--rate <N>` | target journeys per second with --load and duration (1..1000) |  |
| `--iterations <N>` | total journeys for --load (1..10000, default 100) |  |
| `--duration <DURATION>` | schedule journeys for this duration with --load (1s..5m) |  |
| `--pacing <DURATION>` | pause between each user&#39;s load journeys (0s..1m) |  |
| `--progress` | print live load progress to stderr |  |
| `--baseline <PATH>` | compare local load with a saved successful JSON report; apply regression budgets |  |
| `--profile <PROFILE>` | required lifecycle (default all) | one of `warm` · `cold` · `restored` · `all` |
| `--repeat <N>` | runs per lifecycle profile or local case (1..20) |  |
| `--max-workload-minutes <N>` | abort if the estimated VM workload-minute ceiling exceeds N |  |
| `--manifest <PATH>` | scenario manifest path |  |
| `--report <PATH>` | write a JSON report |  |
| `--junit <PATH>` | write a JUnit XML report |  |
| `--html <PATH>` | write a standalone HTML report |  |

Examples:

```sh
gregale test init --from openapi.yaml --project my-api
gregale test import --from collection.json --project my-api
gregale test --validate
gregale test --suite smoke --engine local --fail-fast --junit test-results.xml
gregale test --suite smoke --engine local --report test-results.json --junit test-results.xml --html test-results.html
gregale test compare baseline.json current.json --budget test-budget.yaml --html comparison.html
gregale test --suite regression --validate
gregale test --scenario customer-export --preflight
gregale test --scenario customer-export --profile restored --repeat 3 --max-workload-minutes 135 --report test-results.json --junit test-results.xml
gregale test --scenario api-smoke --engine local
gregale test --scenario api-smoke --engine local --load --baseline baseline.json --report current.json --junit current.xml
gregale test --scenario customer-export --engine local --base-url http://localhost:3000 --data cases.json
gregale test --scenario api-smoke --engine local --base-url http://localhost:3000 --load --vus 5 --duration 30s --pacing 100ms --progress
gregale test --scenario api-smoke --engine local --load --rate 20 --duration 30s --vus 10 --progress
gregale test --scenario customer-export --engine simulated
```

### test init

Create public GET smoke checks from a local OpenAPI document

| Flag | Meaning | |
|---|---|---|
| `--from <PATH>` | local OpenAPI 3.0 or 3.1 document | required |
| `--project <SLUG>` | Gregale project slug | required |
| `--source <DIR>` | application source directory |  |
| `--scenario <NAME>` | scenario name |  |
| `--output <PATH>` | new manifest path |  |

Examples:

```sh
gregale test init --from openapi.yaml --project my-api --source .
```

### test compare

Compare saved JSON run reports, enforce budgets, and write summaries

`gregale test compare <before.json> <after.json> [--budget <PATH>] [--html <PATH>] [--markdown <PATH>] [--github-summary]`

| Flag | Meaning | |
|---|---|---|
| `--budget <PATH>` | apply comparison budgets from a YAML file; fail if a check fails or is inconclusive |  |
| `--html <PATH>` | write a standalone HTML comparison report |  |
| `--markdown <PATH>` | write a Markdown comparison summary |  |
| `--github-summary` | append a Markdown summary to GITHUB_STEP_SUMMARY |  |

Examples:

```sh
gregale test compare baseline.json current.json
gregale test compare baseline.json current.json --budget test-budget.yaml --markdown comparison.md
gregale test compare baseline.json current.json --budget test-budget.yaml --github-summary
```

### test ci

Set up GitHub Actions for scenario tests

#### test ci init

Generate GitHub Actions for local, simulated, or real-VM scenario tests

`gregale test ci init [--manifest <PATH>] [--suite <NAME>] [--engine <ENGINE>] [--load] [--repeat <N>] [--profiles <LIST>] [--max-workload-minutes <N>] [--environment <NAME>] [--branch <NAME>] [--baseline-max-age-days <N>] [--workflow <PATH>] [--budget <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--manifest <PATH>` | scenario manifest path |  |
| `--suite <NAME>` | suite from the manifest (inferred when exactly one exists) |  |
| `--engine <ENGINE>` | override suite engine for CI | one of `local` · `simulated` · `real-vm` |
| `--load` | include local HTTP load execution |  |
| `--repeat <N>` | runs per scenario (default 1; default 3 with --load) |  |
| `--profiles <LIST>` | real-VM lifecycle profiles (all or comma-separated) |  |
| `--max-workload-minutes <N>` | required real-VM workload-minute limit per profile job |  |
| `--environment <NAME>` | GitHub environment containing real-VM credentials |  |
| `--branch <NAME>` | baseline branch for local and simulated workflows |  |
| `--baseline-max-age-days <N>` | maximum baseline artifact age (default 30; 0 disables age check) |  |
| `--workflow <PATH>` | new GitHub Actions workflow path |  |
| `--budget <PATH>` | comparison budget path for local and simulated workflows |  |

Examples:

```sh
gregale test ci init --manifest gregale-test.yaml --suite smoke
gregale test ci init --manifest gregale-test.yaml --suite smoke --baseline-max-age-days 14
gregale test ci init --manifest tests/scenario-acceptance/gregale-test.yaml --suite real-vm --engine real-vm --profiles warm,cold,restored --max-workload-minutes 135
```

### test import

Create draft native requests from a local Postman Collection v2.1 export

| Flag | Meaning | |
|---|---|---|
| `--from <PATH>` | local Postman Collection v2.1 JSON export | required |
| `--project <SLUG>` | Gregale project slug | required |
| `--source <DIR>` | command working directory |  |
| `--scenario <NAME>` | scenario name (default api-collection) |  |
| `--output <PATH>` | new manifest path |  |
| `--requests-only` | explicitly omit Postman scripts; add their assertions and setup as native steps |  |
| `--status <CODE>` | fallback expected status without a unique saved response (default 200) |  |

Examples:

```sh
gregale test import --from collection.json --project my-api
gregale test import --from collection.json --project my-api --requests-only --status 202
```


## preview

Manage preview environments for pull requests

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

Examples:

```sh
gregale preview create --app my-api --repo acme/my-api --ref feature/cache --pr-number 42 --open
gregale preview create --app my-api --repo acme/my-api --ref feature/cache --pr-number 42 --no-wait
```

### preview list

List pull-request and developer previews (defaults to the linked app)

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | parent app slug |  |

Examples:

```sh
gregale preview list --app my-api
gregale preview list
```

### preview show

Inspect a preview and its latest deployment

`gregale preview show <preview-slug>`

Examples:

```sh
gregale preview show pr-42-my-api
```

### preview wait

Wait for a preview deployment to become ready

`gregale preview wait <preview-slug> [--progress] [--open] [--timeout <SECONDS>]`

| Flag | Meaning | |
|---|---|---|
| `--progress` | print deployment transitions while waiting |  |
| `--open` | open the preview URL after it becomes ready |  |
| `--timeout <SECONDS>` | maximum seconds to wait |  |

Examples:

```sh
gregale preview wait pr-42-my-api --progress --open
```

### preview destroy

Tear down a preview app

`gregale preview destroy <preview-slug>`

Examples:

```sh
gregale preview destroy pr-42-my-api
```


## flags

Release application behavior to selected customers

`gregale flags [<subcommand>] --project <slug> [--environment <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--project <slug>` | project slug | required |
| `--environment <slug>` | named environment (default production) |  |

### flags get

Read current flag configuration

### flags apply

Publish a versioned configuration

| Flag | Meaning | |
|---|---|---|
| `--file <path>` | JSON update bundle | required |

### flags history

List immutable configuration versions

| Flag | Meaning | |
|---|---|---|
| `--before-version <number>` | page before this version |  |

### flags inspect

Explain a customer&#39;s decision

| Flag | Meaning | |
|---|---|---|
| `--key <key>` | flag key | required |
| `--customer-id <UUID>` | customer UUID |  |
| `--version <number>` | historical configuration version |  |

### flags rollback

Publish an earlier configuration

| Flag | Meaning | |
|---|---|---|
| `--version <number>` | version to restore | required |
| `--expected-version <number>` | current version | required |

### flags requests

Inspect request evidence by flag value

| Flag | Meaning | |
|---|---|---|
| `--key <key>` | flag key | required |
| `--customer-id <UUID>` | customer UUID |  |
| `--value <bool>` | true or false |  |
| `--used <bool>` | true or false exposure |  |
| `--since <duration>` | lookback (default 24h) |  |
| `--cursor <cursor>` | next-page cursor |  |


## platform-tenants

Manage one customer across app consumers and tenant hostnames

`gregale platform-tenants [<subcommand>]`

### platform-tenants list

List platform customers

### platform-tenants add

Register a customer by external reference

### platform-tenants apply

Preview or apply an onboarding bundle

| Flag | Meaning | |
|---|---|---|
| `--file <path>` | JSON onboarding bundle |  |
| `--dry-run` | Preview without changes |  |

### platform-tenants credentials-list

List metadata for a customer&#39;s cross-app keys

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | platform tenant UUID | required |
| `--limit <number>` | page size (1..100) |  |
| `--offset <number>` | page offset |  |

### platform-tenants credentials-apply

Preview or apply hash-only key issuance and rotation

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | platform tenant UUID | required |
| `--file <path>` | hash-only credential bundle | required |
| `--dry-run` | preview without changes |  |

### platform-tenants info

Show linked consumers and surfaces

### platform-tenants activation

Show or wait for customer hostname activation

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | platform tenant UUID | required |
| `--wait` | poll until all surfaces are ready |  |
| `--timeout <duration>` | maximum wait (default 10m) |  |

### platform-tenants link-consumer

Attach an existing app consumer

### platform-tenants link-surface

Attach an existing tenant surface

### platform-tenants usage

Show cross-app raw usage

### platform-tenants suspend

Stop linked credentials and hostnames

### platform-tenants resume

Restore linked credentials and hostnames


## edge-rules

Per-app edge rules (edge-rules list|trace|create|get|update|rm --app &lt;slug&gt;)

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

### edge-rules trace

Simulate composed edge-rule outcomes and budget, throttle, retry, circuit-breaker, and async-route policy; --config loads reusable JSON scenarios (see edge-rule-trace docs)

| Flag | Meaning | |
|---|---|---|
| `--config <file|->` | load a versioned JSON scenario (headers array; body or body_base64); - reads stdin and is exclusive with request flags |  |
| `--app <slug>` | app slug (required unless --config is used) |  |
| `--url <URL>` | absolute HTTP(S) request URL (required unless --config is used) |  |
| `--method <method>` | request method (default GET) |  |
| `--client-ip <IP>` | simulated client IP for kind=ip rules |  |
| `--country <CC>` | simulated ISO alpha-2 country for kind=geo rules |  |
| `--header <Name:Value>` | simulated request header; repeat for multiple values |  |
| `--body-file <path|->` | request body file or - for stdin (max 1 MiB; contents are withheld) |  |

### edge-rules create

Add an edge rule

| Flag | Meaning | |
|---|---|---|
| `--on-success-webhook <ID>` | success webhook subscription; repeat when updating async policy |  |
| `--on-failure-webhook <ID>` | failure webhook subscription; repeat when updating async policy |  |
| `--async-max-attempts <N>` | total attempts (0 = plan default; capped by plan) |  |
| `--async-retry-base-seconds <N>` | exponential retry base delay |  |
| `--async-retry-max-seconds <N>` | maximum exponential retry delay |  |
| `--async-retry-jitter-seconds <N>` | retry jitter fraction (0..1) |  |
| `--async-max-age-seconds <N>` | invocation lifetime from acceptance (0 = plan default; capped by plan) |  |
| `--validate-schema <JSON|@FILE|->` | JSON Schema (inline JSON, @file, or - for stdin; max 64 KiB) |  |
| `--validate-mode <MODE>` | invalid-request behavior (default block) | one of `block` · `observe` · `warn` |
| `--validate-content-type <TYPE>` | accepted application media type (repeat; e.g. application/json) |  |
| `--validate-max-body-bytes <N>` | optional body cap in bytes (0 = plan default) |  |
| `--validate-apply-while-streaming` | also validate streaming requests |  |
| `--validate-reject-unknown-fields` | reject fields not declared by the schema |  |

Examples:

```sh
gregale edge-rules create --app my-api --kind validate --match-host api.example.com --validate-schema @schema.json --validate-content-type application/json --validate-mode block
cat schema.json | gregale edge-rules create --app my-api --kind validate --match-host api.example.com --validate-schema -
```

### edge-rules get

Show one edge rule

### edge-rules update

Update one edge rule

| Flag | Meaning | |
|---|---|---|
| `--on-success-webhook <ID>` | success webhook subscription |  |
| `--on-failure-webhook <ID>` | failure webhook subscription |  |
| `--async-max-attempts <N>` | total attempts (0 = plan default; capped by plan) |  |
| `--async-retry-base-seconds <N>` | exponential retry base delay |  |
| `--async-retry-max-seconds <N>` | maximum exponential retry delay |  |
| `--async-retry-jitter-seconds <N>` | retry jitter fraction (0..1) |  |
| `--async-max-age-seconds <N>` | invocation lifetime from acceptance (0 = plan default; capped by plan) |  |
| `--validate-schema <JSON|@FILE|->` | replacement schema; required when updating action fields (inline JSON, @file, or -; max 64 KiB) |  |
| `--validate-mode <MODE>` | invalid-request behavior | one of `block` · `observe` · `warn` |
| `--validate-content-type <TYPE>` | accepted application media type (repeat; e.g. application/json) |  |
| `--validate-max-body-bytes <N>` | body cap in bytes (0 = plan default) |  |
| `--validate-apply-while-streaming` | also validate streaming requests |  |
| `--validate-reject-unknown-fields` | reject fields not declared by the schema |  |

Examples:

```sh
gregale edge-rules update RULE_ID --kind validate --validate-schema @schema.json --validate-mode block
gregale edge-rules update RULE_ID --kind validate --validate-mode observe
```

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

Clone project environments or manage app runtime env/secrets

`gregale env [<subcommand>] [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked context) |  |

### env create

Clone a project environment with isolated managed data by default

| Flag | Meaning | |
|---|---|---|
| `--from <ENV>` | source environment | required |
| `--project <SLUG>` | project slug (defaults to linked project) |  |
| `--protected` | protect the new environment |  |
| `--share-resources` | use source managed data with fresh target credentials instead of isolating it |  |

### env pull

Pull sealed-secret keys to a .env skeleton (values blank)

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked context) |  |
| `--scope <SCOPE>` | env scope (defaults to linked project environment) |  |

Examples:

```sh
gregale env pull --app my-api
gregale env pull --app my-api --scope staging
```

### env push

Push KEY=VALUE pairs to sealed secrets (use --restart to apply now)

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked context) |  |
| `--scope <SCOPE>` | env scope (defaults to linked project environment) |  |
| `--from-stdin` | read KEY=VALUE pairs from stdin |  |
| `--restart` | restart app after applying changes (otherwise changes apply on next cold wake) |  |

Examples:

```sh
printf 'LOG_LEVEL=info\n' | gregale env push --app my-api --from-stdin
gregale env push --app my-api --restart
```

### env diff

Render the env-diff matrix (presence / value-equality across scopes)

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked context) |  |

Examples:

```sh
gregale env diff --app my-api
gregale env diff --app my-api --json
```


## init

Scaffold a project from a built-in template

`gregale init --template <NAME> --path <DIR> [--deploy] [--name <SLUG>] [--secrets-file <PATH>] [--list]`

| Flag | Meaning | |
|---|---|---|
| `--template <NAME>` | template name | required; one of `hello-node` · `hello-python` · `hello-go` · `cron-example` · `function-node` · `function-python` · `function-go` · `function-node24` · `function-python313` · `event-worker` · `queue-worker` · `s3-uploader` · `slack-bot` · `rest-api-postgres` · `cron-worker` · `webhook-receiver` · `ai-chat` · `secret-reload-node` · `customer-platform` |
| `--path <DIR>` | target directory | required |
| `--deploy` | deploy after scaffolding |  |
| `--name <SLUG>` | app slug used with --deploy |  |
| `--secrets-file <PATH>` | seal KEY=VALUE pairs before the first deployment (requires --deploy) |  |
| `--list` | list available templates |  |

Examples:

```sh
gregale init --list
gregale init --template hello-node --path ./my-api
```


## inspect

Explain an app from its runtime, deployment, API, data, scaling, and release signals (slug defaults to linked context)

`gregale inspect [<slug>] [--upstreams] [--scope <scope>] [--errors]`

| Flag | Meaning | |
|---|---|---|
| `--upstreams` | List data upstreams captured for this app |  |
| `--scope <scope>` | filter by scope (defaults to linked project environment; used with --upstreams) |  |
| `--errors` | show the latest failed deployment&#39;s persisted error explanation |  |

Examples:

```sh
gregale inspect my-api
gregale inspect my-api --upstreams
```


## invoke

Functional smoke test (invoke [--async] &lt;slug&gt; [--payload J|@file|-]; slug defaults to linked context)

`gregale invoke [<slug>] [--async] [--payload <J|@file|->] [--on-success-webhook <ID>] [--on-failure-webhook <ID>] [--work-policy <NAME>] [--work-key <JSON>] [--work-fairness-key <JSON>]`

| Flag | Meaning | |
|---|---|---|
| `--async` | return immediately with status_url |  |
| `--payload <J|@file|->` | JSON payload (inline \| @file \| -) |  |
| `--on-success-webhook <ID>` | app webhook id for completed invocation callbacks |  |
| `--on-failure-webhook <ID>` | app webhook id for failed or dead-lettered callbacks |  |
| `--work-policy <NAME>` | named app work policy for async invocation |  |
| `--work-key <JSON>` | JSON scalar application key for async invocation |  |
| `--work-fairness-key <JSON>` | JSON scalar fairness group for async invocation |  |


## run

Run untrusted code in an isolated disposable microVM

`gregale run [--profile <P>] [--runtime <R>] [--source <CODE>] [--file <PATH>] [--input <J|@file|->] [--timeout-ms <N>] [--memory-mb <N>] [--cpu-millicores <N>] [--ephemeral-disk-mb <N>] [--max-output-bytes <N>] [--output-file <PATH>] [--output-dir <DIR>] [--wait] [--watch] [--poll-interval <D>] [--wait-timeout <D>]`

| Flag | Meaning | |
|---|---|---|
| `--profile <P>` | preinstalled dependencies (data requires python313) | one of `standard` · `python-data-v1` |
| `--runtime <R>` | runtime (node22\|node24\|python312\|python313) | one of `node22` · `node24` · `python312` · `python313` |
| `--source <CODE>` | inline source code |  |
| `--file <PATH>` | source file (regular file only) |  |
| `--input <J|@file|->` | JSON input (inline \| @file \| -) |  |
| `--timeout-ms <N>` | execution timeout |  |
| `--memory-mb <N>` | memory limit |  |
| `--cpu-millicores <N>` | CPU limit |  |
| `--ephemeral-disk-mb <N>` | ephemeral scratch size |  |
| `--max-output-bytes <N>` | combined output cap |  |
| `--output-file <PATH>` | output file below context.output_dir to export (repeatable) |  |
| `--output-dir <DIR>` | save artifacts locally; implies --wait |  |
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

### runs artifacts

Save output artifacts from a successful run

`gregale runs artifacts <id> [--output-dir <DIR>]`

| Flag | Meaning | |
|---|---|---|
| `--output-dir <DIR>` | local destination (required) |  |

### runs get

Show one run

### runs status

Show one run (alias for get)

### runs cancel

Cancel one run


## invocations

Per-account invocation ledger (invocations list|get|wait &lt;id&gt;)

`gregale invocations [<subcommand>] <id>`

### invocations list

List invocations

### invocations get

Show one invocation

### invocations wait

Wait for one invocation to finish

| Flag | Meaning | |
|---|---|---|
| `--timeout <D>` | stop waiting after this duration (0 waits indefinitely) |  |
| `--interval <D>` | time between status checks (default 1s) |  |


## issues

Group failures and track ownership and release-aware resolution

`gregale issues [<subcommand>] [--app <SLUG>] [--deployment <UUID>] [--state <STATE>] [--environment <ENV>] [--cursor <CURSOR>] [--release-cursor <CURSOR>] [--activity-cursor <CURSOR>] [--assignee <UUID>] [--since <RFC3339>] [--until <RFC3339>] [--name <NAME>] [--expires-in <D>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug |  |
| `--deployment <UUID>` | fixed or token-bound deployment |  |
| `--state <STATE>` | filter issue state |  |
| `--environment <ENV>` | environment filter |  |
| `--cursor <CURSOR>` | issue-list or occurrence cursor |  |
| `--release-cursor <CURSOR>` | release history cursor |  |
| `--activity-cursor <CURSOR>` | activity history cursor |  |
| `--assignee <UUID>` | owner account |  |
| `--since <RFC3339>` | impact window start |  |
| `--until <RFC3339>` | ignore until |  |
| `--name <NAME>` | credential name |  |
| `--expires-in <D>` | credential lifetime |  |

Examples:

```sh
gregale issues list --app my-api
gregale issues get ISSUE_ID --app my-api
gregale issues resolve ISSUE_ID --app my-api --deployment DEPLOYMENT_ID
```

### issues list

List grouped issues

### issues get

Read evidence and release history

### issues assign

Assign an issue to an account

### issues resolve

Resolve in a deployment

### issues reopen

Reopen an issue

### issues ignore

Ignore until a timestamp

### issues tokens

List ingest credentials

### issues create-token

Create a deployment-bound ingest credential

### issues revoke-token

Revoke an ingest credential


## debug

Inspect production requests and regressions

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

`gregale keys add <label>`

### keys rm

Revoke an API key

### keys rotate

Rotate an API key

### keys grace-window

Set the rotation grace window


## login

Authenticate this machine

`gregale login [--token <TOKEN>] [--token-stdin]`

| Flag | Meaning | |
|---|---|---|
| `--token <TOKEN>` | use a pre-minted token (CI) |  |
| `--token-stdin` | read a pre-minted token from stdin (CI) |  |

Examples:

```sh
gregale login
printf '%s' "$GREGALE_TOKEN" | gregale login --token-stdin
```


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

Query runtime logs and HTTP request events

`gregale logs [<slug>] [--follow] [--deployment <ID>] [--release <ID|vN>] [--source <SOURCE>] [--grep <SUBSTR>] [--since <15m|3d|RFC3339>] [--level <LEVEL>] [--status <100..599>] [--route <PATH>] [--request <ID>] [--trace <TRACE_ID>] [--limit <N>] [--all] [--explain] [--archive] [--instance <ID>] [--date <YYYY-MM-DD>]`

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
| `--trace <TRACE_ID>` | show HTTP access logs correlated with a W3C trace id |  |
| `--limit <N>` | HTTP request page size (1..200) |  |
| `--all` | read every retained HTTP request page |  |
| `--explain` | summarize the last failure and common error patterns |  |
| `--archive` | read durable logs for one instance and UTC day |  |
| `--instance <ID>` | instance id for --archive |  |
| `--date <YYYY-MM-DD>` | UTC day for --archive |  |

Examples:

```sh
gregale logs my-api --follow
gregale logs my-api --since 1h --level error
```


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
| `--work-policy <NAME>` | named work policy for an unnamed queue |  |
| `--work-key <JSON>` | JSON scalar identifying related work |  |
| `--work-fairness-key <JSON>` | JSON scalar shared by related work keys |  |

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

Examples:

```sh
gregale rollback my-api
gregale rollback my-api --to v41
```


## projects

Inspect and recover repository projects

`gregale projects [<subcommand>] <project-slug>`

### projects list

List projects in this account

### projects info

Show a project and its workloads

### projects environments

Manage project environments (list|create|protect|unprotect|inspect|release-sets|releases|qualify|preflight|history|config [set]|routes set|diff|preview|promote|status|rollback); qualify binds probes to release, config, and secret revisions

#### projects environments list

List environments

`gregale projects environments list`

#### projects environments create

Create or clone an environment

`gregale projects environments create`

#### projects environments protect

Protect an environment

`gregale projects environments protect`

#### projects environments unprotect

Remove environment protection

`gregale projects environments unprotect`

#### projects environments inspect

Inspect the active graph and environment deployments

`gregale projects environments inspect`

#### projects environments release-sets

List release graphs and their retention deadlines

`gregale projects environments release-sets [--before <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--before <CURSOR>` | page cursor |  |
| `--limit <N>` | page size |  |

#### projects environments releases

List live workload deployments

`gregale projects environments releases`

#### projects environments qualify

Run health and smoke GET probes against exact active release-set deployments

`gregale projects environments qualify <project-slug> <environment-slug> --profile <FILE>`

| Flag | Meaning | |
|---|---|---|
| `--profile <FILE>` | YAML probe profile defining every release-set workload | required |

#### projects environments preflight

Qualify the source release and check promotion readiness for CI

`gregale projects environments preflight <project-slug> --from <ENV> --to <ENV> --profile <FILE> [--sync-config]`

| Flag | Meaning | |
|---|---|---|
| `--from <ENV>` | source environment to qualify and promote | required |
| `--to <ENV>` | target environment to check | required |
| `--profile <FILE>` | YAML probe profile defining every source workload | required |
| `--sync-config` | include non-secret source config in the promotion preview |  |

#### projects environments history

List environment promotions

`gregale projects environments history`

#### projects environments config

Manage environment configuration

`gregale projects environments config`

#### projects environments routes

Manage environment routes

`gregale projects environments routes`

#### projects environments policies

Manage environment policies

`gregale projects environments policies`

#### projects environments diff

Compare environments

`gregale projects environments diff`

#### projects environments preview

Plan a promotion

`gregale projects environments preview --from <ENV> --to <ENV> [--sync-config]`

| Flag | Meaning | |
|---|---|---|
| `--from <ENV>` | source environment | required |
| `--to <ENV>` | target environment | required |
| `--sync-config` | include non-secret source config in the promotion preview |  |

#### projects environments promote

Promote workloads

`gregale projects environments promote --from <ENV> --to <ENV> [--sync-config] [--yes] [--idempotency-key <KEY>] [--wait] [--progress] [--timeout <SECONDS>]`

| Flag | Meaning | |
|---|---|---|
| `--from <ENV>` | source environment | required |
| `--to <ENV>` | target environment | required |
| `--sync-config` | copy source non-secret environment configuration to the target |  |
| `--yes` | confirm the promotion |  |
| `--idempotency-key <KEY>` | stable key for retrying this promotion |  |
| `--wait` | wait for the promotion to reach a terminal status |  |
| `--progress` | print promotion transitions while waiting (human output only) |  |
| `--timeout <SECONDS>` | maximum seconds to wait for promotion completion |  |

#### projects environments status

Inspect a promotion

`gregale projects environments status`

#### projects environments rollback

Roll back a promotion

`gregale projects environments rollback`

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
| `--exclude <SLUGS>` | omit workloads (comma-separated slugs; cannot combine with --only) |  |
| `--show-affected` | show workloads that deploy or stay unchanged |  |
| `--explain` | show detector provenance and skipped/merged decisions |  |
| `--persist-exclude` | save --exclude slugs for future project deploys |  |


## secrets

Manage env secrets (secrets list|set|unset|list-all|audit|rotate)

`gregale secrets [<subcommand>]`

### secrets list

List sealed secrets

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--scope <SCOPE|__all__>` | env scope filter (defaults to linked project environment) |  |
| `--class <CLASS>` | filter by snapshot-retention class | one of `persistent` · `ephemeral` |
| `--older-than <DURATION>` | filter to secrets not updated within a duration (for example 90d or 2160h); unknown timestamps are excluded |  |

Examples:

```sh
gregale secrets list --app my-api
gregale secrets list --app my-api --scope __all__
gregale secrets list --app my-api --class ephemeral
gregale secrets list --app my-api --older-than 90d
```

### secrets set

Set a sealed secret; ephemeral values disable VM snapshots for the scope

`gregale secrets set [<KEY=VALUE>...] --app <slug> [--from-stdin] [--scope <SCOPE>] [--class <CLASS>] [--restart]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--from-stdin` | read KEY=VALUE pairs from stdin |  |
| `--scope <SCOPE>` | env scope to write (defaults to linked project environment) |  |
| `--class <CLASS>` | retention: persistent by default; ephemeral disables init/warm captures and forces cold boots; omission preserves an existing class | one of `persistent` · `ephemeral` |
| `--restart` | restart the app and apply updated secrets now |  |

Examples:

```sh
gregale secrets set --app my-api DATABASE_URL="$DATABASE_URL"
printf '%s\n' "DATABASE_URL=$DATABASE_URL" | gregale secrets set --app my-api --from-stdin
gregale secrets set --app my-api DATABASE_URL="$DATABASE_URL" --restart
gregale secrets set --app my-api SESSION_TOKEN="$SESSION_TOKEN" --class ephemeral
```

### secrets unset

Remove a sealed secret

`gregale secrets unset <KEY> --app <slug> [--scope <SCOPE>] [--wait-for-ack] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--scope <SCOPE>` | env scope to delete from (defaults to linked project environment) |  |
| `--wait-for-ack` | wait until every active authorized runtime confirms it removed the secret |  |
| `--timeout <DURATION>` | maximum time to wait for runtime acknowledgements |  |

Examples:

```sh
gregale secrets unset --app my-api OLD_API_KEY
gregale secrets unset --app my-api OLD_API_KEY --scope staging
gregale secrets unset --app my-api OLD_API_KEY --wait-for-ack
```

### secrets list-all

List every secret across apps

| Flag | Meaning | |
|---|---|---|
| `--before <slug|key>` | pagination cursor from a previous call&#39;s next_before |  |
| `--limit <N>` | page size (1..200; server caps at 200) |  |
| `--class <CLASS>` | filter this page by snapshot-retention class | one of `persistent` · `ephemeral` |
| `--older-than <DURATION>` | filter this page to secrets not updated within a duration; unknown timestamps are excluded |  |

Examples:

```sh
gregale secrets list-all --class ephemeral
gregale secrets list-all --older-than 90d
```

### secrets audit

Audit secret update age and report unknown timestamps without exposing values

| Flag | Meaning | |
|---|---|---|
| `--older-than <DURATION>` | required threshold based on when Gregale last updated the value, not provider rotation time (for example 90d or 2160h) | required |
| `--fail-on-stale` | exit non-zero when any secret exceeds the age threshold |  |

Examples:

```sh
gregale secrets audit --older-than 90d
gregale secrets audit --older-than 90d --fail-on-stale --json
```

### secrets rotate

Rotate a secret and optionally wait for runtime application

`gregale secrets rotate [<KEY=VALUE>] --app <slug> [--from-stdin] [--scope <SCOPE>] [--restart] [--wait-for-ack] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--from-stdin` | read one KEY=VALUE pair from stdin |  |
| `--scope <SCOPE>` | env scope to rotate (defaults to linked project environment) |  |
| `--restart` | restart the app and apply the rotated secret now |  |
| `--wait-for-ack` | wait until every active authorized runtime confirms it applied the secret (works with --restart) |  |
| `--timeout <DURATION>` | maximum time to wait for restart and application acknowledgements |  |

Examples:

```sh
printf '%s\n' "DATABASE_URL=$DATABASE_URL" | gregale secrets rotate --app my-api --from-stdin --restart --wait-for-ack
printf '%s\n' "DATABASE_URL=$DATABASE_URL" | gregale secrets rotate --app my-api --from-stdin --scope production --restart --wait-for-ack --timeout 5m
```


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

Manage traffic mirroring and sanitized replay (Pro/Scale only). Rules default to 5% and mirror only safe methods; bodies over 64 KiB are skipped, and raw bodies are never retained.

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
| `--percent <N>` | fan-out percent in [0, 100]; defaults to a 5% sample |  |
| `--include-body` | compare response values using hashes; raw response bodies are never retained |  |
| `--allow-unsafe-methods` | also mirror POST, PUT, PATCH, and DELETE; these can cause side effects |  |
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
| `--allow-unsafe-methods` | also mirror POST, PUT, PATCH, and DELETE |  |
| `--safe-methods-only` | skip POST, PUT, PATCH, and DELETE |  |
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

Purge cached responses: cache purge &lt;slug&gt; [--path GLOB | --tag TAG]

| Flag | Meaning | |
|---|---|---|
| `--path <GLOB>` | optional normalized request path glob |  |
| `--tag <TAG>` | optional cache tag |  |


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

Manage app and account release webhooks (webhooks account &lt;verb&gt;)

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

### webhooks account

Manage one release receiver across all account apps


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
