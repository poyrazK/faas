# gregale CLI reference

Generated from the CLI's command manifest by `gregale man --markdown`. Do not edit by hand.

Automation: put `--non-interactive` before the command to disable prompts and browser launches; use `--json` for structured output. Required confirmations must be supplied explicitly. Connection selection: `gregale --profile <name> <command>`. Put this option before the command; command-local `--profile` options retain their documented meaning. See [CLI configuration](cli-config.md) for connection profiles and environment precedence.

| Command | What it does |
|---|---|
| [`mcp`](#mcp) | Scaffold, deploy and verify stateless MCP servers |
| [`start`](#start) | Get your first app live with a few guided prompts |
| [`account`](#account) | Manage the local account (account export\|delete\|restore\|status\|dpa\|slo) |
| [`add`](#add) | Provision and bind managed resources to an app |
| [`bucket`](#bucket) | Manage object encryption, Object Lock, copy sources, tags, versioning, lifecycle rules, receipts and capacity |
| [`bindings`](#bindings) | Inspect app bindings, verification, runtime freshness, and rotation progress |
| [`capabilities`](#capabilities) | Show feature maturity and plan availability |
| [`alerts`](#alerts) | Per-app alert rules (alerts list\|add\|info\|update\|rm\|rotate-secret\|preset\|actions --app &lt;slug&gt;) |
| [`audit-events`](#audit-events) | Audit-log query (audit-events list\|get &lt;id&gt;) |
| [`commit`](#commit) | Manage transactional PostgreSQL outbox sources (internal) |
| [`events`](#events) | Preview routing, publish events, inspect deliveries, and backfill retained events |
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
| [`automations`](#automations) | Build, monitor and control customer-built automations |
| [`workflows`](#workflows) | Manage durable execution workflows |
| [`dashboard`](#dashboard) | Open the account dashboard in your browser |
| [`doctor`](#doctor) | Preflight local source or OCI image metadata; runtime checks are skipped |
| [`delayed-task`](#delayed-task) | Schedule and inspect deferred invocations |
| [`deployments`](#deployments) | List deployments or manage stable named URLs for immutable revisions |
| [`deployment`](#deployment) | Inspect a deployment, wait for its rollout, advance a canary, or set its minimum instances |
| [`deploys`](#deploys) | Deployment drill-downs (deploys show\|status\|cancel\|reorder\|clear\|clear-obsolete\|retry) |
| [`deploy`](#deploy) | Deploy an app, function, or project |
| [`domains`](#domains) | Manage custom domains |
| [`dev`](#dev) | Sync local changes to a developer environment |
| [`diff`](#diff) | Compare two named environments in the linked project |
| [`test`](#test) | Run scenario suites, lifecycle profiles, and bounded local HTTP load tests |
| [`chaos`](#chaos) | Inject bounded faults into isolated real-VM scenario tests |
| [`preview`](#preview) | Manage preview environments for pull requests |
| [`flags`](#flags) | Release application behavior to selected customers |
| [`platform-tenants`](#platform-tenants) | Manage one customer across app consumers and tenant hostnames |
| [`edge-rules`](#edge-rules) | Per-app edge rules (edge-rules list\|trace\|create\|get\|update --app &lt;slug&gt;; edge-rules rm &lt;id&gt;) |
| [`openapi`](#openapi) | Manage app OpenAPI docs + pre-publish schema-drift checks |
| [`routes`](#routes) | Analyze route changes, migrations, lifecycle and production policies |
| [`env`](#env) | Clone project environments or manage app runtime env/secrets |
| [`init`](#init) | Scaffold a project from a built-in template |
| [`inspect`](#inspect) | Explain an app from its runtime, deployment, API, data, scaling, and release signals (slug defaults to linked context) |
| [`invoke`](#invoke) | Functional smoke test (invoke [--async] &lt;slug&gt; [--payload J\|@file\|-]; slug defaults to linked context) |
| [`run`](#run) | Run untrusted code in an isolated disposable microVM |
| [`runs`](#runs) | Inspect or cancel isolated disposable runs |
| [`invocations`](#invocations) | Per-account invocation ledger (invocations list\|get\|wait &lt;id&gt;) |
| [`issues`](#issues) | Group failures and track ownership and release-aware resolution |
| [`customer-operations`](#customer-operations) | Inspect customer work, verify downloads and reconcile outcomes |
| [`operations`](#operations) | Coordinate named work with leases, explicit contention policy, and fenced ownership |
| [`debug`](#debug) | Inspect production requests and regressions |
| [`trace`](#trace) | Look up a W3C trace through the account trace index |
| [`invitations`](#invitations) | Standalone invitation actions (invitations peek &lt;token&gt;\|accept &lt;token&gt;) |
| [`invoices`](#invoices) | List issued invoices |
| [`keys`](#keys) | Manage API keys (keys list\|add\|rm\|rotate\|grace-window) |
| [`login`](#login) | Authenticate this machine |
| [`link`](#link) | Link this checkout to a Gregale project |
| [`logout`](#logout) | Revoke the managed CLI session and remove the stored token |
| [`unlink`](#unlink) | Remove the linked project from this checkout |
| [`profile`](#profile) | Manage named API connections and isolated credentials |
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
| [`data-api`](#data-api) | Create schema-generated PostgreSQL APIs and export application types |
| [`ps`](#ps) | Show live instances + state for an app (slug defaults to linked context) |
| [`queue`](#queue) | Inspect queues and manage first-class queue bindings |
| [`dlq`](#dlq) | Inspect, replay, or purge unified dead-letter events |
| [`registry`](#registry) | Manage private registry credentials and deploy published images |
| [`realtime`](#realtime) | Manage realtime endpoints, policies, connections, channels, and auth |
| [`rollback`](#rollback) | Restore a previous deployment, or check an exact historical rollback |
| [`projects`](#projects) | Inspect and recover repository projects |
| [`scan`](#scan) | Decomposition dry-run (--tarball \| --path \| --repo OWNER/NAME) |
| [`secrets`](#secrets) | Manage sealed secrets and environment secret references |
| [`slo`](#slo) | Per-app SLO panel (gregale slo &lt;slug&gt; [--window 24h]; slug defaults to linked context) |
| [`status`](#status) | Platform status: API availability, wake p95 and deployment success (not account-specific) |
| [`tail`](#tail) | Live tail of the unified event stream (app defaults to linked context) |
| [`trusted-publishers`](#trusted-publishers) | Per-app cosign trusted-publisher list (admin; trusted-publishers add\|remove\|list) |
| [`usage`](#usage) | Show this month&#39;s usage (gregale usage [--month YYYY-MM]\|daily [--day YYYY-MM-DD]\|storage [--day YYYY-MM-DD]\|summary) |
| [`version`](#version) | Print the CLI version |
| [`config`](#config) | Manage non-secret local CLI settings (config get\|set\|list) |
| [`wake-timeline`](#wake-timeline) | Walk the per-wake event stream (wake-timeline [&lt;slug&gt;] &lt;wake-id&gt; [--app SLUG] [--since RFC3339] [--limit N] [--all] [--verbose]; slug defaults to linked context) |
| [`throttle-suggestions`](#throttle-suggestions) | Per-route throttle recommendations + dry-run preview (gregale throttle-suggestions &lt;slug&gt; [--range 5m] [--dry-run --candidate-rps N --candidate-burst N]) |
| [`wake`](#wake) | Wake a parked app (pulls out of snapshot) |
| [`traffic`](#traffic) | Manage deployment traffic split (available on every plan) |
| [`log-drains`](#log-drains) | Ship app runtime logs to an HTTP JSON or OTLP endpoint |
| [`mirror`](#mirror) | Manage traffic mirroring and sanitized replay (Pro/Scale only). Rules default to 5% and mirror only safe methods; bodies over 64 KiB are skipped, and raw bodies are never retained. |
| [`cache`](#cache) | Declare or purge response caching (cache GET /path/:id for 30s) |
| [`upload-cache`](#upload-cache) | Inspect or clean resumable source-upload recovery state |
| [`webhooks`](#webhooks) | Manage app and account release webhooks (webhooks account &lt;verb&gt;) |
| [`whoami`](#whoami) | Show the authenticated account |
| [`completion`](#completion) | Print a shell completion script (bash\|zsh\|fish\|powershell) |
| [`man`](#man) | Print the gregale(1) man page (or gregale-&lt;command&gt;(1) with one arg) |

## mcp

Scaffold, deploy and verify stateless MCP servers

`gregale mcp [<subcommand>]`

### mcp init

Create the Node MCP starter

`gregale mcp init --path <DIR>`

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | empty destination directory | required |

Examples:

```sh
gregale mcp init --path ./my-mcp
```

### mcp deploy

Deploy the worktree, enable streaming and verify MCP discovery

`gregale mcp deploy [--path <DIR>] --name <SLUG> [--profile <NAME>] [--token-env <ENV>] [--secrets-file <PATH>] [--timeout <SECONDS>]`

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | source directory (default .) |  |
| `--name <SLUG>` | app slug | required |
| `--profile <NAME>` | app resource profile | one of `micro` · `small` · `medium` · `large` · `xlarge` |
| `--token-env <ENV>` | client token for external OAuth verification |  |
| `--secrets-file <PATH>` | sealed app secrets |  |
| `--timeout <SECONDS>` | deployment wait timeout in seconds (default 1200) |  |

Examples:

```sh
gregale mcp deploy --path ./my-mcp --name my-mcp --profile small
```

### mcp doctor

Check discovery, Origin rejection, compatibility and optional streaming

`gregale mcp doctor [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--legacy` | check legacy compatibility (doctor), or use protocol 2025-11-25 |  |
| `--tool <NAME>` | tool to execute (call) |  |
| `--uri <URI>` | resource URI to read (resource-read) |  |
| `--prompt <NAME>` | prompt to render (prompt-get) |  |
| `--stream-tool <NAME>` | explicitly execute a tool to verify live progress (doctor) |  |
| `--arguments <JSON>` | tool or prompt arguments as a JSON object |  |
| `--arguments-file <PATH>` | JSON file containing tool or prompt arguments |  |
| `--name <NAME>` | connection name (config) |  |
| `--timeout <DURATION>` | total diagnostic timeout (default 30s) |  |

Examples:

```sh
gregale mcp doctor --app my-mcp --legacy --stream-tool stream_demo
```

### mcp tools

Discover tool schemas without invoking tools

`gregale mcp tools [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--legacy` | check legacy compatibility (doctor), or use protocol 2025-11-25 |  |
| `--tool <NAME>` | tool to execute (call) |  |
| `--uri <URI>` | resource URI to read (resource-read) |  |
| `--prompt <NAME>` | prompt to render (prompt-get) |  |
| `--stream-tool <NAME>` | explicitly execute a tool to verify live progress (doctor) |  |
| `--arguments <JSON>` | tool or prompt arguments as a JSON object |  |
| `--arguments-file <PATH>` | JSON file containing tool or prompt arguments |  |
| `--name <NAME>` | connection name (config) |  |
| `--timeout <DURATION>` | total diagnostic timeout (default 30s) |  |

Examples:

```sh
gregale mcp tools --app my-mcp
```

### mcp resources

Discover resource and template definitions without reading contents

`gregale mcp resources [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--legacy` | check legacy compatibility (doctor), or use protocol 2025-11-25 |  |
| `--tool <NAME>` | tool to execute (call) |  |
| `--uri <URI>` | resource URI to read (resource-read) |  |
| `--prompt <NAME>` | prompt to render (prompt-get) |  |
| `--stream-tool <NAME>` | explicitly execute a tool to verify live progress (doctor) |  |
| `--arguments <JSON>` | tool or prompt arguments as a JSON object |  |
| `--arguments-file <PATH>` | JSON file containing tool or prompt arguments |  |
| `--name <NAME>` | connection name (config) |  |
| `--timeout <DURATION>` | total diagnostic timeout (default 30s) |  |

Examples:

```sh
gregale mcp resources --app my-mcp
```

### mcp resource-read

Read one explicitly selected resource URI

`gregale mcp resource-read [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--legacy` | check legacy compatibility (doctor), or use protocol 2025-11-25 |  |
| `--tool <NAME>` | tool to execute (call) |  |
| `--uri <URI>` | resource URI to read (resource-read) |  |
| `--prompt <NAME>` | prompt to render (prompt-get) |  |
| `--stream-tool <NAME>` | explicitly execute a tool to verify live progress (doctor) |  |
| `--arguments <JSON>` | tool or prompt arguments as a JSON object |  |
| `--arguments-file <PATH>` | JSON file containing tool or prompt arguments |  |
| `--name <NAME>` | connection name (config) |  |
| `--timeout <DURATION>` | total diagnostic timeout (default 30s) |  |

Examples:

```sh
gregale mcp resource-read --app my-mcp --uri 'file:///reports/current'
```

### mcp resource-watch

Watch one resource URI for content updates

`gregale mcp resource-watch [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] --uri <URI> [--interval <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--uri <URI>` | resource URI to read and watch | required |
| `--interval <DURATION>` | poll and stream reconciliation interval (default 30s) |  |

Examples:

```sh
gregale mcp resource-watch --app my-mcp --uri 'file:///reports/current'
```

### mcp prompts

Discover prompt definitions without rendering them

`gregale mcp prompts [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--legacy` | check legacy compatibility (doctor), or use protocol 2025-11-25 |  |
| `--tool <NAME>` | tool to execute (call) |  |
| `--uri <URI>` | resource URI to read (resource-read) |  |
| `--prompt <NAME>` | prompt to render (prompt-get) |  |
| `--stream-tool <NAME>` | explicitly execute a tool to verify live progress (doctor) |  |
| `--arguments <JSON>` | tool or prompt arguments as a JSON object |  |
| `--arguments-file <PATH>` | JSON file containing tool or prompt arguments |  |
| `--name <NAME>` | connection name (config) |  |
| `--timeout <DURATION>` | total diagnostic timeout (default 30s) |  |

Examples:

```sh
gregale mcp prompts --app my-mcp
```

### mcp prompt-get

Render one explicitly selected prompt

`gregale mcp prompt-get [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--legacy` | check legacy compatibility (doctor), or use protocol 2025-11-25 |  |
| `--tool <NAME>` | tool to execute (call) |  |
| `--uri <URI>` | resource URI to read (resource-read) |  |
| `--prompt <NAME>` | prompt to render (prompt-get) |  |
| `--stream-tool <NAME>` | explicitly execute a tool to verify live progress (doctor) |  |
| `--arguments <JSON>` | tool or prompt arguments as a JSON object |  |
| `--arguments-file <PATH>` | JSON file containing tool or prompt arguments |  |
| `--name <NAME>` | connection name (config) |  |
| `--timeout <DURATION>` | total diagnostic timeout (default 30s) |  |

Examples:

```sh
gregale mcp prompt-get --app my-mcp --prompt summarize --arguments '{"text":"weekly report"}'
```

### mcp complete

Request bounded suggestions for a prompt or resource-template argument

`gregale mcp complete [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--prompt <NAME>] [--resource-template <URI-TEMPLATE>] --argument <NAME> --value <TEXT> [--context <JSON>] [--context-file <PATH>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--legacy` | use protocol 2025-11-25 |  |
| `--prompt <NAME>` | prompt name to complete |  |
| `--resource-template <URI-TEMPLATE>` | resource URI template to complete |  |
| `--argument <NAME>` | prompt argument or template variable name | required |
| `--value <TEXT>` | partial value; empty requests the first suggestions | required |
| `--context <JSON>` | previously resolved arguments as a JSON object |  |
| `--context-file <PATH>` | read previous arguments from a JSON file |  |
| `--timeout <DURATION>` | total completion request timeout (default 30s) |  |

Examples:

```sh
gregale mcp complete --app my-mcp --prompt summarize --argument style --value exec
gregale mcp complete --app my-mcp --resource-template 'customer://records/{recordId}' --argument recordId --value example-
```

### mcp call

Execute one discovered tool; opt in to input requests or durable tasks

`gregale mcp call [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [--interactive] [--input-responses-file <PATH>] [--tasks] [--wait] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--legacy` | check legacy compatibility (doctor), or use protocol 2025-11-25 |  |
| `--tool <NAME>` | tool to execute (call) |  |
| `--uri <URI>` | resource URI to read (resource-read) |  |
| `--prompt <NAME>` | prompt to render (prompt-get) |  |
| `--stream-tool <NAME>` | explicitly execute a tool to verify live progress (doctor) |  |
| `--arguments <JSON>` | tool or prompt arguments as a JSON object |  |
| `--arguments-file <PATH>` | JSON file containing tool or prompt arguments |  |
| `--name <NAME>` | connection name (config) |  |
| `--timeout <DURATION>` | total request timeout (default 30s; interactive/--wait 5m) |  |
| `--interactive` | answer modern MCP input forms in the terminal |  |
| `--input-responses-file <PATH>` | JSON file with elicitation responses keyed by request ID |  |
| `--tasks` | accept modern MCP task handles for later inspection |  |
| `--wait` | wait for a task result; request cancellation on timeout |  |

Examples:

```sh
gregale mcp call --app my-mcp --tool add --arguments '{"a":7,"b":5}'
gregale mcp call --app my-mcp --tool report_preview --wait
gregale mcp call --app my-mcp --tool report_preview --tasks
```

### mcp task-get

Read the status or result of a previously returned task handle

`gregale mcp task-get [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [--task-id <ID>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--legacy` | check legacy compatibility (doctor), or use protocol 2025-11-25 |  |
| `--tool <NAME>` | tool to execute (call) |  |
| `--uri <URI>` | resource URI to read (resource-read) |  |
| `--prompt <NAME>` | prompt to render (prompt-get) |  |
| `--stream-tool <NAME>` | explicitly execute a tool to verify live progress (doctor) |  |
| `--arguments <JSON>` | tool or prompt arguments as a JSON object |  |
| `--arguments-file <PATH>` | JSON file containing tool or prompt arguments |  |
| `--name <NAME>` | connection name (config) |  |
| `--timeout <DURATION>` | total diagnostic timeout (default 30s) |  |
| `--task-id <ID>` | opaque task ID (task-get, task-wait or task-cancel) |  |

Examples:

```sh
gregale mcp task-get --app my-mcp --task-id 786512e2-9e0d-44bd-8f29-789f320fe840
```

### mcp task-wait

Resume waiting for a task to finish

`gregale mcp task-wait [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [--task-id <ID>] [--interactive] [--input-responses-file <PATH>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--legacy` | check legacy compatibility (doctor), or use protocol 2025-11-25 |  |
| `--tool <NAME>` | tool to execute (call) |  |
| `--uri <URI>` | resource URI to read (resource-read) |  |
| `--prompt <NAME>` | prompt to render (prompt-get) |  |
| `--stream-tool <NAME>` | explicitly execute a tool to verify live progress (doctor) |  |
| `--arguments <JSON>` | tool or prompt arguments as a JSON object |  |
| `--arguments-file <PATH>` | JSON file containing tool or prompt arguments |  |
| `--name <NAME>` | connection name (config) |  |
| `--timeout <DURATION>` | total task wait timeout (default 5m) |  |
| `--task-id <ID>` | opaque task ID (task-get, task-wait or task-cancel) |  |
| `--interactive` | answer modern MCP task input forms in the terminal |  |
| `--input-responses-file <PATH>` | JSON file with elicitation responses keyed by request ID |  |

Examples:

```sh
gregale mcp task-wait --app my-mcp --task-id 786512e2-9e0d-44bd-8f29-789f320fe840
gregale mcp task-wait --app my-mcp --task-id 786512e2-9e0d-44bd-8f29-789f320fe840 --interactive
```

### mcp task-cancel

Request cooperative cancellation of a previously returned task

`gregale mcp task-cancel [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [--task-id <ID>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--legacy` | check legacy compatibility (doctor), or use protocol 2025-11-25 |  |
| `--tool <NAME>` | tool to execute (call) |  |
| `--uri <URI>` | resource URI to read (resource-read) |  |
| `--prompt <NAME>` | prompt to render (prompt-get) |  |
| `--stream-tool <NAME>` | explicitly execute a tool to verify live progress (doctor) |  |
| `--arguments <JSON>` | tool or prompt arguments as a JSON object |  |
| `--arguments-file <PATH>` | JSON file containing tool or prompt arguments |  |
| `--name <NAME>` | connection name (config) |  |
| `--timeout <DURATION>` | total diagnostic timeout (default 30s) |  |
| `--task-id <ID>` | opaque task ID (task-get, task-wait or task-cancel) |  |

Examples:

```sh
gregale mcp task-cancel --app my-mcp --task-id 786512e2-9e0d-44bd-8f29-789f320fe840
```

### mcp tasks

Configure and inspect durable task worker scaling

#### mcp tasks setup

Preview or apply the task-backlog scaling policy

`gregale mcp tasks setup [--app <SLUG>] [--min <N>] [--max <N>] [--target <N>] [--apply]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | worker app slug (defaults to the linked project app) |  |
| `--min <N>` | minimum replicas (default 1; use 0 with an always-on observer) |  |
| `--max <N>` | maximum replicas (default 10) |  |
| `--target <N>` | outstanding tasks per worker (default 4) |  |
| `--apply` | apply the proposed worker scaling policy |  |

Examples:

```sh
gregale mcp tasks setup --app mcp-worker
gregale mcp tasks setup --app mcp-worker --min 0 --apply
```

#### mcp tasks status

Show task scaling policy and custom metric freshness

`gregale mcp tasks status [--app <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | worker app slug (defaults to the linked project app) |  |

Examples:

```sh
gregale mcp tasks status --app mcp-worker --json
```

### mcp watch

Watch caller-visible MCP catalog definitions for drift

`gregale mcp watch [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] --baseline <PATH> [--interval <DURATION>] [--tools] [--resources] [--prompts]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--baseline <PATH>` | contract snapshot to watch for drift | required |
| `--interval <DURATION>` | poll interval and stream reconciliation interval (default 30s) |  |
| `--tools` | subscribe to tool definition changes |  |
| `--resources` | subscribe to resource and template definition changes |  |
| `--prompts` | subscribe to prompt definition changes |  |

Examples:

```sh
gregale mcp watch --app my-mcp --baseline gregale-mcp.lock.json
```

### mcp config

Emit remote MCP connection JSON without credentials

`gregale mcp config [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--legacy` | check legacy compatibility (doctor), or use protocol 2025-11-25 |  |
| `--tool <NAME>` | tool to execute (call) |  |
| `--uri <URI>` | resource URI to read (resource-read) |  |
| `--prompt <NAME>` | prompt to render (prompt-get) |  |
| `--stream-tool <NAME>` | explicitly execute a tool to verify live progress (doctor) |  |
| `--arguments <JSON>` | tool or prompt arguments as a JSON object |  |
| `--arguments-file <PATH>` | JSON file containing tool or prompt arguments |  |
| `--name <NAME>` | connection name (config) |  |
| `--timeout <DURATION>` | total diagnostic timeout (default 30s) |  |

Examples:

```sh
gregale mcp config --app my-mcp --name my-mcp
```

### mcp lock

Capture MCP catalog definitions without reading resources, rendering prompts or invoking tools

`gregale mcp lock [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--out <PATH>] [--force] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing a client token |  |
| `--legacy` | use protocol 2025-11-25 |  |
| `--out <PATH>` | snapshot destination (default gregale-mcp.lock.json) |  |
| `--force` | replace an existing regular snapshot file |  |
| `--timeout <DURATION>` | total discovery timeout (default 30s) |  |

Examples:

```sh
gregale mcp lock --app my-mcp --out baseline.json
```

### mcp diff

Compare local MCP catalogs and report changes needing review

`gregale mcp diff --before <PATH> --after <PATH> [--check] [--strict-catalog]`

| Flag | Meaning | |
|---|---|---|
| `--before <PATH>` | baseline MCP contract snapshot | required |
| `--after <PATH>` | candidate MCP contract snapshot | required |
| `--check` | fail on breaking changes or changes needing review |  |
| `--strict-catalog` | require review when a caller gains visibility of any catalog definition |  |

Examples:

```sh
gregale mcp diff --before baseline.json --after candidate.json --check --json
```


## start

Get your first app live with a few guided prompts

`gregale start`

Examples:

```sh
gregale start
```


## account

Manage the local account (account export|delete|restore|status|dpa|slo)

`gregale account [<subcommand>]`

### account export

Export account data (GDPR)

`gregale account export [-o <PATH>] [--no-secrets]`

| Flag | Meaning | |
|---|---|---|
| `-o <PATH>` | output file |  |
| `--no-secrets` | exclude the sealed-secret ciphertext slice |  |

### account delete

Schedule account deletion

`gregale account delete [-q] [--quiet] [--yes]`

| Flag | Meaning | |
|---|---|---|
| `-q` | skip the confirmation prompt |  |
| `--quiet` | confirm account deletion without prompting |  |
| `--yes` | confirm account deletion without prompting |  |

### account restore

Cancel a pending deletion

### account status

Show account status

### account dpa

Show DPA metadata

`gregale account dpa [-o <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `-o <PATH>` | write DPA metadata to a file |  |

### account slo

Account-wide SLO panel


## add

Provision and bind managed resources to an app

`gregale add [<subcommand>]`

### add postgres

Provision or attach PostgreSQL and inject DATABASE_URL

`gregale add postgres --app <APP> [--env <SCOPE>] [--scope <SCOPE>] [--database <REF>] [--region <REGION>] [--postgres-major <N>] [--class <CLASS>] [--availability <MODE>] [--scale-to-zero] [--environment-key <KEY>] [--access <MODE>] [--wait-timeout <DURATION>]`

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
| `--access <MODE>` | credential access | one of `read_write` · `read_only` · `migration` · `data_api` |
| `--wait-timeout <DURATION>` | readiness timeout |  |

### add bucket

Provision or attach object storage and inject sealed S3 settings

`gregale add bucket --app <APP> [--env <SCOPE>] [--scope <SCOPE>] [--region <REGION>] [--public] [--serve-at <PATH>] [--permission <MODE>] [--label <LABEL>] [--prefix <PREFIX>] [--wait-timeout <DURATION>]`

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


## bucket

Manage object encryption, Object Lock, copy sources, tags, versioning, lifecycle rules, receipts and capacity

`gregale bucket [<subcommand>]`

### bucket uploads

Inspect owned multipart upload sessions and parts

#### bucket uploads list

List multipart sessions

`gregale bucket uploads list [--limit <N>] [--cursor <ID>] <app> <bucket-id>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1..1000) |  |
| `--cursor <ID>` | next page cursor |  |

#### bucket uploads status

Inspect a multipart session returned by an upload

`gregale bucket uploads status <app> <bucket-id> <upload-id>`

#### bucket uploads parts

List uploaded multipart parts

`gregale bucket uploads parts [--limit <N>] [--part-number-marker <N>] <app> <bucket-id> <upload-id>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1..1000) |  |
| `--part-number-marker <N>` | last part from the previous page |  |

### bucket upload

Upload a file with resumable multipart transfers

`gregale bucket upload [--content-type <TYPE>] [--resume <UPLOAD-ID>] [--timeout <DURATION>] <app> <bucket-id> <key> <file>`

| Flag | Meaning | |
|---|---|---|
| `--content-type <TYPE>` | object MIME type |  |
| `--resume <UPLOAD-ID>` | resume a multipart upload from its local checkpoint |  |
| `--timeout <DURATION>` | transfer deadline (default 30m) |  |

### bucket download

Download an object to a file after a complete transfer

`gregale bucket download [--version-id <VERSION>] [--force] [--timeout <DURATION>] <app> <bucket-id> <key> <file>`

| Flag | Meaning | |
|---|---|---|
| `--version-id <VERSION>` | download this owned immutable version |  |
| `--force` | replace destination after a complete transfer |  |
| `--timeout <DURATION>` | transfer deadline (default 30m) |  |

### bucket versions

Browse retained object versions and delete markers

#### bucket versions list

List a page of owned public versions

`gregale bucket versions list [--prefix <PREFIX>] [--delimiter <DELIMITER>] [--limit <N>] [--key-marker <KEY>] [--version-id-marker <VERSION>] <app> <bucket-id>`

| Flag | Meaning | |
|---|---|---|
| `--prefix <PREFIX>` | key prefix |  |
| `--delimiter <DELIMITER>` | group matching keys |  |
| `--limit <N>` | maximum items and prefixes (1-1000) |  |
| `--key-marker <KEY>` | continuation key from the previous page |  |
| `--version-id-marker <VERSION>` | public continuation version from the previous page |  |

### bucket copy-sources

Manage copy-only owned source grants

#### bucket copy-sources list

List source grants for a destination credential

`gregale bucket copy-sources list <app> <bucket-id> <credential-id>`

#### bucket copy-sources grant

Allow copying an owned source bucket or prefix

`gregale bucket copy-sources grant <app> <bucket-id> <credential-id> <source-bucket-id> [prefix]`

#### bucket copy-sources revoke

Prevent new copy dispatch from a source

`gregale bucket copy-sources revoke <app> <bucket-id> <credential-id> <source-bucket-id>`

### bucket encryption-keys

List owned encryption capabilities and key references

`gregale bucket encryption-keys <app> <bucket-id>`

### bucket encryption

Inspect or configure verified bucket encryption defaults

#### bucket encryption status

Show durable encryption progress

`gregale bucket encryption status <app> <bucket-id>`

#### bucket encryption clear

Remove the default for new writes

`gregale bucket encryption clear <app> <bucket-id>`

#### bucket encryption AES256

Set provider AES256 encryption

`gregale bucket encryption AES256 <app> <bucket-id>`

#### bucket encryption aws:kms

Set owned KMS encryption

`gregale bucket encryption aws:kms <app> <bucket-id> <owned-key-ref> [bucket-key-enabled]`

#### bucket encryption aws:kms:dsse

Set owned dual-layer KMS encryption

`gregale bucket encryption aws:kms:dsse <app> <bucket-id> <owned-key-ref>`

### bucket object-lock

Inspect permanent Object Lock and configure retention defaults

#### bucket object-lock status

Show durable Object Lock progress

`gregale bucket object-lock status <app> <bucket-id>`

#### bucket object-lock capabilities

Show enrolled bucket lock capabilities

`gregale bucket object-lock capabilities <app> <bucket-id>`

#### bucket object-lock enable

Permanently enable Object Lock without defaults

`gregale bucket object-lock enable <app> <bucket-id>`

#### bucket object-lock clear-default

Clear future defaults while keeping Object Lock enabled

`gregale bucket object-lock clear-default <app> <bucket-id>`

#### bucket object-lock GOVERNANCE

Set governance defaults

`gregale bucket object-lock GOVERNANCE [--days <N>] [--years <N>] [--event-days <N>] [--event-years <N>] <app> <bucket-id>`

| Flag | Meaning | |
|---|---|---|
| `--days <N>` | fixed retention in days |  |
| `--years <N>` | fixed retention in years |  |
| `--event-days <N>` | event hold duration in days |  |
| `--event-years <N>` | event hold duration in years |  |

#### bucket object-lock COMPLIANCE

Set compliance defaults

`gregale bucket object-lock COMPLIANCE [--days <N>] [--years <N>] [--event-days <N>] [--event-years <N>] <app> <bucket-id>`

| Flag | Meaning | |
|---|---|---|
| `--days <N>` | fixed retention in days |  |
| `--years <N>` | fixed retention in years |  |
| `--event-days <N>` | event hold duration in days |  |
| `--event-years <N>` | event hold duration in years |  |

### bucket protection

Manage exact version retention and legal holds

#### bucket protection status

Inspect a durable protection operation

`gregale bucket protection status <app> <bucket-id> <operation-id>`

#### bucket protection retention

Read, set or clear fixed retention

`gregale bucket protection retention <app> <bucket-id> <key> <version-id> [clear operation-id | GOVERNANCE|COMPLIANCE retain-until operation-id]`

#### bucket protection legal-hold

Read or change an independent legal hold

`gregale bucket protection legal-hold <app> <bucket-id> <key> <version-id> [ON|OFF operation-id]`

#### bucket protection event-hold

Set or release event retention for an exact version

`gregale bucket protection event-hold <app> <bucket-id> <key> <version-id> <GOVERNANCE|COMPLIANCE> <ON|OFF> [days|years duration] [--retain-until timestamp] <operation-id>`

### bucket reconcile

Start, inspect or cancel a fenced capacity inventory

#### bucket reconcile start

Pause writes and request capacity reconciliation

`gregale bucket reconcile start <app> <bucket-id>`

#### bucket reconcile status

Show reconciliation progress and reclaimed capacity

`gregale bucket reconcile status <app> <bucket-id> <job-id>`

#### bucket reconcile cancel

Cancel reconciliation and resume writes

`gregale bucket reconcile cancel <app> <bucket-id> <job-id>`

### bucket writes

Inspect tracked writes and await recovery

#### bucket writes list

List pending writes or recent completed and failed receipts

`gregale bucket writes list [--status <STATUS>] [--limit <N>] [--cursor <TOKEN>] <app> <bucket-id>`

| Flag | Meaning | |
|---|---|---|
| `--status <STATUS>` | pending (default), completed, failed or all |  |
| `--limit <N>` | page size (default 50, maximum 100) |  |
| `--cursor <TOKEN>` | next page cursor |  |

#### bucket writes status

Read a tracked write receipt

`gregale bucket writes status <app> <bucket-id> <receipt-id>`

#### bucket writes wait

Poll until completed or failed; pending timeout retains the receipt

`gregale bucket writes wait [--timeout <DURATION>] [--poll-interval <DURATION>] <app> <bucket-id> <receipt-id>`

| Flag | Meaning | |
|---|---|---|
| `--timeout <DURATION>` | maximum wait (default 5m) |  |
| `--poll-interval <DURATION>` | time between reads (default 5s, minimum 1s) |  |

### bucket tags

Read, replace or clear tags on current or selected data

#### bucket tags get

Read object tags

`gregale bucket tags get <app> <bucket-id> <key> [version-id|null]`

#### bucket tags set

Replace the complete tag set

`gregale bucket tags set <app> <bucket-id> <key> <URL-encoded-tags> [version-id|null]`

#### bucket tags clear

Remove all object tags

`gregale bucket tags clear <app> <bucket-id> <key> [version-id|null]`

### bucket deletions

Create or inspect durable object deletions

#### bucket deletions start

Delete current data or an owned version with a retry identity

`gregale bucket deletions start <app> <bucket-id> <key> <request-id> [version-id|null]`

#### bucket deletions status

Show a persisted deletion receipt

`gregale bucket deletions status <app> <bucket-id> <request-id>`

### bucket version-delete

Permanently delete an owned immutable version or marker

`gregale bucket version-delete <app> <bucket-id> <key> <version-id>`

### bucket notifications

Manage bucket event notifications

#### bucket notifications get

Read notification rules

`gregale bucket notifications get <app> <bucket-id>`

#### bucket notifications set

Replace rules from a JSON file or stdin

`gregale bucket notifications set <app> <bucket-id> <JSON-file|->`

#### bucket notifications clear

Remove notification rules

`gregale bucket notifications clear <app> <bucket-id>`

### bucket lifecycle

Manage lifecycle rules and discovery progress

#### bucket lifecycle get

Read the complete lifecycle policy

`gregale bucket lifecycle get <app> <bucket-id>`

#### bucket lifecycle set

Replace rules from a JSON file or stdin

`gregale bucket lifecycle set <app> <bucket-id> <JSON-file|->`

#### bucket lifecycle clear

Remove rules; admitted cleanup continues

`gregale bucket lifecycle clear <app> <bucket-id>`

#### bucket lifecycle scan

Start or resume due discovery

`gregale bucket lifecycle scan <app> <bucket-id>`

#### bucket lifecycle status

Read discovery progress

`gregale bucket lifecycle status <app> <bucket-id> <scan-id>`

### bucket versioning

Inspect or configure bucket versioning

#### bucket versioning status

Show durable versioning progress

`gregale bucket versioning status <app> <bucket-id>`

#### bucket versioning enable

Enable retained versions

`gregale bucket versioning enable <app> <bucket-id>`

#### bucket versioning suspend

Suspend versioning while retaining older versions

`gregale bucket versioning suspend <app> <bucket-id>`


## bindings

Inspect app bindings, verification, runtime freshness, and rotation progress

`gregale bindings [<subcommand>] <app> [--require-complete] [--scope <SCOPE>]`

| Flag | Meaning | |
|---|---|---|
| `--require-complete` | fail if binding metadata, verification, runtime freshness or refresh progress is incomplete |  |
| `--scope <SCOPE>` | filter resource bindings by environment scope; app-wide bindings remain included |  |

### bindings release-policy

Require fresh binding evidence for traffic increases in a scope

#### bindings release-policy get

Read the stored release policy

`gregale bindings release-policy get [--scope <SCOPE>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | deployment scope (default default) |  |

#### bindings release-policy set

Replace the release policy using its current revision

`gregale bindings release-policy set [--scope <SCOPE>] [--mode <off|enforce>] [--require-verification] [--max-age <DURATION>] [--require-application-ack] --expected-revision <N> [--reason <TEXT>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | deployment scope (default default) |  |
| `--mode <off|enforce>` | disable or enable enforcement |  |
| `--require-verification` | alias for --mode enforce |  |
| `--max-age <DURATION>` | maximum verification age (default 10m; 1s to 24h) |  |
| `--require-application-ack` | require current application acknowledgements |  |
| `--expected-revision <N>` | current policy revision; use 0 initially | required |
| `--reason <TEXT>` | update reason; required when disabling enforcement |  |

Examples:

```sh
gregale bindings release-policy set public-api --scope production --require-verification --max-age 10m --expected-revision 0
```

### bindings probe-policy

Configure or remove an outbound integration probe

`gregale bindings probe-policy [--path <PATH>] [--method <METHOD>] [--expect-status <STATUS>] [--delete] <integration-id>`

| Flag | Meaning | |
|---|---|---|
| `--path <PATH>` | provider path declared safe to probe |  |
| `--method <METHOD>` | GET or HEAD (default GET) |  |
| `--expect-status <STATUS>` | expected successful response status (default 200) |  |
| `--delete` | remove probe configuration |  |

Examples:

```sh
gregale bindings probe-policy INTEGRATION_ID --path /health --method GET --expect-status 200
```

### bindings check

Evaluate recorded binding evidence and runtime freshness for CI

`gregale bindings check [--scope <SCOPE>] [--max-verification-age <DURATION>] [--deployment <ID|vN>] [--allow-unsupported] [--require-application-ack] [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | require the selected deployment to use this scope (default its current scope) |  |
| `--max-verification-age <DURATION>` | maximum age of passed probe evidence (default 10m) |  |
| `--deployment <ID|vN>` | exact live deployment whose evidence must pass, including zero-traffic candidates |  |
| `--allow-unsupported` | waive connectivity coverage for active queue and outbound bindings |  |
| `--require-application-ack` | require current PostgreSQL/object-storage application acknowledgements |  |
| `--wait` | poll read-only inventory while probes, refreshes or application acknowledgements are pending |  |
| `--timeout <DURATION>` | maximum preflight wait (default 5m) |  |
| `--poll-interval <DURATION>` | inventory polling interval with --wait (default 1s) |  |

Examples:

```sh
gregale bindings check my-api --max-verification-age 10m --json
gregale bindings check my-api --scope production
gregale bindings check my-api --deployment v12 --max-verification-age 10m --json
```

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

`gregale bindings object-storage rotate [--wait] [--wait-timeout <DURATION>] [--poll-interval <DURATION>] <app> <bucket> <binding-id>`

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

Check a private service route or test a managed PostgreSQL or object-storage binding

`gregale bindings verify [--all] [--postgres <ENVIRONMENT_KEY>] [--outbound <INTEGRATION_ID>] [--object-storage <PREFIX>] [--deployment <ID|vN>] [--poll-interval <D>] [--wait-timeout <D>] <app> [<service>]`

| Flag | Meaning | |
|---|---|---|
| `--all` | verify services, managed PostgreSQL, object storage and configured outbound bindings |  |
| `--postgres <ENVIRONMENT_KEY>` | verify one managed PostgreSQL binding by environment key |  |
| `--outbound <INTEGRATION_ID>` | verify a configured outbound integration by UUID |  |
| `--object-storage <PREFIX>` | verify one object-storage binding by environment prefix (read access only) |  |
| `--deployment <ID|vN>` | exact live deployment to verify, including zero-traffic candidates |  |
| `--poll-interval <D>` | status polling interval while the canary runs |  |
| `--wait-timeout <D>` | maximum time to wait for the canary task |  |

Examples:

```sh
gregale bindings verify my-api billing
gregale bindings verify my-api --all
gregale bindings verify my-api --deployment v12 --all
gregale bindings verify my-api --postgres DATABASE_URL
gregale bindings verify my-api --object-storage GREGALE_S3_ASSETS
```

### bindings smoke

Invoke a path on one exact live target deployment over the private HTTPS binding

`gregale bindings smoke [--target-deployment <ID>] [--deployment <ID>] [--caller-deployment <ID|vN>] --path <PATH> [--expect-status <CODE>] [--poll-interval <D>] [--wait-timeout <D>] <app> <service>`

| Flag | Meaning | |
|---|---|---|
| `--target-deployment <ID>` | exact live target deployment UUID to invoke (or use --deployment) |  |
| `--deployment <ID>` | alias for --target-deployment |  |
| `--caller-deployment <ID|vN>` | exact live caller deployment, including zero-traffic candidates |  |
| `--path <PATH>` | absolute path on the target service | required |
| `--expect-status <CODE>` | require this exact HTTP status; default accepts any 2xx response |  |
| `--poll-interval <D>` | status polling interval while the smoke task runs |  |
| `--wait-timeout <D>` | maximum time to wait for the smoke task |  |

Examples:

```sh
gregale bindings smoke public-api billing --caller-deployment v12 --target-deployment TARGET_UUID --path /ready --expect-status 200
```


## capabilities

Show feature maturity and plan availability

`gregale capabilities`


## alerts

Per-app alert rules (alerts list|add|info|update|rm|rotate-secret|preset|actions --app &lt;slug&gt;)

`gregale alerts [<subcommand>] [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug |  |

### alerts actions

Read or wait for automatic rollback status, deployment evidence and service handoffs

`gregale alerts actions --app <slug> [--fire <UUID>] [--wait] [--timeout <duration>] [--poll-interval <duration>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--fire <UUID>` | one production alert delivery UUID |  |
| `--wait` | wait for the selected fire to complete |  |
| `--timeout <duration>` | wait deadline (default 10m) |  |
| `--poll-interval <duration>` | poll interval (default 2s) |  |

### alerts list

List alert rules

`gregale alerts list --app <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### alerts add

Add an alert rule

`gregale alerts add --app <slug> --name <NAME> [--metric <METRIC>] [--comparison <OP>] [--threshold <N>] [--window-spec <WINDOW>] [--failure-source <SOURCE>] --webhook-url <URL> [--action <ACTION>] [--post-deploy-rollback-window <duration>] [--webhook-secret-stdin] [--webhook-secret <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--name <NAME>` | rule name (3..120 chars) | required |
| `--metric <METRIC>` | metric, e.g. error_rate_pct or latency_p95_ms |  |
| `--comparison <OP>` | gt\|gte\|lt\|lte |  |
| `--threshold <N>` | threshold value |  |
| `--window-spec <WINDOW>` | 5m\|15m\|1h\|6h\|24h\|7d\|15d |  |
| `--failure-source <SOURCE>` | any\|cron\|queue\|delayed_task\|async_invoke\|inbound_webhook |  |
| `--webhook-url <URL>` | https webhook URL | required |
| `--action <ACTION>` | alert action | one of `webhook` · `rollback` · `demote` · `promote` |
| `--post-deploy-rollback-window <duration>` | completed-release rollback window (0 off; up to 1h) |  |
| `--webhook-secret-stdin` | read the webhook signing secret from stdin (this or --webhook-secret is required) |  |
| `--webhook-secret <VALUE>` | webhook signing secret (prefer --webhook-secret-stdin) |  |

Examples:

```sh
printf '%s\n' "$WEBHOOK_SECRET" | gregale alerts add --app my-api --name p95-latency --metric latency_p95_ms --comparison gt --threshold 800 --window-spec 15m --webhook-url https://hooks.example.com/gregale --webhook-secret-stdin
```

### alerts info

Show one alert rule and its last delivery

`gregale alerts info --app <slug> <alert-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### alerts deliveries

List a rule&#39;s webhook deliveries, newest first

`gregale alerts deliveries --app <slug> [--limit <N>] [--include-test] <alert-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--limit <N>` | max deliveries (1..100, default 20) |  |
| `--include-test` | include test deliveries |  |

### alerts update

Update one alert rule

`gregale alerts update [--action <ACTION>] [--post-deploy-rollback-window <duration>] [--webhook-secret-stdin] <alert-id>`

| Flag | Meaning | |
|---|---|---|
| `--action <ACTION>` | alert action | one of `webhook` · `rollback` · `demote` · `promote` |
| `--post-deploy-rollback-window <duration>` | completed-release rollback window (0 off; up to 1h) |  |
| `--webhook-secret-stdin` | read the replacement webhook secret from stdin |  |

### alerts rm

Delete one alert rule

`gregale alerts rm <alert-id>`

### alerts rotate-secret

Rotate the alert&#39;s webhook secret

`gregale alerts rotate-secret --app <slug> [--from-stdin]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--from-stdin` | read the replacement secret from stdin |  |

### alerts preset

Browse or enable catalog alert presets

#### alerts preset list

List the global alert preset catalog

`gregale alerts preset list`

#### alerts preset enable

Create an app alert from a preset; supply a webhook secret via stdin or --webhook-secret

`gregale alerts preset enable --app <slug> --webhook-url <URL> [--webhook-secret-stdin] [--webhook-secret <VALUE>] [--action <ACTION>] [--cooldown-minutes <N>] [--enabled] <preset-name>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--webhook-url <URL>` | HTTPS webhook receiver URL | required |
| `--webhook-secret-stdin` | read the webhook signing secret from stdin |  |
| `--webhook-secret <VALUE>` | signing secret (prefer --webhook-secret-stdin) |  |
| `--action <ACTION>` | alert action (default webhook) | one of `webhook` · `rollback` · `demote` · `promote` |
| `--cooldown-minutes <N>` | cooldown override; 0 uses the preset default |  |
| `--enabled` | rule is enabled by default; --enabled=false disables it |  |


## audit-events

Audit-log query (audit-events list|get &lt;id&gt;)

`gregale audit-events [<subcommand>] [<id>]`

### audit-events list

List audit events

`gregale audit-events list [--kind-prefix <PREFIX>] [--app-id <UUID>] [--since <RFC3339>] [--limit <N>] [--include-anonymous] [--verbose]`

| Flag | Meaning | |
|---|---|---|
| `--kind-prefix <PREFIX>` | filter by kind prefix |  |
| `--app-id <UUID>` | filter by app UUID |  |
| `--since <RFC3339>` | RFC3339 lower bound |  |
| `--limit <N>` | maximum rows (1..100; default 50) |  |
| `--include-anonymous` | include rows without a subject |  |
| `--verbose` | expand stateless advisory rows |  |

### audit-events get

Show one audit event

`gregale audit-events get <id>`


## commit

Manage transactional PostgreSQL outbox sources (internal)

`gregale commit [<subcommand>]`

### commit add

Register a fixed app destination

`gregale commit add --name <NAME> --operation-policy <NAME> [--contract-version <VERSION>] [--allow-tenant-selection]`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | account source name | required |
| `--operation-policy <NAME>` | managed Operations queue policy | required |
| `--contract-version <VERSION>` | immutable source contract version (default 1) | one of `1` · `2` |
| `--allow-tenant-selection` | grant version 2 account-owner customer selection |  |

### commit connection

Seal database credentials from a file

`gregale commit connection --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | connection URL file | required |

### commit pause

Pause new acceptance

### commit resume

Resume new acceptance

### commit info

Inspect source health and pending/blocked work

### commit doctor

Read-only source diagnostics and optional local database checks

`gregale commit doctor [--file <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | local TLS PostgreSQL credential file; never uploaded |  |

### commit inspect

Inspect event acceptance, retained execution and blocked observations

### commit wait

Wait without cancelling durable work on timeout

`gregale commit wait [--until <STATE>] [--timeout <D>] [--interval <D>]`

| Flag | Meaning | |
|---|---|---|
| `--until <STATE>` | accepted or completed |  |
| `--timeout <D>` | maximum wait (default 2m) |  |
| `--interval <D>` | poll interval (default 1s) |  |

### commit blocked

Inspect the bounded blocked-event snapshot

### commit replay

Request durable replay after correcting an unaccepted event

### commit receipt

Recover a source event acceptance receipt

### commit operation

Inspect durable execution status


## events

Preview routing, publish events, inspect deliveries, and backfill retained events

`gregale events [<subcommand>]`

### events preview

Preview account-wide event routing without publishing

`gregale events preview [--id <ID>] [--source <SOURCE>] [--type <TYPE>] --data <J|@file|-> [--time <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--id <ID>` | event id to use when filters inspect the CloudEvents id |  |
| `--source <SOURCE>` | event source (or first positional argument) |  |
| `--type <TYPE>` | event type (or second positional argument) |  |
| `--data <J|@file|->` | JSON event data (inline \| @file \| -) | required |
| `--time <RFC3339>` | event time (RFC3339; defaults to server time) |  |

### events replay-preview

Preview retained events for one current subscription without creating deliveries

`gregale events replay-preview --subscription-id <UUID> --from <RFC3339> --until <RFC3339> [--after <CURSOR>] [--limit <N>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--subscription-id <UUID>` | target ordinary application subscription UUID | required |
| `--from <RFC3339>` | inclusive acceptance timestamp | required |
| `--until <RFC3339>` | exclusive acceptance timestamp | required |
| `--after <CURSOR>` | opaque continuation cursor; keep target and range unchanged |  |
| `--limit <N>` | envelopes examined per page (1..100; default 50) |  |

### events backfill

Create a durable bounded delivery job for matching retained events

`gregale events backfill --subscription-id <UUID> --from <RFC3339> --until <RFC3339> --yes <app>`

| Flag | Meaning | |
|---|---|---|
| `--subscription-id <UUID>` | target ordinary application subscription UUID | required |
| `--from <RFC3339>` | inclusive platform acceptance timestamp | required |
| `--until <RFC3339>` | exclusive platform acceptance timestamp | required |
| `--yes` | confirm that matching historical events may invoke this consumer | required |

### events backfill-status

Read durable event backfill progress

`gregale events backfill-status <job-id>`

### events backfill-items

Inspect paginated per-envelope outcomes for a backfill job

`gregale events backfill-items [--state <STATE>] [--limit <N>] [--after <CURSOR>] <job-id>`

| Flag | Meaning | |
|---|---|---|
| `--state <STATE>` | filter by a routing outcome state |  |
| `--limit <N>` | items per page (1..100; default 50) |  |
| `--after <CURSOR>` | opaque continuation cursor; keep job and state unchanged |  |

### events backfill-retry

Retry a bounded batch of failed backfill deliveries

`gregale events backfill-retry [--limit <N>] --yes <job-id>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | failed routing recipients to requeue (1..100; default 100) |  |
| `--yes` | confirm requeueing failed event deliveries | required |

### events publish

Publish one event (SOURCE TYPE can be positional; ID is generated by default)

`gregale events publish [--id <ID>] [--source <SOURCE>] [--type <TYPE>] --data <J|@file|-> [--time <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--id <ID>` | stable event id (generated when omitted) |  |
| `--source <SOURCE>` | event source (or first positional argument) |  |
| `--type <TYPE>` | event type (or second positional argument) |  |
| `--data <J|@file|->` | JSON event data (inline \| @file \| -) | required |
| `--time <RFC3339>` | event time (RFC3339; defaults to server time) |  |

### events backlog

Discover waiting event recipients and consumer counts

`gregale events backlog [--app <APP>] [--subscription-id <ID>] [--state <STATE>] [--capacity-scope <SCOPE>] [--min-age <DURATION>] [--after <CURSOR>] [--consumers-after <CURSOR>] [--limit <N>] [--consumer-limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <APP>` | filter by owned app slug |  |
| `--subscription-id <ID>` | filter by captured recipient identifier |  |
| `--state <STATE>` | pending or processing |  |
| `--capacity-scope <SCOPE>` | consumer, app or account |  |
| `--min-age <DURATION>` | minimum acceptance age in whole seconds (e.g. 10m) |  |
| `--after <CURSOR>` | opaque recipient continuation cursor |  |
| `--consumers-after <CURSOR>` | opaque consumer continuation cursor |  |
| `--limit <N>` | recipients per page (1..200, default 100) |  |
| `--consumer-limit <N>` | consumers per page (1..200, default 100) |  |

### events inspect

Inspect event routing, execution and replay recovery

`gregale events inspect --source <SOURCE> --id <ID> [--subscription <SUB>] [--after <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--source <SOURCE>` | published event source | required |
| `--id <ID>` | published event id | required |
| `--subscription <SUB>` | list retained handler replays for one captured recipient |  |
| `--after <CURSOR>` | opaque next_after cursor for recipients or replays |  |
| `--limit <N>` | max recipients or replays (1..200, default 100) |  |

### events recover

Recover one event consumer using its current receipt action

`gregale events recover --source <SOURCE> --id <ID> --subscription <SUB> [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--source <SOURCE>` | published event source | required |
| `--id <ID>` | published event id | required |
| `--subscription <SUB>` | captured recipient identifier | required |
| `--dry-run` | show recovery availability and action without replaying |  |

### events attempts

Inspect retained handler attempts, including retries and replay

`gregale events attempts --source <SOURCE> --id <ID> --subscription <SUB> [--after <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--source <SOURCE>` | published event source | required |
| `--id <ID>` | published event id | required |
| `--subscription <SUB>` | captured recipient identifier | required |
| `--after <CURSOR>` | opaque next_after attempt cursor |  |
| `--limit <N>` | max attempts (1..200, default 100) |  |

### events subscriptions

List subscriptions reconciled from the app manifest

`gregale events subscriptions <app>`

### events deliveries

Inspect event deliveries, replays, and pre-invocation fanout failures

`gregale events deliveries [--event-source <SOURCE>] [--event-id <ID>] [--state <STATE>] [--before <CURSOR>] [--fanout-before <CURSOR>] [--limit <N>] [--all] <app>`

| Flag | Meaning | |
|---|---|---|
| `--event-source <SOURCE>` | narrow event filter to one published source; requires --event-id |  |
| `--event-id <ID>` | filter by published event id |  |
| `--state <STATE>` | filter by delivery state; failed includes recipient fanout failures |  |
| `--before <CURSOR>` | pagination cursor |  |
| `--fanout-before <CURSOR>` | pre-invocation failure pagination cursor |  |
| `--limit <N>` | page size per stream (1..200, default 20) |  |
| `--all` | walk both streams with independent cursors |  |

### events fanout-history

Inspect immutable routing outcomes and replay history for one event

`gregale events fanout-history --event-source <SOURCE> --event-id <ID> [--subscription-id <ID>] [--before <CURSOR>] [--cursor <CURSOR>] [--all] [--limit <N>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--event-source <SOURCE>` | published event source | required |
| `--event-id <ID>` | published event id | required |
| `--subscription-id <ID>` | narrow history to one recipient |  |
| `--before <CURSOR>` | alias for --cursor |  |
| `--cursor <CURSOR>` | opaque continuation cursor |  |
| `--all` | walk every page using --limit and --cursor |  |
| `--limit <N>` | max history rows (1..200) |  |

### events replay

Retry one terminal pre-invocation recipient failure using its event identity and subscription ID from events deliveries

`gregale events replay --event-id <ID> --event-source <SOURCE> --subscription-id <ID> <app>`

| Flag | Meaning | |
|---|---|---|
| `--event-id <ID>` | published event id | required |
| `--event-source <SOURCE>` | published event source | required |
| `--subscription-id <ID>` | failed subscription id | required |

### events replay-retryable

Retry a bounded batch of terminal failures classified as retryable; pass --event-source and --event-id together to filter

`gregale events replay-retryable [--event-source <SOURCE>] [--event-id <ID>] [--limit <N>] --yes <app>`

| Flag | Meaning | |
|---|---|---|
| `--event-source <SOURCE>` | limit replay to one published event source |  |
| `--event-id <ID>` | limit replay to one published event |  |
| `--limit <N>` | max recipients to requeue (1..100) |  |
| `--yes` | confirm requeueing retryable event recipients | required |


## send

Reliably send work to another Gregale application

`gregale send <target-app> --type <TYPE> --data <J|@file|-> [--id <ID>] [--source <SOURCE>] [--time <RFC3339>] [--queue-name <QUEUE>] [--environment <ENV>] [--work-policy <NAME>] [--work-key <JSON>] [--work-fairness-key <JSON>] [--idempotency-key <KEY>]`

| Flag | Meaning | |
|---|---|---|
| `--type <TYPE>` | event type | required |
| `--data <J|@file|->` | JSON event data (inline \| @file \| -) | required |
| `--id <ID>` | stable event id |  |
| `--source <SOURCE>` | event source |  |
| `--time <RFC3339>` | event time |  |
| `--queue-name <QUEUE>` | target logical queue name |  |
| `--environment <ENV>` | registered project environment with an enabled queue binding |  |
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

`gregale apps [<subcommand>] [--dry-run] [--quiet|-q] [--yes]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview app deletion without changing resources |  |
| `--quiet|-q` | delete one app without prompting |  |
| `--yes` | confirm app deletion without prompting |  |

Examples:

```sh
gregale apps
gregale apps --json
gregale apps -q my-api
```

### apps ls

Alias for the default list action

### apps restore

Restore an app during its deletion grace window

`gregale apps restore <slug>`

### apps routes

List admitted per-route labels for one app

`gregale apps routes <slug>`

### apps tcp

Manage raw TCP listeners for an app

`gregale apps tcp <slug> <list|add|tls|tls-status|enable|disable|rm|delete>`

#### apps tcp list

List TCP listeners

`gregale apps tcp <slug> list`

#### apps tcp add

Create a TCP listener

`gregale apps tcp <slug> add --name <NAME> --guest-port <PORT> [--public-port <PORT>] [--tls-mode <MODE>] [--tls-hostname <HOST>]`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | listener name | required |
| `--guest-port <PORT>` | workload TCP port | required |
| `--public-port <PORT>` | stable public TCP port (40000..49999) |  |
| `--tls-mode <MODE>` | TLS mode (default passthrough) | one of `passthrough` · `terminate` |
| `--tls-hostname <HOST>` | verified app-owned hostname for TLS termination |  |

#### apps tcp tls

Update one listener&#39;s TLS policy

`gregale apps tcp <slug> tls <name> --tls-mode <MODE> [--tls-hostname <HOST>]`

| Flag | Meaning | |
|---|---|---|
| `--tls-mode <MODE>` | required TLS mode | required; one of `passthrough` · `terminate` |
| `--tls-hostname <HOST>` | verified app-owned termination hostname |  |

#### apps tcp tls-status

Show certificate observations for a listener

`gregale apps tcp <slug> tls-status <name>`

#### apps tcp enable

Enable one listener

`gregale apps tcp <slug> enable <name>`

#### apps tcp disable

Disable one listener

`gregale apps tcp <slug> disable <name>`

#### apps tcp rm

Delete one listener

`gregale apps tcp <slug> rm <name>`

#### apps tcp delete

Alias for rm

`gregale apps tcp <slug> delete <name>`

### apps udp

Manage raw UDP listeners for an app

`gregale apps udp <slug> <list|add|enable|disable|rm|delete>`

#### apps udp list

List UDP listeners

`gregale apps udp <slug> list`

#### apps udp add

Create a UDP listener

`gregale apps udp <slug> add --name <NAME> --guest-port <PORT> [--public-port <PORT>]`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | listener name | required |
| `--guest-port <PORT>` | workload UDP port | required |
| `--public-port <PORT>` | stable public UDP port |  |

#### apps udp enable

Enable one listener

`gregale apps udp <slug> enable <name>`

#### apps udp disable

Disable one listener

`gregale apps udp <slug> disable <name>`

#### apps udp rm

Delete one listener

`gregale apps udp <slug> rm <name>`

#### apps udp delete

Alias for rm

`gregale apps udp <slug> delete <name>`

### apps streaming-cap

Show app streaming classification

`gregale apps streaming-cap <slug>`


## app

Get/update one app or run a deployment-attached command

`gregale app <slug> [<subcommand>] [--concurrency] [--environment <SLUG>] [--visibility <public|internal>] [--profile <micro|small|medium|large|xlarge>] [--ram <MB>] [--cpu-millicores <250|500|1000>] [--max-concurrency <N>] [--concurrency-overflow <value>] [--max-queue-depth <N>] [--max-queue-wait <DURATION>] [--max-queue-wait-ms <N>] [--wake-max-queue-depth <N>] [--wake-max-queue-wait-seconds <N>] [--idle <SEC>] [--request-timeout <SEC>] [--require-signed <value>] [--security-policy <value>] [--basic-user <USER>] [--basic-pass <PASS>] [--min <N>] [--autoscale-target-rps <N>] [--autoscale-target-cpu-pct <1..100>] [--warm-snapshot] [--no-warm-snapshot] [--warm-snapshot-min-requests <N>] [--warm-snapshot-min-ms <MS>] [--warm-pool-size <N>] [--eviction-priority <best_effort|reserved>] [--require-authn] [--no-require-authn] [--platform-tenant-required] [--no-platform-tenant-required] [--maintenance] [--no-maintenance] [--streaming-enabled] [--no-streaming-enabled] [--websocket-enabled] [--no-websocket] [--route-metrics] [--no-route-metrics] [--consumer-auth-mode <optional|required>] [--only-declared-routes] [--no-only-declared-routes] [--head-wakes[=true|false]] [--crawler-policy <wake|cached|block>] [--health-path <PATH>] [--health-path-wakes] [--no-health-path-wakes] [--app-protocol <http1|http2|grpc>] [--public-auth <open|bearer|basic|ip_allowlist|internal_only>] [--ip-allowlist <CIDR>]... [--overflow-node <NAME>]`

| Flag | Meaning | |
|---|---|---|
| `--concurrency` | print only the per-VM concurrency bound for the app&#39;s plan |  |
| `--environment <SLUG>` | read or edit desired workload settings in a project environment |  |
| `--visibility <public|internal>` | set public edge exposure | one of `public` · `internal` |
| `--profile <micro|small|medium|large|xlarge>` | set a named RAM/CPU profile | one of `micro` · `small` · `medium` · `large` · `xlarge` |
| `--ram <MB>` | set RAM in MB |  |
| `--cpu-millicores <250|500|1000>` | set sustained CPU allowance | one of `250` · `500` · `1000` |
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
| `--head-wakes[=true|false]` | wake a parked app for HEAD / | one of `true` · `false` |
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
gregale app my-api --environment staging --ram 512
gregale app my-api --no-maintenance --streaming-enabled --websocket-enabled --route-metrics
gregale app my-api --consumer-auth-mode required --json
```

### app health

Explain default-scope serving health and missing evidence

`gregale app <slug> health`

### app scale

Preview, save, apply or update app resource and runtime settings

`gregale app <slug> scale [--plan] [--out <PATH>] [--apply <PATH>] [--confirm] [--environment <SLUG>] [--profile <PROFILE>] [--ram <MB>] [--cpu-millicores <250|500|1000>] [--max-concurrency <N>] [--concurrency-overflow <POLICY>] [--max-queue-depth <N>] [--max-queue-wait <DURATION>] [--max-queue-wait-ms <MS>] [--wake-max-queue-depth <N>] [--wake-max-queue-wait-seconds <SECONDS>] [--idle <SECONDS>] [--request-timeout <SECONDS>] [--min <N>] [--autoscale-target-rps <N>] [--autoscale-target-cpu-pct <1..100>] [--warm-snapshot] [--no-warm-snapshot] [--warm-snapshot-min-requests <N>] [--warm-snapshot-min-ms <MS>] [--warm-pool-size <N>] [--require-authn] [--no-require-authn] [--head-wakes[=true|false]] [--crawler-policy <POLICY>] [--health-path <PATH>] [--health-path-wakes] [--no-health-path-wakes] [--app-protocol <PROTOCOL>]`

| Flag | Meaning | |
|---|---|---|
| `--plan` | show changes and supported plan effects without applying them |  |
| `--out <PATH>` | write a reusable plan JSON to a new file (requires --plan) |  |
| `--apply <PATH>` | apply a saved scale plan JSON file |  |
| `--confirm` | confirm applying the saved plan (requires --apply) |  |
| `--environment <SLUG>` | edit desired workload settings in a project environment |  |
| `--profile <PROFILE>` | named RAM/CPU profile | one of `micro` · `small` · `medium` · `large` · `xlarge` |
| `--ram <MB>` | RAM in MB |  |
| `--cpu-millicores <250|500|1000>` | sustained CPU allowance | one of `250` · `500` · `1000` |
| `--max-concurrency <N>` | maximum concurrent requests |  |
| `--concurrency-overflow <POLICY>` | saturated concurrency behavior | one of `queue` · `drop` |
| `--max-queue-depth <N>` | maximum queued requests at warm saturation |  |
| `--max-queue-wait <DURATION>` | maximum warm-saturation wait |  |
| `--max-queue-wait-ms <MS>` | maximum queued concurrency wait |  |
| `--wake-max-queue-depth <N>` | per-app cold-wake waiter cap |  |
| `--wake-max-queue-wait-seconds <SECONDS>` | per-app cold-wake wait budget |  |
| `--idle <SECONDS>` | idle timeout |  |
| `--request-timeout <SECONDS>` | per-app request timeout |  |
| `--min <N>` | minimum warm instances |  |
| `--autoscale-target-rps <N>` | per-instance RPS scale-up target |  |
| `--autoscale-target-cpu-pct <1..100>` | per-instance CPU scale-up target |  |
| `--warm-snapshot` | enable warm-snapshot tier |  |
| `--no-warm-snapshot` | disable warm-snapshot tier |  |
| `--warm-snapshot-min-requests <N>` | warm-snapshot minimum request gate |  |
| `--warm-snapshot-min-ms <MS>` | warm-snapshot ready-time gate |  |
| `--warm-pool-size <N>` | paused warm-pool size |  |
| `--require-authn` | require a bearer token on each request |  |
| `--no-require-authn` | remove the bearer-token requirement |  |
| `--head-wakes[=true|false]` | wake a parked app for HEAD requests | one of `true` · `false` |
| `--crawler-policy <POLICY>` | monitor/crawler wake policy | one of `wake` · `cached` · `block` |
| `--health-path <PATH>` | monitor-facing health path |  |
| `--health-path-wakes` | allow health probes to wake the app |  |
| `--no-health-path-wakes` | answer health probes without waking |  |
| `--app-protocol <PROTOCOL>` | wire-protocol selector | one of `http1` · `http2` · `grpc` |

Examples:

```sh
gregale app my-api scale --plan --ram 512 --out scale-change.json
gregale app my-api scale --apply scale-change.json --confirm
```

### app costs

Show this app&#39;s attributed usage costs and source coverage

`gregale app <slug> costs [--month <YYYY-MM>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--month <YYYY-MM>` | UTC usage month (defaults to current) |  |
| `--json` | Print the machine-readable app cost report |  |

Examples:

```sh
gregale app my-api costs
gregale app my-api costs --month 2026-10 --json
```

### app rename

Rename an app

`gregale app <slug> rename`

### app restart

Request a snapshot restart, or track a fresh runtime-configuration restart

`gregale app <slug> restart <status> [--fresh] [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--fresh` | cold-boot replacements with current runtime configuration |  |
| `--wait` | wait for processing; requires --fresh |  |
| `--timeout <DURATION>` | client deadline (default 10m) |  |
| `--poll-interval <DURATION>` | status polling interval (default 2s) |  |
| `--json` | print the accepted ID or last observed restart receipt |  |

#### app restart status

Follow an accepted fresh restart without submitting another request

`gregale app <slug> restart status --wake-id <UUID> [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--wake-id <UUID>` | accepted fresh restart UUID | required |
| `--wait` | wait for processing completion, separately from application health |  |
| `--timeout <DURATION>` | client deadline (default 10m) |  |
| `--poll-interval <DURATION>` | status polling interval (default 2s) |  |
| `--json` | print the last observed restart receipt |  |

### app exec

Run a one-off command against the live deployment

`gregale app <slug> exec [--shell] [--detach] [--timeout-seconds <N>] [--max-output-bytes <N>] [--operation-policy <NAME>] [--operation-key <JSON>] [--equivalence-key <KEY>] [--idempotency-key <KEY>] [--poll-interval <D>] [--wait-timeout <D>]`

| Flag | Meaning | |
|---|---|---|
| `--shell` | interpret one command string through the app shell |  |
| `--detach` | return after the task is queued |  |
| `--timeout-seconds <N>` | server-side command timeout |  |
| `--max-output-bytes <N>` | combined stdout/stderr tail cap |  |
| `--operation-policy <NAME>` | route through a managed exclusive-operation policy |  |
| `--operation-key <JSON>` | JSON scalar business coordination key |  |
| `--equivalence-key <KEY>` | equivalent request identity for join_existing policies |  |
| `--idempotency-key <KEY>` | stable retry identity for this submission |  |
| `--poll-interval <D>` | status polling interval while attached |  |
| `--wait-timeout <D>` | maximum attached wait |  |

### app security

Show posture or configure deploy enforcement

`gregale app <slug> security [--posture] [--require-signed <true|false>] [--security-policy <off|warn|enforce>]`

| Flag | Meaning | |
|---|---|---|
| `--posture` | show the read-only security posture |  |
| `--require-signed <true|false>` | require signed images on deploy | one of `true` · `false` |
| `--security-policy <off|warn|enforce>` | deploy posture policy | one of `off` · `warn` · `enforce` |

### app egress-allowlist

Inspect or update the outbound CIDR allowlist

`gregale app <slug> egress-allowlist <show|add|remove|clear>`

#### app egress-allowlist show

Show the current app egress policy

`gregale app <slug> egress-allowlist show`

#### app egress-allowlist add

Add one allowed destination

`gregale app <slug> egress-allowlist add <cidr>`

#### app egress-allowlist remove

Remove one allowed destination

`gregale app <slug> egress-allowlist remove <cidr>`

#### app egress-allowlist clear

Clear the configured egress policy

`gregale app <slug> egress-allowlist clear`

### app egress-ports

Inspect or update the extra outbound TCP ports (Pro/Scale)

`gregale app <slug> egress-ports <show|add|remove|clear>`

#### app egress-ports show

Show the current app egress policy

`gregale app <slug> egress-ports show`

#### app egress-ports add

Add one allowed destination

`gregale app <slug> egress-ports add <port>`

#### app egress-ports remove

Remove one allowed destination

`gregale app <slug> egress-ports remove <port>`

#### app egress-ports clear

Clear the configured egress policy

`gregale app <slug> egress-ports clear`

### app network

Inspect networking or manage private-network attachments

`gregale app <slug> network <show|doctor|attach|detach>`

#### app network show

Show network configuration and captured upstream telemetry

`gregale app <slug> network show`

#### app network doctor

Check observed network health without sending probe traffic

`gregale app <slug> network doctor`

#### app network attach

Request a private-network attachment

`gregale app <slug> network attach --region <REGION> --cidrs <CIDRS> <network-id>`

| Flag | Meaning | |
|---|---|---|
| `--region <REGION>` | private network region | required |
| `--cidrs <CIDRS>` | comma-separated private network CIDRs | required |

#### app network detach

Remove the private-network attachment

`gregale app <slug> network detach`

### app static-egress-ip

Inspect or pin a static outbound address (Pro/Scale)

`gregale app <slug> static-egress-ip <show|set|clear>`

#### app static-egress-ip show

Show the pinned address and plan eligibility

`gregale app <slug> static-egress-ip show`

#### app static-egress-ip set

Pin a static outbound address

`gregale app <slug> static-egress-ip set <ip>`

#### app static-egress-ip clear

Clear the pinned outbound address

`gregale app <slug> static-egress-ip clear`

### app routes

List admitted per-route labels for one app

`gregale app <slug> routes`

### app tcp

Manage raw TCP listeners for an app

`gregale app <slug> tcp <list|add|tls|tls-status|enable|disable|rm|delete>`

#### app tcp list

List TCP listeners

`gregale app <slug> tcp list`

#### app tcp add

Create a TCP listener

`gregale app <slug> tcp add --name <NAME> --guest-port <PORT> [--public-port <PORT>] [--tls-mode <MODE>] [--tls-hostname <HOST>]`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | listener name | required |
| `--guest-port <PORT>` | workload TCP port | required |
| `--public-port <PORT>` | stable public TCP port (40000..49999) |  |
| `--tls-mode <MODE>` | TLS mode (default passthrough) | one of `passthrough` · `terminate` |
| `--tls-hostname <HOST>` | verified app-owned hostname for TLS termination |  |

#### app tcp tls

Update one listener&#39;s TLS policy

`gregale app <slug> tcp tls <name> --tls-mode <MODE> [--tls-hostname <HOST>]`

| Flag | Meaning | |
|---|---|---|
| `--tls-mode <MODE>` | required TLS mode | required; one of `passthrough` · `terminate` |
| `--tls-hostname <HOST>` | verified app-owned termination hostname |  |

#### app tcp tls-status

Show certificate observations for a listener

`gregale app <slug> tcp tls-status <name>`

#### app tcp enable

Enable one listener

`gregale app <slug> tcp enable <name>`

#### app tcp disable

Disable one listener

`gregale app <slug> tcp disable <name>`

#### app tcp rm

Delete one listener

`gregale app <slug> tcp rm <name>`

#### app tcp delete

Alias for rm

`gregale app <slug> tcp delete <name>`

### app udp

Manage raw UDP listeners for an app

`gregale app <slug> udp <list|add|enable|disable|rm|delete>`

#### app udp list

List UDP listeners

`gregale app <slug> udp list`

#### app udp add

Create a UDP listener

`gregale app <slug> udp add --name <NAME> --guest-port <PORT> [--public-port <PORT>]`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | listener name | required |
| `--guest-port <PORT>` | workload UDP port | required |
| `--public-port <PORT>` | stable public UDP port |  |

#### app udp enable

Enable one listener

`gregale app <slug> udp enable <name>`

#### app udp disable

Disable one listener

`gregale app <slug> udp disable <name>`

#### app udp rm

Delete one listener

`gregale app <slug> udp rm <name>`

#### app udp delete

Alias for rm

`gregale app <slug> udp delete <name>`


## billing

Manage billing (portal, invoices, subscription, card on file)

`gregale billing [<subcommand>]`

Examples:

```sh
gregale billing export --month 2026-09 --out invoices.zip
gregale billing export --month 2026-09 --format csv --out invoices.csv
```

### billing portal

Open the active billing provider&#39;s portal

`gregale billing portal [--print] [--no-open]`

| Flag | Meaning | |
|---|---|---|
| `--print` | print the portal URL without opening a browser |  |
| `--no-open` | alias of --print |  |

### billing retry

Retry failed payment when supported; Polar uses the portal

### billing cancel

Cancel the subscription at period end

### billing payment-method

Show the card on file

`gregale billing payment-method [--print] [--no-open]`

| Flag | Meaning | |
|---|---|---|
| `--print` | print the card summary and portal URL without opening a browser |  |
| `--no-open` | alias of --print |  |

### billing status

Show subscription status

### billing costs

Explain retained usage costs and source coverage

`gregale billing costs [--month <YYYY-MM>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--month <YYYY-MM>` | UTC usage month (defaults to current) |  |
| `--json` | Print the machine-readable cost report |  |

Examples:

```sh
gregale billing costs --month 2026-10 --json
```

### billing forecast

Show usage cost forecasts and their availability

`gregale billing forecast [--month <YYYY-MM>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--month <YYYY-MM>` | UTC usage month (defaults to current) |  |
| `--json` | Print the machine-readable forecast |  |

Examples:

```sh
gregale billing forecast --json
```

### billing budget-preview

Preview a budget&#39;s cost and workload consequences without writes

`gregale billing budget-preview [--file <PATH>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | Budget spec JSON file |  |
| `--json` | Print the machine-readable preview |  |

Examples:

```sh
gregale billing budget-preview --file budget.json --json
```

### billing budgets

Manage revisioned budget drafts; activation is gated

#### billing budgets list

List account budget drafts

`gregale billing budgets list [--json]`

| Flag | Meaning | |
|---|---|---|
| `--json` | Print JSON |  |

#### billing budgets get

Read a budget, including a deletion tombstone

`gregale billing budgets get [--json] ID`

| Flag | Meaning | |
|---|---|---|
| `--json` | Print JSON |  |

#### billing budgets create

Save a budget draft

`gregale billing budgets create [--file <PATH>] [--key <KEY>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | Budget spec JSON file (required) |  |
| `--key <KEY>` | Stable operation key for retries |  |
| `--json` | Print JSON |  |

Examples:

```sh
gregale billing budgets create --file budget.json --key previews-october --json
```

#### billing budgets update

Replace a draft at its expected revision

`gregale billing budgets update [--file <PATH>] [--expected-revision <N>] [--key <KEY>] [--json] ID`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | Budget spec JSON file (required) |  |
| `--expected-revision <N>` | Current revision (required) |  |
| `--key <KEY>` | Stable operation key for retries |  |
| `--json` | Print JSON |  |

#### billing budgets delete

Tombstone a policy and retain its audit

`gregale billing budgets delete [--expected-revision <N>] [--key <KEY>] [--json] ID`

| Flag | Meaning | |
|---|---|---|
| `--expected-revision <N>` | Current revision (required) |  |
| `--key <KEY>` | Stable operation key for retries |  |
| `--json` | Print JSON |  |

#### billing budgets history

Page through immutable policy revisions

`gregale billing budgets history [--after-revision <N>] [--limit <N>] [--json] ID`

| Flag | Meaning | |
|---|---|---|
| `--after-revision <N>` | Continue after this revision |  |
| `--limit <N>` | Page size (1..100) |  |
| `--json` | Print JSON |  |

### billing refresh-invoice

Refresh provider facts for an existing invoice

`gregale billing refresh-invoice ID`

Examples:

```sh
gregale billing refresh-invoice INVOICE_ID
```

### billing backfill-invoices

Import one page of missing provider invoices

Examples:

```sh
gregale billing backfill-invoices
gregale billing backfill-invoices --cursor TOKEN
```

### billing export

Export a partial FOCUS 1.4 invoice projection

`gregale billing export [--month <YYYY-MM>] [--format <FORMAT>] [--out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--month <YYYY-MM>` | invoice period-end month (required) |  |
| `--format <FORMAT>` | export encoding (default zip with CSV and metadata) | one of `zip` · `csv` · `metadata` |
| `--out <PATH>` | new output file (required for zip); - writes stdout |  |


## canary

Project a canary preset against recent app traffic (canary simulate &lt;slug&gt;)

`gregale canary [<subcommand>] <slug>`

### canary simulate

Estimate per-stage canary success from the last hour

`gregale canary simulate [--canary-preset <PRESET>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--canary-preset <PRESET>` | canary ladder preset | one of `slow` · `balanced` · `aggressive` · `1-10-50-100` |


## build

Inspect builds (build status|list|provenance|sbom)

`gregale build [<subcommand>]`

### build status

Show the current status of one build

`gregale build status <id>`

### build list

List builds and discover build IDs

`gregale build list [--app <SLUG>] [--status <STATUS>] [--limit <N>] [--before <CURSOR>] [--cursor <CURSOR>] [--all]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | filter to one app |  |
| `--status <STATUS>` | filter by lifecycle status | one of `queued` · `running` · `succeeded` · `failed` · `cancelled` |
| `--limit <N>` | page size (1..200) |  |
| `--before <CURSOR>` | alias for --cursor |  |
| `--cursor <CURSOR>` | opaque cursor from a prior page |  |
| `--all` | walk every page using --limit and --cursor |  |

### build provenance

Show the build provenance attestation

`gregale build provenance <id>`

### build sbom

Show the build SBOM

`gregale build sbom <id>`


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

`gregale github bind [--installation-id <ID>] --repo <OWNER/NAME> [--branch <BRANCH>] [--deploy-branches <MAPPINGS>] <slug>`

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

`gregale github setup [--repo <OWNER/NAME>] [--production-branch <BRANCH>] [--deploy-branches <MAPPINGS>] [--pinned-sha <SHA>] [--pin-action] [--enable-action-updates] [--workflow <PATH>] [--preview] [--no-preview] [--preview-ttl-hours <HOURS>] [--preview-service-policy <POLICY>] [--root-dir <DIR>] [--ignore <PATHS>] [--rollout <MODE>] [--dry-run] [--force] <slug>`

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

`gregale github disconnect [--yes] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | confirm removing the repository binding |  |


## cors

Configure CORS for an app (allow|ls|rm|show)

`gregale cors [<subcommand>]`

### cors allow

Attach a CORS rule to &lt;slug&gt;

`gregale cors allow [--method <VERB>] [--credentials] [--max-age <N>] [--host <HOST>] <slug> <origin> [<origin>...]`

| Flag | Meaning | |
|---|---|---|
| `--method <VERB>` | allowed method (repeat) |  |
| `--credentials` | enable Access-Control-Allow-Credentials |  |
| `--max-age <N>` | Access-Control-Max-Age in seconds (default 600) |  |
| `--host <HOST>` | match host (default: the app&#39;s first verified custom domain) |  |

Examples:

```sh
gregale cors allow my-api https://app.example.com --method GET --method POST
```

### cors ls

List CORS rules bound to &lt;slug&gt; (defaults to linked context)

`gregale cors ls [<slug>]`

### cors rm

Delete a CORS rule by id

`gregale cors rm [<slug>] <rule-id>`

### cors show

Show per-app default CORS + active rules (defaults to linked context)

`gregale cors show [<slug>]`


## crons

Manage scheduled HTTP requests and deployment commands

`gregale crons [<subcommand>]`

### crons list

List cron rules

`gregale crons list --app <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### crons add

Schedule an HTTP request or deployment command

`gregale crons add --app <slug> --schedule <EXPR> [--path <PATH>] [--command <EXEC>] [--arg <ARG>] [--shell] [--timeout-seconds <N>] [--max-output-bytes <N>] [--timezone <TZ>] [--skip-if-running] [--retry-max] [--retry-backoff-seconds] [--schedule-policy <JSON>] [--failure-rules <JSON>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--schedule <EXPR>` | five-field cron expression | required |
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
| `--failure-rules <JSON>` | versioned failure and outcome-code rules JSON |  |

### crons info

Show one cron rule

`gregale crons info <id>`

### crons update

Update one cron rule

`gregale crons update [--schedule <EXPR>] [--path <PATH>] [--timezone <TZ>] [--enable] [--disable] [--skip-if-running] [--allow-overlap] [--retry-max] [--retry-backoff-seconds <N>] [--schedule-policy <JSON>] [--failure-rules <JSON>] <id>`

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
| `--failure-rules <JSON>` | replace versioned failure and outcome-code rules JSON |  |

### crons rm

Delete one cron rule

`gregale crons rm <id>`

### crons run

Fire one cron immediately

`gregale crons run <cron-id>`

### crons fire-now

Show the status of a manual fire request

`gregale crons fire-now <request-id>`

### crons runs

Show execution history

`gregale crons runs [--before <CURSOR>] [--limit <N>] [--run <TASK-ID>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--before <CURSOR>` | pagination cursor for older runs |  |
| `--limit <N>` | max runs to show (1..100) |  |
| `--run <TASK-ID>` | show details and captured output for one command run |  |

### crons occurrences

Inspect scheduled occurrence decisions

`gregale crons occurrences [--before <ID>] [--cursor <ID>] [--all] [--limit <N>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--before <ID>` | alias for --cursor |  |
| `--cursor <ID>` | opaque continuation cursor |  |
| `--all` | walk every page using --limit and --cursor |  |
| `--limit <N>` | max occurrence decisions (1..200) |  |

### crons cancel

Request cancellation of one command-cron run

`gregale crons cancel <cron-id> <run-id>`


## triggers

Manage unified event triggers (broker mappings + cron-linked rows)

`gregale triggers [<subcommand>]`

### triggers list

List triggers

`gregale triggers list [--app <slug>] [--kind <value>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | filter to an app slug |  |
| `--kind <value>` | filter by trigger kind | one of `cron` · `kafka` · `nats` · `redis_streams` · `sqs_compat` · `queue` |

### triggers get

Show one trigger

`gregale triggers get <id>`

### triggers create

Create a broker trigger

`gregale triggers create --app <slug> --kind <kind> [--slug <slug>] [--config <JSON>] [--enabled] [--disabled] [--batch-size <N>] [--batch-window-ms <N>] [--max-attempts <N>] [--payload-max-bytes <N>] [--broker-poison-strategy <commit|seek-to-offset>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--kind <kind>` | trigger kind | required; one of `kafka` · `nats` · `redis_streams` · `sqs_compat` · `queue` |
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

`gregale triggers update [--enabled] [--disabled] [--config <JSON>] [--schedule <EXPR>] [--path <PATH>] [--batch-size <N>] [--batch-window-ms <N>] [--max-attempts <N>] [--payload-max-bytes <N>] [--broker-poison-strategy <commit|seek-to-offset>] <id>`

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

`gregale triggers delete [--quiet] [--yes] <id>`

| Flag | Meaning | |
|---|---|---|
| `--quiet` | skip the typed confirmation (for scripts) |  |
| `--yes` | confirm trigger deletion without prompting |  |

### triggers pause

Disable one trigger

`gregale triggers pause <id>`

### triggers resume

Enable one trigger

`gregale triggers resume <id>`

### triggers records

List recent trigger records

`gregale triggers records [--state <STATE>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--state <STATE>` | filter by record state | one of `pending` · `claimed` · `succeeded` · `retry` · `dead_letter` |

### triggers retry

Re-drive one trigger record

`gregale triggers retry <trigger-id> <record-id>`

### triggers drop

Drop one trigger record

`gregale triggers drop <trigger-id> <record-id>`

### triggers dlq

List dead-letter records

`gregale triggers dlq [--reason <REASON>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--reason <REASON>` | filter by dead-letter reason |  |

### triggers metrics

Show per-state trigger metrics

`gregale triggers metrics <id>`


## workers

Inspect and manage background worker pools

`gregale workers [<subcommand>] [<slug>]`

### workers list

List background worker pools

### workers status

Show real-time status and autoscaling for a worker pool

`gregale workers status [--app <slug>] [<app>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (optional; defaults to linked context) |  |

### workers logs

Tail logs for a background worker pool

`gregale workers logs [--follow] [--grep <SUBSTR>] [--since <RFC3339>] [--level <LEVEL>] [<app>]`

| Flag | Meaning | |
|---|---|---|
| `--follow` | follow new log lines |  |
| `--grep <SUBSTR>` | filter log lines by substring |  |
| `--since <RFC3339>` | filter log lines after timestamp |  |
| `--level <LEVEL>` | filter log lines by level (info\|warn\|error) |  |

### workers scale

Adjust scaling bounds and graceful drain for a worker pool

`gregale workers scale [--min <N>] [--max <N>] [--target <N>] [--metric <METRIC>] [--name <CUSTOM_METRIC>] [--drain-timeout <DURATION>] [--stop-signal <SIG>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--min <N>` | min worker replicas (0 = scale-to-zero) |  |
| `--max <N>` | max worker replicas |  |
| `--target <N>` | target backlog per worker |  |
| `--metric <METRIC>` | autoscaling metric (queue_lag \| queue_depth \| custom) |  |
| `--name <CUSTOM_METRIC>` | custom metric name (required with --metric custom) |  |
| `--drain-timeout <DURATION>` | shutdown grace duration (e.g. 90s, 2m) |  |
| `--stop-signal <SIG>` | stop signal (e.g. SIGTERM, SIGINT, SIGQUIT) |  |


## jobs

Manage jobs (run-to-completion workloads)

`gregale jobs [<subcommand>]`

### jobs list

List jobs in this account

`gregale jobs list [--limit <N>] [--offset <N>] [--all]`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1..200, default 50) |  |
| `--offset <N>` | starting offset (&gt;= 0) |  |
| `--all` | walk every page using --limit and --offset |  |

### jobs add

Create a new job

`gregale jobs add --image <REF> [--command <ARGV>] [--ram <MB>] [--timeout <SECONDS>] [--parallelism <N>] [--retries <N>] [--schedule <EXPR>] [--timezone <TZ>] [--schedule-policy <JSON>] [--failure-rules <JSON>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--image <REF>` | OCI image | required |
| `--command <ARGV>` | comma-separated entrypoint (e.g. /bin/sh,-c,echo hi) |  |
| `--ram <MB>` | billable memory in MB (0 = plan default) |  |
| `--timeout <SECONDS>` | per-task wall-clock deadline (0 = plan default) |  |
| `--parallelism <N>` | max concurrent tasks across a run (0 = plan default) |  |
| `--retries <N>` | per-task max retries (0 = plan default) |  |
| `--schedule <EXPR>` | recurring five-field cron schedule |  |
| `--timezone <TZ>` | IANA timezone for the recurring schedule |  |
| `--schedule-policy <JSON>` | versioned recurring schedule policy JSON |  |
| `--failure-rules <JSON>` | versioned exit-code and outcome retry rules JSON |  |

### jobs info

Show one job

`gregale jobs info <name>`

### jobs update

Update one job

`gregale jobs update [--image <REF>] [--command <ARGV>] [--ram <MB>] [--timeout <SECONDS>] [--parallelism <N>] [--retries <N>] [--pause] [--resume] [--schedule <EXPR>] [--timezone <TZ>] [--unschedule] [--schedule-policy <JSON>] [--failure-rules <JSON>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--image <REF>` | new OCI image |  |
| `--command <ARGV>` | new comma-separated entrypoint |  |
| `--ram <MB>` | new RAM (MB) |  |
| `--timeout <SECONDS>` | new per-task timeout |  |
| `--parallelism <N>` | new max parallel tasks |  |
| `--retries <N>` | new per-task max retries |  |
| `--pause` | halt future dispatches (status=paused) |  |
| `--resume` | resume dispatches (status=active) |  |
| `--schedule <EXPR>` | replace recurring cron schedule |  |
| `--timezone <TZ>` | replace schedule IANA timezone |  |
| `--unschedule` | remove recurring schedule |  |
| `--schedule-policy <JSON>` | replace versioned recurring schedule policy JSON |  |
| `--failure-rules <JSON>` | replace versioned exit-code and outcome retry rules JSON |  |

### jobs rm

Soft-delete one job

`gregale jobs rm <name>`

### jobs run

Dispatch a new run (fan-out N tasks)

`gregale jobs run [--tasks <N>] [--retries <N>] [--timeout <SECONDS>] [--input <ID=REF>] [--input-manifest-uri <URI>] [--input-manifest-sha256 <DIGEST>] [--parallelism <N>] [--flexible] [--eligible-at <RFC3339>] [--latest-start-at <RFC3339>] [--fail-fast] [--failure-rules <JSON>] <job-name>`

| Flag | Meaning | |
|---|---|---|
| `--tasks <N>` | number of tasks to fan out (or use --input) |  |
| `--retries <N>` | override retry max for this run |  |
| `--timeout <SECONDS>` | override task timeout for this run |  |
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

`gregale jobs runs [--limit <N>] [--offset <N>] [--all] <name>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1..200, default 50) |  |
| `--offset <N>` | starting offset (&gt;= 0) |  |
| `--all` | walk every page using --limit and --offset |  |

### jobs occurrences

Inspect recurring schedule decisions

`gregale jobs occurrences [--before <ID>] [--cursor <ID>] [--all] [--limit <N>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--before <ID>` | alias for --cursor |  |
| `--cursor <ID>` | opaque continuation cursor |  |
| `--all` | walk every page using --limit and --cursor |  |
| `--limit <N>` | max occurrence decisions (1..200) |  |

### jobs cancel

Cancel a run

`gregale jobs cancel <name> <run-id>`

### jobs tasks

List tasks for one run

`gregale jobs tasks <name> <run-id>`

### jobs attempts

List retained attempts for one task

`gregale jobs attempts <name> <run-id> <task-index>`

### jobs retry

Retry one failed task

`gregale jobs retry <name> <run-id> <task-index>`

### jobs replay-failed

Replay unsuccessful tasks in a linked run

`gregale jobs replay-failed <name> <run-id>`

### jobs artifact-url

Verify a managed result and get a signed URL

`gregale jobs artifact-url <name> <run-id> <task-index> <artifact-name>`

### jobs logs

Tail logs for one task

`gregale jobs logs [--max-bytes <N>] <name> <run-id> <task-index>`

| Flag | Meaning | |
|---|---|---|
| `--max-bytes <N>` | maximum log payload size (1..1048576) |  |

### jobs registry

Manage private registry credentials for one job

#### jobs registry list

List registry credentials for the job

`gregale jobs registry list <job>`

#### jobs registry set

Store a registry credential for the job

`gregale jobs registry set --registry <HOST> --user <USER> [--password-stdin] [--password <PASSWORD>] <job>`

| Flag | Meaning | |
|---|---|---|
| `--registry <HOST>` | registry host | required |
| `--user <USER>` | registry user | required |
| `--password-stdin` | read the password from stdin |  |
| `--password <PASSWORD>` | registry password (prefer --password-stdin) |  |

#### jobs registry rm

Remove a registry credential from the job

`gregale jobs registry rm --registry <HOST> <job>`

| Flag | Meaning | |
|---|---|---|
| `--registry <HOST>` | registry host | required |


## automations

Build, monitor and control customer-built automations

`gregale automations [<subcommand>]`

Examples:

```sh
gregale automations list --app billing
gregale automations get --app billing --name paid-invoice
gregale automations health --app billing --name paid-invoice
gregale automations pause --app billing --name paid-invoice --expected-version 8
gregale automations resume --app billing --name paid-invoice --expected-version 9
gregale automations revisions list --app billing --name paid-invoice
gregale automations revisions show --app billing --name paid-invoice --revision 42
gregale automations validate --app billing --file automation.yaml
gregale automations simulate --app billing --file automation.yaml --input-file sample.json
gregale automations apply --app billing --file automation.yaml --expected-version 0
gregale automations publish --app billing --name paid-invoice --expected-version 1
gregale automations restore --app billing --name paid-invoice --revision 42 --expected-version 47
gregale automations delete --app billing --name paid-invoice --expected-version 48 --yes
```

### automations list

List automation versions and ownership for an app

`gregale automations list --app <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |

### automations get

Inspect automation state or export a definition

`gregale automations get --app <SLUG> --name <NAME> [--definition-out <PATH>] [--published]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--definition-out <PATH>` | export the selected definition as JSON to a new file |  |
| `--published` | export the published definition instead of the draft |  |

### automations health

Show bounded run reliability and failed-step metrics

`gregale automations health --app <SLUG> --name <NAME> [--created-after <RFC3339>] [--created-before <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--created-after <RFC3339>` | inclusive RFC3339 window start (max 30 days) |  |
| `--created-before <RFC3339>` | inclusive RFC3339 window end |  |

### automations pause

Stop future scheduled and event-triggered admissions

`gregale automations pause --app <SLUG> --name <NAME> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--expected-version <N>` | current automation version | required |

### automations resume

Resume automatic scheduled and event-triggered admissions

`gregale automations resume --app <SLUG> --name <NAME> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--expected-version <N>` | current automation version | required |

### automations revisions

Inspect immutable published snapshots

#### automations revisions list

List published revisions for an automation

`gregale automations revisions list --app <SLUG> --name <NAME> [--limit <N>] [--offset <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--limit <N>` | page size (1..100, default 50) |  |
| `--offset <N>` | number of revisions to skip |  |

#### automations revisions show

Inspect a revision or export its definition

`gregale automations revisions show --app <SLUG> --name <NAME> --revision <N> [--definition-out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--revision <N>` | published revision number | required |
| `--definition-out <PATH>` | export the definition as JSON to a new file |  |

### automations restore

Restore a published revision as a draft

`gregale automations restore --app <SLUG> --name <NAME> --revision <N> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--revision <N>` | published revision number to restore | required |
| `--expected-version <N>` | current version; use 0 if deleted | required |

### automations delete

Delete an automation using its current version

`gregale automations delete --app <SLUG> --name <NAME> --expected-version <N> --yes [--restore-manifest]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--expected-version <N>` | current automation version | required |
| `--yes` | required explicit confirmation of automation deletion | required |
| `--restore-manifest` | allow the current YAML definition to own this automation again |  |

### automations validate

Validate an automation definition without saving it

`gregale automations validate --app <SLUG> --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--file <PATH>` | YAML or JSON definition file | required |

### automations simulate

Trace an automation using sample input and mocked outputs, without running steps

`gregale automations simulate --app <SLUG> --file <PATH> [--input-file <PATH>] [--mock-outputs-file <PATH>] [--mock-item-outputs-file <PATH>] [--mock-attempts-file <PATH>] [--require-complete]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--file <PATH>` | YAML or JSON definition file | required |
| `--input-file <PATH>` | sample workflow input JSON file |  |
| `--mock-outputs-file <PATH>` | JSON object of action outputs keyed by step name |  |
| `--mock-item-outputs-file <PATH>` | JSON object of for_each output arrays keyed by step name |  |
| `--mock-attempts-file <PATH>` | JSON object of ordered attempt outcomes keyed by step name |  |
| `--require-complete` | fail if mocks leave steps unresolved |  |

### automations apply

Save an automation definition as a draft

`gregale automations apply --app <SLUG> --file <PATH> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--file <PATH>` | YAML or JSON definition file | required |
| `--expected-version <N>` | current version; use 0 for a new draft | required |

### automations publish

Publish the current automation draft

`gregale automations publish --app <SLUG> --name <NAME> --expected-version <N> [--take-over-manifest]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--expected-version <N>` | current version of the draft | required |
| `--take-over-manifest` | explicitly take over YAML ownership |  |


## workflows

Manage durable execution workflows

`gregale workflows [<subcommand>]`

### workflows list

List workflow runs for an app

`gregale workflows list --app <SLUG> [--limit <N>] [--offset <N>] [--status <STATUS>] [--workflow-name <NAME>] [--created-after <RFC3339>] [--created-before <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--limit <N>` | page size (1..100) |  |
| `--offset <N>` | page offset |  |
| `--status <STATUS>` | filter by workflow run status |  |
| `--workflow-name <NAME>` | filter by exact workflow name |  |
| `--created-after <RFC3339>` | inclusive RFC3339 creation-time start |  |
| `--created-before <RFC3339>` | inclusive RFC3339 creation-time end |  |

Examples:

```sh
gregale workflows list --app billing --workflow-name paid-invoice --status failed
gregale workflows list --app billing --created-after 2026-10-01T00:00:00Z --created-before 2026-10-05T23:59:59Z
```

### workflows schedules

Inspect recurring workflow schedules and their latest admission

`gregale workflows schedules --app <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |

### workflows run

Trigger a new workflow run

`gregale workflows run --app <slug> [--input <JSON>] [--idempotency-key <KEY>] <workflow-name>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--input <JSON>` | JSON input payload (default {}) |  |
| `--idempotency-key <KEY>` | stable key for retrying an uncertain run start |  |

### workflows status

Show details of a workflow run

`gregale workflows status <run_id>`

### workflows steps

List steps for a workflow run

`gregale workflows steps <run_id>`

### workflows attempts

List retry attempts and managed effect delivery status for a workflow step

`gregale workflows attempts <run_id> <step_name>`

### workflows retry

Retry one safely resumable failed HTTP step

`gregale workflows retry <run_id> <step_name>`

### workflows resume

Resume eligible failed actions in a workflow run

`gregale workflows resume --expected-resume-count <N> [--idempotency-key <KEY>] <run_id>`

| Flag | Meaning | |
|---|---|---|
| `--expected-resume-count <N>` | current resume_count shown by workflows status | required |
| `--idempotency-key <KEY>` | stable key for retrying the same resume request |  |

### workflows resumes

List continuation history for a workflow run

`gregale workflows resumes <run_id>`

### workflows cancel

Cancel an active workflow run

`gregale workflows cancel <run_id>`

### workflows events

Send external event to a workflow run

`gregale workflows events [--payload <JSON>] <run_id> <event_name>`

| Flag | Meaning | |
|---|---|---|
| `--payload <JSON>` | JSON event payload (default {}) |  |


## dashboard

Open the account dashboard in your browser

`gregale dashboard [--stateless]`

| Flag | Meaning | |
|---|---|---|
| `--stateless` | open the stateless-advisory landing page instead of the account page |  |


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

`gregale delayed-task add --app <SLUG> [--scheduled-at <RFC3339>] [--delay <DURATION>] [--payload <JSON|@FILE|->] [--method <METHOD>] [--path <PATH>] [--work-policy <NAME>] [--work-key <JSON>] [--work-fairness-key <JSON>] [--header <NAME:VALUE>] [--max-attempts <N>] [--retry-base-seconds <N>] [--retry-max-seconds <N>] [--retry-jitter-seconds <N>] [--retention <DURATION>] [--on-success-webhook <ID>] [--on-failure-webhook <ID>] [--idempotency-key <KEY>] <duration>`

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

`gregale delayed-task list --app <SLUG> [--limit <N>] [--before <ID>] [--cursor <CURSOR>] [--all]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--limit <N>` | page size (1-200) |  |
| `--before <ID>` | alias for --cursor |  |
| `--cursor <CURSOR>` | opaque continuation cursor |  |
| `--all` | walk every page |  |

### delayed-task get

Show one delayed task

`gregale delayed-task get <id>`

### delayed-task info

Alias for get

`gregale delayed-task info <id>`

### delayed-task cancel

Cancel a delayed task

`gregale delayed-task cancel <id>`


## deployments

List deployments or manage stable named URLs for immutable revisions

`gregale deployments [<subcommand>] [--app <slug>] [--limit <N>] [--before <CURSOR>] [--cursor <CURSOR>] [--all] [--wide]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (app-scoped deployment history) |  |
| `--limit <N>` | page size (1-200) |  |
| `--before <CURSOR>` | alias for --cursor |  |
| `--cursor <CURSOR>` | opaque cursor from a prior page |  |
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

Inspect a deployment, wait for its rollout, advance a canary, or set its minimum instances

`gregale deployment [<subcommand>] <id|vN> [--app <SLUG>] [--show-scan] [--show-secret-scan] [--min <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--show-scan` | include the per-deploy grype scan payload |  |
| `--show-secret-scan` | include the per-deploy image-layer secret-scan payload |  |
| `--min <N>` | min_instances floor (&gt;= 0) |  |

Examples:

```sh
gregale deployment summary v42 --app my-api
gregale deployment wait v42 --app my-api
```

### deployment runtime

Inspect runtime identity or preview a published runtime change

`gregale deployment runtime [--app <SLUG>] [--target <RELEASE_ID>] <ID|vN>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug for a vN revision |  |
| `--target <RELEASE_ID>` | published runtime release to preview without applying |  |

Examples:

```sh
gregale deployment runtime v42 --app my-function
gregale deployment runtime v42 --app my-function --target RELEASE_ID --json
```

### deployment advance

Advance a canary by one stage with route enforcement

`gregale deployment advance --expected-step <N> [--app <SLUG>] <ID|vN>`

| Flag | Meaning | |
|---|---|---|
| `--expected-step <N>` | observed current canary step (see deployment summary) | required |
| `--app <SLUG>` | app slug, to resolve a vN revision |  |

Examples:

```sh
gregale deployment advance DEPLOYMENT_UUID --expected-step 1
gregale deployment advance v42 --app my-api --expected-step 1
```

### deployment summary

Show the release diff and rollback target

`gregale deployment summary --app <SLUG> <id|vN>`

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

`gregale deployment wait [--app <SLUG>] [--rollout] [--progress] [--timeout <SECONDS|DURATION>] <id|vN>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug for a vN revision outside a linked project |  |
| `--rollout` | wait for safe rollout to reach 100% traffic |  |
| `--progress` | print rollout transitions while waiting (human output only) |  |
| `--timeout <SECONDS|DURATION>` | maximum wait (seconds, or a duration such as 10m) |  |

Examples:

```sh
gregale deployment wait 00000000000000000000000000000001
gregale deployment wait 00000000000000000000000000000001 --rollout --progress
```

### deployment set-min-instances

Set the per-deployment cold-wake floor

`gregale deployment set-min-instances --min <N> <id>`

| Flag | Meaning | |
|---|---|---|
| `--min <N>` | minimum warm instances for this deployment | required |


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

`gregale deploys show [--app <SLUG>] [--status] [--url] <id|vN>`

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

`gregale deploys status [--app <SLUG>] <id|vN>`

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

`gregale deploys cancel <id>`

### deploys reorder

Change one pending deployment&#39;s queue priority

`gregale deploys reorder <id>`

### deploys clear

Hide one deployment from the list

`gregale deploys clear [--app <SLUG>] [--dry-run] [--force] <id|vN>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug for revision resolution |  |
| `--dry-run` | preview cleanup without changing resources |  |
| `--force` | confirm cleanup without prompting |  |

### deploys clear-obsolete

Hide obsolete deployments older than a cutoff

`gregale deploys clear-obsolete --app <slug> [--older-than <D>] [--dry-run] [--force]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--older-than <D>` | cutoff age (default 168h) |  |
| `--dry-run` | preview age/status candidates without changing resources |  |
| `--force` | skip the confirmation |  |

### deploys retry

Retry a failed deployment from a specific stage (--from=&lt;stage&gt;)

`gregale deploys retry [--from <STAGE>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--from <STAGE>` | retry from this stage (defaults to the failing stage) | one of `source_download` · `dependency_restore` · `image_build` · `security_scan` · `snapshot_prepare` · `readiness` |


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
| `--template <NAME>` | scaffold from a built-in template | one of `hello-node` · `hello-python` · `hello-go` · `cron-example` · `function-node` · `function-python` · `function-go` · `function-node24` · `function-python313` · `event-worker` · `queue-worker` · `s3-uploader` · `slack-bot` · `rest-api-postgres` · `cron-worker` · `webhook-receiver` · `ai-chat` · `secret-reload-node` · `customer-platform` · `mcp-node` · `data-api` · `data-api-starter` |
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
| `--canary-stages <STAGES>` | custom percent@duration canary stages (requires --canary-preset custom) |  |
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

`gregale domains add [--domain <DOMAIN>] --app <SLUG> [--environment <SLUG>] [<domain>]`

| Flag | Meaning | |
|---|---|---|
| `--domain <DOMAIN>` | domain to attach (or the first argument) |  |
| `--app <SLUG>` | app slug to attach to | required |
| `--environment <SLUG>` | project environment to route this domain to |  |

### domains rm

Remove a custom domain binding

`gregale domains rm <domain>`

### domains set-default

Set a verified domain as the app default

`gregale domains set-default <domain>`

### domains verify

Check DNS and certificate verification status; exits nonzero while pending

`gregale domains verify <domain>`

### domains show

Show a domain&#39;s cert details

`gregale domains show <domain>`

### domains status

Show durable TLS status for all domains

### domains doctor

5-check readiness report; exits nonzero when unhealthy, including in JSON mode

`gregale domains doctor <domain>`


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

`gregale dev bridge <list|status|revoke|doctor> [--environment <ENV>] [--local-port <PORT>] [--dependencies <APPS>] [--entrypoint <APP>] [--inspect] [--replay-webhook <INVOCATION>] [--ready-path <PATH>] [--bind-env <BINDING>]`

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

`gregale dev bridge doctor [--environment <ENV>] [--local-port <PORT>] [--entrypoint <APP>] APP`

| Flag | Meaning | |
|---|---|---|
| `--environment <ENV>` | named development environment |  |
| `--local-port <PORT>` | local HTTP service port |  |
| `--entrypoint <APP>` | remote frontend |  |

### dev history

show edit-to-live timings and SLO guidance

`gregale dev history [--path <DIR>] [--name <PROJECT>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | source directory |  |
| `--name <PROJECT>` | developer-session project name |  |
| `--limit <N>` | number of recent syncs to show |  |

### dev setup

preflight a project and prepare the first developer environment

`gregale dev setup [--path <DIR>] [--name <PROJECT>] [--env-file <PATH>] [--service-override-file <PATH>] [--start] [--once] [--no-logs] [--open] [--postgres] [--postgres-region <REGION>]`

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

Run scenario suites, lifecycle profiles, and bounded local HTTP load tests

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

`gregale test init --from <PATH> --project <SLUG> [--source <DIR>] [--scenario <NAME>] [--output <PATH>]`

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

`gregale test compare [--budget <PATH>] [--html <PATH>] [--markdown <PATH>] [--github-summary] <before.json> <after.json>`

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

`gregale test import --from <PATH> --project <SLUG> [--source <DIR>] [--scenario <NAME>] [--output <PATH>] [--requests-only] [--status <CODE>]`

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


## chaos

Inject bounded faults into isolated real-VM scenario tests

`gregale chaos [<subcommand>]`

### chaos inject

Run one scenario profile with a scoped service fault

`gregale chaos inject --scenario <NAME> [--manifest <PATH>] --target <SERVICE> [--from <SERVICE>] [--latency <DURATION>] [--error <CODE>] [--percent <N>] [--duration <DURATION>] [--profile <PROFILE>] [--seed <N>]`

| Flag | Meaning | |
|---|---|---|
| `--scenario <NAME>` | scenario declared in gregale-test.yaml | required |
| `--manifest <PATH>` | scenario manifest path |  |
| `--target <SERVICE>` | scenario service workload to affect | required |
| `--from <SERVICE>` | only affect calls from this workload |  |
| `--latency <DURATION>` | add this delay to selected requests, such as 1500ms |  |
| `--error <CODE>` | return this synthetic HTTP 5xx status |  |
| `--percent <N>` | fraction of matching requests affected (1..100) |  |
| `--duration <DURATION>` | maximum fault lease duration (1s..5m) |  |
| `--profile <PROFILE>` | real-VM lifecycle profile | one of `warm` · `cold` · `restored` |
| `--seed <N>` | deterministic fault-selection seed |  |

Examples:

```sh
gregale chaos inject --scenario customer-export --target inventory --error 503 --percent 10 --duration 5m
gregale chaos inject --scenario customer-export --target payment --latency 1500ms --percent 20 --from worker --profile restored
```


## preview

Manage preview environments for pull requests

`gregale preview [<subcommand>]`

### preview create

Create and deploy a pull-request preview from a GitHub ref

`gregale preview create [--app <slug>] --repo <OWNER/NAME> --ref <REF> --pr-number <N> [--ttl-hours <HOURS>] [--wait] [--no-wait] [--timeout <SECONDS>] [--idempotency-key <KEY>] [--open]`

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

`gregale preview list [--app <slug>]`

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

### preview report

Review deployment route changes, gateway rule drift, and available test/traffic evidence

`gregale preview report [--format <FORMAT>] [--since <DURATION>] [--customer-details] [--baseline-deployment <ID>] [--test-report <PATH>] [--source-impact <PATH>] [--requirements <PATH>] [--fail-on-breaking] [--fail-on-request-breaking] [--fail-on-security-regression] [--fail-on-policy-drift] [--fail-on-incomplete] [--fail-on-requirements] <preview-slug>`

| Flag | Meaning | |
|---|---|---|
| `--format <FORMAT>` | report format: text or markdown (or use --json) |  |
| `--since <DURATION>` | traffic lookback duration (default 24h) |  |
| `--customer-details` | include observed consumer and tenant IDs in the report |  |
| `--baseline-deployment <ID>` | explicit parent deployment ID |  |
| `--test-report <PATH>` | JSON receipts from gregale test |  |
| `--source-impact <PATH>` | route impact report from gregale routes impact |  |
| `--requirements <PATH>` | versioned route requirements YAML or JSON file |  |
| `--fail-on-breaking` | exit 1 for known response-contract breaks |  |
| `--fail-on-request-breaking` | exit 1 for known request-contract restrictions |  |
| `--fail-on-security-regression` | exit 1 for known reductions in declared authentication requirements |  |
| `--fail-on-policy-drift` | exit 1 for changed or incomplete route rule policy comparison |  |
| `--fail-on-incomplete` | exit 1 when evidence is missing or needs review |  |
| `--fail-on-requirements` | exit 1 for violated or unknown route requirements |  |

Examples:

```sh
gregale preview report pr-42-my-api
gregale preview report pr-42-my-api --format markdown --fail-on-breaking
gregale preview report pr-42-my-api --test-report results.json --json
gregale preview report pr-42-my-api --source-impact impact.json --test-report results.json --format markdown
gregale preview report pr-42-my-api --fail-on-request-breaking --format markdown
gregale preview report pr-42-my-api --fail-on-policy-drift --format markdown
```

### preview review

Review route risk across multiple app previews in one release

`gregale preview review [--format <FORMAT>] [--since <DURATION>] [--customer-details] [--baseline-deployment <PREVIEW=ID>] [--test-report <PREVIEW=PATH>] [--source-impact <PREVIEW=PATH>] [--requirements <PREVIEW=PATH>] [--fail-on-breaking] [--fail-on-request-breaking] [--fail-on-security-regression] [--fail-on-policy-drift] [--fail-on-incomplete] [--fail-on-requirements] <preview-slug>...`

| Flag | Meaning | |
|---|---|---|
| `--format <FORMAT>` | report format: text or markdown (or use --json) |  |
| `--since <DURATION>` | traffic lookback duration (default 24h) |  |
| `--customer-details` | include observed consumer and tenant IDs in each app report |  |
| `--baseline-deployment <PREVIEW=ID>` | explicit parent deployment as PREVIEW=ID; repeat per preview |  |
| `--test-report <PREVIEW=PATH>` | test receipts as PREVIEW=PATH; repeat per preview |  |
| `--source-impact <PREVIEW=PATH>` | route impact report as PREVIEW=PATH; repeat per preview |  |
| `--requirements <PREVIEW=PATH>` | route requirements as PREVIEW=PATH; repeat per preview |  |
| `--fail-on-breaking` | exit 1 if any app has known response-contract breaks |  |
| `--fail-on-request-breaking` | exit 1 if any app has known request restrictions |  |
| `--fail-on-security-regression` | exit 1 if any app reduces declared authentication requirements |  |
| `--fail-on-policy-drift` | exit 1 if any app has changed or incomplete route policy comparison |  |
| `--fail-on-incomplete` | exit 1 if any app report is unavailable or needs review |  |
| `--fail-on-requirements` | exit 1 unless every app has satisfied route requirements |  |

Examples:

```sh
gregale preview review pr-42-api pr-42-worker --format markdown
gregale preview review pr-42-api pr-42-worker --source-impact pr-42-api=api-impact.json --source-impact pr-42-worker=worker-impact.json --json
gregale preview review pr-42-api pr-42-worker --test-report pr-42-api=api-tests.json --fail-on-breaking --fail-on-incomplete
```

### preview customers

Build customer impact rosters and track route migrations

`gregale preview customers <track|progress|migration> --report <PATH> [--by <consumer|tenant>] [--format <FORMAT>] [--out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--report <PATH>` | preview report or multi-app release review JSON with --customer-details | required |
| `--by <consumer|tenant>` | group by consumer (app scoped) or tenant (account scoped; default consumer) | one of `consumer` · `tenant` |
| `--format <FORMAT>` | text, Markdown, or CSV output (default text; --json emits machine-readable JSON) | one of `text` · `markdown` · `csv` |
| `--out <PATH>` | write a machine-readable roster to a new JSON file |  |

Examples:

```sh
gregale preview customers --report route-report.json --by consumer --format markdown
gregale preview customers --report release-review.json --by tenant --format csv
gregale preview customers track --roster customer-roster.json --mapping route-successors.json --deployment checkout=00000000-0000-4000-8000-000000000001
```

#### preview customers track

Compare a saved cohort with current route-customer telemetry

`gregale preview customers track --roster <PATH> --mapping <PATH> --deployment <APP=ID>... [--since <DURATION>] [--format <FORMAT>] [--out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--roster <PATH>` | version 1 customer roster JSON produced by preview customers | required |
| `--mapping <PATH>` | version 1 explicit old-to-successor route mapping JSON | required |
| `--deployment <APP=ID>` | immutable current deployment as APP=ID; repeat for each app | required |
| `--since <DURATION>` | post-release observation window (duration or RFC3339 timestamp; default 14d) |  |
| `--format <FORMAT>` | text, Markdown, or CSV output (default text; --json emits machine-readable JSON) | one of `text` · `markdown` · `csv` |
| `--out <PATH>` | write the full machine-readable tracker to a new JSON file |  |

Examples:

```sh
gregale preview customers track --roster customer-roster.json --mapping route-successors.json --deployment checkout=00000000-0000-4000-8000-000000000001 --since 14d --format markdown
```

#### preview customers progress

Measure sustained old-route traffic and customer migration progress across saved tracker windows

`gregale preview customers progress --snapshot <PATH>... [--grace-period <DURATION>] [--min-windows <COUNT>] [--max-staleness <DURATION>] [--format <FORMAT>] [--out <PATH>] [--fail-on-incomplete] [--fail-on-not-ready]`

| Flag | Meaning | |
|---|---|---|
| `--snapshot <PATH>` | saved route customer tracker JSON; repeat for each observation window | required |
| `--grace-period <DURATION>` | minimum continuous zero-traffic period before owner review (default 30d) |  |
| `--min-windows <COUNT>` | minimum distinct complete observation windows (default 2) |  |
| `--max-staleness <DURATION>` | maximum age of the latest telemetry watermark (default 72h) |  |
| `--format <FORMAT>` | text or Markdown output (default text; --json emits machine-readable JSON) | one of `text` · `markdown` |
| `--out <PATH>` | write the full machine-readable progress report to a new JSON file |  |
| `--fail-on-incomplete` | exit 1 when evidence is incomplete |  |
| `--fail-on-not-ready` | exit 1 unless every route is ready for owner review |  |

Examples:

```sh
gregale preview customers progress --snapshot migration-week-1.json --snapshot migration-week-2.json --grace-period 30d --format markdown
```

#### preview customers migration

Join contract compatibility with customer cutover evidence

`gregale preview customers migration <review|diff>`

##### preview customers migration review

Review contract compatibility and customer-by-customer route migration progress

`gregale preview customers migration review --contract-review <PATH> --snapshot <PATH>... [--grace-period <DURATION>] [--min-windows <COUNT>] [--max-staleness <DURATION>] [--format <FORMAT>] [--out <PATH>] [--fail-on-breaking] [--fail-on-incomplete] [--fail-on-not-ready]`

| Flag | Meaning | |
|---|---|---|
| `--contract-review <PATH>` | version 1 JSON from gregale routes migration review | required |
| `--snapshot <PATH>` | saved route customer tracker JSON; repeat for each observation window | required |
| `--grace-period <DURATION>` | minimum continuous zero-traffic period before owner review (default 30d) |  |
| `--min-windows <COUNT>` | minimum distinct complete observation windows (default 2) |  |
| `--max-staleness <DURATION>` | maximum age of the latest telemetry watermark (default 72h) |  |
| `--format <FORMAT>` | text, Markdown, or prioritized CSV action queue (default text; --json emits machine-readable JSON) | one of `text` · `markdown` · `csv` |
| `--out <PATH>` | write the full machine-readable cutover review to a new JSON file |  |
| `--fail-on-breaking` | exit 1 when any mapped successor has a declared breaking change |  |
| `--fail-on-incomplete` | exit 1 when contract or telemetry evidence is incomplete |  |
| `--fail-on-not-ready` | exit 1 unless every route is ready for owner review |  |

Examples:

```sh
gregale preview customers migration review --contract-review migration-review.json --snapshot migration-week-1.json --snapshot migration-week-2.json --grace-period 30d --format markdown
gregale preview customers migration review --contract-review migration-review.json --snapshot migration-week-1.json --snapshot migration-week-2.json --format csv
```

##### preview customers migration diff

Compare customer migration evidence between two cutover reviews

`gregale preview customers migration diff --before <PATH> --after <PATH> [--format <FORMAT>] [--out <PATH>] [--fail-on-regression]`

| Flag | Meaning | |
|---|---|---|
| `--before <PATH>` | previous version 1 customer migration cutover review JSON | required |
| `--after <PATH>` | current version 1 customer migration cutover review JSON | required |
| `--format <FORMAT>` | text, Markdown, or CSV output (default text; --json emits machine-readable JSON) | one of `text` · `markdown` · `csv` |
| `--out <PATH>` | write the machine-readable migration diff to a new JSON file |  |
| `--fail-on-regression` | exit 1 when confirmed customer migration regressions are found |  |

Examples:

```sh
gregale preview customers migration diff --before migration-last-week.json --after migration-today.json --fail-on-regression --format markdown
```

### preview wait

Wait for a preview deployment to become ready

`gregale preview wait [--progress] [--open] [--timeout <SECONDS|DURATION>] <preview-slug>`

| Flag | Meaning | |
|---|---|---|
| `--progress` | print deployment transitions while waiting |  |
| `--open` | open the preview URL after it becomes ready |  |
| `--timeout <SECONDS|DURATION>` | maximum wait (seconds, or a duration such as 10m) |  |

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

`gregale flags apply --file <path>`

| Flag | Meaning | |
|---|---|---|
| `--file <path>` | JSON update bundle | required |

### flags history

List immutable configuration versions

`gregale flags history [--before-version <number>]`

| Flag | Meaning | |
|---|---|---|
| `--before-version <number>` | page before this version |  |

### flags inspect

Explain a customer&#39;s decision

`gregale flags inspect --key <key> [--customer-id <UUID>] [--subject-id <ID>] [--fallback-variant <NAME>] [--version <number>]`

| Flag | Meaning | |
|---|---|---|
| `--key <key>` | flag key | required |
| `--customer-id <UUID>` | customer UUID |  |
| `--subject-id <ID>` | authenticated application subject (requires --customer-id) |  |
| `--fallback-variant <NAME>` | named-variant fallback |  |
| `--version <number>` | historical configuration version |  |

### flags rollback

Publish an earlier configuration

`gregale flags rollback --version <number> --expected-version <number>`

| Flag | Meaning | |
|---|---|---|
| `--version <number>` | version to restore | required |
| `--expected-version <number>` | current version | required |

### flags requests

Inspect request evidence by flag value

`gregale flags requests --key <key> [--customer-id <UUID>] [--value <bool>] [--variant <NAME>] [--used <bool>] [--since <duration>] [--cursor <cursor>]`

| Flag | Meaning | |
|---|---|---|
| `--key <key>` | flag key | required |
| `--customer-id <UUID>` | customer UUID |  |
| `--value <bool>` | true or false |  |
| `--variant <NAME>` | filter by named variant |  |
| `--used <bool>` | true or false exposure |  |
| `--since <duration>` | lookback (default 24h) |  |
| `--cursor <cursor>` | next-page cursor |  |

### flags outcomes

Inspect flag decision outcomes

`gregale flags outcomes --key <key> [--customer-id <UUID>] [--rule-id <ID>] [--config-version <number>] [--since <duration>]`

| Flag | Meaning | |
|---|---|---|
| `--key <key>` | flag key | required |
| `--customer-id <UUID>` | customer UUID |  |
| `--rule-id <ID>` | filter to a targeting rule |  |
| `--config-version <number>` | filter to one configuration version |  |
| `--since <duration>` | lookback (default 24h) |  |

### flags promote

Promote a targeting rule rollout

`gregale flags promote --key <key> --rule-id <ID> --expected-version <number>`

| Flag | Meaning | |
|---|---|---|
| `--key <key>` | flag key | required |
| `--rule-id <ID>` | targeting rule ID | required |
| `--expected-version <number>` | current configuration version | required |


## platform-tenants

Manage one customer across app consumers and tenant hostnames

`gregale platform-tenants [<subcommand>]`

### platform-tenants list

List platform customers

`gregale platform-tenants list [--limit <N>] [--offset <N>]`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1..100) |  |
| `--offset <N>` | page offset |  |

### platform-tenants add

Register a customer by external reference

`gregale platform-tenants add --external-ref <REF> --name <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--external-ref <REF>` | stable customer reference | required |
| `--name <TEXT>` | customer display name | required |

### platform-tenants apply

Preview or apply an onboarding bundle

`gregale platform-tenants apply [--file <path>] [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--file <path>` | JSON onboarding bundle |  |
| `--dry-run` | Preview without changes |  |

### platform-tenants credentials-list

List metadata for a customer&#39;s cross-app keys

`gregale platform-tenants credentials-list --id <UUID> [--limit <number>] [--offset <number>]`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | platform tenant UUID | required |
| `--limit <number>` | page size (1..100) |  |
| `--offset <number>` | page offset |  |

### platform-tenants credentials-apply

Preview or apply hash-only key issuance and rotation

`gregale platform-tenants credentials-apply --id <UUID> --file <path> [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | platform tenant UUID | required |
| `--file <path>` | hash-only credential bundle | required |
| `--dry-run` | preview without changes |  |

### platform-tenants info

Show linked consumers and surfaces

`gregale platform-tenants info --id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | platform tenant UUID | required |

### platform-tenants activation

Show or wait for customer hostname activation

`gregale platform-tenants activation --id <UUID> [--wait] [--timeout <duration>]`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | platform tenant UUID | required |
| `--wait` | poll until all surfaces are ready |  |
| `--timeout <duration>` | maximum wait (default 10m) |  |

### platform-tenants link-consumer

Attach an existing app consumer

`gregale platform-tenants link-consumer --id <UUID> --consumer-id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | platform tenant UUID | required |
| `--consumer-id <UUID>` | existing app consumer UUID | required |

### platform-tenants link-surface

Attach an existing tenant surface

`gregale platform-tenants link-surface --id <UUID> --surface-id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | platform tenant UUID | required |
| `--surface-id <UUID>` | existing tenant surface UUID | required |

### platform-tenants usage

Show cross-app raw usage

`gregale platform-tenants usage --id <UUID> [--since <RFC3339>] [--until <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | platform tenant UUID | required |
| `--since <RFC3339>` | usage window start (RFC3339) |  |
| `--until <RFC3339>` | usage window end (RFC3339) |  |

### platform-tenants activity

Show recent request activity across linked apps

`gregale platform-tenants activity --id <UUID> [--since <DURATION>] [--app-id <UUID>] [--status <N>] [--cursor <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | platform tenant UUID | required |
| `--since <DURATION>` | lookback (e.g. 24h) |  |
| `--app-id <UUID>` | filter to one linked app UUID |  |
| `--status <N>` | filter to one HTTP status (100..599) |  |
| `--cursor <CURSOR>` | opaque next-page cursor |  |
| `--limit <N>` | page size (1..200) |  |

### platform-tenants suspend

Stop linked credentials and hostnames

`gregale platform-tenants suspend --id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | platform tenant UUID | required |

### platform-tenants resume

Restore linked credentials and hostnames

`gregale platform-tenants resume --id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | platform tenant UUID | required |


## edge-rules

Per-app edge rules (edge-rules list|trace|create|get|update --app &lt;slug&gt;; edge-rules rm &lt;id&gt;)

`gregale edge-rules [<subcommand>] --app <slug> [--kind <value>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--kind <value>` | rule kind | one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` · `respond` · `retry` · `circuit_breaker` · `async` |

### edge-rules list

List edge rules

`gregale edge-rules list [--app <slug>] [--kind <value>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | filter to a single app slug |  |
| `--kind <value>` | filter to a single kind | one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` · `respond` · `retry` · `circuit_breaker` · `async` |

### edge-rules trace

Simulate composed edge-rule outcomes and budget, throttle, retry, circuit-breaker, and async-route policy; --config loads reusable JSON scenarios (see edge-rule-trace docs)

`gregale edge-rules trace [--config <file|->] [--app <slug>] [--url <URL>] [--method <method>] [--client-ip <IP>] [--country <CC>] [--header <Name:Value>] [--body-file <path|->]`

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

`gregale edge-rules create --app <slug> --kind <KIND> --match-host <HOST> [--match-path <PATH>] [--match-method <METHOD>] [--match-header <Name=Value>] [--priority <N>] [--enabled[=true|false]] [--throttle-requests-per-second <RPS>] [--throttle-burst <N>] [--throttle-key-by <KEY>] [--redirect-status <CODE>] [--redirect-to <URL>] [--rewrite-from <PATH>] [--rewrite-to <PATH>] [--route-target-slug <slug>] [--cache-max-age-seconds <N>] [--cache-stale-while-revalidate-seconds <N>] [--budget-ms <MS>] [--retry-max-attempts <N>] [--circuit-failure-threshold <RATIO>] [--circuit-open-seconds <N>] [--respond-status <CODE>] [--respond-body <JSON>] [--ip-allow <CIDR>] [--ip-deny <CIDR>] [--geo-allow <CC>] [--geo-deny <CC>] [--jwt-issuer <ISSUER>] [--jwt-jwks-url <URL>] [--on-success-webhook <ID>] [--on-failure-webhook <ID>] [--async-max-attempts <N>] [--async-retry-base-seconds <N>] [--async-retry-max-seconds <N>] [--async-retry-jitter-seconds <N>] [--async-max-age-seconds <N>] [--validate-schema <JSON|@FILE|->] [--validate-mode <MODE>] [--validate-content-type <TYPE>] [--validate-max-body-bytes <N>] [--validate-apply-while-streaming] [--validate-reject-unknown-fields] [--cors-allow-credentials] [--cors-max-age-seconds <SECONDS>] [--jwt-platform-tenant-external-ref-claim <CLAIM>] [--limit-max-body-bytes <BYTES>] [--limit-max-body-bytes-streaming <BYTES>] [--throttle-jwt-claim <CLAIM>] [--throttle-max-keys-per-rule <N>] [--throttle-missing-key-policy <POLICY>] [--cache-stale-if-error-seconds <SECONDS>] [--budget-allow-override-header <HEADER>] [--retry-allow-non-idempotent] [--retry-min-remaining-ms <MS>] [--retry-backoff-ms <MS>] [--retry-budget-percent <PERCENT>] [--retry-budget-min-retries <N>] [--circuit-min-requests <N>] [--circuit-window-seconds <SECONDS>] [--circuit-max-open-seconds <SECONDS>] [--maintenance-retry-after-seconds <SECONDS>] [--maintenance-message <TEXT>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--kind <KIND>` | rule kind | required; one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` · `respond` · `retry` · `circuit_breaker` · `async` |
| `--match-host <HOST>` | host to match | required |
| `--match-path <PATH>` | path to match (default /) |  |
| `--match-method <METHOD>` | HTTP method to match (repeat for multiple) |  |
| `--match-header <Name=Value>` | exact request header selector (repeat) |  |
| `--priority <N>` | match priority; lower wins (default 100) |  |
| `--enabled[=true|false]` | whether the rule is enabled (default true) | one of `true` · `false` |
| `--throttle-requests-per-second <RPS>` | kind=throttle: refill rate in requests per second |  |
| `--throttle-burst <N>` | kind=throttle: token-bucket burst |  |
| `--throttle-key-by <KEY>` | kind=throttle: bucket key (none\|api_key\|consumer_id\|jwt_subject\|jwt_claim\|country) |  |
| `--redirect-status <CODE>` | kind=redirect: 301\|302\|307\|308 |  |
| `--redirect-to <URL>` | kind=redirect: Location URL |  |
| `--rewrite-from <PATH>` | kind=rewrite: from path |  |
| `--rewrite-to <PATH>` | kind=rewrite: to path |  |
| `--route-target-slug <slug>` | kind=route: target app slug |  |
| `--cache-max-age-seconds <N>` | kind=cache: fresh window (default 60; max 3600) |  |
| `--cache-stale-while-revalidate-seconds <N>` | kind=cache: serve stale during a background refresh (max 300) |  |
| `--budget-ms <MS>` | kind=budget: per-request wall-clock budget in ms (max 30000) |  |
| `--retry-max-attempts <N>` | kind=retry: total attempts including the original (default 2; max 3) |  |
| `--circuit-failure-threshold <RATIO>` | kind=circuit_breaker: failure ratio that opens the app&#39;s instance-health breaker (default 0.5); the highest-priority rule tunes every instance, selectors do not partition it |  |
| `--circuit-open-seconds <N>` | kind=circuit_breaker: first open interval (default 5) |  |
| `--respond-status <CODE>` | kind=respond: response status (default 200) |  |
| `--respond-body <JSON>` | kind=respond: JSON response body (max 64 KiB) |  |
| `--ip-allow <CIDR>` | kind=ip: allow CIDR (repeat) |  |
| `--ip-deny <CIDR>` | kind=ip: deny CIDR (repeat) |  |
| `--geo-allow <CC>` | kind=geo: allow ISO country code (repeat) |  |
| `--geo-deny <CC>` | kind=geo: deny ISO country code (repeat) |  |
| `--jwt-issuer <ISSUER>` | kind=jwt: token issuer |  |
| `--jwt-jwks-url <URL>` | kind=jwt: JWKS URL (https) |  |
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
| `--cors-allow-credentials` | kind=cors: allow credentials |  |
| `--cors-max-age-seconds <SECONDS>` | kind=cors: preflight max age |  |
| `--jwt-platform-tenant-external-ref-claim <CLAIM>` | kind=jwt: claim containing the platform tenant external reference |  |
| `--limit-max-body-bytes <BYTES>` | kind=limit: buffered body cap (required; 1..25 MiB) |  |
| `--limit-max-body-bytes-streaming <BYTES>` | kind=limit: streaming body cap (0 inherits buffered cap) |  |
| `--throttle-jwt-claim <CLAIM>` | kind=throttle: JWT claim when key-by is jwt_claim |  |
| `--throttle-max-keys-per-rule <N>` | kind=throttle: maximum distinct consumer buckets |  |
| `--throttle-missing-key-policy <POLICY>` | kind=throttle: behavior when identity is missing | one of `shared` · `reject` |
| `--cache-stale-if-error-seconds <SECONDS>` | kind=cache: serve stale on origin failure (max 300) |  |
| `--budget-allow-override-header <HEADER>` | kind=budget: header allowed to override the budget |  |
| `--retry-allow-non-idempotent` | kind=retry: allow POST/PATCH replay when Idempotency-Key is honored |  |
| `--retry-min-remaining-ms <MS>` | kind=retry: remaining request budget required before replay |  |
| `--retry-backoff-ms <MS>` | kind=retry: delay before replay |  |
| `--retry-budget-percent <PERCENT>` | kind=retry: retry budget as percent of originals |  |
| `--retry-budget-min-retries <N>` | kind=retry: minimum retries allowed per window |  |
| `--circuit-min-requests <N>` | kind=circuit_breaker: observations before consulting the failure ratio |  |
| `--circuit-window-seconds <SECONDS>` | kind=circuit_breaker: rolling failure window |  |
| `--circuit-max-open-seconds <SECONDS>` | kind=circuit_breaker: maximum open interval |  |
| `--maintenance-retry-after-seconds <SECONDS>` | kind=maintenance: Retry-After hint |  |
| `--maintenance-message <TEXT>` | kind=maintenance: operator message |  |

Examples:

```sh
gregale edge-rules create --app my-api --kind throttle --match-host my-api.gregale.dev --match-path /search --throttle-requests-per-second 5 --throttle-burst 10
gregale edge-rules create --app my-api --kind redirect --match-host my-api.gregale.dev --match-path /old --redirect-status 308 --redirect-to https://my-api.gregale.dev/new
gregale edge-rules create --app my-api --kind cache --match-host my-api.gregale.dev --match-path /catalog --cache-max-age-seconds 60
gregale edge-rules create --app my-api --kind budget --match-host my-api.gregale.dev --match-path /reports --budget-ms 20000
gregale edge-rules create --app my-api --kind validate --match-host api.example.com --validate-schema @schema.json --validate-content-type application/json --validate-mode block
cat schema.json | gregale edge-rules create --app my-api --kind validate --match-host api.example.com --validate-schema -
```

### edge-rules get

Show one edge rule

`gregale edge-rules get <id>`

### edge-rules update

Update one edge rule

`gregale edge-rules update [--match-host <HOST>] [--match-path <PATH>] [--match-method <METHOD>]... [--match-header <NAME=VALUE>]... [--clear-match-headers] [--priority <N>] [--enable] [--disable] [--kind <KIND>] [--route-target-slug <SLUG>] [--rewrite-from <PATH>] [--rewrite-to <PATH>] [--redirect-status <CODE>] [--redirect-to <URL>] [--redirect-header <NAME:VALUE>]... [--headers-request-add <NAME:VALUE>]... [--headers-request-set <NAME:VALUE>]... [--headers-request-remove <NAME>]... [--headers-response-add <NAME:VALUE>]... [--headers-response-set <NAME:VALUE>]... [--headers-response-remove <NAME>]... [--cors-allow-origin <ORIGIN>]... [--cors-allow-method <METHOD>]... [--cors-allow-header <HEADER>]... [--cors-expose-header <HEADER>]... [--cors-allow-credentials] [--cors-max-age-seconds <SECONDS>] [--jwt-issuer <ISSUER>] [--jwt-jwks-url <URL>] [--jwt-audience <AUDIENCE>]... [--jwt-algorithm <ALG>]... [--jwt-required-claim <NAME=VALUE>]... [--jwt-platform-tenant-external-ref-claim <CLAIM>] [--ip-allow <CIDR>]... [--ip-deny <CIDR>]... [--geo-allow <CC>]... [--geo-deny <CC>]... [--limit-max-body-bytes <BYTES>] [--limit-max-body-bytes-streaming <BYTES>] [--throttle-requests-per-second <RPS>] [--throttle-burst <N>] [--throttle-key-by <KEY>] [--throttle-jwt-claim <CLAIM>] [--throttle-max-keys-per-rule <N>] [--throttle-missing-key-policy <POLICY>] [--cache-max-age-seconds <SECONDS>] [--cache-stale-while-revalidate-seconds <SECONDS>] [--cache-stale-if-error-seconds <SECONDS>] [--cache-vary-on <HEADER>]... [--cache-methods <METHOD>]... [--budget-ms <MS>] [--budget-allow-override-header <HEADER>] [--retry-max-attempts <N>] [--retry-allow-non-idempotent] [--retry-min-remaining-ms <MS>] [--retry-backoff-ms <MS>] [--retry-budget-percent <PERCENT>] [--retry-budget-min-retries <N>] [--circuit-failure-threshold <RATIO>] [--circuit-min-requests <N>] [--circuit-window-seconds <SECONDS>] [--circuit-open-seconds <SECONDS>] [--circuit-max-open-seconds <SECONDS>] [--maintenance-retry-after-seconds <SECONDS>] [--maintenance-message <TEXT>] [--respond-status <CODE>] [--respond-body <JSON>] [--on-success-webhook <ID>] [--on-failure-webhook <ID>] [--async-max-attempts <N>] [--async-retry-base-seconds <N>] [--async-retry-max-seconds <N>] [--async-retry-jitter-seconds <N>] [--async-max-age-seconds <N>] [--validate-schema <JSON|@FILE|->] [--validate-mode <MODE>] [--validate-content-type <TYPE>] [--validate-max-body-bytes <N>] [--validate-apply-while-streaming] [--validate-reject-unknown-fields] <id>`

| Flag | Meaning | |
|---|---|---|
| `--match-host <HOST>` | new host to match |  |
| `--match-path <PATH>` | new path to match |  |
| `--match-method <METHOD>` | replacement HTTP method (repeatable) |  |
| `--match-header <NAME=VALUE>` | replacement exact request header selector (repeatable) |  |
| `--clear-match-headers` | remove all request header selectors |  |
| `--priority <N>` | new match priority |  |
| `--enable` | enable the rule |  |
| `--disable` | disable the rule |  |
| `--kind <KIND>` | new action kind (required when changing action flags) | one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` · `respond` · `retry` · `circuit_breaker` · `async` |
| `--route-target-slug <SLUG>` | kind=route: target app slug |  |
| `--rewrite-from <PATH>` | kind=rewrite: source path |  |
| `--rewrite-to <PATH>` | kind=rewrite: destination path |  |
| `--redirect-status <CODE>` | kind=redirect: response status |  |
| `--redirect-to <URL>` | kind=redirect: Location URL |  |
| `--redirect-header <NAME:VALUE>` | kind=redirect: extra response header (repeatable) |  |
| `--headers-request-add <NAME:VALUE>` | kind=headers: request header to add (repeatable) |  |
| `--headers-request-set <NAME:VALUE>` | kind=headers: request header to set (repeatable) |  |
| `--headers-request-remove <NAME>` | kind=headers: request header to remove (repeatable) |  |
| `--headers-response-add <NAME:VALUE>` | kind=headers: response header to add (repeatable) |  |
| `--headers-response-set <NAME:VALUE>` | kind=headers: response header to set (repeatable) |  |
| `--headers-response-remove <NAME>` | kind=headers: response header to remove (repeatable) |  |
| `--cors-allow-origin <ORIGIN>` | kind=cors: allowed origin (repeatable) |  |
| `--cors-allow-method <METHOD>` | kind=cors: allowed method (repeatable) |  |
| `--cors-allow-header <HEADER>` | kind=cors: allowed header (repeatable) |  |
| `--cors-expose-header <HEADER>` | kind=cors: exposed header (repeatable) |  |
| `--cors-allow-credentials` | kind=cors: allow credentials |  |
| `--cors-max-age-seconds <SECONDS>` | kind=cors: preflight max age |  |
| `--jwt-issuer <ISSUER>` | kind=jwt: token issuer |  |
| `--jwt-jwks-url <URL>` | kind=jwt: JWKS URL |  |
| `--jwt-audience <AUDIENCE>` | kind=jwt: required audience (repeatable) |  |
| `--jwt-algorithm <ALG>` | kind=jwt: allowed signing algorithm (repeatable) |  |
| `--jwt-required-claim <NAME=VALUE>` | kind=jwt: required claim (repeatable) |  |
| `--jwt-platform-tenant-external-ref-claim <CLAIM>` | kind=jwt: claim containing the platform tenant external reference |  |
| `--ip-allow <CIDR>` | kind=ip: allowed CIDR (repeatable) |  |
| `--ip-deny <CIDR>` | kind=ip: denied CIDR (repeatable) |  |
| `--geo-allow <CC>` | kind=geo: allowed country code (repeatable) |  |
| `--geo-deny <CC>` | kind=geo: denied country code (repeatable) |  |
| `--limit-max-body-bytes <BYTES>` | kind=limit: buffered body cap |  |
| `--limit-max-body-bytes-streaming <BYTES>` | kind=limit: streaming body cap (0 inherits buffered cap) |  |
| `--throttle-requests-per-second <RPS>` | kind=throttle: refill rate |  |
| `--throttle-burst <N>` | kind=throttle: token-bucket burst |  |
| `--throttle-key-by <KEY>` | kind=throttle: bucket key | one of `none` · `api_key` · `consumer_id` · `jwt_subject` · `jwt_claim` · `country` |
| `--throttle-jwt-claim <CLAIM>` | kind=throttle: JWT claim when key-by is jwt_claim |  |
| `--throttle-max-keys-per-rule <N>` | kind=throttle: maximum distinct consumer buckets |  |
| `--throttle-missing-key-policy <POLICY>` | kind=throttle: behavior when identity is missing | one of `shared` · `reject` |
| `--cache-max-age-seconds <SECONDS>` | kind=cache: fresh window |  |
| `--cache-stale-while-revalidate-seconds <SECONDS>` | kind=cache: stale-while-revalidate window |  |
| `--cache-stale-if-error-seconds <SECONDS>` | kind=cache: serve stale on origin failure |  |
| `--cache-vary-on <HEADER>` | kind=cache: header to vary on (repeatable) |  |
| `--cache-methods <METHOD>` | kind=cache: cacheable method (repeatable) |  |
| `--budget-ms <MS>` | kind=budget: per-request wall-clock budget |  |
| `--budget-allow-override-header <HEADER>` | kind=budget: header allowed to override the budget |  |
| `--retry-max-attempts <N>` | kind=retry: total attempts including the original |  |
| `--retry-allow-non-idempotent` | kind=retry: allow POST/PATCH replay when Idempotency-Key is honored |  |
| `--retry-min-remaining-ms <MS>` | kind=retry: remaining budget required before replay |  |
| `--retry-backoff-ms <MS>` | kind=retry: delay before replay |  |
| `--retry-budget-percent <PERCENT>` | kind=retry: retry budget as percent of originals |  |
| `--retry-budget-min-retries <N>` | kind=retry: minimum retries allowed per window |  |
| `--circuit-failure-threshold <RATIO>` | kind=circuit_breaker: failure ratio that opens the breaker |  |
| `--circuit-min-requests <N>` | kind=circuit_breaker: observations before consulting the ratio |  |
| `--circuit-window-seconds <SECONDS>` | kind=circuit_breaker: rolling failure window |  |
| `--circuit-open-seconds <SECONDS>` | kind=circuit_breaker: first open interval |  |
| `--circuit-max-open-seconds <SECONDS>` | kind=circuit_breaker: maximum open interval |  |
| `--maintenance-retry-after-seconds <SECONDS>` | kind=maintenance: Retry-After hint |  |
| `--maintenance-message <TEXT>` | kind=maintenance: operator message |  |
| `--respond-status <CODE>` | kind=respond: response status |  |
| `--respond-body <JSON>` | kind=respond: JSON response body |  |
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

`gregale edge-rules rm <id>`


## openapi

Manage app OpenAPI docs + pre-publish schema-drift checks

`gregale openapi [<subcommand>]`

### openapi diff

Diff two openapi.yaml files; exit 2 on any BREAKING row

`gregale openapi diff <baseline.yaml> <proposed.yaml>`

### openapi get

Fetch an app OpenAPI document (manual_import|auto; slug defaults to linked context)

`gregale openapi get [--source <manual_import|auto>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--source <manual_import|auto>` | document source | one of `manual_import` · `auto` |

### openapi import

Import an app OpenAPI document from a JSON file or stdin

`gregale openapi import <slug> <file|->`

### openapi dry-run

Preview uncovered routes without importing the document (slug defaults to linked context)

`gregale openapi dry-run [<slug>] <file|->`

### openapi preview

Preview routes, edge policies, and the read-only OpenAPI contract diff (slug defaults to linked context)

`gregale openapi preview [--scope <scope>] [--fail-on-unavailable] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--scope <scope>` | deployment scope to compare (defaults to linked environment, otherwise prod) |  |
| `--fail-on-unavailable` | fail when the contract-diff backend is unavailable |  |

### openapi apply

Plan or apply generated validation edge rules

`gregale openapi apply [--confirm] [--preview-sha256 <SHA256>] [--match-host <HOST>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--confirm` | apply the reviewed plan |  |
| `--preview-sha256 <SHA256>` | approval hash from the plan |  |
| `--match-host <HOST>` | hostname for generated rules |  |

### openapi rm

Remove the imported app OpenAPI document

`gregale openapi rm <slug>`


## routes

Analyze route changes, migrations, lifecycle and production policies

`gregale routes [<subcommand>] [<slug>]`

### routes requirements

Save or read versioned route requirements for an app

#### routes requirements set

Save version 2 route intent after comparing the current revision

`gregale routes requirements set --requirements <PATH> --expected-revision <N> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--requirements <PATH>` | version 2 requirements YAML or JSON file | required |
| `--expected-revision <N>` | current saved revision; use 0 for the first save | required |

Examples:

```sh
gregale routes requirements set my-api --requirements gregale-routes.yaml --expected-revision 0
```

#### routes requirements get

Read current route intent and optionally export requirements for planning

`gregale routes requirements get [--out <PATH>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--out <PATH>` | export normalized requirements JSON to a new file |  |

### routes monitor

Monitor absolute route budgets after production promotion

#### routes monitor get

Read production route budgets and revision

`gregale routes monitor get <slug>`

#### routes monitor preview

Evaluate proposed budgets against recent production traffic without saving them

`gregale routes monitor preview --routes <PATH> [--customer-group-by <DIMENSION>] [--customer-details] [--fail-on-unhealthy] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--routes <PATH>` | JSON array of proposed exact route budgets | required |
| `--customer-group-by <DIMENSION>` | optionally evaluate budgets per tenant or consumer | one of `tenant` · `consumer` |
| `--customer-details` | include observed tenant or consumer IDs (when enabled) |  |
| `--fail-on-unhealthy` | exit nonzero unless all proposed budgets are healthy |  |

Examples:

```sh
gregale routes monitor preview my-api --routes production-routes.json --customer-group-by tenant
```

#### routes monitor set

Save advisory production route budgets

`gregale routes monitor set --mode <MODE> --routes <PATH> --expected-revision <N> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--mode <MODE>` | enabled or disabled | required; one of `enabled` · `disabled` |
| `--routes <PATH>` | JSON array of exact method/path labels with max_5xx_rate_bps and/or max_p95_ms | required |
| `--expected-revision <N>` | current monitor revision; 0 initially | required |

#### routes monitor report

Read observed health for the fully serving production deployment

`gregale routes monitor report [--fail-on-unhealthy] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--fail-on-unhealthy` | exit nonzero unless every selected budget is healthy |  |

#### routes monitor incidents

List retained production route incidents

`gregale routes monitor incidents [--limit <N>] [--before <ID>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (default 5; maximum 10) |  |
| `--before <ID>` | page before a retained incident UUID |  |

#### routes monitor explain

Inspect a saved incident and optionally correlate affected customers and changed route owners

`gregale routes monitor explain --incident <ID> [--out <PATH>] [--source-impact <PATH|auto>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--incident <ID>` | saved incident UUID | required |
| `--out <PATH>` | save incident evidence JSON to a new file |  |
| `--source-impact <PATH|auto>` | correlate source, aggregate customer impact, and candidate-commit CODEOWNERS from the matching local repository |  |

Examples:

```sh
gregale routes monitor explain api --incident INCIDENT_UUID --source-impact auto
```

### routes lifecycle

Review deployed routes for carefully evidenced retirement candidates

#### routes lifecycle review

Compare captured routes, observed usage, source and requirements

`gregale routes lifecycle review --deployment <ID> [--since <WINDOW>] [--source-impact <PATH>] [--out <PATH>] [--fail-on-incomplete] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | immutable deployed contract UUID | required |
| `--since <WINDOW>` | route-usage window (default 14d; plan retention may clamp it) |  |
| `--source-impact <PATH>` | complete source impact report whose candidate revision matches the deployment commit |  |
| `--out <PATH>` | save full JSON review to a new file |  |
| `--fail-on-incomplete` | exit nonzero when any route remains inconclusive |  |

Examples:

```sh
gregale routes lifecycle review api --deployment DEPLOYMENT_UUID --since 14d --source-impact impact.json --out lifecycle-review.json
```

### routes migration

Check mapped route successors against immutable deployment contracts

#### routes migration review

Compare method, path parameters, request, response and security contracts

`gregale routes migration review --mapping <PATH> --from-deployment <APP=ID>... --to-deployment <APP=ID>... [--format <FORMAT>] [--out <PATH>] [--fail-on-breaking] [--fail-on-incomplete]`

| Flag | Meaning | |
|---|---|---|
| `--mapping <PATH>` | version 1 explicit old-to-successor route mapping JSON | required |
| `--from-deployment <APP=ID>` | immutable baseline deployment as APP=ID; repeat for each app | required |
| `--to-deployment <APP=ID>` | immutable successor deployment as APP=ID; repeat for each app | required |
| `--format <FORMAT>` | text or Markdown output (default text; --json emits machine-readable JSON) | one of `text` · `markdown` |
| `--out <PATH>` | save the full JSON review to a new file |  |
| `--fail-on-breaking` | exit nonzero when any successor has a declared breaking change |  |
| `--fail-on-incomplete` | exit nonzero when any route lacks complete contract evidence |  |

Examples:

```sh
gregale routes migration review --mapping route-successors.json --from-deployment checkout=OLD_DEPLOYMENT --to-deployment checkout=NEW_DEPLOYMENT --format markdown --out migration-review.json
```

### routes health

Compare critical route errors and optional p95 latency to gate canary progression

#### routes health get

Read selected routes, mode and revision

`gregale routes health get <slug>`

#### routes health set

Save exact normalized telemetry route selectors

`gregale routes health set --routes <PATH> --mode <MODE> [--on-regression <ACTION>] --expected-revision <N> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--routes <PATH>` | JSON array of method/path selectors with optional latency checks and advisory watch_statuses | required |
| `--mode <MODE>` | report (enforcement unavailable in preview) | required; one of `report` · `enforce` |
| `--on-regression <ACTION>` | hold (default) or automatically abort on confirmed route 5xx regression | one of `hold` · `abort` |
| `--expected-revision <N>` | current revision; 0 initially | required |

#### routes health suggest

Rank observed routes by customer reach and traffic; emit reviewable canary selectors

`gregale routes health suggest --deployment <ID> [--since <WINDOW>] [--customer-group-by <DIMENSION>] [--limit <N>] [--preview-report <PATH>] [--out <PATH>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | immutable baseline deployment UUID; must match --preview-report when supplied | required |
| `--since <WINDOW>` | observed usage window (default 168h; accepts 7d or RFC3339) |  |
| `--customer-group-by <DIMENSION>` | rank by distinct tenant (default) or consumer count | one of `tenant` · `consumer` |
| `--limit <N>` | number of route selectors (default 10; maximum 20) |  |
| `--preview-report <PATH>` | restrict suggestions to source-affected routes in a bound preview report for this app and deployment |  |
| `--out <PATH>` | save a JSON selector array ready for routes health set |  |

#### routes health review

Join source-affected routes to configured canary coverage and candidate health evidence

`gregale routes health review --deployment <ID> --preview-report <PATH> [--fail-on-incomplete] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | candidate deployment UUID currently receiving canary traffic | required |
| `--preview-report <PATH>` | bound preview report containing source-affected routes | required |
| `--fail-on-incomplete` | exit nonzero unless source impact is complete and all affected route health is ready |  |

#### routes health review-release

Gate every app in an aggregate preview review on source-affected canary route health

`gregale routes health review-release --release-report <PATH> [--fail-on-incomplete]`

| Flag | Meaning | |
|---|---|---|
| `--release-report <PATH>` | aggregate JSON report from gregale preview review | required |
| `--fail-on-incomplete` | exit nonzero unless every app has complete, healthy affected-route coverage |  |

Examples:

```sh
gregale routes health review-release --release-report release-review.json --fail-on-incomplete --json
```

#### routes health report

Read candidate/stable counts, selected p95 checks and route verdicts

`gregale routes health report --deployment <ID> [--fail-on-unhealthy] [--customers] [--customer-group-by <DIMENSION>] [--customer-details] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | candidate deployment UUID | required |
| `--fail-on-unhealthy` | exit nonzero unless every selected route is healthy |  |
| `--customers` | include advisory customer health comparisons |  |
| `--customer-group-by <DIMENSION>` | tenant (default) or consumer; requires --customers | one of `tenant` · `consumer` |
| `--customer-details` | include customer IDs; requires --customers |  |

#### routes health investigate

Investigate route errors or latency with bounded retained evidence

`gregale routes health investigate --deployment <ID> --route <LABEL> [--source-impact <PATH>] [--signal <SIGNAL>] [--status <CODE>] [--customer-id <ID>] [--customer-group-by <DIMENSION>] [--out <PATH>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | candidate deployment UUID | required |
| `--route <LABEL>` | exact configured METHOD /path telemetry label | required |
| `--source-impact <PATH>` | correlate a local route impact report with both deployment revisions |  |
| `--signal <SIGNAL>` | errors (default) or latency; requires a configured latency check | one of `errors` · `latency` |
| `--status <CODE>` | watched 4xx code; 0 (default) selects all 5xx |  |
| `--customer-id <ID>` | recorded customer UUID; explicitly includes this ID |  |
| `--customer-group-by <DIMENSION>` | tenant (default) or consumer; requires --customer-id | one of `tenant` · `consumer` |
| `--out <PATH>` | save the investigation JSON to a new file |  |

#### routes health correlate

Find dependency slowdowns shared by multiple configured latency routes

`gregale routes health correlate --deployment <ID> [--limit <N>] [--out <PATH>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | candidate deployment UUID | required |
| `--limit <N>` | shared dependency groups to show (default 10; maximum 20) |  |
| `--out <PATH>` | save correlation JSON to a new file |  |

Examples:

```sh
gregale routes health correlate api --deployment CANDIDATE_UUID --json
```

#### routes health explain

Explain saved canary health decisions and their evidence timeline

`gregale routes health explain --deployment <ID> [--decision <ID>] [--limit <N>] [--before <ID>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | candidate deployment UUID | required |
| `--decision <ID>` | read one saved decision UUID |  |
| `--limit <N>` | timeline page size (default 5; maximum 10) |  |
| `--before <ID>` | page before a retained decision UUID |  |

### routes gate

Read or change the canary route enforcement mode

#### routes gate get

Read the current gate mode and revision

`gregale routes gate get <slug>`

Examples:

```sh
gregale routes gate get my-api --json
```

#### routes gate set

Change report or enforce mode using the current gate revision

`gregale routes gate set --mode <MODE> --expected-revision <N> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--mode <MODE>` | report (enforcement unavailable in preview) | required; one of `report` · `enforce` |
| `--expected-revision <N>` | current gate revision; 0 initially | required |

Examples:

```sh
gregale routes gate set my-api --mode enforce --expected-revision 0
```

### routes results

Read the latest automatic check with current freshness

`gregale routes results [--changes] --deployment <ID> [--expected-revision <N>] [--refresh] [--wait] [--timeout <DURATION>] [--out <PATH>] [--fail-on-requirements] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--changes` | show finding changes against prior known evidence |  |
| `--deployment <ID>` | app-owned deployment UUID | required |
| `--expected-revision <N>` | require this current saved intent revision |  |
| `--refresh` | queue a new check of current configuration |  |
| `--wait` | wait for pending work to complete |  |
| `--timeout <DURATION>` | maximum wait duration (default 2m; at most 10m) |  |
| `--out <PATH>` | export the result to a new JSON file |  |
| `--fail-on-requirements` | require completed, current and satisfied evidence |  |

Examples:

```sh
gregale routes results my-api --deployment DEPLOYMENT_ID --wait --fail-on-requirements --json
gregale routes results my-api --deployment DEPLOYMENT_ID --refresh --wait
```

### routes check

Check saved requirements against a captured deployment and current app policy

`gregale routes check --deployment <ID> [--expected-revision <N>] [--format <FORMAT>] [--out <PATH>] [--fail-on-requirements] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | app-owned captured deployment UUID | required |
| `--expected-revision <N>` | fail if the saved revision differs |  |
| `--format <FORMAT>` | human or markdown |  |
| `--out <PATH>` | save the JSON check to a new file |  |
| `--fail-on-requirements` | exit 1 for violations or incomplete inventory evidence |  |

Examples:

```sh
gregale routes check my-api --deployment DEPLOYMENT_ID --expected-revision 1 --fail-on-requirements --json
```

### routes plan

Plan throttle and budget patches with concrete or captured-family coverage

`gregale routes plan [--requirements <PATH>] [--saved] [--expected-revision <N>] [--out <PATH>] [--throttle-burst <N>] [--deployment <ID>] [--consolidate-budgets] [--fail-on-unresolved] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--requirements <PATH>` | versioned route requirements YAML or JSON; mutually exclusive with --saved |  |
| `--saved` | use the app&#39;s saved requirements and bind their revision |  |
| `--expected-revision <N>` | require this saved requirements revision; requires --saved |  |
| `--out <PATH>` | save JSON plan to a new owner-readable file |  |
| `--throttle-burst <N>` | burst for new throttles without an existing policy |  |
| `--deployment <ID>` | captured deployment UUID required for version 2 groups |  |
| `--consolidate-budgets` | combine compatible budgets within declared group prefixes, including uncaptured paths |  |
| `--fail-on-unresolved` | exit 1 when requirements remain unresolved after proposed changes |  |

Examples:

```sh
gregale routes plan my-api --requirements gregale-routes.yaml --out route-plan.json
gregale routes plan my-api --saved --deployment DEPLOYMENT_ID --expected-revision 1 --out repair.json
gregale routes plan pr-42-api --requirements gregale-routes.yaml --throttle-burst 20 --fail-on-unresolved --json
```

### routes apply

Atomically apply a reviewed server plan and recover its durable receipt

`gregale routes apply --plan <PATH> --confirm <value> [--idempotency-key <KEY>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | reviewed version 2 or 3 server plan JSON file | required |
| `--confirm <value>` | confirm application of every reviewed change | required |
| `--idempotency-key <KEY>` | stable retry key, defaults to plan SHA-256 |  |

Examples:

```sh
gregale routes apply my-api --plan route-plan.json --confirm
```

### routes impact

Explain FastAPI, Go HTTP, Express, or Hono route impact between Git revisions

`gregale routes impact --base <REF> [--head <REF>] [--path <DIR>] [--framework <FRAMEWORK>] [--entrypoint <MODULE:VARIABLE>] [--format <text|markdown>] [--out <PATH>] [--fail-on-impact] [--fail-on-incomplete] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--base <REF>` | baseline Git revision | required |
| `--head <REF>` | candidate Git revision (defaults to working tree) |  |
| `--path <DIR>` | application source directory inside the repository |  |
| `--framework <FRAMEWORK>` | source framework: auto, fastapi, go-nethttp, or node-http | one of `auto` · `fastapi` · `go-nethttp` · `node-http` |
| `--entrypoint <MODULE:VARIABLE>` | FastAPI module:variable (inferred when exactly one exists) |  |
| `--format <text|markdown>` | report format | one of `text` · `markdown` |
| `--out <PATH>` | save JSON report to a new owner-readable file |  |
| `--fail-on-impact` | exit 1 when routes were added, removed, or may be affected |  |
| `--fail-on-incomplete` | exit 1 when static analysis is incomplete |  |

Examples:

```sh
gregale routes impact my-api --base origin/main --path . --entrypoint main:app
gregale routes impact --base HEAD~1 --head HEAD --format markdown
gregale routes impact --base origin/main --framework go-nethttp --path services/api --json
gregale routes impact --base origin/main --framework node-http --path services/node-api --json
gregale routes impact --base origin/main --fail-on-impact --fail-on-incomplete --json
```

### routes contract

Check statically discovered source routes against a local OpenAPI contract

#### routes contract check

Find source-only and contract-only routes with conservative parameter matching

`gregale routes contract check --openapi <FILE> --base <REF> [--head <REF>] [--path <DIR>] [--framework <FRAMEWORK>] [--entrypoint <MODULE:VARIABLE>] [--format <text|markdown>] [--out <PATH>] [--fail-on-drift] [--fail-on-incomplete] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--openapi <FILE>` | local OpenAPI 3.0 or 3.1 document | required |
| `--base <REF>` | baseline Git revision | required |
| `--head <REF>` | candidate Git revision (defaults to working tree) |  |
| `--path <DIR>` | application source directory inside the repository |  |
| `--framework <FRAMEWORK>` | source framework: auto, fastapi, go-nethttp, or node-http | one of `auto` · `fastapi` · `go-nethttp` · `node-http` |
| `--entrypoint <MODULE:VARIABLE>` | FastAPI module:variable (inferred when exactly one exists) |  |
| `--format <text|markdown>` | report format | one of `text` · `markdown` |
| `--out <PATH>` | save JSON report to a new owner-readable file |  |
| `--fail-on-drift` | exit 1 for confirmed source-only or contract-only routes |  |
| `--fail-on-incomplete` | exit 1 when route or OpenAPI analysis is inconclusive |  |

Examples:

```sh
gregale routes contract check my-api --openapi openapi.yaml --base origin/main --fail-on-drift --json
gregale routes contract check --openapi openapi.yaml --base HEAD --path services/api --framework go-nethttp --format markdown
```


## env

Clone project environments or manage app runtime env/secrets

`gregale env [<subcommand>] [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked context) |  |

### env create

Clone a project environment with isolated managed data by default; full-copy admission currently returns environment_full_clone_unavailable with named blockers

`gregale env create --from <ENV> [--project <SLUG>] [--protected] [--share-resources] [--full] [--wait] [--timeout <SECONDS>] <stage>`

| Flag | Meaning | |
|---|---|---|
| `--from <ENV>` | source environment | required |
| `--project <SLUG>` | project slug (defaults to linked project) |  |
| `--protected` | protect the new environment |  |
| `--share-resources` | use source managed data with fresh target credentials instead of isolating it |  |
| `--full` | require complete configuration, workloads, policies and isolated data coverage; never fall back to a partial clone |  |
| `--wait` | wait for a full clone operation to finish (requires --full) |  |
| `--timeout <SECONDS>` | maximum local wait time; the durable server operation continues after timeout |  |

Examples:

```sh
gregale env create staging --from production --full --wait
```

### env clone-status

Read durable clone progress before or after the target exists; timeout exits 3 with a resume command, failed or compensated operations exit 1

`gregale env clone-status [--project <SLUG>] [--wait] [--timeout <SECONDS>] <operation-id>`

| Flag | Meaning | |
|---|---|---|
| `--project <SLUG>` | project slug (defaults to linked project) |  |
| `--wait` | wait for the same durable clone operation to finish |  |
| `--timeout <SECONDS>` | maximum local wait time |  |

Examples:

```sh
gregale env clone-status <operation-id> --project shop --wait
```

### env pull

Pull sealed-secret keys to a .env skeleton (values blank)

`gregale env pull [--app <slug>] [--scope <SCOPE>] [-o <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked context) |  |
| `--scope <SCOPE>` | env scope (defaults to linked project environment) |  |
| `-o <PATH>` | output file (default .env) |  |

Examples:

```sh
gregale env pull --app my-api
gregale env pull --app my-api --scope staging
```

### env push

Push KEY=VALUE pairs to sealed secrets (use --restart to apply now)

`gregale env push [--app <slug>] [--scope <SCOPE>] [-f <PATH>] [--from-stdin] [--restart] [--secret-scan <MODE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked context) |  |
| `--scope <SCOPE>` | env scope (defaults to linked project environment) |  |
| `-f <PATH>` | input file (default .env) |  |
| `--from-stdin` | read KEY=VALUE pairs from stdin |  |
| `--restart` | restart app after applying changes (otherwise changes apply on next cold wake) |  |
| `--secret-scan <MODE>` | scan pairs before pushing | one of `on` · `off` · `strict` · `source-tree` |

Examples:

```sh
printf 'LOG_LEVEL=info\n' | gregale env push --app my-api --from-stdin
gregale env push --app my-api --restart
```

### env diff

Render the env-diff matrix (presence / value-equality across scopes)

`gregale env diff [--app <slug>]`

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
| `--template <NAME>` | template name | required; one of `hello-node` · `hello-python` · `hello-go` · `cron-example` · `function-node` · `function-python` · `function-go` · `function-node24` · `function-python313` · `event-worker` · `queue-worker` · `s3-uploader` · `slack-bot` · `rest-api-postgres` · `cron-worker` · `webhook-receiver` · `ai-chat` · `secret-reload-node` · `customer-platform` · `mcp-node` · `data-api` · `data-api-starter` |
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

`gregale inspect [<slug>] [--upstreams] [--scope <scope>] [--errors] [--watch] [--interval <DURATION>] [--timeout <DURATION>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--upstreams` | List data upstreams captured for this app |  |
| `--scope <scope>` | filter by scope (defaults to linked project environment; used with --upstreams) |  |
| `--errors` | show the latest failed deployment&#39;s persisted error explanation |  |
| `--watch` | watch summary changes using read-only requests; incompatible with --upstreams and --errors |  |
| `--interval <DURATION>` | time between watch reads (default 5s; 1s..1h); requires --watch |  |
| `--timeout <DURATION>` | watch duration (default 0: until Ctrl-C); requires --watch |  |
| `--json` | print summary JSON, or JSON Lines events with --watch |  |

Examples:

```sh
gregale inspect my-api
gregale inspect my-api --watch
gregale inspect my-api --watch --interval 5s --timeout 10m --json
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

`gregale run [--workflow-id <ID>] [--step-label <LABEL>] [--profile <P>] [--runtime <R>] [--source <CODE>] [--file <PATH>] [--input <J|@file|->] [--timeout-ms <N>] [--memory-mb <N>] [--cpu-millicores <N>] [--ephemeral-disk-mb <N>] [--max-output-bytes <N>] [--output-file <PATH>] [--output-dir <DIR>] [--wait] [--watch] [--poll-interval <D>] [--wait-timeout <D>]`

| Flag | Meaning | |
|---|---|---|
| `--workflow-id <ID>` | caller-generated workflow grouping id |  |
| `--step-label <LABEL>` | short label for this step within --workflow-id |  |
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

`gregale runs list [--limit <N>] [--offset <N>] [--status <STATUS>] [--workflow-id <ID>]`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | maximum number of runs (1..200) |  |
| `--offset <N>` | number of matching runs to skip |  |
| `--status <STATUS>` | filter by lifecycle status | one of `queued` · `restoring` · `running` · `succeeded` · `failed` · `timed_out` · `out_of_memory` · `cancelled` |
| `--workflow-id <ID>` | filter by caller-generated workflow id |  |

### runs workflow

Show workflow status or manage an agent-owned Runs plan

`gregale runs workflow <run> <workflow-id>`

#### runs workflow run

Run, resume, or preview a disposable Runs plan

`gregale runs workflow run --manifest <PLAN.json> [--managed] [--dry-run] [--poll-interval <D>] [--wait-timeout <D>]`

| Flag | Meaning | |
|---|---|---|
| `--manifest <PLAN.json>` | JSON workflow plan file | required |
| `--managed` | continue a bounded Run DAG on the control plane after this client exits |  |
| `--dry-run` | validate and preview without creating Runs |  |
| `--poll-interval <D>` | status polling interval |  |
| `--wait-timeout <D>` | maximum client wait duration |  |

Examples:

```sh
gregale runs workflow run --manifest incident.json --json
```

### runs artifacts

Save output artifacts from a successful run

`gregale runs artifacts [--output-dir <DIR>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--output-dir <DIR>` | local destination (required) |  |

### runs get

Show one run

`gregale runs get <id>`

### runs status

Show one run (alias for get)

`gregale runs status <id>`

### runs cancel

Cancel one run

`gregale runs cancel <id>`


## invocations

Per-account invocation ledger (invocations list|get|wait &lt;id&gt;)

`gregale invocations [<subcommand>] <id>`

### invocations list

List invocations

`gregale invocations list [--limit <N>] [--cursor <CURSOR>] [--before <CURSOR>] [--all]`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1-100, default 50) |  |
| `--cursor <CURSOR>` | opaque cursor from a prior page |  |
| `--before <CURSOR>` | alias for --cursor |  |
| `--all` | walk every page using --limit and --cursor |  |

### invocations get

Show or recover one invocation

`gregale invocations get [--replay] [--replay-keyed] <id>`

| Flag | Meaning | |
|---|---|---|
| `--replay` | re-issue failed unkeyed work |  |
| `--replay-keyed` | recover failed keyed work in its captured policy lane |  |

### invocations wait

Wait for one invocation to finish (exit 124 on timeout, 130 on Ctrl-C)

`gregale invocations wait [--timeout <D>] [--interval <D>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--timeout <D>` | stop waiting without canceling the invocation (0 waits indefinitely) |  |
| `--interval <D>` | time between status checks (default 1s) |  |


## issues

Group failures and track ownership and release-aware resolution

`gregale issues [<subcommand>] [--app <SLUG>] [--deployment <UUID>] [--state <STATE>] [--environment <ENV>] [--cursor <CURSOR>] [--release-cursor <CURSOR>] [--activity-cursor <CURSOR>] [--assignee <OWNER>] [--sort <ORDER>] [--min-customers <N>] [--since <RFC3339>] [--until <RFC3339>] [--name <NAME>] [--expires-in <D>] [--rules-file <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug |  |
| `--deployment <UUID>` | fixed or token-bound deployment |  |
| `--state <STATE>` | filter issue state |  |
| `--environment <ENV>` | environment filter |  |
| `--cursor <CURSOR>` | issue-list or occurrence cursor |  |
| `--release-cursor <CURSOR>` | release history cursor |  |
| `--activity-cursor <CURSOR>` | activity history cursor |  |
| `--assignee <OWNER>` | list filter me, unassigned, or account UUID; assignment owner UUID |  |
| `--sort <ORDER>` | list order: recent or impact by verified customers in 24h |  |
| `--min-customers <N>` | issue list threshold, or impact-alert policy threshold (0 disables) |  |
| `--since <RFC3339>` | impact window start |  |
| `--until <RFC3339>` | ignore until |  |
| `--name <NAME>` | credential name |  |
| `--expires-in <D>` | credential lifetime |  |
| `--rules-file <PATH>` | ownership-rules JSON policy file to replace rules; use - for stdin |  |

Examples:

```sh
gregale issues list --app my-api
gregale issues list --app my-api --assignee me
gregale issues list --app my-api --assignee unassigned
gregale issues list --app my-api --sort impact
gregale issues impact-alert --app my-api --min-customers 5
gregale issues ownership-rules --app my-api
gregale issues ownership-rules --app my-api --rules-file issue-routing.json
gregale issues get ISSUE_ID --app my-api
gregale issues resolve ISSUE_ID --app my-api --deployment DEPLOYMENT_ID
```

### issues list

List grouped issues

`gregale issues list --app <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |

### issues get

Read evidence and release history

`gregale issues get --app <SLUG> <issue-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |

### issues assign

Assign an issue to an account

`gregale issues assign --app <SLUG> [--assignee <UUID>] <issue-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--assignee <UUID>` | owner account UUID (empty unassigns) |  |

### issues resolve

Resolve in a deployment

`gregale issues resolve --app <SLUG> --deployment <UUID> <issue-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--deployment <UUID>` | deployment UUID that fixed the issue | required |

### issues reopen

Reopen an issue

`gregale issues reopen --app <SLUG> <issue-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |

### issues ignore

Ignore until a timestamp

`gregale issues ignore --app <SLUG> --until <RFC3339> <issue-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--until <RFC3339>` | ignore until (RFC3339) | required |

### issues impact-alert

Read or configure customer-impact alert threshold

`gregale issues impact-alert --app <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |

### issues ownership-rules

Read or replace automatic assignment rules

`gregale issues ownership-rules --app <SLUG> [--rules-file <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--rules-file <PATH>` | JSON policy file to replace rules; use - for stdin |  |

### issues tokens

List ingest credentials

`gregale issues tokens --app <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |

### issues create-token

Create a deployment-bound ingest credential

`gregale issues create-token --app <SLUG> --deployment <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--deployment <UUID>` | deployment UUID the credential is bound to | required |

### issues revoke-token

Revoke an ingest credential

`gregale issues revoke-token --app <SLUG> <token-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |


## customer-operations

Inspect customer work, verify downloads and reconcile outcomes

`gregale customer-operations [<subcommand>]`

Examples:

```sh
gregale customer-operations list --app exports --scope production
gregale customer-operations watch <id> --app exports --timeout 5m --json
```

### customer-operations doctor

Observe submission blockers, delivery warnings and unverified qualification

`gregale customer-operations doctor --app <SLUG> --deployment <UUID> --tenant <UUID> [--name <NAME>] [--timeout <D>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owned app | required |
| `--deployment <UUID>` | exact deployment | required |
| `--tenant <UUID>` | owned platform tenant | required |
| `--name <NAME>` | optional operation name |  |
| `--timeout <D>` | local diagnostic deadline |  |

### customer-operations definitions

Discover deployed immutable contracts

#### customer-operations definitions list

List ordered contract metadata

`gregale customer-operations definitions list --app <SLUG> --deployment <UUID> [--timeout <D>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owned application | required |
| `--deployment <UUID>` | immutable deployment ID | required |
| `--timeout <D>` | local request deadline |  |

#### customer-operations definitions get

Read schemas and deployment pins as JSON

`gregale customer-operations definitions get --app <SLUG> --deployment <UUID> [--timeout <D>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owned application | required |
| `--deployment <UUID>` | immutable deployment ID | required |
| `--timeout <D>` | local request deadline |  |

### customer-operations validate

Validate source contracts and optional sample input without credentials

`gregale customer-operations validate --app <SLUG> --plan <PLAN> [--dir <PATH>] [--name <NAME>] [--input-file <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | selected manifest app | required |
| `--plan <PLAN>` | explicit target plan | required; one of `free` · `hobby` · `pro` · `scale` |
| `--dir <PATH>` | source directory, default current directory |  |
| `--name <NAME>` | selected operation; required for a sample |  |
| `--input-file <PATH>` | sample JSON input |  |

### customer-operations start

Submit tenant-owned work with an immutable retry receipt

`gregale customer-operations start --self <value> [--definition <UUID>] [--input-file <PATH>] [--idempotency-key <KEY>] --receipt-file <PATH> [--timeout <D>]`

| Flag | Meaning | |
|---|---|---|
| `--self <value>` | derive tenant from credentials | required |
| `--definition <UUID>` | immutable definition ID for a new receipt |  |
| `--input-file <PATH>` | JSON input for a new receipt |  |
| `--idempotency-key <KEY>` | stable request identity for a new receipt |  |
| `--receipt-file <PATH>` | private request receipt; existing files must match | required |
| `--timeout <D>` | local request deadline; resume from the receipt |  |

### customer-operations list

List one bounded page of retained business work

`gregale customer-operations list [--app <SLUG>] [--timeout <D>] --scope <SCOPE> [--tenant <UUID>] [--name <NAME>] [--state <STATE>] [--limit <N>] [--cursor <CURSOR>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--scope <SCOPE>` | explicit deployment environment | required |
| `--tenant <UUID>` | optional platform tenant filter |  |
| `--name <NAME>` | operation name |  |
| `--state <STATE>` | business state | one of `accepted` · `running` · `succeeded` · `failed` · `cancelled` · `requires_reconciliation` |
| `--limit <N>` | page size, 1–100 |  |
| `--cursor <CURSOR>` | next page cursor |  |

### customer-operations get

Read business result and independent delivery status

`gregale customer-operations get [--app <SLUG>] [--timeout <D>] [--self] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--self` | use authenticated tenant routes; omit --app |  |

### customer-operations events

Read durable event evidence and resync marker

`gregale customer-operations events [--app <SLUG>] [--timeout <D>] [--after <N>] [--self] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--after <N>` | event sequence or execution generation watermark |  |
| `--self` | use authenticated tenant routes; omit --app |  |

### customer-operations executions

Read retained execution generations and attempt counts

`gregale customer-operations executions [--app <SLUG>] [--timeout <D>] [--after <N>] [--limit <N>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--after <N>` | event sequence or execution generation watermark |  |
| `--limit <N>` | page size, 1–100 |  |

### customer-operations watch

Watch changed status; exit 0 success, 1 failure/cancel, 4 reconciliation, 124 timeout, 130 interrupt

`gregale customer-operations watch [--app <SLUG>] [--timeout <D>] [--interval <D>] [--self] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--interval <D>` | polling interval, default 1s |  |
| `--self` | use authenticated tenant routes; omit --app |  |

### customer-operations download

Publish verified artifact bytes to a new private file

`gregale customer-operations download [--app <SLUG>] [--timeout <D>] [--artifact <UUID>] --output <PATH> [--self] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--artifact <UUID>` | artifact ID; optional only when one artifact exists |  |
| `--output <PATH>` | new output file | required |
| `--self` | use authenticated tenant routes; omit --app |  |

### customer-operations cancel

Request cancellation at an observed generation

`gregale customer-operations cancel [--app <SLUG>] [--timeout <D>] --expected-generation <N> [--self] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--expected-generation <N>` | observed generation; stale decisions are rejected | required |
| `--self` | use authenticated tenant routes; omit --app |  |

### customer-operations recover

Record an evidenced reconciliation decision

`gregale customer-operations recover [--app <SLUG>] [--timeout <D>] --expected-generation <N> --recovery-id <ID> --resolution <RESOLUTION> --evidence-file <PATH> [--result-file <PATH>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--expected-generation <N>` | observed generation; stale decisions are rejected | required |
| `--recovery-id <ID>` | stable decision ID for duplicate requests | required |
| `--resolution <RESOLUTION>` | explicit reconciliation result | required; one of `succeeded` · `failed` · `cancelled` · `safe_to_retry` |
| `--evidence-file <PATH>` | nonempty reconciliation evidence | required |
| `--result-file <PATH>` | JSON result required for succeeded |  |

### customer-operations delivery

Inspect notification state, replay generation and receiver cooldown

`gregale customer-operations delivery [--app <SLUG>] [--timeout <D>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |

### customer-operations delivery-attempts

Read retained notification attempt evidence

`gregale customer-operations delivery-attempts [--app <SLUG>] [--timeout <D>] [--limit <N>] [--cursor <CURSOR>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--limit <N>` | page size, 1–100 |  |
| `--cursor <CURSOR>` | next attempt page cursor |  |

### customer-operations retry-delivery

Record a receipt-backed notification retry without repeating business work

`gregale customer-operations retry-delivery [--app <SLUG>] [--timeout <D>] [--delivery <UUID>] [--expected-replay-generation <N>] [--retry-id <ID>] --receipt-file <PATH> <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--delivery <UUID>` | observed dead delivery; omit selectors to resume |  |
| `--expected-replay-generation <N>` | observed notification generation; zero must be explicit |  |
| `--retry-id <ID>` | stable retry decision identity |  |
| `--receipt-file <PATH>` | private immutable request receipt | required |


## operations

Coordinate named work with leases, explicit contention policy, and fenced ownership

`gregale operations [<subcommand>]`

### operations policy

List or configure account operation policies

#### operations policy list

List operation policies

`gregale operations policy list`

#### operations policy upsert

Create or revise a policy from JSON

`gregale operations policy upsert --file <POLICY.json> <name>`

| Flag | Meaning | |
|---|---|---|
| `--file <POLICY.json>` | policy JSON file | required |

#### operations policy retire

Retire an idle policy and preserve ownership history

`gregale operations policy retire <name>`

### operations bind-trigger

Route an account-owned cron, inbound webhook, broker trigger, or Job schedule through a policy

`gregale operations bind-trigger --policy <NAME> --key <JSON> [--tenant <ID>] [--equivalence-key <KEY>] <cron|inbound_webhook|broker|job_schedule> <trigger-id>`

| Flag | Meaning | |
|---|---|---|
| `--policy <NAME>` | managed operation policy | required |
| `--key <JSON>` | JSON scalar business coordination key | required |
| `--tenant <ID>` | account-authorized platform customer tenant |  |
| `--equivalence-key <KEY>` | equivalent request identity for join_existing |  |

### operations unbind-trigger

Remove a trigger&#39;s managed operation policy binding

`gregale operations unbind-trigger <cron|inbound_webhook|broker|job_schedule> <trigger-id>`

### operations reconcile

Apply policies and trigger bindings declared in the project manifest

`gregale operations reconcile [--dir <PROJECT_DIR>]`

| Flag | Meaning | |
|---|---|---|
| `--dir <PROJECT_DIR>` | project directory containing gregale.yaml or gregale.toml |  |

### operations start

Submit work through a named coordination key

`gregale operations start --policy <NAME> --key <JSON> [--tenant <ID>] [--self] [--equivalence-key <KEY>] [--idempotency-key <KEY>] [--payload <JSON>] [--method <METHOD>] [--path <PATH>] <app-slug>`

| Flag | Meaning | |
|---|---|---|
| `--policy <NAME>` | managed operation policy | required |
| `--key <JSON>` | JSON scalar concurrency key | required |
| `--tenant <ID>` | authorized platform customer tenant |  |
| `--self` | derive tenant identity from a platform-customer credential |  |
| `--equivalence-key <KEY>` | equivalent request identity for join_existing |  |
| `--idempotency-key <KEY>` | stable retry identity for this submission |  |
| `--payload <JSON>` | request body delivered to the app |  |
| `--method <METHOD>` | HTTP method delivered to the app |  |
| `--path <PATH>` | app route delivered to the app |  |

Examples:

```sh
gregale operations start --policy crm-sync --key '"customer:acme:crm-sync"' --tenant TENANT_ID my-api
```

### operations start-job

Submit a Job run through a named coordination key

`gregale operations start-job --policy <NAME> --key <JSON> [--equivalence-key <KEY>] [--idempotency-key <KEY>] [--tasks <N>] [--run-file <FILE>] <job-name>`

| Flag | Meaning | |
|---|---|---|
| `--policy <NAME>` | managed operation policy | required |
| `--key <JSON>` | JSON scalar business coordination key | required |
| `--equivalence-key <KEY>` | equivalent request identity for join_existing |  |
| `--idempotency-key <KEY>` | stable retry identity for this submission |  |
| `--tasks <N>` | number of Job tasks (default 1; omit with --run-file) |  |
| `--run-file <FILE>` | JSON CreateJobRunRequest |  |

Examples:

```sh
gregale operations start-job --policy imports --key '"customer:acme:import"' nightly-import
```

### operations get

Inspect operation state, committed result, and effect delivery status

`gregale operations get [--self] <id>`

| Flag | Meaning | |
|---|---|---|
| `--self` | use the authenticated platform-customer scope |  |

### operations wait

Wait for a terminal operation state

`gregale operations wait [--self] [--timeout <DURATION>] [--interval <DURATION>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--self` | use the authenticated platform-customer scope |  |
| `--timeout <DURATION>` | stop waiting after this duration |  |
| `--interval <DURATION>` | time between status checks |  |

### operations cancel

Request cancellation of pending or active work

`gregale operations cancel [--self] <id>`

| Flag | Meaning | |
|---|---|---|
| `--self` | use the authenticated platform-customer scope |  |


## debug

Inspect production requests and regressions

`gregale debug [<subcommand>] [flags] <slug> [<request-id>]`

### debug requests

Per-request telemetry and root-cause synthesis

#### debug requests list

List recent request telemetry

`gregale debug requests list [--since <DURATION>] [--route <PATH>] [--deployment-id <UUID>] [--status <N>] [--cold-boot <BOOL>] [--consumer-id <ID>] [--min-latency-ms <N>] [--limit <N>] [--cursor <CURSOR>] [--all] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | lookback window |  |
| `--route <PATH>` | exact route filter |  |
| `--deployment-id <UUID>` | deployment filter |  |
| `--status <N>` | exact HTTP status (100..599) |  |
| `--cold-boot <BOOL>` | cold-start filter | one of `true` · `false` |
| `--consumer-id <ID>` | consumer UUID or __anonymous__ |  |
| `--min-latency-ms <N>` | minimum latency bucket in milliseconds |  |
| `--limit <N>` | maximum rows per page (1..200) |  |
| `--cursor <CURSOR>` | opaque pagination cursor |  |
| `--all` | walk every retained page |  |

#### debug requests export

Export metadata-only request telemetry

`gregale debug requests export [--since <DURATION>] [--route <PATH>] [--format <FORMAT>] [--limit <N>] [--output <PATH>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | lookback window |  |
| `--route <PATH>` | exact route filter |  |
| `--format <FORMAT>` | export format (default ndjson) | one of `ndjson` · `csv` |
| `--limit <N>` | maximum exported rows (1..10000) |  |
| `--output <PATH>` | destination file; - writes stdout |  |

#### debug requests watch

Watch new or changed telemetry rows

`gregale debug requests watch [--since <DURATION>] [--route <PATH>] [--deployment-id <UUID>] [--status <N>] [--cold-boot <BOOL>] [--consumer-id <ID>] [--min-latency-ms <N>] [--limit <N>] [--interval <DURATION>] [--once] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | lookback window |  |
| `--route <PATH>` | exact route filter |  |
| `--deployment-id <UUID>` | deployment filter |  |
| `--status <N>` | exact HTTP status (100..599) |  |
| `--cold-boot <BOOL>` | cold-start filter | one of `true` · `false` |
| `--consumer-id <ID>` | consumer UUID or __anonymous__ |  |
| `--min-latency-ms <N>` | minimum latency bucket in milliseconds |  |
| `--limit <N>` | maximum rows per poll (1..200) |  |
| `--interval <DURATION>` | poll interval (250ms..1h) |  |
| `--once` | poll once and exit |  |

#### debug requests get

Show request metadata

`gregale debug requests get <slug> <request-id-or-row-id>`

#### debug requests show

Show the request timeline and evidence

`gregale debug requests show <slug> <request-id-or-row-id>`

#### debug requests evidence

Show request evidence and explanation

`gregale debug requests evidence <slug> <request-id-or-row-id>`

#### debug requests explain

Synthesize findings and next actions

`gregale debug requests explain <slug> <request-id-or-row-id>`

#### debug requests trace

Show the linked span tree

`gregale debug requests trace <slug> <request-id-or-row-id>`

#### debug requests inspect

Inspect one retained request or select --latest

`gregale debug requests inspect [--since <DURATION>] [--route <PATH>] [--deployment-id <UUID>] [--status <N>] [--cold-boot <BOOL>] [--consumer-id <ID>] [--min-latency-ms <N>] [--latest] <slug> [<request-id-or-row-id>]`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | lookback window |  |
| `--route <PATH>` | exact route filter |  |
| `--deployment-id <UUID>` | deployment filter |  |
| `--status <N>` | exact HTTP status (100..599) |  |
| `--cold-boot <BOOL>` | cold-start filter | one of `true` · `false` |
| `--consumer-id <ID>` | consumer UUID or __anonymous__ |  |
| `--min-latency-ms <N>` | minimum latency bucket in milliseconds |  |
| `--latest` | select the newest retained request matching the filters |  |

#### debug requests replay

Queue a request replay against a mirror target

`gregale debug requests replay [--deployment-id <UUID>] [--deployment <UUID>] [--wait] [--timeout <DURATION>] [--interval <DURATION>] <slug> <request-id-or-row-id>`

| Flag | Meaning | |
|---|---|---|
| `--deployment-id <UUID>` | enabled mirror target deployment |  |
| `--deployment <UUID>` | alias for --deployment-id |  |
| `--wait` | wait for a terminal replay state |  |
| `--timeout <DURATION>` | maximum wait (1s..1h) |  |
| `--interval <DURATION>` | poll interval (250ms..1m) |  |

### debug dependencies

Show observed dependency latency and regressions

`gregale debug dependencies [--since <DURATION>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | lookback window |  |

### debug coverage

Observed debugger signal coverage (coverage &lt;slug&gt; [--since D])

`gregale debug coverage [--since <DURATION>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | lookback window |  |

### debug running

Explain why an app is still running, with request evidence when available (running &lt;slug&gt; [--since D] [--limit N])

`gregale debug running [--since <DURATION>] [--limit <N>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | lookback window |  |
| `--limit <N>` | maximum recent observations (1..100; default 20) |  |

### debug regressions

List, watch, triage, or roll back regressions

`gregale debug regressions <watch|ack|acknowledge|dismiss|resolve|reopen|rollback> [--since <DURATION>] [--all] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | lookback window |  |
| `--all` | include every app in the account |  |

#### debug regressions watch

Watch regression changes (live stream by default)

`gregale debug regressions watch [--since <DURATION>] [--interval <DURATION>] [--all] [--poll] [--once] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | lookback window |  |
| `--interval <DURATION>` | poll interval (250ms..1h) |  |
| `--all` | watch every app in the account |  |
| `--poll` | poll instead of using the live event stream |  |
| `--once` | poll once and exit |  |

#### debug regressions ack

Acknowledge a regression

`gregale debug regressions ack --deployment-id <UUID> --route <PATH> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment-id <UUID>` | regression deployment UUID | required |
| `--route <PATH>` | regression route | required |

#### debug regressions acknowledge

Acknowledge a regression

`gregale debug regressions acknowledge --deployment-id <UUID> --route <PATH> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment-id <UUID>` | regression deployment UUID | required |
| `--route <PATH>` | regression route | required |

#### debug regressions dismiss

Dismiss a regression

`gregale debug regressions dismiss --deployment-id <UUID> --route <PATH> [--dismissed-until <RFC3339>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment-id <UUID>` | regression deployment UUID | required |
| `--route <PATH>` | regression route | required |
| `--dismissed-until <RFC3339>` | dismissal expiry (RFC3339; default 24h) |  |

#### debug regressions resolve

Resolve a regression

`gregale debug regressions resolve --deployment-id <UUID> --route <PATH> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment-id <UUID>` | regression deployment UUID | required |
| `--route <PATH>` | regression route | required |

#### debug regressions reopen

Reopen a regression

`gregale debug regressions reopen --deployment-id <UUID> --route <PATH> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment-id <UUID>` | regression deployment UUID | required |
| `--route <PATH>` | regression route | required |

#### debug regressions rollback

Roll back an app from the regressions view

`gregale debug regressions rollback [--to <ID>] --yes <slug>`

| Flag | Meaning | |
|---|---|---|
| `--to <ID>` | target superseded deployment ID |  |
| `--yes` | confirm the rollback | required |

### debug compare

Per-route deployment-vs-deployment compare

`gregale debug compare --source <ID> --mirror <ID> [--route <PATH>] [--since <DURATION>] [--until <RFC3339>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--source <ID>` | source deployment ID | required |
| `--mirror <ID>` | comparison deployment ID | required |
| `--route <PATH>` | exact route filter |  |
| `--since <DURATION>` | lookback window |  |
| `--until <RFC3339>` | end of comparison window (RFC3339) |  |

### debug bundle

Export a redacted incident bundle with coverage

`gregale debug bundle [--since <DURATION>] [--route <PATH>] [--source <ID>] [--mirror <ID>] [--output <PATH>] <slug> <request-id-or-row-id>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | regression and comparison lookback |  |
| `--route <PATH>` | route filter for optional deployment comparison |  |
| `--source <ID>` | source deployment ID (requires --mirror) |  |
| `--mirror <ID>` | comparison deployment ID (requires --source) |  |
| `--output <PATH>` | write bundle to PATH (default stdout; use - for stdout) |  |


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

`gregale invitations peek <token>`

### invitations accept

Accept an invitation

`gregale invitations accept <token>`


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

`gregale keys add [--scopes <SCOPE,...>] <label>`

| Flag | Meaning | |
|---|---|---|
| `--scopes <SCOPE,...>` | comma-separated API key scopes; omit for full admin access |  |

Examples:

```sh
gregale keys add agent-runner --scopes runs:write
```

### keys rm

Revoke an API key

`gregale keys rm <id>`

### keys rotate

Rotate an API key

`gregale keys rotate <key-id>`

Examples:

```sh
gregale keys rotate 7f8c2a1e-6d3b-4c55-9a7e-0b1d2c3e4f5a
```

### keys grace-window

Read or update the rotation grace window

`gregale keys grace-window [--reset] [--days <N>]`

| Flag | Meaning | |
|---|---|---|
| `--reset` | clear the account override and use the plan default |  |
| `--days <N>` | new grace window in days (0 or greater) |  |


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


## profile

Manage named API connections and isolated credentials

`gregale profile [<subcommand>]`

### profile add

Add a connection without changing the active profile

`gregale profile add <name> <api-url>`

### profile list

List connections and the active profile

### profile check

Verify the selected API connection and account identity

`gregale profile check [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--timeout <DURATION>` | maximum request duration (default 10s) |  |

Examples:

```sh
gregale profile check
gregale --profile staging profile check --timeout 5s --json
```

### profile use

Select the default connection

`gregale profile use <name>`

### profile remove

Remove an inactive connection and its credentials

`gregale profile remove <name>`


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

`gregale mfa enroll [--qr-out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--qr-out <PATH>` | write the QR PNG to this path |  |

### mfa confirm

Confirm an enrolment code (positional code or --code)

`gregale mfa confirm [--code <CODE>] [<6-digit-code>]`

| Flag | Meaning | |
|---|---|---|
| `--code <CODE>` | 6-digit TOTP (alternative to positional code) |  |

### mfa verify

Verify a TOTP code (step-up; positional code or --code)

`gregale mfa verify [--code <CODE>] [<6-digit-code>]`

| Flag | Meaning | |
|---|---|---|
| `--code <CODE>` | 6-digit TOTP (alternative to positional code) |  |

### mfa recover

Use a recovery code (positional code or --code)

`gregale mfa recover [--code <CODE>] [<recovery-code>]`

| Flag | Meaning | |
|---|---|---|
| `--code <CODE>` | recovery code (alternative to positional code) |  |

### mfa disable

Disable MFA

`gregale mfa disable [--password <PASSWORD>] [--recovery-code <CODE>]`

| Flag | Meaning | |
|---|---|---|
| `--password <PASSWORD>` | account password (prompts when omitted) |  |
| `--recovery-code <CODE>` | single-use recovery code (alternative to password) |  |


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

`gregale orgs create --slug <SLUG> --name <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--slug <SLUG>` | org slug (lowercase alphanumeric + dashes) | required |
| `--name <TEXT>` | display name | required |

### orgs info

Show one org

`gregale orgs info <slug>`

### orgs activity

Show the global infrastructure timeline

`gregale orgs activity --org <SLUG> [--before <CURSOR>] [--cursor <CURSOR>] [--all] [--kind-prefix <PREFIX>] [--actor-type <TYPE>] [--app-id <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--before <CURSOR>` | alias for --cursor |  |
| `--cursor <CURSOR>` | opaque continuation cursor |  |
| `--all` | walk every page |  |
| `--kind-prefix <PREFIX>` | filter by activity kind prefix |  |
| `--actor-type <TYPE>` | filter by actor category | one of `user` · `api_key` · `github` · `system` · `operator` |
| `--app-id <UUID>` | filter by application UUID |  |
| `--limit <N>` | page size (1..100) |  |

### orgs rm

Delete one org

`gregale orgs rm [-q] <slug>`

| Flag | Meaning | |
|---|---|---|
| `-q` | skip the confirmation prompt |  |

### orgs members

Manage org members

#### orgs members list

List org members

`gregale orgs members list <slug>`

#### orgs members invite

Invite a member by email

`gregale orgs members invite --org <SLUG> --email <ADDR> [--role <ROLE>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--email <ADDR>` | invitee email | required |
| `--role <ROLE>` | role (default developer; owner is rejected) | one of `admin` · `developer` · `viewer` · `billing` |

#### orgs members change-role

Change a member&#39;s role

`gregale orgs members change-role --org <SLUG> --user <USER-ID> --role <ROLE>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--user <USER-ID>` | member user ID | required |
| `--role <ROLE>` | new role (owner is rejected) | required; one of `admin` · `developer` · `viewer` · `billing` |

#### orgs members rm

Remove a member

`gregale orgs members rm --org <SLUG> --user <USER-ID>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--user <USER-ID>` | member user ID | required |

### orgs keys

Manage org API keys

#### orgs keys list

List org API keys

`gregale orgs keys list --org <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |

#### orgs keys add

Create an org API key; the plaintext is shown once

`gregale orgs keys add --org <SLUG> --label <TEXT> [--scopes <SCOPES>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--label <TEXT>` | key label | required |
| `--scopes <SCOPES>` | comma-separated scopes (default admin) |  |

#### orgs keys info

Show one org API key

`gregale orgs keys info --org <SLUG> <key-id>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |

#### orgs keys rm

Revoke an org API key

`gregale orgs keys rm --org <SLUG> <key-id>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |

#### orgs keys rotate

Rotate an org API key; the new plaintext is shown once

`gregale orgs keys rotate --org <SLUG> [--label <TEXT>] <key-id>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--label <TEXT>` | new label (empty keeps the current one) |  |

### orgs transfer-ownership

Transfer org ownership

`gregale orgs transfer-ownership --org <SLUG> --to <USER-ID>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--to <USER-ID>` | user ID of the new owner | required |

### orgs seat-usage

Show seat usage

`gregale orgs seat-usage --org <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |

### orgs invitations

Manage org invitations

#### orgs invitations list

List pending invitations

`gregale orgs invitations list --org <SLUG> [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--limit <N>` | max rows (1..200) |  |

#### orgs invitations list-all

List invitations in every state

`gregale orgs invitations list-all --org <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |

#### orgs invitations revoke

Revoke a pending invitation

`gregale orgs invitations revoke --org <SLUG> --invitation <ID>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--invitation <ID>` | invitation ID | required |

### orgs me

Show current org membership

### orgs update

Update org metadata

`gregale orgs update --org <SLUG> [--name <TEXT>] [--plan <PLAN>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--name <TEXT>` | new display name (1..120 chars) |  |
| `--plan <PLAN>` | new plan | one of `free` · `hobby` · `pro` · `scale` |


## overage-cap

Set / clear the account&#39;s overage cap (--clear | &lt;cents&gt;)

`gregale overage-cap <cents> [--clear]`

| Flag | Meaning | |
|---|---|---|
| `--clear` | remove the overage cap |  |


## park

Park an app cold (kill all live instances)

`gregale park <slug>`

Examples:

```sh
gregale park my-api
```


## plan

Change plan (free|hobby|pro|scale); paid upgrades open the provider checkout

`gregale plan`


## data-api

Create schema-generated PostgreSQL APIs and export application types

`gregale data-api [<subcommand>]`

### data-api create

Deploy a managed PostgREST Data API

`gregale data-api create --database <DATABASE> [--schema <SCHEMA>] [--scope <SCOPE>] --issuer <HTTPS_URL> --jwks-url <HTTPS_URL> --audience <AUDIENCE> [--origins <ORIGINS>] [--resume] <name>`

| Flag | Meaning | |
|---|---|---|
| `--database <DATABASE>` | ready managed database name or ID | required |
| `--schema <SCHEMA>` | exposed schema (api) |  |
| `--scope <SCOPE>` | environment scope |  |
| `--issuer <HTTPS_URL>` | application JWT issuer | required |
| `--jwks-url <HTTPS_URL>` | application JWKS URL | required |
| `--audience <AUDIENCE>` | application JWT audience | required |
| `--origins <ORIGINS>` | comma-separated browser origins |  |
| `--resume` | resume configuration and deployment of an existing app |  |

### data-api types

Generate types in an owner-authenticated app task

`gregale data-api types [--output <FILE>] [--check] [--snapshot] [--timeout <DURATION>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--output <FILE>` | generated TypeScript or snapshot output |  |
| `--check` | fail if the output file is stale |  |
| `--snapshot` | export a JSON baseline for data-api diff |  |
| `--timeout <DURATION>` | task wait deadline (default 2m) |  |

### data-api diff

Compare the current schema with a saved JSON baseline

`gregale data-api diff --baseline <FILE> [--check] [--timeout <DURATION>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--baseline <FILE>` | JSON snapshot exported with types --snapshot | required |
| `--check` | fail on breaking contract changes |  |
| `--timeout <DURATION>` | task wait deadline (default 2m) |  |

Examples:

```sh
gregale data-api diff notes-data --baseline schema.json --check
```

### data-api refresh

Request a fresh restart to reload the database schema

`gregale data-api refresh [--wait] [--timeout <DURATION>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for fresh-restart completion and Data API readiness |  |
| `--timeout <DURATION>` | complete wait deadline (default 5m, maximum 1h; requires --wait) |  |

Examples:

```sh
gregale data-api refresh notes-data --wait --timeout 5m
```

### data-api sync

Run migrations, refresh the API, export types and check the client

`gregale data-api sync --config <FILE> [--timeout <DURATION>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--config <FILE>` | JSON workflow with output, migrate and check argument arrays | required |
| `--timeout <DURATION>` | entire workflow deadline (default 20m, maximum 1h) |  |

Examples:

```sh
gregale data-api sync notes-data --config data-api.json
```


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

`gregale queue tail <slug>`

### queue send

Enqueue a wake request

`gregale queue send [--payload <J>] [--queue-name <QUEUE>] [--environment <ENV>] [--work-policy <NAME>] [--work-key <JSON>] [--work-fairness-key <JSON>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--payload <J>` | JSON payload (inline \| @file \| -) |  |
| `--queue-name <QUEUE>` | logical queue name |  |
| `--environment <ENV>` | registered project environment with an enabled queue binding |  |
| `--work-policy <NAME>` | named work policy for an unnamed queue |  |
| `--work-key <JSON>` | JSON scalar identifying related work |  |
| `--work-fairness-key <JSON>` | JSON scalar shared by related work keys |  |

### queue receive

Wait for the next queue row the platform delivers

`gregale queue receive <slug>`

### queue state

Show queue state

`gregale queue state <slug>`

### queue status

Show queue depth, scaling, bindings, and liveness

`gregale queue status <slug>`

### queue peek

Peek at the next wake

`gregale queue peek [--limit <N>] [--cursor <CURSOR>] [--before <CURSOR>] [--all] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1..100, default 50) |  |
| `--cursor <CURSOR>` | opaque continuation cursor |  |
| `--before <CURSOR>` | alias for --cursor |  |
| `--all` | walk every page using --limit and --cursor |  |

### queue dead-letter

Inspect the dead-letter queue

`gregale queue dead-letter [--limit <N>] [--cursor <CURSOR>] [--before <CURSOR>] [--all] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1..100, default 50) |  |
| `--cursor <CURSOR>` | opaque continuation cursor |  |
| `--before <CURSOR>` | alias for --cursor |  |
| `--all` | walk every page using --limit and --cursor |  |

### queue ack

Ack a wake

`gregale queue ack <slug> <row-id>`

### queue setup

Configure a simple push workload with queue-depth scaling

`gregale queue setup [--queue-name <QUEUE>] [--target-depth <N>] [--max-concurrency <N>] [--max-attempts <N>] [--retry-base-seconds <N>] [--retry-max-seconds <N>] [--retry-jitter-seconds <N>] [--force] <slug>`

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

#### queue bindings list

List app queue bindings

`gregale queue bindings list [--include-retired] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--include-retired` | include retained binding UUIDs for reviewed recovery |  |

#### queue bindings create

Bind the app to a queue

`gregale queue bindings create --name <NAME> --queue-name <QUEUE> [--mode <MODE>] [--workload-class <CLASS>] [--max-concurrency <N>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | binding name | required |
| `--queue-name <QUEUE>` | queue to bind | required |
| `--mode <MODE>` | delivery mode | one of `pull` · `push` |
| `--workload-class <CLASS>` | consumer workload class | one of `worker` · `job` · `http` |
| `--max-concurrency <N>` | maximum concurrent deliveries |  |

#### queue bindings update

Update a queue binding

`gregale queue bindings update [--queue-name <QUEUE>] [--mode <MODE>] [--workload-class <CLASS>] [--max-concurrency <N>] <slug> <binding-id>`

| Flag | Meaning | |
|---|---|---|
| `--queue-name <QUEUE>` | queue to bind |  |
| `--mode <MODE>` | delivery mode | one of `pull` · `push` |
| `--workload-class <CLASS>` | consumer workload class | one of `worker` · `job` · `http` |
| `--max-concurrency <N>` | maximum concurrent deliveries |  |

#### queue bindings rm

Retire a queue binding

`gregale queue bindings rm <slug> <binding-id>`


## dlq

Inspect, replay, or purge unified dead-letter events

`gregale dlq [<subcommand>] <app> [<event-id>]`

### dlq list

List app dead-letter events

`gregale dlq list [--limit <N>] [--before <ID>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | max events (1..200) |  |
| `--before <ID>` | pagination cursor |  |

### dlq inspect

Inspect one dead-letter event

`gregale dlq inspect <app> <event-id>`

### dlq replay

Replay one event or --all

`gregale dlq replay [--all] [--limit <N>] <app> [<event-id>]`

| Flag | Meaning | |
|---|---|---|
| `--all` | replay pending events |  |
| `--limit <N>` | maximum events (1..200) |  |

### dlq purge

Purge one event or --all

`gregale dlq purge [--all] [--limit <N>] <app> [<event-id>]`

| Flag | Meaning | |
|---|---|---|
| `--all` | purge all events |  |
| `--limit <N>` | page size (1..200) |  |


## registry

Manage private registry credentials and deploy published images

`gregale registry [<subcommand>]`

### registry published

Deploy an image after CI publishes its immutable digest

`gregale registry published --app <slug> --image <REF> [--scope <SLUG>] [--environment <SLUG>] [--wait] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--image <REF>` | published digest-pinned image reference | required |
| `--scope <SLUG>` | deployment scope |  |
| `--environment <SLUG>` | registered project environment |  |
| `--wait` | wait for the image deployment |  |
| `--timeout <DURATION>` | deployment wait timeout |  |

### registry list

List registry credentials

`gregale registry list --app <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### registry set

Set a registry credential

`gregale registry set --app <slug> --registry <host> --user <user> [--password-stdin]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--registry <host>` | registry host | required |
| `--user <user>` | registry username | required |
| `--password-stdin` | read the registry password/token from stdin |  |

### registry rm

Remove a registry credential

`gregale registry rm --app <slug> --registry <host>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--registry <host>` | registry host | required |


## realtime

Manage realtime endpoints, policies, connections, channels, and auth

`gregale realtime [<subcommand>]`

### realtime list

List managed realtime endpoints

`gregale realtime list <app>`

### realtime get

Show one endpoint and safe auth-rotation status

`gregale realtime get <app> <endpoint-id>`

### realtime create

Create a managed realtime endpoint

`gregale realtime create --callback-url <URL> [--callback-auth-token-stdin] [--callback-auth-token <TOKEN>] [--connect-path <PATH>] [--message-path <PATH>] [--disconnect-path <PATH>] [--auth-mode <MODE>] [--auth-token-stdin] [--auth-token <TOKEN>] [--auth-issuer <URL>] [--auth-jwks-url <URL>] [--auth-audience <AUDIENCE>]... [--auth-algorithm <ALG>]... [--auth-claim <KEY=VALUE>]... [--allowed-origin <ORIGIN>]... [--max-connections <N>] [--max-message-bytes <N>] [--max-connection-age-seconds <SECONDS>] [--enabled[=true|false]] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--callback-url <URL>` | application callback URL | required |
| `--callback-auth-token-stdin` | read the callback bearer token from stdin (this or --callback-auth-token is required) |  |
| `--callback-auth-token <TOKEN>` | callback bearer token your app verifies (prefer --callback-auth-token-stdin) |  |
| `--connect-path <PATH>` | callback path for connect events |  |
| `--message-path <PATH>` | callback path for message events |  |
| `--disconnect-path <PATH>` | callback path for disconnect events |  |
| `--auth-mode <MODE>` | client auth mode | one of `none` · `static_bearer` · `oidc_jwt` |
| `--auth-token-stdin` | read the client static bearer token from stdin |  |
| `--auth-token <TOKEN>` | client static bearer token (prefer --auth-token-stdin) |  |
| `--auth-issuer <URL>` | OIDC issuer URL |  |
| `--auth-jwks-url <URL>` | OIDC JWKS URL |  |
| `--auth-audience <AUDIENCE>` | OIDC audience (repeatable) |  |
| `--auth-algorithm <ALG>` | OIDC signing algorithm (repeatable) |  |
| `--auth-claim <KEY=VALUE>` | required OIDC claim as KEY=VALUE (repeatable) |  |
| `--allowed-origin <ORIGIN>` | exact browser origin (repeatable) |  |
| `--max-connections <N>` | per-endpoint connection cap (0 inherits the default) |  |
| `--max-message-bytes <N>` | decoded message size cap (0 inherits the default) |  |
| `--max-connection-age-seconds <SECONDS>` | connection age cap (0 inherits the default) |  |
| `--enabled[=true|false]` | create enabled or disabled | one of `true` · `false` |

### realtime update

Update endpoint callback, auth, or connection policy

`gregale realtime update [--callback-url <URL>] [--callback-auth-token-stdin] [--callback-auth-token <TOKEN>] [--connect-path <PATH>] [--message-path <PATH>] [--disconnect-path <PATH>] [--auth-token-stdin] [--auth-token <TOKEN>] [--auth-mode <MODE>] [--auth-issuer <URL>] [--auth-jwks-url <URL>] [--auth-audience <AUDIENCE>]... [--auth-algorithm <ALG>]... [--auth-claim <KEY=VALUE>]... [--clear-auth-claims] [--allowed-origin <ORIGIN>]... [--clear-allowed-origins] [--max-connections <N>] [--max-message-bytes <N>] [--max-connection-age-seconds <SECONDS>] [--enable] [--disable] <app> <endpoint-id>`

| Flag | Meaning | |
|---|---|---|
| `--callback-url <URL>` | new application callback URL |  |
| `--callback-auth-token-stdin` | read replacement callback bearer token from stdin |  |
| `--callback-auth-token <TOKEN>` | replacement callback bearer token (prefer --callback-auth-token-stdin) |  |
| `--connect-path <PATH>` | new callback path for connect events |  |
| `--message-path <PATH>` | new callback path for message events |  |
| `--disconnect-path <PATH>` | new callback path for disconnect events |  |
| `--auth-token-stdin` | read replacement client bearer token from stdin |  |
| `--auth-token <TOKEN>` | new client static bearer token (prefer --auth-token-stdin) |  |
| `--auth-mode <MODE>` | new client auth mode | one of `none` · `static_bearer` · `oidc_jwt` |
| `--auth-issuer <URL>` | new OIDC issuer URL |  |
| `--auth-jwks-url <URL>` | new OIDC JWKS URL |  |
| `--auth-audience <AUDIENCE>` | replace OIDC audiences (repeatable) |  |
| `--auth-algorithm <ALG>` | replace OIDC signing algorithms (repeatable) |  |
| `--auth-claim <KEY=VALUE>` | set required OIDC claim as KEY=VALUE (repeatable) |  |
| `--clear-auth-claims` | remove all required OIDC claims |  |
| `--allowed-origin <ORIGIN>` | replace exact browser origins (repeatable) |  |
| `--clear-allowed-origins` | remove the browser-origin allowlist |  |
| `--max-connections <N>` | new per-endpoint connection cap |  |
| `--max-message-bytes <N>` | new decoded message size cap |  |
| `--max-connection-age-seconds <SECONDS>` | new connection age cap |  |
| `--enable` | enable the endpoint |  |
| `--disable` | disable the endpoint |  |

### realtime delete

Delete a managed realtime endpoint

`gregale realtime delete --yes <value> <app> <endpoint-id>`

| Flag | Meaning | |
|---|---|---|
| `--yes <value>` | confirm the deletion | required |

### realtime connections

List live connections for an endpoint

`gregale realtime connections [--channel <CHANNEL>] [--principal <PRINCIPAL>] [--limit <N>] [--cursor <TOKEN>] <app> <endpoint-id>`

| Flag | Meaning | |
|---|---|---|
| `--channel <CHANNEL>` | only connections subscribed to this channel |  |
| `--principal <PRINCIPAL>` | only connections for this authenticated principal |  |
| `--limit <N>` | maximum connections to return (1-1000) |  |
| `--cursor <TOKEN>` | continue from a previous response&#39;s next_cursor |  |

### realtime drain

Close a bounded, filtered set of live connections

`gregale realtime drain --reason <TEXT> [--channel <CHANNEL>] [--principal <PRINCIPAL>] [--connection-id <ID>] [--limit <N>] [--dry-run] [--allow-partial] [--wait] [--timeout <DURATION>] <app> <endpoint-id>`

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

`gregale realtime drain-status [--wait] [--timeout <DURATION>] <app> <endpoint-id> <operation-id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the drain to reach a terminal state |  |
| `--timeout <DURATION>` | maximum time to wait with --wait |  |

### realtime send

Send a message to one live connection

`gregale realtime send [--data <DATA>] [--data-stdin] [--binary] <app> <endpoint-id> <connection-id>`

| Flag | Meaning | |
|---|---|---|
| `--data <DATA>` | message text (or --data-stdin) |  |
| `--data-stdin` | read the message from stdin |  |
| `--binary` | send as a binary frame |  |

### realtime close

Close one live connection

`gregale realtime close [--reason <TEXT>] <app> <endpoint-id> <connection-id>`

| Flag | Meaning | |
|---|---|---|
| `--reason <TEXT>` | close reason |  |

### realtime subscribe

Subscribe one live connection to a channel

`gregale realtime subscribe <app> <endpoint-id> <connection-id> <channel>`

### realtime unsubscribe

Remove one live connection from a channel

`gregale realtime unsubscribe <app> <endpoint-id> <connection-id> <channel>`

### realtime publish

Publish a message to a channel

`gregale realtime publish [--data <DATA>] [--data-stdin] [--binary] [--delivery <MODE>] [--idempotency-key <KEY>] <app> <endpoint-id> <channel>`

| Flag | Meaning | |
|---|---|---|
| `--data <DATA>` | message text (or --data-stdin) |  |
| `--data-stdin` | read the message from stdin |  |
| `--binary` | send as a binary frame |  |
| `--delivery <MODE>` | live by default or preview-only retained (up to 4 KiB) | one of `live` · `retained` |
| `--idempotency-key <KEY>` | stable retry key; required for retained delivery |  |

### realtime auth

Rotate, finalize, or inspect static bearer auth

#### realtime auth rotate

Stage a new bearer token; the old one stays valid for the grace period

`gregale realtime auth rotate [--token-stdin] [--token <TOKEN>] [--grace-period <SECONDS>] <app> <endpoint-id>`

| Flag | Meaning | |
|---|---|---|
| `--token-stdin` | read the new token from stdin |  |
| `--token <TOKEN>` | new token (prefer --token-stdin) |  |
| `--grace-period <SECONDS>` | seconds the previous token stays valid |  |

#### realtime auth finalize

Retire the previous bearer token now

`gregale realtime auth finalize <app> <endpoint-id>`

#### realtime auth status

Show bearer token rotation state

`gregale realtime auth status <app> <endpoint-id>`


## rollback

Restore a previous deployment, or check an exact historical rollback

`gregale rollback [<subcommand>] <slug> [--to <deployment_id|vN>] [--expected-current <deployment_id|vN>] [--reason <TEXT>] [--wait] [--timeout <duration>] [--poll-interval <duration>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--to <deployment_id|vN>` | target deployment id or vN revision (e.g. v41) |  |
| `--expected-current <deployment_id|vN>` | exact completed serving deployment; requires --to |  |
| `--reason <TEXT>` | one-line reason of at most 256 bytes; requires --expected-current |  |
| `--wait` | wait for binding checks and service handoff completion; requires --expected-current |  |
| `--timeout <duration>` | wait deadline (default 10m) |  |
| `--poll-interval <duration>` | poll interval (default 2s) |  |
| `--json` | machine-readable output |  |

Examples:

```sh
gregale rollback my-api
gregale rollback my-api --to v41
gregale rollback my-api --to v41 --expected-current v42 --wait
```

### rollback status

Read an exact rollback operation; waiting never submits another rollback

`gregale rollback status --operation <UUID> [--wait] [--timeout <duration>] [--poll-interval <duration>] [--json] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--operation <UUID>` | accepted rollback operation UUID | required |
| `--wait` | wait for completion with a committed audit receipt |  |
| `--timeout <duration>` | wait deadline (default 10m) |  |
| `--poll-interval <duration>` | poll interval (default 2s) |  |
| `--json` | print the operation receipt |  |


## projects

Inspect and recover repository projects

`gregale projects [<subcommand>] <project-slug>`

### projects list

List projects in this account

### projects info

Show a project and its workloads

`gregale projects info <slug>`

### projects environments

Manage project environments (list|create|protect|unprotect|inspect|release-sets|releases|qualify|preflight|history|config [set]|routes set|queues get|queues set|diff|preview|promote|status|rollback); qualify binds probes to release, config, and secret revisions

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

`gregale projects environments release-sets [--before <CURSOR>] [--cursor <CURSOR>] [--all] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--before <CURSOR>` | alias for --cursor |  |
| `--cursor <CURSOR>` | opaque continuation cursor |  |
| `--all` | walk every page |  |
| `--limit <N>` | page size |  |

#### projects environments releases

List live workload deployments

`gregale projects environments releases`

#### projects environments qualify

Run health and smoke GET probes against exact active release-set deployments

`gregale projects environments qualify --profile <FILE> <project-slug> <environment-slug>`

| Flag | Meaning | |
|---|---|---|
| `--profile <FILE>` | YAML probe profile defining every release-set workload | required |

#### projects environments preflight

Qualify the source release and check promotion readiness for CI

`gregale projects environments preflight --from <ENV> --to <ENV> --profile <FILE> [--sync-config] <project-slug>`

| Flag | Meaning | |
|---|---|---|
| `--from <ENV>` | source environment to qualify and promote | required |
| `--to <ENV>` | target environment to check | required |
| `--profile <FILE>` | YAML probe profile defining every source workload | required |
| `--sync-config` | include non-secret source config in the promotion preview |  |

#### projects environments history

List environment promotions

`gregale projects environments history [--before <CURSOR>] [--cursor <CURSOR>] [--all] [--limit <N>] [--from <ENV>] [--status <STATUS>]`

| Flag | Meaning | |
|---|---|---|
| `--before <CURSOR>` | alias for --cursor |  |
| `--cursor <CURSOR>` | opaque continuation cursor |  |
| `--all` | walk every page |  |
| `--limit <N>` | page size (1..100) |  |
| `--from <ENV>` | source environment filter |  |
| `--status <STATUS>` | running\|succeeded\|failed |  |

#### projects environments config

Read or update environment configuration

`gregale projects environments config <set|apply> <project-slug> <environment-slug>`

##### projects environments config set

Update environment configuration with optimistic hash checking

`gregale projects environments config set [--file <PATH>] [--stdin] [--dry-run] [--if-hash <HASH>] [--yes] <project-slug> <environment-slug>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | JSON configuration file (choose --file or --stdin) |  |
| `--stdin` | read JSON configuration from stdin |  |
| `--dry-run` | preview the change without writing it |  |
| `--if-hash <HASH>` | apply only if the current config hash matches |  |
| `--yes` | confirm the configuration update |  |

##### projects environments config apply

Alias for config set

`gregale projects environments config apply [--file <PATH>] [--stdin] [--dry-run] [--if-hash <HASH>] [--yes] <project-slug> <environment-slug>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | JSON configuration file (choose --file or --stdin) |  |
| `--stdin` | read JSON configuration from stdin |  |
| `--dry-run` | preview the change without writing it |  |
| `--if-hash <HASH>` | apply only if the current config hash matches |  |
| `--yes` | confirm the configuration update |  |

#### projects environments routes

Manage environment routes

`gregale projects environments routes`

#### projects environments policies

Manage environment policies

`gregale projects environments policies`

#### projects environments queues

Read or replace a stage workload&#39;s complete desired queue collection; consumer activation is unavailable. Set input contains expected_revision and bindings; [] removes all definitions

`gregale projects environments queues <get|set> [--file <PATH>] [--stdin] <get|set> <project> <stage> <workload>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | set reads this JSON file (choose --file or --stdin) |  |
| `--stdin` | set reads JSON from stdin |  |

Examples:

```sh
gregale projects environments queues get shop staging shop-worker
gregale projects environments queues set shop staging shop-worker --file queues.json
```

##### projects environments queues get

Read queue definitions and their workload revision as JSON

`gregale projects environments queues get <project> <stage> <workload>`

Examples:

```sh
gregale projects environments queues get shop staging shop-worker
```

##### projects environments queues set

Replace queue definitions from JSON containing expected_revision and bindings; [] removes all stage bindings

`gregale projects environments queues set [--file <PATH>] [--stdin] <project> <stage> <workload>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | JSON queue configuration file (choose --file or --stdin) |  |
| `--stdin` | read JSON queue configuration from stdin |  |

Examples:

```sh
gregale projects environments queues set shop staging shop-worker --file queues.json
```

#### projects environments gitops

Review Git definitions, adopt owned fields, and inspect reconciliation (JSON output)

`gregale projects environments gitops <status|bind|rebind|unbind|review|approve|adoption-preview|adopt|controls|override|remove-override>`

##### projects environments gitops status

Inspect the source and recent reconciliation attempts

`gregale projects environments gitops status <project> <environment>`

##### projects environments gitops bind

Bind the verified project repository to a definition

`gregale projects environments gitops bind [--manifest-path <PATH>] [--ref <REF>] [--mode <MODE>] [--prune] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--manifest-path <PATH>` | Environment definition path in Git |  |
| `--ref <REF>` | Git ref selecting revision candidates |  |
| `--mode <MODE>` | report (default; enforcement unavailable in preview) |  |
| `--prune` | Allow removal of previously owned fields |  |

##### projects environments gitops rebind

Release ownership and replace the source binding

`gregale projects environments gitops rebind --expected-generation <N> --manifest-path <PATH> [--ref <REF>] [--approval-policy <POLICY>] [--yes] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--expected-generation <N>` | Reviewed current source generation | required |
| `--manifest-path <PATH>` | Replacement definition path | required |
| `--ref <REF>` | Replacement Git ref |  |
| `--approval-policy <POLICY>` | manual or protected_branch |  |
| `--yes` | Confirm ownership release while preserving values |  |

##### projects environments gitops unbind

Disconnect the source and release its ownership

`gregale projects environments gitops unbind --expected-generation <N> [--yes] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--expected-generation <N>` | Reviewed current source generation | required |
| `--yes` | Confirm ownership release while preserving values |  |

##### projects environments gitops review

Fetch an immutable commit and output a review receipt

`gregale projects environments gitops review [--commit <SHA>] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--commit <SHA>` | Exact lowercase GitHub commit SHA |  |

##### projects environments gitops approve

Approve the digest and generation in a reviewed receipt

`gregale projects environments gitops approve [--file <PATH>] [--yes] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | Saved revision review JSON |  |
| `--yes` | Confirm approval of the reviewed bytes |  |

##### projects environments gitops adoption-preview

Inspect ownership transfer without changing values

`gregale projects environments gitops adoption-preview <project> <environment>`

##### projects environments gitops adopt

Transfer ownership from a reviewed adoption plan

`gregale projects environments gitops adopt [--file <PATH>] [--yes] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | Saved adoption plan JSON |  |
| `--yes` | Confirm the reviewed ownership transfer |  |

##### projects environments gitops controls

Update fenced reporting, pruning, or suspension controls

`gregale projects environments gitops controls [--generation <N>] [--mode <MODE>] [--prune] [--suspended] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--generation <N>` | Current source generation |  |
| `--mode <MODE>` | report (enforcement unavailable in preview) |  |
| `--prune` | Set pruning (accepts =false) |  |
| `--suspended` | Set suspension (accepts =false) |  |

##### projects environments gitops override

Permit an expiring edit to an owned field

`gregale projects environments gitops override [--resource <RESOURCE>] [--path <PATH>] [--reason <REASON>] [--expires <RFC3339>] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--resource <RESOURCE>` | Logical resource |  |
| `--path <PATH>` | Owned field path |  |
| `--reason <REASON>` | Reason for the temporary edit |  |
| `--expires <RFC3339>` | Expiry within twenty-four hours |  |

##### projects environments gitops remove-override

Revoke a field override

`gregale projects environments gitops remove-override [--resource <RESOURCE>] [--path <PATH>] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--resource <RESOURCE>` | Logical resource |  |
| `--path <PATH>` | Owned field path |  |

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

`gregale projects environments promote --from <ENV> --to <ENV> [--sync-config] [--yes] [--idempotency-key <KEY>] [--wait] [--progress] [--timeout <SECONDS|DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--from <ENV>` | source environment | required |
| `--to <ENV>` | target environment | required |
| `--sync-config` | copy source non-secret environment configuration to the target |  |
| `--yes` | confirm the promotion |  |
| `--idempotency-key <KEY>` | stable key for retrying this promotion |  |
| `--wait` | wait for the promotion to reach a terminal status |  |
| `--progress` | print promotion transitions while waiting (human output only) |  |
| `--timeout <SECONDS|DURATION>` | maximum wait for promotion completion (seconds, or a duration such as 10m) |  |

#### projects environments status

Inspect a promotion

`gregale projects environments status`

#### projects environments rollback

Roll back a promotion

`gregale projects environments rollback`

### projects update

Update repository or production branch

`gregale projects update [--repo <OWNER/NAME>] [--branch <BRANCH>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--repo <OWNER/NAME>` | GitHub repository owner/name; empty unbinds |  |
| `--branch <BRANCH>` | production branch |  |

### projects rm

Preview or delete a project

`gregale projects rm [--dry-run] [--yes] <slug>`

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

Manage sealed secrets and environment secret references

`gregale secrets [<subcommand>]`

### secrets refs

Manage destination-to-source names in a registered environment

#### secrets refs list

List reference names and shared environment-key quota

`gregale secrets refs list --app <slug> --environment <ENV>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--environment <ENV>` | registered project environment | required |

Examples:

```sh
gregale secrets refs list --app my-api --environment production
```

#### secrets refs set

Select an existing scoped secret; respects Git field ownership

`gregale secrets refs set --app <slug> --environment <ENV> <KEY=secret:NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--environment <ENV>` | registered project environment | required |

Examples:

```sh
gregale secrets refs set --app my-api --environment production DATABASE_URL=secret:DATABASE_PRIMARY
```

#### secrets refs unset

Suppress a primary workload secret destination and preserve the sealed source

`gregale secrets refs unset --app <slug> --environment <ENV> <KEY>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--environment <ENV>` | registered project environment | required |

Examples:

```sh
gregale secrets refs unset --app my-api --environment production DATABASE_URL
```

### secrets list

List sealed secrets

`gregale secrets list --app <slug> [--scope <SCOPE|__all__>] [--class <CLASS>] [--older-than <DURATION>]`

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

`gregale secrets set --app <slug> [--from-stdin] [--scope <SCOPE>] [--class <CLASS>] [--restart] [<KEY=VALUE>...]`

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

Remove a sealed secret (alias: rm)

`gregale secrets unset --app <slug> [--scope <SCOPE>] [--restart] [--wait-for-ack] [--timeout <DURATION>] <KEY>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--scope <SCOPE>` | env scope to delete from (defaults to linked project environment) |  |
| `--restart` | restart the app so running instances drop the removed secret now |  |
| `--wait-for-ack` | wait until every active authorized runtime confirms it removed the secret |  |
| `--timeout <DURATION>` | maximum time to wait for runtime acknowledgements |  |

Examples:

```sh
gregale secrets unset --app my-api OLD_API_KEY
gregale secrets unset --app my-api OLD_API_KEY --scope staging
gregale secrets unset --app my-api OLD_API_KEY --wait-for-ack
gregale secrets unset --app my-api OLD_API_KEY --restart
```

### secrets list-all

List every secret across apps

`gregale secrets list-all [--before <slug|key>] [--limit <N>] [--class <CLASS>] [--older-than <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--before <slug|key>` | pagination cursor from a previous call&#39;s next_before |  |
| `--limit <N>` | page size (1..100; server caps at 100) |  |
| `--class <CLASS>` | filter this page by snapshot-retention class | one of `persistent` · `ephemeral` |
| `--older-than <DURATION>` | filter this page to secrets not updated within a duration; unknown timestamps are excluded |  |

Examples:

```sh
gregale secrets list-all --class ephemeral
gregale secrets list-all --older-than 90d
```

### secrets audit

Audit secret update age and report unknown timestamps without exposing values

`gregale secrets audit --older-than <DURATION> [--fail-on-stale]`

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

`gregale secrets rotate --app <slug> [--from-stdin] [--scope <SCOPE>] [--restart] [--wait-for-ack] [--timeout <DURATION>] [<KEY=VALUE>]`

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

Platform status: API availability, wake p95 and deployment success (not account-specific)

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

`gregale trusted-publishers add <slug> <name> <pub.pem>`

### trusted-publishers remove

Remove a trusted publisher

`gregale trusted-publishers remove <slug> <name>`

### trusted-publishers list

List trusted publishers

`gregale trusted-publishers list <slug>`


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

### usage object-storage

Account object storage observations, safety policy and billing state

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

`gregale config get <api-base|json>`

### config set

Persist one non-secret setting

`gregale config set <api-base|json> <value>`

### config list

Show all effective settings


## wake-timeline

Walk the per-wake event stream (wake-timeline [&lt;slug&gt;] &lt;wake-id&gt; [--app SLUG] [--since RFC3339] [--limit N] [--all] [--verbose]; slug defaults to linked context)

`gregale wake-timeline [<slug>] <wake-id> [--app <SLUG>] [--since <RFC3339>] [--limit <N>] [--all] [--verbose]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (alternative to the leading slug positional) |  |
| `--since <RFC3339>` | RFC3339 timestamp |  |
| `--limit <N>` | page size (1..1000) |  |
| `--all` | walk every page |  |
| `--verbose` | show detailed restore phases in human output |  |


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

`gregale wake <slug> [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the requested wake to reach running |  |
| `--timeout <DURATION>` | maximum time to wait for the requested wake (default 1m) |  |
| `--poll-interval <DURATION>` | interval between instance status checks (default 250ms) |  |

Examples:

```sh
gregale wake my-api
gregale wake --wait --timeout 2m my-api
```


## traffic

Manage deployment traffic split (available on every plan)

`gregale traffic [<subcommand>]`

### traffic set

Set the traffic split for a deployment

`gregale traffic set [--app <SLUG>] --deployment <ID> --percent <N> [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--deployment <ID>` | deployment id or vN revision to set the traffic split on | required |
| `--percent <N>` | traffic weight in [0, 100]; -1 = unset (server default 100) | required |

### traffic promote

Promote a live deployment to 100% production traffic

`gregale traffic promote [--app <SLUG>] --deployment <ID> [--if-serving <ID>] [--require-bindings] [--max-verification-age <DURATION>] [--allow-unsupported] [--require-application-ack] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--deployment <ID>` | deployment id or vN revision to promote | required |
| `--if-serving <ID>` | require this deployment id or vN revision to remain at 100% traffic |  |
| `--require-bindings` | enforce a bindings check at the server&#39;s traffic write |  |
| `--max-verification-age <DURATION>` | maximum probe age (default 10m); requires --require-bindings |  |
| `--allow-unsupported` | waive unsupported queue/outbound probes; requires --require-bindings |  |
| `--require-application-ack` | require current application acknowledgements; requires --require-bindings |  |

### traffic status

Show live deployment traffic weights for an app

`gregale traffic status <slug>`


## log-drains

Ship app runtime logs to an HTTP JSON or OTLP endpoint

`gregale log-drains [<subcommand>]`

### log-drains list

List an app&#39;s log drains

`gregale log-drains list [--app <SLUG>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug |  |

Examples:

```sh
gregale log-drains list --app my-app
```

### log-drains add

Add a log drain; the credential is read from an environment variable

`gregale log-drains add [--app <SLUG>] --url <URL> [--kind <KIND>] [--auth-header-env <ENV>] [--disabled] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug |  |
| `--url <URL>` | destination URL | required |
| `--kind <KIND>` | destination format (default http_json) | one of `http_json` · `otlp` |
| `--auth-header-env <ENV>` | environment variable holding the Authorization header value |  |
| `--disabled` | create the drain disabled |  |

Examples:

```sh
LOG_TOKEN='Bearer …' gregale log-drains add --app my-app --url https://logs.example.com/ingest --auth-header-env LOG_TOKEN
```

### log-drains get

Show one log drain (credential masked)

`gregale log-drains get [--app <SLUG>] --id <ID> [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug |  |
| `--id <ID>` | log drain id | required |

### log-drains health

Show delivery health: queue, delivered, failed and last error

`gregale log-drains health [--app <SLUG>] --id <ID> [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug |  |
| `--id <ID>` | log drain id | required |

Examples:

```sh
gregale log-drains health --app my-app --id <drain-id>
```

### log-drains update

Change a drain&#39;s URL or credential, or pause and resume it

`gregale log-drains update [--app <SLUG>] --id <ID> [--url <URL>] [--auth-header-env <ENV>] [--enable] [--disable] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug |  |
| `--id <ID>` | log drain id | required |
| `--url <URL>` | new destination URL |  |
| `--auth-header-env <ENV>` | environment variable holding the new Authorization header value |  |
| `--enable` | resume delivery |  |
| `--disable` | pause delivery |  |

### log-drains rm

Delete a log drain

`gregale log-drains rm [--app <SLUG>] --id <ID> [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug |  |
| `--id <ID>` | log drain id | required |


## mirror

Manage traffic mirroring and sanitized replay (Pro/Scale only). Rules default to 5% and mirror only safe methods; bodies over 64 KiB are skipped, and raw bodies are never retained.

`gregale mirror [<subcommand>]`

### mirror list

List mirror rules

`gregale mirror list --app <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### mirror create

Create a mirror rule

`gregale mirror create --app <slug> --source <ID> --mirror <ID> [--percent <N>] [--include-body] [--allow-unsafe-methods] [--redact-header <NAME>]`

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

`gregale mirror info --app <slug> --id <ID>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--id <ID>` | mirror rule id | required |

### mirror update

Patch a mirror rule (patch semantics)

`gregale mirror update --app <slug> --id <ID> [--percent <N>] [--enable] [--disable] [--include-body] [--no-include-body] [--allow-unsafe-methods] [--safe-methods-only] [--redact-header <NAME>] [--clear-redact]`

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

`gregale mirror rm --app <slug> --id <ID>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--id <ID>` | mirror rule id | required |

### mirror summary

Aggregate mirror drift counts over a window

`gregale mirror summary --app <slug> --id <ID> [--window <WINDOW>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--id <ID>` | mirror rule id | required |
| `--window <WINDOW>` | summary window: 1h \| 24h \| 7d (default 1h) | one of `1h` · `24h` · `7d` |

### mirror replay

Replay a sanitized historical request corpus

`gregale mirror replay --app <slug> --id <ID> --file <PATH> [--allow-unsafe-methods]`

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

`gregale cache GET [--app <SLUG>] [--host <HOST>] [--stale-while-revalidate <DURATION>] [--stale-if-error <DURATION>] [--vary-on <HEADER>] [--priority <N>] <path> for <duration>`

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

`gregale cache HEAD [--app <SLUG>] [--host <HOST>] [--stale-while-revalidate <DURATION>] [--stale-if-error <DURATION>] [--vary-on <HEADER>] [--priority <N>] <path> for <duration>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (defaults to linked project context) |  |
| `--host <HOST>` | hostname override |  |
| `--stale-while-revalidate <DURATION>` | serve stale while refreshing |  |
| `--stale-if-error <DURATION>` | serve stale when the origin fails |  |
| `--vary-on <HEADER>` | header included in the cache key | one of `Accept-Language` · `Accept-Encoding` |
| `--priority <N>` | match priority (lower wins) |  |

### cache purge

Purge cached responses for one app

`gregale cache purge [--path <GLOB>] [--tag <TAG>] <slug>`

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

`gregale upload-cache cleanup [--older-than <D>] [--max-entries <N>] [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--older-than <D>` | maximum recovery-state age |  |
| `--max-entries <N>` | maximum recovery records to retain |  |
| `--dry-run` | show actions without deleting files |  |


## webhooks

Manage app and account release webhooks (webhooks account &lt;verb&gt;)

`gregale webhooks [<subcommand>]`

### webhooks list

List an app&#39;s webhooks (slug defaults to linked context)

`gregale webhooks list [--app <slug>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (alternative to the positional) |  |

### webhooks add

Add a webhook

`gregale webhooks add --app <slug> --target-url <URL> [--event <EVENT>] [--retry-policy <POLICY>] [--delivery-format <FORMAT>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--target-url <URL>` | HTTPS target URL | required |
| `--event <EVENT>` | event to deliver (repeat) |  |
| `--retry-policy <POLICY>` | default\|aggressive\|none |  |
| `--delivery-format <FORMAT>` | json\|cloudevents |  |

### webhooks info

Show one webhook

`gregale webhooks info <webhook-id>`

### webhooks update

Update one webhook

`gregale webhooks update <id>`

### webhooks rm

Delete one webhook

`gregale webhooks rm <id>`

### webhooks deliveries

Show the delivery ledger

`gregale webhooks deliveries --app <SLUG> [--status <STATUS>] [--limit <N>] [--page-size <N>] [--cursor <CURSOR>] [--page-token <CURSOR>] [--all] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--status <STATUS>` | filter delivery status | one of `pending` · `in_flight` · `succeeded` · `failed` · `dead` |
| `--limit <N>` | page size (1..100, default 50) |  |
| `--page-size <N>` | alias for --limit |  |
| `--cursor <CURSOR>` | opaque continuation cursor |  |
| `--page-token <CURSOR>` | alias for --cursor |  |
| `--all` | walk every page using --limit and --cursor |  |

### webhooks retry

Retry a failed delivery

`gregale webhooks retry <webhook-id> <delivery-id>`

### webhooks rotate-secret

Rotate the webhook signing secret

`gregale webhooks rotate-secret --app <slug> [--secret <VALUE>] [--from-stdin] <webhook-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--secret <VALUE>` | replacement HMAC-SHA256 secret |  |
| `--from-stdin` | read the replacement secret from stdin |  |

### webhooks account

Manage one release receiver across all account apps

#### webhooks account list

List account release receivers

`gregale webhooks account list`

#### webhooks account add

Create a receiver; omitted signing secret is generated and shown once

`gregale webhooks account add --target-url <URL> --event <EVENT>... [--secret <VALUE>] [--from-stdin] [--retry-policy <POLICY>] [--delivery-format <FORMAT>]`

| Flag | Meaning | |
|---|---|---|
| `--target-url <URL>` | HTTPS receiver URL | required |
| `--event <EVENT>` | distinct release event (repeatable) | required; one of `deployment.live` · `deployment.failed` · `rollout.completed` · `rollout.aborted` |
| `--secret <VALUE>` | signing secret (prefer --from-stdin) |  |
| `--from-stdin` | read the signing secret from stdin; mutually exclusive with --secret |  |
| `--retry-policy <POLICY>` | delivery retry policy | one of `default` · `aggressive` · `none` |
| `--delivery-format <FORMAT>` | delivery envelope | one of `json` · `cloudevents` |

#### webhooks account info

Show one receiver with a masked signing secret

`gregale webhooks account info <id>`

#### webhooks account update

Update a receiver

`gregale webhooks account update [--target-url <URL>] [--event <EVENT>]... [--enable] [--disable] [--secret <VALUE>] [--from-stdin] [--retry-policy <POLICY>] [--delivery-format <FORMAT>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--target-url <URL>` | replacement HTTPS receiver URL |  |
| `--event <EVENT>` | replacement release event filter (repeatable) | one of `deployment.live` · `deployment.failed` · `rollout.completed` · `rollout.aborted` |
| `--enable` | enable the receiver; mutually exclusive with --disable |  |
| `--disable` | disable the receiver; mutually exclusive with --enable |  |
| `--secret <VALUE>` | signing secret (prefer --from-stdin) |  |
| `--from-stdin` | read the signing secret from stdin; mutually exclusive with --secret |  |
| `--retry-policy <POLICY>` | delivery retry policy | one of `default` · `aggressive` · `none` |
| `--delivery-format <FORMAT>` | delivery envelope | one of `json` · `cloudevents` |

#### webhooks account rm

Delete a receiver

`gregale webhooks account rm <id>`

#### webhooks account deliveries

Page through receiver deliveries

`gregale webhooks account deliveries [--page-size <N>] [--page-token <CURSOR>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--page-size <N>` | delivery page size (1..100, default 50) |  |
| `--page-token <CURSOR>` | opaque delivery cursor |  |

#### webhooks account retry

Retry one failed delivery

`gregale webhooks account retry <id> <delivery-id>`

#### webhooks account rotate-secret

Replace the signing secret using --from-stdin or --secret

`gregale webhooks account rotate-secret [--secret <VALUE>] [--from-stdin] <id>`

| Flag | Meaning | |
|---|---|---|
| `--secret <VALUE>` | signing secret (prefer --from-stdin) |  |
| `--from-stdin` | read the signing secret from stdin; mutually exclusive with --secret |  |


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
