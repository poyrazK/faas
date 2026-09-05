# gregale CLI reference

Generated from the CLI's command manifest by `gregale man --markdown`. Do not edit by hand.

| Command | What it does |
|---|---|
| [`account`](#account) | Manage the local account (account export\|delete\|restore\|status\|dpa\|slo) |
| [`admin`](#admin) | Operator-only billing ops (admin credit\|refund\|consume-credits) |
| [`alerts`](#alerts) | Per-app alert rules (alerts list\|add\|info\|update\|rm\|rotate-secret\|preset --app &lt;slug&gt;) |
| [`audit-events`](#audit-events) | Audit-log query (audit-events list\|get &lt;id&gt;) |
| [`apps`](#apps) | List your apps |
| [`app`](#app) | Get/update one app (gregale app &lt;slug&gt; [scale\|rename &lt;new&gt;\|--ram N\|…]) |
| [`billing`](#billing) | Manage billing (portal, invoices, subscription, card on file) |
| [`canary`](#canary) | Project a canary preset against recent app traffic (canary simulate &lt;slug&gt;) |
| [`build`](#build) | Build provenance + sbom (build provenance &lt;id&gt;\|build sbom &lt;id&gt;) |
| [`connect`](#connect) | Connect a third-party service (github \| repo OWNER/NAME) |
| [`cors`](#cors) | Configure CORS for an app (allow\|ls\|rm\|show) |
| [`crons`](#crons) | Manage scheduled requests |
| [`triggers`](#triggers) | Manage unified event triggers (broker mappings + cron-linked rows) |
| [`jobs`](#jobs) | Manage jobs (run-to-completion workloads) |
| [`workflows`](#workflows) | Manage durable execution workflows |
| [`dashboard`](#dashboard) | Open the account dashboard in your browser |
| [`doctor`](#doctor) | Preflight local source or OCI image metadata; runtime checks are skipped |
| [`delayed-task`](#delayed-task) | Schedule a deferred invocation (delayed-task add\|get\|cancel) |
| [`deployments`](#deployments) | List deployments (--limit N \| --before C \| --all) |
| [`deployment`](#deployment) | Get or wait for one deployment (&lt;id&gt; \| wait &lt;id&gt; \| set-min-instances &lt;id&gt;) |
| [`deploys`](#deploys) | Deployment drill-downs (deploys show\|status\|cancel\|reorder\|clear\|clear-obsolete) |
| [`deploy`](#deploy) | Deploy (--path DIR \| --image REF \| --tarball PATH \| --repo OWNER/NAME --ref REF \| --github \| --template NAME) |
| [`domains`](#domains) | Manage custom domains |
| [`dev`](#dev) | Sync the dirty working tree to a stable remote developer environment |
| [`preview`](#preview) | Manage preview environments (Mega-C PR-1 / issue #961 leaf 3) |
| [`tenant-surfaces`](#tenant-surfaces) | Manage tenant surfaces (multi-hostname SAN bundle per app) |
| [`edge-rules`](#edge-rules) | Per-app edge rules (edge-rules list\|create\|get\|update\|delete --app &lt;slug&gt;) |
| [`openapi`](#openapi) | Manage app OpenAPI docs + pre-publish schema-drift checks |
| [`env`](#env) | Pull/push .env &lt;-&gt; sealed secrets (--app &lt;slug&gt;) |
| [`init`](#init) | Scaffold a reference project from a built-in template (--template NAME --path DIR [--deploy]) |
| [`inspect`](#inspect) | Read-only operator surface (inspect &lt;slug&gt; --upstreams [--scope &lt;scope&gt;] [--json]) |
| [`invoke`](#invoke) | Functional smoke test (invoke [--async] &lt;slug&gt; [--payload J\|@file\|-]) |
| [`invocations`](#invocations) | Per-account invocation ledger (invocations list\|get &lt;id&gt;) |
| [`debug`](#debug) | Production debugger (ADR-127 / PR-B) |
| [`invitations`](#invitations) | Standalone invitation actions (invitations peek &lt;token&gt;\|accept &lt;token&gt;) |
| [`invoices`](#invoices) | List issued invoices |
| [`keys`](#keys) | Manage API keys (keys list\|add\|rm\|rotate\|grace-window) |
| [`login`](#login) | Authenticate this machine (--token for CI) |
| [`logout`](#logout) | Remove the stored token |
| [`signup`](#signup) | Create a new account (signup [--email-only EMAIL \| --password-stdin]) |
| [`logs`](#logs) | Tail app or deployment logs (--follow) |
| [`metrics`](#metrics) | Per-app or account-wide metrics (gregale metrics &lt;slug&gt; [--range 5m] \| --account) |
| [`mfa`](#mfa) | Manage account MFA (mfa enroll\|confirm\|verify\|recover\|disable) |
| [`open`](#open) | Open the app&#39;s URL (or its dashboard page) in your browser |
| [`orgs`](#orgs) | Manage orgs + members (orgs ls\|create\|info\|rm\|members ...\|keys ...\|transfer-ownership\|seat-usage\|invitations ...\|me) |
| [`overage-cap`](#overage-cap) | Set / clear the account&#39;s overage cap (--clear \| &lt;cents&gt;) |
| [`park`](#park) | Park an app cold (kill all live instances) |
| [`plan`](#plan) | Change plan (free\|hobby\|pro\|scale); paid upgrades open the provider checkout |
| [`ps`](#ps) | Show live instances + state for an app |
| [`queue`](#queue) | Inspect the wake-queue depth (queue tail\|send\|receive\|state\|peek\|dead-letter\|ack) |
| [`registry`](#registry) | Per-app private container registry credentials (registry list\|set\|rm --app &lt;slug&gt;) |
| [`rollback`](#rollback) | Re-promote the previous deployment |
| [`rollouts`](#rollouts) | Operator manual rollout recovery (rollouts recover &lt;slug&gt; --action advance\|promote\|abort --reason &lt;text&gt;) |
| [`scan`](#scan) | Decomposition dry-run (--tarball \| --path \| --repo OWNER/NAME) |
| [`secrets`](#secrets) | Manage env secrets (secrets list\|set\|unset\|list-all\|rotate) |
| [`github-webhook-secret`](#github-webhook-secret) | Manage legacy installation-scoped webhook secrets (admin) |
| [`slo`](#slo) | Per-app SLO panel (gregale slo &lt;slug&gt; [--window 24h]) |
| [`status`](#status) | Personal SLO numbers (availability, wake p95, build success) |
| [`tail`](#tail) | Live tail of the unified event stream |
| [`trusted-publishers`](#trusted-publishers) | Per-app cosign trusted-publisher list (admin; trusted-publishers add\|remove\|list) |
| [`usage`](#usage) | Show this month&#39;s usage (gregale usage [--month YYYY-MM]\|daily [--day YYYY-MM-DD]\|storage [--day YYYY-MM-DD]\|summary) |
| [`version`](#version) | Print the CLI version |
| [`wake-timeline`](#wake-timeline) | Walk the per-wake event stream (wake-timeline &lt;slug&gt; &lt;wake-id&gt; [--since RFC3339] [--limit N] [--all]) |
| [`throttle-suggestions`](#throttle-suggestions) | Per-route throttle recommendations + dry-run preview (gregale throttle-suggestions &lt;slug&gt; [--range 5m] [--dry-run --candidate-rps N --candidate-burst N]) |
| [`mail`](#mail) | Mail operator dry-run (issue #246 acceptance item 6): `gregale mail dry-run [--unsubscribe-url URL]` renders every production template against a fixture account + day and writes the wire payload as JSON. The eyeball gate before flipping a box to FAAS_MAIL_TRANSPORT=resend. |
| [`wake`](#wake) | Wake a parked app (pulls out of snapshot) |
| [`traffic`](#traffic) | Manage deployment traffic split (issue #556; Pro/Scale only) |
| [`mirror`](#mirror) | Manage traffic mirroring (mirror list\|create\|info\|update\|rm\|summary --app &lt;slug&gt;; issue #72 / ADR-124; Pro/Scale only) |
| [`cache`](#cache) | Manage response cache (cache purge &lt;slug&gt; [--path GLOB]) |
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


## admin

Operator-only billing ops (admin credit|refund|consume-credits)

`gregale admin [<subcommand>] <uuid> <cents>`

### admin credit

Issue a billing credit

| Flag | Meaning | |
|---|---|---|
| `--reason <text>` | credit reason text | required |

### admin refund

Refund a paid Polar invoice

| Flag | Meaning | |
|---|---|---|
| `--reason <text>` | refund reason text | required |
| `--idempotency-key <key>` | stable provider retry key |  |

### admin consume-credits

Consume credits against an invoice


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

### alerts info

Show one alert rule

### alerts update

Update one alert rule

### alerts rm

Delete one alert rule

### alerts rotate-secret

Rotate the alert&#39;s webhook secret

### alerts preset

Alert preset catalog (preset list|enable --app &lt;slug&gt;)


## audit-events

Audit-log query (audit-events list|get &lt;id&gt;)

`gregale audit-events [<subcommand>] <id>`

### audit-events list

List audit events

### audit-events get

Show one audit event


## apps

List your apps

`gregale apps [<subcommand>] [--q] [--quiet]`

| Flag | Meaning | |
|---|---|---|
| `--q` | delete one app |  |
| `--quiet` | delete one app |  |

### apps ls

Alias for the default list action

### apps routes

List admitted per-route labels for one app (ADR-093)

### apps streaming-cap

Per-app streaming classification probe (ADR-102 D6)

### apps -q

Delete one app (positional: &lt;slug&gt;)

### apps --quiet

Delete one app (positional: &lt;slug&gt;)


## app

Get/update one app (gregale app &lt;slug&gt; [scale|rename &lt;new&gt;|--ram N|…])

`gregale app [<subcommand>] <slug> [--ram <MB>] [--max-concurrency <N>] [--require-signed <value>]`

| Flag | Meaning | |
|---|---|---|
| `--ram <MB>` | set RAM in MB |  |
| `--max-concurrency <N>` | set max_concurrency |  |
| `--require-signed <value>` | toggle require_signed | one of `true` · `false` |

### app scale

Set max_concurrency / RAM / CPU

### app rename

Rename an app

### app security

Toggle require_signed on deploys

### app routes

List admitted per-route labels for one app (ADR-093)


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

### billing price-catalog

Inspect the price catalog (admin)

### billing reconcile

Reconcile an invoice with the provider (admin)

### billing reconcile-paddle-overage

Reconcile Paddle overage charges (admin)

### billing webhook-test

Send a signed test webhook (operator)


## canary

Project a canary preset against recent app traffic (canary simulate &lt;slug&gt;)

`gregale canary [<subcommand>] <slug>`

### canary simulate

Estimate per-stage canary success from the last hour

| Flag | Meaning | |
|---|---|---|
| `--canary-preset <PRESET>` | canary ladder preset | one of `slow` · `balanced` · `aggressive` · `1-10-50-100` |


## build

Build provenance + sbom (build provenance &lt;id&gt;|build sbom &lt;id&gt;)

`gregale build [<subcommand>]`

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


## cors

Configure CORS for an app (allow|ls|rm|show)

`gregale cors [<subcommand>]`

### cors allow

Attach a CORS rule to &lt;slug&gt;

### cors ls

List CORS rules bound to &lt;slug&gt;

### cors rm

Delete a CORS rule by id

### cors show

Show per-app default CORS + active rules


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

### jobs logs

Tail logs for one task


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

Schedule a deferred invocation (delayed-task add|get|cancel)

`gregale delayed-task [<subcommand>]`

### delayed-task add

Schedule a deferred invocation

### delayed-task get

Show one delayed task

### delayed-task info

Alias for get

### delayed-task cancel

Cancel a delayed task


## deployments

List deployments (--limit N | --before C | --all)

`gregale deployments [--limit <N>] [--before <cursor>] [--all]`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1-200) |  |
| `--before <cursor>` | pagination cursor (RFC3339Nano) |  |
| `--all` | walk every page |  |


## deployment

Get or wait for one deployment (&lt;id&gt; | wait &lt;id&gt; | set-min-instances &lt;id&gt;)

`gregale deployment [<subcommand>] <id> [--show-scan] [--min <N>]`

| Flag | Meaning | |
|---|---|---|
| `--show-scan` | include the per-deploy grype scan payload |  |
| `--min <N>` | min_instances floor (&gt;= 0) |  |

### deployment wait

Wait until a deployment is live

| Flag | Meaning | |
|---|---|---|
| `--timeout <SECONDS>` | maximum seconds to wait |  |

### deployment set-min-instances

Set the per-deployment cold-wake floor


## deploys

Deployment drill-downs (deploys show|status|cancel|reorder|clear|clear-obsolete)

`gregale deploys [<subcommand>] <id>`

### deploys show

Print the closed 6-stage post-stream summary

### deploys status

Print the stage summary with terminal-status footer (live since / failed at)

### deploys retry

Retry a failed deployment from a specific stage (--from=&lt;stage&gt;)


## deploy

Deploy (--path DIR | --image REF | --tarball PATH | --repo OWNER/NAME --ref REF | --github | --template NAME)

`gregale deploy [--image <REF>] [--tarball <PATH>] [--path <DIR>] [--worktree] [--repo <OWNER/NAME>] [--ref <REF>] [--github] [--template <NAME>] [--dockerfile] [--runtime <RUNTIME>] [--handler <HANDLER>] [--name <SLUG>] [--function] [--app] [--yes] [--only <SLUGS>] [--reason <text>] [--tag <TAG>] [--deployed-by <NAME>] [--pr-number <N>] [--exclude <SLUGS>] [--show-affected] [--persist-exclude] [--project-slug <SLUG>] [--canary-preset <PRESET>] [--canary-stages <STAGES>] [--require-authn] [--no-require-authn] [--app-protocol <PROTOCOL>] [--traffic-percent <PERCENT>] [--no-triggers] [--wait] [--no-wait] [--secret-scan <on|off>] [--diff] [--strict] [--lenient] [--server-diff] [--doctor-strict]`

| Flag | Meaning | |
|---|---|---|
| `--image <REF>` | deploy from a container image reference |  |
| `--tarball <PATH>` | deploy from a source tarball |  |
| `--path <DIR>` | deploy a selected local source directory (relative to the current directory) |  |
| `--worktree` | deploy the selected source directory from the working tree, including local changes |  |
| `--repo <OWNER/NAME>` | deploy from a GitHub repo |  |
| `--ref <REF>` | git ref for --repo (branch, tag, or 40-char SHA) |  |
| `--github` | emit a GitHub Actions workflow snippet for the Gregale deploy action |  |
| `--template <NAME>` | scaffold from a built-in template | one of `hello-node` · `hello-python` · `hello-go` · `cron-example` · `function-node` · `function-python` · `function-go` · `function-node24` · `function-python313` · `s3-uploader` · `slack-bot` · `rest-api-postgres` · `cron-worker` · `webhook-receiver` · `ai-chat` |
| `--dockerfile` | build with the supplied Dockerfile inside --tarball |  |
| `--runtime <RUNTIME>` | function runtime | one of `node22` · `python312` · `go124` · `go124-alpine` · `node24` · `python313` |
| `--handler <HANDLER>` | function handler |  |
| `--name <SLUG>` | app name (default: selected source directory, or current directory) |  |
| `--function` | deploy as a function; skip shape auto-detection |  |
| `--app` | deploy as an app; skip shape auto-detection |  |
| `--yes` | skip the apply confirmation prompt |  |
| `--only <SLUGS>` | workloads to apply (comma-separated; project apply path) |  |
| `--reason <text>` | free-text deploy reason (≤280 chars) |  |
| `--tag <TAG>` | annotation tag | one of `incident_recovery` · `hotfix` · `scheduled_maintenance` · `compliance_hold` · `partner_request` |
| `--deployed-by <NAME>` | operator label (auto-resolved from git config user.name) |  |
| `--pr-number <N>` | GitHub PR number (positive int; 0 = absent). CI paths stamp via the GitHub Action. |  |
| `--exclude <SLUGS>` | omit workloads (slug, comma-separated; mutex with --only; ADR-124) |  |
| `--show-affected` | render the WillDeploy + Skipped + Unaffected + Removed partition (ADR-124) |  |
| `--persist-exclude` | record --exclude slugs into deployment_scope_exclusions (apply path only; ADR-124 follow-up #3) |  |
| `--project-slug <SLUG>` | kebab slug for the project (one-key provision) |  |
| `--canary-preset <PRESET>` | canary ladder preset | one of `none` · `slow` · `balanced` · `aggressive` · `1-10-50-100` · `custom` |
| `--canary-stages <STAGES>` | custom percent@duration canary stages |  |
| `--require-authn` | require bearer auth on every request |  |
| `--no-require-authn` | drop the token requirement |  |
| `--app-protocol <PROTOCOL>` | wire protocol selector | one of `http1` · `http2` · `grpc` |
| `--traffic-percent <PERCENT>` | deployment traffic split weight (0-100) |  |
| `--no-triggers` | skip gregale.yaml trigger fan-out |  |
| `--wait` | wait for deployment to become live (default) |  |
| `--no-wait` | return after deployment is queued |  |
| `--secret-scan <on|off>` | scan .env files before packing | one of `on` · `off` |
| `--diff` | preview what would change without deploying |  |
| `--strict` | fail on diff schema/quota/env breaks |  |
| `--lenient` | return success even when diff has breaks |  |
| `--server-diff` | compute deploy diff on apid |  |
| `--doctor-strict` | run doctor before deploy and abort on errors |  |


## domains

Manage custom domains

`gregale domains [<subcommand>]`

### domains list

List custom domain bindings

### domains add

Bind a custom domain to an app

### domains rm

Remove a custom domain binding

### domains verify

Re-verify DNS + cert for a domain

### domains show

Show a domain&#39;s cert details

### domains doctor

5-check doctor report (DNS / CNAME / TLS / CAA / IPv6)


## dev

Sync the dirty working tree to a stable remote developer environment

`gregale dev [--path <DIR>] [--name <PROJECT>] [--once] [--stop] [--no-logs]`

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | source directory |  |
| `--name <PROJECT>` | developer-session project name |  |
| `--once` | deploy once and exit |  |
| `--stop` | tear down the developer environment |  |
| `--no-logs` | do not attach the live runtime log stream |  |


## preview

Manage preview environments (Mega-C PR-1 / issue #961 leaf 3)

`gregale preview [<subcommand>]`

### preview destroy

Tear down a preview app (POST /v1/preview/{slug}/destroy)


## tenant-surfaces

Manage tenant surfaces (multi-hostname SAN bundle per app)

`gregale tenant-surfaces [<subcommand>] [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug |  |

### tenant-surfaces list

List tenant surfaces on an app

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (required) |  |

### tenant-surfaces add

Add a tenant surface (with seed hostnames)

### tenant-surfaces rm

Remove a tenant surface (cascades hostnames)

### tenant-surfaces hostname

Manage hostnames on a surface (add|rm)


## edge-rules

Per-app edge rules (edge-rules list|create|get|update|delete --app &lt;slug&gt;)

`gregale edge-rules [<subcommand>] --app <slug> [--kind <value>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--kind <value>` | rule kind | one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` |

### edge-rules list

List edge rules

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | filter to a single app slug |  |
| `--kind <value>` | filter to a single kind | one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` |

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

Fetch an app OpenAPI document (manual_import|auto)

| Flag | Meaning | |
|---|---|---|
| `--source <manual_import|auto>` | document source | one of `manual_import` · `auto` |

### openapi import

Import an app OpenAPI document from a JSON file or stdin

### openapi dry-run

Preview uncovered routes without importing the document

### openapi rm

Remove the imported app OpenAPI document


## env

Pull/push .env &lt;-&gt; sealed secrets (--app &lt;slug&gt;)

`gregale env [<subcommand>] --app <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### env pull

Pull sealed-secret keys to a .env skeleton (values blank)

### env push

Push KEY=VALUE pairs to sealed secrets

### env diff

Render the env-diff matrix (presence / value-equality across scopes)


## init

Scaffold a reference project from a built-in template (--template NAME --path DIR [--deploy])

`gregale init --template <NAME> --path <DIR> [--deploy] [--name <SLUG>] [--list]`

| Flag | Meaning | |
|---|---|---|
| `--template <NAME>` | template name | required; one of `hello-node` · `hello-python` · `hello-go` · `cron-example` · `function-node` · `function-python` · `function-go` · `function-node24` · `function-python313` · `s3-uploader` · `slack-bot` · `rest-api-postgres` · `cron-worker` · `webhook-receiver` · `ai-chat` |
| `--path <DIR>` | target directory | required |
| `--deploy` | deploy after scaffolding |  |
| `--name <SLUG>` | app slug used with --deploy |  |
| `--list` | list available templates |  |


## inspect

Read-only operator surface (inspect &lt;slug&gt; --upstreams [--scope &lt;scope&gt;] [--json])

`gregale inspect <slug> [--upstreams] [--scope <scope>] [--errors]`

| Flag | Meaning | |
|---|---|---|
| `--upstreams` | List data upstreams captured for this app (ADR-098 §9.A) |  |
| `--scope <scope>` | filter by scope (forwarded as ?scope=, used with --upstreams) |  |
| `--errors` | show the latest failed deployment&#39;s persisted error explanation |  |


## invoke

Functional smoke test (invoke [--async] &lt;slug&gt; [--payload J|@file|-])

`gregale invoke <slug> [--async] [--payload <J|@file|->]`

| Flag | Meaning | |
|---|---|---|
| `--async` | return immediately with status_url |  |
| `--payload <J|@file|->` | JSON payload (inline \| @file \| -) |  |


## invocations

Per-account invocation ledger (invocations list|get &lt;id&gt;)

`gregale invocations [<subcommand>] <id>`

### invocations list

List invocations

### invocations get

Show one invocation


## debug

Production debugger (ADR-127 / PR-B)

`gregale debug [<subcommand>] <slug>`

### debug requests

Per-request telemetry (list|get|replay)

### debug regressions

Active regression observations

### debug compare

Per-route deployment-vs-deployment compare


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


## logout

Remove the stored token

`gregale logout`


## signup

Create a new account (signup [--email-only EMAIL | --password-stdin])

`gregale signup [--email-only <EMAIL>] [--password-stdin]`

| Flag | Meaning | |
|---|---|---|
| `--email-only <EMAIL>` | send a one-time signup link to this email (no password prompt) |  |
| `--password-stdin` | read password from stdin (CI; mutually exclusive with --email-only) |  |


## logs

Tail app or deployment logs (--follow)

`gregale logs [--follow]`

| Flag | Meaning | |
|---|---|---|
| `--follow` | stream logs until interrupted |  |


## metrics

Per-app or account-wide metrics (gregale metrics &lt;slug&gt; [--range 5m] | --account)

`gregale metrics <slug> [--range <WINDOW>] [--account]`

| Flag | Meaning | |
|---|---|---|
| `--range <WINDOW>` | window (5m\|15m\|1h\|6h\|24h\|7d) | one of `5m` · `15m` · `1h` · `6h` · `24h` · `7d` |
| `--account` | account-wide roll-up |  |


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

Open the app&#39;s URL (or its dashboard page) in your browser

`gregale open [<subcommand>]`

### open docs

Open a CLI docs page (open docs [&lt;slug&gt;])


## orgs

Manage orgs + members (orgs ls|create|info|rm|members ...|keys ...|transfer-ownership|seat-usage|invitations ...|me)

`gregale orgs [<subcommand>]`

### orgs ls

List orgs

### orgs create

Create an org

### orgs info

Show one org

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

Show live instances + state for an app

`gregale ps`


## queue

Inspect the wake-queue depth (queue tail|send|receive|state|peek|dead-letter|ack)

`gregale queue [<subcommand>]`

### queue tail

Tail the wake queue

### queue send

Enqueue a wake request

### queue receive

Receive a wake request

### queue status

Show queue state

### queue peek

Peek at the next wake

### queue dead-letter

Inspect the dead-letter queue

### queue ack

Ack a wake


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

### registry rm

Remove a registry credential


## rollback

Re-promote the previous deployment

`gregale rollback`


## rollouts

Operator manual rollout recovery (rollouts recover &lt;slug&gt; --action advance|promote|abort --reason &lt;text&gt;)

`gregale rollouts [<subcommand>] <slug> --action <value> [--reason <text>]`

| Flag | Meaning | |
|---|---|---|
| `--action <value>` | recover action | required; one of `advance` · `promote` · `abort` |
| `--reason <text>` | operator-supplied reason (logged to deployment_audit) |  |

### rollouts recover

Manually advance / promote / abort a stuck rollout (operator escape hatch)


## scan

Decomposition dry-run (--tarball | --path | --repo OWNER/NAME)

`gregale scan [--tarball <PATH>] [--path <DIR>] [--repo <OWNER/NAME>] [--exclude <SLUGS>] [--show-affected] [--persist-exclude]`

| Flag | Meaning | |
|---|---|---|
| `--tarball <PATH>` | scan a source tarball |  |
| `--path <DIR>` | scan a local directory |  |
| `--repo <OWNER/NAME>` | scan a GitHub repo |  |
| `--exclude <SLUGS>` | omit workloads (slug, comma-separated; mutex with --only; ADR-124) |  |
| `--show-affected` | render the WillDeploy + Unaffected tables (ADR-124) |  |
| `--persist-exclude` | record --exclude slugs into deployment_scope_exclusions (apply path only; ADR-124 follow-up #3) |  |


## secrets

Manage env secrets (secrets list|set|unset|list-all|rotate)

`gregale secrets [<subcommand>]`

### secrets list

List sealed secrets

### secrets set

Set a sealed secret

### secrets unset

Remove a sealed secret

### secrets list-all

List every secret across apps

### secrets rotate

Re-seal one secret under the current host key


## github-webhook-secret

Manage legacy installation-scoped webhook secrets (admin)

`gregale github-webhook-secret [<subcommand>]`

### github-webhook-secret set

Rotate the secret for one installation_id


## slo

Per-app SLO panel (gregale slo &lt;slug&gt; [--window 24h])

`gregale slo <slug> [--window <WINDOW>]`

| Flag | Meaning | |
|---|---|---|
| `--window <WINDOW>` | window (1h\|24h\|7d) | one of `1h` · `24h` · `7d` |


## status

Personal SLO numbers (availability, wake p95, build success)

`gregale status`


## tail

Live tail of the unified event stream

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


## wake-timeline

Walk the per-wake event stream (wake-timeline &lt;slug&gt; &lt;wake-id&gt; [--since RFC3339] [--limit N] [--all])

`gregale wake-timeline <slug> <wake-id> [--since <RFC3339>] [--limit <N>] [--all]`

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
| `--range <WINDOW>` | observation window (e.g. 5m\|1h\|24h) | one of `5m` · `15m` · `1h` · `6h` · `24h` |
| `--dry-run` | enable the dry-run preview pass (requires --candidate-rps) |  |
| `--candidate-rps <N>` | candidate rate-limit rps for the dry-run preview |  |
| `--candidate-burst <N>` | candidate burst for the dry-run preview |  |


## mail

Mail operator dry-run (issue #246 acceptance item 6): `gregale mail dry-run [--unsubscribe-url URL]` renders every production template against a fixture account + day and writes the wire payload as JSON. The eyeball gate before flipping a box to FAAS_MAIL_TRANSPORT=resend.

`gregale mail [<subcommand>] [--unsubscribe-url <URL>]`

| Flag | Meaning | |
|---|---|---|
| `--unsubscribe-url <URL>` | List-Unsubscribe URL (RFC 8058); empty disables the header |  |

### mail dry-run

render every mail template against a fixture; print wire JSON


## wake

Wake a parked app (pulls out of snapshot)

`gregale wake`


## traffic

Manage deployment traffic split (issue #556; Pro/Scale only)

`gregale traffic [<subcommand>]`

### traffic set

Set the traffic split for a deployment

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | deployment id to set the traffic split on | required |
| `--percent <N>` | traffic weight in [0, 100]; -1 = unset (server default 100) | required |


## mirror

Manage traffic mirroring (mirror list|create|info|update|rm|summary --app &lt;slug&gt;; issue #72 / ADR-124; Pro/Scale only)

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
| `--source <ID>` | source deployment id (live) | required |
| `--mirror <ID>` | mirror deployment id (live; same app) | required |
| `--percent <N>` | fan-out percent in [0, 100]; 100 = every request |  |
| `--include-body` | include request/response bodies in the comparison ledger |  |
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
| `--include-body` | enable body capture (mutually exclusive with --no-include-body) |  |
| `--no-include-body` | disable body capture |  |
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


## cache

Manage response cache (cache purge &lt;slug&gt; [--path GLOB])

`gregale cache [<subcommand>] <slug>`

### cache purge

Purge cached responses for an app

| Flag | Meaning | |
|---|---|---|
| `--path <GLOB>` | optional normalized request path glob |  |


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

