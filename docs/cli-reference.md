# gregale CLI reference

Generated from the CLI's command manifest by `gregale man --markdown`. Do not edit by hand.

Local help: `gregale deploy --help` and `gregale app --help` show common tasks. Add `--all` to see every option grouped by purpose, or use `gregale help deploy --all`. This reference always lists every option.

App target: a command that takes `--app` and no other arguments also accepts the app as its first argument (`gregale alerts list my-api` is `gregale alerts list --app my-api`), and slug-first commands such as `logs` and `ps` accept `--app`.

Automation: put `--non-interactive` before the command to disable prompts and browser launches; use `--json` for structured output. Required confirmations must be supplied explicitly. Connection selection: `gregale --profile <name> <command>`. Put this option before the command; command-local `--profile` options retain their documented meaning. See [CLI configuration](cli-config.md) for connection profiles and environment precedence.

| Command | What it does |
|---|---|
| [`mcp`](#mcp) | Connect agents to Gregale, or scaffold and deploy MCP servers |
| [`menu`](#menu) | Choose a common task and open its guided command |
| [`start`](#start) | Get your first app live with a few guided prompts |
| [`account`](#account) | Export or delete your account, and view its status, DPA and SLO |
| [`add`](#add) | Provision and bind managed resources to an app |
| [`bucket`](#bucket) | Manage object storage buckets: encryption, versioning and lifecycle |
| [`bindings`](#bindings) | Inspect an app&#39;s resource bindings and credential rotation |
| [`capabilities`](#capabilities) | Show feature maturity and plan availability |
| [`ai`](#ai) | Configure the AI gateway: providers, cost caps, guardrails and usage |
| [`channels`](#channels) | Manage where alerts are sent: Slack, PagerDuty or email |
| [`dashboards`](#dashboards) | Save dashboards that combine metrics, log counts and SLOs across apps |
| [`incidents`](#incidents) | List open and recent incidents: alerts, route monitors, status page |
| [`slos`](#slos) | Define SLOs and track their error budgets |
| [`synthetics`](#synthetics) | Schedule HTTP checks against an app |
| [`alerts`](#alerts) | Manage an app&#39;s alert rules |
| [`audit-events`](#audit-events) | Search your account&#39;s audit log |
| [`events`](#events) | Publish events, preview routing, inspect deliveries and backfill |
| [`send`](#send) | Reliably send work to another Gregale application |
| [`deliver`](#deliver) | Reliably deliver an event to a registered webhook |
| [`apps`](#apps) | List your apps |
| [`app`](#app) | Inspect an app, change settings, or manage its runtime |
| [`billing`](#billing) | Manage your subscription, payment method, budgets and cost reports |
| [`approvals`](#approvals) | Make production releases wait in their canary for approval |
| [`scheduled-releases`](#scheduled-releases) | Build production releases now and start them at a set time |
| [`freeze`](#freeze) | Pause production releases for a window, weekly, or after rollbacks |
| [`release-gates`](#release-gates) | Stop rollouts on SLO, error, metric or AI regressions and roll back |
| [`canary`](#canary) | Advance canary stages, check their gates, or simulate a preset |
| [`build`](#build) | Inspect builds, their provenance and SBOMs |
| [`connect`](#connect) | Connect your GitHub account or start the repository wizard |
| [`github`](#github) | Bind an app to a GitHub repository and check its connection |
| [`cors`](#cors) | Configure CORS for an app |
| [`crons`](#crons) | Manage scheduled HTTP requests and deployment commands |
| [`triggers`](#triggers) | Manage event triggers from message brokers and crons |
| [`workers`](#workers) | Inspect and manage background worker pools |
| [`jobs`](#jobs) | Manage jobs that run to completion |
| [`automations`](#automations) | Build, monitor and control customer-built automations |
| [`workflows`](#workflows) | Manage durable execution workflows |
| [`dashboard`](#dashboard) | Open the account dashboard in your browser |
| [`doctor`](#doctor) | Check local source or an image for problems before deploying |
| [`delayed-task`](#delayed-task) | Schedule and inspect deferred invocations |
| [`deployments`](#deployments) | List an app&#39;s deployments and manage named revision URLs |
| [`deployment`](#deployment) | Inspect one release, wait for its rollout, or advance a canary |
| [`deploys`](#deploys) | Follow one deployment&#39;s stages and logs, or cancel or retry it |
| [`deploy`](#deploy) | Deploy an app, function, or project |
| [`domains`](#domains) | Manage custom domains |
| [`dev`](#dev) | Sync local changes to a developer environment |
| [`diff`](#diff) | Compare two named environments in the linked project |
| [`test`](#test) | Run scenario suites, lifecycle checks and local load tests |
| [`chaos`](#chaos) | Inject faults into isolated scenario tests |
| [`preview`](#preview) | Manage preview environments for pull requests |
| [`previews`](#previews) | Join or leave early platform features |
| [`flags`](#flags) | Roll out feature flags to selected customers |
| [`consumers`](#consumers) | Issue keys to your API&#39;s consumers and meter their requests |
| [`platform-tenants`](#platform-tenants) | Manage one customer across app consumers and tenant hostnames |
| [`edge-rule-lists`](#edge-rule-lists) | Manage reusable IP, country, host and text lists for edge rules |
| [`edge-rules`](#edge-rules) | Route, rewrite, protect or rate-limit an app&#39;s requests at the edge |
| [`openapi`](#openapi) | Manage an app&#39;s OpenAPI docs and check for breaking changes |
| [`routes`](#routes) | Analyze route changes, migrations, lifecycle and production policies |
| [`env`](#env) | Clone environments, sync .env files and roll back config changes |
| [`init`](#init) | Scaffold a project from a built-in template |
| [`inspect`](#inspect) | Explain an app&#39;s health from runtime, release, data and scaling |
| [`invoke`](#invoke) | Call an app or function once to smoke-test it |
| [`run`](#run) | Run code, or one command from an image, in a disposable sandbox |
| [`runs`](#runs) | List, inspect or cancel sandbox runs |
| [`invocations`](#invocations) | List, inspect and wait on invocations across your account |
| [`issues`](#issues) | Group failures into issues and track ownership and resolution |
| [`customer-operations`](#customer-operations) | Inspect customer work, verify downloads and reconcile outcomes |
| [`operations`](#operations) | Coordinate named work so one worker owns it at a time |
| [`debug`](#debug) | Inspect production requests and regressions |
| [`trace`](#trace) | Look up a distributed trace by its trace ID |
| [`invitations`](#invitations) | View or accept an organization invitation |
| [`invoices`](#invoices) | List issued invoices |
| [`keys`](#keys) | Manage your account&#39;s API keys |
| [`login`](#login) | Authenticate this machine |
| [`link`](#link) | Link this checkout to a Gregale project |
| [`logout`](#logout) | Revoke the managed CLI session and remove the stored token |
| [`unlink`](#unlink) | Remove the linked project from this checkout |
| [`profile`](#profile) | Manage named connections to Gregale and their credentials |
| [`context`](#context) | Show linked targets, configuration sources, and missing defaults |
| [`signup`](#signup) | Create a new account |
| [`logs`](#logs) | Search an app&#39;s runtime logs and HTTP requests, or follow them |
| [`metrics`](#metrics) | Show app or account metrics, or the service map |
| [`analytics`](#analytics) | Break down past requests by route, country, referrer or status |
| [`mfa`](#mfa) | Set up and manage multi-factor authentication |
| [`open`](#open) | Open an app&#39;s URL in your browser |
| [`orgs`](#orgs) | Manage orgs, members, and workspace activity |
| [`overage-cap`](#overage-cap) | Set or clear a spending cap for usage beyond your plan |
| [`park`](#park) | Stop all of an app&#39;s instances until its next request |
| [`plan`](#plan) | Change your subscription plan |
| [`pipeline`](#pipeline) | Sync PostgreSQL sources into read-only mirrors |
| [`data-api`](#data-api) | Generate an API from a PostgreSQL schema and export its types |
| [`ps`](#ps) | Show an app&#39;s live instances and their state |
| [`queue`](#queue) | Inspect queues and manage queue bindings |
| [`dlq`](#dlq) | Inspect, replay or purge events that failed delivery |
| [`registry`](#registry) | Manage private registry credentials and deploy published images |
| [`realtime`](#realtime) | Manage realtime endpoints, policies, connections, channels, and auth |
| [`rollback`](#rollback) | Restore a previous deployment, or check an exact historical rollback |
| [`projects`](#projects) | Inspect and recover repository projects |
| [`scan`](#scan) | Show how a source tree splits into deployable workloads |
| [`secrets`](#secrets) | Set, rotate and audit encrypted secrets for apps and environments |
| [`slo`](#slo) | Show an app&#39;s SLO panel for a time window |
| [`status`](#status) | Show Gregale platform status (not specific to your account) |
| [`tail`](#tail) | Watch invocations finish across your account in real time |
| [`trusted-publishers`](#trusted-publishers) | Manage public keys trusted to sign an app&#39;s images |
| [`usage`](#usage) | Show this month&#39;s usage, or daily, storage and summary views |
| [`version`](#version) | Print the CLI version |
| [`config`](#config) | Show or change non-secret local CLI settings |
| [`wake-timeline`](#wake-timeline) | Show the step-by-step timeline of one app wake |
| [`throttle-suggestions`](#throttle-suggestions) | Suggest per-route rate limits and preview their effect |
| [`wake`](#wake) | Wake a parked app before traffic arrives |
| [`wake-ahead`](#wake-ahead) | Wake the services an app calls while the app itself wakes |
| [`traffic`](#traffic) | Split traffic between an app&#39;s deployments |
| [`log-drains`](#log-drains) | Ship app runtime logs to an HTTP JSON or OTLP endpoint |
| [`mirror`](#mirror) | Mirror live traffic to a deployment and replay sanitized requests |
| [`cache`](#cache) | Cache an app&#39;s responses at the edge, or purge them |
| [`upload-cache`](#upload-cache) | Inspect or clear saved state for resumable source uploads |
| [`webhooks`](#webhooks) | Manage release webhooks for apps and your account |
| [`whoami`](#whoami) | Show the authenticated account |
| [`completion`](#completion) | Generate shell completion scripts or install them interactively |
| [`man`](#man) | Print the CLI manual page, or one command&#39;s page |

## mcp

Connect agents to Gregale, or scaffold and deploy MCP servers

`gregale mcp [<subcommand>]`

### mcp serve

Expose Gregale management tools over stdio or OAuth HTTP

`gregale mcp serve [--transport <TRANSPORT>] [--timeout <DURATION>] [--allow-writes] [--tools <TOOLSETS>] [--listen <ADDR>] [--resource-url <URL>] [--oauth-onboarding <MODE>] [--state-file <PATH>] [--admin-socket <PATH>] [--metrics-listen <ADDR>] [--state-key-file <PATH>] [--oauth-redirect-uri <URI>]`

| Flag | Meaning | |
|---|---|---|
| `--transport <TRANSPORT>` | management MCP transport (default stdio) | one of `stdio` · `streamable-http` |
| `--timeout <DURATION>` | per-tool API timeout (default 30s, maximum 1m) |  |
| `--allow-writes` | enable reviewed deploy, promotion, and rollback actions |  |
| `--tools <TOOLSETS>` | toolsets to list, comma-separated: core, release, diagnose, provision (default all) |  |
| `--listen <ADDR>` | HTTP listen address behind TLS proxy (default 127.0.0.1:8787) |  |
| `--resource-url <URL>` | public HTTPS URL ending in /mcp (required for HTTP) |  |
| `--oauth-onboarding <MODE>` | remote onboarding (default browser) | one of `browser` · `api-key` |
| `--state-file <PATH>` | private encrypted remote state file |  |
| `--admin-socket <PATH>` | optional owner-protected Unix socket for listing and revoking HTTP grants |  |
| `--metrics-listen <ADDR>` | optional literal loopback IP:port for private management MCP Prometheus metrics |  |
| `--state-key-file <PATH>` | independent private 32-byte state encryption key file |  |
| `--oauth-redirect-uri <URI>` | allowed OAuth callback; repeat; omit loopback HTTP port for ephemeral ports |  |

Examples:

```sh
gregale mcp serve --transport stdio
gregale --profile staging mcp serve --timeout 15s
```

### mcp connections

Inspect or revoke agent grants through a local operator socket

#### mcp connections list

List bounded connection metadata without credentials

`gregale mcp connections list --socket <PATH> [--limit <N>] [--offset <N>]`

| Flag | Meaning | |
|---|---|---|
| `--socket <PATH>` | owner-protected management control socket | required |
| `--limit <N>` | page size from 1 to 100 (default 20) |  |
| `--offset <N>` | non-negative page offset |  |

#### mcp connections revoke

Revoke one exact bridge grant and stop its observations

`gregale mcp connections revoke --socket <PATH> --grant-id <ID>`

| Flag | Meaning | |
|---|---|---|
| `--socket <PATH>` | owner-protected management control socket | required |
| `--grant-id <ID>` | exact active bridge grant ID to revoke | required |

### mcp readiness

Check deployment public MCP readiness without executing tools

`gregale mcp readiness <pagerduty|reminder-defaults|fallback|attention|notifications|maintenance|incidents|policy> --app <SLUG> --deployment <UUID> [--endpoint <PATH>] [--token-env <ENV>] [--baseline <PATH>] [--record] [--role <NAME>] [--legacy] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--deployment <UUID>` | deployment UUID | required |
| `--endpoint <PATH>` | MCP path (default /mcp) |  |
| `--token-env <ENV>` | dedicated scoped probe token variable |  |
| `--baseline <PATH>` | expected role-visible MCP catalog lock |  |
| `--record` | run and retain a server-side policy-bound probe |  |
| `--role <NAME>` | configured probe role with --record (default default) |  |
| `--legacy` | use the legacy protocol |  |
| `--timeout <DURATION>` | overall deadline up to 30s |  |

Examples:

```sh
gregale mcp readiness --app my-mcp --deployment <uuid> --token-env MCP_PROBE_TOKEN --baseline gregale-mcp.lock.json --json
```

#### mcp readiness pagerduty

Configure native PagerDuty readiness incidents

`gregale mcp readiness pagerduty <acknowledgements|get|set|disable>`

##### mcp readiness pagerduty acknowledgements

Sync signed PagerDuty acknowledgements into readiness reminders

`gregale mcp readiness pagerduty acknowledgements <get|set|disable|events>`

###### mcp readiness pagerduty acknowledgements get

Read masked webhook configuration

`gregale mcp readiness pagerduty acknowledgements get --app <SLUG> --rule <UUID> [--destination <primary|fallback>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--destination <primary|fallback>` | native PagerDuty receiver |  |

###### mcp readiness pagerduty acknowledgements set

Bind a service and webhook subscription signing secret

`gregale mcp readiness pagerduty acknowledgements set --app <SLUG> --rule <UUID> --expected-version <N> [--destination <primary|fallback>] [--expected-pagerduty-version <N>] [--expected-fallback-version <N>] --service-id <ID> --secret-env <ENV> [--sync-unacknowledgements]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--expected-version <N>` | current reminder defaults version | required |
| `--destination <primary|fallback>` | native PagerDuty receiver; default primary |  |
| `--expected-pagerduty-version <N>` | current native primary version; required for primary |  |
| `--expected-fallback-version <N>` | current fallback version; required for fallback |  |
| `--service-id <ID>` | PagerDuty service ID | required |
| `--secret-env <ENV>` | environment variable containing the webhook signing secret | required |
| `--sync-unacknowledgements` | resume bounded reminders after matching provider unacknowledgements (opt-in) |  |

###### mcp readiness pagerduty acknowledgements disable

Revoke webhook signing credentials immediately

`gregale mcp readiness pagerduty acknowledgements disable --app <SLUG> --rule <UUID> --expected-version <N> [--destination <primary|fallback>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--expected-version <N>` | current reminder defaults version | required |
| `--destination <primary|fallback>` | native PagerDuty receiver; omission selects the original callback binding |  |

###### mcp readiness pagerduty acknowledgements events

Page through durable provider acknowledgement receipts

`gregale mcp readiness pagerduty acknowledgements events --app <SLUG> --rule <UUID> [--before <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--before <CURSOR>` | opaque event cursor |  |
| `--limit <N>` | page size 1–50 |  |

##### mcp readiness pagerduty get

Read masked PagerDuty settings

`gregale mcp readiness pagerduty get --app <SLUG> --rule <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |

##### mcp readiness pagerduty set

Use PagerDuty as primary for unattempted incidents

`gregale mcp readiness pagerduty set --app <SLUG> --rule <UUID> --expected-version <N> --key-env <ENV> [--region <us|eu>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--expected-version <N>` | current reminder defaults version | required |
| `--key-env <ENV>` | environment variable containing the integration key | required |
| `--region <us|eu>` | PagerDuty Events API region (default us) |  |

##### mcp readiness pagerduty disable

Restore webhook primary for unattempted incidents

`gregale mcp readiness pagerduty disable --app <SLUG> --rule <UUID> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--expected-version <N>` | current reminder defaults version | required |

#### mcp readiness reminder-defaults

Configure reminder defaults for future incidents

`gregale mcp readiness reminder-defaults <get|set|disable>`

##### mcp readiness reminder-defaults get

Read the rule reminder defaults

`gregale mcp readiness reminder-defaults get --app <SLUG> --rule <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |

##### mcp readiness reminder-defaults set

Set reminders inherited by new incidents

`gregale mcp readiness reminder-defaults set --app <SLUG> --rule <UUID> --expected-version <N> --interval-seconds <N> --max-reminders <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--expected-version <N>` | current reminder defaults version | required |
| `--interval-seconds <N>` | reminder interval, 60–86400 seconds | required |
| `--max-reminders <N>` | per-incident reminder budget, 1–10 | required |

##### mcp readiness reminder-defaults disable

Disable reminders for future incidents

`gregale mcp readiness reminder-defaults disable --app <SLUG> --rule <UUID> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--expected-version <N>` | current reminder defaults version | required |

#### mcp readiness fallback

Configure webhook or native PagerDuty readiness fallback

`gregale mcp readiness fallback <get|set|disable>`

##### mcp readiness fallback get

Read the masked fallback policy

`gregale mcp readiness fallback get --app <SLUG> --rule <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule UUID | required |

##### mcp readiness fallback set

Enable fallback for incidents not yet attempted

`gregale mcp readiness fallback set --app <SLUG> --rule <UUID> --expected-version <N> [--provider <webhook|pagerduty>] [--webhook-url <URL>] [--secret-env <VAR>] [--key-env <VAR>] [--region <us|eu>] --after-failures <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule UUID | required |
| `--expected-version <N>` | current fallback policy version | required |
| `--provider <webhook|pagerduty>` | fallback provider; default webhook |  |
| `--webhook-url <URL>` | public HTTP(S) receiver for webhook fallback |  |
| `--secret-env <VAR>` | environment variable holding webhook signing secret |  |
| `--key-env <VAR>` | environment variable holding PagerDuty integration key |  |
| `--region <us|eu>` | PagerDuty region; default us |  |
| `--after-failures <N>` | completed primary failures, 1–10 | required |

##### mcp readiness fallback disable

Disable fallback for new incidents

`gregale mcp readiness fallback disable --app <SLUG> --rule <UUID> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule UUID | required |
| `--expected-version <N>` | current fallback policy version | required |

#### mcp readiness attention

Acknowledge readiness incidents and configure bounded reminders

`gregale mcp readiness attention <list|history|acknowledge|release|configure>`

##### mcp readiness attention list

List open incidents and attention state

`gregale mcp readiness attention list --app <SLUG> [--scope <SCOPE>] [--after <UUID>] [--limit <N>] [--include-resolved]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--scope <SCOPE>` | environment; includes all-environment incidents |  |
| `--after <UUID>` | incident UUID cursor |  |
| `--limit <N>` | page size 1–50 |  |
| `--include-resolved` | include resolved incident attention |  |

##### mcp readiness attention history

List retained acknowledgement and policy decisions

`gregale mcp readiness attention history --app <SLUG> --incident <UUID> [--before <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--incident <UUID>` | incident | required |
| `--before <CURSOR>` | attention history cursor |  |
| `--limit <N>` | page size 1–50 |  |

##### mcp readiness attention acknowledge

Record ownership without changing readiness

`gregale mcp readiness attention acknowledge --app <SLUG> --incident <UUID> --expected-version <N> --request-id <UUID> --reason <TEXT> [--expires-at <TIME>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--incident <UUID>` | incident | required |
| `--expected-version <N>` | current attention version | required |
| `--request-id <UUID>` | stable decision UUID; reuse on retries | required |
| `--reason <TEXT>` | operator reason | required |
| `--expires-at <TIME>` | optional absolute RFC3339 expiry |  |

##### mcp readiness attention release

Release acknowledgement and preserve its history

`gregale mcp readiness attention release --app <SLUG> --incident <UUID> --expected-version <N> --request-id <UUID> --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--incident <UUID>` | incident | required |
| `--expected-version <N>` | current attention version | required |
| `--request-id <UUID>` | stable decision UUID; reuse on retries | required |
| `--reason <TEXT>` | operator reason | required |

##### mcp readiness attention configure

Set a finite reminder interval and lifetime budget

`gregale mcp readiness attention configure --app <SLUG> --incident <UUID> --expected-version <N> --request-id <UUID> --reason <TEXT> --interval <DURATION> --max-reminders <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--incident <UUID>` | incident | required |
| `--expected-version <N>` | current attention version | required |
| `--request-id <UUID>` | stable decision UUID; reuse on retries | required |
| `--reason <TEXT>` | operator reason | required |
| `--interval <DURATION>` | whole-second interval 1m–24h; zero disables | required |
| `--max-reminders <N>` | lifetime limit 1–10; zero disables | required |

#### mcp readiness notifications

Inspect readiness notification queues and recover blocked delivery

`gregale mcp readiness notifications <list|retry|abandon>`

##### mcp readiness notifications list

Show queue heads, age and delivery blockers

`gregale mcp readiness notifications list --app <SLUG> [--scope <SCOPE>] [--after <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--scope <SCOPE>` | environment; includes all-environment rules |  |
| `--after <UUID>` | rule UUID cursor |  |
| `--limit <N>` | page size 1–50 |  |

##### mcp readiness notifications retry

Make a failed head eligible for the next evaluator pass

`gregale mcp readiness notifications retry [--destination <primary|fallback>] --app <SLUG> --event <UUID> --expected-version <N> --request-id <UUID> --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--destination <primary|fallback>` | receiver queue; omit for the whole event |  |
| `--app <SLUG>` | owning app slug | required |
| `--event <UUID>` | head event | required |
| `--expected-version <N>` | current head delivery version | required |
| `--request-id <UUID>` | stable request ID; reuse on retries | required |
| `--reason <TEXT>` | operator reason | required |

##### mcp readiness notifications abandon

Permanently stop a head notification and allow the queue to advance

`gregale mcp readiness notifications abandon [--destination <primary|fallback>] --app <SLUG> --event <UUID> --expected-version <N> --request-id <UUID> --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--destination <primary|fallback>` | receiver queue; omit for the whole event |  |
| `--app <SLUG>` | owning app slug | required |
| `--event <UUID>` | head event | required |
| `--expected-version <N>` | current head delivery version | required |
| `--request-id <UUID>` | stable request ID; reuse on retries | required |
| `--reason <TEXT>` | operator reason | required |

#### mcp readiness maintenance

Schedule maintenance and temporary readiness notification silences

`gregale mcp readiness maintenance <list|create|cancel>`

##### mcp readiness maintenance list

List maintenance windows including expired and cancelled history

`gregale mcp readiness maintenance list --app <SLUG> [--scope <SCOPE>] [--before <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--scope <SCOPE>` | environment (default default) |  |
| `--before <CURSOR>` | history cursor |  |
| `--limit <N>` | page size 1–50 |  |

##### mcp readiness maintenance create

Mute notifications while recording incidents

`gregale mcp readiness maintenance create --app <SLUG> [--scope <SCOPE>] [--start <TIME>] [--end <TIME>] [--duration <DURATION>] --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--scope <SCOPE>` | exact environment (default default) |  |
| `--start <TIME>` | RFC3339 start (default now) |  |
| `--end <TIME>` | RFC3339 end; mutually exclusive with duration |  |
| `--duration <DURATION>` | duration from start up to 720h |  |
| `--reason <TEXT>` | maintenance reason | required |

##### mcp readiness maintenance cancel

End a window and retain cancellation attribution

`gregale mcp readiness maintenance cancel --app <SLUG> --id <UUID> --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--id <UUID>` | maintenance window | required |
| `--reason <TEXT>` | cancellation reason | required |

#### mcp readiness incidents

List readiness events with incident and notification status

`gregale mcp readiness incidents --app <SLUG> [--scope <SCOPE>] [--before <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--scope <SCOPE>` | environment (default default; empty selects all) |  |
| `--before <CURSOR>` | timeline cursor |  |
| `--limit <N>` | page size 1–50 |  |

#### mcp readiness policy

Manage environment readiness policies

`gregale mcp readiness policy <history|diff|restore|get|set>`

##### mcp readiness policy history

List environment policy revisions

`gregale mcp readiness policy history --app <SLUG> [--scope <SCOPE>] [--before-revision <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--scope <SCOPE>` | environment (default default) |  |
| `--before-revision <UUID>` | history page cursor |  |
| `--limit <N>` | page size 1–50 (default 20) |  |

##### mcp readiness policy diff

Compare historical policy configuration

`gregale mcp readiness policy diff --app <SLUG> [--scope <SCOPE>] --from-revision <UUID> [--to-revision <UUID>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--scope <SCOPE>` | environment (default default) |  |
| `--from-revision <UUID>` | historical revision | required |
| `--to-revision <UUID>` | comparison revision (default current) |  |

##### mcp readiness policy restore

Restore as a new revision requiring fresh evidence

`gregale mcp readiness policy restore --app <SLUG> [--scope <SCOPE>] --revision <UUID> --expected-revision <UUID> --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--scope <SCOPE>` | environment (default default) |  |
| `--revision <UUID>` | historical revision | required |
| `--expected-revision <UUID>` | current policy revision CAS | required |
| `--reason <TEXT>` | audit reason | required |

##### mcp readiness policy get

Read an environment policy

`gregale mcp readiness policy get --app <SLUG> [--scope <SCOPE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--scope <SCOPE>` | environment (default default) |  |

##### mcp readiness policy set

Save a reviewed environment policy with revision CAS

`gregale mcp readiness policy set --app <SLUG> [--scope <SCOPE>] --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--scope <SCOPE>` | environment (default default) |  |
| `--file <PATH>` | reviewed policy JSON | required |

### mcp init

Create an MCP starter in Node, Go or Python

`gregale mcp init --path <DIR> [--language <LANGUAGE>] [--runtime <RUNTIME>] [--auth <MODE>] [--issuer <URL>] [--jwks-url <URL>] [--resource <URL>] [--scopes <SCOPES>] [--tasks] [--name <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | empty destination directory | required |
| `--language <LANGUAGE>` | starter language alias | one of `node` · `go` · `python` |
| `--runtime <RUNTIME>` | starter language (node, go or python; default node) | one of `node` · `go` · `python` |
| `--auth <MODE>` | access mode (default open) | one of `open` · `external-oauth` |
| `--issuer <URL>` | HTTPS OAuth issuer for private access |  |
| `--jwks-url <URL>` | HTTPS OAuth signing-key URL |  |
| `--resource <URL>` | canonical HTTPS MCP endpoint and token audience |  |
| `--scopes <SCOPES>` | comma-separated required OAuth scopes |  |
| `--tasks` | scaffold separate web, worker and observer task projects |  |
| `--name <SLUG>` | app slug prefix and task namespace (required with --tasks) |  |

Examples:

```sh
gregale mcp init --path ./my-mcp
gregale mcp init --path ./my-mcp --runtime go
gregale mcp init --path ./my-mcp --runtime python
gregale mcp init --path ./private-mcp --auth external-oauth --issuer https://identity.example.com --jwks-url https://identity.example.com/jwks --resource https://mcp.example.com/mcp --scopes mcp:tools
gregale mcp init --path ./reports --tasks --name reports
```

### mcp capabilities

Show the built-in starter contract and runtime differences

`gregale mcp capabilities [--runtime <RUNTIME>] [--markdown]`

| Flag | Meaning | |
|---|---|---|
| `--runtime <RUNTIME>` | filter to one starter runtime | one of `node` · `go` · `python` |
| `--markdown` | render the generated starter capability reference |  |

Examples:

```sh
gregale mcp capabilities --json
gregale mcp capabilities --runtime python
```

### mcp monitor

Manage reviewed catalog roles and OAuth connections

#### mcp monitor setup

setup catalog observer

`gregale mcp monitor setup [--scope <VALUE>] [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--scope <VALUE>` | connection environment (default when omitted) |  |
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | reviewed MCP authorization role |  |
| `--all-roles` | aggregate all configured roles (status only) |  |
| `--oauth-revocation-endpoint <VALUE>` | explicit HTTPS RFC 7009 endpoint |  |
| `--oauth-revocation-auth-method <VALUE>` | revocation authentication: none, client_secret_basic or client_secret_post |  |
| `--oauth-profile <VALUE>` | provider profile: generic (default), google or oidc |  |
| `--oauth-token-auth-method <VALUE>` | none, client_secret_basic or client_secret_post |  |
| `--oauth-browser-redirect-uri <VALUE>` | registered HTTPS console MCP callback URL |  |
| `--oauth-callback-port <VALUE>` | fixed loopback port; 0 chooses a dynamic port |  |
| `--oauth-resource <VALUE>` | reviewed HTTPS MCP resource indicator |  |
| `--oauth-issuer <VALUE>` | reviewed HTTPS provider issuer |  |
| `--oauth-discovery-url <VALUE>` | explicit HTTPS provider metadata URL |  |
| `--oauth-guided` | use browser authorization instead of a refresh secret |  |
| `--oauth-authorization-endpoint <VALUE>` | explicit HTTPS browser authorization endpoint |  |
| `--oauth-scopes <VALUE>` | space-separated OAuth scopes |  |
| `--oauth-token-endpoint <VALUE>` | explicit HTTPS OAuth token endpoint (managed setup) |  |
| `--oauth-client-id <VALUE>` | OAuth client ID (managed setup) |  |
| `--oauth-refresh-secret-ref <VALUE>` | app secret holding initial OAuth refresh token |  |
| `--oauth-client-secret-ref <VALUE>` | optional app secret for confidential client authentication |  |
| `--managed` | run a platform-managed observer (setup only) |  |
| `--secret-ref <VALUE>` | app environment secret:NAME for the managed MCP token (setup only) |  |
| `--baseline <VALUE>` | reviewed format-2 catalog baseline (setup only) |  |
| `--freshness-seconds <VALUE>` | maximum observation age (setup only) |  |
| `--token-env <VALUE>` | MCP client token environment variable name (setup/run) |  |
| `--endpoint <VALUE>` | MCP endpoint path (managed setup/run) |  |
| `--interval <VALUE>` | catalog check interval (managed setup/run) |  |
| `--timeout <VALUE>` | per-check read and report deadline |  |

#### mcp monitor disable

disable catalog observer

`gregale mcp monitor disable [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | reviewed MCP authorization role |  |
| `--all-roles` | aggregate all configured roles (status only) |  |
| `--oauth-revocation-endpoint <VALUE>` | explicit HTTPS RFC 7009 endpoint |  |
| `--oauth-revocation-auth-method <VALUE>` | revocation authentication: none, client_secret_basic or client_secret_post |  |
| `--oauth-profile <VALUE>` | provider profile: generic (default), google or oidc |  |
| `--oauth-token-auth-method <VALUE>` | none, client_secret_basic or client_secret_post |  |
| `--oauth-browser-redirect-uri <VALUE>` | registered HTTPS console MCP callback URL |  |
| `--oauth-callback-port <VALUE>` | fixed loopback port; 0 chooses a dynamic port |  |
| `--oauth-resource <VALUE>` | reviewed HTTPS MCP resource indicator |  |
| `--oauth-issuer <VALUE>` | reviewed HTTPS provider issuer |  |
| `--oauth-discovery-url <VALUE>` | explicit HTTPS provider metadata URL |  |
| `--oauth-guided` | use browser authorization instead of a refresh secret |  |
| `--oauth-authorization-endpoint <VALUE>` | explicit HTTPS browser authorization endpoint |  |
| `--oauth-scopes <VALUE>` | space-separated OAuth scopes |  |
| `--oauth-token-endpoint <VALUE>` | explicit HTTPS OAuth token endpoint (managed setup) |  |
| `--oauth-client-id <VALUE>` | OAuth client ID (managed setup) |  |
| `--oauth-refresh-secret-ref <VALUE>` | app secret holding initial OAuth refresh token |  |
| `--oauth-client-secret-ref <VALUE>` | optional app secret for confidential client authentication |  |
| `--managed` | run a platform-managed observer (setup only) |  |
| `--secret-ref <VALUE>` | app environment secret:NAME for the managed MCP token (setup only) |  |
| `--baseline <VALUE>` | reviewed format-2 catalog baseline (setup only) |  |
| `--freshness-seconds <VALUE>` | maximum observation age (setup only) |  |
| `--token-env <VALUE>` | MCP client token environment variable name (setup/run) |  |
| `--endpoint <VALUE>` | MCP endpoint path (managed setup/run) |  |
| `--interval <VALUE>` | catalog check interval (managed setup/run) |  |
| `--timeout <VALUE>` | per-check read and report deadline |  |

#### mcp monitor status

status catalog observer

`gregale mcp monitor status [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | reviewed MCP authorization role |  |
| `--all-roles` | aggregate all configured roles (status only) |  |
| `--oauth-revocation-endpoint <VALUE>` | explicit HTTPS RFC 7009 endpoint |  |
| `--oauth-revocation-auth-method <VALUE>` | revocation authentication: none, client_secret_basic or client_secret_post |  |
| `--oauth-profile <VALUE>` | provider profile: generic (default), google or oidc |  |
| `--oauth-token-auth-method <VALUE>` | none, client_secret_basic or client_secret_post |  |
| `--oauth-browser-redirect-uri <VALUE>` | registered HTTPS console MCP callback URL |  |
| `--oauth-callback-port <VALUE>` | fixed loopback port; 0 chooses a dynamic port |  |
| `--oauth-resource <VALUE>` | reviewed HTTPS MCP resource indicator |  |
| `--oauth-issuer <VALUE>` | reviewed HTTPS provider issuer |  |
| `--oauth-discovery-url <VALUE>` | explicit HTTPS provider metadata URL |  |
| `--oauth-guided` | use browser authorization instead of a refresh secret |  |
| `--oauth-authorization-endpoint <VALUE>` | explicit HTTPS browser authorization endpoint |  |
| `--oauth-scopes <VALUE>` | space-separated OAuth scopes |  |
| `--oauth-token-endpoint <VALUE>` | explicit HTTPS OAuth token endpoint (managed setup) |  |
| `--oauth-client-id <VALUE>` | OAuth client ID (managed setup) |  |
| `--oauth-refresh-secret-ref <VALUE>` | app secret holding initial OAuth refresh token |  |
| `--oauth-client-secret-ref <VALUE>` | optional app secret for confidential client authentication |  |
| `--managed` | run a platform-managed observer (setup only) |  |
| `--secret-ref <VALUE>` | app environment secret:NAME for the managed MCP token (setup only) |  |
| `--baseline <VALUE>` | reviewed format-2 catalog baseline (setup only) |  |
| `--freshness-seconds <VALUE>` | maximum observation age (setup only) |  |
| `--token-env <VALUE>` | MCP client token environment variable name (setup/run) |  |
| `--endpoint <VALUE>` | MCP endpoint path (managed setup/run) |  |
| `--interval <VALUE>` | catalog check interval (managed setup/run) |  |
| `--timeout <VALUE>` | per-check read and report deadline |  |

#### mcp monitor run

run catalog observer

`gregale mcp monitor run [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | reviewed MCP authorization role |  |
| `--all-roles` | aggregate all configured roles (status only) |  |
| `--oauth-revocation-endpoint <VALUE>` | explicit HTTPS RFC 7009 endpoint |  |
| `--oauth-revocation-auth-method <VALUE>` | revocation authentication: none, client_secret_basic or client_secret_post |  |
| `--oauth-profile <VALUE>` | provider profile: generic (default), google or oidc |  |
| `--oauth-token-auth-method <VALUE>` | none, client_secret_basic or client_secret_post |  |
| `--oauth-browser-redirect-uri <VALUE>` | registered HTTPS console MCP callback URL |  |
| `--oauth-callback-port <VALUE>` | fixed loopback port; 0 chooses a dynamic port |  |
| `--oauth-resource <VALUE>` | reviewed HTTPS MCP resource indicator |  |
| `--oauth-issuer <VALUE>` | reviewed HTTPS provider issuer |  |
| `--oauth-discovery-url <VALUE>` | explicit HTTPS provider metadata URL |  |
| `--oauth-guided` | use browser authorization instead of a refresh secret |  |
| `--oauth-authorization-endpoint <VALUE>` | explicit HTTPS browser authorization endpoint |  |
| `--oauth-scopes <VALUE>` | space-separated OAuth scopes |  |
| `--oauth-token-endpoint <VALUE>` | explicit HTTPS OAuth token endpoint (managed setup) |  |
| `--oauth-client-id <VALUE>` | OAuth client ID (managed setup) |  |
| `--oauth-refresh-secret-ref <VALUE>` | app secret holding initial OAuth refresh token |  |
| `--oauth-client-secret-ref <VALUE>` | optional app secret for confidential client authentication |  |
| `--managed` | run a platform-managed observer (setup only) |  |
| `--secret-ref <VALUE>` | app environment secret:NAME for the managed MCP token (setup only) |  |
| `--baseline <VALUE>` | reviewed format-2 catalog baseline (setup only) |  |
| `--freshness-seconds <VALUE>` | maximum observation age (setup only) |  |
| `--token-env <VALUE>` | MCP client token environment variable name (setup/run) |  |
| `--endpoint <VALUE>` | MCP endpoint path (managed setup/run) |  |
| `--interval <VALUE>` | catalog check interval (managed setup/run) |  |
| `--timeout <VALUE>` | per-check read and report deadline |  |

#### mcp monitor pause

pause catalog observer

`gregale mcp monitor pause [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | reviewed MCP authorization role |  |
| `--all-roles` | aggregate all configured roles (status only) |  |
| `--oauth-revocation-endpoint <VALUE>` | explicit HTTPS RFC 7009 endpoint |  |
| `--oauth-revocation-auth-method <VALUE>` | revocation authentication: none, client_secret_basic or client_secret_post |  |
| `--oauth-profile <VALUE>` | provider profile: generic (default), google or oidc |  |
| `--oauth-token-auth-method <VALUE>` | none, client_secret_basic or client_secret_post |  |
| `--oauth-browser-redirect-uri <VALUE>` | registered HTTPS console MCP callback URL |  |
| `--oauth-callback-port <VALUE>` | fixed loopback port; 0 chooses a dynamic port |  |
| `--oauth-resource <VALUE>` | reviewed HTTPS MCP resource indicator |  |
| `--oauth-issuer <VALUE>` | reviewed HTTPS provider issuer |  |
| `--oauth-discovery-url <VALUE>` | explicit HTTPS provider metadata URL |  |
| `--oauth-guided` | use browser authorization instead of a refresh secret |  |
| `--oauth-authorization-endpoint <VALUE>` | explicit HTTPS browser authorization endpoint |  |
| `--oauth-scopes <VALUE>` | space-separated OAuth scopes |  |
| `--oauth-token-endpoint <VALUE>` | explicit HTTPS OAuth token endpoint (managed setup) |  |
| `--oauth-client-id <VALUE>` | OAuth client ID (managed setup) |  |
| `--oauth-refresh-secret-ref <VALUE>` | app secret holding initial OAuth refresh token |  |
| `--oauth-client-secret-ref <VALUE>` | optional app secret for confidential client authentication |  |
| `--managed` | run a platform-managed observer (setup only) |  |
| `--secret-ref <VALUE>` | app environment secret:NAME for the managed MCP token (setup only) |  |
| `--baseline <VALUE>` | reviewed format-2 catalog baseline (setup only) |  |
| `--freshness-seconds <VALUE>` | maximum observation age (setup only) |  |
| `--token-env <VALUE>` | MCP client token environment variable name (setup/run) |  |
| `--endpoint <VALUE>` | MCP endpoint path (managed setup/run) |  |
| `--interval <VALUE>` | catalog check interval (managed setup/run) |  |
| `--timeout <VALUE>` | per-check read and report deadline |  |

#### mcp monitor resume

resume catalog observer

`gregale mcp monitor resume [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | reviewed MCP authorization role |  |
| `--all-roles` | aggregate all configured roles (status only) |  |
| `--oauth-revocation-endpoint <VALUE>` | explicit HTTPS RFC 7009 endpoint |  |
| `--oauth-revocation-auth-method <VALUE>` | revocation authentication: none, client_secret_basic or client_secret_post |  |
| `--oauth-profile <VALUE>` | provider profile: generic (default), google or oidc |  |
| `--oauth-token-auth-method <VALUE>` | none, client_secret_basic or client_secret_post |  |
| `--oauth-browser-redirect-uri <VALUE>` | registered HTTPS console MCP callback URL |  |
| `--oauth-callback-port <VALUE>` | fixed loopback port; 0 chooses a dynamic port |  |
| `--oauth-resource <VALUE>` | reviewed HTTPS MCP resource indicator |  |
| `--oauth-issuer <VALUE>` | reviewed HTTPS provider issuer |  |
| `--oauth-discovery-url <VALUE>` | explicit HTTPS provider metadata URL |  |
| `--oauth-guided` | use browser authorization instead of a refresh secret |  |
| `--oauth-authorization-endpoint <VALUE>` | explicit HTTPS browser authorization endpoint |  |
| `--oauth-scopes <VALUE>` | space-separated OAuth scopes |  |
| `--oauth-token-endpoint <VALUE>` | explicit HTTPS OAuth token endpoint (managed setup) |  |
| `--oauth-client-id <VALUE>` | OAuth client ID (managed setup) |  |
| `--oauth-refresh-secret-ref <VALUE>` | app secret holding initial OAuth refresh token |  |
| `--oauth-client-secret-ref <VALUE>` | optional app secret for confidential client authentication |  |
| `--managed` | run a platform-managed observer (setup only) |  |
| `--secret-ref <VALUE>` | app environment secret:NAME for the managed MCP token (setup only) |  |
| `--baseline <VALUE>` | reviewed format-2 catalog baseline (setup only) |  |
| `--freshness-seconds <VALUE>` | maximum observation age (setup only) |  |
| `--token-env <VALUE>` | MCP client token environment variable name (setup/run) |  |
| `--endpoint <VALUE>` | MCP endpoint path (managed setup/run) |  |
| `--interval <VALUE>` | catalog check interval (managed setup/run) |  |
| `--timeout <VALUE>` | per-check read and report deadline |  |

#### mcp monitor restart

restart catalog observer

`gregale mcp monitor restart [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | reviewed MCP authorization role |  |
| `--all-roles` | aggregate all configured roles (status only) |  |
| `--oauth-revocation-endpoint <VALUE>` | explicit HTTPS RFC 7009 endpoint |  |
| `--oauth-revocation-auth-method <VALUE>` | revocation authentication: none, client_secret_basic or client_secret_post |  |
| `--oauth-profile <VALUE>` | provider profile: generic (default), google or oidc |  |
| `--oauth-token-auth-method <VALUE>` | none, client_secret_basic or client_secret_post |  |
| `--oauth-browser-redirect-uri <VALUE>` | registered HTTPS console MCP callback URL |  |
| `--oauth-callback-port <VALUE>` | fixed loopback port; 0 chooses a dynamic port |  |
| `--oauth-resource <VALUE>` | reviewed HTTPS MCP resource indicator |  |
| `--oauth-issuer <VALUE>` | reviewed HTTPS provider issuer |  |
| `--oauth-discovery-url <VALUE>` | explicit HTTPS provider metadata URL |  |
| `--oauth-guided` | use browser authorization instead of a refresh secret |  |
| `--oauth-authorization-endpoint <VALUE>` | explicit HTTPS browser authorization endpoint |  |
| `--oauth-scopes <VALUE>` | space-separated OAuth scopes |  |
| `--oauth-token-endpoint <VALUE>` | explicit HTTPS OAuth token endpoint (managed setup) |  |
| `--oauth-client-id <VALUE>` | OAuth client ID (managed setup) |  |
| `--oauth-refresh-secret-ref <VALUE>` | app secret holding initial OAuth refresh token |  |
| `--oauth-client-secret-ref <VALUE>` | optional app secret for confidential client authentication |  |
| `--managed` | run a platform-managed observer (setup only) |  |
| `--secret-ref <VALUE>` | app environment secret:NAME for the managed MCP token (setup only) |  |
| `--baseline <VALUE>` | reviewed format-2 catalog baseline (setup only) |  |
| `--freshness-seconds <VALUE>` | maximum observation age (setup only) |  |
| `--token-env <VALUE>` | MCP client token environment variable name (setup/run) |  |
| `--endpoint <VALUE>` | MCP endpoint path (managed setup/run) |  |
| `--interval <VALUE>` | catalog check interval (managed setup/run) |  |
| `--timeout <VALUE>` | per-check read and report deadline |  |

#### mcp monitor stop

stop catalog observer

`gregale mcp monitor stop [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | reviewed MCP authorization role |  |
| `--all-roles` | aggregate all configured roles (status only) |  |
| `--oauth-revocation-endpoint <VALUE>` | explicit HTTPS RFC 7009 endpoint |  |
| `--oauth-revocation-auth-method <VALUE>` | revocation authentication: none, client_secret_basic or client_secret_post |  |
| `--oauth-profile <VALUE>` | provider profile: generic (default), google or oidc |  |
| `--oauth-token-auth-method <VALUE>` | none, client_secret_basic or client_secret_post |  |
| `--oauth-browser-redirect-uri <VALUE>` | registered HTTPS console MCP callback URL |  |
| `--oauth-callback-port <VALUE>` | fixed loopback port; 0 chooses a dynamic port |  |
| `--oauth-resource <VALUE>` | reviewed HTTPS MCP resource indicator |  |
| `--oauth-issuer <VALUE>` | reviewed HTTPS provider issuer |  |
| `--oauth-discovery-url <VALUE>` | explicit HTTPS provider metadata URL |  |
| `--oauth-guided` | use browser authorization instead of a refresh secret |  |
| `--oauth-authorization-endpoint <VALUE>` | explicit HTTPS browser authorization endpoint |  |
| `--oauth-scopes <VALUE>` | space-separated OAuth scopes |  |
| `--oauth-token-endpoint <VALUE>` | explicit HTTPS OAuth token endpoint (managed setup) |  |
| `--oauth-client-id <VALUE>` | OAuth client ID (managed setup) |  |
| `--oauth-refresh-secret-ref <VALUE>` | app secret holding initial OAuth refresh token |  |
| `--oauth-client-secret-ref <VALUE>` | optional app secret for confidential client authentication |  |
| `--managed` | run a platform-managed observer (setup only) |  |
| `--secret-ref <VALUE>` | app environment secret:NAME for the managed MCP token (setup only) |  |
| `--baseline <VALUE>` | reviewed format-2 catalog baseline (setup only) |  |
| `--freshness-seconds <VALUE>` | maximum observation age (setup only) |  |
| `--token-env <VALUE>` | MCP client token environment variable name (setup/run) |  |
| `--endpoint <VALUE>` | MCP endpoint path (managed setup/run) |  |
| `--interval <VALUE>` | catalog check interval (managed setup/run) |  |
| `--timeout <VALUE>` | per-check read and report deadline |  |

#### mcp monitor connect

connect catalog observer

`gregale mcp monitor connect [--app <VALUE>] [--role <VALUE>] [--callback-port <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | managed OAuth role |  |
| `--callback-port <VALUE>` | override a dynamic loopback callback port (0–65535) |  |
| `--timeout <VALUE>` | browser authorization deadline |  |

#### mcp monitor reconnect

reconnect catalog observer

`gregale mcp monitor reconnect [--app <VALUE>] [--role <VALUE>] [--callback-port <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | managed OAuth role |  |
| `--callback-port <VALUE>` | override a dynamic loopback callback port (0–65535) |  |
| `--timeout <VALUE>` | browser authorization deadline |  |

#### mcp monitor disconnect

disconnect catalog observer

`gregale mcp monitor disconnect [--app <VALUE>] [--role <VALUE>] [--callback-port <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | managed OAuth role |  |
| `--callback-port <VALUE>` | override a dynamic loopback callback port (0–65535) |  |
| `--timeout <VALUE>` | browser authorization deadline |  |

#### mcp monitor provider-check

provider-check catalog observer

`gregale mcp monitor provider-check [--app <VALUE>] [--role <VALUE>] [--callback-port <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | managed OAuth role |  |
| `--callback-port <VALUE>` | override a dynamic loopback callback port (0–65535) |  |
| `--timeout <VALUE>` | browser authorization deadline |  |

### mcp access

Audit MCP access-policy drift and admission evidence

#### mcp access audit

Compare source and role policy with stored account gateway rules

`gregale mcp access audit --app <SLUG> [--path <DIR>] --auth-placement <PLACEMENT> [--algorithms <LIST>] [--release-policy <PATH>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | MCP application slug | required |
| `--path <DIR>` | reviewed source directory (default .) |  |
| `--auth-placement <PLACEMENT>` | explicit authentication placement | required; one of `gateway` · `guest-only` |
| `--algorithms <LIST>` | reviewed gateway algorithms (default RS256,ES256) |  |
| `--release-policy <PATH>` | optional confined role baselines and exclusions; tokens are not read |  |
| `--timeout <DURATION>` | overall audit deadline (default 30s, at most 2m) |  |

Examples:

```sh
gregale mcp access audit --app my-mcp --path ./my-mcp --auth-placement gateway --json
gregale mcp access audit --app my-mcp --auth-placement guest-only --release-policy release.json
```

#### mcp access history

history MCP access evidence

`gregale mcp access history [--deployment <VALUE>] [--limit <VALUE>] [--cursor <VALUE>] [--outcome <VALUE>] [--evidence <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--deployment <VALUE>` | deployment id to inspect (promotion or recovery target) |  |
| `--limit <VALUE>` | MCP receipts per page, 1–50 |  |
| `--cursor <VALUE>` | next cursor from a previous page |  |
| `--outcome <VALUE>` | filter accepted or rejected receipts |  |
| `--evidence <VALUE>` | inspect an exact evidence UUID instead of deployment history |  |
| `--timeout <VALUE>` | overall read deadline, at most 2m |  |

#### mcp access explain

explain MCP access evidence

`gregale mcp access explain [--evidence <VALUE>] [--app <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--evidence <VALUE>` | retained admission evidence UUID |  |
| `--app <VALUE>` | optional application slug for current convergence |  |
| `--timeout <VALUE>` | overall read deadline, at most 2m |  |

### mcp health

Inspect account MCP connection health

`gregale mcp health [--app <SLUG>] [--offset <N>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | optional app filter |  |
| `--offset <N>` | issue page offset |  |
| `--timeout <DURATION>` | API deadline |  |

### mcp diagnostics

Summarize retained MCP transport failures

`gregale mcp diagnostics --app <SLUG> [--route <PATH>] [--since <DURATION>] [--cursor <CURSOR>] [--limit <N>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--route <PATH>` | retained route label |  |
| `--since <DURATION>` | telemetry window |  |
| `--cursor <CURSOR>` | next page cursor |  |
| `--limit <N>` | rows per page |  |
| `--timeout <DURATION>` | API deadline |  |

### mcp validate

Validate MCP protocol and catalog compatibility

`gregale mcp validate --url <URL> [--app <SLUG>] [--deployment <ID>] [--endpoint-path <PATH>] [--token-env <ENV>] [--protocol-version <VERSION>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | MCP endpoint | required |
| `--app <SLUG>` | optional app for exact-instance probes |  |
| `--deployment <ID>` | deployment to qualify |  |
| `--endpoint-path <PATH>` | configured MCP path |  |
| `--token-env <ENV>` | MCP token environment variable |  |
| `--protocol-version <VERSION>` | selected protocol version |  |
| `--timeout <DURATION>` | overall deadline |  |

### mcp test

Run reviewed MCP application smoke fixtures

`gregale mcp test --url <URL> [--fixtures <PATH>] [--token-env <ENV>] [--allow-tool-execution] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | MCP endpoint | required |
| `--fixtures <PATH>` | fixture JSON file |  |
| `--token-env <ENV>` | MCP token environment variable |  |
| `--allow-tool-execution` | execute reviewed tool fixtures once |  |
| `--timeout <DURATION>` | overall deadline |  |

### mcp retirements

Inspect and recover durable OAuth grant retirement

#### mcp retirements list

List retirement history

`gregale mcp retirements list [--offset <N>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--offset <N>` | history page offset |  |
| `--timeout <DURATION>` | API deadline |  |

#### mcp retirements retry

Retry provider retirement

`gregale mcp retirements retry --id <ID> [--allow-active-grant-revocation] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--id <ID>` | retirement UUID | required |
| `--allow-active-grant-revocation` | allow active grant revocation |  |
| `--timeout <DURATION>` | API deadline |  |

#### mcp retirements acknowledge-manual

Acknowledge manual provider cleanup

`gregale mcp retirements acknowledge-manual --id <ID> [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--id <ID>` | retirement UUID | required |
| `--timeout <DURATION>` | API deadline |  |

### mcp deploy

Pin the candidate catalog, verify MCP discovery and promote

`gregale mcp deploy [--path <DIR>] --name <SLUG> [--size <SIZE>] [--profile <SIZE>] [--token-env <ENV>] [--secrets-file <PATH>] [--release-policy <PATH>] [--validate-url <URL>] [--fixtures <PATH>] [--allow-tool-execution] [--access-audit <PLACEMENT>] [--access-audit-algorithms <LIST>] [--idempotency-key <KEY>] [--mcp-task-migration-override] [--timeout <SECONDS>]`

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | source directory (default .) |  |
| `--name <SLUG>` | app slug | required |
| `--size <SIZE>` | app RAM/CPU resource size | one of `micro` · `small` · `medium` · `large` · `xlarge` |
| `--profile <SIZE>` | deprecated: use --size |  |
| `--token-env <ENV>` | client token for external OAuth verification |  |
| `--secrets-file <PATH>` | sealed app secrets |  |
| `--release-policy <PATH>` | role-specific catalog baselines to verify before promotion |  |
| `--validate-url <URL>` | pre-upload compatibility gate endpoint |  |
| `--fixtures <PATH>` | candidate smoke fixtures |  |
| `--allow-tool-execution` | execute reviewed tool fixtures once |  |
| `--access-audit <PLACEMENT>` | optional stored-policy gate with gateway or guest-only placement | one of `gateway` · `guest-only` |
| `--access-audit-algorithms <LIST>` | reviewed gateway algorithms for --access-audit (default RS256,ES256) |  |
| `--idempotency-key <KEY>` | retry identity printed by a failed submission |  |
| `--mcp-task-migration-override` | allow this deployment to replace incompatible MCP task workers once |  |
| `--timeout <SECONDS>` | deployment wait timeout in seconds (default 1200) |  |

Examples:

```sh
gregale mcp deploy --path ./my-mcp --name my-mcp --size small
```

### mcp overview

Inspect replicas, Task workers, observers and promotion holds

`gregale mcp overview --app <SLUG> [--worker-app <SLUG>] [--observer-app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--release-policy <PATH>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | serving HTTP MCP application | required |
| `--worker-app <SLUG>` | optional separate durable Task worker application |  |
| `--observer-app <SLUG>` | observer to inspect (defaults to worker scaling link) |  |
| `--endpoint <PATH>` | literal MCP endpoint path (default /mcp) |  |
| `--token-env <ENV>` | MCP bearer token environment variable |  |
| `--release-policy <PATH>` | role-specific reviewed catalog baselines |  |
| `--timeout <DURATION>` | overall inspection deadline (default 30s, at most 2m) |  |

Examples:

```sh
gregale mcp overview --app my-mcp
gregale mcp overview --app reports-web --worker-app reports-worker --observer-app reports-observer --json
gregale mcp overview --app my-mcp --release-policy release.json
```

### mcp status

Qualify serving replicas and watch health or catalog changes

`gregale mcp status --app <SLUG> [--endpoint <PATH>] [--token-env <ENV>] [--release-policy <PATH>] [--timeout <DURATION>] [--watch] [--interval <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | Gregale MCP app slug | required |
| `--endpoint <PATH>` | literal MCP endpoint path (default /mcp) |  |
| `--token-env <ENV>` | MCP client bearer token environment variable |  |
| `--release-policy <PATH>` | role-specific catalog baselines and token environments |  |
| `--timeout <DURATION>` | overall replica qualification deadline (default 10s) |  |
| `--watch` | poll until interrupted and report status changes |  |
| `--interval <DURATION>` | poll interval with --watch (default 30s) |  |

Examples:

```sh
gregale mcp status --app my-mcp
gregale mcp status --app my-mcp --token-env MCP_READER_TOKEN
gregale mcp status --app my-mcp --release-policy release.json --json
gregale mcp status --app my-mcp --watch --interval 15s --json
```

### mcp doctor

Check discovery, Origin rejection, compatibility and streaming

`gregale mcp doctor [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [--stack] [--hosting] [--path <DIR>] [--worker-app <SLUG>] [--observer-app <SLUG>] [--release-policy <PATH>] [<app>]`

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
| `--stack` | check hosting overview and reviewed OAuth configuration |  |
| `--hosting` | run the existing Task hosting doctor |  |
| `--path <DIR>` | source configuration directory (with --stack) |  |
| `--worker-app <SLUG>` | distinct Task worker app (with --stack) |  |
| `--observer-app <SLUG>` | observer app (with --stack) |  |
| `--release-policy <PATH>` | reviewed role catalog baselines (with --stack) |  |

Examples:

```sh
gregale mcp doctor --app my-mcp --legacy --stream-tool stream_demo
gregale mcp doctor --stack --app reports-web --worker-app reports-worker --path ./reports/web --json
```

The streaming check is optional.

### mcp tools

Discover tool schemas without invoking tools

`gregale mcp tools [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<app>]`

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

`gregale mcp resources [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<app>]`

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

`gregale mcp resource-read [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<app>]`

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

Configure, release and inspect durable task workers

#### mcp tasks configure

Preview or apply non-secret role settings before first deployment

`gregale mcp tasks configure --app <SLUG> --role <ROLE> --namespace <NAME> [--worker-app <SLUG>] [--apply]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | reserved task app slug | required |
| `--role <ROLE>` | task process role | required; one of `web` · `worker` · `observer` |
| `--namespace <NAME>` | shared stable queue namespace | required |
| `--worker-app <SLUG>` | metrics destination for worker or observer |  |
| `--apply` | write the previewed non-secret settings |  |

Examples:

```sh
gregale mcp tasks configure --app reports-worker --role worker --namespace reports --worker-app reports-worker
gregale mcp tasks configure --app reports-observer --role observer --namespace reports --worker-app reports-worker --apply
```

#### mcp tasks release

Deploy and resume a gated web and worker release

`gregale mcp tasks release [--resume] --plan <PATH> --state <PATH> [run|status|recover|restore|quarantine|retire]`

| Flag | Meaning | |
|---|---|---|
| `--resume` | resume a healthy failed rollout (recover only) |  |
| `--plan <PATH>` | native deployment plan JSON | required |
| `--state <PATH>` | persistent release journal outside source directories | required |

Examples:

```sh
gregale mcp tasks release --plan release.json --state ./release-state.json
gregale mcp tasks release status --plan release.json --state ./release-state.json
```

#### mcp tasks setup

Preview or apply the task-backlog scaling policy

`gregale mcp tasks setup [--app <SLUG>] [--min <N>] [--max <N>] [--target <N>] [--observer-app <SLUG>] [--apply]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | worker app slug (defaults to the linked project app) |  |
| `--min <N>` | minimum replicas (default 1; use 0 with an always-on observer) |  |
| `--max <N>` | maximum replicas (default 10) |  |
| `--target <N>` | outstanding tasks per worker (default 4) |  |
| `--observer-app <SLUG>` | separate always-on observer publishing worker metrics |  |
| `--apply` | apply the proposed worker scaling policy |  |

Examples:

```sh
gregale mcp tasks setup --app mcp-worker
gregale mcp tasks setup --app mcp-worker --observer-app mcp-observer --min 0 --apply
```

#### mcp tasks observer

Validate observer credentials, namespace and metric readiness

`gregale mcp tasks observer <setup|status>`

##### mcp tasks observer setup

Inspect the observer and optionally ensure minimum one replica

`gregale mcp tasks observer setup --app <SLUG> --worker-app <SLUG> [--config <PATH>] [--apply]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | observer worker app slug | required |
| `--worker-app <SLUG>` | task worker app slug | required |
| `--config <PATH>` | local MCP source config (default gregale-mcp.json) |  |
| `--apply` | ensure at least one configured observer replica |  |

##### mcp tasks observer status

Read observer configuration and heartbeat freshness

`gregale mcp tasks observer status [--app <SLUG>] --worker-app <SLUG> [--config <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | observer slug (defaults to linked observer) |  |
| `--worker-app <SLUG>` | task worker app slug | required |
| `--config <PATH>` | optional local MCP source config |  |

#### mcp tasks status

Show task scaling policy and custom metric freshness

`gregale mcp tasks status [--app <SLUG>] [--details] [--require-fresh] [--max-oldest-age <DURATION>] [--max-without-compatible-worker <N>] [--sustain-for <DURATION>] [--poll-interval <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | worker app slug (defaults to the linked project app) |  |
| `--details` | include observer handler-version compatibility details |  |
| `--require-fresh` | fail when required metrics or observer signals are unavailable |  |
| `--max-oldest-age <DURATION>` | maximum oldest queued Task age |  |
| `--max-without-compatible-worker <N>` | maximum pending Tasks without compatible workers |  |
| `--sustain-for <DURATION>` | require a violation to persist before failing |  |
| `--poll-interval <DURATION>` | poll interval for sustained checks (default 15s) |  |

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

Capture MCP catalog definitions without invoking anything

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

Does not read resources, render prompts or invoke tools.

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


## menu

Choose a common task and open its guided command

`gregale menu`

Examples:

```sh
gregale menu
```


## start

Get your first app live with a few guided prompts

`gregale start [path]`

Examples:

```sh
gregale start
gregale start ./my-api
```


## account

Export or delete your account, and view its status, DPA and SLO

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

Print the Data Processing Agreement template

`gregale account dpa [-o <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `-o <PATH>` | write the DPA template to a file |  |

Placeholders are filled when the agreement is signed.

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

`gregale add bucket --app <APP> [--env <SCOPE>] [--scope <SCOPE>] [--region <REGION>] [--public] [--serve-at <PATH>] [--permission <MODE>] [--label <LABEL>] [--prefix <PREFIX>] [--wait-timeout <DURATION>] <name>`

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

### add datadog

Send the app&#39;s logs and deployment events to Datadog

`gregale add datadog --app <APP> [--site <SITE>] [--api-key-env <NAME>] [--api-key-stdin] [--dry-run] [--remove]`

| Flag | Meaning | |
|---|---|---|
| `--app <APP>` | app slug | required |
| `--site <SITE>` | Datadog site (default us1) | one of `ap1` · `eu1` · `us1` · `us3` · `us5` |
| `--api-key-env <NAME>` | environment variable holding the API key (default DD_API_KEY) |  |
| `--api-key-stdin` | read the API key from stdin |  |
| `--dry-run` | show the plan without changing anything |  |
| `--remove` | remove the app&#39;s Datadog log drain and webhook |  |

Examples:

```sh
DD_API_KEY=... gregale add datadog --app my-api --site eu1
gregale add datadog --app my-api --dry-run
gregale add datadog --app my-api --remove
```


## bucket

Manage object storage buckets: encryption, versioning and lifecycle

`gregale bucket [<subcommand>]`

### bucket list

List an app&#39;s object-storage buckets and their IDs

`gregale bucket list <app>`

### bucket delete

Delete an empty bucket (including retained versions)

`gregale bucket delete [--yes] <app> <bucket-id>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | skip the typed confirmation (alias: --quiet) |  |

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

`gregale bucket upload [--content-type <TYPE>] [--if-match <ETAG>] [--if-none-match <*>] [--resume <UPLOAD-ID>] [--timeout <DURATION>] <app> <bucket-id> <key> <file>`

| Flag | Meaning | |
|---|---|---|
| `--content-type <TYPE>` | object MIME type |  |
| `--if-match <ETAG>` | replace a matching object (single PUT only) |  |
| `--if-none-match <*>` | create only if absent (single PUT only) |  |
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

### bucket cors

Let browsers on your sites use the bucket&#39;s signed URLs

#### bucket cors get

Read the bucket&#39;s CORS rules

`gregale bucket cors get <app> <bucket-id>`

#### bucket cors set

Replace rules from a JSON file or stdin

`gregale bucket cors set <app> <bucket-id> <JSON-file|->`

#### bucket cors clear

Remove the rules; console origins stay allowed

`gregale bucket cors clear <app> <bucket-id>`

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

Inspect an app&#39;s resource bindings and credential rotation

`gregale bindings [<subcommand>] <app> [--deployment <ID|vN>] [--require-complete] [--scope <SCOPE>]`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID|vN>` | inspect exact deployment service bindings, including frozen environment routes |  |
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

`gregale bindings release-policy set [--scope <SCOPE>] [--mode <off|enforce>] [--require-verification] [--max-age <DURATION>] [--require-application-ack] [--object-storage-require <PREFIX=read|write|read_write>] [--clear-object-storage-requirements] [--queue-require <NAME=consumer_readiness>] [--clear-queue-requirements] --expected-revision <N> [--reason <TEXT>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | deployment scope (default default) |  |
| `--mode <off|enforce>` | disable or enable enforcement |  |
| `--require-verification` | alias for --mode enforce |  |
| `--max-age <DURATION>` | maximum verification age (default 10m; 1s to 24h) |  |
| `--require-application-ack` | require current application acknowledgements |  |
| `--object-storage-require <PREFIX=read|write|read_write>` | require storage operations (repeatable; replaces all storage requirements) |  |
| `--clear-object-storage-requirements` | explicitly remove all storage operation requirements |  |
| `--queue-require <NAME=consumer_readiness>` | require push queue readiness (repeatable; connectivity remains partial) |  |
| `--clear-queue-requirements` | explicitly remove all queue readiness requirements |  |
| `--expected-revision <N>` | current policy revision; use 0 initially | required |
| `--reason <TEXT>` | update reason; required when disabling enforcement |  |

Examples:

```sh
gregale bindings release-policy set public-api --scope production --require-verification --max-age 10m --expected-revision 0
```

### bindings service

Configure standalone service bindings

#### bindings service explain

Explain service authorization, optionally for one deployment

`gregale bindings service explain [--deployment <ID>] [--scope <ENV>] [--method <METHOD>] [--path <PATH>] <caller> <target>`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | exact caller deployment UUID; use server routing and effective policy |  |
| `--scope <ENV>` | assert deployment environment; requires --deployment |  |
| `--method <METHOD>` | HTTP method (default GET) |  |
| `--path <PATH>` | origin-form request path (default /) |  |

Exits 0 when allowed, 1 when denied, and 2 when indeterminate or on error.

#### bindings service export

Export live service settings as a reusable policy

`gregale bindings service export [--out <PATH>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--out <PATH>` | write to a new private file (default stdout) |  |

#### bindings service check

Compare live settings with a policy; exits 0 match, 1 drift, 2 error

`gregale bindings service check --file <PATH> <app>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | JSON service binding settings policy | required |

#### bindings service configure

Patch service targets, permissions, reliability and URL transport

`gregale bindings service configure [--plan-out <PATH>] --file <PATH> [--dry-run] <app>`

| Flag | Meaning | |
|---|---|---|
| `--plan-out <PATH>` | save a reviewed plan without changes; destination must be new |  |
| `--file <PATH>` | JSON service binding settings patch | required |
| `--dry-run` | show current and proposed binding settings without changes |  |

Examples:

```sh
gregale bindings service configure public-api --file policy.json --dry-run
```

#### bindings service apply

Apply a saved service binding plan if reviewed settings are unchanged

`gregale bindings service apply --plan <PATH> <app>`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | saved service binding plan | required |

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

`gregale bindings check [--interactive] [--app <SLUG>] [--scope <SCOPE>] [--max-verification-age <DURATION>] [--deployment <ID|vN>] [--allow-unsupported] [--require-application-ack] [--release-policy] [--object-storage-require <PREFIX=read|write|read_write>] [--queue-require <NAME=consumer_readiness>] [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] [<app>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a live deployment and readiness options |  |
| `--app <SLUG>` | app slug (linked app or picker in interactive mode) |  |
| `--scope <SCOPE>` | require the selected deployment to use this scope (default its current scope) |  |
| `--max-verification-age <DURATION>` | maximum age of passed probe evidence (default 10m) |  |
| `--deployment <ID|vN>` | exact live deployment whose evidence must pass, including zero-traffic candidates |  |
| `--allow-unsupported` | waive connectivity coverage for active queue and outbound bindings |  |
| `--require-application-ack` | require current PostgreSQL/object-storage application acknowledgements |  |
| `--release-policy` | apply stored scope requirements and confirm the policy after every inventory evaluation (read-only) |  |
| `--object-storage-require <PREFIX=read|write|read_write>` | require recorded storage operation evidence (repeatable; no writes) |  |
| `--queue-require <NAME=consumer_readiness>` | require recorded push queue readiness (repeatable; connectivity remains partial) |  |
| `--wait` | poll read-only inventory while probes, refreshes or application acknowledgements are pending |  |
| `--timeout <DURATION>` | maximum waiting or policy-aware preflight time (default 5m) |  |
| `--poll-interval <DURATION>` | inventory polling interval with --wait (default 1s) |  |

Examples:

```sh
gregale bindings check --app my-api --interactive
gregale bindings check my-api --max-verification-age 10m --json
gregale bindings check my-api --scope production
gregale bindings check my-api --deployment v12 --release-policy --wait --json
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

### bindings verification-runs

Inspect, resume or cancel a durable binding verification run

#### bindings verification-runs list

List retained receipt summaries without requalifying evidence

`gregale bindings verification-runs list [--limit <N>] [--cursor <CURSOR>] [--deployment <UUID>] [--status <STATUS>] [--scope <SCOPE>] [--include-members] <app>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (default 20; range 1–100) |  |
| `--cursor <CURSOR>` | next page cursor; reuse filters |  |
| `--deployment <UUID>` | exact deployment filter |  |
| `--status <STATUS>` | stored state or observed expiry |  |
| `--scope <SCOPE>` | deployment scope |  |
| `--include-members` | include graph members with root linkage |  |

#### bindings verification-runs diagnostics

Explain blockers, public changes and recovery actions

`gregale bindings verification-runs diagnostics <app> <run-id>`

#### bindings verification-runs status

Read the original plan and current resource progress

`gregale bindings verification-runs status <app> <run-id>`

#### bindings verification-runs resume

Continue the original review without renewing deadlines

`gregale bindings verification-runs resume [--progress <MODE>] [--status-read-retries <N>] [--concurrency <N>] [--poll-interval <DURATION>] [--wait-timeout <DURATION>] <app> <run-id>`

| Flag | Meaning | |
|---|---|---|
| `--progress <MODE>` | durable stderr progress: auto, plain, ndjson or off |  |
| `--status-read-retries <N>` | transient durable status GET retries (default 3; range 0–8) |  |
| `--concurrency <N>` | concurrent probe limit (default 2; range 1–8) |  |
| `--poll-interval <DURATION>` | probe polling interval |  |
| `--wait-timeout <DURATION>` | wait budget bounded by the original deadline |  |

#### bindings verification-runs cancel

Block new admission and cancel unfinished owned work

`gregale bindings verification-runs cancel <app> <run-id>`

### bindings verify

Check a private route or test a PostgreSQL or storage binding

`gregale bindings verify [--all] [--require-complete] [--release-policy] [--durable] [--run-id <UUID>] [--progress <MODE>] [--status-read-retries <N>] [--concurrency <N>] [--dry-run] [--postgres <ENVIRONMENT_KEY>] [--outbound <INTEGRATION_ID>] [--object-storage <PREFIX>] [--queue <BINDING>] [--queue-publish] [--object-storage-write] [--deployment <ID|vN>] [--poll-interval <D>] [--wait-timeout <D>] <app> [<service>]`

| Flag | Meaning | |
|---|---|---|
| `--all` | verify services, PostgreSQL, storage, outbound and eligible queues (publication opt-in) |  |
| `--require-complete` | with --all, fail unless every eligible binding is checked and passes |  |
| `--release-policy` | reuse current evidence, verify required bindings and wait for policy-aware preflight |  |
| `--durable` | persist a single-app release-policy verification receipt |  |
| `--run-id <UUID>` | caller-selected durable run identity for recovery |  |
| `--progress <MODE>` | durable stderr progress: auto, plain, ndjson or off |  |
| `--status-read-retries <N>` | transient durable status GET retries (default 3; range 0–8) |  |
| `--concurrency <N>` | release-policy probe limit (default 2; range 1–8) |  |
| `--dry-run` | preview --all or --release-policy probes and required consent without creating tasks or messages |  |
| `--postgres <ENVIRONMENT_KEY>` | verify one managed PostgreSQL binding by environment key |  |
| `--outbound <INTEGRATION_ID>` | verify a configured outbound integration by UUID |  |
| `--object-storage <PREFIX>` | verify one object-storage binding by environment prefix (read access only) |  |
| `--queue <BINDING>` | verify a safe push queue message on the exact deployment |  |
| `--queue-publish` | consent to safe queue messages; --all also requires --deployment |  |
| `--object-storage-write` | opt into storage upload and cleanup (plus download for read/write); --all requires --deployment |  |
| `--deployment <ID|vN>` | exact live deployment to verify, including zero-traffic candidates |  |
| `--poll-interval <D>` | status polling interval while the canary runs |  |
| `--wait-timeout <D>` | maximum time to wait for canaries; bounds the entire --release-policy workflow |  |

Examples:

```sh
gregale bindings verify my-api billing
gregale bindings verify my-api --all
gregale bindings verify my-api --deployment v12 --all
gregale bindings verify my-api --all --deployment v12 --dry-run --json
gregale bindings verify my-api --postgres DATABASE_URL
gregale bindings verify my-api --object-storage GREGALE_S3_ASSETS
gregale bindings verify my-api --deployment v12 --release-policy --dry-run --json
gregale bindings verify my-api --deployment v12 --release-policy --object-storage-write --json
```

### bindings smoke

Invoke a path on one live deployment over the private binding

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


## ai

Configure the AI gateway: providers, cost caps, guardrails and usage

`gregale ai [<subcommand>]`

Examples:

```sh
gregale ai policy <integration-id> --provider anthropic --daily-tokens 2000000 --daily-cost 25 --request-log on
gregale ai guardrails <integration-id> --app my-api --rpm 60 --models 'gpt-4o-mini,o3*' --max-output-tokens 2048
gregale ai prices set <integration-id> --price model=gpt-4o-mini,input=0.15,cached=0.075,output=0.60
gregale ai usage <integration-id> --date 2026-10-11
gregale ai policy <integration-id> --user-daily-tokens 200000 --user-daily-cost 2
gregale ai users <integration-id> --limit 20
gregale ai users export <integration-id> --from 2026-10-01 > ai-users.csv
gregale ai catalog openai --model gpt-4o-mini
gregale ai routes create --name chat --format openai --target <openai-id> --target <anthropic-id>:claude-sonnet-4
gregale ai routes canary <route-id> --position 2 --percent 10 --max-latency-increase 50
gregale ai routes set <route-id> --target <openai-id> --target <anthropic-id>:claude-sonnet-4-5 --strategy latency
gregale ai content-policy <integration-id> --moderation block --moderation-kind classifier --moderation-integration <id> --moderation-model gpt-5-nano --moderation-categories prompt_injection,harassment
printf '%s\n' "$SECOND_KEY" | gregale ai keys add <integration-id> --label second --from-stdin
gregale ai logs <integration-id> --limit 20
gregale ai tags <integration-id> --key feature
gregale ai tags export <integration-id> --from 2026-10-01 --to 2026-10-31 --key customer > ai-tags.csv
gregale ai tag-limits <integration-id> --set feature=search:20
gregale ai traces <integration-id> --limit 20
gregale ai traces <integration-id> <trace-id>
gregale ai policy <integration-id> --trace-max-calls 50 --trace-max-cost 2 --trace-max-repeated-tool-calls 3
gregale ai policy <integration-id> --request-log-content on --online-eval-percent 5 --online-eval-judge <openai-id> --online-eval-model gpt-5-mini --quality-alerts on
gregale ai feedback <integration-id> <call-id> --bad --label wrong_answer
gregale ai quality-alerts <integration-id>
gregale ai logs show <integration-id> <request-id>
gregale ai content-policy <integration-id> --email redact --secrets block --block-term 'Project Falcon'
gregale ai content-policy <integration-id> --response-email redact --response-secrets block --response-terms block --response-moderation flag
gregale ai replay <integration-id> <request-id> --target <anthropic-id>:claude-sonnet-4-5 --target <openai-id>:gpt-4o-mini
gregale ai evaluate <route-id> --position 2 --judge <openai-id> --judge-model gpt-5 --canary-percent 10
gregale ai tokens create --app my-api --name laptop --expires-in-hours 24
```

### ai policy

Set the AI provider and token or cost limits

`gregale ai policy [--provider <PROVIDER>] [--daily-tokens <N>] [--daily-cost <USD>] [--monthly-cost <USD>] [--request-log <on|off>] [--request-log-content <on|off>] [--user-daily-tokens <N>] [--user-daily-cost <USD>] [--log-export <on|off>] [--allow-unmetered <on|off>] [--prompt-cache <on|off>] [--trace-max-calls <N>] [--trace-max-cost <USD>] [--trace-max-repeated-tool-calls <N>] [--online-eval-percent <N>] [--online-eval-judge <ID>] [--online-eval-model <MODEL>] [--online-eval-rubric <TEXT>] [--quality-alerts <on|off>] <integration-id>`

| Flag | Meaning | |
|---|---|---|
| `--provider <PROVIDER>` | AI provider | one of `openai` · `anthropic` · `gemini` · `azure_openai` · `bedrock` · `vertex` · `off` |
| `--daily-tokens <N>` | UTC-day input+output token limit, or none |  |
| `--daily-cost <USD>` | UTC-day cost limit in USD, or none |  |
| `--monthly-cost <USD>` | calendar-month cost limit in USD, or none |  |
| `--request-log <on|off>` | log per-call metadata for 7 days | one of `on` · `off` |
| `--request-log-content <on|off>` | also keep the first 32 KiB of each logged call&#39;s bodies for 3 days | one of `on` · `off` |
| `--user-daily-tokens <N>` | each end user&#39;s UTC-day token limit (X-Gregale-AI-User), or none |  |
| `--user-daily-cost <USD>` | each end user&#39;s UTC-day cost limit in USD, or none |  |
| `--log-export <on|off>` | send each app call&#39;s metadata to the app&#39;s log drains | one of `on` · `off` |
| `--allow-unmetered <on|off>` | let through calls the gateway cannot meter (realtime client secrets, vector stores, fine-tuning), outside the caps | one of `on` · `off` |
| `--prompt-cache <on|off>` | add prompt-cache breakpoints to Claude requests that carry none: after a repeated system prompt or tools, and after a continuing conversation | one of `on` · `off` |
| `--trace-max-calls <N>` | calls each AI trace (X-Gregale-AI-Trace or traceparent) may make, or none |  |
| `--trace-max-cost <USD>` | cost each AI trace may reach in USD, or none |  |
| `--trace-max-repeated-tool-calls <N>` | consecutive calls of a trace that may request the same tools with the same arguments, or none |  |
| `--online-eval-percent <N>` | share of answered app calls a judge model scores from 1 to 5 (needs --request-log-content on), or none |  |
| `--online-eval-judge <ID>` | the AI integration that runs the judge model |  |
| `--online-eval-model <MODEL>` | the judge model |  |
| `--online-eval-rubric <TEXT>` | what a good answer does for this app, up to 1,000 bytes, or none |  |
| `--quality-alerts <on|off>` | alert bound apps when a model&#39;s quality drifts from its previous 7 days | one of `on` · `off` |

Flags you omit keep their values.

### ai content-policy

Show or set content guardrails for requests and responses

`gregale ai content-policy [--email <ACTION>] [--payment-card <ACTION>] [--iban <ACTION>] [--secrets <ACTION>] [--block-term <TERM>] [--clear-terms] [--moderation <ACTION>] [--moderation-kind <KIND>] [--moderation-integration <ID>] [--moderation-model <MODEL>] [--moderation-categories <LIST>] [--moderation-on-error <allow|block>] [--response-email <ACTION>] [--response-payment-card <ACTION>] [--response-iban <ACTION>] [--response-secrets <ACTION>] [--response-terms <off|block>] [--response-moderation <ACTION>] <integration-id>`

| Flag | Meaning | |
|---|---|---|
| `--email <ACTION>` | e-mail addresses | one of `off` · `redact` · `block` |
| `--payment-card <ACTION>` | payment card numbers | one of `off` · `redact` · `block` |
| `--iban <ACTION>` | IBANs | one of `off` · `redact` · `block` |
| `--secrets <ACTION>` | API keys, tokens and private keys | one of `off` · `redact` · `block` |
| `--block-term <TERM>` | block requests containing this term, ignoring case (repeat) |  |
| `--clear-terms` | remove every blocked term first |  |
| `--moderation <ACTION>` | check requests with a model: off, flag or block | one of `off` · `flag` · `block` |
| `--moderation-kind <KIND>` | openai_moderation or classifier | one of `openai_moderation` · `classifier` |
| `--moderation-integration <ID>` | the AI integration that runs the check |  |
| `--moderation-model <MODEL>` | the check&#39;s model (required for classifier) |  |
| `--moderation-categories <LIST>` | comma-separated categories to check |  |
| `--moderation-on-error <allow|block>` | when the check fails | one of `allow` · `block` |
| `--response-email <ACTION>` | e-mail addresses in responses | one of `off` · `redact` · `block` |
| `--response-payment-card <ACTION>` | payment card numbers in responses | one of `off` · `redact` · `block` |
| `--response-iban <ACTION>` | IBANs in responses | one of `off` · `redact` · `block` |
| `--response-secrets <ACTION>` | API keys, tokens and private keys in responses | one of `off` · `redact` · `block` |
| `--response-terms <off|block>` | block responses containing a blocked term | one of `off` · `block` |
| `--response-moderation <ACTION>` | check responses with the moderation check | one of `off` · `flag` · `block` |

Redacts or blocks e-mail, cards, IBANs and secrets, and blocks terms. Flags you omit keep their values.

### ai tags

Show one UTC day&#39;s AI usage by tag, or export a day range

`gregale ai tags [--date <YYYY-MM-DD>] [--key <KEY>] [--limit <N>] [--from <YYYY-MM-DD>] [--to <YYYY-MM-DD>] [--format <FORMAT>] [export] <integration-id>`

| Flag | Meaning | |
|---|---|---|
| `--date <YYYY-MM-DD>` | UTC day within the last 31 days (default today) |  |
| `--key <KEY>` | show or export only this tag key&#39;s tags |  |
| `--limit <N>` | tags to show, 1 to 1000 |  |
| `--from <YYYY-MM-DD>` | export: first UTC day within the last 31 days (default today) |  |
| `--to <YYYY-MM-DD>` | export: last UTC day (default today) |  |
| `--format <FORMAT>` | export: csv (default) or ndjson | one of `csv` · `ndjson` |

Tags come from the X-Gregale-AI-Tags header, each with its limit. Export with: tags export &lt;integration-id&gt;.

### ai traces

List an integration&#39;s AI traces, newest first, or show one trace

`gregale ai traces [--app <ID>] [--user <ID>] [--limit <N>] <integration-id> [trace-id]`

| Flag | Meaning | |
|---|---|---|
| `--app <ID>` | only traces whose first call came from this app (UUID) |  |
| `--user <ID>` | only traces whose first call named this end user |  |
| `--limit <N>` | traces to show, 1 to 200 |  |

Pass a trace ID after the integration ID to show that trace&#39;s calls.

### ai feedback

Rate a logged AI call by its X-Gregale-AI-Call ID

`gregale ai feedback [--good] [--bad] [--label <LABEL>] <integration-id> <call-id>`

| Flag | Meaning | |
|---|---|---|
| `--good` | rate the call good |  |
| `--bad` | rate the call bad |  |
| `--label <LABEL>` | a short label such as wrong_answer |  |

### ai quality-alerts

List an integration&#39;s AI quality drift alerts, newest first

`gregale ai quality-alerts [--limit <N>] <integration-id>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | alerts to show, 1 to 100 |  |

### ai deployments

List an app&#39;s AI gateway calls, errors and cost per deployment

`gregale ai deployments [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project app) |  |

Covers the last 14 days, including flagged answers and cost per call.

### ai tag-limits

Show or change per-tag daily cost limits (unchanged limits are kept)

`gregale ai tag-limits [--set <SPEC>] [--remove <TAG>] [--clear] <integration-id>`

| Flag | Meaning | |
|---|---|---|
| `--set <SPEC>` | cap a tag&#39;s UTC-day cost: key=value:USD (repeat) |  |
| `--remove <TAG>` | remove a tag&#39;s limit (repeat) |  |
| `--clear` | remove every limit before applying --set |  |

### ai app-limit

Set or clear one app&#39;s own daily AI token limit

`gregale ai app-limit --app <slug> --daily-tokens <N> <integration-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | bound app slug | required |
| `--daily-tokens <N>` | token limit, or none | required |

### ai guardrails

Set an app&#39;s per-minute limits, model allow-list and output cap

`gregale ai guardrails --app <slug> [--rpm <N>] [--tpm <N>] [--models <LIST>] [--max-output-tokens <N>] <integration-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | bound app slug | required |
| `--rpm <N>` | admitted calls per UTC minute, or none |  |
| `--tpm <N>` | tokens per UTC minute, or none |  |
| `--models <LIST>` | comma-separated models or prefixes ending in *, or none |  |
| `--max-output-tokens <N>` | per-request output-token ceiling, or none |  |

Flags you omit keep their values.

### ai prices

Show the price list, or replace it with prices set

`gregale ai prices [--price <SPEC>] [set] <integration-id>`

| Flag | Meaning | |
|---|---|---|
| `--price <SPEC>` | model=PREFIX,input=USD,output=USD[,cached=USD][,cache-write=USD][,input-image=USD][,output-image=USD] per million tokens, [,image=USD] per image, [,audio-minute=USD] per minute, [,characters=USD] per million characters, [,video-second=USD] per second of video, with model=&lt;model&gt;@&lt;resolution&gt; for one resolution, [,web-search=USD] [,file-search=USD] per use, [,code-session-gb=USD] per GB container session, [,input-audio=USD][,cached-audio=USD][,output-audio=USD] per million audio tokens (repeat; none clears) |  |

Replace with: prices set &lt;integration-id&gt; --price ...

### ai catalog

Show built-in prices for models missing from a price list

`gregale ai catalog [--model <NAME>] <provider>`

| Flag | Meaning | |
|---|---|---|
| `--model <NAME>` | show only the entry that prices this model |  |

### ai usage

Show one UTC day of token usage and cost by caller and model

`gregale ai usage [--date <YYYY-MM-DD>] <integration-id>`

| Flag | Meaning | |
|---|---|---|
| `--date <YYYY-MM-DD>` | UTC day within the last 90 days |  |

### ai users

Show one UTC day of usage by end user, or export a day range

`gregale ai users [--date <YYYY-MM-DD>] [--user <ID>] [--limit <N>] [--from <YYYY-MM-DD>] [--to <YYYY-MM-DD>] [--format <csv|ndjson>] [export] <integration-id>`

| Flag | Meaning | |
|---|---|---|
| `--date <YYYY-MM-DD>` | UTC day within the last 31 days |  |
| `--user <ID>` | show only this end user |  |
| `--limit <N>` | end users to show, 1 to 1000 |  |
| `--from <YYYY-MM-DD>` | export: first UTC day within the last 31 days (default today) |  |
| `--to <YYYY-MM-DD>` | export: last UTC day (default today) |  |
| `--format <csv|ndjson>` | export format | one of `csv` · `ndjson` |

End users come from the X-Gregale-AI-User header, most expensive first.

### ai routes

Manage AI routes, run a model canary, or show target stats

`gregale ai routes [--name <NAME>] [--format <FORMAT>] [--target <TARGET>] [--strategy <STRATEGY>] [--weights <70,30>] [--max-ttfb-ms <N>] [--position <N>] [--percent <N>] [--max-error-increase <N>] [--max-latency-increase <N>] [--min-calls <N>] [--window <N>] <list|create|set|rm|canary|stats> [<route-id>] [status|promote|stop]`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | route name for create |  |
| `--format <FORMAT>` | route format for create: native or openai (translates OpenAI chat completions) | one of `native` · `openai` |
| `--target <TARGET>` | &lt;integration-id&gt;[:&lt;model&gt;] in fallback order (repeat) |  |
| `--strategy <STRATEGY>` | first-target choice for create or set: ordered, weighted, latency or cost | one of `ordered` · `weighted` · `latency` · `cost` |
| `--weights <70,30>` | a weighted route&#39;s target weights, 0 to 100, in target order |  |
| `--max-ttfb-ms <N>` | a cost route&#39;s latency target in milliseconds |  |
| `--position <N>` | canary: the route target (2 to 5) tried first |  |
| `--percent <N>` | canary: share of calls sent to it first, 1 to 50 |  |
| `--max-error-increase <N>` | canary: roll back past this many error-rate points worse (default 5) |  |
| `--max-latency-increase <N>` | canary: roll back past this many percent slower time to first byte |  |
| `--min-calls <N>` | canary: calls needed before judging (default 50) |  |
| `--window <N>` | canary: minutes of calls to judge (default 30) |  |

List, create, update (set) or delete (rm) routes.

### ai keys

List, add (value from stdin) or remove (rm) extra provider keys

`gregale ai keys [--label <LABEL>] [--from-stdin] <list|add|rm> <integration-id> [<key-id>]`

| Flag | Meaning | |
|---|---|---|
| `--label <LABEL>` | key label for add |  |
| `--from-stdin` | read the key value from stdin for add |  |

### ai alerts

List budget alerts recorded at 80% and 100%

`gregale ai alerts [--limit <N>] <integration-id>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | alerts to show, 1 to 100 |  |

### ai replay

Resend a captured call to other models and compare the results

`gregale ai replay [--target <TARGET>] [--no-wait] [--timeout <duration>] [show] <integration-id|replay-id> [<request-id>]`

| Flag | Meaning | |
|---|---|---|
| `--target <TARGET>` | &lt;integration-id&gt;[:&lt;model&gt;] to replay on (repeat, at most 5) |  |
| `--no-wait` | print the replay without waiting for results |  |
| `--timeout <duration>` | how long to wait for results (default 3m) |  |

Compares status, tokens, cost, latency and output across integrations or models, or shows a replay.

### ai evaluate

Judge a route target against its first target on captured calls

`gregale ai evaluate [--position <N>] [--judge <INTEGRATION>] [--judge-model <MODEL>] [--sample <N>] [--max-loss-rate <N>] [--canary-percent <N>] [--no-wait] [--timeout <duration>] [list|show] <route-id|evaluation-id>`

| Flag | Meaning | |
|---|---|---|
| `--position <N>` | the route target (2 to 5) to evaluate |  |
| `--judge <INTEGRATION>` | the AI integration whose model judges the answers |  |
| `--judge-model <MODEL>` | the judge model |  |
| `--sample <N>` | captured calls to replay, 1 to 50 (default 20) |  |
| `--max-loss-rate <N>` | pass when losses and errors are at most this percent of the sample (default 20) |  |
| `--canary-percent <N>` | start a canary with this share of calls when the evaluation passes |  |
| `--no-wait` | print the evaluation without waiting for its verdict |  |
| `--timeout <duration>` | how long to wait for the verdict (default 30m) |  |

Optionally starts a canary when the target passes; also lists and shows evaluations.

### ai tokens

Manage tokens that let dev, CI or notebooks call the AI gateway

`gregale ai tokens [--app <SLUG>] [--name <NAME>] [--expires-in-hours <N>] [--integration <INTEGRATION>] <list|create|revoke> [<token-id>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | the app the token acts as |  |
| `--name <NAME>` | token name for create |  |
| `--expires-in-hours <N>` | token lifetime for create, 1 to 2160 (default 168) |  |
| `--integration <INTEGRATION>` | limit the token to this AI integration (repeat) |  |

List, create or revoke tokens; a token acts as the app.

### ai logs

Page the opt-in AI request log, or show one call&#39;s bodies

`gregale ai logs [--limit <N>] [--before <TIME>] [show] <integration-id> [<request-id>]`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | calls to show, 1 to 200 |  |
| `--before <TIME>` | show calls before this RFC 3339 time |  |

Newest first.


## channels

Manage where alerts are sent: Slack, PagerDuty or email

`gregale channels [<subcommand>]`

### channels list

List channels with their last delivery

### channels add

Add a channel; Slack URLs and PagerDuty keys are read from stdin

`gregale channels add --name <NAME> --kind <KIND> [--secret-stdin] [--pagerduty-region <REGION>] [--email <EMAIL>]`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | channel name, unique per account | required |
| `--kind <KIND>` | destination type | required; one of `slack` · `pagerduty` · `email` |
| `--secret-stdin` | read the Slack webhook URL or PagerDuty routing key from stdin |  |
| `--pagerduty-region <REGION>` | PagerDuty service region | one of `us` · `eu` |
| `--email <EMAIL>` | your account email (kind email) |  |

### channels test

Send a marked test notification

`gregale channels test <channel-id>`

### channels rm

Delete a channel

`gregale channels rm <channel-id>`


## dashboards

Save dashboards that combine metrics, log counts and SLOs across apps

`gregale dashboards [<subcommand>]`

### dashboards list

List the account&#39;s dashboards

Examples:

```sh
gregale dashboards list
```

### dashboards show

Show every widget&#39;s current value

`gregale dashboards show <dashboard-id>`

Examples:

```sh
gregale dashboards show DASHBOARD_ID
```

### dashboards get

Show a dashboard&#39;s widgets

`gregale dashboards get <dashboard-id>`

### dashboards create

Create a dashboard from a widgets JSON file

`gregale dashboards create --file <PATH> <name>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | JSON file with the widgets array (- for stdin) | required |

Examples:

```sh
gregale dashboards create checkout --file checkout.json
```

### dashboards replace

Replace a dashboard&#39;s name and widgets

`gregale dashboards replace --name <NAME> --file <PATH> <dashboard-id>`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | dashboard name | required |
| `--file <PATH>` | JSON file with the widgets array (- for stdin) | required |

### dashboards rm

Delete a dashboard

`gregale dashboards rm <dashboard-id>`


## incidents

List open and recent incidents: alerts, route monitors, status page

`gregale incidents [<subcommand>]`

### incidents list

List open incidents and those resolved in the window

`gregale incidents list [--app <SLUG>] [--open] [--since <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | keep one app&#39;s incidents |  |
| `--open` | open incidents only |  |
| `--since <DURATION>` | resolved incidents from this long ago (default 7d, at most 30d) |  |

Examples:

```sh
gregale incidents list
gregale incidents list --app shop --open
```

### incidents show

Show an incident with its evidence and notes

`gregale incidents show <incident-id>`

Examples:

```sh
gregale incidents show INCIDENT_ID
```

Evidence is what the platform has for the incident: an alert&#39;s notifications and the log context its newest one carried, a platform incident&#39;s status updates, and an app incident&#39;s changes from an hour before it opened.

### incidents ack

Acknowledge an open incident so the account sees someone is on it

`gregale incidents ack <incident-id>`

Examples:

```sh
gregale incidents ack INCIDENT_ID
```

### incidents note

Add a note to an incident; notes cannot be edited

`gregale incidents note <incident-id> <text>`

Examples:

```sh
gregale incidents note INCIDENT_ID "rolled back to v41"
```


## slos

Define SLOs and track their error budgets

See also: [`slo`](#slo).

`gregale slos [<subcommand>]`

### slos list

List the app&#39;s SLOs

`gregale slos list --app <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### slos create

Define an availability or latency SLO

`gregale slos create --app <slug> --name <NAME> --objective <PCT> [--sli <SLI>] [--latency-threshold-ms <MS>] [--window-days <DAYS>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--name <NAME>` | SLO name, unique per app | required |
| `--objective <PCT>` | objective percentage, 90 to 99.99 | required |
| `--sli <SLI>` | service level indicator | one of `availability` · `latency` |
| `--latency-threshold-ms <MS>` | latency threshold (5\|10\|25\|50\|100\|250\|500\|1000\|2000\|5000\|10000) |  |
| `--window-days <DAYS>` | rolling window | one of `7` · `30` |

### slos status

Show an SLO&#39;s attainment, budget remaining and burn rate

`gregale slos status --app <slug> <slo-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### slos rm

Delete one SLO

`gregale slos rm --app <slug> [--yes] <slo-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--yes` | accepted for consistency; rm does not prompt |  |


## synthetics

Schedule HTTP checks against an app

`gregale synthetics [<subcommand>]`

### synthetics list

List the app&#39;s synthetic checks

`gregale synthetics list --app <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### synthetics create

Schedule a GET or HEAD check on a path

`gregale synthetics create --app <slug> --name <NAME> [--path <PATH>] [--method <METHOD>] [--expect-status <N>] [--timeout-ms <MS>] [--every-minutes <MIN>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--name <NAME>` | check name, unique per app | required |
| `--path <PATH>` | path on the app (default /) |  |
| `--method <METHOD>` | HTTP method | one of `GET` · `HEAD` |
| `--expect-status <N>` | exact expected status (default any 2xx) |  |
| `--timeout-ms <MS>` | timeout including any wake, 1000-30000 |  |
| `--every-minutes <MIN>` | interval | one of `5` · `15` · `60` |

### synthetics status

Show a check&#39;s uptime, latency and recent runs

`gregale synthetics status --app <slug> <check-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### synthetics pause

Stop running a check

`gregale synthetics pause --app <slug> <check-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### synthetics resume

Resume a paused check

`gregale synthetics resume --app <slug> <check-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### synthetics rm

Delete a check and its history

`gregale synthetics rm --app <slug> [--yes] <check-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--yes` | accepted for consistency; rm does not prompt |  |


## alerts

Manage an app&#39;s alert rules

`gregale alerts [<subcommand>] [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug |  |

### alerts actions

Read or wait for automatic rollback status and evidence

`gregale alerts actions [--interactive] [--app <slug>] [--fire <UUID>] [--wait] [--timeout <duration>] [--poll-interval <duration>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose an action, inspect details, and optionally follow it |  |
| `--app <slug>` | app slug (linked app or picker in interactive mode) |  |
| `--fire <UUID>` | one production alert delivery UUID |  |
| `--wait` | wait for the selected fire to complete |  |
| `--timeout <duration>` | wait deadline (default 10m) |  |
| `--poll-interval <duration>` | poll interval (default 2s) |  |

Examples:

```sh
gregale alerts actions --app my-api --interactive
```

Includes deployment evidence and service handoffs.

### alerts list

List alert rules

`gregale alerts list --app <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### alerts add

Add an alert rule

`gregale alerts add --app <slug> --name <NAME> [--metric <METRIC>] [--comparison <OP>] [--threshold <N>] [--window-spec <WINDOW>] [--event-subscription-id <UUID>] [--edge-rule-id <ID>] [--mcp-readiness-scope <SCOPE>] [--failure-source <SOURCE>] [--channel <CHANNEL_ID>]... [--custom-metric <NAME>] [--log-query <FILTERS>] [--saved-search <NAME>] [--slo <SLO_ID>] [--synthetic-check <CHECK_ID>] [--webhook-url <URL>] [--action <ACTION>] [--post-deploy-rollback-window <duration>] [--webhook-secret-stdin] [--webhook-secret <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--name <NAME>` | rule name (3..120 chars) | required |
| `--metric <METRIC>` | metric; MCP task gauges: mcp_tasks_oldest_age_seconds, mcp_tasks_metrics_stale (includes min=0 observer heartbeat) |  |
| `--comparison <OP>` | gt\|gte\|lt\|lte\|above_baseline\|below_baseline |  |
| `--threshold <N>` | threshold value |  |
| `--window-spec <WINDOW>` | 5m\|15m\|1h\|6h\|24h\|7d\|15d |  |
| `--event-subscription-id <UUID>` | Subscription UUID for event consumer metrics |  |
| `--edge-rule-id <ID>` | edge rule for edge_rule_hits / edge_rule_log_hits (empty = all rules) |  |
| `--mcp-readiness-scope <SCOPE>` | readiness environment; omitted means all environments (immutable) |  |
| `--failure-source <SOURCE>` | any\|cron\|queue\|delayed_task\|async_invoke\|inbound_webhook |  |
| `--channel <CHANNEL_ID>` | notification channel id (repeatable); with one, the webhook is optional |  |
| `--custom-metric <NAME>` | Pushed custom metric to watch; required with --metric custom_metric |  |
| `--log-query <FILTERS>` | Log search filters to count, e.g. level=error&amp;q=timeout; with --metric log_search_matches |  |
| `--saved-search <NAME>` | Copy a saved log search&#39;s filters; with --metric log_search_matches |  |
| `--slo <SLO_ID>` | SLO id for slo_budget_burn or slo_budget_remaining_pct |  |
| `--synthetic-check <CHECK_ID>` | synthetic check id for synthetic_check_* metrics |  |
| `--webhook-url <URL>` | http(s) webhook URL (https recommended: deliveries are signed, not encrypted); required unless --channel |  |
| `--action <ACTION>` | alert action | one of `webhook` · `rollback` · `demote` · `promote` |
| `--post-deploy-rollback-window <duration>` | completed-release rollback window (0 off; up to 1h) |  |
| `--webhook-secret-stdin` | read the webhook signing secret from stdin (this or --webhook-secret is required) |  |
| `--webhook-secret <VALUE>` | webhook signing secret (prefer --webhook-secret-stdin) |  |

Examples:

```sh
printf '%s\n' "$WEBHOOK_SECRET" | gregale alerts add --app my-api --name p95-latency --metric latency_p95_ms --comparison gt --threshold 800 --window-spec 15m --webhook-url https://hooks.example.com/gregale --webhook-secret-stdin
printf '%s\n' "$WEBHOOK_SECRET" | gregale alerts add --app mcp-worker --name mcp-oldest-task-age --metric mcp_tasks_oldest_age_seconds --comparison gt --threshold 300 --window-spec 5m --webhook-url https://hooks.example.com/gregale --webhook-secret-stdin
printf '%s\n' "$WEBHOOK_SECRET" | gregale alerts add --app mcp-worker --name mcp-task-metrics-stale --metric mcp_tasks_metrics_stale --comparison gt --threshold 0 --window-spec 5m --webhook-url https://hooks.example.com/gregale --webhook-secret-stdin
```

### alerts info

Show one alert rule and its last delivery

`gregale alerts info --app <slug> <alert-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### alerts deliveries

List a rule&#39;s webhook deliveries, newest first

`gregale alerts deliveries [--app <slug>] [--interactive] [--limit <N>] [--include-test] [<alert-id>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (linked app or picker in interactive mode) |  |
| `--interactive` | choose a rule and delivery options |  |
| `--limit <N>` | max deliveries (1..100, default 20) |  |
| `--include-test` | include test deliveries |  |

Examples:

```sh
gregale alerts deliveries --app my-api --interactive
```

### alerts update

Update one alert rule

`gregale alerts update [--interactive] [--app <slug>] [--action <ACTION>] [--post-deploy-rollback-window <duration>] [--webhook-secret-stdin] [<alert-id>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a rule, edit current settings, and confirm changes |  |
| `--app <slug>` | app slug (linked app or picker in interactive mode) |  |
| `--action <ACTION>` | alert action | one of `webhook` · `rollback` · `demote` · `promote` |
| `--post-deploy-rollback-window <duration>` | completed-release rollback window (0 off; up to 1h) |  |
| `--webhook-secret-stdin` | read the replacement webhook secret from stdin |  |

Examples:

```sh
gregale alerts update --app my-api --interactive
```

### alerts rm

Delete one alert rule

`gregale alerts rm --app <slug> [--yes] <alert-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--yes` | accepted for consistency; rm does not prompt |  |

### alerts rotate-secret

Rotate the alert&#39;s webhook secret

`gregale alerts rotate-secret --app <slug> [--from-stdin] <alert-id>`

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

Create an app alert from a preset or choose one interactively

`gregale alerts preset enable [--interactive] [--app <slug>] [--webhook-url <URL>] [--channel <CHANNEL_ID>]... [--webhook-secret-stdin] [--webhook-secret <VALUE>] [--action <ACTION>] [--cooldown-minutes <N>] [--enabled] [<preset-name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a preset, review its rule, and enter a hidden signing secret |  |
| `--app <slug>` | app slug (interactive mode can use linked app or picker) |  |
| `--webhook-url <URL>` | HTTPS webhook receiver URL (required unless interactive or --channel) |  |
| `--channel <CHANNEL_ID>` | notification channel id to deliver to (repeatable) |  |
| `--webhook-secret-stdin` | read the webhook signing secret from stdin |  |
| `--webhook-secret <VALUE>` | signing secret (prefer --webhook-secret-stdin) |  |
| `--action <ACTION>` | alert action (default webhook) | one of `webhook` · `rollback` · `demote` · `promote` |
| `--cooldown-minutes <N>` | cooldown override; 0 uses the preset default |  |
| `--enabled` | rule is enabled by default; --enabled=false disables it |  |

Examples:

```sh
gregale alerts preset enable --app my-api --interactive
```


## audit-events

Search your account&#39;s audit log

`gregale audit-events [<subcommand>] [<id>]`

### audit-events list

List audit events

`gregale audit-events list [--kind-prefix <PREFIX>] [--app <SLUG>] [--app-id <UUID>] [--since <RFC3339>] [--limit <N>] [--include-anonymous] [--verbose]`

| Flag | Meaning | |
|---|---|---|
| `--kind-prefix <PREFIX>` | filter by kind prefix |  |
| `--app <SLUG>` | filter by app slug |  |
| `--app-id <UUID>` | filter by app UUID |  |
| `--since <RFC3339>` | RFC3339 lower bound |  |
| `--limit <N>` | maximum rows (1..100; default 50) |  |
| `--include-anonymous` | include rows without a subject |  |
| `--verbose` | expand stateless advisory rows |  |

### audit-events get

Show one audit event

`gregale audit-events get <id>`


## events

Publish events, preview routing, inspect deliveries and backfill

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

Preview retained events for one subscription without delivering

`gregale events replay-preview --subscription-id <UUID> --from <RFC3339> --until <RFC3339> [--after <CURSOR>] [--limit <N>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--subscription-id <UUID>` | target ordinary application subscription UUID | required |
| `--from <RFC3339>` | inclusive acceptance timestamp | required |
| `--until <RFC3339>` | exclusive acceptance timestamp | required |
| `--after <CURSOR>` | opaque continuation cursor; keep target and range unchanged |  |
| `--limit <N>` | envelopes examined per page (1..100; default 50) |  |

### events workflow-replay-preview

Preview workflow event recipients without starting runs

`gregale events workflow-replay-preview --workflow-name <NAME> --from <RFC3339> --until <RFC3339> [--after <CURSOR>] [--limit <N>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--workflow-name <NAME>` | workflow name from captured event recipient snapshots | required |
| `--from <RFC3339>` | inclusive acceptance timestamp | required |
| `--until <RFC3339>` | exclusive acceptance timestamp | required |
| `--after <CURSOR>` | opaque continuation cursor; keep app, workflow and range unchanged |  |
| `--limit <N>` | envelopes examined per page (1..100; default 50) |  |

Shows captured recipients and admission status.

### events backfill

Create a durable bounded delivery job for matching retained events

`gregale events backfill [--allow-expired] --subscription-id <UUID> --from <RFC3339> --until <RFC3339> --yes <app>`

| Flag | Meaning | |
|---|---|---|
| `--allow-expired` | explicitly bypass captured delivery age |  |
| `--subscription-id <UUID>` | target ordinary application subscription UUID | required |
| `--from <RFC3339>` | inclusive platform acceptance timestamp | required |
| `--until <RFC3339>` | exclusive platform acceptance timestamp | required |
| `--yes` | confirm that matching historical events may invoke this consumer | required |

### events workflow-backfill

Start event-triggered workflow runs for past events

`gregale events workflow-backfill --workflow-name <NAME> --from <RFC3339> --until <RFC3339> --yes <app>`

| Flag | Meaning | |
|---|---|---|
| `--workflow-name <NAME>` | target event-triggered workflow | required |
| `--from <RFC3339>` | inclusive acceptance time | required |
| `--until <RFC3339>` | exclusive acceptance time | required |
| `--yes` | confirm that matching past events may start runs | required |

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

### events retention

Inspect receipt expiry, backfill holds and account storage

`gregale events retention [--source <SOURCE>] [--app <APP>] [--window <DURATION>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--source <SOURCE>` | exact event source |  |
| `--app <APP>` | receipt application filter; storage remains account-wide |  |
| `--window <DURATION>` | expiry lookahead (1s..720h; default 24h) |  |
| `--limit <N>` | maximum sampled receipts (1..100) |  |

### events publish-app-verify

Compare a publish file with retained content without publishing

`gregale events publish-app-verify [--expected-accepted-at <RFC3339>] [--file <PATH>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--expected-accepted-at <RFC3339>` | Pin the exact accepted_at from the saved receipt |  |
| `--file <PATH>` | Original publish JSON with key, type and data |  |

Examples:

```sh
gregale events publish-app-verify my-app --file event.json --json
```

### events publish-app-status

Reconcile retained publication and consumer evidence

`gregale events publish-app-status [--expected-accepted-at <RFC3339>] [--key <KEY>] [--after <CURSOR>] [--limit <N>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--expected-accepted-at <RFC3339>` | Pin the exact accepted_at from the saved receipt |  |
| `--key <KEY>` | Original exact application producer key |  |
| `--after <CURSOR>` | Continue the recipient evidence page |  |
| `--limit <N>` | Recipients per page (1..200; default 100) |  |

Examples:

```sh
gregale events publish-app-status my-app --key order-123-created --json
```

Does not submit an event.

### events publish-app

Publish an event using a durable application-scoped producer key

`gregale events publish-app [--file <PATH>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | JSON event with a stable key, type and data |  |

Examples:

```sh
gregale events publish-app my-app --file event.json --json
```

### events publish-batch

Publish a bounded JSONL batch with per-event results

`gregale events publish-batch --file <FILE>`

| Flag | Meaning | |
|---|---|---|
| `--file <FILE>` | JSONL file or - for stdin; stable ids required | required |

### events publish

Publish one event

`gregale events publish [--id <ID>] [--source <SOURCE>] [--type <TYPE>] --data <J|@file|-> [--time <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--id <ID>` | stable event id (generated when omitted) |  |
| `--source <SOURCE>` | event source (or first positional argument) |  |
| `--type <TYPE>` | event type (or second positional argument) |  |
| `--data <J|@file|->` | JSON event data (inline \| @file \| -) | required |
| `--time <RFC3339>` | event time (RFC3339; defaults to server time) |  |

SOURCE and TYPE can be positional; the ID is generated by default.

### events subscription-pause

Pause new routing admissions for one event consumer

`gregale events subscription-pause --yes <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | confirm pausing subscription delivery | required |

### events subscription-resume

Resume one consumer with paced backlog draining

`gregale events subscription-resume [--rate <N>] --yes <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--rate <N>` | admissions per second (0..100; default 10; 0 removes pacing) |  |
| `--yes` | confirm resuming subscription delivery | required |

### events schema-rollout-preview

Preview schema version coverage and validate payload samples

`gregale events schema-rollout-preview [--version <VERSION>] [--schema <JSON|@FILE|->] [--samples <JSON|@FILE|->] [--from <RFC3339>] [--until <RFC3339>] [--retained-limit <N>] <source> <type>`

| Flag | Meaning | |
|---|---|---|
| `--version <VERSION>` | proposed or registered version |  |
| `--schema <JSON|@FILE|->` | proposed schema; omitted uses registry |  |
| `--samples <JSON|@FILE|->` | array of event data samples |  |
| `--from <RFC3339>` | retained acceptance start |  |
| `--until <RFC3339>` | exclusive retained acceptance end |  |
| `--retained-limit <N>` | 1..100 matching payloads; default 100 |  |

### events subscription-versions-status

Inspect schema version selection

`gregale events subscription-versions-status <app> <subscription-id>`

### events subscription-versions-set

Select schema versions for future events

`gregale events subscription-versions-set [--versions <v1,v2>] [--yes] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--versions <v1,v2>` | up to 16 unique version identifiers |  |
| `--yes` | confirm selection |  |

### events subscription-versions-reset

Accept all schema versions for future events

`gregale events subscription-versions-reset [--yes] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | confirm reset |  |

### events subscription-retry-status

Inspect retry policy for future event routing

`gregale events subscription-retry-status <app> <subscription-id>`

### events subscription-retry-reset

Restore legacy retry defaults for future events

`gregale events subscription-retry-reset [--yes] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | confirm retry policy reset |  |

### events subscription-circuit-status

Show consumer circuit breaker state

`gregale events subscription-circuit-status <app> <subscription-id>`

### events subscription-circuit-set

Enable automatic consumer routing circuit breaker

`gregale events subscription-circuit-set [--failure-threshold-pct <N>] [--min-samples <N>] [--window-seconds <N>] [--cooldown-seconds <N>] [--probe-successes <N>] [--recovery-max-rate <N>] [--recovery-seconds <N>] --yes <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--failure-threshold-pct <N>` | failure percentage (default 50) |  |
| `--min-samples <N>` | minimum outcomes (default 20) |  |
| `--window-seconds <N>` | observation window (default 300) |  |
| `--cooldown-seconds <N>` | cooldown (default 60) |  |
| `--probe-successes <N>` | required successes (default 3) |  |
| `--recovery-max-rate <N>` | maximum routes per second (default 10) |  |
| `--recovery-seconds <N>` | recovery duration (default 60) |  |
| `--yes` | confirm change | required |

### events subscription-circuit-disable

Disable automatic consumer routing circuit breaker

`gregale events subscription-circuit-disable --yes <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | confirm change | required |

### events subscription-circuit-reset

Close enabled circuit breaker and reset observation window

`gregale events subscription-circuit-reset --yes <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | confirm change | required |

### events subscription-retry-set

Configure bounded routing retries for future events

`gregale events subscription-retry-set [--max-delivery-age <DURATION>] [--max-attempts <N>] [--initial-backoff <DURATION>] [--max-backoff <DURATION>] [--max-retry-duration <DURATION>] [--jitter] [--yes] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--max-delivery-age <DURATION>` | wall-clock age from acceptance (up to 720h; 0 disables expiry) |  |
| `--max-attempts <N>` | 1..100 |  |
| `--initial-backoff <DURATION>` | initial delay |  |
| `--max-backoff <DURATION>` | maximum delay (up to 1h) |  |
| `--max-retry-duration <DURATION>` | attempt-time plus scheduled-delay budget (up to 7d; 0 disables) |  |
| `--jitter` | spread retries (default true) |  |
| `--yes` | confirm retry policy change |  |

### events subscription-lineage-case-backfill-pause

Pause a retained lineage diagnostic scan

`gregale events subscription-lineage-case-backfill-pause [--expected-created-at <TIMESTAMP>] <app> <subscription-id> <request-id>`

| Flag | Meaning | |
|---|---|---|
| `--expected-created-at <TIMESTAMP>` | original RFC3339 creation timestamp from the reviewed scan |  |

### events subscription-lineage-case-backfill-resume

Resume a retained lineage diagnostic scan

`gregale events subscription-lineage-case-backfill-resume [--expected-created-at <TIMESTAMP>] <app> <subscription-id> <request-id>`

| Flag | Meaning | |
|---|---|---|
| `--expected-created-at <TIMESTAMP>` | original RFC3339 creation timestamp from the reviewed scan |  |

### events subscription-lineage-case-backfill-cancel

Cancel a retained lineage diagnostic scan

`gregale events subscription-lineage-case-backfill-cancel [--expected-created-at <TIMESTAMP>] <app> <subscription-id> <request-id>`

| Flag | Meaning | |
|---|---|---|
| `--expected-created-at <TIMESTAMP>` | original RFC3339 creation timestamp from the reviewed scan |  |

### events subscription-lineage-case-backfill

Inspect latest lineage diagnostic scan

`gregale events subscription-lineage-case-backfill <app> <subscription-id>`

### events subscription-lineage-case-backfill-request

Backfill missing lineage diagnostics from retained evidence

`gregale events subscription-lineage-case-backfill-request [--request-id <UUID>] [--expected-previous-request-id <UUID>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--request-id <UUID>` | stable scan identity |  |
| `--expected-previous-request-id <UUID>` | reviewed completed, cancelled or expired scan identity |  |

### events subscription-lineage-retry-preview

Preview retained unresolved lineage cases

`gregale events subscription-lineage-retry-preview [--limit <N>] [--after <ID>] [--reason <CODE>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (default 50, maximum 200) |  |
| `--after <ID>` | exclusive outbox cursor |  |
| `--reason <CODE>` | exact case reason filter |  |

### events subscription-lineage-retry-export

Save a guarded preview as a reviewable retry request

`gregale events subscription-lineage-retry-export [--file <PATH>] [--raw] [--limit <N>] [--after <ID>] [--reason <CODE>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | new scoped retry file (never overwrite) |  |
| `--raw` | legacy unscoped API body |  |
| `--limit <N>` | selected cases (default 50, maximum 200) |  |
| `--after <ID>` | exclusive outbox cursor |  |
| `--reason <CODE>` | exact case reason filter |  |

### events subscription-lineage-retry-control-plan-export

Save fixed guarded controls from one attention page

`gregale events subscription-lineage-retry-control-plan-export [--file <PATH>] [--action <ACTION>] [--attention <WARNING>] [--status <STATE>] [--limit <N>] [--after <CURSOR>] [--timeout <DURATION>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | new private plan file (never overwrite) |  |
| `--action <ACTION>` | pause, resume or cancel |  |
| `--attention <WARNING>` | any, stalled or retention_risk (default any) |  |
| `--status <STATE>` | pending or paused |  |
| `--limit <N>` | fixed jobs (default 50, maximum 200) |  |
| `--after <CURSOR>` | attention-list cursor |  |
| `--timeout <DURATION>` | total duration (default 2m) |  |

### events subscription-lineage-retry-control-plan-apply

Apply a reviewed fixed control plan with independent results

`gregale events subscription-lineage-retry-control-plan-apply [--report <PATH>] [--resume-report <PATH>] [--file <PATH>] [--timeout <DURATION>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--report <PATH>` | new private durable execution report |  |
| `--resume-report <PATH>` | inspect prior report before retry; requires new --report |  |
| `--file <PATH>` | reviewed plan; unchanged on apply |  |
| `--timeout <DURATION>` | total duration (default 2m) |  |

### events subscription-lineage-retry-control-plan-preflight

Check reviewed commands with read-only job and receipt inspection

`gregale events subscription-lineage-retry-control-plan-preflight [--file <PATH>] [--timeout <DURATION>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | reviewed plan; unchanged on preflight |  |
| `--timeout <DURATION>` | total duration (default 2m) |  |

### events subscription-lineage-retry-control-plan-reconcile

Reconcile execution evidence with read-only receipt inspection

`gregale events subscription-lineage-retry-control-plan-reconcile [--file <PATH>] [--source-report <PATH>] [--report <PATH>] [--timeout <DURATION>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | unchanged reviewed plan |  |
| `--source-report <PATH>` | previous matching execution report |  |
| `--report <PATH>` | new private reconciled report; never overwrite |  |
| `--timeout <DURATION>` | total duration (default 2m) |  |

### events subscription-lineage-retry-jobs

List retained bulk lineage retry job summaries

`gregale events subscription-lineage-retry-jobs [--attention <WARNING>] [--status <STATE>] [--limit <N>] [--after <CURSOR>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--attention <WARNING>` | any, stalled or retention_risk (active jobs only) |  |
| `--status <STATE>` | pending, paused, completed or cancelled (default all) |  |
| `--limit <N>` | page size (default 50, maximum 200) |  |
| `--after <CURSOR>` | continuation cursor from the previous page |  |

### events subscription-lineage-retry-preflight

Check a saved retry request without changing it

`gregale events subscription-lineage-retry-preflight [--file <PATH>] [--timeout <DURATION>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | scoped retry file or legacy request body |  |
| `--timeout <DURATION>` | maximum preflight duration (default 2m) |  |

### events subscription-lineage-retry-job-create

Submit a reviewed fixed lineage retry selection

`gregale events subscription-lineage-retry-job-create [--file <PATH>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | scoped retry file or legacy request body (maximum 200) |  |

### events subscription-lineage-retry-job-controls-inspect

Inspect or compare saved control history offline

`gregale events subscription-lineage-retry-job-controls-inspect [--file <PATH>] [--compare <BASELINE_PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | saved control-history artifact (local read only) |  |
| `--compare <BASELINE_PATH>` | earlier artifact with the same job scope and filters |  |

### events subscription-lineage-retry-job-controls-export

Export a guarded private control-history snapshot

`gregale events subscription-lineage-retry-job-controls-export [--file <PATH>] [--expected-created-at <TIMESTAMP>] [--action <ACTION>] [--actor-id <UUID_OR_STORE>] [--credential-type <TYPE>] [--timeout <DURATION>] <app> <subscription-id> <request-id>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | new private JSON artifact (never overwrite) |  |
| `--expected-created-at <TIMESTAMP>` | required original job creation timestamp |  |
| `--action <ACTION>` | exact action: pause, resume or cancel |  |
| `--actor-id <UUID_OR_STORE>` | exact original actor identity |  |
| `--credential-type <TYPE>` | exact original credential type |  |
| `--timeout <DURATION>` | maximum export duration (default 2m) |  |

### events subscription-lineage-retry-job-controls

List retained immutable bulk retry command receipts

`gregale events subscription-lineage-retry-job-controls [--action <ACTION>] [--actor-id <UUID_OR_STORE>] [--credential-type <TYPE>] [--expected-created-at <TIMESTAMP>] [--limit <N>] [--after <SEQUENCE>] <app> <subscription-id> <request-id>`

| Flag | Meaning | |
|---|---|---|
| `--action <ACTION>` | exact action: pause, resume or cancel |  |
| `--actor-id <UUID_OR_STORE>` | exact original actor identity |  |
| `--credential-type <TYPE>` | exact original credential type |  |
| `--expected-created-at <TIMESTAMP>` | required original job creation timestamp |  |
| `--limit <N>` | page size (default 50, maximum 200) |  |
| `--after <SEQUENCE>` | exclusive recorded command sequence |  |

### events subscription-lineage-retry-job-pause

Pause unprocessed bulk lineage retries

`gregale events subscription-lineage-retry-job-pause [--expected-created-at <TIMESTAMP>] [--command-id <UUID>] <app> <subscription-id> <request-id>`

| Flag | Meaning | |
|---|---|---|
| `--expected-created-at <TIMESTAMP>` | original RFC3339 creation timestamp from the reviewed job |  |
| `--command-id <UUID>` | optional immutable receipt identity; reuse after timeouts |  |

### events subscription-lineage-retry-job-resume

Resume a paused bulk lineage retry job

`gregale events subscription-lineage-retry-job-resume [--expected-created-at <TIMESTAMP>] [--command-id <UUID>] <app> <subscription-id> <request-id>`

| Flag | Meaning | |
|---|---|---|
| `--expected-created-at <TIMESTAMP>` | original RFC3339 creation timestamp from the reviewed job |  |
| `--command-id <UUID>` | optional immutable receipt identity; reuse after timeouts |  |

### events subscription-lineage-retry-job-cancel

Cancel unprocessed lineage retries

`gregale events subscription-lineage-retry-job-cancel [--command-id <UUID>] [--expected-created-at <TIMESTAMP>] <app> <subscription-id> <request-id>`

| Flag | Meaning | |
|---|---|---|
| `--command-id <UUID>` | optional reusable UUID for an immutable control receipt |  |
| `--expected-created-at <TIMESTAMP>` | original RFC3339 creation timestamp from the reviewed job |  |

### events subscription-lineage-retry-job-export

Export current guards for remaining cases from a finished job

`gregale events subscription-lineage-retry-job-export [--file <PATH>] [--raw] [--expected-created-at <TIMESTAMP>] [--outcomes <STATES>] [--timeout <DURATION>] <app> <subscription-id> <source-request-id>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | new scoped retry file (never overwrite) |  |
| `--raw` | legacy unscoped API body without provenance |  |
| `--expected-created-at <TIMESTAMP>` | required reviewed source creation timestamp |  |
| `--outcomes <STATES>` | original outcomes (default unresolved,conflict,changed,unavailable; cancelled also allowed) |  |
| `--timeout <DURATION>` | maximum export duration (default 2m) |  |

### events subscription-lineage-retry-job-watch

Watch a retained bulk retry job and summarize outcomes

`gregale events subscription-lineage-retry-job-watch [--timeout <DURATION>] [--poll-interval <DURATION>] [--expected-created-at <TIMESTAMP>] <app> <subscription-id> <request-id>`

| Flag | Meaning | |
|---|---|---|
| `--timeout <DURATION>` | maximum watch duration (default 10m) |  |
| `--poll-interval <DURATION>` | delay between reads (default 2s, between 1s and 1h) |  |
| `--expected-created-at <TIMESTAMP>` | optional reviewed original RFC3339 creation timestamp |  |

### events subscription-lineage-retry-job

Inspect durable lineage retry progress

`gregale events subscription-lineage-retry-job <app> <subscription-id> <request-id>`

### events subscription-lineage-cases

List retained lineage repair cases

`gregale events subscription-lineage-cases [--limit <N>] [--after <ID>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (default 50, maximum 200) |  |
| `--after <ID>` | exclusive outbox cursor |  |

### events subscription-lineage-retry

Retry one reviewed lineage case

`gregale events subscription-lineage-retry [--request-id <UUID>] [--expected-revision <N>] [--expected-diagnostic-id <UUID>] <app> <subscription-id> <outbox-id>`

| Flag | Meaning | |
|---|---|---|
| `--request-id <UUID>` | stable request identity |  |
| `--expected-revision <N>` | reviewed case revision |  |
| `--expected-diagnostic-id <UUID>` | incarnation guard for a backfilled case |  |

### events subscription-execution-reconciliation

Inspect durable execution coverage reconciliation

`gregale events subscription-execution-reconciliation <app> <subscription-id>`

### events subscription-reconcile-execution

Request a forward-only execution coverage boundary

`gregale events subscription-reconcile-execution <app> <subscription-id>`

### events subscription-execution-health

Inspect executions, handler outcomes, and active retry-job health

`gregale events subscription-execution-health [--window <5m|15m|1h|6h|24h>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--window <5m|15m|1h|6h|24h>` | attempt and completion window (default 5m) |  |

### events delivery-health-alerts

Inspect, replay, or acknowledge a local delivery health alert queue

`gregale events delivery-health-alerts [--checkpoint <PATH>] [--replay] [--ack-alerts <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--checkpoint <PATH>` | delivery health checkpoint |  |
| `--replay` | replay pending JSON lines |  |
| `--ack-alerts <PATH>` | exact acknowledgements JSON |  |

### events delivery-retry

Retry a deferred delivery using its current secret token

`gregale events delivery-retry [--request-file <PATH>] <app> <invocation-id>`

| Flag | Meaning | |
|---|---|---|
| `--request-file <PATH>` | protected JSON file containing token and non-sensitive reason |  |

### events delivery-dead-letter

Dead-letter a deferred delivery using its current secret token

`gregale events delivery-dead-letter [--request-file <PATH>] <app> <invocation-id>`

| Flag | Meaning | |
|---|---|---|
| `--request-file <PATH>` | protected JSON file containing token and non-sensitive reason |  |

### events deadline-alert-investigate

Inspect deadline identities and blockers for a saved Go alert

`gregale events deadline-alert-investigate [--alert-file <PATH>] [--limit <N>] [--cursor <CURSOR>] [--explain] <app>`

| Flag | Meaning | |
|---|---|---|
| `--alert-file <PATH>` | single Go/CLI lifecycle alert JSON object (maximum 64 KiB) |  |
| `--limit <N>` | current recipients per page (default 50; 1..100, or 1..50 with explain) |  |
| `--cursor <CURSOR>` | opaque next_cursor for the same app, alert selection and limit |  |
| `--explain` | read acceptance-guarded blocker explanations for this page |  |

### events subscription-delivery-health

Inspect routing, executions, retry jobs, and pending blockers

`gregale events subscription-delivery-health [--deadline-expired-ratio-threshold <PERCENT>] [--deadline-expired-ratio-recovery <PERCENT>] [--deadline-expired-ratio-min-recipients <N>] [--deadline-window <DURATION>] [--deadline-upcoming-threshold <N>] [--deadline-expired-threshold <N>] [--deadline-upcoming-recovery <N>] [--deadline-expired-recovery <N>] [--deadline-upcoming-reason <REASON=N>] [--deadline-expired-reason <REASON=N>] [--deadline-upcoming-reason-recovery <REASON=N>] [--deadline-expired-reason-recovery <REASON=N>] [--deadline-sustain <DURATION>] [--deadline-recovery-sustain <DURATION>] [--renewal-rejection-threshold <FRACTION>] [--lease-expiry-rate-threshold <RATE>] [--renewal-min-samples <N>] [--processing-window-sustain <DURATION>] [--processing-metrics-recording-since <RFC3339>] [--processing-oldest-threshold <DURATION>] [--processing-lease-risk-threshold <DURATION>] [--processing-expired-threshold <DURATION>] [--lineage-stalled-threshold <N>] [--lineage-retention-risk-threshold <N>] [--lineage-paused-threshold <DURATION>] [--receipt-budget-full-threshold <DURATION>] [--receipt-budget-remaining-threshold <N>] [--receipt-budget-low-sustain <DURATION>] [--receipt-budget-recovery-threshold <N>] [--receipt-budget-recovery-sustain <DURATION>] [--window <WINDOW>] [--watch] [--alerts-json] [--confirmation-progress] [--interval <DURATION>] [--timeout <DURATION>] [--max-age <DURATION>] [--oldest-pending-threshold <DURATION>] [--retrying-threshold <N>] [--dead-letters-threshold <N>] [--pause-threshold <DURATION>] [--circuit-threshold <DURATION>] [--capacity-threshold <DURATION>] [--waiting-reason-count <REASON=N>] [--waiting-reason-duration <REASON=DURATION>] [--checkpoint <PATH>] [--resume] [--ack-inbox <PATH>] [--slo-routing-latency <DURATION>] [--slo-completion-latency <DURATION>] [--slo-retry-rate <N>] [--slo-dead-letter-rate <N>] [--slo-routing-failure-budget <PERCENT>] [--slo-min-samples <N>] [--slo-sustain <DURATION>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--deadline-expired-ratio-threshold <PERCENT>` | expired percentage warning, 0.01..100 (two decimal places); zero disables |  |
| `--deadline-expired-ratio-recovery <PERCENT>` | inclusive percentage below warning; default warning minus 0.01 percentage points |  |
| `--deadline-expired-ratio-min-recipients <N>` | required minimum pending recipients with deadlines, 1..9007199254740991 |  |
| `--deadline-window <DURATION>` | upcoming horizon, whole milliseconds 100ms-24h; required for deadline alerts |  |
| `--deadline-upcoming-threshold <N>` | upcoming deadline count warning; zero disables |  |
| `--deadline-expired-threshold <N>` | all expired deadline count warning; zero disables |  |
| `--deadline-upcoming-recovery <N>` | inclusive recovery count below upcoming warning; default warning minus one |  |
| `--deadline-expired-recovery <N>` | inclusive recovery count below expired warning; default warning minus one |  |
| `--deadline-upcoming-reason <REASON=N>` | repeatable upcoming reason count warning |  |
| `--deadline-expired-reason <REASON=N>` | repeatable expired reason count warning |  |
| `--deadline-upcoming-reason-recovery <REASON=N>` | repeatable inclusive recovery count for enabled upcoming reason |  |
| `--deadline-expired-reason-recovery <REASON=N>` | repeatable inclusive recovery count for enabled expired reason |  |
| `--deadline-sustain <DURATION>` | deadline warning confirmation, 100ms-24h; default 1m |  |
| `--deadline-recovery-sustain <DURATION>` | deadline recovery confirmation, 100ms-24h; default 1m |  |
| `--renewal-rejection-threshold <FRACTION>` | recorded renewal rejection fraction threshold (0..1) |  |
| `--lease-expiry-rate-threshold <RATE>` | recorded lease expiry transitions per second |  |
| `--renewal-min-samples <N>` | minimum recorded renewal attempts (default 10) |  |
| `--processing-window-sustain <DURATION>` | breach and recovery confirmation duration (at least 1m) |  |
| `--processing-metrics-recording-since <RFC3339>` | manual recording coverage override; default uses verified server inventory |  |
| `--processing-oldest-threshold <DURATION>` | oldest active processing attempt age; confirms for 30s |  |
| `--processing-lease-risk-threshold <DURATION>` | sustained nonzero leases in their final third (capped at 30s) |  |
| `--processing-expired-threshold <DURATION>` | sustained expired processing leases |  |
| `--lineage-stalled-threshold <N>` | stalled pending retry-job count threshold |  |
| `--lineage-retention-risk-threshold <N>` | active retry-job retention-risk count threshold |  |
| `--lineage-paused-threshold <DURATION>` | sustained nonzero paused retry-job backlog |  |
| `--receipt-budget-full-threshold <DURATION>` | sustained full capacity-command receipt budget |  |
| `--receipt-budget-remaining-threshold <N>` | warn below N remaining receipt slots (1..128; zero disables) |  |
| `--receipt-budget-low-sustain <DURATION>` | confirm low remaining slots (default 30s when enabled) |  |
| `--receipt-budget-recovery-threshold <N>` | opt-in recovery at N remaining slots; N must be at least the warning threshold |  |
| `--receipt-budget-recovery-sustain <DURATION>` | fresh recovery confirmation (default 30s when recovery enabled) |  |
| `--window <WINDOW>` | 5m\|15m\|1h\|6h\|24h |  |
| `--watch` | watch configured local health thresholds |  |
| `--alerts-json` | lifecycle alert JSON lines on stderr |  |
| `--confirmation-progress` | live delivery-health confirmation details on text stdout; omit with --json |  |
| `--interval <DURATION>` | poll interval (100ms-1m) |  |
| `--timeout <DURATION>` | watch deadline (up to 24h) |  |
| `--max-age <DURATION>` | maximum sample age |  |
| `--oldest-pending-threshold <DURATION>` | pending acceptance age threshold |  |
| `--retrying-threshold <N>` | retained retrying execution threshold |  |
| `--dead-letters-threshold <N>` | retained dead-letter execution threshold |  |
| `--pause-threshold <DURATION>` | subscription pause duration threshold |  |
| `--circuit-threshold <DURATION>` | locally sampled circuit restriction duration |  |
| `--capacity-threshold <DURATION>` | locally sampled capacity wait duration |  |
| `--waiting-reason-count <REASON=N>` | repeatable pending reason count threshold |  |
| `--waiting-reason-duration <REASON=DURATION>` | repeatable sustained nonzero reason threshold |  |
| `--checkpoint <PATH>` | persist delivery health watch state |  |
| `--resume` | resume checkpoint with fresh evidence |  |
| `--ack-inbox <PATH>` | live acknowledgement JSON batch; requires checkpoint |  |
| `--slo-routing-latency <DURATION>` | recorded routing p95 target |  |
| `--slo-completion-latency <DURATION>` | successful completion p95 target |  |
| `--slo-retry-rate <N>` | routing retries/second target |  |
| `--slo-dead-letter-rate <N>` | dead letters/second target |  |
| `--slo-routing-failure-budget <PERCENT>` | terminal routing failure budget |  |
| `--slo-min-samples <N>` | minimum window samples |  |
| `--slo-sustain <DURATION>` | sustained fresh breach duration |  |

### events subscription-health

Inspect consumer backlog, routing rates, latency, and pause duration

`gregale events subscription-health [--window <WINDOW>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--window <WINDOW>` | 5m\|15m\|1h\|6h\|24h |  |

### events subscription-status

Inspect delivery pause, pacing, and oldest waiting event

`gregale events subscription-status <app> <subscription-id>`

### events subscription-capacity

Set a lower live-delivery limit for one subscription

`gregale events subscription-capacity [--command-id <UUID>] [--expected-max-pending <N>] [--expected-plan-max-pending <N>] [--max-pending <N>] [--yes] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--command-id <UUID>` | retain original outcome; reuse unchanged on uncertain response |  |
| `--expected-max-pending <N>` | reviewed saved setting; use with expected-plan-max-pending |  |
| `--expected-plan-max-pending <N>` | reviewed plan ceiling; use with expected-max-pending |  |
| `--max-pending <N>` | pending plus dispatching ceiling; 0 restores plan default |  |
| `--yes` | confirm changing subscription capacity |  |

### events subscription-capacity-commands

List a page of retained original capacity-command receipts

`gregale events subscription-capacity-commands [--outcome <FILTER>] [--limit <N>] [--after <CURSOR>] [--timeout <DURATION>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--outcome <FILTER>` | any (default), applied or unchanged |  |
| `--limit <N>` | receipts per page (default 25; maximum 100) |  |
| `--after <CURSOR>` | next_after for the same app, subscription and outcome filter |  |
| `--timeout <DURATION>` | total read deadline (default 5s; maximum 1m) |  |

### events subscription-capacity-command

Read retained capacity-command evidence without changing anything

`gregale events subscription-capacity-command [--timeout <DURATION>] <app> <subscription-id> <command-id>`

| Flag | Meaning | |
|---|---|---|
| `--timeout <DURATION>` | total read deadline (default 5s; maximum 1m) |  |

### events subscription-capacity-preview

Preview delivery capacity and command receipt budget

`gregale events subscription-capacity-preview [--max-pending <N>] [--timeout <DURATION>] [--max-age <DURATION>] <app> <subscription-id>`

| Flag | Meaning | |
|---|---|---|
| `--max-pending <N>` | proposed live ceiling; 0 restores plan default |  |
| `--timeout <DURATION>` | total read deadline (default 5s) |  |
| `--max-age <DURATION>` | maximum pressure snapshot age (default 30s) |  |

Does not change controls.

### events recovery-preview

Preview a bounded selection of failed event consumers

`gregale events recovery-preview [--mode <MODE>] [--parent-job <UUID>] [--request-id <UUID>] [--outcome <OUTCOME>] [--subscription-id <ID>] [--event-source <SOURCE>] [--event-type <TYPE>] [--failure-code <CODE>] [--min-age <DURATION>] [--include-non-retryable] [--protect-receipts] [--rate <N>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--mode <MODE>` | routing (default) or execution recovery; parent-job defaults to execution |  |
| `--parent-job <UUID>` | select saved failures from one terminal execution recovery |  |
| `--request-id <UUID>` | stable request UUID; required for child recovery creation |  |
| `--outcome <OUTCOME>` | execution outcome: failed or dead_letter |  |
| `--subscription-id <ID>` | filter by captured consumer identifier |  |
| `--event-source <SOURCE>` | filter by exact event source |  |
| `--event-type <TYPE>` | filter by exact event type |  |
| `--failure-code <CODE>` | filter by failure classification |  |
| `--min-age <DURATION>` | minimum failure age in whole seconds |  |
| `--include-non-retryable` | include failures classified as non-retryable |  |
| `--protect-receipts` | hold pending receipts until admission or job expiry; preview creates no holds |  |
| `--rate <N>` | maximum retries per second (1..100; default 10) |  |

### events recovery-create

Create a durable bulk recovery job

`gregale events recovery-create [--mode <MODE>] [--parent-job <UUID>] [--request-id <UUID>] [--outcome <OUTCOME>] [--subscription-id <ID>] [--event-source <SOURCE>] [--event-type <TYPE>] [--failure-code <CODE>] [--min-age <DURATION>] [--include-non-retryable] [--protect-receipts] [--rate <N>] [--reason <TEXT>] --yes <app>`

| Flag | Meaning | |
|---|---|---|
| `--mode <MODE>` | routing (default) or execution recovery; parent-job defaults to execution |  |
| `--parent-job <UUID>` | select saved failures from one terminal execution recovery |  |
| `--request-id <UUID>` | stable request UUID; required for child recovery creation |  |
| `--outcome <OUTCOME>` | execution outcome: failed or dead_letter |  |
| `--subscription-id <ID>` | filter by captured consumer identifier |  |
| `--event-source <SOURCE>` | filter by exact event source |  |
| `--event-type <TYPE>` | filter by exact event type |  |
| `--failure-code <CODE>` | filter by failure classification |  |
| `--min-age <DURATION>` | minimum failure age in whole seconds |  |
| `--include-non-retryable` | include failures classified as non-retryable |  |
| `--protect-receipts` | hold pending receipts until admission or job expiry; preview creates no holds |  |
| `--rate <N>` | maximum retries per second (1..100; default 10) |  |
| `--reason <TEXT>` | optional operator reason (at most 512 bytes) |  |
| `--yes` | confirm creating a recovery job | required |

### events recovery-preflight

Assess frozen recovery eligibility and optimistic timing

`gregale events recovery-preflight <job-id>`

### events recovery-notifications

Inspect recovery notification capture and receiver acknowledgements

`gregale events recovery-notifications <job-id>`

### events notification-retry-reconcile

Read saved plan decisions and original-generation delivery outcomes

`gregale events notification-retry-reconcile [--file <PATH>] [--wait] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | Prepared retry plan JSON |  |
| `--wait` | Wait while all unresolved outcomes are pending |  |
| `--timeout <DURATION>` | Overall wait deadline (default 5m); requires --wait |  |

Examples:

```sh
gregale events notification-retry-reconcile --file plan.json --wait --timeout 5m
```

### events notification-retry-plan

Preview notification retries across app jobs and save a plan

`gregale events notification-retry-plan [--file <PATH>] [--output <NEW_PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | Selection JSON with app_id and stable per-job requests |  |
| `--output <NEW_PATH>` | Create a new private plan file; never overwrite |  |

Covers up to ten app jobs.

### events notification-retry-apply

Apply a prepared plan and emit a JSON decision receipt

`gregale events notification-retry-apply [--file <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | Prepared plan; preserve request IDs when resuming |  |

### events notification-retry-backlog

Inspect notification retry requests across retained app recovery jobs

`gregale events notification-retry-backlog [--status <STATUS,...>] [--page-size <N>] [--cursor <CURSOR>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--status <STATUS,...>` | Request statuses (default failed,pending,inconclusive) |  |
| `--page-size <N>` | Jobs inspected per page (1..10; default 5) |  |
| `--cursor <CURSOR>` | Continue to the next job page |  |

Examples:

```sh
gregale events notification-retry-backlog my-app --status failed,pending,inconclusive --json
```

### events recovery-notification-retry-history

Inspect saved retry decisions and current delivery status

`gregale events recovery-notification-retry-history [--status <STATUS,...>] [--request-id <UUID>] [--wait] [--timeout <DURATION>] <job-id>`

| Flag | Meaning | |
|---|---|---|
| `--status <STATUS,...>` | Filter list by succeeded, failed, pending, or inconclusive; incompatible with --request-id |  |
| `--request-id <UUID>` | Show one saved retry request in detail |  |
| `--wait` | Wait for the requested retry generations; requires --request-id |  |
| `--timeout <DURATION>` | Maximum wait duration (default 5m); requires --wait |  |

Examples:

```sh
gregale events recovery-notification-retry-history JOB_ID --status failed,inconclusive --json
gregale events recovery-notification-retry-history JOB_ID --request-id REQUEST_ID --wait --timeout 5m --json
```

### events recovery-notification-retry-preview

Preview eligible recovery notification receivers

`gregale events recovery-notification-retry-preview <job-id>`

### events recovery-notification-retry

Retry an explicit recovery notification receiver selection

`gregale events recovery-notification-retry --request-file <PATH> <job-id>`

| Flag | Meaning | |
|---|---|---|
| `--request-file <PATH>` | JSON request with stable request_id and explicit targets | required |

### events recovery-health

Inspect active recovery progress and expiry risk

`gregale events recovery-health <app>`

### events recovery-history

Inspect recovery control audit history

`gregale events recovery-history [--after <ID>] [--limit <N>] <job-id>`

| Flag | Meaning | |
|---|---|---|
| `--after <ID>` | last entry ID from the previous page |  |
| `--limit <N>` | entries per page (1..100; default 100) |  |

### events recovery-list

Discover retained recovery jobs

`gregale events recovery-list [--state <STATE>] [--mode <MODE>] [--subscription-id <ID>] [--created-after <TIME>] [--created-before <TIME>] [--cursor <CURSOR>] [--limit <N>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--state <STATE>` | filter by recovery state |  |
| `--mode <MODE>` | routing, execution or workflow |  |
| `--subscription-id <ID>` | selected or captured subscription |  |
| `--created-after <TIME>` | exclusive creation lower bound (RFC3339) |  |
| `--created-before <TIME>` | exclusive creation upper bound (RFC3339) |  |
| `--cursor <CURSOR>` | next cursor from the previous page |  |
| `--limit <N>` | jobs per page (1..50; default 50) |  |

### events recovery-status

Inspect recovery admission progress and execution outcomes

`gregale events recovery-status <job-id>`

### events recovery-items

Inspect recovery items, replay identities, and execution outcomes

`gregale events recovery-items [--after <POSITION>] [--limit <N>] <job-id>`

| Flag | Meaning | |
|---|---|---|
| `--after <POSITION>` | last item position from the previous page |  |
| `--limit <N>` | items per page (1..100; default 100) |  |

### events recovery-pause

Pause further recovery admissions

`gregale events recovery-pause [--reason <TEXT>] --yes <job-id>`

| Flag | Meaning | |
|---|---|---|
| `--reason <TEXT>` | optional operator reason (at most 512 bytes) |  |
| `--yes` | confirm pause | required |

### events recovery-resume

Resume the same frozen recovery selection

`gregale events recovery-resume [--reason <TEXT>] --yes <job-id>`

| Flag | Meaning | |
|---|---|---|
| `--reason <TEXT>` | optional operator reason (at most 512 bytes) |  |
| `--yes` | confirm resume | required |

### events recovery-rate

Change recovery admission rate without resetting its budget

`gregale events recovery-rate --rate <N> [--reason <TEXT>] --yes <job-id>`

| Flag | Meaning | |
|---|---|---|
| `--rate <N>` | items per second (1..100) | required |
| `--reason <TEXT>` | optional operator reason (at most 512 bytes) |  |
| `--yes` | confirm rate change | required |

### events recovery-cancel

Cancel remaining bulk recovery retries

`gregale events recovery-cancel [--reason <TEXT>] --yes <job-id>`

| Flag | Meaning | |
|---|---|---|
| `--reason <TEXT>` | optional operator reason (at most 512 bytes) |  |
| `--yes` | confirm cancelling remaining retries | required |

### events backlog

Discover waiting event consumers and recipient counts

`gregale events backlog [--app <APP>] [--subscription-id <ID>] [--consumer-kind <KIND>] [--origin <ORIGIN>] [--state <STATE>] [--waiting-reason <REASON>] [--capacity-scope <SCOPE>] [--min-age <DURATION>] [--after <CURSOR>] [--consumers-after <CURSOR>] [--limit <N>] [--consumer-limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <APP>` | filter by owned app slug |  |
| `--subscription-id <ID>` | filter by captured or backfilled recipient identifier |  |
| `--consumer-kind <KIND>` | application or workflow |  |
| `--origin <ORIGIN>` | acceptance or backfill |  |
| `--state <STATE>` | pending or processing |  |
| `--waiting-reason <REASON>` | filter by waiting reason, e.g. ordering_blocked |  |
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

List manifest subscriptions and keyed ordering status

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

Retry one terminal pre-invocation recipient failure

`gregale events replay [--expected-accepted-at <RFC3339>] [--expected-generation <N>] [--allow-expired] --event-id <ID> --event-source <SOURCE> --subscription-id <ID> <app>`

| Flag | Meaning | |
|---|---|---|
| `--expected-accepted-at <RFC3339>` | guard original acceptance in replay transaction |  |
| `--expected-generation <N>` | guard routing generation; requires expected-accepted-at |  |
| `--allow-expired` | explicitly bypass captured delivery age |  |
| `--event-id <ID>` | published event id | required |
| `--event-source <SOURCE>` | published event source | required |
| `--subscription-id <ID>` | failed subscription id | required |

Uses the event identity and subscription ID from events deliveries.

### events replay-retryable

Retry a bounded batch of retryable terminal failures

`gregale events replay-retryable [--event-source <SOURCE>] [--event-id <ID>] [--limit <N>] --yes <app>`

| Flag | Meaning | |
|---|---|---|
| `--event-source <SOURCE>` | limit replay to one published event source |  |
| `--event-id <ID>` | limit replay to one published event |  |
| `--limit <N>` | max recipients to requeue (1..100) |  |
| `--yes` | confirm requeueing retryable event recipients | required |

Pass --event-source and --event-id together to filter.


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
gregale apps rm my-api --dry-run
gregale apps rm my-api --yes
```

### apps ls

Alias for the default list action

### apps rm

Delete an app; restore it within the deletion grace window

`gregale apps rm [--dry-run] [--yes] [--quiet] [-q] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview deletion without changing resources |  |
| `--yes` | confirm deletion without prompting; required in non-interactive and JSON mode |  |
| `--quiet` | confirm deletion without prompting (alias of --yes) |  |
| `-q` | confirm deletion without prompting (alias of --yes) |  |

Also accepted as delete.

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

Inspect an app, change settings, or manage its runtime

`gregale app <slug> [<subcommand>] [--concurrency] [--environment <SLUG>] [--visibility <public|internal>] [--size <SIZE>] [--profile <SIZE>] [--ram <MB>] [--cpu-millicores <250|500|1000>] [--max-concurrency <N>] [--concurrency-overflow <value>] [--max-queue-depth <N>] [--max-queue-wait <DURATION>] [--max-queue-wait-ms <N>] [--wake-max-queue-depth <N>] [--wake-max-queue-wait-seconds <N>] [--idle <SEC>] [--request-timeout <SEC>] [--function-concurrency <N>] [--revision-pin-ttl <DURATION>] [--require-signed <true|false>] [--security-policy <value>] [--basic-user <USER>] [--basic-pass <PASS>] [--min <N>] [--autoscale-target-rps <N>] [--autoscale-target-cpu-pct <1..100>] [--warm-snapshot] [--no-warm-snapshot] [--warm-snapshot-min-requests <N>] [--warm-snapshot-min-ms <MS>] [--warm-pool-size <N>] [--eviction-priority <best_effort|reserved>] [--require-authn] [--no-require-authn] [--platform-tenant-required] [--no-platform-tenant-required] [--maintenance] [--no-maintenance] [--streaming-enabled] [--no-streaming-enabled] [--websocket-enabled] [--no-websocket] [--route-metrics] [--no-route-metrics] [--consumer-auth-mode <optional|required>] [--only-declared-routes] [--no-only-declared-routes] [--head-wakes[=true|false]] [--crawler-policy <wake|cached|block>] [--pre-auth <off|observe|enforce>] [--pre-auth-rps <N>] [--pre-auth-burst <N>] [--release-policy <safe|fast|immediate>] [--release-probe-paths <PATH,PATH>] [--health-path <PATH>] [--health-path-wakes] [--no-health-path-wakes] [--app-protocol <http1|http2|grpc>] [--public-auth <open|bearer|basic|ip_allowlist|internal_only>] [--ip-allowlist <CIDR>]... [--overflow-node <NAME>]`

Without update flags, shows the app. Omitted settings stay unchanged.

RAM/CPU: use --size SIZE (--profile SIZE after the command is deprecated). Connection: use --profile NAME before the command.

| Flag | Meaning | |
|---|---|---|
| `--concurrency` | print only the per-VM concurrency bound for the app&#39;s plan |  |
| `--environment <SLUG>` | read or edit this project environment |  |
| `--visibility <public|internal>` | set public edge exposure | one of `public` · `internal` |
| `--size <SIZE>` | set RAM/CPU resource size | one of `micro` · `small` · `medium` · `large` · `xlarge` |
| `--profile <SIZE>` | deprecated: use --size |  |
| `--ram <MB>` | set RAM in MB |  |
| `--cpu-millicores <250|500|1000>` | set sustained CPU allowance | one of `250` · `500` · `1000` |
| `--max-concurrency <N>` | set the maximum instances (replicas) the app scales to |  |
| `--concurrency-overflow <value>` | set saturated concurrency behavior | one of `queue` · `drop` |
| `--max-queue-depth <N>` | set maximum queued requests (0 uses plan default) |  |
| `--max-queue-wait <DURATION>` | set queue wait, e.g. 750ms (0 uses plan default); excludes --max-queue-wait-ms |  |
| `--max-queue-wait-ms <N>` | set queue wait in milliseconds (0 uses plan default); excludes --max-queue-wait |  |
| `--wake-max-queue-depth <N>` | set per-app cold-wake waiter cap |  |
| `--wake-max-queue-wait-seconds <N>` | set per-app cold-wake wait budget |  |
| `--idle <SEC>` | set idle timeout in seconds |  |
| `--request-timeout <SEC>` | set per-app request timeout in seconds |  |
| `--function-concurrency <N>` | set requests one Node/Python function worker serves at once |  |
| `--revision-pin-ttl <DURATION>` | revision pin lifetime; 0 disables, maximum 168h |  |
| `--require-signed <true|false>` | require signed images on deploy | one of `true` · `false` |
| `--security-policy <value>` | deploy posture policy | one of `off` · `warn` · `enforce` |
| `--basic-user <USER>` | basic-auth username (required with --public-auth=basic) |  |
| `--basic-pass <PASS>` | basic-auth password (required with --public-auth=basic) |  |
| `--min <N>` | set minimum warm instances (Pro/Scale; 0 scales to zero) |  |
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
| `--pre-auth <off|observe|enforce>` | per-source pre-auth limit mode; enforce first prints the 24h readiness check | one of `off` · `observe` · `enforce` |
| `--pre-auth-rps <N>` | pre-auth requests per second per source |  |
| `--pre-auth-burst <N>` | pre-auth burst per source |  |
| `--release-policy <safe|fast|immediate>` | default rollout for production releases | one of `safe` · `fast` · `immediate` |
| `--release-probe-paths <PATH,PATH>` | GET paths (at most 3) the default rollout probe checks instead of /; empty restores / |  |
| `--health-path <PATH>` | set the monitor-facing health path |  |
| `--health-path-wakes` | allow health probes to wake the app |  |
| `--no-health-path-wakes` | answer health probes without waking the app |  |
| `--app-protocol <http1|http2|grpc>` | set the wire-protocol selector | one of `http1` · `http2` · `grpc` |
| `--public-auth <open|bearer|basic|ip_allowlist|internal_only>` | set public URL authentication; internal_only admits Gregale internal services, ip_allowlist is Pro+ | one of `open` · `bearer` · `basic` · `ip_allowlist` · `internal_only` |
| `--ip-allowlist <CIDR>` | allow a CIDR through the public URL; repeat for multiple ranges; requires --public-auth ip_allowlist |  |
| `--overflow-node <NAME>` | set or clear the preferred overflow compute node |  |

Examples:

```sh
gregale app my-api
gregale app my-api scale --interactive
gregale app my-api --environment staging --ram 512
```

### app health

Check serving health and readiness

`gregale app <slug> health [--check] [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] [--history] [--limit <N>] [--before <CURSOR>] [--environment <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--check` | single read; exit 0 only for a fresh healthy default-scope assessment; excludes wait and history options |  |
| `--wait` | wait for a fresh healthy default-scope assessment |  |
| `--timeout <DURATION>` | maximum wait (default 2m; requires --wait) |  |
| `--poll-interval <DURATION>` | poll interval (default 2s; 1s to 1h; requires --wait) |  |
| `--history` | show recorded health changes; excludes --wait |  |
| `--limit <N>` | history page size |  |
| `--before <CURSOR>` | history cursor |  |
| `--environment <SLUG>` | assess this project environment instead of the default scope; excludes history options |  |

Examples:

```sh
gregale app my-api health --wait --timeout 2m
```

### app changes

List recorded changes and health transitions, newest first

`gregale app <slug> changes [--since <RFC3339>] [--until <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--since <RFC3339>` | window start, RFC3339 (default: 24h before until) |  |
| `--until <RFC3339>` | window end, RFC3339 (default: now) |  |

Examples:

```sh
gregale app my-api changes
gregale app my-api changes --since 2026-10-08T12:00:00Z --json
```

### app scale

Preview or change resources and settings

`gregale app <slug> scale [--interactive] [--plan] [--out <PATH>] [--apply <PATH>] [--confirm] [--environment <SLUG>] [--size <SIZE>] [--profile <SIZE>] [--ram <MB>] [--cpu-millicores <250|500|1000>] [--max-concurrency <N>] [--concurrency-overflow <POLICY>] [--max-queue-depth <N>] [--max-queue-wait <DURATION>] [--max-queue-wait-ms <MS>] [--wake-max-queue-depth <N>] [--wake-max-queue-wait-seconds <SECONDS>] [--idle <SECONDS>] [--request-timeout <SECONDS>] [--min <N>] [--autoscale-target-rps <N>] [--autoscale-target-cpu-pct <1..100>] [--warm-snapshot] [--no-warm-snapshot] [--warm-snapshot-min-requests <N>] [--warm-snapshot-min-ms <MS>] [--warm-pool-size <N>] [--require-authn] [--no-require-authn] [--head-wakes[=true|false]] [--crawler-policy <POLICY>] [--health-path <PATH>] [--health-path-wakes] [--no-health-path-wakes] [--app-protocol <PROTOCOL>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose settings with a preview and confirmation; accepts only --environment |  |
| `--plan` | show changes and supported plan effects without applying them |  |
| `--out <PATH>` | write a reusable plan JSON to a new file (requires --plan) |  |
| `--apply <PATH>` | apply a saved scale plan JSON file |  |
| `--confirm` | confirm applying the saved plan (requires --apply) |  |
| `--environment <SLUG>` | edit desired workload settings in a project environment |  |
| `--size <SIZE>` | RAM/CPU resource size | one of `micro` · `small` · `medium` · `large` · `xlarge` |
| `--profile <SIZE>` | deprecated: use --size |  |
| `--ram <MB>` | RAM in MB |  |
| `--cpu-millicores <250|500|1000>` | sustained CPU allowance | one of `250` · `500` · `1000` |
| `--max-concurrency <N>` | maximum instances (replicas) the app scales to |  |
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
gregale app my-api scale --interactive
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

Rename an app; the old hostname stops routing

`gregale app <slug> rename <new-slug>`

Examples:

```sh
gregale app my-api rename my-api-v2
```

### app restart

Restart from a snapshot or fresh configuration

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

Run a command against the live deployment

`gregale app <slug> exec [--interactive] [--tty] [--shell] [--detach] [--timeout-seconds <N>] [--max-output-bytes <N>] [--operation-policy <NAME>] [--operation-key <JSON>] [--equivalence-key <KEY>] [--idempotency-key <KEY>] [--poll-interval <D>] [--wait-timeout <D>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | attach stdin and stream output live (-i; default command /bin/sh) |  |
| `--tty` | allocate a remote terminal (-t; use -it) |  |
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

### app cp

Copy a path out of a fresh VM of the live deployment

`gregale app <slug> cp [--wait-timeout <D>] <remote-path> <local-path|->`

| Flag | Meaning | |
|---|---|---|
| `--wait-timeout <D>` | maximum time to wait for the task VM to start |  |

### app port-forward

Forward a local port to a host and port inside the app&#39;s network

`gregale app <slug> port-forward [--address <A>] [--wait-timeout <D>] [LOCAL_PORT:]HOST:PORT`

| Flag | Meaning | |
|---|---|---|
| `--address <A>` | local address to listen on (default 127.0.0.1) |  |
| `--wait-timeout <D>` | maximum time to wait for the task VM to start |  |

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

### app traffic-status

Show policy convergence and fresh gateway traffic wiring

`gregale app <slug> traffic-status`

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

Manage your subscription, payment method, budgets and cost reports

See also: [`plan`](#plan), [`usage`](#usage), [`invoices`](#invoices), [`overage-cap`](#overage-cap).

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

Manage monitored alerts, preview stopping and budget drafts

#### billing budgets list

List account budget policies and monitoring status

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

Save a draft or activate monitored alerts or preview stopping

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

Replace a budget policy at its expected revision

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

#### billing budgets channels

Show or replace the notification channels a budget&#39;s alerts also go to

`gregale billing budgets channels [--set <ID,ID>] [--clear] [--json] ID`

| Flag | Meaning | |
|---|---|---|
| `--set <ID,ID>` | Replace with these comma-separated channel IDs |  |
| `--clear` | Remove every channel; alerts go to the account email only |  |
| `--json` | Print JSON |  |

Examples:

```sh
gregale billing budgets channels BUDGET_ID
gregale billing budgets channels --set CHANNEL_ID,CHANNEL_ID BUDGET_ID
```

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


## approvals

Make production releases wait in their canary for approval

`gregale approvals [<subcommand>]`

Examples:

```sh
gregale approvals require --app my-api --approvals 2 --notify releases
gregale approvals require --app my-api --approvals 1 --approvers developer --timeout 4h
gregale approvals pending --app my-api
gregale approvals approve 3f9c1e2a-0b7d-4c55-9e1a-2d6f8b4c7e10 --comment "canary looks clean"
gregale approvals reject v42 --app my-api --reason "checkout latency is up"
```

### approvals require

Require approvals for an app&#39;s production releases

`gregale approvals require [--app <slug>] --approvals <N> [--after-stage <N>] [--timeout <DURATION>] [--approvers <ROLE>] [--allow-self-approval] [--notify <CHANNEL,...>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project app) |  |
| `--approvals <N>` | approvals each production release needs (1-5) | required |
| `--after-stage <N>` | canary stage the release waits in (default 1) |  |
| `--timeout <DURATION>` | abort a release that waits longer (default 24h) |  |
| `--approvers <ROLE>` | who may approve (default admin: owners and admins) | one of `admin` · `developer` |
| `--allow-self-approval` | count the deployer&#39;s own approval |  |
| `--notify <CHANNEL,...>` | Slack or email channels told when a release waits, is decided or times out |  |

### approvals policy

Show an app&#39;s approval policy

`gregale approvals policy [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project app) |  |

### approvals off

Stop requiring approvals for an app

`gregale approvals off [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project app) |  |

### approvals pending

List an app&#39;s releases waiting for approval

`gregale approvals pending [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project app) |  |

### approvals status

Show a release&#39;s approvals

`gregale approvals status [--app <slug>] <deployment>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug, to resolve a vN revision |  |

### approvals approve

Approve a release

`gregale approvals approve [--app <slug>] [--comment <TEXT>] <deployment>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug, to resolve a vN revision |  |
| `--comment <TEXT>` | note recorded with the approval |  |

### approvals reject

Reject a release and abort its rollout

`gregale approvals reject [--app <slug>] --reason <TEXT> <deployment>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug, to resolve a vN revision |  |
| `--reason <TEXT>` | why the release is rejected | required |


## scheduled-releases

Build production releases now and start them at a set time

`gregale scheduled-releases [<subcommand>]`

Examples:

```sh
gregale deploy --at 02:00 --release-notify releases
gregale scheduled-releases list
gregale scheduled-releases set v42 --app my-api --at "2026-10-22 09:00" --rollout safe
gregale scheduled-releases set v42 --app my-api --at now
gregale scheduled-releases cancel v42 --app my-api
```

### scheduled-releases list

List scheduled releases, pending ones first

`gregale scheduled-releases list [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | only this app&#39;s schedules |  |

### scheduled-releases show

Show a release&#39;s schedule

`gregale scheduled-releases show [--app <slug>] <deployment>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug, to resolve a vN revision |  |

### scheduled-releases set

Schedule, or move, the start of a release waiting without traffic

`gregale scheduled-releases set [--app <slug>] --at <WHEN> [--rollout <MODE>] [--notify <CHANNEL,...>] [--override-freeze <REASON>] <deployment>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug, to resolve a vN revision |  |
| `--at <WHEN>` | when the rollout starts: now, +2h, 02:00, &#34;2026-10-22 02:00&#34; (local) or RFC 3339 | required |
| `--rollout <MODE>` | how the release reaches traffic (default: the app release policy) | one of `safe` · `fast` · `immediate` |
| `--notify <CHANNEL,...>` | Slack or email channels told when the release starts or cannot start |  |
| `--override-freeze <REASON>` | release into deploy freezes active at that time; the reason is audited |  |

### scheduled-releases cancel

Cancel a pending schedule; the release stays built without traffic

`gregale scheduled-releases cancel [--app <slug>] <deployment>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug, to resolve a vN revision |  |


## freeze

Pause production releases for a window, weekly, or after rollbacks

`gregale freeze [<subcommand>]`

Examples:

```sh
gregale freeze create --app my-api --for 2h --reason "incident 4711"
gregale freeze create --all-apps --weekly fri --at 16:00 --for 64h --tz Europe/Istanbul --reason "no weekend releases"
gregale freeze create --app my-api --after-rollback 1h --reason "settle after automatic rollbacks"
gregale freeze status --app my-api
```

### freeze create

Create a freeze

`gregale freeze create [--app <slug>] [--all-apps] --reason <TEXT> [--for <DURATION>] [--from <TIME>] [--until <TIME>] [--weekly <DAYS>] [--at <HH:MM>] [--tz <ZONE>] [--after-rollback <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project app) |  |
| `--all-apps` | freeze every app of the account |  |
| `--reason <TEXT>` | why releases are frozen (required, at most 200 characters) | required |
| `--for <DURATION>` | window length from --from (default now), or weekly length |  |
| `--from <TIME>` | window start (RFC 3339) |  |
| `--until <TIME>` | window end (RFC 3339); omit --for and --until to freeze until lifted |  |
| `--weekly <DAYS>` | weekly days, comma-separated |  |
| `--at <HH:MM>` | weekly start time |  |
| `--tz <ZONE>` | weekly time zone (IANA; default UTC) |  |
| `--after-rollback <DURATION>` | freeze an app for this long after Gregale rolls it back automatically |  |

### freeze list

List freezes that have not ended

`gregale freeze list [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | only freezes covering this app |  |

### freeze lift

Lift a freeze

`gregale freeze lift <id>`

### freeze status

Show whether production releases of an app are frozen now

`gregale freeze status [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project app) |  |


## release-gates

Stop rollouts on SLO, error, metric or AI regressions and roll back

`gregale release-gates [<subcommand>]`

Examples:

```sh
gregale release-gates add --app my-api --name checkout-slo --slo checkout --max-burn-rate 10
gregale release-gates add --app my-api --name no-new-errors --new-errors --min-events 3
gregale release-gates add --app my-api --name orders --metric orders_per_minute --below 5
gregale release-gates add --app my-api --name ai-cost --ai cost_per_call --max-increase 30
gregale release-gates list --app my-api
```

### release-gates list

List an app&#39;s release gates

`gregale release-gates list [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project app) |  |

### release-gates add

Add a release gate

`gregale release-gates add [--app <slug>] --name <NAME> [--slo <NAME>] [--max-burn-rate <N>] [--new-errors] [--max-new-groups <N>] [--min-events <N>] [--metric <NAME>] [--below <VALUE>] [--above <VALUE>] [--ai <METRIC>] [--max-increase <PCT>] [--min-samples <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project app) |  |
| `--name <NAME>` | gate name | required |
| `--slo <NAME>` | fail when this SLO burns too fast |  |
| `--max-burn-rate <N>` | SLO burn rate that fails the gate (default 10) |  |
| `--new-errors` | fail when the release introduces new error groups |  |
| `--max-new-groups <N>` | new error groups allowed (default 0) |  |
| `--min-events <N>` | events before a new error group counts (default 3) |  |
| `--metric <NAME>` | fail when this custom metric crosses a bound |  |
| `--below <VALUE>` | fail when the metric averages below this |  |
| `--above <VALUE>` | fail when the metric averages above this |  |
| `--ai <METRIC>` | fail when this AI gateway metric gets worse than the previous release&#39;s: error_rate, flagged_rate, cost_per_call or ttfb_ms |  |
| `--max-increase <PCT>` | percent over the previous release that fails an --ai gate (default 50) |  |
| `--min-samples <N>` | requests, metric values or AI calls before the gate is judged (default 20) |  |

### release-gates rm

Remove a release gate

`gregale release-gates rm [--app <slug>] [--yes] <name>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project app) |  |
| `--yes` | accepted for consistency; rm does not prompt |  |


## canary

Advance canary stages, check their gates, or simulate a preset

`gregale canary [<subcommand>]`

### canary gate

Read the current profiling gate and route evidence

`gregale canary gate <deployment-id>`

### canary advance

Advance one observed stage with an optional audited profiling override

`gregale canary advance [--expected-step <N>] [--profile-policy-revision <REV>] [--profile-override-reason <TEXT>] <deployment-id>`

| Flag | Meaning | |
|---|---|---|
| `--expected-step <N>` | observed current canary step |  |
| `--profile-policy-revision <REV>` | current policy revision for an explicit override |  |
| `--profile-override-reason <TEXT>` | audited reason for overriding only the profiling gate |  |

### canary simulate

Estimate per-stage canary success from the last hour

`gregale canary simulate [--canary-preset <PRESET>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--canary-preset <PRESET>` | canary ladder preset | one of `slow` · `balanced` · `aggressive` · `1-10-50-100` · `adaptive` |


## build

Inspect builds, their provenance and SBOMs

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

Connect your GitHub account or start the repository wizard

See also: [`github`](#github).

`gregale connect [<subcommand>]`

### connect github

Connect a GitHub account for repo deploys

Examples:

```sh
gregale connect github
```

### connect repo

Open the dashboard wizard to bind a repository to an app

`gregale connect repo <owner>/<name>`

Examples:

```sh
gregale connect repo acme/my-api
```


## github

Bind an app to a GitHub repository and check its connection

See also: [`connect`](#connect).

`gregale github [<subcommand>] <slug>`

### github status

Show an app&#39;s GitHub connection health

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

List repositories the connected GitHub installation can see

`gregale github repos <slug>`

Examples:

```sh
gregale github repos my-api
```

### github bind

Bind an app to a visible GitHub repository

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
| `--rollout <MODE>` | production rollout mode: standard\|safe | one of `standard` · `safe` |
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

Configure CORS for an app

`gregale cors [<subcommand>]`

### cors allow

Attach a CORS rule to an app

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

List an app&#39;s CORS rules

`gregale cors ls [<slug>]`

The app defaults to the linked one.

### cors rm

Delete a CORS rule by id

`gregale cors rm [<slug>] <rule-id>`

### cors show

Show per-app default CORS + active rules (defaults to linked context)

`gregale cors show [<slug>]`


## crons

Manage scheduled HTTP requests and deployment commands

See also: [`triggers`](#triggers).

`gregale crons [<subcommand>]`

### crons next

Show upcoming expression times across app tasks

`gregale crons next [--app <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (linked app or picker by default) |  |

Examples:

```sh
gregale crons next --app my-api
gregale crons next --app my-api --json
```

Includes state and policy caveats.

### crons list

List cron rules

`gregale crons list --app <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### crons add

Schedule a POST to an app path, or a deployment command

`gregale crons add [--interactive] [--app <slug>] [--schedule <EXPR>] [--path <PATH>] [--command <EXEC>] [--arg <ARG>] [--shell] [--timeout-seconds <N>] [--max-output-bytes <N>] [--timezone <TZ>] [--skip-if-running] [--retry-max] [--retry-backoff-seconds] [--schedule-policy <JSON>] [--failure-rules <JSON>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose an HTTP path, schedule, and timezone, preview run times, and confirm creation |  |
| `--app <slug>` | app slug (interactive mode can use linked app or picker) |  |
| `--schedule <EXPR>` | five-field cron expression (required unless interactive) |  |
| `--path <PATH>` | path the cron sends a POST to (mutually exclusive with --command) |  |
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

Examples:

```sh
gregale crons add --app my-api --interactive
```

### crons info

Show one cron rule

`gregale crons info <id>`

### crons update

Update one cron rule

`gregale crons update [--interactive] [--app <SLUG>] [--schedule <EXPR>] [--path <PATH>] [--timezone <TZ>] [--enable] [--disable] [--skip-if-running] [--allow-overlap] [--retry-max] [--retry-backoff-seconds <N>] [--schedule-policy <JSON>] [--failure-rules <JSON>] [<id>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose an HTTP task, edit current values, preview times, and confirm changes |  |
| `--app <SLUG>` | app slug for interactive task selection (linked app or picker by default) |  |
| `--schedule <EXPR>` | new five-field cron expression |  |
| `--path <PATH>` | path the cron sends a POST to |  |
| `--timezone <TZ>` | IANA timezone |  |
| `--enable` | enable the cron |  |
| `--disable` | disable the cron |  |
| `--skip-if-running` | skip fires while a previous run is active |  |
| `--allow-overlap` | allow scheduled fires to overlap |  |
| `--retry-max` | additional command attempts after failure or timeout |  |
| `--retry-backoff-seconds <N>` | base retry delay; doubles per attempt |  |
| `--schedule-policy <JSON>` | replace versioned schedule policy JSON |  |
| `--failure-rules <JSON>` | replace versioned failure and outcome-code rules JSON |  |

Examples:

```sh
gregale crons update --app my-api --interactive
```

### crons rm

Delete one cron rule

`gregale crons rm [--yes] <id>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | accepted for scripts; crons rm does not prompt |  |

### crons run

Fire one cron immediately or choose and follow a manual request

`gregale crons run [--interactive] [--app <SLUG>] [--timeout <DURATION>] [<cron-id>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a task, review and confirm one manual run, then follow its request |  |
| `--app <SLUG>` | app slug for interactive selection (linked app or picker by default) |  |
| `--timeout <DURATION>` | maximum interactive follow duration (default 2m) |  |

Examples:

```sh
gregale crons run --app my-api --interactive
gregale crons run <cron-id>
```

### crons fire-now

Read or follow an existing manual fire request

`gregale crons fire-now [--wait] [--timeout <DURATION>] <request-id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | follow the existing request until terminal status |  |
| `--timeout <DURATION>` | maximum wait duration (default 2m; requires --wait) |  |

Examples:

```sh
gregale crons fire-now REQUEST_ID --wait --timeout 5m
```

### crons runs

Show execution history

`gregale crons runs [--interactive] [--app <SLUG>] [--before <CURSOR>] [--limit <N>] [--run <TASK-ID>] [<id>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a task, browse runs, and inspect command output |  |
| `--app <SLUG>` | app slug for interactive selection (linked app or picker by default) |  |
| `--before <CURSOR>` | pagination cursor for older runs |  |
| `--limit <N>` | max runs to show (1..100) |  |
| `--run <TASK-ID>` | show details and captured output for one command run |  |

Examples:

```sh
gregale crons runs --app my-api --interactive
gregale crons runs <id>
```

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

Manage event triggers from message brokers and crons

See also: [`crons`](#crons).

`gregale triggers [<subcommand>]`

### triggers list

List triggers

`gregale triggers list [--app <slug>] [--kind <value>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | filter to an app slug |  |
| `--kind <value>` | filter by trigger kind | one of `cron` · `kafka` · `nats` · `redis_streams` · `sqs_compat` · `queue` · `realtime` |

### triggers get

Show one trigger

`gregale triggers get <id>`

### triggers create

Create a broker trigger

`gregale triggers create --app <slug> --kind <kind> [--slug <slug>] [--config <JSON>] [--enabled] [--disabled] [--batch-size <N>] [--batch-window-ms <N>] [--max-attempts <N>] [--payload-max-bytes <N>] [--broker-poison-strategy <commit|seek-to-offset>] [--delivery <record|batch>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--kind <kind>` | trigger kind | required; one of `kafka` · `nats` · `redis_streams` · `sqs_compat` · `queue` · `realtime` |
| `--slug <slug>` | trigger slug (required for non-cron kinds) |  |
| `--config <JSON>` | JSON config (inline \| @file \| -) |  |
| `--enabled` | enable the trigger |  |
| `--disabled` | disable the trigger |  |
| `--batch-size <N>` | maximum records per dispatch batch |  |
| `--batch-window-ms <N>` | maximum batch dwell time in milliseconds |  |
| `--max-attempts <N>` | maximum delivery attempts |  |
| `--payload-max-bytes <N>` | maximum broker payload size |  |
| `--broker-poison-strategy <commit|seek-to-offset>` | kafka poison strategy | one of `commit` · `seek-to-offset` |
| `--delivery <record|batch>` | one invocation per record or per dispatch batch | one of `record` · `batch` |

### triggers update

Update one trigger

`gregale triggers update [--enabled] [--disabled] [--config <JSON>] [--schedule <EXPR>] [--path <PATH>] [--batch-size <N>] [--batch-window-ms <N>] [--max-attempts <N>] [--payload-max-bytes <N>] [--broker-poison-strategy <commit|seek-to-offset>] [--delivery <record|batch>] <id>`

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
| `--delivery <record|batch>` | one invocation per record or per dispatch batch | one of `record` · `batch` |

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
| `--state <STATE>` | filter by record state | one of `pending` · `claimed` · `succeeded` · `retry` · `dead_letter` · `superseded` |

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

List background worker pools, or one pool by app

`gregale workers list [--app <slug>] [<app>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | list only this app&#39;s worker pool |  |

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

Manage jobs that run to completion

`gregale jobs [<subcommand>]`

Examples:

```sh
gregale jobs
gregale jobs overview nightly-report
gregale jobs --help
```

### jobs resume

Recover a saved Job run: observe, read failures or collect results

`gregale jobs resume [--dry-run] [--run <UUID|PREFIX>] [--list] [--overview] [--current-connection] [--forget <UUID>] [--timeout <DURATION>] [--poll-interval <DURATION>] [--job <NAME>] [--saved-job] [--latest] [--logs] [--results]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview the selected run and action offline; no waiting, API access, or file writes |  |
| `--run <UUID|PREFIX>` | saved run UUID or unique prefix (at least 8 characters), resolved on the current profile/API |  |
| `--list` | list saved identifiers without API access |  |
| `--overview` | group recorded statuses into in progress, finished, and unavailable; retains records |  |
| `--current-connection` | filter --list or --overview by current profile and API; retain all records |  |
| `--forget <UUID>` | remove one local record only; requires its recorded API/profile, makes no requests |  |
| `--timeout <DURATION>` | maximum wait (default 10m) or overview duration (default 30s); positive |  |
| `--poll-interval <DURATION>` | poll interval (default 2s; 1s to 1h) |  |
| `--job <NAME>` | filter saved runs by exact Job name on the current profile/API; supports --list and --overview |  |
| `--saved-job` | use the Job name from local jobs defaults; excludes --job and --forget; inherits no run settings |  |
| `--latest` | select the most recently recorded run for --saved-job on the current profile/API; excludes explicit run and maintenance selectors |  |
| `--logs` | read current failed-task logs from the exact recorded run; accepts --job and --run |  |
| `--results` | wait for success and collect exact recorded run outputs into ./results/JOB/RUN_ID |  |

Examples:

```sh
gregale jobs resume --list
gregale jobs resume --job nightly-report --overview
gregale jobs resume --job nightly-report --logs
gregale jobs resume --job nightly-report --results
gregale jobs resume --run RUN_ID
```

### jobs list

List jobs in this account

`gregale jobs list [--limit <N>] [--offset <N>] [--all]`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1..200, default 50) |  |
| `--offset <N>` | starting offset (&gt;= 0) |  |
| `--all` | walk every page using --limit and --offset |  |

### jobs next

Show upcoming recurring Job expression times and readiness

Examples:

```sh
gregale jobs next
gregale jobs next --json
```

### jobs add

Create a new job

`gregale jobs add [--interactive] [--image <REF>] [--command <ARGV>] [--ram <MB>] [--timeout <SECONDS>] [--parallelism <N>] [--retries <N>] [--schedule <EXPR>] [--timezone <TZ>] [--schedule-policy <JSON>] [--failure-rules <JSON>] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a name, image, command, resources, and optional recurring schedule |  |
| `--image <REF>` | OCI image (required unless interactive) |  |
| `--command <ARGV>` | comma-separated entrypoint (e.g. /bin/sh,-c,echo hi) |  |
| `--ram <MB>` | billable memory in MB (0 = plan default) |  |
| `--timeout <SECONDS>` | per-task wall-clock deadline (0 = plan default) |  |
| `--parallelism <N>` | max concurrent tasks across a run (0 = plan default) |  |
| `--retries <N>` | per-task max retries (0 = plan default) |  |
| `--schedule <EXPR>` | recurring five-field cron schedule |  |
| `--timezone <TZ>` | IANA timezone for the recurring schedule |  |
| `--schedule-policy <JSON>` | versioned recurring schedule policy JSON |  |
| `--failure-rules <JSON>` | versioned exit-code and outcome retry rules JSON |  |

Examples:

```sh
gregale jobs add --interactive
```

### jobs info

Inspect Job readiness (saved Job by default)

`gregale jobs info [--check] [--wait-ready] [--timeout <DURATION>] [--poll-interval <DURATION>] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--check` | exit 0 only for an active Job with a ready image; excludes --wait-ready |  |
| `--wait-ready` | wait for selected image preparation |  |
| `--timeout <DURATION>` | maximum image wait (default 5m) |  |
| `--poll-interval <DURATION>` | polling interval (default 2s) |  |

Examples:

```sh
gregale jobs info
gregale jobs info --check
gregale jobs info --wait-ready
gregale jobs info my-job --wait-ready --timeout 5m
```

### jobs update

Update one job

`gregale jobs update [--interactive] [--image <REF>] [--command <ARGV>] [--ram <MB>] [--timeout <SECONDS>] [--parallelism <N>] [--retries <N>] [--pause] [--resume] [--schedule <EXPR>] [--timezone <TZ>] [--unschedule] [--schedule-policy <JSON>] [--failure-rules <JSON>] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a Job, edit resources and schedule, and confirm changes |  |
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

Examples:

```sh
gregale jobs update --interactive
```

### jobs rm

Soft-delete one job

`gregale jobs rm [--interactive] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a Job, review configuration, and confirm removal |  |

Examples:

```sh
gregale jobs rm --interactive
```

### jobs run

Dispatch a new run (fan-out N tasks)

`gregale jobs run [--saved-job] [--wait-ready] [--image-wait-timeout <DURATION>] [--image-poll-interval <DURATION>] [--interactive] [--wait] [--results] [--out-dir <DIR>] [--all-artifacts] [--include <GLOB>] [--exclude <GLOB>] [--wait-timeout <DURATION>] [--poll-interval <DURATION>] [--dry-run] [--tasks <N>] [--retries <N>] [--timeout <SECONDS>] [--input <ID=REF>] [--input-manifest-uri <URI>] [--input-manifest-sha256 <DIGEST>] [--parallelism <N>] [--flexible] [--eligible-at <RFC3339>] [--latest-start-at <RFC3339>] [--fail-fast] [--failure-rules <JSON>] [<job-name>]`

| Flag | Meaning | |
|---|---|---|
| `--saved-job` | use local jobs defaults for the Job name and omitted settings; excludes a positional Job name |  |
| `--wait-ready` | wait for selected image preparation before submitting |  |
| `--image-wait-timeout <DURATION>` | image preparation deadline (default 5m; requires --wait-ready) |  |
| `--image-poll-interval <DURATION>` | image polling interval (default 2s; requires --wait-ready) |  |
| `--interactive` | choose a Job, review settings, and confirm one run |  |
| `--wait` | wait for the accepted run to succeed (explicit Job only) |  |
| `--results` | wait and collect outputs (default ./results/JOB/RUN_ID) |  |
| `--out-dir <DIR>` | collect whole-run managed artifacts after success; implies --wait |  |
| `--all-artifacts` | ignore saved artifact filters for this invocation |  |
| `--include <GLOB>` | repeatable artifact-name glob for collection; requires --out-dir or --results; quote patterns |  |
| `--exclude <GLOB>` | repeatable artifact-name glob to omit; requires --out-dir or --results; exclusions take precedence |  |
| `--wait-timeout <DURATION>` | wait deadline after acceptance (default 10m; requires --wait, --out-dir, or --results) |  |
| `--poll-interval <DURATION>` | wait read interval (default 2s; 1s to 1h; requires --wait, --out-dir, or --results) |  |
| `--dry-run` | preview effective run settings locally without submitting |  |
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

Examples:

```sh
gregale jobs run --interactive
gregale jobs run <job-name>
gregale jobs run nightly-report --tasks 10 --wait --wait-timeout 10m
gregale jobs run nightly-report --tasks 10 --out-dir ./results
gregale jobs run nightly-report --tasks 10 --results
gregale jobs run --dry-run
gregale jobs run --saved-job --dry-run
gregale jobs run --wait-ready --results
```

### jobs runs

List runs (saved Job by default)

`gregale jobs runs [--failed-only] [--status <STATUS>] [--limit <N>] [--offset <N>] [--all] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--failed-only` | show failed and dead-letter runs in fetched history; use --all for subsequent pages |  |
| `--status <STATUS>` | filter fetched history by aggregate status; use --all to scan subsequent pages | one of `queued` · `running` · `succeeded` · `failed` · `cancelled` · `dead_letter` |
| `--limit <N>` | page size (1..200, default 50) |  |
| `--offset <N>` | starting offset (&gt;= 0) |  |
| `--all` | walk every page using --limit and --offset |  |

Examples:

```sh
gregale jobs runs
gregale jobs runs --failed-only --all
```

### jobs overview

Summarize Job readiness, latest run and next commands

`gregale jobs overview [--check] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--check` | exit 0 only for an active Job, ready image, and succeeded latest run |  |

Examples:

```sh
gregale jobs overview
gregale jobs overview --check
gregale jobs overview nightly-report --json
```

Uses the saved Job by default.

### jobs status

Read one exact or latest Job run summary

`gregale jobs status [--check] [<name>] [<run-id|latest>]`

| Flag | Meaning | |
|---|---|---|
| `--check` | exit 0 only when the recorded run status is succeeded |  |

Examples:

```sh
gregale jobs status
gregale jobs status nightly-report
gregale jobs status nightly-report RUN_ID --check
gregale jobs status nightly-report latest
gregale --json jobs status nightly-report RUN_ID
```

Uses the saved Job and its latest run by default.

### jobs occurrences

Inspect recurring schedule decisions

`gregale jobs occurrences [--interactive] [--before <ID>] [--cursor <ID>] [--all] [--limit <N>] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a Job and browse scheduled occurrence decisions |  |
| `--before <ID>` | alias for --cursor |  |
| `--cursor <ID>` | opaque continuation cursor |  |
| `--all` | walk every page using --limit and --cursor |  |
| `--limit <N>` | max occurrence decisions (1..200) |  |

Examples:

```sh
gregale jobs occurrences --interactive
```

### jobs wait

Follow a Job run (saved Job and latest by default)

`gregale jobs wait [--interactive] [--out-dir <DIR>] [--include <GLOB>] [--exclude <GLOB>] [--timeout <DURATION>] [--poll-interval <DURATION>] [<name>] [<run-id|latest>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a Job and run |  |
| `--out-dir <DIR>` | collect artifacts after success, verifying matching existing files; non-interactive target only |  |
| `--include <GLOB>` | repeatable artifact-name glob for collection; requires --out-dir; quote patterns |  |
| `--exclude <GLOB>` | repeatable artifact-name glob to omit; requires --out-dir; exclusions take precedence |  |
| `--timeout <DURATION>` | maximum wait duration (default 10m) |  |
| `--poll-interval <DURATION>` | polling interval (default 2s) |  |

Examples:

```sh
gregale jobs wait
gregale jobs wait nightly-report
gregale jobs wait nightly-report latest --timeout 10m
gregale jobs wait --interactive
gregale jobs wait my-job RUN_ID --timeout 10m --json
```

### jobs cancel

Cancel a run

`gregale jobs cancel [--dry-run] [--interactive] [<name>] [<run-id>]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | inspect target status, task counts, and cancellation effects without cancelling |  |
| `--interactive` | choose an unfinished run, review progress, and confirm cancellation |  |

Examples:

```sh
gregale jobs cancel nightly-report RUN_ID --dry-run
gregale jobs cancel --interactive
```

### jobs defaults

Show or manage non-secret project-local Job defaults

Examples:

```sh
gregale jobs defaults
gregale jobs defaults show
gregale jobs defaults --help
```

#### jobs defaults set

Replace saved Job defaults

`gregale jobs defaults set [--dry-run] [--tasks <N>] [--results] [--wait-ready] [--include <GLOB>] [--exclude <GLOB>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview current/proposed settings without saving or submitting |  |
| `--tasks <N>` | default positive task count |  |
| `--results` | collect results by default |  |
| `--wait-ready` | wait for image preparation before submitting by default |  |
| `--include <GLOB>` | repeatable saved artifact-name glob; quote patterns |  |
| `--exclude <GLOB>` | repeatable saved artifact-name glob to omit |  |

#### jobs defaults update

Change saved Job defaults while preserving omitted settings

`gregale jobs defaults update [--dry-run] [--tasks <N>] [--clear-tasks] [--results] [--wait-ready] [--include <GLOB>] [--exclude <GLOB>] [--all-artifacts] [--add-include <GLOB>]... [--remove-include <GLOB>]... [--add-exclude <GLOB>]... [--remove-exclude <GLOB>]... [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview changes while preserving omitted settings; no writes or submissions |  |
| `--tasks <N>` | replace default positive task count |  |
| `--clear-tasks` | unset saved task count while preserving other settings; excludes --tasks |  |
| `--results` | replace results preference; use --results=false to disable |  |
| `--wait-ready` | replace image-wait preference; use --wait-ready=false to disable |  |
| `--include <GLOB>` | repeatable glob; explicit filters replace both saved lists |  |
| `--exclude <GLOB>` | repeatable glob to omit; explicit filters replace both saved lists |  |
| `--all-artifacts` | clear saved artifact filters |  |
| `--add-include <GLOB>` | append include glob; preserve other filters and skip duplicates |  |
| `--remove-include <GLOB>` | remove exact saved include glob; unknown patterns fail |  |
| `--add-exclude <GLOB>` | append exclude glob; preserve other filters and skip duplicates |  |
| `--remove-exclude <GLOB>` | remove exact saved exclude glob; unknown patterns fail |  |

Examples:

```sh
gregale jobs defaults update --tasks 3 --dry-run
gregale jobs defaults update --clear-tasks --dry-run
gregale jobs defaults update --results=false
gregale jobs defaults update --all-artifacts
```

#### jobs defaults show

Show saved Job settings, their path, and local recovery readiness

`gregale jobs defaults show`

#### jobs defaults clear

Remove saved Job defaults

`gregale jobs defaults clear [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | show settings and inheritance effects without removing the file |  |

Examples:

```sh
gregale jobs defaults clear --dry-run
```

### jobs results

Wait for success and collect outputs

`gregale jobs results [--out-dir <DIR>] [--all-artifacts] [--dry-run] [--timeout <DURATION>] [--poll-interval <DURATION>] [--include <GLOB>] [--exclude <GLOB>] [<name>] [<run-id|latest>]`

| Flag | Meaning | |
|---|---|---|
| `--out-dir <DIR>` | local destination (default ./results/JOB/RUN_ID) |  |
| `--all-artifacts` | ignore saved artifact filters for this invocation |  |
| `--dry-run` | preview current artifacts without waiting or writing files |  |
| `--timeout <DURATION>` | maximum wait duration (default 10m) |  |
| `--poll-interval <DURATION>` | polling interval (default 2s) |  |
| `--include <GLOB>` | repeatable artifact-name glob; quote patterns |  |
| `--exclude <GLOB>` | repeatable artifact-name glob to omit; exclusions take precedence |  |

Examples:

```sh
gregale jobs results nightly-report
gregale jobs results nightly-report latest --out-dir ./results
gregale jobs results nightly-report latest --out-dir ./results --dry-run
```

Output files that already exist are verified, not replaced.

### jobs tasks

List tasks (saved Job and latest run by default)

`gregale jobs tasks [--all] [--limit <N>] [--offset <N>] [--unsuccessful-only] [--failed-only] [--artifacts-only] [--include <GLOB>] [--exclude <GLOB>] [--interactive] [<name>] [<run-id|latest>]`

| Flag | Meaning | |
|---|---|---|
| `--all` | read all task pages from the starting offset |  |
| `--limit <N>` | task page size (1..200) |  |
| `--offset <N>` | starting task offset (non-negative) |  |
| `--unsuccessful-only` | scan all task pages for failed, timeout, oom, and cancelled tasks |  |
| `--failed-only` | scan all task pages and show failed, timeout, and oom tasks |  |
| `--artifacts-only` | scan task pages for valid manifests containing artifacts |  |
| `--include <GLOB>` | repeatable artifact-name glob; requires --artifacts-only; quote patterns |  |
| `--exclude <GLOB>` | repeatable artifact-name glob to omit; requires --artifacts-only; exclusions take precedence |  |
| `--interactive` | choose a run, browse task pages, and filter unsuccessful tasks |  |

Examples:

```sh
gregale jobs tasks --unsuccessful-only
gregale jobs tasks
gregale jobs tasks --failed-only
gregale jobs tasks --artifacts-only
gregale jobs tasks nightly-report latest --all
gregale jobs tasks nightly-report latest --failed-only
gregale jobs tasks nightly-report latest
gregale jobs tasks --interactive
```

### jobs attempts

List retained attempts for one task in an exact or latest run

`gregale jobs attempts [--task <N>] [--unsuccessful-only] [--failed-only] [--all] [--limit <N>] [--offset <N>] [--interactive] [<name>] [<run-id|latest>] [<task-index>]`

| Flag | Meaning | |
|---|---|---|
| `--task <N>` | task index; omitted Job uses local defaults, omitted run uses latest |  |
| `--unsuccessful-only` | filter fetched history to failed, timeout, oom, and cancelled attempts |  |
| `--failed-only` | filter fetched history to failed, timeout, and oom attempts |  |
| `--all` | read all retained attempt pages from the starting offset |  |
| `--limit <N>` | history page size (1..200; server default 50) |  |
| `--offset <N>` | starting history offset (non-negative) |  |
| `--interactive` | choose a task and inspect retained attempts and output |  |

Examples:

```sh
gregale jobs attempts --task 2 --unsuccessful-only --all
gregale jobs attempts --task 2 --failed-only --all
gregale jobs attempts nightly-report latest 0 --all --failed-only
gregale jobs attempts nightly-report latest 0 --all
gregale jobs attempts nightly-report latest 0
gregale jobs attempts --interactive
```

### jobs retry

Retry one failed task

`gregale jobs retry [--dry-run] [--results] [--include <GLOB>] [--exclude <GLOB>] [--all-artifacts] [--task <N>] [--wait] [--wait-timeout <DURATION>] [--poll-interval <DURATION>] [--interactive] [<name>] [<run-id>] [<task-index>]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | read task state and preview retry without starting another attempt |  |
| `--results` | wait for success and collect outputs into ./results/JOB/RUN_ID, verifying existing files |  |
| `--include <GLOB>` | artifact-name glob; requires --results |  |
| `--exclude <GLOB>` | artifact-name glob to omit; requires --results |  |
| `--all-artifacts` | ignore saved artifact filters; requires --results |  |
| `--task <N>` | task index; omitted Job uses local defaults, omitted run uses latest |  |
| `--wait` | wait for the whole run after one task retry |  |
| `--wait-timeout <DURATION>` | wait deadline after retry acceptance (default 10m; requires --wait or --results) |  |
| `--poll-interval <DURATION>` | wait read interval (default 2s; 1s to 1h; requires --wait or --results) |  |
| `--interactive` | choose a failed task, review eligibility, and confirm one retry |  |

Examples:

```sh
gregale jobs retry --task 2 --dry-run
gregale jobs retry --task 2 --results
gregale jobs retry --task 2
gregale jobs retry nightly-report latest --task 2 --wait
gregale jobs retry nightly-report RUN_ID 0 --wait
gregale jobs retry --interactive
```

With --task, uses the saved Job and its newest run.

### jobs replay-failed

Replay unsuccessful tasks of a Job run

`gregale jobs replay-failed [--dry-run] [--results] [--include <GLOB>] [--exclude <GLOB>] [--all-artifacts] [--wait] [--wait-timeout <DURATION>] [--poll-interval <DURATION>] [--interactive] [<name>] [<run-id|latest>]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | read source tasks and preview replay without starting a new run |  |
| `--results` | wait for recovery success and collect outputs into ./results/JOB/NEW_RUN_ID, verifying existing files |  |
| `--include <GLOB>` | artifact-name glob; requires --results |  |
| `--exclude <GLOB>` | artifact-name glob to omit; requires --results |  |
| `--all-artifacts` | ignore saved artifact filters; requires --results |  |
| `--wait` | wait for the new recovery run |  |
| `--wait-timeout <DURATION>` | wait deadline after replay acceptance (default 10m; requires --wait or --results) |  |
| `--poll-interval <DURATION>` | wait read interval (default 2s; 1s to 1h; requires --wait or --results) |  |
| `--interactive` | choose a source run, review unsuccessful tasks, and confirm replay |  |

Examples:

```sh
gregale jobs replay-failed nightly-report latest --dry-run
gregale jobs replay-failed --results
gregale jobs replay-failed
gregale jobs replay-failed nightly-report latest --wait
gregale jobs replay-failed nightly-report RUN_ID --wait
gregale jobs replay-failed --interactive
```

An omitted Job uses local defaults; an omitted run uses the latest.

### jobs artifact-url

Verify a managed result and get a signed URL

`gregale jobs artifact-url [--interactive] [<name>] [<run-id|latest>] [<task-index>] [<artifact-name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a task and managed output artifact |  |

Examples:

```sh
gregale jobs artifact-url nightly-report latest 0 report.csv
gregale jobs artifact-url --interactive
gregale jobs artifact-url <name> <run-id> <task-index> <artifact-name>
```

### jobs artifact-download

Download and verify current managed artifacts

`gregale jobs artifact-download [--task <N>] [--out <PATH>] [--all] [--all-tasks] [--include <GLOB>] [--exclude <GLOB>] [--dry-run] [--resume] [--out-dir <DIR>] [<name>] [<run-id|latest>] [<task-index>] [<artifact-name>]`

| Flag | Meaning | |
|---|---|---|
| `--task <N>` | single-artifact task index; omitted Job uses local defaults, omitted run uses latest |  |
| `--out <PATH>` | new destination file; required for a single artifact |  |
| `--all` | download all current managed artifacts |  |
| `--all-tasks` | download current managed artifacts across all task pages into task directories |  |
| `--include <GLOB>` | repeatable artifact-name glob for bulk modes; quote patterns |  |
| `--exclude <GLOB>` | repeatable artifact-name glob to omit in bulk modes; exclusions take precedence |  |
| `--dry-run` | preview bulk destinations, bytes, and conflicts; requires --all or --all-tasks |  |
| `--resume` | verify and skip matching files; requires bulk mode and an explicit run UUID |  |
| `--out-dir <DIR>` | destination directory, created when missing; required in bulk mode |  |

Examples:

```sh
gregale jobs artifact-download --task 2 report.csv --out report.csv
gregale jobs artifact-download nightly-report latest 0 report.csv --out report.csv
```

### jobs artifacts

List recorded task artifacts and managed download commands

`gregale jobs artifacts [--task <N>] [--attempt <N>] [--include <GLOB>] [--exclude <GLOB>] [<name>] [<run-id|latest>] [<task-index>]`

| Flag | Meaning | |
|---|---|---|
| `--task <N>` | task index; omitted Job uses local defaults, omitted run uses latest |  |
| `--attempt <N>` | retained attempt number (metadata only) |  |
| `--include <GLOB>` | repeatable artifact-name glob; quote patterns |  |
| `--exclude <GLOB>` | repeatable artifact-name glob to omit; exclusions take precedence |  |

Examples:

```sh
gregale jobs artifacts --task 2
gregale jobs artifacts --task 2 --attempt 1
gregale jobs artifacts nightly-report latest 0
```

### jobs logs

Read unsuccessful-task logs by default, or select one task

`gregale jobs logs [--follow] [--failed-only] [--unsuccessful-only] [--task <N>] [--attempt <N>] [--interactive] [--max-bytes <N>] [<name>] [<run-id|latest>] [<task-index>]`

| Flag | Meaning | |
|---|---|---|
| `--follow` | stream the task&#39;s output live until it finishes (-f) |  |
| `--failed-only` | scan task pages and read current logs for failed, timeout, and oom tasks |  |
| `--unsuccessful-only` | scan task pages and read current logs for failed, timeout, oom, and cancelled tasks |  |
| `--task <N>` | task index shortcut; omitted Job uses saved defaults, omitted run uses latest |  |
| `--attempt <N>` | read retained output for a positive attempt number (explicit task only) |  |
| `--interactive` | choose a Job, run, and task to inspect |  |
| `--max-bytes <N>` | maximum log payload size (1..1048576) |  |

Examples:

```sh
gregale jobs logs
gregale jobs logs nightly-report latest 0 --follow
gregale jobs logs nightly-report
gregale jobs logs --failed-only
gregale jobs logs --task 0
gregale jobs logs nightly-report --task 0
gregale jobs logs nightly-report latest 0
gregale jobs logs --interactive
```

### jobs registry

Manage private registry credentials for one job

#### jobs registry list

List registry credentials for the job

`gregale jobs registry list <job>`

#### jobs registry set

Store a registry credential for the job

`gregale jobs registry set [--interactive] --registry <HOST> --user <USER> [--password-stdin] [--password <PASSWORD>] [<job>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a Job, enter a hidden token, and review add or replace |  |
| `--registry <HOST>` | registry host | required |
| `--user <USER>` | registry user | required |
| `--password-stdin` | read the password from stdin |  |
| `--password <PASSWORD>` | registry password (prefer --password-stdin) |  |

Examples:

```sh
gregale jobs registry set --interactive
```

#### jobs registry rm

Remove a registry credential from the job

`gregale jobs registry rm [--interactive] [--registry <HOST>] [<job>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a credential, review metadata, and confirm removal |  |
| `--registry <HOST>` | registry host (required unless interactive) |  |

Examples:

```sh
gregale jobs registry rm --interactive
```


## automations

Build, monitor and control customer-built automations

`gregale automations [<subcommand>]`

Examples:

```sh
gregale automations init --list
gregale automations check --app billing --scenarios scenarios.yaml
gregale automations diff --app billing --name approval-flow
gregale automations init --template scheduled-report --path reports
gregale automations runs --app billing --name approval-flow --status failed
gregale automations diagnose --app billing --name approval-flow --run RUN_ID
gregale automations recovery-preview --app billing --name approval-flow --from 2026-10-01T00:00:00Z --until 2026-10-02T00:00:00Z
gregale automations recover --app billing --name approval-flow --run RUN_ID:COUNT --idempotency-key recovery-batch-1
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

### automations live

Start, observe and check saved cases against real execution

#### automations live baseline

Manage named baselines and inspect audit history

`gregale automations live baseline <retire|list|show|history|promote>`

##### automations live baseline retire

Permanently retire a baseline with audit history

`gregale automations live baseline retire --app <SLUG> --name <NAME> --baseline-name <NAME> --retirement-id <UUID> --expected-revision <N> --confirm-retirement`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--baseline-name <NAME>` | named release track, such as production | required |
| `--retirement-id <UUID>` | stable retirement UUID; preserve for retries | required |
| `--expected-revision <N>` | current positive baseline revision | required |
| `--confirm-retirement` | confirm permanent retirement; release slot and evidence pin | required |

Protected by revision checks.

##### automations live baseline list

Find active baselines and read retained evidence health

`gregale automations live baseline list --app <SLUG> --name <NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |

Never executes anything.

##### automations live baseline show

Read the current immutable promotion receipt

`gregale automations live baseline show --app <SLUG> --name <NAME> --baseline-name <NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--baseline-name <NAME>` | named release track, such as production | required |

##### automations live baseline history

Read up to 100 recent promotions

`gregale automations live baseline history --app <SLUG> --name <NAME> --baseline-name <NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--baseline-name <NAME>` | named release track, such as production | required |

##### automations live baseline promote

Promote passing evidence to a baseline with revision checks

`gregale automations live baseline promote --app <SLUG> --name <NAME> --baseline-name <NAME> --suite <UUID> --promotion-id <UUID> --expected-revision <N> --expected-version <N> --expected-published-version <N> --expected-deployment-id <UUID> --policy <FILE>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--baseline-name <NAME>` | named release track, such as production | required |
| `--suite <UUID>` | passing retained suite UUID | required |
| `--promotion-id <UUID>` | stable promotion UUID; preserve for retries | required |
| `--expected-revision <N>` | current baseline revision; explicit 0 creates | required |
| `--expected-version <N>` | expected candidate saved version | required |
| `--expected-published-version <N>` | expected published revision | required |
| `--expected-deployment-id <UUID>` | expected candidate deployment UUID | required |
| `--policy <FILE>` | version 2 coverage and age policy JSON | required |

Never executes workflows.

#### automations live gate

Require passing suite evidence for a candidate target

`gregale automations live gate --app <SLUG> --name <NAME> [--policy <FILE>] [--baseline <UUID>] [--baseline-name <NAME>] --candidate <UUID> --expected-version <N> --expected-published-version <N> --expected-deployment-id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--policy <FILE>` | versioned required-case coverage policy JSON |  |
| `--baseline <UUID>` | retained baseline suite UUID; exclusive with baseline-name |  |
| `--baseline-name <NAME>` | resolve a named baseline; exclusive with baseline |  |
| `--candidate <UUID>` | distinct retained candidate suite UUID | required |
| `--expected-version <N>` | expected candidate saved automation version | required |
| `--expected-published-version <N>` | expected candidate published revision | required |
| `--expected-deployment-id <UUID>` | expected candidate live default deployment UUID | required |

Read-only.

#### automations live suite

Run selected live cases sequentially with resumable private progress

`gregale automations live suite [--app <SLUG>] [--name <NAME>] [--case <NAME>] [--expected-version <N>] [--expected-published-version <N>] [--expected-deployment-id <UUID>] [--idempotency-key <KEY>] [--journal <PATH>] [--resume <PATH>] [--share] --confirm-real-actions [--timeout <DURATION>] [--poll-interval <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug for a new suite |  |
| `--name <NAME>` | automation name for a new suite |  |
| `--case <NAME>` | explicit case selection; repeat in execution order, at most 32 |  |
| `--expected-version <N>` | saved automation version for a new suite |  |
| `--expected-published-version <N>` | published revision for a new suite |  |
| `--expected-deployment-id <UUID>` | live default deployment UUID for a new suite |  |
| `--idempotency-key <KEY>` | stable suite key for a new suite |  |
| `--journal <PATH>` | new private progress file; required for a new suite |  |
| `--resume <PATH>` | continue the original selection from this suite journal |  |
| `--share` | save a new suite in shared dashboard history; persisted for resume |  |
| `--confirm-real-actions` | confirm execution of remaining selected cases | required |
| `--timeout <DURATION>` | total invocation deadline (default 5m, up to 1h) |  |
| `--poll-interval <DURATION>` | poll interval (default 3s, 1s to 1m) |  |

#### automations live list

Read current target IDs and recent live verifications

`gregale automations live list --app <SLUG> --name <NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |

#### automations live start

Explicitly start one saved case against real handlers

`gregale automations live start --app <SLUG> --name <NAME> --case <NAME> --expected-version <N> --expected-published-version <N> --expected-deployment-id <UUID> --idempotency-key <KEY> --confirm-real-actions [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--case <NAME>` | saved case with live expectations | required |
| `--expected-version <N>` | current saved automation version | required |
| `--expected-published-version <N>` | published automation revision | required |
| `--expected-deployment-id <UUID>` | live default deployment UUID | required |
| `--idempotency-key <KEY>` | stable key; retry only with the original start flags | required |
| `--confirm-real-actions` | confirm this case can perform real actions | required |
| `--wait` | wait and require a current passing assertion verdict |  |
| `--timeout <DURATION>` | maximum wait (default 5m, up to 1h) |  |
| `--poll-interval <DURATION>` | poll interval (default 3s, 1s to 1m) |  |

#### automations live show

Read one verification without requiring a verdict

`gregale automations live show --app <SLUG> --name <NAME> --run <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--run <UUID>` | existing live verification run UUID | required |

#### automations live check

Check a terminal verification and require a current passing verdict

`gregale automations live check --app <SLUG> --name <NAME> --run <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--run <UUID>` | existing live verification run UUID | required |

#### automations live wait

Observe a run, check assertions and require a passing verdict

`gregale automations live wait --app <SLUG> --name <NAME> --run <UUID> [--timeout <DURATION>] [--poll-interval <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--run <UUID>` | existing live verification run UUID | required |
| `--timeout <DURATION>` | maximum wait (default 5m, up to 1h) |  |
| `--poll-interval <DURATION>` | poll interval (default 3s, 1s to 1m) |  |

### automations diff

Compare draft and published definition fields without exposing values

`gregale automations diff --app <SLUG> --name <NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |

### automations check

Check simulation scenarios and expected step outcomes

`gregale automations check --app <SLUG> --scenarios <PATH> [--require-coverage] [--suggest] [--suggest-out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--scenarios <PATH>` | scenario suite YAML or JSON file | required |
| `--require-coverage` | fail on unexcluded coverage hints |  |
| `--suggest` | write coverage scenario starters to a new file |  |
| `--suggest-out <PATH>` | new suggestion file (requires --suggest; defaults beside the suite) |  |

### automations init

Create a local automation starter with simulation samples

`gregale automations init [--template <NAME>] [--path <DIR>] [--list]`

| Flag | Meaning | |
|---|---|---|
| `--template <NAME>` | automation starter name |  |
| `--path <DIR>` | new directory (defaults to starter name) |  |
| `--list` | list starters without creating files |  |

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

### automations runs

List run history for one automation

`gregale automations runs --app <SLUG> --name <NAME> [--status <STATUS>] [--limit <N>] [--offset <N>] [--created-after <RFC3339>] [--created-before <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--status <STATUS>` | pending, running, awaiting_event, succeeded, failed or dead |  |
| `--limit <N>` | page size (1..100, default 50) |  |
| `--offset <N>` | page offset |  |
| `--created-after <RFC3339>` | inclusive RFC3339 creation-time start |  |
| `--created-before <RFC3339>` | inclusive RFC3339 creation-time end |  |

### automations diagnose

Diagnose a run belonging to the selected automation

`gregale automations diagnose --app <SLUG> --name <NAME> --run <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--run <UUID>` | workflow run UUID | required |

### automations history

Inspect retained recovery automation history

`gregale automations history --app <SLUG> [--workflow <NAME>] [--run <UUID>] [--policy-version <N>] [--created-after <RFC3339>] [--created-before <RFC3339>] [--after <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--workflow <NAME>` | native workflow filter |  |
| `--run <UUID>` | native run filter |  |
| `--policy-version <N>` | policy version filter |  |
| `--created-after <RFC3339>` | inclusive start |  |
| `--created-before <RFC3339>` | inclusive end |  |
| `--after <CURSOR>` | next page cursor |  |
| `--limit <N>` | page size (default 25, max 100) |  |

### automations health

Inspect app recovery health or named automation run reliability

`gregale automations health [--stall-after-seconds <SECONDS>] --app <SLUG> [--name <NAME>] [--created-after <RFC3339>] [--created-before <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--stall-after-seconds <SECONDS>` | app recovery evaluation threshold (default 300) |  |
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name |  |
| `--created-after <RFC3339>` | inclusive RFC3339 window start (max 30 days) |  |
| `--created-before <RFC3339>` | inclusive RFC3339 window end |  |

### automations report

Summarize workflow recovery outcomes over a time window

`gregale automations report --app <SLUG> [--workflow <NAME>] [--policy-version <N>] [--since <DURATION>] [--created-after <RFC3339>] [--created-before <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--workflow <NAME>` | workflow name filter |  |
| `--policy-version <N>` | policy version filter |  |
| `--since <DURATION>` | lookback, at most 30 days (default 7d) |  |
| `--created-after <RFC3339>` | inclusive start; excludes --since |  |
| `--created-before <RFC3339>` | inclusive end |  |

### automations recover

Resume selected failed runs with per-run generation checks

`gregale automations recover [--plan-out <PATH>] --app <SLUG> --name <NAME> --run <UUID:COUNT> --idempotency-key <KEY>`

| Flag | Meaning | |
|---|---|---|
| `--plan-out <PATH>` | save reviewed selection to a new plan file without resuming |  |
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--run <UUID:COUNT>` | run UUID and original expected resume count; repeatable, at most 25 | required |
| `--idempotency-key <KEY>` | stable batch key; reuse for interrupted retries | required |

Reports each run&#39;s outcome.

### automations event-recovery-plan

Save selected retained automation routing actions for review

`gregale automations event-recovery-plan --app <SLUG> --name <NAME> --source <SOURCE> --event <ID> --idempotency-key <KEY> --out <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--source <SOURCE>` | event source | required |
| `--event <ID>` | selected event ID; repeatable, at most 25 | required |
| `--idempotency-key <KEY>` | stable batch key | required |
| `--out <PATH>` | new event plan file | required |

### automations recovery-health

Inspect recovery execution attention and explicit action hints

`gregale automations recovery-health --app <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |

### automations recovery-policy

Inspect, preview or update recovery policy

`gregale automations recovery-policy --app <SLUG> [--file <PATH>] [--preview] [--queue] [--control <pause|resume>] [--reason <TEXT>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--file <PATH>` | policy JSON with expected_version |  |
| `--preview` | preview --file without changing policy |  |
| `--queue` | inspect automatic retry queue |  |
| `--control <pause|resume>` | control automatic execution |  |
| `--reason <TEXT>` | operator control reason |  |

### automations recovery-policy-evaluate

Compare a proposed recovery policy with recent history

`gregale automations recovery-policy-evaluate --app <SLUG> --file <PATH> [--workflow <NAME>] [--since <DURATION>] [--created-after <RFC3339>] [--created-before <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--file <PATH>` | proposed policy JSON with expected_version | required |
| `--workflow <NAME>` | only sample this workflow |  |
| `--since <DURATION>` | lookback, at most 30 days (default 7d) |  |
| `--created-after <RFC3339>` | inclusive start; excludes --since |  |
| `--created-before <RFC3339>` | inclusive end |  |

### automations recovery-suppress

Stop one queued automatic retry

`gregale automations recovery-suppress --app <SLUG> --review <UUID> --action <UUID> --policy-version <N> --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--review <UUID>` | review ID | required |
| `--action <UUID>` | queued action ID | required |
| `--policy-version <N>` | observed policy version | required |
| `--reason <TEXT>` | why the retry is stopped | required |

### automations recovery-policy-reviews

Inspect policy decisions and export prepared plans

`gregale automations recovery-policy-reviews --app <SLUG> [--review <UUID>] [--plan-out <PATH>] [--after <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--review <UUID>` | retained review |  |
| `--plan-out <PATH>` | export selected prepared plan |  |
| `--after <UUID>` | review pagination cursor |  |
| `--limit <N>` | page limit |  |

### automations recovery-response-plan

Save reviewed cancellation and retry actions

`gregale automations recovery-response-plan --app <SLUG> --job <UUID> --key <KEY> --reason <TEXT> --plan-out <PATH> [--cancel-position <N>] [--retry-position <N>] [--rate <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--job <UUID>` | native recovery job | required |
| `--key <KEY>` | stable response key | required |
| `--reason <TEXT>` | operator reason | required |
| `--plan-out <PATH>` | new saved plan | required |
| `--cancel-position <N>` | item to cancel (repeatable) |  |
| `--retry-position <N>` | saved failed item to retry (repeatable) |  |
| `--rate <N>` | retry admission rate |  |

### automations recovery-response-preview

Recheck saved actions without applying

`gregale automations recovery-response-preview --plan <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | saved response plan | required |

### automations recovery-response-apply

Apply saved actions with durable receipts

`gregale automations recovery-response-apply --plan <PATH> --yes`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | saved response plan | required |
| `--yes` | confirm reviewed actions | required |

### automations recovery-response-status

Reconcile retained action receipts and execution outcomes

`gregale automations recovery-response-status --plan <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | saved response plan | required |

### automations recovery-cancel-preview

Review or cancel one admitted recovery generation

`gregale automations recovery-cancel-preview --app <SLUG> --job <UUID> --position <N> --resume-count <N> --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--job <UUID>` | native recovery job | required |
| `--position <N>` | frozen item position | required |
| `--resume-count <N>` | reviewed admitted generation | required |
| `--reason <TEXT>` | operator reason | required |

### automations recovery-cancel

Review or cancel one admitted recovery generation

`gregale automations recovery-cancel --app <SLUG> --job <UUID> --position <N> --resume-count <N> --reason <TEXT> --yes`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--job <UUID>` | native recovery job | required |
| `--position <N>` | frozen item position | required |
| `--resume-count <N>` | reviewed admitted generation | required |
| `--reason <TEXT>` | operator reason | required |
| `--yes` | confirm exact generation cancellation | required |

### automations failures

List captured initial workflow failures

`gregale automations failures --app <SLUG> [--name <NAME>] [--after <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | workflow filter |  |
| `--after <CURSOR>` | next page cursor |  |
| `--limit <N>` | page size (at most 100) |  |

### automations recovery-retry-evidence

Diagnose or repair verified automatic retry evidence

`gregale automations recovery-retry-evidence --app <SLUG> --review <UUID> --action <UUID> [--parent] [--repair] [--request-id <UUID>] [--reason <TEXT>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--review <UUID>` | automatic retry review | required |
| `--action <UUID>` | automatic retry action | required |
| `--parent` | inspect or repair pending retry parent evidence |  |
| `--repair` | save a verified terminal outcome |  |
| `--request-id <UUID>` | stable repair request identifier |  |
| `--reason <TEXT>` | operator repair reason |  |

### automations failure-recovery-evidence

Diagnose or repair verified initial recovery evidence

`gregale automations failure-recovery-evidence --app <SLUG> --review <UUID> [--repair] [--request-id <UUID>] [--reason <TEXT>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--review <UUID>` | initial recovery review | required |
| `--repair` | save a verified terminal outcome |  |
| `--request-id <UUID>` | stable repair request identifier |  |
| `--reason <TEXT>` | operator repair reason |  |

### automations failure-recovery-reviews

List retained durable first recovery reviews

`gregale automations failure-recovery-reviews --app <SLUG> [--after <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--after <UUID>` | next page cursor |  |
| `--limit <N>` | page size |  |

### automations failure-recovery-review

Prepare, inspect, submit or cancel a durable first recovery review

`gregale automations failure-recovery-review --app <SLUG> [--failure <UUID>] [--expected-policy-version <N>] [--review <UUID>] [--decision <DECISION>] [--reason <TEXT>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--failure <UUID>` | captured initial failure |  |
| `--expected-policy-version <N>` | saved policy version |  |
| `--review <UUID>` | durable review |  |
| `--decision <DECISION>` | submit or cancel |  |
| `--reason <TEXT>` | operator reason |  |

### automations failure-recovery-plan

Preview an initial failure and save a reviewed first recovery plan

`gregale automations failure-recovery-plan --app <SLUG> --failure <UUID> [--policy <PATH>] [--plan-out <PATH>] [--key <KEY>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--failure <UUID>` | captured initial failure | required |
| `--policy <PATH>` | candidate policy dry run |  |
| `--plan-out <PATH>` | new reviewed recovery plan |  |
| `--key <KEY>` | stable recovery key |  |

### automations recovery-retry-plan

Review recovery failures and save an explicit retry plan

`gregale automations recovery-retry-plan --app <SLUG> --job <UUID> [--position <N>] [--plan-out <PATH>] [--key <KEY>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--job <UUID>` | parent recovery job | required |
| `--position <N>` | reviewed parent item position (repeatable) |  |
| `--plan-out <PATH>` | new reviewed retry plan file |  |
| `--key <KEY>` | stable idempotency key for the retry plan |  |

### automations recovery-start

Submit a reviewed automation run plan as a durable server job

`gregale automations recovery-start --plan <PATH> [--rate <N>] [--execution-deadline <DURATION>] [--execution-stall <DURATION>] [--reason <TEXT>]`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | reviewed run plan | required |
| `--rate <N>` | maximum recoveries per second (1..100) |  |
| `--execution-deadline <DURATION>` | advisory execution deadline (zero disables) |  |
| `--execution-stall <DURATION>` | no-progress warning threshold (default 15m) |  |
| `--reason <TEXT>` | optional operator reason |  |

### automations event-recovery-start

Submit a reviewed automation event plan as a durable server job

`gregale automations event-recovery-start --plan <PATH> [--rate <N>] [--reason <TEXT>]`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | reviewed event plan | required |
| `--rate <N>` | maximum recoveries per second (1..100) |  |
| `--reason <TEXT>` | optional operator reason |  |

### automations event-recover

Execute a reviewed event plan with fresh receipt checks and a journal

`gregale automations event-recover --plan <PATH> --journal <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | reviewed event plan | required |
| `--journal <PATH>` | new execution journal | required |

### automations recovery-plan

Execute a saved recovery plan with a durable per-attempt journal

`gregale automations recovery-plan --plan <PATH> --journal <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | reviewed recovery plan file | required |
| `--journal <PATH>` | new journal file for this execution | required |

### automations recovery-preview

Inspect failed-run recovery and schedule catch-up without writes

`gregale automations recovery-preview --app <SLUG> --name <NAME> --from <TIME> [--until <TIME>] [--limit <N>] [--run-offset <N>] [--event-after <CURSOR>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--from <TIME>` | RFC3339 event acceptance and schedule preview start | required |
| `--until <TIME>` | RFC3339 end/evaluation time (default now) |  |
| `--limit <N>` | runs per status and event scan page (1..25, default 10) |  |
| `--run-offset <N>` | offset in each failed/dead run page |  |
| `--event-after <CURSOR>` | retained event preview cursor |  |

Includes retained events.

### automations failure-policy

Inspect failure pauses and history, or configure monitoring

`gregale automations failure-policy --app <SLUG> --name <NAME> [--enabled <BOOL>] [--expected-version <N>] [--failure-threshold <N>] [--min-completed-runs <N>] [--window-seconds <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--enabled <BOOL>` | enable or disable monitoring; required when configuring |  |
| `--expected-version <N>` | current failure policy version; required when configuring |  |
| `--failure-threshold <N>` | terminal failures needed to pause (1..10000, default 3) |  |
| `--min-completed-runs <N>` | minimum completed outcomes (1..10000, default 5) |  |
| `--window-seconds <N>` | observation window (60..86400, default 300) |  |

Also previews a resume.

### automations failure-resume

Preview retained work and explicitly clear a failure pause

`gregale automations failure-resume --app <SLUG> --name <NAME> --expected-generation <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--expected-generation <N>` | current failure pause generation | required |

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

Trace an automation with sample input and mocked outputs

`gregale automations simulate --app <SLUG> --file <PATH> [--input-file <PATH>] [--mock-outputs-file <PATH>] [--mock-item-outputs-file <PATH>] [--mock-attempts-file <PATH>] [--mock-item-attempts-file <PATH>] [--require-complete]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--file <PATH>` | YAML or JSON definition file | required |
| `--input-file <PATH>` | sample workflow input JSON file |  |
| `--mock-outputs-file <PATH>` | JSON object of action outputs keyed by step name |  |
| `--mock-item-outputs-file <PATH>` | JSON object of for_each output arrays keyed by step name |  |
| `--mock-attempts-file <PATH>` | JSON object of ordered attempt outcomes keyed by step name |  |
| `--mock-item-attempts-file <PATH>` | JSON object mapping loops and zero-based item indexes to attempt outcomes |  |
| `--require-complete` | fail if mocks leave steps unresolved |  |

Does not run steps.

### automations apply

Save an automation definition as a draft

`gregale automations apply --app <SLUG> --file <PATH> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--file <PATH>` | YAML or JSON definition file | required |
| `--expected-version <N>` | current version; use 0 for a new draft | required |

### automations publish-policy

Inspect or set required server publishing checks

`gregale automations publish-policy --app <SLUG> [--mode <MODE>] [--expected-version <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--mode <MODE>` | set publishing requirement (admin scope) | one of `optional` · `scenarios` · `coverage` |
| `--expected-version <N>` | current policy version (required when setting mode) |  |

### automations publish

Publish the current automation draft

`gregale automations publish --app <SLUG> --name <NAME> --expected-version <N> [--take-over-manifest] [--scenarios <PATH>] [--require-coverage]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | automation name | required |
| `--expected-version <N>` | current version of the draft | required |
| `--take-over-manifest` | explicitly take over YAML ownership |  |
| `--scenarios <PATH>` | check scenarios against the saved draft before publishing |  |
| `--require-coverage` | require coverage before publishing (requires --scenarios) |  |


## workflows

Manage durable execution workflows

`gregale workflows [<subcommand>]`

### workflows defaults

Show or manage project-local workflow recovery selectors

Examples:

```sh
gregale workflows defaults
gregale workflows defaults --help
```

#### workflows defaults set

Save a preferred workflow name and recorded app locally

`gregale workflows defaults set --app <SLUG> [--dry-run] <name>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | recorded app slug for recovery; target existence is not checked | required |
| `--dry-run` | preview destination, before/after selectors, and proposed recovery readiness without saving |  |

Examples:

```sh
gregale workflows defaults set order-processing --app payments-api
gregale workflows defaults set order-processing --app payments-api --dry-run
```

#### workflows defaults show

Show saved workflow and app selectors and recovery readiness

`gregale workflows defaults show`

Includes the preference file path.

#### workflows defaults update

Change saved workflow selectors while preserving omitted settings

`gregale workflows defaults update [--app <SLUG>] [--dry-run] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | replace the saved recorded app selector |  |
| `--dry-run` | preview updated selectors and local recovery readiness without saving |  |

Examples:

```sh
gregale workflows defaults update --app billing-api --dry-run
gregale workflows defaults update nightly-report --dry-run
```

#### workflows defaults clear

Clear workflow recovery preferences without deleting run records

`gregale workflows defaults clear [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview saved selectors and removal effects without deleting files |  |

Examples:

```sh
gregale workflows defaults clear --dry-run
```

### workflows resume-wait

Recover a saved Workflow run: observe or read diagnostics

`gregale workflows resume-wait [--dry-run] [--run <UUID|PREFIX>] [--list] [--overview] [--current-connection] [--forget <UUID>] [--timeout <DURATION>] [--poll-interval <DURATION>] [--saved-workflow] [--latest] [--app <SLUG>] [--workflow <NAME>] [--diagnose]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview the selected run and action offline; no waiting, API access, or file writes |  |
| `--run <UUID|PREFIX>` | saved run UUID or unique prefix (at least 8 characters), resolved on the current profile/API |  |
| `--list` | list saved identifiers without API access |  |
| `--overview` | group recorded statuses into in progress, finished, and unavailable; retains records |  |
| `--current-connection` | filter --list or --overview by current profile and API; retain all records |  |
| `--forget <UUID>` | remove one local record only; requires its recorded API/profile, makes no requests |  |
| `--timeout <DURATION>` | maximum wait (default 10m) or overview duration (default 30s); positive |  |
| `--poll-interval <DURATION>` | poll interval (default 2s; 1s to 1h) |  |
| `--saved-workflow` | use the workflow name and recorded app from local defaults; excludes --workflow, --app, and --forget |  |
| `--latest` | select the most recently recorded workflow/app run from explicit selectors or --saved-workflow on the current profile/API |  |
| `--app <SLUG>` | filter local workflow records by exact recorded app on the current profile/API; supports --list and --overview |  |
| `--workflow <NAME>` | filter saved runs by exact Workflow name on the current profile/API; supports --list and --overview |  |
| `--diagnose` | read exact recorded workflow diagnostics; accepts --workflow and --run |  |

Examples:

```sh
gregale workflows resume-wait --saved-workflow --latest --dry-run
gregale workflows resume-wait --list
gregale workflows resume-wait --workflow order-processing --overview
gregale workflows resume-wait --workflow order-processing --diagnose
gregale workflows resume-wait --run RUN_ID
```

### workflows list

List workflow runs for an app

`gregale workflows list [--app <SLUG>] [--limit <N>] [--offset <N>] [--all] [--status <STATUS>] [--workflow-name <NAME>] [--created-after <RFC3339>] [--created-before <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (defaults to linked project app) |  |
| `--limit <N>` | page size (1..100) |  |
| `--offset <N>` | starting page offset |  |
| `--all` | walk all matching pages from the selected offset |  |
| `--status <STATUS>` | filter by workflow run status |  |
| `--workflow-name <NAME>` | filter by exact workflow name |  |
| `--created-after <RFC3339>` | inclusive RFC3339 creation-time start |  |
| `--created-before <RFC3339>` | inclusive RFC3339 creation-time end |  |

Examples:

```sh
gregale workflows list --workflow-name nightly-report --all
gregale workflows list --app billing --workflow-name paid-invoice --status failed
gregale workflows list --app billing --created-after 2026-10-01T00:00:00Z --created-before 2026-10-05T23:59:59Z
```

### workflows schedules

Inspect recurring workflow schedules and preview their next fires

`gregale workflows schedules <preview> --app <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |

#### workflows schedules preview

Simulate upcoming fires and the next catch-up decision

`gregale workflows schedules preview --app <SLUG> --workflow <NAME> [--at <RFC3339>] [--since <RFC3339>] [--count <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--workflow <NAME>` | schedule workflow name | required |
| `--at <RFC3339>` | hypothetical evaluator time |  |
| `--since <RFC3339>` | simulated previous evaluation time |  |
| `--count <N>` | upcoming fires to return (1..20, default 5) |  |

Examples:

```sh
gregale workflows schedules preview --app billing --workflow nightly
gregale workflows schedules preview --app billing --workflow nightly --at 2027-03-28T00:00:00Z --count 8
```

### workflows schedule-history

Inspect recurring workflow admission history

`gregale workflows schedule-history <replay-preview|replay> --app <SLUG> [--platform-tenant-id <UUID>] [--cursor <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--platform-tenant-id <UUID>` | filter by tenant UUID |  |
| `--cursor <UUID>` | next cursor from the previous page |  |
| `--limit <N>` | maximum occurrences (1-200) |  |

#### workflows schedule-history replay-preview

Check skipped occurrences against definitions, overlap and quota

`gregale workflows schedule-history replay-preview --app <SLUG> --occurrence-id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--occurrence-id <UUID>` | skipped occurrence UUID (repeat up to 20 times) | required |

Examples:

```sh
gregale workflows schedule-history replay-preview --app billing --occurrence-id <uuid>
```

#### workflows schedule-history replay

Replay eligible skipped occurrences with the current workflow

`gregale workflows schedule-history replay --app <SLUG> --occurrence-id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--occurrence-id <UUID>` | skipped occurrence UUID (repeat up to 20 times) | required |

Examples:

```sh
gregale workflows schedule-history replay --app billing --occurrence-id <uuid>
```

Normal quota applies.

### workflows run

Trigger a new workflow run

`gregale workflows run [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] [--app <slug>] [--input <JSON>] [--input-file <PATH|->] [--idempotency-key <KEY>] <workflow-name>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | follow the accepted run to success without resubmitting |  |
| `--timeout <DURATION>` | wait deadline after acceptance (default 5m; requires --wait) |  |
| `--poll-interval <DURATION>` | wait read interval (default 2s; 1s to 1h; requires --wait) |  |
| `--app <slug>` | app slug (defaults to linked project app) |  |
| `--input <JSON>` | JSON input payload (default {}; excludes --input-file) |  |
| `--input-file <PATH|->` | read one JSON value from a file, or - for stdin; maximum 1 MiB; excludes --input |  |
| `--idempotency-key <KEY>` | stable key for retrying an uncertain run start |  |

Examples:

```sh
gregale workflows run nightly-report --input-file input.json --wait
gregale workflows run nightly-report --input-file input.json
gregale workflows run nightly-report --input-file -
```

### workflows wait

Wait for an exact or latest workflow run to succeed

`gregale workflows wait [--app <SLUG>] [--workflow-name <NAME>] [--timeout <DURATION>] [--poll-interval <DURATION>] <run_id|latest>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app for latest (defaults to linked project app) |  |
| `--workflow-name <NAME>` | exact workflow name for latest (inferred when the app&#39;s recent runs share one) |  |
| `--timeout <DURATION>` | maximum wait (default 5m; positive) |  |
| `--poll-interval <DURATION>` | read interval (default 2s; 1s to 1h) |  |

Examples:

```sh
gregale workflows wait RUN_ID --timeout 5m
gregale workflows wait latest --workflow-name billing --timeout 5m
```

Exits nonzero on failure or timeout.

### workflows status

Show details of a workflow run

`gregale workflows status [--check] [--interactive] [--app <slug>] [--workflow-name <NAME>] [<run_id|latest>]`

| Flag | Meaning | |
|---|---|---|
| `--check` | exit 0 only for a succeeded, non-cancelled run (explicit read only) |  |
| `--interactive` | choose a run for inspection |  |
| `--app <slug>` | app for latest or interactive selection (defaults to linked project app) |  |
| `--workflow-name <NAME>` | exact workflow name for latest (inferred when the app&#39;s recent runs share one) |  |

Examples:

```sh
gregale workflows status RUN_ID --check
gregale workflows status latest --workflow-name nightly-report
gregale workflows status --interactive
gregale workflows status --app billing --interactive
gregale workflows status <run_id>
```

### workflows diagnose

Inspect queue reasons and preview recovery without changing the run

`gregale workflows diagnose [--app <SLUG>] [--workflow-name <NAME>] <run_id|latest>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug for latest (defaults to linked project app) |  |
| `--workflow-name <NAME>` | exact workflow name for latest (inferred when the app&#39;s recent runs share one) |  |

Examples:

```sh
gregale workflows diagnose latest --workflow-name nightly-report
gregale workflows diagnose <run_id>
gregale --json workflows diagnose <run_id>
```

### workflows steps

List steps for an exact or latest workflow run

`gregale workflows steps [--failed-only] [--app <SLUG>] [--workflow-name <NAME>] <run_id|latest>`

| Flag | Meaning | |
|---|---|---|
| `--failed-only` | show only failed steps with inspection commands |  |
| `--app <SLUG>` | app slug for latest (defaults to linked project app) |  |
| `--workflow-name <NAME>` | exact workflow name for latest (inferred when the app&#39;s recent runs share one) |  |

Examples:

```sh
gregale workflows steps latest --workflow-name billing --failed-only
gregale workflows steps latest --workflow-name nightly-report
```

### workflows attempts

List a step&#39;s retry attempts and managed effect delivery

`gregale workflows attempts [--failed-only] [--app <SLUG>] [--workflow-name <NAME>] <run_id|latest> <step_name>`

| Flag | Meaning | |
|---|---|---|
| `--failed-only` | show only failed attempts |  |
| `--app <SLUG>` | app for latest (defaults to linked project app) |  |
| `--workflow-name <NAME>` | exact workflow name for latest (inferred when the app&#39;s recent runs share one) |  |

Examples:

```sh
gregale workflows attempts latest charge-card --workflow-name billing --failed-only
gregale workflows attempts latest charge-card --workflow-name billing
```

### workflows retry

Retry one safely resumable failed HTTP step

`gregale workflows retry [--interactive] [--app <slug>] [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] [<run_id>] [<step_name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a run; preview and confirm changes for retry or cancel |  |
| `--app <slug>` | app for interactive run selection (otherwise use linked app or picker) |  |
| `--wait` | follow the retried run to success (explicit run and step only) |  |
| `--timeout <DURATION>` | wait deadline after retry acceptance (default 5m; requires --wait) |  |
| `--poll-interval <DURATION>` | wait read interval (default 2s; 1s to 1h; requires --wait) |  |

Examples:

```sh
gregale workflows retry --app billing --interactive
gregale workflows retry <run_id> <step_name>
gregale workflows retry RUN_ID STEP_NAME --wait
```

### workflows resume

Resume eligible failed actions in a workflow run

`gregale workflows resume [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] --expected-resume-count <N> [--idempotency-key <KEY>] <run_id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | follow the resumed run to success without another resume request |  |
| `--timeout <DURATION>` | wait deadline after resume acceptance (default 5m; requires --wait) |  |
| `--poll-interval <DURATION>` | wait read interval (default 2s; 1s to 1h; requires --wait) |  |
| `--expected-resume-count <N>` | current resume_count shown by workflows status | required |
| `--idempotency-key <KEY>` | stable key for retrying the same resume request |  |

Examples:

```sh
gregale workflows resume RUN_ID --expected-resume-count 1 --wait
```

### workflows resumes

List continuation history for an exact or latest workflow run

`gregale workflows resumes [--app <SLUG>] [--workflow-name <NAME>] <run_id|latest>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app for latest (defaults to linked project app) |  |
| `--workflow-name <NAME>` | exact workflow name for latest (inferred when the app&#39;s recent runs share one) |  |

Examples:

```sh
gregale workflows resumes latest --workflow-name billing
```

### workflows cancel

Cancel an active workflow run

`gregale workflows cancel [--interactive] [--app <slug>] [<run_id>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a run; preview and confirm changes for retry or cancel |  |
| `--app <slug>` | app for interactive run selection (otherwise use linked app or picker) |  |

Examples:

```sh
gregale workflows cancel --app billing --interactive
gregale workflows cancel <run_id>
```

### workflows cancel-queued-preview

Preview selected runs that are still pending and have never started

`gregale workflows cancel-queued-preview --app <SLUG> [--workflow-name <NAME>] --run-id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--workflow-name <NAME>` | require an exact workflow name |  |
| `--run-id <UUID>` | selected run UUID (repeat up to 20 times) | required |

Examples:

```sh
gregale workflows cancel-queued-preview --app billing --run-id <uuid> --run-id <uuid>
```

### workflows cancel-queued

Cancel selected runs that remain pending and have never started

`gregale workflows cancel-queued --app <SLUG> [--workflow-name <NAME>] --run-id <UUID> --yes`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--workflow-name <NAME>` | require an exact workflow name |  |
| `--run-id <UUID>` | selected run UUID (repeat up to 20 times) | required |
| `--yes` | confirm cancellation of eligible selected runs | required |

Examples:

```sh
gregale workflows cancel-queued --app billing --run-id <uuid> --yes
```

### workflows events

Send external event to a workflow run

`gregale workflows events [--payload <JSON>] [--payload-file <PATH|->] <run_id> <event_name>`

| Flag | Meaning | |
|---|---|---|
| `--payload <JSON>` | JSON event payload (default {}; excludes --payload-file) |  |
| `--payload-file <PATH|->` | read one JSON value from a file, or - for stdin; request maximum 1 MiB; excludes --payload |  |

Examples:

```sh
gregale workflows events RUN_ID approved --payload-file approval.json
gregale workflows events RUN_ID approved --payload-file -
```


## dashboard

Open the account dashboard in your browser

`gregale dashboard [--stateless]`

| Flag | Meaning | |
|---|---|---|
| `--stateless` | open the stateless-advisory landing page instead of the account page |  |


## doctor

Check local source or an image for problems before deploying

`gregale doctor [--path <DIR>] [--image <REF>] [--registry-user <USER>] [--registry-password-stdin] [--strict] [--json]`

Checks local source or image metadata only; runtime checks are skipped.

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | source directory (default: current directory; a positional DIR also works) |  |
| `--image <REF>` | inspect the Linux/amd64 image without downloading layers |  |
| `--registry-user <USER>` | registry username; requires --registry-password-stdin |  |
| `--registry-password-stdin` | read registry password/token from stdin; requires --image and --registry-user |  |
| `--strict` | exit 1 on warn (default: exit 0 on warn) |  |
| `--json` | machine output (default: human prose) |  |

Examples:

```sh
gregale doctor
gregale doctor --strict
gregale doctor --path packages/api
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

`gregale delayed-task get [--app <SLUG>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | require the task to belong to this app |  |

### delayed-task info

Alias for get

`gregale delayed-task info [--app <SLUG>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | require the task to belong to this app |  |

### delayed-task cancel

Cancel a delayed task

`gregale delayed-task cancel [--app <SLUG>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | require the task to belong to this app |  |


## deployments

List an app&#39;s deployments and manage named revision URLs

See also: [`deploy`](#deploy), [`deploys`](#deploys), [`deployment`](#deployment).

`gregale deployments [<subcommand>] [--app <slug>] [--failed-only] [--limit <N>] [--before <CURSOR>] [--cursor <CURSOR>] [--all] [--wide]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (app-scoped deployment history) |  |
| `--failed-only` | show only failed deployments in fetched history |  |
| `--limit <N>` | page size (1-200) |  |
| `--before <CURSOR>` | alias for --cursor |  |
| `--cursor <CURSOR>` | opaque cursor from a prior page |  |
| `--all` | walk every page |  |
| `--wide` | include the revision (vN) and annotation columns (by / pr / tag / reason) |  |

Examples:

```sh
gregale deployments my-api --limit 10
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

Inspect one release, wait for its rollout, or advance a canary

See also: [`deploy`](#deploy), [`deploys`](#deploys), [`deployments`](#deployments).

`gregale deployment [<subcommand>] <id|vN|latest> [--app <SLUG>] [--show-scan] [--show-secret-scan] [--min <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug for vN or latest outside a linked project |  |
| `--show-scan` | include the per-deploy grype scan payload |  |
| `--show-secret-scan` | include the per-deploy image-layer secret-scan payload |  |
| `--min <N>` | min_instances floor (&gt;= 0) |  |

Examples:

```sh
gregale deployment v42 --app my-api
gregale deployment v42 --app my-api --json
gregale deployment summary v42 --app my-api
gregale deployment wait v42 --app my-api
gregale deployment latest --app my-api
```

### deployment retry-hold

Schedule an MCP promotion hold retry with all activation checks

`gregale deployment retry-hold [--app <SLUG>] <ID|vN>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug, to resolve a vN revision |  |

Examples:

```sh
gregale deployment retry-hold v42 --app my-api
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

### deployment diff

Show what changed since the previous release

`gregale deployment diff [--against <ID|vN>] [--app <SLUG>] <id|vN>`

| Flag | Meaning | |
|---|---|---|
| `--against <ID|vN>` | release to compare with (default: the release that served before it) |  |
| `--app <SLUG>` | app slug, to resolve vN revisions |  |

Examples:

```sh
gregale deployment diff v42 --app my-api
gregale deployment diff v42 --against v38 --app my-api
```

Covers code, config keys, packages, runtime settings and the release command.

### deployment summary

Show the release diff and rollback target

`gregale deployment summary [--interactive] [--app <SLUG>] [<id|vN>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a release from app history and show its summary |  |
| `--app <SLUG>` | app slug (defaults to linked context or interactive picker) |  |

Examples:

```sh
gregale deployment summary --app my-api --interactive
gregale deployment summary v42 --app my-api
gregale deployment summary v42 --app my-api --json
```

### deployment wait

Wait until a deployment is live (or safe rollout completes)

`gregale deployment wait [--app <SLUG>] [--rollout] [--progress] [--timeout <SECONDS|DURATION>] <id|vN|latest>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug for vN or latest outside a linked project |  |
| `--rollout` | wait for safe rollout to reach 100% traffic |  |
| `--progress` | print rollout transitions while waiting (human output only) |  |
| `--timeout <SECONDS|DURATION>` | maximum wait (seconds, or a duration such as 10m) |  |

Examples:

```sh
gregale deployment wait <id|vN> --app my-api
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

Follow one deployment&#39;s stages and logs, or cancel or retry it

See also: [`deploy`](#deploy), [`deployment`](#deployment), [`deployments`](#deployments).

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

### deploys logs

Print one deployment&#39;s build and deploy log

`gregale deploys logs [--app <SLUG>] [--limit <N>] <id|vN>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--limit <N>` | most recent log lines to print (1-5000) |  |

Examples:

```sh
gregale deploys logs v42 --app my-api
```

### deploys cancel

Cancel one pending deployment

`gregale deploys cancel [--app <slug>] [--reason <REASON>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to the linked project, or the deployment&#39;s own app) |  |
| `--reason <REASON>` | cancel reason (default user) | one of `user` · `auto_quota` · `auto_health` · `system` |

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

Retry a failed deployment from a specific stage

`gregale deploys retry [--from <STAGE>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--from <STAGE>` | retry from this stage (defaults to the failing stage) | one of `source_download` · `dependency_restore` · `image_build` · `security_scan` · `snapshot_prepare` · `readiness` |

Choose the stage with --from.


## deploy

Deploy an app, function, or project

See also: [`deploys`](#deploys), [`deployment`](#deployment), [`deployments`](#deployments).

`gregale deploy [<subcommand>] [--atomic] [--add-services] [--verify] [--workers] [--stabilize] [--progressive] [--worker-health] [--migrate] [--release-secrets-file <PATH>] [--release-secrets-request-id <ID>] [--config-file <PATH>] [--image <REF>] [--tarball <PATH>] [--path <DIR>] [--source <auto|head|worktree>] [--worktree] [--repo <OWNER/NAME>] [--repository <OWNER/NAME>] [--install-id <N>] [--production-branch <BRANCH>] [--ref <REF>] [--source-branch <BRANCH>] [--github] [--pinned-sha <SHA>] [--pin-action] [--template <NAME>] [--dockerfile] [--issue-source-maps <PATH>] [--issue-source-map-directory <PATH>] [--runtime <RUNTIME>] [--handler <HANDLER>] [--name <SLUG>] [--size <SIZE>] [--profile <SIZE>] [--vcpu <N>] [--execution-mode <MODE>] [--restart-policy <POLICY>] [--startup-deadline-s <SECONDS>] [--max-retries <N>] [--function] [--app] [--only <SLUGS>] [--project] [--environment <SLUG>] [--reason <text>] [--tag <TAG>] [--deployed-by <NAME>] [--pr-number <N>] [--exclude <SLUGS>] [--show-affected] [--persist-exclude] [--project-slug <SLUG>] [--rollout <MODE>] [--override-freeze <REASON>] [--at <WHEN>] [--release-notify <CHANNEL,...>] [--canary-preset <PRESET>] [--canary-stages <STAGES>] [--safe] [--require-authn] [--no-require-authn] [--platform-tenant-required] [--no-platform-tenant-required] [--app-protocol <PROTOCOL>] [--traffic-percent <PERCENT>] [--no-traffic] [--rollback-on-5xx] [--queue-canary-binding <NAME>]... [--healthcheck-path <PATH>] [--healthcheck-grpc] [--healthcheck-grpc-service <SERVICE>] [--disable-startup-cpu-boost] [--mcp-task-migration-override] [--no-triggers] [--wait] [--no-wait] [--create-only] [--timeout <DURATION>] [--idempotency-key <KEY>] [--secrets-file <PATH>] [--secret-scan <on|off>] [--diff] [--preview] [--plan] [--dry-run] [--yes] [--strict] [--lenient] [--server-diff] [--doctor-strict] [--no-doctor]`

Source defaults to committed HEAD when origin exists; otherwise local files. Use --source=worktree to include local changes.

RAM/CPU: use --size SIZE (--profile SIZE after the command is deprecated). Connection: use --profile NAME before the command.

| Flag | Meaning | |
|---|---|---|
| `--atomic` | publish an existing HTTP project only after all zero-traffic candidates are ready; requires --project |  |
| `--add-services` | reserve new HTTP services and publish them with the atomic project release |  |
| `--verify` | verify critical probe routes after atomic cutover and automatically restore the previous project on regression; requires --atomic |  |
| `--workers` | include one push-queue worker in a verified atomic project release |  |
| `--stabilize` | monitor health for ten minutes after progressive rollout succeeds |  |
| `--progressive` | route project traffic at 5%, 25%, then 100%; requires paired configuration and verification |  |
| `--worker-health` | require real worker job health; requires --workers |  |
| `--migrate` | run one backward-compatible migration before verified atomic cutover |  |
| `--release-secrets-file <PATH>` | rotate complete production secret snapshots with atomic verified HTTP source |  |
| `--release-secrets-request-id <ID>` | replay the printed encrypted staging request |  |
| `--config-file <PATH>` | activate non-secret workload variables with an atomic verified release |  |
| `--image <REF>` | deploy a digest-pinned container image |  |
| `--tarball <PATH>` | deploy from a source tarball |  |
| `--path <DIR>` | source directory (default: current directory) |  |
| `--source <auto|head|worktree>` | local source policy (default: auto) | one of `auto` · `head` · `worktree` |
| `--worktree` | deploy the selected source directory from the working tree, including local changes |  |
| `--repo <OWNER/NAME>` | deploy a GitHub repository (requires --ref) |  |
| `--repository <OWNER/NAME>` | bind a project to GitHub (requires --install-id) |  |
| `--install-id <N>` | GitHub installation ID (requires --repository) |  |
| `--production-branch <BRANCH>` | production branch for a project binding (default: main) |  |
| `--ref <REF>` | branch, tag, or 40-character SHA (required with --repo) |  |
| `--source-branch <BRANCH>` | reject promotion if the branch for a pinned --ref moves |  |
| `--github` | emit a GitHub Actions workflow snippet for the Gregale deploy action |  |
| `--pinned-sha <SHA>` | with --github only, pin the generated Action to this full 40-character commit SHA |  |
| `--pin-action` | with --github only, resolve the current v0 Action tag to its commit SHA |  |
| `--template <NAME>` | scaffold from a built-in template | one of `hello-node` · `hello-python` · `hello-go` · `cron-example` · `function-node` · `function-python` · `function-go` · `function-node24` · `function-python313` · `function-rust` · `event-worker` · `queue-worker` · `s3-uploader` · `object-storage-node` · `object-storage-python` · `slack-bot` · `rest-api-postgres` · `rest-api-postgres-python` · `cron-worker` · `webhook-receiver` · `ai-chat` · `secret-reload-node` · `customer-platform` · `mcp-node` · `mcp-go` · `mcp-python` · `customer-operation-export` · `customer-operation-job-export` · `customer-operation-workflow-export` · `data-api` · `data-api-starter` · `data-api-teams` · `data-api-support-console` |
| `--dockerfile` | build with the supplied Dockerfile inside --tarball |  |
| `--issue-source-maps <PATH>` | publish a portable manifest from the selected source; requires zero production traffic |  |
| `--issue-source-map-directory <PATH>` | deployed bundle directory corresponding to the manifest directory |  |
| `--runtime <RUNTIME>` | function runtime | one of `node22` · `python312` · `go124` · `go124-alpine` · `node24` · `python313` · `binary` |
| `--handler <HANDLER>` | function handler |  |
| `--name <SLUG>` | app slug (default: source directory name) |  |
| `--size <SIZE>` | RAM/CPU size for one app; excludes --repo and project deploys | one of `micro` · `small` · `medium` · `large` · `xlarge` |
| `--profile <SIZE>` | deprecated: use --size |  |
| `--vcpu <N>` | assert the plan guest vCPU shape (omit to use the plan default) |  |
| `--execution-mode <MODE>` | app lifecycle mode | one of `request` · `service` · `worker` · `job` |
| `--restart-policy <POLICY>` | app restart policy | one of `no` · `on-failure` · `always` · `unless-stopped` |
| `--startup-deadline-s <SECONDS>` | maximum startup seconds (0 uses plan default) |  |
| `--max-retries <N>` | maximum restart attempts (0 uses plan default) |  |
| `--function` | deploy as a function; skip shape auto-detection |  |
| `--app` | deploy as an app; skip shape auto-detection |  |
| `--only <SLUGS>` | workloads to apply; retain unselected project workloads (comma-separated) |  |
| `--project` | deploy all detected workloads as one project (slug defaults from --name or source) |  |
| `--environment <SLUG>` | deploy to a registered project environment (defaults to linked context) |  |
| `--reason <text>` | free-text deploy reason (≤280 chars) |  |
| `--tag <TAG>` | deployment annotation tag | one of `incident_recovery` · `hotfix` · `scheduled_maintenance` · `compliance_hold` · `partner_request` |
| `--deployed-by <NAME>` | operator label (auto-resolved from git config user.name) |  |
| `--pr-number <N>` | GitHub PR number (positive int; 0 = absent). CI paths stamp via the GitHub Action. |  |
| `--exclude <SLUGS>` | omit workloads (comma-separated slugs; cannot combine with --only) |  |
| `--show-affected` | show workloads that deploy, stay unchanged, or are removed |  |
| `--persist-exclude` | save --exclude slugs for future project deploys |  |
| `--project-slug <SLUG>` | kebab slug for the project (one-key provision) |  |
| `--rollout <MODE>` | how this release reaches traffic: safe (adaptive canary with rollback; waits for 100%), fast (5% -&gt; 50% -&gt; 100%) or immediate; default: the app release policy | one of `safe` · `fast` · `immediate` |
| `--override-freeze <REASON>` | release to production during a deploy freeze; the reason is audited |  |
| `--at <WHEN>` | build now and start the production rollout at this time: +2h, 02:00, &#34;2026-10-22 02:00&#34; (local) or RFC 3339; freezes are checked then |  |
| `--release-notify <CHANNEL,...>` | Slack or email channels told when a scheduled release starts or cannot start (with --at) |  |
| `--canary-preset <PRESET>` | deprecated: use --rollout. Canary ladder preset | one of `none` · `slow` · `balanced` · `aggressive` · `1-10-50-100` · `adaptive` · `custom` |
| `--canary-stages <STAGES>` | deprecated: use --rollout; custom percent@duration canary stages (requires --canary-preset custom) |  |
| `--safe` | deprecated: use --rollout safe |  |
| `--require-authn` | require bearer auth on every request |  |
| `--no-require-authn` | drop the token requirement |  |
| `--platform-tenant-required` | require verified customer identity on selected apps (Hobby and above) |  |
| `--no-platform-tenant-required` | allow selected apps to receive traffic without customer identity |  |
| `--app-protocol <PROTOCOL>` | wire protocol selector | one of `http1` · `http2` · `grpc` |
| `--traffic-percent <PERCENT>` | deployment traffic split weight (0-100) |  |
| `--no-traffic` | stage with 0% production traffic and print the preview URL |  |
| `--rollback-on-5xx` | roll back after repeated first-wake 5xx responses |  |
| `--queue-canary-binding <NAME>` | declare safe queue canary support for this deployment |  |
| `--healthcheck-path <PATH>` | startup HTTP readiness path |  |
| `--healthcheck-grpc` | use standard gRPC health for startup readiness |  |
| `--healthcheck-grpc-service <SERVICE>` | service name (requires --healthcheck-grpc; default: overall server) |  |
| `--disable-startup-cpu-boost` | disable temporary CPU boost during VM startup |  |
| `--mcp-task-migration-override` | allow this deployment to replace incompatible MCP task workers once |  |
| `--no-triggers` | skip gregale.yaml trigger and async-route changes |  |
| `--wait` | wait for readiness; projects also wait for rollout and configuration (default) |  |
| `--no-wait` | return after deployment is queued |  |
| `--create-only` | create or reserve the app without uploading a deployment |  |
| `--timeout <DURATION>` | readiness wait limit; seconds or duration, e.g. 10m (default: 1200s; maximum: 24h) |  |
| `--idempotency-key <KEY>` | stable logical retry key for this deployment |  |
| `--secrets-file <PATH>` | seal KEY=VALUE pairs before the first deployment |  |
| `--secret-scan <on|off>` | scan .env files before packing (default: on) | one of `on` · `off` |
| `--diff` | alias of --dry-run |  |
| `--preview` | preview local app defaults; no login needed |  |
| `--plan` | alias of --preview |  |
| `--dry-run` | check against remote state without deploying |  |
| `--yes` | skip the apply confirmation prompt |  |
| `--strict` | fail on schema/quota/env breaks (default with --dry-run, --diff, or --server-diff) |  |
| `--lenient` | allow diff breaks; requires --dry-run, --diff, or --server-diff; excludes --strict |  |
| `--server-diff` | preview changes using the server diff engine |  |
| `--doctor-strict` | run doctor before deploy and abort on errors |  |
| `--no-doctor` | skip the automatic local doctor preflight |  |

Examples:

```sh
gregale deploy --preview
gregale deploy --source=head --name my-api
gregale deploy --path packages/api --source=worktree
```

### deploy check

Check project prerequisites without uploading or submitting

`gregale deploy check --project [--project-slug <SLUG>] [--path <DIR>] [--source <MODE>] [--worktree] [--environment <ENV>] [--from <ENV>] [--expected-state <SHA256>] [--atomic] [--add-services] [--verify] [--workers] [--worker-health] [--migrate] [--progressive] [--stabilize] [--sync-config] [--config-file <PATH>] [--secrets-file <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--project` | check the complete project | required |
| `--project-slug <SLUG>` | project slug (defaults to linked context or selected directory) |  |
| `--path <DIR>` | selected source directory |  |
| `--source <MODE>` | local source policy (default auto) | one of `auto` · `head` · `worktree` |
| `--worktree` | check local working tree changes |  |
| `--environment <ENV>` | destination environment (default production) |  |
| `--from <ENV>` | source environment for a promotion check |  |
| `--expected-state <SHA256>` | compare a previous check&#39;s state hash |  |
| `--atomic` | check atomic source release prerequisites |  |
| `--add-services` | check HTTP service addition prerequisites |  |
| `--verify` | check critical health verification prerequisites |  |
| `--workers` | check push worker release prerequisites |  |
| `--worker-health` | require worker delivery health during deployment |  |
| `--migrate` | check one backward-compatible migration |  |
| `--progressive` | check progressive rollout prerequisites |  |
| `--stabilize` | check post-success monitoring prerequisites |  |
| `--sync-config` | check synchronized promotion configuration |  |
| `--config-file <PATH>` | proposed non-secret paired configuration |  |
| `--secrets-file <PATH>` | proposed secret input; only key names leave this machine |  |

Examples:

```sh
gregale deploy check --project --json
gregale deploy check --project --atomic --verify
gregale deploy check --project --project-slug shop --from staging --environment production --sync-config --verify
```

### deploy resolve-contract

Record checks and a verified roll-forward after failed cleanup

`gregale deploy resolve-contract [--replacement-attempt <ID>] [--task-id <ID>] [--generation <N>] [--request-id <ID>] [--evidence-hash <SHA256>] [--database-state-checked] [--execution-quiescent] ATTEMPT_ID`

| Flag | Meaning | |
|---|---|---|
| `--replacement-attempt <ID>` | verified corrected deployment attempt |  |
| `--task-id <ID>` | inspected original cleanup task |  |
| `--generation <N>` | inspected cleanup generation |  |
| `--request-id <ID>` | reuse after a lost recovery response |  |
| `--evidence-hash <SHA256>` | SHA256 of private SQL/execution evidence |  |
| `--database-state-checked` | attest database inspection/repair |  |
| `--execution-quiescent` | attest original cleanup execution has stopped |  |

### deploy contract

Run planned schema cleanup after retained versions and work drain

`gregale deploy contract [--request-id <ID>] [--generation <N>] ATTEMPT_ID`

| Flag | Meaning | |
|---|---|---|
| `--request-id <ID>` | reuse this contract identity after an ambiguous response |  |
| `--generation <N>` | require this inspected checkpoint generation |  |

### deploy status

Inspect an atomic project attempt and available recovery actions

`gregale deploy status ATTEMPT_ID`

### deploy abort

Stop a progressive rollout and request guarded baseline restoration

`gregale deploy abort [--request-id <ID>] [--generation <N>] ATTEMPT_ID`

| Flag | Meaning | |
|---|---|---|
| `--request-id <ID>` | reuse this control identity after an ambiguous response |  |
| `--generation <N>` | require this inspected checkpoint generation |  |

### deploy recover

Retry baseline health verification for a blocked progressive deploy

`gregale deploy recover [--request-id <ID>] [--generation <N>] ATTEMPT_ID`

| Flag | Meaning | |
|---|---|---|
| `--request-id <ID>` | reuse this control identity after an ambiguous response |  |
| `--generation <N>` | require this inspected checkpoint generation |  |

### deploy retry

Rebuild confirmed failed workloads from a saved project deploy

`gregale deploy retry [--tarball <PATH>] [--path <DIR>] [--source <auto|head|worktree>] [--worktree] [--yes] [--no-wait] [--timeout <SECONDS>] SESSION_ID`

| Flag | Meaning | |
|---|---|---|
| `--tarball <PATH>` | source archive to rebuild |  |
| `--path <DIR>` | source directory to rebuild (default: current directory) |  |
| `--source <auto|head|worktree>` | local source policy (default: auto) | one of `auto` · `head` · `worktree` |
| `--worktree` | include working tree changes (alias for --source=worktree) |  |
| `--yes` | confirm the retry plan |  |
| `--no-wait` | return after acceptance |  |
| `--timeout <SECONDS>` | maximum wait seconds (default 1200) |  |

Examples:

```sh
gregale deploy retry SESSION_ID --path . --source=worktree --yes
gregale deploy retry SESSION_ID --tarball source.tar.gz --yes
```

### deploy resume

Reconnect to a saved app or project deploy and finish pending checks

`gregale deploy resume [--app <SLUG>] [--timeout <SECONDS>] [SESSION_ID]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | select the most recent saved deploy for this app |  |
| `--timeout <SECONDS>` | maximum wait seconds (default 1200) |  |

Examples:

```sh
gregale deploy resume --app my-api
gregale deploy resume SESSION_ID --json
```


## domains

Manage custom domains

`gregale domains [<subcommand>]`

### domains setup

Guide DNS setup and wait for verification and TLS

`gregale domains setup --app <SLUG> [--environment <SLUG>] [--timeout <DURATION>] [--poll-interval <DURATION>] <domain>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app to attach to | required |
| `--environment <SLUG>` | project environment to route to |  |
| `--timeout <DURATION>` | verification wait deadline (default 10m, maximum 1h) |  |
| `--poll-interval <DURATION>` | verification interval (5s..1m, default 10s) |  |

Examples:

```sh
gregale domains setup api.example.com --app my-api
```

Offers a default-domain change at the end.

### domains list

List custom domain bindings

`gregale domains list [--app <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | only this app&#39;s domains |  |

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

Check DNS and certificate verification status

`gregale domains verify <domain>`

Exits nonzero while pending.

### domains show

Show a domain&#39;s cert details

`gregale domains show <domain>`

### domains status

Show durable TLS status for all domains

### domains doctor

Run a 5-check domain readiness report

`gregale domains doctor <domain>`

Exits nonzero when unhealthy, including in JSON mode.


## dev

Sync local changes to a developer environment

`gregale dev [<subcommand>] [--path <DIR>] [--all] [--name <PROJECT>] [--env-file <PATH>] [--service-override-file <PATH>] [--once] [--stop] [--no-logs] [--open] [--bucket <NAME>] [--postgres] [--postgres-region <REGION>] [--postgres-seed <CMD>] [--reseed] [--debug] [--debug-port <PORT>] [--ttl <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | source directory |  |
| `--all` | run one developer loop per deployable workspace app |  |
| `--name <PROJECT>` | developer-session project name |  |
| `--env-file <PATH>` | sync KEY=VALUE entries as developer secrets |  |
| `--service-override-file <PATH>` | sync validated service URLs as developer secrets |  |
| `--once` | deploy once and exit |  |
| `--stop` | tear down the developer environment |  |
| `--no-logs` | do not attach the live runtime log stream |  |
| `--open` | open the developer environment URL after the first live sync |  |
| `--bucket <NAME>` | provision a private workspace bucket; repeatable |  |
| `--postgres` | provision an isolated PostgreSQL database and inject DATABASE_URL |  |
| `--postgres-region <REGION>` | choose managed database placement |  |
| `--postgres-seed <CMD>` | shell command run once in the developer app after its database is ready |  |
| `--reseed` | run the seed again even if this database was already seeded |  |
| `--debug` | start the Node.js inspector and expose it on a local port |  |
| `--debug-port <PORT>` | local port for --debug (default 9229) |  |
| `--ttl <DURATION>` | environment lease after the latest sync (default 24h; plan maximum applies) |  |

Examples:

```sh
gregale dev --once
gregale dev --path ./api --once
gregale dev --all
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

### dev info

Show the developer environment&#39;s URL, app, lease and database

`gregale dev info [--path <DIR>] [--name <PROJECT>]`

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | source directory |  |
| `--name <PROJECT>` | developer-session project name |  |

Does not renew the lease.

### dev trigger

Invoke the developer app, fire a cron route or schedule a task

`gregale dev trigger <invoke|cron|delayed-task> [--path <DIR>] [--name <PROJECT>]`

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | source directory (before the verb) |  |
| `--name <PROJECT>` | developer-session project name (before the verb) |  |

Examples:

```sh
gregale dev trigger invoke --method POST --path /orders --payload @order.json
gregale dev trigger cron /jobs/nightly
gregale dev trigger delayed-task --delay 5m --path /reminders
```

#### dev trigger invoke

Invoke the developer app

`gregale dev trigger invoke`

#### dev trigger cron

POST to a cron route declared for this app in gregale.yaml

`gregale dev trigger cron [ROUTE]`

#### dev trigger delayed-task

Schedule a delayed task on the developer app

`gregale dev trigger delayed-task`

### dev setup

preflight a project and prepare the first developer environment

`gregale dev setup [--path <DIR>] [--name <PROJECT>] [--env-file <PATH>] [--service-override-file <PATH>] [--start] [--save] [--dry-run] [--once] [--no-logs] [--open] [--bucket <NAME>] [--postgres] [--postgres-region <REGION>] [--ttl <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | source directory |  |
| `--name <PROJECT>` | developer-session project name |  |
| `--env-file <PATH>` | validate and sync developer secrets |  |
| `--service-override-file <PATH>` | validate and sync service URLs |  |
| `--start` | start after preflight |  |
| `--save` | save portable developer defaults in gregale.yaml |  |
| `--dry-run` | preview saved defaults; requires --save |  |
| `--once` | sync once and exit |  |
| `--no-logs` | do not attach runtime logs |  |
| `--open` | open the verified URL |  |
| `--bucket <NAME>` | provision a private workspace bucket; repeatable |  |
| `--postgres` | provision an isolated PostgreSQL database |  |
| `--postgres-region <REGION>` | choose managed database placement |  |
| `--ttl <DURATION>` | environment lease after the latest sync |  |


## diff

Compare two named environments in the linked project

`gregale diff <from-environment> <to-environment> [--project <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--project <SLUG>` | project slug (defaults to linked project) |  |


## test

Run scenario suites, lifecycle checks and local load tests

`gregale test [<subcommand>] [--scenario <NAME>] [--suite <NAME>] [--fail-fast] [--validate] [--preflight] [--engine <ENGINE>] [--base-url <URL>] [--data <PATH>] [--load] [--vus <N>] [--rate <N>] [--iterations <N>] [--duration <DURATION>] [--pacing <DURATION>] [--progress] [--baseline <PATH>] [--sweep-baseline <PATH>] [--recovery-baseline <PATH>] [--profile <PROFILE>] [--repeat <N>] [--max-workload-minutes <N>] [--manifest <PATH>] [--report <PATH>] [--junit <PATH>] [--html <PATH>]`

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
| `--sweep-baseline <PATH>` | compare resilience sweeps with a saved JSON report |  |
| `--recovery-baseline <PATH>` | compare resilience recovery with a saved successful JSON report |  |
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

### test rehearse

Compare two pinned Git revisions with one trusted behavior scenario

`gregale test rehearse [--repo <DIR>] --base <REF> --candidate <REF> [--manifest <PATH>] --scenario <NAME> [--engine <ENGINE>] [--profile <PROFILE>] [--repeat <N>] [--max-workload-minutes <N>] [--validate] [--report <PATH>] [--html <PATH>] [--markdown <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--repo <DIR>` | local Git checkout containing both revisions and trusted inputs |  |
| `--base <REF>` | baseline revision resolved once to a commit | required |
| `--candidate <REF>` | candidate revision resolved once to a commit | required |
| `--manifest <PATH>` | trusted manifest from the invoking checkout |  |
| `--scenario <NAME>` | scenario with declared behavior observations | required |
| `--engine <ENGINE>` | execution engine (default real-vm) | one of `real-vm` · `local` · `simulated` |
| `--profile <PROFILE>` | real-vm lifecycle profile (default warm) | one of `warm` · `cold` · `restored` · `all` |
| `--repeat <N>` | fresh runs per revision and profile (1..20; default 1) |  |
| `--max-workload-minutes <N>` | required total real-vm workload-minute limit across both revisions |  |
| `--validate` | validate both snapshots without execution or platform access |  |
| `--report <PATH>` | required combined JSON receipt for execution; new file |  |
| `--html <PATH>` | standalone comparison HTML; new file |  |
| `--markdown <PATH>` | comparison Markdown; new file |  |

Examples:

```sh
gregale test rehearse --base main --candidate HEAD --scenario customer-export --engine simulated --report rehearsal.json --html rehearsal.html
gregale test rehearse --base main --candidate HEAD --scenario customer-export --profile restored --max-workload-minutes 90 --report rehearsal.json
```

### test init

Create public GET smoke checks from a local OpenAPI document

`gregale test init --from <PATH> --project <SLUG> [--source <DIR>] [--scenario <NAME>] [--output <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--from <PATH>` | local OpenAPI 3.0 or 3.1 document | required |
| `--project <SLUG>` | Gregale project slug | required |
| `--source <DIR>` | application source directory, relative to the manifest (default: the workload holding --from, else .) |  |
| `--scenario <NAME>` | scenario name |  |
| `--output <PATH>` | new manifest path |  |

Examples:

```sh
gregale test init --from openapi.yaml --project my-api --source .
```

### test recovery-trend

Render recovery performance trends from campaign reports

`gregale test recovery-trend [--manifest <PATH>] --scenario <NAME> --config <PATH> --html <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--manifest <PATH>` | campaign scenario manifest |  |
| `--scenario <NAME>` | campaign scenario name | required |
| `--config <PATH>` | ordered reports and recovery tolerances YAML | required |
| `--html <PATH>` | standalone HTML recovery trend output | required |

Works offline on ordered reports.

### test coverage-trend

Render offline campaign coverage trends from ordered reports

`gregale test coverage-trend [--manifest <PATH>] --scenario <NAME> --config <PATH> --html <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--manifest <PATH>` | campaign scenario manifest |  |
| `--scenario <NAME>` | campaign scenario name | required |
| `--config <PATH>` | ordered report inputs and tolerances YAML | required |
| `--html <PATH>` | standalone HTML trend output | required |

### test replay

Replay a failure, configuration or sweep run from a saved report

`gregale test replay --from <PATH> --run-id <ID> [--manifest <PATH>] [--validate] [--report <PATH>] [--junit <PATH>] [--html <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--from <PATH>` | saved JSON report containing the failed run | required |
| `--run-id <ID>` | replayable isolated run ID | required |
| `--manifest <PATH>` | original scenario manifest |  |
| `--validate` | validate replay without provisioning |  |
| `--report <PATH>` | write replay JSON report |  |
| `--junit <PATH>` | write replay JUnit report |  |
| `--html <PATH>` | write replay HTML report |  |

Examples:

```sh
gregale test replay --from failed.json --run-id RUN_ID --manifest gregale-test.yaml --validate
gregale test replay --from failed.json --run-id RUN_ID --manifest gregale-test.yaml --report replay.json
```

Covers real-VM failures and passing recovery or duration configurations.

### test minimize

Minimize faults, outages, stability checks or recovery deadlines

`gregale test minimize --from <PATH> --run-id <ID> --assertion <TARGET> [--mode <MODE>] [--rule-index <N>] [--resolution <DURATION>] [--max-rate-kib-per-second <N>] [--rate-resolution-kib-per-second <N>] [--percent-resolution <N>] [--max-recovery-within <DURATION>] [--duration-resolution <DURATION>] [--stability-resolution <DURATION>] [--recovery-resolution <DURATION>] [--manifest <PATH>] [--max-runs <N>] --max-workload-minutes <N> [--validate] [--report <PATH>] [--junit <PATH>] [--html <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--from <PATH>` | saved JSON report containing the original failure | required |
| `--run-id <ID>` | original replayable isolated run ID | required |
| `--assertion <TARGET>` | recovery assertion: STEP/PHASE or STEP/PHASE/REQUEST | required |
| `--mode <MODE>` | minimize rules (default), latency, bandwidth, percent, recovery, stability, or duration | one of `rules` · `latency` · `bandwidth` · `percent` · `recovery` · `stability` · `duration` |
| `--rule-index <N>` | required original zero-based rule index in severity modes |  |
| `--resolution <DURATION>` | latency search resolution (default 25ms; whole milliseconds) |  |
| `--max-rate-kib-per-second <N>` | required bandwidth search ceiling (1..1000000 KiB/s) |  |
| `--rate-resolution-kib-per-second <N>` | bandwidth search resolution (default 1 KiB/s) |  |
| `--percent-resolution <N>` | percentage search resolution (default 1; 1..100 percentage points) |  |
| `--max-recovery-within <DURATION>` | required recovery deadline ceiling fitting the existing phase |  |
| `--duration-resolution <DURATION>` | fault duration search resolution (default 100ms; whole milliseconds) |  |
| `--stability-resolution <DURATION>` | stability observation search resolution (default 25ms; whole milliseconds) |  |
| `--recovery-resolution <DURATION>` | recovery deadline search resolution (default 25ms; whole milliseconds) |  |
| `--manifest <PATH>` | original scenario manifest |  |
| `--max-runs <N>` | maximum fresh runs including confirmations (2..20; default 10) |  |
| `--max-workload-minutes <N>` | required maximum estimated VM workload-minutes | required |
| `--validate` | validate selection and budgets without provisioning |  |
| `--report <PATH>` | required JSON output for execution |  |
| `--junit <PATH>` | write minimization JUnit report |  |
| `--html <PATH>` | write minimization HTML report |  |

Examples:

```sh
gregale test minimize --from failed.json --run-id RUN_ID --assertion load/recovery --mode duration --duration-resolution 100ms --max-runs 20 --max-workload-minutes 180 --report duration.json
gregale test minimize --from failed.json --run-id RUN_ID --assertion load/recovery --max-runs 10 --max-workload-minutes 180 --validate
gregale test minimize --from failed.json --run-id RUN_ID --assertion load/recovery/checkout --max-runs 10 --max-workload-minutes 180 --report minimized.json --html minimized.html
gregale test minimize --from failed.json --run-id RUN_ID --assertion load/recovery --mode stability --stability-resolution 100ms --max-runs 20 --max-workload-minutes 180 --report stability.json
gregale test minimize --from failed.json --run-id RUN_ID --assertion load/recovery --mode recovery --max-recovery-within 10s --recovery-resolution 100ms --max-runs 20 --max-workload-minutes 180 --report deadline.json
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

Generate GitHub Actions for scenario tests

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

Supports local, simulated and real-VM tests.

### test import

Create draft requests from a Postman Collection v2.1 export

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

Inject faults into isolated scenario tests

`gregale chaos [<subcommand>]`

### chaos inject

Run one scenario profile with a scoped service fault

`gregale chaos inject --scenario <NAME> [--manifest <PATH>] --target <SERVICE> [--from <SERVICE>] [--latency <DURATION>] [--error <CODE>] [--tcp-port <PORT>] [--direction <DIRECTION>] [--bandwidth <N>] [--timeout] [--reset] [--reset-after <DURATION>] [--connect-timeout] [--connect-refused] [--dns-latency <DURATION>] [--dns-timeout] [--dns-servfail] [--dns-nxdomain] [--percent <N>] [--duration <DURATION>] [--profile <PROFILE>] [--seed <N>]`

| Flag | Meaning | |
|---|---|---|
| `--scenario <NAME>` | scenario declared in gregale-test.yaml | required |
| `--manifest <PATH>` | scenario manifest path |  |
| `--target <SERVICE>` | scenario service workload to affect | required |
| `--from <SERVICE>` | only affect calls from this workload |  |
| `--latency <DURATION>` | add this delay to selected requests, such as 1500ms |  |
| `--error <CODE>` | return this synthetic HTTP 5xx status |  |
| `--tcp-port <PORT>` | target TCP port; selects TCP latency |  |
| `--direction <DIRECTION>` | TCP traffic direction | one of `upstream` · `downstream` · `both` |
| `--bandwidth <N>` | TCP throughput cap in KiB/s |  |
| `--timeout` | stall TCP traffic until the lease expires |  |
| `--reset` | reset selected TCP connections |  |
| `--reset-after <DURATION>` | delay before a TCP reset |  |
| `--connect-timeout` | hold new service connections until timeout or fault clear |  |
| `--connect-refused` | reset new service connections before reaching the target |  |
| `--dns-latency <DURATION>` | delay private service-name DNS responses |  |
| `--dns-timeout` | drop private service-name DNS queries |  |
| `--dns-servfail` | return SERVFAIL for private service-name DNS queries |  |
| `--dns-nxdomain` | return NXDOMAIN for private service-name DNS queries |  |
| `--percent <N>` | fraction of matching requests, DNS queries, or TCP connections affected (1..100) |  |
| `--duration <DURATION>` | maximum fault lease duration (1s..5m) |  |
| `--profile <PROFILE>` | real-VM lifecycle profile | one of `warm` · `cold` · `restored` |
| `--seed <N>` | deterministic fault-selection seed |  |

Examples:

```sh
gregale chaos inject --scenario cache-resilience --target cache --tcp-port 6379 --bandwidth 64 --direction downstream
gregale chaos inject --scenario customer-export --target inventory --error 503 --percent 10 --duration 5m
gregale chaos inject --scenario customer-export --target payment --latency 1500ms --percent 20 --from worker --profile restored
gregale chaos inject --scenario customer-export --target inventory --dns-nxdomain --percent 100
gregale chaos inject --scenario cache-resilience --target cache --tcp-port 6379 --connect-timeout --duration 8s
gregale chaos inject --scenario cache-resilience --target cache --tcp-port 6379 --connect-refused
```


## preview

Manage preview environments for pull requests

See also: [`previews`](#previews).

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

Review route changes, gateway rule drift and test evidence

`gregale preview report [--format <FORMAT>] [--since <DURATION>] [--customer-details] [--baseline-deployment <ID>] [--test-report <PATH>] [--source-impact <PATH>] [--requirements <PATH>] [--fail-on-breaking] [--fail-on-request-breaking] [--fail-on-security-regression] [--fail-on-policy-drift] [--fail-on-incomplete] [--fail-on-requirements] [--route-removal-mode <MODE>] [--route-readiness <PATH>] [--route-mapping <PATH>] [--route-owner-approval <PATH>] [--route-evidence-max-age <DURATION>] <preview-slug>`

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
| `--route-removal-mode <MODE>` | CLI removal gate: report (default) or enforce | one of `report` · `enforce` |
| `--route-readiness <PATH>` | migration readiness JSON for the serving production deployment |  |
| `--route-mapping <PATH>` | reviewed successor mapping JSON |  |
| `--route-owner-approval <PATH>` | owner attestation bound to this exact change and evidence |  |
| `--route-evidence-max-age <DURATION>` | maximum route evidence age (default and maximum 72h) |  |

Examples:

```sh
gregale preview report pr-42-my-api
gregale preview report pr-42-my-api --format markdown --fail-on-breaking
gregale preview report pr-42-my-api --test-report results.json --json
gregale preview report pr-42-my-api --source-impact impact.json --test-report results.json --format markdown
gregale preview report pr-42-my-api --fail-on-request-breaking --format markdown
gregale preview report pr-42-my-api --fail-on-policy-drift --format markdown
```

Includes available test and traffic evidence.

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

Measure old-route traffic and customer migration progress

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

Across saved tracker windows.

#### preview customers migration

Join contract compatibility with customer cutover evidence

`gregale preview customers migration <review|diff>`

##### preview customers migration review

Review contract compatibility and per-customer migration

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


## previews

Join or leave early platform features

See also: [`preview`](#preview).

`gregale previews [<subcommand>]`

Joining releases an early platform feature to your account; a preview never adds a plan entitlement.

With --org, an owner or admin joins for the whole organization: members get it while acting for the company.

### previews list

List previews you can join or already joined

`gregale previews list [--org <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | act for this organization (owner or admin to join or leave) |  |

### previews join

Join a preview

`gregale previews join [--org <SLUG>] <key>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | act for this organization (owner or admin to join or leave) |  |

Examples:

```sh
gregale previews join change_timeline
gregale previews join change_timeline --org acme
```

### previews leave

Leave a preview

`gregale previews leave [--org <SLUG>] <key>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | act for this organization (owner or admin to join or leave) |  |


## flags

Roll out feature flags to selected customers

`gregale flags [<subcommand>] --project <slug> [--environment <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--project <slug>` | project slug | required |
| `--environment <slug>` | named environment (default production) |  |

### flags get

Read current flag configuration

`gregale flags get --project <slug> [--environment <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--project <slug>` | project slug | required |
| `--environment <slug>` | named environment (default production) |  |

### flags apply

Publish a versioned configuration

`gregale flags apply --project <slug> [--environment <slug>] --file <path>`

| Flag | Meaning | |
|---|---|---|
| `--project <slug>` | project slug | required |
| `--environment <slug>` | named environment (default production) |  |
| `--file <path>` | JSON update bundle | required |

### flags history

List immutable configuration versions

`gregale flags history --project <slug> [--environment <slug>] [--before-version <number>]`

| Flag | Meaning | |
|---|---|---|
| `--project <slug>` | project slug | required |
| `--environment <slug>` | named environment (default production) |  |
| `--before-version <number>` | page before this version |  |

### flags inspect

Explain a customer&#39;s decision

`gregale flags inspect --project <slug> [--environment <slug>] --key <key> [--customer-id <UUID>] [--subject-id <ID>] [--fallback-variant <NAME>] [--version <number>]`

| Flag | Meaning | |
|---|---|---|
| `--project <slug>` | project slug | required |
| `--environment <slug>` | named environment (default production) |  |
| `--key <key>` | flag key | required |
| `--customer-id <UUID>` | customer UUID |  |
| `--subject-id <ID>` | authenticated application subject (requires --customer-id) |  |
| `--fallback-variant <NAME>` | named-variant fallback |  |
| `--version <number>` | historical configuration version |  |

### flags rollback

Publish an earlier configuration

`gregale flags rollback --project <slug> [--environment <slug>] --version <number> --expected-version <number>`

| Flag | Meaning | |
|---|---|---|
| `--project <slug>` | project slug | required |
| `--environment <slug>` | named environment (default production) |  |
| `--version <number>` | version to restore | required |
| `--expected-version <number>` | current version | required |

### flags requests

Inspect request evidence by flag value

`gregale flags requests --project <slug> [--environment <slug>] --key <key> [--customer-id <UUID>] [--value <bool>] [--variant <NAME>] [--used <bool>] [--since <duration>] [--cursor <cursor>]`

| Flag | Meaning | |
|---|---|---|
| `--project <slug>` | project slug | required |
| `--environment <slug>` | named environment (default production) |  |
| `--key <key>` | flag key | required |
| `--customer-id <UUID>` | customer UUID |  |
| `--value <bool>` | true or false |  |
| `--variant <NAME>` | filter by named variant |  |
| `--used <bool>` | true or false exposure |  |
| `--since <duration>` | lookback (default 24h) |  |
| `--cursor <cursor>` | next-page cursor |  |

### flags outcomes

Inspect flag decision outcomes

`gregale flags outcomes --project <slug> [--environment <slug>] --key <key> [--customer-id <UUID>] [--rule-id <ID>] [--config-version <number>] [--since <duration>]`

| Flag | Meaning | |
|---|---|---|
| `--project <slug>` | project slug | required |
| `--environment <slug>` | named environment (default production) |  |
| `--key <key>` | flag key | required |
| `--customer-id <UUID>` | customer UUID |  |
| `--rule-id <ID>` | filter to a targeting rule |  |
| `--config-version <number>` | filter to one configuration version |  |
| `--since <duration>` | lookback (default 24h) |  |

### flags promote

Promote a targeting rule rollout

`gregale flags promote --project <slug> [--environment <slug>] --key <key> --rule-id <ID> --expected-version <number>`

| Flag | Meaning | |
|---|---|---|
| `--project <slug>` | project slug | required |
| `--environment <slug>` | named environment (default production) |  |
| `--key <key>` | flag key | required |
| `--rule-id <ID>` | targeting rule ID | required |
| `--expected-version <number>` | current configuration version | required |


## consumers

Issue keys to your API&#39;s consumers and meter their requests

`gregale consumers [<subcommand>]`

Examples:

```sh
gregale consumers create my-api --external-ref customer-42 --name "Customer 42"
gregale consumers key-create my-api CONSUMER_ID --name production --scopes read,write
```

### consumers list

List an app&#39;s API consumers

`gregale consumers list <slug>`

### consumers create

Register an API consumer

`gregale consumers create --external-ref <REF> --name <TEXT> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--external-ref <REF>` | stable consumer reference | required |
| `--name <TEXT>` | consumer display name | required |

### consumers info

Show one API consumer

`gregale consumers info <slug> <consumer-id>`

### consumers revoke

Revoke an API consumer; its keys stop authenticating

`gregale consumers revoke <slug> <consumer-id>`

### consumers keys

List a consumer&#39;s API keys

`gregale consumers keys <slug> <consumer-id>`

### consumers key-create

Issue a consumer API key (secret shown once)

`gregale consumers key-create --name <TEXT> [--scopes <LIST>] [--expires <RFC3339>] <slug> <consumer-id>`

| Flag | Meaning | |
|---|---|---|
| `--name <TEXT>` | key display name | required |
| `--scopes <LIST>` | comma-separated scopes: read, write, admin (default write) |  |
| `--expires <RFC3339>` | key expiry |  |

### consumers key-revoke

Revoke one consumer API key

`gregale consumers key-revoke <slug> <consumer-id> <key-id>`

### consumers usage

Show a consumer&#39;s metered requests

`gregale consumers usage [--since <RFC3339>] [--until <RFC3339>] <slug> <consumer-id>`

| Flag | Meaning | |
|---|---|---|
| `--since <RFC3339>` | window start |  |
| `--until <RFC3339>` | window end |  |


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


## edge-rule-lists

Manage reusable IP, country, host and text lists for edge rules

`gregale edge-rule-lists [<subcommand>]`

### edge-rule-lists list

List the account&#39;s edge-rule lists

### edge-rule-lists get

Show one list with its items

`gregale edge-rule-lists get <name>`

### edge-rule-lists create

Create a list

`gregale edge-rule-lists create --kind <KIND> [--item <VALUE>] [--items-file <path|->] [--description <TEXT>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--kind <KIND>` | list kind | required; one of `ip` · `country` · `host` · `string` · `asn` |
| `--item <VALUE>` | list item (repeat) |  |
| `--items-file <path|->` | file with one item per line (# comments allowed), or - for stdin |  |
| `--description <TEXT>` | free-text description |  |

Examples:

```sh
gregale edge-rule-lists create office-ips --kind ip --item 203.0.113.0/24 --item 2001:db8::1
gregale edge-rule-lists create blocked --kind country --items-file countries.txt
gregale edge-rules create --app my-api --kind throttle --match-host api.example.com --throttle-requests-per-second 5 --match '{"not":{"field":"client_ip","op":"in_list","list":"office-ips"}}'
```

### edge-rule-lists update

Edit a list; referencing rules pick up the change within seconds

`gregale edge-rule-lists update [--add <VALUE>] [--remove <VALUE>] [--replace-file <path|->] [--description <TEXT>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--add <VALUE>` | item to add (repeat) |  |
| `--remove <VALUE>` | item to remove (repeat) |  |
| `--replace-file <path|->` | replace every item from a file, or - for stdin |  |
| `--description <TEXT>` | new description |  |

### edge-rule-lists rm

Delete a list no rule references

`gregale edge-rule-lists rm <name>`


## edge-rules

Route, rewrite, protect or rate-limit an app&#39;s requests at the edge

`gregale edge-rules [<subcommand>] [--app <slug>] [--kind <value>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (for subcommands that take one) |  |
| `--kind <value>` | rule kind | one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` · `respond` · `retry` · `circuit_breaker` · `waf` · `async` · `challenge` |

### edge-rules list

List edge rules

`gregale edge-rules list [--app <slug>] [--kind <value>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | filter to a single app slug |  |
| `--kind <value>` | filter to a single kind | one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` · `respond` · `retry` · `circuit_breaker` · `waf` · `async` · `challenge` |

### edge-rules summary

Show what the edge rejected for an app

`gregale edge-rules summary --app <slug> [--range <WINDOW>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--range <WINDOW>` | time window (default 1h) | one of `5m` · `15m` · `1h` · `6h` · `24h` · `7d` · `15d` |

Includes pre-auth blocks, validation failures and gate rejections by status.

### edge-rules attack-mode

Switch attack mode on, off, or show it

`gregale edge-rules attack-mode [--app <slug>] [--for <DURATION>] [on|off|status]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project context) |  |
| `--for <DURATION>` | with on: how long attack mode stays on (default 1h, 15m to 24h) |  |

Examples:

```sh
gregale edge-rules attack-mode on --app my-api --for 1h
gregale edge-rules attack-mode --app my-api
gregale edge-rules attack-mode off --app my-api
```

Attack mode challenges every visitor to the app for a while (15m to 24h); clients that are not browsers need a verified API key or JWT.

### edge-rules trace

Simulate composed edge-rule outcomes and request policies

`gregale edge-rules trace [--config <file|->] [--app <slug>] [--url <URL>] [--method <method>] [--client-ip <IP>] [--country <CC>] [--asn <AS>] [--verified-bot] [--verified-ai-crawler] [--header <Name:Value>] [--body-file <path|->] [--proposal <file|->] [--add-rule <JSON|@FILE>] [--remove-rule <ID>]`

| Flag | Meaning | |
|---|---|---|
| `--config <file|->` | load a versioned JSON scenario (headers array; body or body_base64); - reads stdin and is exclusive with request flags |  |
| `--app <slug>` | app slug (required unless --config is used) |  |
| `--url <URL>` | absolute HTTP(S) request URL (required unless --config is used) |  |
| `--method <method>` | request method (default GET) |  |
| `--client-ip <IP>` | simulated client IP for kind=ip rules |  |
| `--country <CC>` | simulated ISO alpha-2 country for kind=geo rules |  |
| `--asn <AS>` | simulated client autonomous system for asn conditions (e.g. AS13335) |  |
| `--verified-bot` | simulate a client IP that passes crawler verification (verified_bot = the crawler the User-Agent claims) |  |
| `--verified-ai-crawler` | simulate a client IP inside the published ranges of the AI crawler the User-Agent claims (verified_ai_crawler) |  |
| `--header <Name:Value>` | simulated request header; repeat for multiple values |  |
| `--body-file <path|->` | request body file or - for stdin (max 1 MiB; contents are withheld) |  |
| `--proposal <file|->` | compare against a proposed change: JSON {add,update,remove} file or - for stdin |  |
| `--add-rule <JSON|@FILE>` | compare against adding this rule (create-request JSON or @file; repeat) |  |
| `--remove-rule <ID>` | compare against removing this rule id (repeat) |  |

Examples:

```sh
gregale edge-rules trace --app my-api --url https://api.example.com/admin --add-rule '{"kind":"maintenance","match_host":"api.example.com","match_path":"/admin/*","action":{"maintenance":{"message":"migrating"}}}'
```

Covers budget, throttle, retry, circuit-breaker and async-route policy. --config loads reusable JSON scenarios; see the edge-rule-trace docs.

### edge-rules create

Add an edge rule

`gregale edge-rules create --app <slug> --kind <KIND> --match-host <HOST> [--match-path <PATH>] [--match-method <METHOD>] [--match-header <Name=Value>] [--priority <N>] [--enabled[=true|false]] [--throttle-requests-per-second <RPS>] [--throttle-burst <N>] [--throttle-key-by <KEY>] [--redirect-status <CODE>] [--redirect-to <URL>] [--template] [--rewrite-from <PATH>] [--rewrite-to <PATH>] [--route-target-slug <slug>] [--cache-max-age-seconds <N>] [--cache-stale-while-revalidate-seconds <N>] [--budget-ms <MS>] [--retry-max-attempts <N>] [--circuit-failure-threshold <RATIO>] [--circuit-open-seconds <N>] [--respond-status <CODE>] [--respond-body <JSON>] [--ip-allow <CIDR>] [--ip-deny <CIDR>] [--geo-allow <CC>] [--geo-deny <CC>] [--jwt-issuer <ISSUER>] [--jwt-jwks-url <URL>] [--on-success-webhook <ID>] [--on-failure-webhook <ID>] [--async-max-attempts <N>] [--async-retry-base-seconds <N>] [--async-retry-max-seconds <N>] [--async-retry-jitter-seconds <N>] [--async-max-age-seconds <N>] [--waf-mode <MODE>] [--waf-paranoia-level <N>] [--waf-anomaly-threshold <N>] [--waf-exclude-rules <IDS>] [--waf-inspect-body-bytes <BYTES>] [--challenge-difficulty <BITS>] [--challenge-pass-ttl <SECONDS>] [--challenge-max-difficulty <BITS>] [--validate-schema <JSON|@FILE|->] [--validate-mode <MODE>] [--validate-content-type <TYPE>] [--validate-max-body-bytes <N>] [--validate-apply-while-streaming] [--validate-reject-unknown-fields] [--validate-path-template <TEMPLATE>] [--validate-path-schema <JSON|@FILE|->] [--validate-query-schema <JSON|@FILE|->] [--validate-headers-schema <JSON|@FILE|->] [--cors-allow-credentials] [--cors-max-age-seconds <SECONDS>] [--jwt-platform-tenant-external-ref-claim <CLAIM>] [--limit-max-body-bytes <BYTES>] [--limit-max-body-bytes-streaming <BYTES>] [--throttle-jwt-claim <CLAIM>] [--throttle-max-keys-per-rule <N>] [--throttle-missing-key-policy <POLICY>] [--throttle-key-field <FIELD>] [--throttle-count-status <CODE>] [--cache-stale-if-error-seconds <SECONDS>] [--total-deadline-ms <MS>] [--budget-allow-override-header <HEADER>] [--retry-allow-non-idempotent] [--retry-min-remaining-ms <MS>] [--retry-backoff-ms <MS>] [--retry-budget-percent <PERCENT>] [--retry-budget-min-retries <N>] [--circuit-min-requests <N>] [--circuit-window-seconds <SECONDS>] [--circuit-max-open-seconds <SECONDS>] [--maintenance-retry-after-seconds <SECONDS>] [--maintenance-message <TEXT>] [--match <JSON|@FILE|->] [--mode <MODE>] [--penalty-for <DURATION>] [--penalty-after <N>] [--penalty-within <DURATION>] [--cache-methods <METHOD>]... [--cache-vary-on <HEADER>]... [--cors-allow-header <HEADER>]... [--cors-allow-method <METHOD>]... [--cors-allow-origin <ORIGIN>]... [--cors-expose-header <HEADER>]... [--description <TEXT>] [--expires-at <RFC3339>] [--expires-in <DURATION>] [--headers-request-add <NAME:VALUE>]... [--headers-request-remove <NAME>]... [--headers-request-set <NAME:VALUE>]... [--headers-response-add <NAME:VALUE>]... [--headers-response-remove <NAME>]... [--headers-response-set <NAME:VALUE>]... [--jwt-algorithm <ALG>]... [--jwt-audience <AUDIENCE>]... [--jwt-require-exp] [--jwt-required-claim <NAME=VALUE>]... [--name <NAME>] [--redirect-header <NAME:VALUE>]...`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--kind <KIND>` | rule kind | required; one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` · `respond` · `retry` · `circuit_breaker` · `waf` · `async` · `challenge` |
| `--match-host <HOST>` | host to match; @app covers every host the app serves | required |
| `--match-path <PATH>` | path glob to match (default / covers only the root; /** covers every path) |  |
| `--match-method <METHOD>` | HTTP method to match (repeat for multiple) |  |
| `--match-header <Name=Value>` | exact request header selector (repeat) |  |
| `--priority <N>` | match priority; lower wins (default 100) |  |
| `--enabled[=true|false]` | whether the rule is enabled (default true) | one of `true` · `false` |
| `--throttle-requests-per-second <RPS>` | kind=throttle: refill rate in requests per second |  |
| `--throttle-burst <N>` | kind=throttle: token-bucket burst |  |
| `--throttle-key-by <KEY>` | kind=throttle: bucket key (none\|api_key\|consumer_id\|jwt_subject\|jwt_claim\|country\|ip\|composite) |  |
| `--redirect-status <CODE>` | kind=redirect: 301\|302\|307\|308 |  |
| `--redirect-to <URL>` | kind=redirect: Location URL |  |
| `--template` | kind=redirect\|headers: expand ${name} request values (host, path, query, client_ip, country, asn, header:&lt;name&gt;, …) |  |
| `--rewrite-from <PATH>` | kind=rewrite: from path |  |
| `--rewrite-to <PATH>` | kind=rewrite: to path |  |
| `--route-target-slug <slug>` | kind=route: target app slug |  |
| `--cache-max-age-seconds <N>` | kind=cache: fresh window (default 60; max 3600) |  |
| `--cache-stale-while-revalidate-seconds <N>` | kind=cache: serve stale during a background refresh (max 300) |  |
| `--budget-ms <MS>` | kind=budget: execution budget in ms (at most the plan ceiling: 30 s Free, 60 s Hobby, 5 min Pro, 15 min Scale) |  |
| `--retry-max-attempts <N>` | kind=retry: total attempts including the original (default 2; max 3) |  |
| `--circuit-failure-threshold <RATIO>` | kind=circuit_breaker: failure ratio that opens the app&#39;s instance-health breaker (default 0.5); the highest-priority rule tunes every instance, selectors do not partition it |  |
| `--circuit-open-seconds <N>` | kind=circuit_breaker: first open interval (default 5) |  |
| `--respond-status <CODE>` | kind=respond (preview apps only): response status (default 200) |  |
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
| `--waf-mode <MODE>` | kind=waf: observe (default), warn, or block; warn and block check headers and URL in-path at paranoia level 1 | one of `observe` · `warn` · `block` |
| `--waf-paranoia-level <N>` | kind=waf: OWASP CRS paranoia level 1 or 2 (default 1) |  |
| `--waf-anomaly-threshold <N>` | kind=waf: anomaly score that counts as a detection, 1..100 (default 5) |  |
| `--waf-exclude-rules <IDS>` | kind=waf: comma-separated CRS rule IDs left out of scoring |  |
| `--waf-inspect-body-bytes <BYTES>` | kind=waf: request body bytes to inspect, 1..65536 (default 8192) |  |
| `--challenge-difficulty <BITS>` | kind=challenge: proof-of-work difficulty in leading zero bits, 10..22 (default 16) |  |
| `--challenge-pass-ttl <SECONDS>` | kind=challenge: seconds a solved challenge lets the browser through, 300..86400 (default 3600) |  |
| `--challenge-max-difficulty <BITS>` | kind=challenge: raise the difficulty up to this many bits, at most 22, while the rule serves far more challenge pages than usual (default: fixed) |  |
| `--validate-schema <JSON|@FILE|->` | JSON Schema (inline JSON, @file, or - for stdin; max 64 KiB) |  |
| `--validate-mode <MODE>` | invalid-request behavior (default block) | one of `block` · `observe` · `warn` |
| `--validate-content-type <TYPE>` | accepted application media type (repeat; e.g. application/json) |  |
| `--validate-max-body-bytes <N>` | optional body cap in bytes (0 = 25 MiB or the plan cap, whichever is lower) |  |
| `--validate-apply-while-streaming` | also validate streaming requests |  |
| `--validate-reject-unknown-fields` | reject fields not declared by the schema |  |
| `--validate-path-template <TEMPLATE>` | OpenAPI path template for the path schema (match-path must use ?* for each placeholder) |  |
| `--validate-path-schema <JSON|@FILE|->` | object JSON Schema for path parameters (inline JSON, @file, or -) |  |
| `--validate-query-schema <JSON|@FILE|->` | object JSON Schema for query parameters (inline JSON, @file, or -) |  |
| `--validate-headers-schema <JSON|@FILE|->` | object JSON Schema for request headers, lowercase names (inline JSON, @file, or -) |  |
| `--cors-allow-credentials` | kind=cors: allow credentials |  |
| `--cors-max-age-seconds <SECONDS>` | kind=cors: preflight max age |  |
| `--jwt-platform-tenant-external-ref-claim <CLAIM>` | kind=jwt: claim containing the platform tenant external reference |  |
| `--limit-max-body-bytes <BYTES>` | kind=limit: buffered body cap (required; 1..25 MiB) |  |
| `--limit-max-body-bytes-streaming <BYTES>` | kind=limit: streaming body cap (0 inherits buffered cap) |  |
| `--throttle-jwt-claim <CLAIM>` | kind=throttle: JWT claim when key-by is jwt_claim |  |
| `--throttle-max-keys-per-rule <N>` | kind=throttle: maximum distinct consumer buckets |  |
| `--throttle-missing-key-policy <POLICY>` | kind=throttle: behavior when identity is missing | one of `shared` · `reject` |
| `--throttle-key-field <FIELD>` | kind=throttle: composite key field (ip\|country\|api_key\|consumer_id\|jwt_subject\|jwt_claim\|method\|path\|header:&lt;name&gt;; repeat) |  |
| `--throttle-count-status <CODE>` | kind=throttle: charge the bucket only for responses with this status (repeat) |  |
| `--cache-stale-if-error-seconds <SECONDS>` | kind=cache: serve stale on origin failure (default 300; 0 disables; max 300) |  |
| `--total-deadline-ms <MS>` | kind=budget: total deadline from public ingress, including upload, wake and queue |  |
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
| `--match <JSON|@FILE|->` | match condition ANDed with the selectors (JSON, @file, or -) |  |
| `--mode <MODE>` | enforce (default) or log: a log-mode rule only counts matches | one of `enforce` · `log` |
| `--penalty-for <DURATION>` | kind=ip\|geo\|throttle\|waf (block): block a client this rule keeps refusing from the whole app for this long (1m to 24h) |  |
| `--penalty-after <N>` | with --penalty-for: refusals that box the client (default 1) |  |
| `--penalty-within <DURATION>` | with --penalty-for: window the refusals count in (default 1m) |  |
| `--cache-methods <METHOD>` | kind=cache: cacheable method (repeatable) |  |
| `--cache-vary-on <HEADER>` | kind=cache: header to vary on (repeatable) |  |
| `--cors-allow-header <HEADER>` | kind=cors: allowed header (repeatable) |  |
| `--cors-allow-method <METHOD>` | kind=cors: allowed method (repeatable) |  |
| `--cors-allow-origin <ORIGIN>` | kind=cors: allowed origin (repeatable) |  |
| `--cors-expose-header <HEADER>` | kind=cors: exposed header (repeatable) |  |
| `--description <TEXT>` | operator-facing description (&lt;=1000 chars) |  |
| `--expires-at <RFC3339>` | stop applying the rule at this RFC 3339 time |  |
| `--expires-in <DURATION>` | stop applying the rule after this duration (e.g. 2h) |  |
| `--headers-request-add <NAME:VALUE>` | kind=headers: request header to add (repeatable) |  |
| `--headers-request-remove <NAME>` | kind=headers: request header to remove (repeatable) |  |
| `--headers-request-set <NAME:VALUE>` | kind=headers: request header to set (repeatable) |  |
| `--headers-response-add <NAME:VALUE>` | kind=headers: response header to add (repeatable) |  |
| `--headers-response-remove <NAME>` | kind=headers: response header to remove (repeatable) |  |
| `--headers-response-set <NAME:VALUE>` | kind=headers: response header to set (repeatable) |  |
| `--jwt-algorithm <ALG>` | kind=jwt: allowed signing algorithm (repeatable) |  |
| `--jwt-audience <AUDIENCE>` | kind=jwt: required audience (repeatable) |  |
| `--jwt-require-exp` | kind=jwt: reject tokens without an exp claim (default true; =false accepts them) |  |
| `--jwt-required-claim <NAME=VALUE>` | kind=jwt: required claim (repeatable) |  |
| `--name <NAME>` | operator-facing rule name (&lt;=100 chars) |  |
| `--redirect-header <NAME:VALUE>` | kind=redirect: extra response header (repeatable) |  |

Examples:

```sh
gregale edge-rules create --app my-api --kind throttle --match-host my-api.gregale.dev --match-path /search --throttle-requests-per-second 5 --throttle-burst 10
gregale edge-rules create --app my-api --kind throttle --match-host my-api.gregale.dev --match-path /login --throttle-requests-per-second 0.1 --throttle-burst 5 --throttle-key-by composite --throttle-key-field ip --throttle-key-field header:x-username --throttle-count-status 401
gregale edge-rules create --app my-api --kind redirect --match-host my-api.gregale.dev --match-path /old --redirect-status 308 --redirect-to https://my-api.gregale.dev/new
gregale edge-rules create --app my-api --kind cache --match-host my-api.gregale.dev --match-path /catalog --cache-max-age-seconds 60
gregale edge-rules create --app my-site --kind challenge --match-host my-site.gregale.dev --match-path '/*' --match '{"field":"verified_bot","op":"missing"}'
gregale edge-rules create --app my-api --kind budget --match-host my-api.gregale.dev --match-path /reports --budget-ms 20000
gregale edge-rules create --app my-api --kind validate --match-host api.example.com --validate-schema @schema.json --validate-content-type application/json --validate-mode block
cat schema.json | gregale edge-rules create --app my-api --kind validate --match-host api.example.com --validate-schema -
gregale edge-rules create --app my-api --kind validate --match-host api.example.com --match-path '/users/?*' --match-method GET --validate-path-template '/users/{id}' --validate-path-schema '{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}' --validate-query-schema @query.json
```

### edge-rules get

Show one edge rule

`gregale edge-rules get <id>`

### edge-rules update

Update one edge rule

`gregale edge-rules update [--match-host <HOST>] [--match-path <PATH>] [--match-method <METHOD>]... [--match-header <NAME=VALUE>]... [--clear-match-headers] [--priority <N>] [--enable] [--disable] [--kind <KIND>] [--route-target-slug <SLUG>] [--rewrite-from <PATH>] [--rewrite-to <PATH>] [--redirect-status <CODE>] [--redirect-to <URL>] [--template] [--redirect-header <NAME:VALUE>]... [--headers-request-add <NAME:VALUE>]... [--headers-request-set <NAME:VALUE>]... [--headers-request-remove <NAME>]... [--headers-response-add <NAME:VALUE>]... [--headers-response-set <NAME:VALUE>]... [--headers-response-remove <NAME>]... [--cors-allow-origin <ORIGIN>]... [--cors-allow-method <METHOD>]... [--cors-allow-header <HEADER>]... [--cors-expose-header <HEADER>]... [--cors-allow-credentials] [--cors-max-age-seconds <SECONDS>] [--jwt-issuer <ISSUER>] [--jwt-jwks-url <URL>] [--jwt-audience <AUDIENCE>]... [--jwt-algorithm <ALG>]... [--jwt-required-claim <NAME=VALUE>]... [--jwt-platform-tenant-external-ref-claim <CLAIM>] [--ip-allow <CIDR>]... [--ip-deny <CIDR>]... [--geo-allow <CC>]... [--geo-deny <CC>]... [--limit-max-body-bytes <BYTES>] [--limit-max-body-bytes-streaming <BYTES>] [--throttle-requests-per-second <RPS>] [--throttle-burst <N>] [--throttle-key-by <KEY>] [--throttle-jwt-claim <CLAIM>] [--throttle-max-keys-per-rule <N>] [--throttle-missing-key-policy <POLICY>] [--throttle-key-field <FIELD>] [--throttle-count-status <CODE>] [--cache-max-age-seconds <SECONDS>] [--cache-stale-while-revalidate-seconds <SECONDS>] [--cache-stale-if-error-seconds <SECONDS>] [--cache-vary-on <HEADER>]... [--cache-methods <METHOD>]... [--budget-ms <MS>] [--total-deadline-ms <MS>] [--budget-allow-override-header <HEADER>] [--retry-max-attempts <N>] [--retry-allow-non-idempotent] [--retry-min-remaining-ms <MS>] [--retry-backoff-ms <MS>] [--retry-budget-percent <PERCENT>] [--retry-budget-min-retries <N>] [--circuit-failure-threshold <RATIO>] [--circuit-min-requests <N>] [--circuit-window-seconds <SECONDS>] [--circuit-open-seconds <SECONDS>] [--circuit-max-open-seconds <SECONDS>] [--maintenance-retry-after-seconds <SECONDS>] [--maintenance-message <TEXT>] [--respond-status <CODE>] [--respond-body <JSON>] [--on-success-webhook <ID>] [--on-failure-webhook <ID>] [--async-max-attempts <N>] [--async-retry-base-seconds <N>] [--async-retry-max-seconds <N>] [--async-retry-jitter-seconds <N>] [--async-max-age-seconds <N>] [--waf-mode <MODE>] [--waf-paranoia-level <N>] [--waf-anomaly-threshold <N>] [--waf-exclude-rules <IDS>] [--waf-inspect-body-bytes <BYTES>] [--challenge-difficulty <BITS>] [--challenge-pass-ttl <SECONDS>] [--challenge-max-difficulty <BITS>] [--validate-schema <JSON|@FILE|->] [--validate-mode <MODE>] [--validate-content-type <TYPE>] [--validate-max-body-bytes <N>] [--validate-apply-while-streaming] [--validate-reject-unknown-fields] [--validate-path-template <TEMPLATE>] [--validate-path-schema <JSON|@FILE|->] [--validate-query-schema <JSON|@FILE|->] [--validate-headers-schema <JSON|@FILE|->] [--match <JSON|@FILE|->] [--clear-match] [--mode <MODE>] [--penalty-for <DURATION>] [--penalty-after <N>] [--penalty-within <DURATION>] [--clear-penalty] [--clear-expiry] [--description <TEXT>] [--expires-at <RFC3339>] [--expires-in <DURATION>] [--jwt-require-exp] [--name <NAME>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--match-host <HOST>` | new host to match; @app covers every host the app serves |  |
| `--match-path <PATH>` | new path to match |  |
| `--match-method <METHOD>` | replacement HTTP method (repeatable) |  |
| `--match-header <NAME=VALUE>` | replacement exact request header selector (repeatable) |  |
| `--clear-match-headers` | remove all request header selectors |  |
| `--priority <N>` | new match priority |  |
| `--enable` | enable the rule |  |
| `--disable` | disable the rule |  |
| `--kind <KIND>` | new action kind (required when changing action flags) | one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` · `respond` · `retry` · `circuit_breaker` · `waf` · `async` · `challenge` |
| `--route-target-slug <SLUG>` | kind=route: target app slug |  |
| `--rewrite-from <PATH>` | kind=rewrite: source path |  |
| `--rewrite-to <PATH>` | kind=rewrite: destination path |  |
| `--redirect-status <CODE>` | kind=redirect: response status |  |
| `--redirect-to <URL>` | kind=redirect: Location URL |  |
| `--template` | kind=redirect\|headers: expand ${name} request values (host, path, query, client_ip, country, asn, header:&lt;name&gt;, …) |  |
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
| `--throttle-key-by <KEY>` | kind=throttle: bucket key | one of `none` · `api_key` · `consumer_id` · `jwt_subject` · `jwt_claim` · `country` · `ip` · `composite` |
| `--throttle-jwt-claim <CLAIM>` | kind=throttle: JWT claim when key-by is jwt_claim |  |
| `--throttle-max-keys-per-rule <N>` | kind=throttle: maximum distinct consumer buckets |  |
| `--throttle-missing-key-policy <POLICY>` | kind=throttle: behavior when identity is missing | one of `shared` · `reject` |
| `--throttle-key-field <FIELD>` | kind=throttle: composite key field (ip\|country\|api_key\|consumer_id\|jwt_subject\|jwt_claim\|method\|path\|header:&lt;name&gt;; repeat) |  |
| `--throttle-count-status <CODE>` | kind=throttle: charge the bucket only for responses with this status (repeat) |  |
| `--cache-max-age-seconds <SECONDS>` | kind=cache: fresh window |  |
| `--cache-stale-while-revalidate-seconds <SECONDS>` | kind=cache: stale-while-revalidate window |  |
| `--cache-stale-if-error-seconds <SECONDS>` | kind=cache: serve stale on origin failure (default 300; 0 disables) |  |
| `--cache-vary-on <HEADER>` | kind=cache: header to vary on (repeatable) |  |
| `--cache-methods <METHOD>` | kind=cache: cacheable method (repeatable) |  |
| `--budget-ms <MS>` | kind=budget: per-request wall-clock budget |  |
| `--total-deadline-ms <MS>` | kind=budget: total deadline from public ingress, including upload, wake and queue |  |
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
| `--respond-status <CODE>` | kind=respond (preview apps only): response status |  |
| `--respond-body <JSON>` | kind=respond: JSON response body |  |
| `--on-success-webhook <ID>` | success webhook subscription |  |
| `--on-failure-webhook <ID>` | failure webhook subscription |  |
| `--async-max-attempts <N>` | total attempts (0 = plan default; capped by plan) |  |
| `--async-retry-base-seconds <N>` | exponential retry base delay |  |
| `--async-retry-max-seconds <N>` | maximum exponential retry delay |  |
| `--async-retry-jitter-seconds <N>` | retry jitter fraction (0..1) |  |
| `--async-max-age-seconds <N>` | invocation lifetime from acceptance (0 = plan default; capped by plan) |  |
| `--waf-mode <MODE>` | kind=waf: observe (default), warn, or block; warn and block check headers and URL in-path at paranoia level 1 | one of `observe` · `warn` · `block` |
| `--waf-paranoia-level <N>` | kind=waf: OWASP CRS paranoia level 1 or 2 (default 1) |  |
| `--waf-anomaly-threshold <N>` | kind=waf: anomaly score that counts as a detection, 1..100 (default 5) |  |
| `--waf-exclude-rules <IDS>` | kind=waf: comma-separated CRS rule IDs left out of scoring |  |
| `--waf-inspect-body-bytes <BYTES>` | kind=waf: request body bytes to inspect, 1..65536 (default 8192) |  |
| `--challenge-difficulty <BITS>` | kind=challenge: proof-of-work difficulty in leading zero bits, 10..22 (default 16) |  |
| `--challenge-pass-ttl <SECONDS>` | kind=challenge: seconds a solved challenge lets the browser through, 300..86400 (default 3600) |  |
| `--challenge-max-difficulty <BITS>` | kind=challenge: raise the difficulty up to this many bits, at most 22, while the rule serves far more challenge pages than usual (default: fixed) |  |
| `--validate-schema <JSON|@FILE|->` | replacement body schema; an action update replaces the body schema and parameter schemas together (inline JSON, @file, or -; max 64 KiB) |  |
| `--validate-mode <MODE>` | invalid-request behavior | one of `block` · `observe` · `warn` |
| `--validate-content-type <TYPE>` | accepted application media type (repeat; e.g. application/json) |  |
| `--validate-max-body-bytes <N>` | body cap in bytes (0 = 25 MiB or the plan cap, whichever is lower) |  |
| `--validate-apply-while-streaming` | also validate streaming requests |  |
| `--validate-reject-unknown-fields` | reject fields not declared by the schema |  |
| `--validate-path-template <TEMPLATE>` | OpenAPI path template for the path schema (match-path must use ?* for each placeholder) |  |
| `--validate-path-schema <JSON|@FILE|->` | object JSON Schema for path parameters (inline JSON, @file, or -) |  |
| `--validate-query-schema <JSON|@FILE|->` | object JSON Schema for query parameters (inline JSON, @file, or -) |  |
| `--validate-headers-schema <JSON|@FILE|->` | object JSON Schema for request headers, lowercase names (inline JSON, @file, or -) |  |
| `--match <JSON|@FILE|->` | replace the match condition (JSON, @file, or -) |  |
| `--clear-match` | remove the match condition |  |
| `--mode <MODE>` | enforce or log (log-mode rules only count matches) | one of `enforce` · `log` |
| `--penalty-for <DURATION>` | kind=ip\|geo\|throttle\|waf (block): block a client this rule keeps refusing from the whole app for this long (1m to 24h) |  |
| `--penalty-after <N>` | with --penalty-for: refusals that box the client (default 1) |  |
| `--penalty-within <DURATION>` | with --penalty-for: window the refusals count in (default 1m) |  |
| `--clear-penalty` | remove the rule&#39;s penalty box, releasing the clients it holds |  |
| `--clear-expiry` | remove the rule&#39;s expiry |  |
| `--description <TEXT>` | operator-facing description (&lt;=1000 chars) |  |
| `--expires-at <RFC3339>` | stop applying the rule at this RFC 3339 time |  |
| `--expires-in <DURATION>` | stop applying the rule after this duration (e.g. 2h) |  |
| `--jwt-require-exp` | kind=jwt: reject tokens without an exp claim (default true; =false accepts them) |  |
| `--name <NAME>` | operator-facing rule name (&lt;=100 chars) |  |

Examples:

```sh
gregale edge-rules update RULE_ID --kind validate --validate-schema @schema.json --validate-mode block
gregale edge-rules update RULE_ID --kind validate --validate-mode observe
```

### edge-rules rm

Delete one edge rule

`gregale edge-rules rm [--yes] [--app <slug>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | skip the typed confirmation (alias: --quiet) |  |
| `--app <slug>` | require the rule to belong to this app |  |

### edge-rules history

List recorded versions of an app&#39;s edge rules

`gregale edge-rules history --app <slug> [--version <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--version <N>` | show the rules recorded in this version |  |

--version N shows that version&#39;s rules.

### edge-rules recipes

List protection recipes: ready-made rules that start in log mode

### edge-rules recipe

Add a protection recipe&#39;s rule to an app in log mode

`gregale edge-rules recipe [--app <slug>] [--host <HOST>] [--set <NAME=VALUE>] [--enforce] [--alert-webhook-url <URL>] [--alert-webhook-secret-stdin] [--alert-webhook-secret <VALUE>] [--alert-threshold <N>] <recipe>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project context) |  |
| `--host <HOST>` | restrict the rule to one hostname (default: every host the app serves) |  |
| `--set <NAME=VALUE>` | recipe parameter name=value (repeat) |  |
| `--enforce` | enforce immediately instead of starting in log mode |  |
| `--alert-webhook-url <URL>` | also alert this webhook when the rule spikes |  |
| `--alert-webhook-secret-stdin` | read the spike alert&#39;s webhook signing secret from stdin |  |
| `--alert-webhook-secret <VALUE>` | spike alert webhook signing secret (prefer --alert-webhook-secret-stdin) |  |
| `--alert-threshold <N>` | spike alert threshold in matches per hour (default 100) |  |

Examples:

```sh
gregale edge-rules recipe block-unverified-bots --app my-api
gregale edge-rules recipe allow-ips-only --app my-api --set cidrs=203.0.113.7,198.51.100.0/24
```

Use --enforce to enforce it immediately.

### edge-rules suggest

Suggest protection recipes from the app&#39;s recent traffic

`gregale edge-rules suggest [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project context) |  |

Examples:

```sh
gregale edge-rules suggest --app my-api
```

Prints the command to add each.

### edge-rules stats

Show match counts per rule

`gregale edge-rules stats --app <slug> [--window <WINDOW>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--window <WINDOW>` | window | one of `1h` · `24h` · `7d` |

Enforced rules count matches; log-mode rules count logged requests.

### edge-rules review

Show which log-mode rules are ready to enforce

`gregale edge-rules review [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project context) |  |

Examples:

```sh
gregale edge-rules review --app my-api
```

Also lists rules still collecting or quiet for 7 days, with the command for each.

### edge-rules events

Show sampled requests that rules matched, newest first

`gregale edge-rules events --app <slug> [--rule <ID>] [--request-id <ID>] [--outcome <OUTCOME>] [--since <DURATION>] [--limit <N>] [--cursor <CURSOR>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--rule <ID>` | only this rule id |  |
| `--request-id <ID>` | only this request, e.g. the reference a visitor without JavaScript saw on the challenge page |  |
| `--outcome <OUTCOME>` | outcome filter; detected, warned and blocked are kind=waf detections; challenged, rejected, solved and failed are kind=challenge outcomes; boxed is a client put in a penalty box | one of `matched` · `logged` · `detected` · `warned` · `blocked` · `challenged` · `rejected` · `solved` · `failed` · `boxed` |
| `--since <DURATION>` | how far back, e.g. 1h, 24h, 7d (clamped to the plan window) |  |
| `--limit <N>` | events per page (1..200) |  |
| `--cursor <CURSOR>` | continue from a previous page&#39;s next cursor |  |

Examples:

```sh
gregale edge-rules events --app my-api --outcome logged --since 7d
gregale edge-rules events --app my-api --outcome blocked
gregale edge-rules events --app my-site --request-id REQUEST_ID
```

Includes kind=waf detections; up to 10 of each per rule per minute, kept 7 days.

### edge-rules waf-review

Group WAF detections by rule and path to find false positives

`gregale edge-rules waf-review [--app <slug>] [--rule <ID>] [--since <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project context) |  |
| `--rule <ID>` | only this kind=waf rule id |  |
| `--since <DURATION>` | how far back, e.g. 24h or 7d (default 7d, clamped to the plan window) |  |

Examples:

```sh
gregale edge-rules waf-review --app my-api
gregale edge-rules waf-review --app my-api --rule RULE_ID --since 24h
```

Each group is assessed: likely_false_positive (a documented false positive, or many clients flagged on one path), likely_attack (clients flagged on many paths), excluded or review.

Detections are sampled, so counts are lower bounds.

### edge-rules waf-exclude

Add or remove CRS rule IDs on a kind=waf rule&#39;s exclusions

`gregale edge-rules waf-exclude [--add <IDS>] [--remove <IDS>] <rule-id>`

| Flag | Meaning | |
|---|---|---|
| `--add <IDS>` | CRS rule IDs to exclude, comma-separated |  |
| `--remove <IDS>` | CRS rule IDs to stop excluding, comma-separated |  |

Examples:

```sh
gregale edge-rules waf-exclude RULE_ID --add 942190
gregale edge-rules waf-exclude RULE_ID --remove 942100
```

Keeps the rest of the rule&#39;s WAF action and its penalty.

### edge-rules rollback

Restore an app&#39;s edge rules to a recorded version

`gregale edge-rules rollback --app <slug> --to <N> [--quiet]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--to <N>` | version to restore | required |
| `--quiet` | skip the typed confirmation (for scripts) |  |

Examples:

```sh
gregale edge-rules rollback --app my-api --to 12
```

The restore is recorded as a new version.


## openapi

Manage an app&#39;s OpenAPI docs and check for breaking changes

`gregale openapi [<subcommand>]`

### openapi diff

Diff two openapi.yaml files; exit 2 on any BREAKING row

`gregale openapi diff <baseline.yaml> <proposed.yaml>`

### openapi get

Fetch an app&#39;s OpenAPI document

`gregale openapi get [--source <manual_import|auto>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--source <manual_import|auto>` | document source | one of `manual_import` · `auto` |

The source is manual_import or auto; the app defaults to the linked one.

### openapi import

Import an app OpenAPI document from a JSON file or stdin

`gregale openapi import <slug> <file|->`

### openapi dry-run

Preview uncovered routes without importing the document

`gregale openapi dry-run [<slug>] <file|->`

The app defaults to the linked one.

### openapi preview

Preview routes, edge policies and the OpenAPI contract diff

`gregale openapi preview [--scope <scope>] [--fail-on-unavailable] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--scope <scope>` | deployment scope to compare (defaults to linked environment, otherwise prod) |  |
| `--fail-on-unavailable` | fail when the contract-diff backend is unavailable |  |

Read-only; the app defaults to the linked one.

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

### routes status

Show each route&#39;s contract, traffic, health, budget and gaps

`gregale routes status [--deployment <ID>] [--since <DURATION>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | serving deployment UUID or vN (default: the app&#39;s serving deployment) |  |
| `--since <DURATION>` | observed usage window (default 168h; also accepts 7d or RFC3339) |  |

Examples:

```sh
gregale routes status my-api
gregale routes status my-api --since 24h --json
```

Also shows canary health and rollback mode.

### routes advise

Suggest cache, async and throttle rules from observed traffic

`gregale routes advise [--since <DURATION>] [--until <RFC3339>] [--cache-max-age <SECONDS>] [--apply <ID>] [--enable] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | traffic window to analyze (default 7d; clamped to telemetry retention) |  |
| `--until <RFC3339>` | end of the window (default now) |  |
| `--cache-max-age <SECONDS>` | what-if cache lifetime for cache suggestions (default 60) |  |
| `--apply <ID>` | create the edge rules of this suggestion (disabled for review) |  |
| `--enable` | with --apply, create the rules enabled |  |

Examples:

```sh
gregale routes advise my-api
gregale routes advise my-api --cache-max-age 300
gregale routes advise my-api --apply 3f9a1c2b7d4e
```

Includes what-if estimates.

### routes awake

Show which routes keep a scale-to-zero app awake and their cost

`gregale routes awake [--since <DURATION>] [--until <RFC3339>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | window to analyze (default 7d; clamped to telemetry retention) |  |
| `--until <RFC3339>` | end of the window (default now) |  |

Examples:

```sh
gregale routes awake my-api
gregale routes awake my-api --since 24h --json
```

Explains how to let the app park.

### routes insights

List routes that keep the app awake or are safe to cache

`gregale routes insights [--fix <INSIGHT_ID>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--fix <INSIGHT_ID>` | apply the fix the insight lists now |  |

Examples:

```sh
gregale routes insights my-api
gregale routes insights my-api --json
gregale routes insights my-api --fix 6c1e0b9a-3f7d-4a51-9c2e-8d4b5f0a1e27
```

Each finding has a fix; findings are evaluated every six hours.

### routes priority

Show or set which routes are served first when instances are busy

`gregale routes priority [--critical <[METHOD] PATH>] [--bulk <[METHOD] PATH>] [--clear] [--reset] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--critical <[METHOD] PATH>` | route served first when the app is saturated (repeatable) |  |
| `--bulk <[METHOD] PATH>` | route served last and turned away first (repeatable) |  |
| `--clear` | save no priorities, not even the route-health default |  |
| `--reset` | delete saved priorities and use the route-health default |  |

Examples:

```sh
gregale routes priority my-api
gregale routes priority my-api --critical "POST /checkout" --bulk "/exports/*"
gregale routes priority my-api --reset
```

Priority routes are also turned away last.

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

Read current route intent and optionally export requirements

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

Evaluate proposed budgets against recent production traffic

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

Nothing is saved.

#### routes monitor set

Save advisory production route budgets

`gregale routes monitor set --mode <MODE> --routes <PATH> --expected-revision <N> [--on-violation <ACTION>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--mode <MODE>` | enabled or disabled | required; one of `enabled` · `disabled` |
| `--routes <PATH>` | JSON array of exact method/path labels with max_5xx_rate_bps and/or max_p95_ms | required |
| `--expected-revision <N>` | current monitor revision; 0 initially | required |
| `--on-violation <ACTION>` | report (default) or rollback to the last healthy deployment when an error budget is violated within 30 minutes of release | one of `report` · `rollback` |

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

Inspect a saved incident, optionally with affected customers

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

Can also correlate changed route owners.

### routes lifecycle

Review deployed routes for carefully evidenced retirement candidates

#### routes lifecycle declarations

Review lifecycle declarations between captured deployments

`gregale routes lifecycle declarations --from-deployment <UUID> --to-deployment <UUID> [--out <PATH>] [--fail-on-findings] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--from-deployment <UUID>` | serving baseline deployment | required |
| `--to-deployment <UUID>` | candidate deployment | required |
| `--out <PATH>` | save JSON to a new file |  |
| `--fail-on-findings` | fail on lifecycle regressions or incomplete declarations |  |

#### routes lifecycle prepare-approval

Prepare a pinned successor approval request for review

`gregale routes lifecycle prepare-approval --from-deployment <UUID> --to-deployment <UUID> --mappings <PATH> --out <PATH> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--from-deployment <UUID>` | serving baseline deployment | required |
| `--to-deployment <UUID>` | candidate deployment | required |
| `--mappings <PATH>` | explicit successor mappings and optional destination pins | required |
| `--out <PATH>` | save request to a new file | required |

#### routes lifecycle approve

Submit a reviewed lifecycle successor approval

`gregale routes lifecycle approve --request <PATH> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--request <PATH>` | pinned request JSON | required |

#### routes lifecycle receipt

Read a persisted lifecycle approval receipt

`gregale routes lifecycle receipt --id <UUID> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | approval receipt ID | required |

#### routes lifecycle history

Inspect applied and blocked production lifecycle reviews

`gregale routes lifecycle history [--limit <N>] [--before <ID>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | reviews per page (default 10; maximum 20) |  |
| `--before <ID>` | retained review ID from next_cursor |  |

Examples:

```sh
gregale routes lifecycle history api --json
```

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

### routes sunsets

Review upcoming sunsets and remaining callers

`gregale routes sunsets <diff> [--source <SOURCE>] --deployment <UUID> [--since <WINDOW>] [--within <DURATION>] [--mapping <PATH>] [--out <PATH>] [--fail-on-overdue] [--fail-on-incomplete] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--source <SOURCE>` | metadata source (default deployment) | one of `deployment` · `manual_import` |
| `--deployment <UUID>` | baseline deployment ID | required |
| `--since <WINDOW>` | retained telemetry window (default 14d) |  |
| `--within <DURATION>` | upcoming sunset horizon (default 720h) |  |
| `--mapping <PATH>` | explicit successor mapping JSON |  |
| `--out <PATH>` | save report JSON to a new file |  |
| `--fail-on-overdue` | fail on elapsed sunset dates |  |
| `--fail-on-incomplete` | fail on missing metadata or incomplete evidence |  |

#### routes sunsets diff

Compare retained sunset evidence and caller regressions

`gregale routes sunsets diff --before <PATH> --after <PATH> [--out <PATH>] [--max-staleness <DURATION>] [--fail-on-regression] [--fail-on-incomplete]`

| Flag | Meaning | |
|---|---|---|
| `--before <PATH>` | previous saved report | required |
| `--after <PATH>` | new saved report | required |
| `--out <PATH>` | save diff JSON to a new file |  |
| `--max-staleness <DURATION>` | maximum report age (default 24h) |  |
| `--fail-on-regression` | fail on observed regressions |  |
| `--fail-on-incomplete` | fail on stale, changed or incomplete evidence |  |

### routes migration

Suggest successors, review contracts and assess cutover readiness

#### routes migration policy

Read or set the app server policy for production route removal

`gregale routes migration policy --app <APP> [--mode <MODE>] [--expected-revision <N>] [--grace-period <DURATION>] [--max-approval-age <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <APP>` | production app slug | required |
| `--mode <MODE>` | omit to read; set report or enforce to write | one of `report` · `enforce` |
| `--expected-revision <N>` | current revision for a write; 0 creates the policy |  |
| `--grace-period <DURATION>` | server-observed quiet period (default 720h, minimum 1h) |  |
| `--max-approval-age <DURATION>` | approval TTL (default 1h, maximum 72h) |  |

#### routes migration server-check

Read authoritative removal blockers for an exact candidate

`gregale routes migration server-check --app <APP> --candidate-deployment <ID> [--fail-on-blocked]`

| Flag | Meaning | |
|---|---|---|
| `--app <APP>` | production app slug | required |
| `--candidate-deployment <ID>` | candidate deployment UUID | required |
| `--fail-on-blocked` | exit 1 if the server check is blocked |  |

#### routes migration authorize

Store an authenticated admin approval after local and server checks

`gregale routes migration authorize --app <APP> --baseline-deployment <ID> --candidate-deployment <ID> --expected-revision <N> --readiness <PATH> --mapping <PATH> --acknowledge-observed-only [--out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <APP>` | production app slug | required |
| `--baseline-deployment <ID>` | serving baseline UUID | required |
| `--candidate-deployment <ID>` | candidate deployment UUID | required |
| `--expected-revision <N>` | current server policy revision | required |
| `--readiness <PATH>` | reviewed migration readiness JSON | required |
| `--mapping <PATH>` | reviewed same-app successor mapping | required |
| `--acknowledge-observed-only` | acknowledge the limits of observed telemetry | required |
| `--out <PATH>` | save server approval receipt to a new file |  |

#### routes migration gate

Explain removed-route blockers; optionally fail a local CI check

`gregale routes migration gate --app <APP> --baseline-deployment <ID> --candidate-deployment <ID> [--candidate-app <APP>] [--readiness <PATH>] [--mapping <PATH>] [--owner-approval <PATH>] [--mode <MODE>] [--max-evidence-age <DURATION>] [--out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <APP>` | production app slug | required |
| `--baseline-deployment <ID>` | serving deployment UUID | required |
| `--candidate-deployment <ID>` | candidate deployment UUID | required |
| `--candidate-app <APP>` | candidate app slug (defaults to --app; may be a preview) |  |
| `--readiness <PATH>` | migration readiness JSON for the serving deployment |  |
| `--mapping <PATH>` | reviewed successor mapping JSON |  |
| `--owner-approval <PATH>` | owner attestation JSON for this exact change |  |
| `--mode <MODE>` | report (default) or enforce (exit 1 for blockers) | one of `report` · `enforce` |
| `--max-evidence-age <DURATION>` | maximum evidence age (default and maximum 72h) |  |
| `--out <PATH>` | save gate JSON to a new file |  |

Examples:

```sh
gregale routes migration gate --app api --baseline-deployment BASELINE_UUID --candidate-deployment CANDIDATE_UUID --readiness readiness.json --mapping route-successors.json --owner-approval approval.json --mode enforce
```

#### routes migration approve

Record a local owner attestation after all cutover checks pass

`gregale routes migration approve --app <APP> --baseline-deployment <ID> --candidate-deployment <ID> [--candidate-app <APP>] --readiness <PATH> --mapping <PATH> --approved-by <OWNER> [--mode <MODE>] [--max-evidence-age <DURATION>] --out <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--app <APP>` | production app slug | required |
| `--baseline-deployment <ID>` | serving deployment UUID | required |
| `--candidate-deployment <ID>` | candidate deployment UUID | required |
| `--candidate-app <APP>` | candidate app slug (defaults to --app; may be a preview) |  |
| `--readiness <PATH>` | migration readiness JSON for the serving deployment | required |
| `--mapping <PATH>` | mapping reviewed with the route owner | required |
| `--approved-by <OWNER>` | owner identity asserted in the local attestation | required |
| `--mode <MODE>` | report or enforce; approvals always reject blockers | one of `report` · `enforce` |
| `--max-evidence-age <DURATION>` | maximum evidence age (default and maximum 72h) |  |
| `--out <PATH>` | save the owner attestation to a new file | required |

Examples:

```sh
gregale routes migration approve --app api --baseline-deployment BASELINE_UUID --candidate-deployment CANDIDATE_UUID --readiness readiness.json --mapping route-successors.json --approved-by owner@example.com --out approval.json
```

#### routes migration readiness

Refresh mapped contracts and build an owner approval checkpoint

`gregale routes migration readiness --mapping <PATH> --from-deployment <APP=ID>... [--to-deployment <APP=ID>]... --snapshot <PATH>... [--grace-period <DURATION>] [--min-windows <COUNT>] [--max-staleness <DURATION>] [--format <FORMAT>] [--out <PATH>] [--fail-on-breaking] [--fail-on-incomplete] [--fail-on-not-ready]`

| Flag | Meaning | |
|---|---|---|
| `--mapping <PATH>` | version 1 explicit mapping reviewed with the route owner | required |
| `--from-deployment <APP=ID>` | baseline deployment as APP=ID; repeat for each app | required |
| `--to-deployment <APP=ID>` | successor deployment as APP=ID; repeat for each app |  |
| `--snapshot <PATH>` | saved customer tracker JSON; at least two observation windows | required |
| `--grace-period <DURATION>` | minimum continuous zero-traffic period before owner approval (default 30d) |  |
| `--min-windows <COUNT>` | minimum distinct complete observation windows (default 2) |  |
| `--max-staleness <DURATION>` | maximum age of the latest telemetry watermark (default 72h) |  |
| `--format <FORMAT>` | text, Markdown, or prioritized CSV action queue (default text; --json emits JSON) | one of `text` · `markdown` · `csv` |
| `--out <PATH>` | save the full readiness report to a new JSON file |  |
| `--fail-on-breaking` | exit 1 when any successor has a declared breaking change |  |
| `--fail-on-incomplete` | exit 1 when contract or telemetry evidence is incomplete |  |
| `--fail-on-not-ready` | exit 1 unless every route is ready for owner approval |  |

Examples:

```sh
gregale routes migration readiness --mapping route-successors.json --from-deployment checkout=OLD_DEPLOYMENT --to-deployment checkout=NEW_DEPLOYMENT --snapshot migration-week-1.json --snapshot migration-week-2.json --format markdown --out readiness.json
```

Joins customer windows into the checkpoint.

#### routes migration suggest

Rank successor routes and write a draft mapping for owner review

`gregale routes migration suggest --from-deployment <APP=ID>... --to-deployment <APP=ID>... [--sources <PATH>] [--since <WINDOW>] [--limit <N>] [--without-traffic] [--customer-details] [--out <PATH>] [--report-out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--from-deployment <APP=ID>` | baseline deployment as APP=ID; repeat for each app | required |
| `--to-deployment <APP=ID>` | successor deployment as APP=ID; repeat for each app | required |
| `--sources <PATH>` | optional version 1 mapping selecting sources with empty successors |  |
| `--since <WINDOW>` | observed usage window (default 14d) |  |
| `--limit <N>` | candidate rows per source (1–10, default 3) |  |
| `--without-traffic` | read captured contracts without usage |  |
| `--customer-details` | include observed source consumer and tenant UUIDs in the report |  |
| `--out <PATH>` | save a draft version 1 mapping to a new file for owner review |  |
| `--report-out <PATH>` | save explanations and captured evidence to a new JSON file |  |

Examples:

```sh
gregale routes migration suggest --from-deployment checkout=OLD_DEPLOYMENT --to-deployment checkout=NEW_DEPLOYMENT --out route-successors.json --report-out suggestions.json
```

#### routes migration review

Compare method, path, request, response and security contracts

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

Compare critical route errors to gate canary progression

Can compare p95 latency too.

#### routes health get

Read selected routes, mode and revision

`gregale routes health get <slug>`

#### routes health profile-history

Read retained CPU profile assessments for canary stages

`gregale routes health profile-history --deployment <ID> [--limit <N>] [--before <CURSOR>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | canary deployment UUID | required |
| `--limit <N>` | history page size (default 5; maximum 10) |  |
| `--before <CURSOR>` | opaque cursor from the prior page |  |

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

Rank routes by customer reach and traffic for canary selectors

`gregale routes health suggest --deployment <ID> [--since <WINDOW>] [--customer-group-by <DIMENSION>] [--limit <N>] [--preview-report <PATH>] [--out <PATH>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | immutable baseline deployment UUID; must match --preview-report when supplied | required |
| `--since <WINDOW>` | observed usage window (default 168h; accepts 7d or RFC3339) |  |
| `--customer-group-by <DIMENSION>` | rank by distinct tenant (default) or consumer count | one of `tenant` · `consumer` |
| `--limit <N>` | number of route selectors (default 10; maximum 20) |  |
| `--preview-report <PATH>` | restrict suggestions to source-affected routes in a bound preview report for this app and deployment |  |
| `--out <PATH>` | save a JSON selector array ready for routes health set |  |

Emits reviewable canary selectors.

#### routes health review

Join source-affected routes to canary coverage and health evidence

`gregale routes health review --deployment <ID> --preview-report <PATH> [--fail-on-incomplete] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--deployment <ID>` | candidate deployment UUID currently receiving canary traffic | required |
| `--preview-report <PATH>` | bound preview report containing source-affected routes | required |
| `--fail-on-incomplete` | exit nonzero unless source impact is complete and all affected route health is ready |  |

#### routes health review-release

Gate an aggregate preview review on canary route health

`gregale routes health review-release --release-report <PATH> [--fail-on-incomplete]`

| Flag | Meaning | |
|---|---|---|
| `--release-report <PATH>` | aggregate JSON report from gregale preview review | required |
| `--fail-on-incomplete` | exit nonzero unless every app has complete, healthy affected-route coverage |  |

Examples:

```sh
gregale routes health review-release --release-report release-review.json --fail-on-incomplete --json
```

Checks every app&#39;s source-affected routes.

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

Check saved requirements against a deployment and app policy

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

Plan throttle and budget patches with route coverage

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

Uses concrete or captured-family coverage.

### routes apply

Apply a reviewed server plan atomically and recover its receipt

`gregale routes apply --plan <PATH> --confirm [--idempotency-key <KEY>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | reviewed version 2 or 3 server plan JSON file | required |
| `--confirm` | confirm application of every reviewed change | required |
| `--idempotency-key <KEY>` | stable retry key, defaults to plan SHA-256 |  |

Examples:

```sh
gregale routes apply my-api --plan route-plan.json --confirm
```

### routes impact

Explain route impact between Git revisions

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

Supports FastAPI, Go HTTP, Express and Hono.

### routes contract

Check source routes against a local OpenAPI contract

Routes are discovered statically.

#### routes contract check

Find source-only and contract-only routes

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

Path parameters are matched conservatively.


## env

Clone environments, sync .env files and roll back config changes

See also: [`secrets`](#secrets).

`gregale env [<subcommand>] [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked context) |  |

### env create

Clone a project environment; managed data is isolated by default

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

Read clone progress before or after the target exists

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

A timeout exits 3 with a resume command; failed or compensated operations exit 1.

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

Push KEY=VALUE pairs to sealed secrets

`gregale env push [--wait-for-ack] [--timeout <DURATION>] [--dry-run] [--app <slug>] [--scope <SCOPE>] [-f <PATH>] [--from-stdin] [--restart] [--secret-scan <MODE>] [--no-guard]`

| Flag | Meaning | |
|---|---|---|
| `--wait-for-ack` | wait for application acknowledgements for uploaded keys |  |
| `--timeout <DURATION>` | restart and acknowledgement wait limit after uploading (default 2m; requires --wait-for-ack) |  |
| `--dry-run` | preview key changes, scan findings, and quota without uploading or restarting |  |
| `--app <slug>` | app slug (defaults to linked context) |  |
| `--scope <SCOPE>` | env scope (defaults to linked project environment) |  |
| `-f <PATH>` | input file (default .env) |  |
| `--from-stdin` | read KEY=VALUE pairs from stdin |  |
| `--restart` | restart app after applying changes (otherwise changes apply on next cold wake) |  |
| `--secret-scan <MODE>` | scan pairs before pushing | one of `on` · `off` · `strict` · `source-tree` |
| `--no-guard` | do not open a config guard for these changes |  |

Examples:

```sh
gregale env push --app my-api --dry-run
printf 'LOG_LEVEL=info\n' | gregale env push --app my-api --from-stdin
gregale env push --app my-api --restart
```

Use --restart to apply them now.

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

### env history

List env and secret revisions and the config guard&#39;s verdicts

`gregale env history [--app <slug>] [--scope <SCOPE>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked context) |  |
| `--scope <SCOPE>` | env scope (defaults to linked project environment) |  |
| `--limit <N>` | revisions to show (1-100, default 20) |  |

Examples:

```sh
gregale env history --app my-api
gregale env history --app my-api --scope staging --json
```

### env rollback

Restore a scope&#39;s variables to a config revision

`gregale env rollback [--app <slug>] [--scope <SCOPE>] [--yes] [--no-guard] <revision>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked context) |  |
| `--scope <SCOPE>` | env scope (defaults to linked project environment) |  |
| `--yes` | skip the typed confirmation |  |
| `--no-guard` | do not open a config guard for the rollback |  |

Examples:

```sh
gregale env rollback 12 --app my-api
gregale env rollback 12 --app my-api --yes
```

Applied as one guarded change; differing secrets are listed, not restored.


## init

Scaffold a project from a built-in template

`gregale init --template <NAME> --path <DIR> [--deploy] [--name <SLUG>] [--secrets-file <PATH>] [--list]`

| Flag | Meaning | |
|---|---|---|
| `--template <NAME>` | template name | required; one of `hello-node` · `hello-python` · `hello-go` · `cron-example` · `function-node` · `function-python` · `function-go` · `function-node24` · `function-python313` · `function-rust` · `event-worker` · `queue-worker` · `s3-uploader` · `object-storage-node` · `object-storage-python` · `slack-bot` · `rest-api-postgres` · `rest-api-postgres-python` · `cron-worker` · `webhook-receiver` · `ai-chat` · `secret-reload-node` · `customer-platform` · `mcp-node` · `mcp-go` · `mcp-python` · `customer-operation-export` · `customer-operation-job-export` · `customer-operation-workflow-export` · `data-api` · `data-api-starter` · `data-api-teams` · `data-api-support-console` |
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

Explain an app&#39;s health from runtime, release, data and scaling

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

Call an app or function once to smoke-test it

`gregale invoke [<slug>] [--async] [--payload <J|@file|->] [--method <METHOD>] [--path <PATH>] [--on-success-webhook <ID>] [--on-failure-webhook <ID>] [--work-policy <NAME>] [--work-key <JSON>] [--work-fairness-key <JSON>]`

| Flag | Meaning | |
|---|---|---|
| `--async` | return immediately with status_url |  |
| `--payload <J|@file|->` | JSON payload (inline \| @file \| -) |  |
| `--method <METHOD>` | HTTP method (default POST, or the function handler&#39;s) |  |
| `--path <PATH>` | URL path (default /, or the function handler&#39;s) |  |
| `--on-success-webhook <ID>` | app webhook id for completed invocation callbacks |  |
| `--on-failure-webhook <ID>` | app webhook id for failed or dead-lettered callbacks |  |
| `--work-policy <NAME>` | named app work policy for async invocation |  |
| `--work-key <JSON>` | JSON scalar application key for async invocation |  |
| `--work-fairness-key <JSON>` | JSON scalar fairness group for async invocation |  |


## run

Run code, or one command from an image, in a disposable sandbox

See also: [`runs`](#runs).

`gregale run [--image <REF>] [--interactive] [--tty] [--entrypoint <CMD|FILE>] [--env <KEY[=VALUE]>] [--env-file <FILE>] [--user <USER>] [--workdir <DIR>] [--registry-username <USER>] [--registry-password-stdin] [--docker-config <PATH>] [--ram <MB>] [--timeout <D>] [--pull <P>] [--detach] [--workflow-id <ID>] [--step-label <LABEL>] [--profile <P>] [--runtime <R>] [--source <CODE>] [--file <PATH>] [--input <J|@file|->] [--timeout-ms <N>] [--memory-mb <N>] [--cpu-millicores <N>] [--ephemeral-disk-mb <N>] [--max-output-bytes <N>] [--output-file <PATH>] [--output-dir <DIR>] [--wait] [--watch] [--poll-interval <D>] [--wait-timeout <D>]`

| Flag | Meaning | |
|---|---|---|
| `--image <REF>` | run one command from this OCI image in a fresh microVM; arguments after the flags replace the image CMD; the image can also be the first positional argument |  |
| `--interactive` | with --image, attach stdin and relay the session until the command exits (-i) |  |
| `--tty` | with --image --interactive, give the command a terminal (-t; -it for both) |  |
| `--entrypoint <CMD|FILE>` | with --image, override the image ENTRYPOINT; with --dir, the bundle file to execute |  |
| `--env <KEY[=VALUE]>` | with --image, KEY=VALUE environment variable, or KEY to pass the local value (repeatable; -e) |  |
| `--env-file <FILE>` | with --image, read KEY=VALUE lines from a file, docker env-file format (repeatable) |  |
| `--user <USER>` | with --image, run as name\|uid[:group\|gid] instead of the image USER (-u) |  |
| `--workdir <DIR>` | with --image, absolute working directory instead of the image WORKDIR (-w) |  |
| `--registry-username <USER>` | with --image, username for a private registry; the password comes from --registry-password-stdin, $GREGALE_REGISTRY_PASSWORD or a prompt |  |
| `--registry-password-stdin` | with --registry-username, read the registry password or token from stdin |  |
| `--docker-config <PATH>` | with --image, use the credentials `docker login` stored for the image&#39;s registry, from this config.json or its directory, including credential helpers |  |
| `--ram <MB>` | with --image, memory in MB (default: Job default) |  |
| `--timeout <D>` | with --image, task deadline (default: the plan&#39;s Job ceiling) |  |
| `--pull <P>` | with --image, re-pull a cached tag (always) or reuse it (missing) | one of `missing` · `always` |
| `--detach` | with --image, queue the run and print its id without waiting (-d); otherwise output streams live |  |
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

Examples:

```sh
gregale run --image alpine echo hello
gregale run --image python:3.12-slim -e MODE=batch python -c 'print(42)'
gregale run -it --image alpine sh
gregale run -u nobody -w /data --env-file .env alpine env
printf '%s' "$TOKEN" | gregale run --registry-username ci --registry-password-stdin ghcr.io/acme/tool:1.4 --version
gregale run --docker-config ~/.docker ghcr.io/acme/tool:1.4 --version
```


## runs

List, inspect or cancel sandbox runs

See also: [`run`](#run).

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

### runs capabilities

Show which runtimes isolated runs can use on your plan

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

List, inspect and wait on invocations across your account

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

Group failures into issues and track ownership and resolution

`gregale issues [<subcommand>] [--app <SLUG>] [--deployment <UUID>] [--state <STATE>] [--environment <ENV>] [--cursor <CURSOR>] [--release-cursor <CURSOR>] [--activity-cursor <CURSOR>] [--team <UUID>] [--assignee <OWNER>] [--sort <ORDER>] [--tag <KEY=VALUE>] [--min-customers <N>] [--since <RFC3339>] [--until <RFC3339>] [--name <NAME>] [--expires-in <D>] [--rules-file <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug |  |
| `--deployment <UUID>` | fixed or token-bound deployment |  |
| `--state <STATE>` | filter issue state |  |
| `--environment <ENV>` | environment filter |  |
| `--cursor <CURSOR>` | issue-list or occurrence cursor |  |
| `--release-cursor <CURSOR>` | release history cursor |  |
| `--activity-cursor <CURSOR>` | activity history cursor |  |
| `--team <UUID>` | team assignment or shared inbox |  |
| `--assignee <OWNER>` | list filter me, unassigned, or account UUID; assignment owner UUID |  |
| `--sort <ORDER>` | list order: recent or impact by verified customers in 24h |  |
| `--tag <KEY=VALUE>` | allowed operational tag filter over retained occurrences in 24h |  |
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

`gregale issues get --app <SLUG> [--interactive] [<issue-id>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--interactive` | browse issues by state and order, then inspect one |  |

Examples:

```sh
gregale issues get --app my-api --interactive
gregale issues get --app <SLUG> <issue-id>
```

### issues assign

Assign an issue to an account or team

`gregale issues assign --app <SLUG> [--interactive] [--team <UUID>] [--assignee <UUID>] [<issue-id>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--interactive` | choose an open issue, assign to yourself or unassign, and confirm ownership |  |
| `--team <UUID>` | team assignment or shared inbox |  |
| `--assignee <UUID>` | owner account UUID (empty unassigns) |  |

Examples:

```sh
gregale issues assign --app my-api --interactive
```

### issues resolve

Resolve in a deployment

`gregale issues resolve --app <SLUG> [--interactive] [--deployment <UUID|vN>] [<issue-id>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--interactive` | choose an open issue and fixing release, then confirm resolution |  |
| `--deployment <UUID|vN>` | fixing deployment UUID or vN revision (required unless interactive) |  |

Examples:

```sh
gregale issues resolve --app my-api --interactive
```

### issues reopen

Reopen an issue

`gregale issues reopen --app <SLUG> [--interactive] [<issue-id>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--interactive` | choose a resolved or ignored issue, review its state, and confirm reopening |  |

Examples:

```sh
gregale issues reopen --app my-api --interactive
```

### issues ignore

Ignore until a timestamp

`gregale issues ignore --app <SLUG> [--interactive] [--until <RFC3339>] [<issue-id>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--interactive` | choose an open issue and duration, then confirm expiry |  |
| `--until <RFC3339>` | ignore until RFC3339 (required unless interactive) |  |

Examples:

```sh
gregale issues ignore --app my-api --interactive
```

### issues merge

Merge an issue into another issue group

`gregale issues merge --app <SLUG> --target <UUID> <issue-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--target <UUID>` | destination issue UUID | required |

### issues split

Split an original group back out of an issue

`gregale issues split --app <SLUG> --member <UUID> <issue-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--member <UUID>` | original group UUID to split out | required |

### issues impact-alert

Read or configure customer-impact alert threshold

`gregale issues impact-alert --app <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |

### issues compound-alerts

Manage and preview shared-window event and customer conditions

`gregale issues compound-alerts --app <SLUG> [--key <KEY>] [--environment <NAME>] [--minimum-events <N>] [--minimum-customers <N>] [--window-seconds <N>] [--cooldown-seconds <N>] [--enabled[=true|false]] [--expected-revision <N>] [--issue <UUID>] <get|save|remove|preview>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` |  | required |
| `--key <KEY>` | stable rule key |  |
| `--environment <NAME>` | exact environment; empty matches all |  |
| `--minimum-events <N>` | event threshold in the shared window |  |
| `--minimum-customers <N>` | verified distinct customer threshold in the shared window |  |
| `--window-seconds <N>` |  |  |
| `--cooldown-seconds <N>` |  |  |
| `--enabled[=true|false]` | use --enabled=false to disable a saved rule | one of `true` · `false` |
| `--expected-revision <N>` | required for save/remove |  |
| `--issue <UUID>` | existing issue for an unsaved preview |  |

### issues tickets

Create tracker tickets or preview and apply lifecycle sync

`gregale issues tickets --app <SLUG> --issue <UUID> [--provider <PROVIDER>] [--target <TARGET>] [--title <TEXT>] [--request-id <UUID>] [--description <TEXT>] [--token-env <NAME>] [--journal-dir <PATH>] [--recover-url <URL>] [--recover] [--link <UUID>] [--direction <push|pull|outbound|inbound|both>] [--apply] [--deployment <UUID>] [--linear-state <UUID>] [--webhook-secret-env <NAME>] [--linear-open-state <UUID>] [--linear-resolved-state <UUID>] [--native] [--enable] [--disable] [--connect] [--disconnect] [--team <UUID>] <create|sync|auto-sync|linear-connection>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` |  | required |
| `--issue <UUID>` |  | required |
| `--provider <PROVIDER>` |  |  |
| `--target <TARGET>` |  |  |
| `--title <TEXT>` |  |  |
| `--request-id <UUID>` |  |  |
| `--description <TEXT>` |  |  |
| `--token-env <NAME>` |  |  |
| `--journal-dir <PATH>` |  |  |
| `--recover-url <URL>` |  |  |
| `--recover` |  |  |
| `--link <UUID>` |  |  |
| `--direction <push|pull|outbound|inbound|both>` |  |  |
| `--apply` |  |  |
| `--deployment <UUID>` |  |  |
| `--linear-state <UUID>` |  |  |
| `--webhook-secret-env <NAME>` |  |  |
| `--linear-open-state <UUID>` |  |  |
| `--linear-resolved-state <UUID>` |  |  |
| `--native` |  |  |
| `--enable` |  |  |
| `--disable` |  |  |
| `--connect` |  |  |
| `--disconnect` |  |  |
| `--team <UUID>` |  |  |

### issues links

Manage GitHub and Linear tracker references

`gregale issues links --app <SLUG> --issue <UUID> [--url <URL>] [--title <TEXT>] [--link <UUID>] [--cursor <CURSOR>] <list|save|delete>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` |  | required |
| `--issue <UUID>` |  | required |
| `--url <URL>` |  |  |
| `--title <TEXT>` |  |  |
| `--link <UUID>` |  |  |
| `--cursor <CURSOR>` |  |  |

### issues teams

Manage team members and shared inboxes

`gregale issues teams --app <SLUG> [--key <KEY>] [--name <NAME>] [--members <UUID,UUID>] [--cursor <CURSOR>] <list|get|save|delete|inbox>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` |  | required |
| `--key <KEY>` |  |  |
| `--name <NAME>` |  |  |
| `--members <UUID,UUID>` |  |  |
| `--cursor <CURSOR>` |  |  |

### issues codeowners

Preview or replace ownership rules from a bounded CODEOWNERS subset

`gregale issues codeowners --file <PATH> --bindings-file <PATH> --source-root <PATH> [--app <SLUG>] [--replace] <preview|apply>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` |  | required |
| `--bindings-file <PATH>` |  | required |
| `--source-root <PATH>` |  | required |
| `--app <SLUG>` |  |  |
| `--replace` | explicitly replace the existing policy |  |

### issues views

Manage and apply shared saved issue inbox filters

`gregale issues views --app <SLUG> [--key <KEY>] [--name <NAME>] [--state <STATE>] [--environment <NAME>] [--team <UUID>] [--assignee <OWNER>] [--sort <ORDER>] [--min-customers <N>] [--tag <KEY=VALUE>] [--cursor <CURSOR>] <list|get|save|delete|apply>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` |  | required |
| `--key <KEY>` |  |  |
| `--name <NAME>` |  |  |
| `--state <STATE>` |  |  |
| `--environment <NAME>` |  |  |
| `--team <UUID>` | team assignment or shared inbox |  |
| `--assignee <OWNER>` |  |  |
| `--sort <ORDER>` |  |  |
| `--min-customers <N>` |  |  |
| `--tag <KEY=VALUE>` |  |  |
| `--cursor <CURSOR>` |  |  |

### issues compare-releases

Compare observed request health and Issues across deployments

`gregale issues compare-releases --app <SLUG> --baseline <UUID> --candidate <UUID> [--environment <NAME>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` |  | required |
| `--baseline <UUID>` |  | required |
| `--candidate <UUID>` |  | required |
| `--environment <NAME>` |  |  |

### issues bulk

Atomically triage up to 50 selected issue groups

`gregale issues bulk --app <SLUG> --issues <UUID,UUID> --action <ACTION> [--team <UUID>] [--assignee <UUID>] [--deployment <UUID>] [--until <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` |  | required |
| `--issues <UUID,UUID>` |  | required |
| `--action <ACTION>` |  | required |
| `--team <UUID>` | team assignment or shared inbox |  |
| `--assignee <UUID>` |  |  |
| `--deployment <UUID>` |  |  |
| `--until <RFC3339>` |  |  |

### issues source-maps

Publish, inspect or retire deployment mapping artifacts

`gregale issues source-maps --app <SLUG> --deployment <UUID> [--directory <PATH>] [--manifest-file <PATH>] <publish|status|retire>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` |  | required |
| `--deployment <UUID>` |  | required |
| `--directory <PATH>` | deployed directory corresponding to the build scan root |  |
| `--manifest-file <PATH>` | portable source-map manifest file |  |

### issues lifecycle-alerts

Read, save or preview new-issue/regression alert filters

`gregale issues lifecycle-alerts --app <SLUG> [--environments <ENVS>] [--cooldown-seconds <N>] [--expected-revision <N>] [--issue <UUID>] [--event <EVENT>] <get|save|preview>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` |  | required |
| `--environments <ENVS>` | comma-separated exact names; empty means all |  |
| `--cooldown-seconds <N>` | shared cooldown; zero disables throttling |  |
| `--expected-revision <N>` | required when saving |  |
| `--issue <UUID>` | existing issue for draft preview |  |
| `--event <EVENT>` | created or regressed for preview |  |

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

### customer-operations outcomes

List completed workflows with explicit business outcomes

`gregale customer-operations outcomes [--app <SLUG>] [--self] [--app-id <UUID>] --scope <SCOPE> [--tenant <UUID>] [--workflow <NAME>] [--code <CODE>] [--limit <N>] [--cursor <CURSOR>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owned app |  |
| `--self` | authenticated customer |  |
| `--app-id <UUID>` | required with --self |  |
| `--scope <SCOPE>` | explicit environment | required |
| `--tenant <UUID>` | account customer filter |  |
| `--workflow <NAME>` | workflow filter |  |
| `--code <CODE>` | outcome code filter |  |
| `--limit <N>` | page size 1–100 |  |
| `--cursor <CURSOR>` | continuation cursor |  |

### customer-operations outcome-summary

Summarize explicit business outcomes

`gregale customer-operations outcome-summary [--group-by <DIMENSION>] [--app <SLUG>] [--self] [--app-id <UUID>] --scope <SCOPE> [--tenant <UUID>] [--workflow <NAME>] [--code <CODE>] [--limit <N>] [--cursor <CURSOR>]`

| Flag | Meaning | |
|---|---|---|
| `--group-by <DIMENSION>` | summary grouping | one of `outcome` · `workflow` · `customer` |
| `--app <SLUG>` | owned app |  |
| `--self` | authenticated customer |  |
| `--app-id <UUID>` | required with --self |  |
| `--scope <SCOPE>` | explicit environment | required |
| `--tenant <UUID>` | account customer filter |  |
| `--workflow <NAME>` | workflow filter |  |
| `--code <CODE>` | outcome code filter |  |
| `--limit <N>` | page size 1–100 |  |
| `--cursor <CURSOR>` | continuation cursor |  |

### customer-operations attention

List workflows needing attention

`gregale customer-operations attention [--app <SLUG>] [--self] [--app-id <UUID>] --scope <SCOPE> [--tenant <UUID>] [--workflow <NAME>] [--target-operation <NAME>] [--dependency-status <STATUS>] [--required-outcome-code <CODE>] [--priority <PRIORITY>] [--sort <ORDER>] [--owner <OWNER>] [--unassigned] [--blocker-code <CODE>] [--reason <REASON>] [--limit <N>] [--cursor <CURSOR>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owned app in account mode |  |
| `--self` | authenticated customer queue |  |
| `--app-id <UUID>` | required with --self |  |
| `--scope <SCOPE>` | explicit environment | required |
| `--tenant <UUID>` | account customer filter |  |
| `--workflow <NAME>` | workflow filter |  |
| `--target-operation <NAME>` | target Operation filter |  |
| `--dependency-status <STATUS>` | unresolved prerequisite status | one of `waiting` · `unknown` · `outcome_mismatch` |
| `--required-outcome-code <CODE>` | required prerequisite outcome |  |
| `--priority <PRIORITY>` | application-assigned blocker urgency | one of `low` · `normal` · `high` · `urgent` |
| `--sort <ORDER>` | queue order; summaries retain group ordering | one of `updated_at` · `deadline` |
| `--owner <OWNER>` | exact application-assigned blocker owner; excludes --unassigned |  |
| `--unassigned` | only matching blockers without an owner; excludes --owner |  |
| `--blocker-code <CODE>` | blocker code filter |  |
| `--reason <REASON>` | attention reason | one of `blocked` · `stale` · `overdue` · `dependency` · `escalated` · `unacknowledged` · `follow_up_overdue` · `awaiting_verification` · `sla_breached` · `sla_at_risk` |
| `--limit <N>` | page size, 1–100 |  |
| `--cursor <CURSOR>` | continuation cursor |  |

### customer-operations attention-summary

Summarize workflows needing attention

`gregale customer-operations attention-summary [--group-by <DIMENSION>] [--app <SLUG>] [--self] [--app-id <UUID>] --scope <SCOPE> [--tenant <UUID>] [--workflow <NAME>] [--target-operation <NAME>] [--dependency-status <STATUS>] [--required-outcome-code <CODE>] [--priority <PRIORITY>] [--sort <ORDER>] [--owner <OWNER>] [--unassigned] [--blocker-code <CODE>] [--reason <REASON>] [--limit <N>] [--cursor <CURSOR>]`

| Flag | Meaning | |
|---|---|---|
| `--group-by <DIMENSION>` | summary grouping | one of `owner` · `workflow` · `blocker_code` · `target_operation` · `customer` · `dependency_status` · `required_outcome_code` |
| `--app <SLUG>` | owned app in account mode |  |
| `--self` | authenticated customer queue |  |
| `--app-id <UUID>` | required with --self |  |
| `--scope <SCOPE>` | explicit environment | required |
| `--tenant <UUID>` | account customer filter |  |
| `--workflow <NAME>` | workflow filter |  |
| `--target-operation <NAME>` | target Operation filter |  |
| `--dependency-status <STATUS>` | unresolved prerequisite status | one of `waiting` · `unknown` · `outcome_mismatch` |
| `--required-outcome-code <CODE>` | required prerequisite outcome |  |
| `--priority <PRIORITY>` | application-assigned blocker urgency | one of `low` · `normal` · `high` · `urgent` |
| `--sort <ORDER>` | queue order; summaries retain group ordering | one of `updated_at` · `deadline` |
| `--owner <OWNER>` | exact application-assigned blocker owner; excludes --unassigned |  |
| `--unassigned` | only matching blockers without an owner; excludes --owner |  |
| `--blocker-code <CODE>` | blocker code filter |  |
| `--reason <REASON>` | attention reason | one of `blocked` · `stale` · `overdue` · `dependency` · `escalated` · `unacknowledged` · `follow_up_overdue` · `awaiting_verification` · `sla_breached` · `sla_at_risk` |
| `--limit <N>` | page size, 1–100 |  |
| `--cursor <CURSOR>` | continuation cursor |  |

### customer-operations bindings

Generate typed application workflow bindings without credentials

`gregale customer-operations bindings --app <SLUG> --plan <PLAN> [--dir <PATH>] --language <LANGUAGE> --output <PATH> [--package <NAME>] [--check]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | selected manifest app | required |
| `--plan <PLAN>` | explicit target plan | required; one of `free` · `hobby` · `pro` · `scale` |
| `--dir <PATH>` | source directory, default current directory |  |
| `--language <LANGUAGE>` | application language | required; one of `typescript` · `javascript` · `go` · `python` |
| `--output <PATH>` | generated module | required |
| `--package <NAME>` | Go package name, default workflowbindings |  |
| `--check` | fail on stale bindings without writing |  |

### customer-operations doctor

Observe submission blockers, delivery warnings and qualification gaps

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

Validate source contracts and sample input without credentials

`gregale customer-operations validate --app <SLUG> --plan <PLAN> [--dir <PATH>] [--name <NAME>] [--input-file <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | selected manifest app | required |
| `--plan <PLAN>` | explicit target plan | required; one of `free` · `hobby` · `pro` · `scale` |
| `--dir <PATH>` | source directory, default current directory |  |
| `--name <NAME>` | selected operation; required for a sample |  |
| `--input-file <PATH>` | sample JSON input |  |

Sample input is optional.

### customer-operations types

Generate or check TypeScript input and output types

`gregale customer-operations types --app <SLUG> --plan <PLAN> [--dir <PATH>] [--name <NAME>] [--output <PATH>] [--check]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | selected manifest app | required |
| `--plan <PLAN>` | explicit target plan | required; one of `free` · `hobby` · `pro` · `scale` |
| `--dir <PATH>` | source directory, default current directory |  |
| `--name <NAME>` | selected operation; defaults to all |  |
| `--output <PATH>` | generated declaration path (default customer-operations.generated.d.ts) |  |
| `--check` | verify generated declarations are current without writing |  |

### customer-operations start

Submit tenant-owned work with an immutable retry receipt

`gregale customer-operations start --self [--definition <UUID>] [--input-file <PATH>] [--idempotency-key <KEY>] --receipt-file <PATH> [--timeout <D>]`

| Flag | Meaning | |
|---|---|---|
| `--self` | derive tenant from credentials | required |
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
| `--state <STATE>` | business state | one of `accepted` · `running` · `waiting` · `succeeded` · `failed` · `cancelled` · `requires_reconciliation` |
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

### customer-operations inspect

Inspect steps, uncertain attempts, retained files and retry blockers

`gregale customer-operations inspect [--app <SLUG>] [--timeout <D>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |

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

### customer-operations timeline

Read one chronological history of events, attempts and deliveries

`gregale customer-operations timeline --app <SLUG> [--timeout <D>] [--limit <N>] [--cursor <CURSOR>] [--all] [--logs] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owned application; account mode only | required |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--limit <N>` | page size, 1–100 |  |
| `--cursor <CURSOR>` | next timeline page cursor |  |
| `--all` | follow cursors to the end |  |
| `--logs` | print archived instance log lines inside each HTTP attempt |  |

### customer-operations milestones

Read business milestones for an operation or reference

`gregale customer-operations milestones [--app <SLUG>] [--self] [--scope <SCOPE>] [--subject-type <TYPE>] [--subject-id <ID>] [--workflow <NAME>] [--workflow-instance-id <ID>] [--workflow-state-cursor <CURSOR>] [--stale-only] [--tenant <UUID>] [--limit <N>] [--cursor <CURSOR>] [<operation-id>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owned app slug, or app UUID with --self |  |
| `--self` | derive customer ownership from credentials |  |
| `--scope <SCOPE>` | environment for a business-reference timeline |  |
| `--subject-type <TYPE>` | business reference type |  |
| `--subject-id <ID>` | exact business reference ID |  |
| `--workflow <NAME>` | workflow for one business workflow run |  |
| `--workflow-instance-id <ID>` | exact workflow run ID |  |
| `--workflow-state-cursor <CURSOR>` | next workflow state history page |  |
| `--stale-only` | only runs past their declared state age threshold |  |
| `--tenant <UUID>` | account customer filter |  |
| `--limit <N>` | page size |  |
| `--cursor <CURSOR>` | next milestone page cursor |  |

### customer-operations watch

Watch an operation&#39;s status changes

`gregale customer-operations watch [--app <SLUG>] [--timeout <D>] [--interval <D>] [--self] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--interval <D>` | polling interval, default 1s |  |
| `--self` | use authenticated tenant routes; omit --app |  |

Exits 0 on success, 1 on failure or cancel, 4 on reconciliation, 124 on timeout and 130 when interrupted.

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

### customer-operations signal

Deliver a named event to a waiting operation

`gregale customer-operations signal [--app <SLUG>] [--timeout <D>] --event <NAME> --signal-id <ID> [--payload <JSON>] [--payload-file <PATH>] [--scope <SCOPE>] [--operation <NAME>] [--subject <TYPE:ID>] [--self] [--definition <UUID>] [<id>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owned application; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--event <NAME>` | event the operation waits for | required |
| `--signal-id <ID>` | stable idempotency key; repeat it to retry safely | required |
| `--payload <JSON>` | JSON payload |  |
| `--payload-file <PATH>` | file holding the JSON payload |  |
| `--scope <SCOPE>` | environment, for a subject signal |  |
| `--operation <NAME>` | operation name, for a subject signal |  |
| `--subject <TYPE:ID>` | business subject, instead of an operation ID |  |
| `--self` | signal the authenticated tenant&#39;s own operations |  |
| `--definition <UUID>` | operation definition, for a --self subject signal |  |

Address the operation by ID or by business subject.

### customer-operations approve

Decide a declared approval on a waiting operation

`gregale customer-operations approve [--app <SLUG>] [--timeout <D>] --expected-generation <N> --step <STEP> --decision <DECISION> [--comment <TEXT>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--expected-generation <N>` | observed generation; stale decisions are rejected | required |
| `--step <STEP>` | declared approval step | required |
| `--decision <DECISION>` | approval decision | required; one of `approved` · `rejected` |
| `--comment <TEXT>` | optional note recorded with the decision |  |

### customer-operations recover

Preview or record an evidenced reconciliation decision

`gregale customer-operations recover [--app <SLUG>] [--timeout <D>] [--expected-generation <N>] [--receipt-file <PATH>] [--preview] [--inspection-revision <REVISION>] [--recovery-id <ID>] [--resolution <RESOLUTION>] [--evidence-file <PATH>] [--result-file <PATH>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--expected-generation <N>` | observed generation for a new decision or preview; omit to resume |  |
| `--receipt-file <PATH>` | private immutable decision request; omit selectors to resume |  |
| `--preview` | read-only plan; exit 4 when blocked; omit decision ID and evidence |  |
| `--inspection-revision <REVISION>` | optional inspection revision checked when applying |  |
| `--recovery-id <ID>` | stable decision ID required when applying |  |
| `--resolution <RESOLUTION>` | explicit resolution for a new decision or preview; omit to resume | one of `succeeded` · `failed` · `cancelled` · `safe_to_retry` |
| `--evidence-file <PATH>` | nonempty reconciliation evidence required when applying |  |
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

Record a notification retry without repeating business work

`gregale customer-operations retry-delivery [--app <SLUG>] [--timeout <D>] [--delivery <UUID>] [--expected-replay-generation <N>] [--retry-id <ID>] --receipt-file <PATH> <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | required in account mode; omit with --self |  |
| `--timeout <D>` | local request/wait deadline; work continues |  |
| `--delivery <UUID>` | observed dead delivery; omit selectors to resume |  |
| `--expected-replay-generation <N>` | observed notification generation; zero must be explicit |  |
| `--retry-id <ID>` | stable retry decision identity |  |
| `--receipt-file <PATH>` | private immutable request receipt | required |

The retry is backed by a receipt.


## operations

Coordinate named work so one worker owns it at a time

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

Route an account trigger or Job schedule through a policy

`gregale operations bind-trigger --policy <NAME> --key <JSON> [--tenant <ID>] [--equivalence-key <KEY>] <cron|inbound_webhook|broker|job_schedule> <trigger-id>`

| Flag | Meaning | |
|---|---|---|
| `--policy <NAME>` | managed operation policy | required |
| `--key <JSON>` | JSON scalar business coordination key | required |
| `--tenant <ID>` | account-authorized platform customer tenant |  |
| `--equivalence-key <KEY>` | equivalent request identity for join_existing |  |

Accepts an account-owned cron, inbound webhook, broker trigger or Job schedule.

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

Inspect one retained request, or the latest one

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

### debug profiles

Sampled CPU or heap functions and deployment comparison

`gregale debug profiles [--type <KIND>] [--deployment-id <UUID>] [--runtime <NAME>] [--start <RFC3339>] [--end <RFC3339>] [--baseline-id <UUID>] [--baseline-start <RFC3339>] [--baseline-end <RFC3339>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--type <KIND>` | cpu (default) or heap |  |
| `--deployment-id <UUID>` | candidate deployment |  |
| `--runtime <NAME>` | runtime name |  |
| `--start <RFC3339>` | capture start |  |
| `--end <RFC3339>` | capture end |  |
| `--baseline-id <UUID>` | baseline deployment |  |
| `--baseline-start <RFC3339>` | baseline capture start |  |
| `--baseline-end <RFC3339>` | baseline capture end |  |

### debug capture

Capture CPU, heap, stack or lock profiles from a running instance

`gregale debug capture [--type <KINDS>] [--duration <DURATION>] [--instance <UUID>] [--output <DIR>] [--top <N>] [--no-wait] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--type <KINDS>` | cpu, heap, stacks, block, mutex, comma-separated (default cpu) |  |
| `--duration <DURATION>` | capture window, 1s..60s (default 10s) |  |
| `--instance <UUID>` | running instance; default the earliest |  |
| `--output <DIR>` | write merged .pb.gz pprof files |  |
| `--top <N>` | functions to print per kind |  |
| `--no-wait` | queue the capture and print its id |  |

### debug captures

List or show on-demand profile captures

`gregale debug captures [--output <DIR>] [--top <N>] <slug> [<capture-id>]`

| Flag | Meaning | |
|---|---|---|
| `--output <DIR>` | write merged .pb.gz pprof files |  |
| `--top <N>` | functions to print per kind |  |

### debug capture-diff

Compare a profile capture with an earlier one to see what grew

`gregale debug capture-diff [--base <ID>] [--type <KIND>] [--top <N>] <slug> <capture-id>`

| Flag | Meaning | |
|---|---|---|
| `--base <ID>` | baseline capture id of the same app |  |
| `--type <KIND>` | cpu, heap, stacks, block or mutex (default: shared kind, heap first) |  |
| `--top <N>` | functions to print |  |

### debug dependencies

Show observed dependency latency and regressions

`gregale debug dependencies [--since <DURATION>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | lookback window |  |

### debug coverage

Show observed debugger signal coverage

`gregale debug coverage [--since <DURATION>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | lookback window |  |

Usage: coverage &lt;slug&gt; [--since D].

### debug running

Explain why an app is still running

`gregale debug running [--since <DURATION>] [--limit <N>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | lookback window |  |
| `--limit <N>` | maximum recent observations (1..100; default 20) |  |

Includes request evidence when available. Usage: running &lt;slug&gt; [--since D] [--limit N].

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

Look up a distributed trace by its trace ID

`gregale trace <trace-id> [--watch] [--interval <DURATION>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--watch` | poll until linked invocations reach a terminal state |  |
| `--interval <DURATION>` | poll interval (default 1s) |  |
| `--timeout <DURATION>` | maximum watch duration (default 5m) |  |


## invitations

View or accept an organization invitation

`gregale invitations [<subcommand>] <token>`

### invitations peek

Look up an invitation by token

`gregale invitations peek <token>`

### invitations accept

Accept an invitation

`gregale invitations accept <token>`


## invoices

List issued invoices

See also: [`plan`](#plan), [`billing`](#billing), [`usage`](#usage), [`overage-cap`](#overage-cap).

`gregale invoices`


## keys

Manage your account&#39;s API keys

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

`gregale link [<project-slug>] [--interactive] [--app <slug>] [--environment <environment>] [--no-gitignore]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose project, app, and environment, review defaults, and confirm saving |  |
| `--app <slug>` | workload/app slug for app-scoped commands |  |
| `--environment <environment>` | default project environment scope |  |
| `--no-gitignore` | do not add .gregale/ to .gitignore |  |

Examples:

```sh
gregale link --interactive
gregale link my-project --interactive
gregale link my-project --app my-api --environment staging
```


## logout

Revoke the managed CLI session and remove the stored token

`gregale logout`


## unlink

Remove the linked project from this checkout

`gregale unlink`


## profile

Manage named connections to Gregale and their credentials

`gregale profile [<subcommand>]`

Examples:

```sh
gregale profile
gregale profile list
gregale profile --help
```

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

Select the default connection or choose and check one interactively

`gregale profile use [--interactive] [--timeout <DURATION>] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a saved connection, check its identity, and confirm switching |  |
| `--timeout <DURATION>` | interactive connection check deadline (default 10s) |  |

Examples:

```sh
gregale profile use staging
gregale profile use --interactive
```

### profile remove

Remove an inactive connection and its credentials

`gregale profile remove <name>`


## context

Show linked targets, configuration sources, and missing defaults

`gregale context [<subcommand>] [--local-config] [--problems-only] [--check] [--verify] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--local-config` | offline project-root summary of preferences, required names, presets, and recovery records; invalid files fail |  |
| `--problems-only` | show only invalid local files, broken references, and recovery issues; requires --local-config |  |
| `--check` | fail when a linked project or default app is missing; does not verify remote resources |  |
| `--verify` | read authenticated account and verify configured linked project/app/environment |  |
| `--timeout <DURATION>` | overall verification deadline (default 10s; requires --verify) |  |

Examples:

```sh
gregale context
gregale context --check --json
gregale context --local-config
```

### context save

Save current linked targets as a named local preset

`gregale context save [--replace] <name>`

| Flag | Meaning | |
|---|---|---|
| `--replace` | explicitly replace an existing preset |  |

### context use

Validate and apply a preset for the currently linked project

`gregale context use [--dry-run] <name>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | validate and preview without saving linked defaults |  |

### context presets

List or manage local target presets

Examples:

```sh
gregale context presets
gregale context presets list
gregale context presets --help
```

#### context presets copy

Copy a local preset without changing the active context

`gregale context presets copy <source-name> <new-name>`

Examples:

```sh
gregale context presets copy staging staging-eu
```

#### context presets rename

Rename a local preset while preserving its targets and active context

`gregale context presets rename <old-name> <new-name>`

Examples:

```sh
gregale context presets rename staging staging-eu
```

#### context presets update

Change selected preset targets without changing active context

`gregale context presets update [--app <SLUG>] [--environment <SLUG>] [--clear-app] [--clear-environment] [--dry-run] <name>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | validate and replace only the preset app |  |
| `--environment <SLUG>` | validate and replace only the preset environment |  |
| `--clear-app` | remove only the preset app |  |
| `--clear-environment` | remove only the preset environment |  |
| `--dry-run` | validate and preview without saving the preset |  |

#### context presets list

List preset targets and their source file

`gregale context presets list`

#### context presets show

Show one saved target preset

`gregale context presets show <name>`

#### context presets delete

Remove a local preset without changing linked targets

`gregale context presets delete [--dry-run] <name>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview targets and source path without deleting |  |

Examples:

```sh
gregale context presets delete staging --dry-run
```

### context set

Update selected linked defaults while preserving omitted settings

`gregale context set [--dry-run] [--app <SLUG>] [--environment <SLUG>] [--clear-app] [--clear-environment]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | validate and display proposed defaults without saving |  |
| `--app <SLUG>` | validate and replace the default app in the linked project |  |
| `--environment <SLUG>` | validate and replace the default project environment |  |
| `--clear-app` | remove only the default app locally |  |
| `--clear-environment` | remove only the default environment locally |  |

Examples:

```sh
gregale context set --environment staging
gregale context set --app payments-api
gregale context set --clear-environment
```


## signup

Create a new account

`gregale signup [--email-only <EMAIL>] [--password-stdin]`

| Flag | Meaning | |
|---|---|---|
| `--email-only <EMAIL>` | send a one-time signup link to this email (no password prompt) |  |
| `--password-stdin` | read password from stdin (CI; mutually exclusive with --email-only) |  |


## logs

Search an app&#39;s runtime logs and HTTP requests, or follow them

See also: [`tail`](#tail).

`gregale logs [<slug>] [--dry-run] [--view <NAME>] [--saved-view] [--clear-status] [--clear-grep] [--clear-level] [--clear-route] [--latest-failure] [--latest-request-error] [--interactive] [--follow] [--deployment <ID>] [--release <ID|vN>] [--source <SOURCE>] [--grep <SUBSTR>] [--since <15m|3d|RFC3339>] [--level <LEVEL>] [--status <100..599>] [--route <METHOD /path>] [--request <ID>] [--trace <TRACE_ID>] [--project <SLUG>] [--service <NAME,...>] [--limit <N>] [--all] [--explain] [--archive] [--instance <ID>] [--date <DAY>]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview effective target, source, and filters offline; no authentication or remote reads |  |
| `--view <NAME>` | reuse a local log view with compatible one-off filter overrides; saved view stays unchanged |  |
| `--saved-view` | use the project-local preferred view and compatible overrides; excludes --view and other selectors |  |
| `--clear-status` | remove the saved status filter for one ordinary HTTP view query; excludes --status |  |
| `--clear-grep` | remove the saved text filter for one runtime view query; excludes --grep |  |
| `--clear-level` | remove the saved level filter for one runtime view query; excludes --level |  |
| `--clear-route` | remove the saved route filter for one HTTP view query; excludes --route |  |
| `--latest-failure` | select latest failed deployment and read up to 500 retained build/deploy entries; accepts only an app target |  |
| `--latest-request-error` | select newest retained HTTP 5xx row; default 15m window; accepts app, since, route |  |
| `--interactive` | choose source, time window, and filters and show the equivalent command |  |
| `--follow` | stream logs until interrupted |  |
| `--deployment <ID>` | deployment id or vN revision (default: latest) |  |
| `--release <ID|vN>` | release id or revision (alias for --deployment) |  |
| `--source <SOURCE>` | log source | one of `runtime` · `http` |
| `--grep <SUBSTR>` | only show lines containing this substring |  |
| `--since <15m|3d|RFC3339>` | lookback duration or RFC3339 timestamp |  |
| `--level <LEVEL>` | only show lines at this level | one of `info` · `warn` · `error` |
| `--status <100..599>` | only show HTTP requests with this status |  |
| `--route <METHOD /path>` | only show HTTP requests for this route label |  |
| `--request <ID>` | show one HTTP request by public request id or row id |  |
| `--trace <TRACE_ID>` | show HTTP access logs correlated with a W3C trace id |  |
| `--project <SLUG>` | read every service of a project together, each line prefixed with its service |  |
| `--service <NAME,...>` | with --project, only these services |  |
| `--limit <N>` | HTTP request page size (1..200) |  |
| `--all` | read every retained HTTP request page |  |
| `--explain` | summarize the last failure and common error patterns |  |
| `--archive` | read durable logs for one instance and UTC day |  |
| `--instance <ID>` | instance id for --archive (1..128 ASCII letters, numbers, dots, underscores, or hyphens) |  |
| `--date <DAY>` | UTC day for --archive: today, yesterday, or YYYY-MM-DD |  |

Examples:

```sh
gregale logs my-api --interactive
gregale logs my-api --follow
gregale logs --project shop --follow
gregale logs my-api --since 1h --level error
gregale logs views save runtime-errors --since 1h --level error
gregale logs my-api --view runtime-errors
```

### logs search

Search the app&#39;s log events by text, level, route and more

`gregale logs search [--app <SLUG>] [--q <TEXT>] [--since <15m|6h|3d|RFC3339>] [--until <RFC3339>] [--source <SOURCE>] [--level <LEVELS>] [--route <ROUTE>] [--status <502|5xx>] [--deployment <ID>] [--request <ID>] [--trace <TRACE_ID>] [--limit <N>] [--saved <NAME>] [--cursor <CURSOR>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional; defaults to linked context) |  |
| `--q <TEXT>` | case-insensitive text the message contains (2..256 characters; windows up to 24h) |  |
| `--since <15m|6h|3d|RFC3339>` | window start: lookback or RFC3339 (default 1h) |  |
| `--until <RFC3339>` | window end (default now) |  |
| `--source <SOURCE>` | ledger source | one of `runtime` · `build` · `deploy` · `http` · `network` · `dns` |
| `--level <LEVELS>` | comma list of levels (trace, debug, info, warn, error, fatal) |  |
| `--route <ROUTE>` | exact route template, e.g. /checkout/{id} |  |
| `--status <502|5xx>` | HTTP status or class |  |
| `--deployment <ID>` | deployment id |  |
| `--request <ID>` | request id or trace id |  |
| `--trace <TRACE_ID>` | W3C trace id |  |
| `--limit <N>` | events per page (1..200) |  |
| `--saved <NAME>` | apply a saved search&#39;s filters; other flags override them |  |
| `--cursor <CURSOR>` | next_cursor from the previous page |  |

Examples:

```sh
gregale logs search my-api --level error --since 6h
gregale logs search my-api --q timeout --status 5xx --since 2h --json
```

Filters combine: text, level, route, HTTP status, release, request ID and trace ID.

### logs facets

Count matching log events by source, level, status class and route

`gregale logs facets [--app <SLUG>] [--q <TEXT>] [--since <15m|6h|3d|RFC3339>] [--until <RFC3339>] [--source <SOURCE>] [--level <LEVELS>] [--route <ROUTE>] [--status <502|5xx>] [--deployment <ID>] [--request <ID>] [--trace <TRACE_ID>] [--saved <NAME>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional; defaults to linked context) |  |
| `--q <TEXT>` | case-insensitive text the message contains (2..256 characters; windows up to 24h) |  |
| `--since <15m|6h|3d|RFC3339>` | window start: lookback or RFC3339 (default 1h) |  |
| `--until <RFC3339>` | window end (default now) |  |
| `--source <SOURCE>` | ledger source | one of `runtime` · `build` · `deploy` · `http` · `network` · `dns` |
| `--level <LEVELS>` | comma list of levels (trace, debug, info, warn, error, fatal) |  |
| `--route <ROUTE>` | exact route template, e.g. /checkout/{id} |  |
| `--status <502|5xx>` | HTTP status or class |  |
| `--deployment <ID>` | deployment id |  |
| `--request <ID>` | request id or trace id |  |
| `--trace <TRACE_ID>` | W3C trace id |  |
| `--saved <NAME>` | apply a saved search&#39;s filters; other flags override them |  |

Examples:

```sh
gregale logs facets my-api --since 24h
gregale logs facets my-api --source http --status 5xx --since 7d
```

### logs saved

List, save or delete the app&#39;s server-side saved log searches

`gregale logs saved <list|save|delete> [--app <SLUG>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional; defaults to linked context) |  |

Examples:

```sh
gregale logs saved my-api
gregale logs saved save checkout-5xx my-api --route /checkout --status 5xx
gregale logs saved delete checkout-5xx my-api
```

#### logs saved list

List the app&#39;s saved searches and their filters

`gregale logs saved list [<slug>]`

Examples:

```sh
gregale logs saved list my-api
```

#### logs saved save

Create a saved search or replace its filters (windows are not stored)

`gregale logs saved save [--q <TEXT>] [--source <SOURCE>] [--level <LEVELS>] [--route <ROUTE>] [--status <502|5xx>] [--deployment <ID>] [--request <ID>] [--trace <TRACE_ID>] <name> [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--q <TEXT>` | case-insensitive text the message contains (2..256 characters) |  |
| `--source <SOURCE>` | ledger source | one of `runtime` · `build` · `deploy` · `http` · `network` · `dns` |
| `--level <LEVELS>` | comma list of levels (trace, debug, info, warn, error, fatal) |  |
| `--route <ROUTE>` | exact route template, e.g. /checkout/{id} |  |
| `--status <502|5xx>` | HTTP status or class |  |
| `--deployment <ID>` | deployment id |  |
| `--request <ID>` | request id or trace id |  |
| `--trace <TRACE_ID>` | W3C trace id |  |

Examples:

```sh
gregale logs saved save db-errors my-api --source runtime --level error --q database
```

#### logs saved delete

Delete a saved search

`gregale logs saved delete <name> [<slug>]`

Examples:

```sh
gregale logs saved delete db-errors my-api
```

### logs defaults

Show or manage the project-local preferred log view

Examples:

```sh
gregale logs defaults
gregale logs defaults --help
```

#### logs defaults set

Save one existing log view name locally

`gregale logs defaults set [--dry-run] <name>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview the preference path and selected filters without saving |  |

Examples:

```sh
gregale logs defaults set errors --dry-run
```

#### logs defaults show

Show the preferred view, filters, reference health and preview

`gregale logs defaults show [--app <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | target the preview command at an explicit, unverified app |  |

Examples:

```sh
gregale logs defaults show --app payments-api
```

#### logs defaults clear

Clear the local preference without changing saved views

`gregale logs defaults clear [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview the preference removal without deleting files |  |

Examples:

```sh
gregale logs defaults clear --dry-run
```

### logs views

List or manage reusable local log filters

`gregale logs views <copy|list|show|rename|update|delete|save> [--name <TEXT>] [--source <SOURCE>] [--details] [--app <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--name <TEXT>` | list view names containing literal, case-sensitive text |  |
| `--source <SOURCE>` | list only saved views of this log source | one of `runtime` · `http` |
| `--details` | include saved filters and query/preview commands in JSON entries |  |
| `--app <SLUG>` | target generated commands at an explicit, unverified app |  |

Examples:

```sh
gregale logs views
gregale logs views --source http
gregale logs views --name errors
gregale --json logs views --details
gregale logs views list
gregale logs views --help
```

#### logs views copy

Copy saved filters as a starting point; the source is kept

`gregale logs views copy [--dry-run] <source-name> <new-name>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview filters and destination without writing |  |

Examples:

```sh
gregale logs views copy errors checkout-errors
```

#### logs views list

List saved log views and copyable query previews

`gregale logs views list [--name <TEXT>] [--source <SOURCE>] [--details] [--app <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--name <TEXT>` | list view names containing literal, case-sensitive text |  |
| `--source <SOURCE>` | list only saved views of this log source; HTTP includes latest-error views | one of `runtime` · `http` |
| `--details` | include saved filters and query/preview commands in JSON entries |  |
| `--app <SLUG>` | target generated commands at an explicit, unverified app |  |

Examples:

```sh
gregale logs views list
gregale --json logs views list --details
```

#### logs views show

Show saved filters and copyable query previews

`gregale logs views show [--app <SLUG>] [--details] <name>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | target generated commands at an explicit, unverified app |  |
| `--details` | include saved filters and query/preview commands in JSON output |  |

Examples:

```sh
gregale logs views show errors --app my-api
gregale --json logs views show errors --app my-api --details
```

#### logs views rename

Rename a saved view without changing filters

`gregale logs views rename [--dry-run] <old-name> <new-name>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview filters, destination, and preferred-view impact without writing |  |

Never overwrites another view.

#### logs views update

Change selected filters; omitted settings are kept

`gregale logs views update [--dry-run] [--since <DURATION>] [--route <PATH>] [--grep <TEXT>] [--level <LEVEL>] [--status <N>] [--limit <N>] [--follow] [--clear-route] [--clear-grep] [--clear-level] <name>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | validate proposed filters and show their run command without saving |  |
| `--since <DURATION>` | replace relative window |  |
| `--route <PATH>` | replace HTTP route |  |
| `--grep <TEXT>` | replace runtime text filter |  |
| `--level <LEVEL>` | replace runtime level |  |
| `--status <N>` | replace HTTP status; 0 clears it |  |
| `--limit <N>` | replace normal HTTP page size |  |
| `--follow` | replace runtime follow mode; false disables it |  |
| `--clear-route` | remove route filter |  |
| `--clear-grep` | remove text filter |  |
| `--clear-level` | remove level filter |  |

The view type is preserved.

#### logs views delete

Delete a local log view

`gregale logs views delete [--dry-run] <name>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview source, filters, and storage path without deleting |  |

Examples:

```sh
gregale logs views delete checkout-errors --dry-run
```

#### logs views save

Save filters as a named view

`gregale logs views save [--dry-run] [--latest-request-error] [--source <SOURCE>] [--since <DURATION>] [--level <LEVEL>] [--grep <TEXT>] [--follow] [--status <100..599>] [--route <PATH>] [--limit <1..200>] [--replace] <name>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview proposed filters, replacement, and commands without saving |  |
| `--latest-request-error` | save newest HTTP 5xx selection with a relative window and optional route |  |
| `--source <SOURCE>` | runtime (default) or http | one of `runtime` · `http` |
| `--since <DURATION>` | relative window (default 15m) |  |
| `--level <LEVEL>` | runtime log level |  |
| `--grep <TEXT>` | runtime text filter |  |
| `--follow` | follow runtime output |  |
| `--status <100..599>` | exact HTTP status |  |
| `--route <PATH>` | HTTP route filter |  |
| `--limit <1..200>` | HTTP page size (default 100) |  |
| `--replace` | replace an existing view |  |


## metrics

Show app or account metrics, or the service map

`gregale metrics [<slug>] [--range <WINDOW>] [--account] [--services] [--custom <NAME>]`

| Flag | Meaning | |
|---|---|---|
| `--range <WINDOW>` | window (5m\|15m\|1h\|6h\|24h\|7d\|15d) | one of `5m` · `15m` · `1h` · `6h` · `24h` · `7d` · `15d` |
| `--account` | account-wide roll-up |  |
| `--services` | service map of app-to-app calls (default range 1h) |  |
| `--custom <NAME>` | history of one pushed custom metric (ranges 1h\|6h\|24h\|7d\|15d) |  |


## analytics

Break down past requests by route, country, referrer or status

`gregale analytics [<slug>] [--since <WINDOW>] [--until <TIMESTAMP>] [--by <DIMENSION>]`

| Flag | Meaning | |
|---|---|---|
| `--since <WINDOW>` | lookback window |  |
| `--until <TIMESTAMP>` | exclusive RFC3339 end |  |
| `--by <DIMENSION>` | grouping dimension | one of `route` · `country` · `referrer_host` · `ua_family` · `status` |


## mfa

Set up and manage multi-factor authentication

`gregale mfa [<subcommand>]`

### mfa enroll

Begin TOTP enrolment

`gregale mfa enroll [--qr-out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--qr-out <PATH>` | write the QR PNG to this path |  |

### mfa confirm

Confirm an enrolment code

`gregale mfa confirm [--code <CODE>] [<6-digit-code>]`

| Flag | Meaning | |
|---|---|---|
| `--code <CODE>` | 6-digit TOTP (alternative to positional code) |  |

Pass the code as an argument or with --code.

### mfa verify

Verify a TOTP code for step-up

`gregale mfa verify [--code <CODE>] [<6-digit-code>]`

| Flag | Meaning | |
|---|---|---|
| `--code <CODE>` | 6-digit TOTP (alternative to positional code) |  |

Pass the code as an argument or with --code.

### mfa recover

Use a recovery code

`gregale mfa recover [--code <CODE>] [<recovery-code>]`

| Flag | Meaning | |
|---|---|---|
| `--code <CODE>` | recovery code (alternative to positional code) |  |

Pass the code as an argument or with --code.

### mfa disable

Disable MFA

`gregale mfa disable [--password <PASSWORD>] [--recovery-code <CODE>]`

| Flag | Meaning | |
|---|---|---|
| `--password <PASSWORD>` | account password (prompts when omitted) |  |
| `--recovery-code <CODE>` | single-use recovery code (alternative to password) |  |


## open

Open an app&#39;s URL in your browser

`gregale open [<subcommand>] [<slug>] [--app <SLUG>] [--dashboard]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional; defaults to linked context) |  |
| `--dashboard` | open the dashboard page instead of the live URL |  |

### open docs

Open a CLI docs page

Usage: open docs [&lt;slug&gt;].


## orgs

Manage orgs, members, and workspace activity

`gregale orgs [<subcommand>]`

### orgs standards

Manage versioned application standards

#### orgs standards assignments

Inspect current versions and revisions, including inactive assignments

`gregale orgs standards assignments <list|show>`

Examples:

```sh
gregale orgs standards assignments list --org acme --limit 100
gregale orgs standards assignments show --org acme --id ASSIGNMENT_UUID
```

##### orgs standards assignments list

Page retained organization assignments

`gregale orgs standards assignments list --org <SLUG> [--after <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--after <UUID>` | last assignment UUID from the previous page |  |
| `--limit <N>` | page size (1..100) |  |

##### orgs standards assignments show

Read the current assignment revision and admission version

`gregale orgs standards assignments show --org <SLUG> --id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--id <UUID>` | assignment UUID | required |

#### orgs standards reviews

Preview assignment changes and inspect saved reviews

`gregale orgs standards reviews <preview|show|approve>`

##### orgs standards reviews preview

Save affected applications and blockers without activating a change

`gregale orgs standards reviews preview --org <SLUG> --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--file <PATH>` | assignment review JSON file | required |

##### orgs standards reviews show

Inspect a saved review and its expiry

`gregale orgs standards reviews show --org <SLUG> --id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--id <UUID>` | review UUID | required |

##### orgs standards reviews approve

Approve the exact saved review; admin only, release gated

`gregale orgs standards reviews approve --org <SLUG> --id <UUID> --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--id <UUID>` | review UUID | required |
| `--file <PATH>` | approval_hash JSON file | required |

#### orgs standards operation

Inspect rollout progress or apply gated operator controls

`gregale orgs standards operation <pause|resume|abort> --org <SLUG> --id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--id <UUID>` | operation UUID | required |

##### orgs standards operation pause

Pause outstanding rollout targets; admin only, release gated

`gregale orgs standards operation pause --org <SLUG> --id <UUID> --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--id <UUID>` | operation UUID | required |
| `--file <PATH>` | expected_updated_at JSON file | required |

##### orgs standards operation resume

Resume a paused rollout; admin only, release gated

`gregale orgs standards operation resume --org <SLUG> --id <UUID> --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--id <UUID>` | operation UUID | required |
| `--file <PATH>` | expected_updated_at JSON file | required |

##### orgs standards operation abort

Stop outstanding targets and keep installed settings

`gregale orgs standards operation abort --org <SLUG> --id <UUID> --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--id <UUID>` | operation UUID | required |
| `--file <PATH>` | expected_updated_at JSON file | required |

Admin only; release gated.

#### orgs standards exceptions

Inspect history or manage bounded exceptions

`gregale orgs standards exceptions <approve|revoke> --org <SLUG> --app <UUID> [--after <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--app <UUID>` | application UUID | required |
| `--after <UUID>` | last exception UUID from the previous page |  |
| `--limit <N>` | page size (1..100) |  |

##### orgs standards exceptions approve

Approve one field with a reason and expiry; admin only, release gated

`gregale orgs standards exceptions approve --org <SLUG> --app <UUID> --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--app <UUID>` | application UUID | required |
| `--file <PATH>` | complete mutation JSON including expected_revision | required |

##### orgs standards exceptions revoke

Revoke an exception and retain its history; admin only, release gated

`gregale orgs standards exceptions revoke --org <SLUG> --app <UUID> --file <PATH> --id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--app <UUID>` | application UUID | required |
| `--file <PATH>` | complete mutation JSON including expected_revision | required |
| `--id <UUID>` | exception UUID | required |

#### orgs standards local-intent

Replace permitted local settings with revision checks; release gated

`gregale orgs standards local-intent --org <SLUG> --app <UUID> --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--app <UUID>` | application UUID | required |
| `--file <PATH>` | complete mutation JSON including expected_revision | required |

#### orgs standards application

Inspect application adoption and installed standards

`gregale orgs standards application --org <SLUG> --app <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--app <UUID>` | application UUID | required |

#### orgs standards destinations

Manage immutable logging destination references

`gregale orgs standards destinations <list|show|create>`

##### orgs standards destinations list

List organization resources

`gregale orgs standards destinations list --org <SLUG> [--after <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--after <UUID>` | last resource UUID from the previous page |  |
| `--limit <N>` | page size (1..100) |  |

##### orgs standards destinations show

Inspect an immutable resource; credentials are omitted

`gregale orgs standards destinations show --org <SLUG> --id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--id <UUID>` | resource UUID | required |

##### orgs standards destinations create

Create an immutable resource from JSON

`gregale orgs standards destinations create --org <SLUG> --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--file <PATH>` | resource JSON file; credentials are sealed server-side | required |

#### orgs standards publishers

Manage immutable trusted publisher references

`gregale orgs standards publishers <list|show|create>`

##### orgs standards publishers list

List organization resources

`gregale orgs standards publishers list --org <SLUG> [--after <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--after <UUID>` | last resource UUID from the previous page |  |
| `--limit <N>` | page size (1..100) |  |

##### orgs standards publishers show

Inspect an immutable resource; credentials are omitted

`gregale orgs standards publishers show --org <SLUG> --id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--id <UUID>` | resource UUID | required |

##### orgs standards publishers create

Create an immutable resource from JSON

`gregale orgs standards publishers create --org <SLUG> --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--file <PATH>` | resource JSON file; credentials are sealed server-side | required |

#### orgs standards list

List the organization&#39;s latest standard versions

`gregale orgs standards list --org <SLUG> [--after <SLUG>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--after <SLUG>` | last standard slug from the previous page |  |
| `--limit <N>` | page size (1..100) |  |

#### orgs standards show

Inspect an immutable standard version

`gregale orgs standards show --org <SLUG> --standard <SLUG> [--version <N>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--standard <SLUG>` | standard slug | required |
| `--version <N>` | version; omitted reads the latest |  |

#### orgs standards publish

Publish a candidate version; does not activate it

`gregale orgs standards publish --org <SLUG> --standard <SLUG> --file <PATH> --expected-version <N> [--description <TEXT>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--standard <SLUG>` | standard slug | required |
| `--file <PATH>` | standard definition JSON file | required |
| `--expected-version <N>` | current version; 0 creates a new standard | required |
| `--description <TEXT>` | version description |  |

Examples:

```sh
gregale orgs standards publish --org acme --standard production-baseline --file standard.json --expected-version 0
```

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

Set or clear a spending cap for usage beyond your plan

See also: [`plan`](#plan), [`billing`](#billing), [`usage`](#usage), [`invoices`](#invoices).

`gregale overage-cap <cents> [--clear]`

| Flag | Meaning | |
|---|---|---|
| `--clear` | remove the overage cap |  |


## park

Stop all of an app&#39;s instances until its next request

`gregale park <slug>`

A min-instances floor stays off, and unbilled, until the next request.

Examples:

```sh
gregale park my-api
```


## plan

Change your subscription plan

See also: [`billing`](#billing), [`usage`](#usage), [`invoices`](#invoices), [`overage-cap`](#overage-cap).

`gregale plan <plan> [--yes]`

Plans: free, hobby, pro and scale. Paid upgrades open the billing provider&#39;s checkout.

&lt;plan&gt; is one of `free` · `hobby` · `pro` · `scale`.

| Flag | Meaning | |
|---|---|---|
| `--yes` | confirm a plan downgrade without prompting |  |

Examples:

```sh
gregale plan pro
```


## pipeline

Sync PostgreSQL sources into read-only mirrors

`gregale pipeline [<subcommand>]`

### pipeline connection

Register and manage encrypted source connections

#### pipeline connection add

Register a PostgreSQL source

`gregale pipeline connection add --url-file <FILE> <name>`

| Flag | Meaning | |
|---|---|---|
| `--url-file <FILE>` | private file containing a verified-TLS source URL | required |

#### pipeline connection list

List sources without credentials

`gregale pipeline connection list`

#### pipeline connection check

Check source compatibility and discover public tables

`gregale pipeline connection check [--database <ID>] [--wait] [--timeout <DURATION>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--database <ID>` | also check an existing empty managed mirror |  |
| `--wait` | wait for a fresh result (default true; use --wait=false to queue only) |  |
| `--timeout <DURATION>` | maximum wait (default 2m; maximum 1h) |  |

#### pipeline connection tables

Read the latest checked source schema and compatibility issues

`gregale pipeline connection tables [--wait] [--timeout <DURATION>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for a fresh result (default true) |  |
| `--timeout <DURATION>` | maximum wait (default 2m; maximum 1h) |  |

#### pipeline connection rotate

Rotate a source credential without changing its database identity

`gregale pipeline connection rotate --url-file <FILE> <id>`

| Flag | Meaning | |
|---|---|---|
| `--url-file <FILE>` | private file containing the new source URL | required |

#### pipeline connection delete

Delete an unused source credential

`gregale pipeline connection delete <id>`

### pipeline create

Create a managed mirror and begin initial copying

`gregale pipeline create [--connection <ID>] [--url-file <FILE>] [--database <DATABASE>] [--region <REGION>] --tables <TABLES> --read-subjects <SUBJECTS> [--wait] [--timeout <DURATION>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--connection <ID>` | registered source connection (or use --url-file) |  |
| `--url-file <FILE>` | register a source from a private URL file |  |
| `--database <DATABASE>` | existing empty managed database; otherwise create NAME-mirror |  |
| `--region <REGION>` | placement of an automatically created mirror |  |
| `--tables <TABLES>` | comma-separated public table names | required |
| `--read-subjects <SUBJECTS>` | application identities allowed to read the mirror | required |
| `--wait` | wait for initial copying and catch-up |  |
| `--timeout <DURATION>` | maximum wait (default 10m; maximum 1h) |  |

### pipeline list

List pipeline intent and observations

### pipeline status

Read pipeline intent and observed progress

`gregale pipeline status [--wait] [--timeout <DURATION>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the requested observation |  |
| `--timeout <DURATION>` | maximum wait (default 10m; maximum 1h) |  |

### pipeline wait

Wait for the pipeline to become live

`gregale pipeline wait [--wait] [--timeout <DURATION>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the requested observation |  |
| `--timeout <DURATION>` | maximum wait (default 10m; maximum 1h) |  |

### pipeline pause

Request a pause

`gregale pipeline pause [--wait] [--timeout <DURATION>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the requested observation |  |
| `--timeout <DURATION>` | maximum wait (default 10m; maximum 1h) |  |

### pipeline resume

Resume replication

`gregale pipeline resume [--wait] [--timeout <DURATION>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the requested observation |  |
| `--timeout <DURATION>` | maximum wait (default 10m; maximum 1h) |  |

### pipeline resync

Replace the owned mirror with a fresh copy

`gregale pipeline resync [--wait] [--timeout <DURATION>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the requested observation |  |
| `--timeout <DURATION>` | maximum wait (default 10m; maximum 1h) |  |

### pipeline verify

Request a bounded consistency check

`gregale pipeline verify [--wait] [--timeout <DURATION>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the requested observation |  |
| `--timeout <DURATION>` | maximum wait (default 10m; maximum 1h) |  |

### pipeline delete

Clean up replication while retaining mirror data

`gregale pipeline delete [--wait] [--timeout <DURATION>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the requested observation |  |
| `--timeout <DURATION>` | maximum wait (default 10m; maximum 1h) |  |


## data-api

Generate an API from a PostgreSQL schema and export its types

`gregale data-api [<subcommand>]`

### data-api transaction

Build a typed atomic business-operation starter

#### data-api transaction init

Generate an editable tenant-scoped order/inventory RPC package

`gregale data-api transaction init --name <NAME> --output <DIR> [--contract <VERSION>]`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | new function/table prefix | required |
| `--output <DIR>` | new transaction package directory | required |
| `--contract <VERSION>` | receipt contract (default v1) | one of `v1` · `v2` |

### data-api rotation

Inspect credential rotation prerequisites and rollout evidence

#### data-api rotation preflight

Check owner catalog prerequisites and current binding rollout

`gregale data-api rotation preflight --binding <UUID> --owner-check <FILE> [--timeout <DURATION>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--binding <UUID>` | managed Data API binding UUID | required |
| `--owner-check <FILE>` | trusted local read-only owner preflight command configuration | required |
| `--timeout <DURATION>` | check deadline (default 2m, maximum 1h) |  |

#### data-api rotation status

Inspect binding refresh and credential retirement metadata

`gregale data-api rotation status --binding <UUID> [--timeout <DURATION>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--binding <UUID>` | managed Data API binding UUID | required |
| `--timeout <DURATION>` | check deadline (default 2m, maximum 1h) |  |

### data-api verify

Verify data integrity assertions and project authorization

`gregale data-api verify <release|coverage|init> [--directory <DIR>] [--config <FILE>] [--checks <FILE>] [--access-checks <FILE>] [--fixture <FILE>]`

| Flag | Meaning | |
|---|---|---|
| `--directory <DIR>` | Data API project directory (default .) |  |
| `--config <FILE>` | project-relative development config |  |
| `--checks <FILE>` | version 1 named SQL assertion manifest (or use --access-checks) |  |
| `--access-checks <FILE>` | request collection or version 3/4/5 guided RLS check manifest |  |
| `--fixture <FILE>` | SQL fixture for the current schema (default: configured seed) |  |

Examples:

```sh
gregale --json data-api verify --checks data-checks.json --fixture fixture.sql
```

Runs locally.

#### data-api verify release

Run local Data API release checks and consolidate reports

`gregale data-api verify release --config <FILE>`

| Flag | Meaning | |
|---|---|---|
| `--config <FILE>` | version 1 release-check configuration | required |

#### data-api verify coverage

Check customer-declared verification coverage offline

`gregale data-api verify coverage --schema <FILE> --policy <FILE> [--verification <FILE>] [--plans <FILE>] [--baseline-schema <FILE>] [--check]`

| Flag | Meaning | |
|---|---|---|
| `--schema <FILE>` | current saved schema contract | required |
| `--policy <FILE>` | explicit relation requirements and named check mappings | required |
| `--verification <FILE>` | data-api verify JSON report |  |
| `--plans <FILE>` | budget-checked data-api explain JSON report |  |
| `--baseline-schema <FILE>` | previous contract to identify newly added relations |  |
| `--check` | fail if customer-required coverage is missing |  |

#### data-api verify init

Generate editable verification templates offline

`gregale data-api verify init --schema <FILE> --config <FILE> --output <DIR> [--plans <FILE>]`

| Flag | Meaning | |
|---|---|---|
| `--schema <FILE>` | saved schema contract | required |
| `--config <FILE>` | development config containing identity names | required |
| `--output <DIR>` | new verification package directory | required |
| `--plans <FILE>` | successful explain JSON report for query budgets |  |

### data-api explain

Review saved read-query plans against a disposable local database

`gregale data-api explain [--directory <DIR>] [--config <FILE>] --collection <FILE> [--fixture <FILE>] [--scenario <NAME>] [--baseline <FILE>] [--check] [--budget <FILE>]`

| Flag | Meaning | |
|---|---|---|
| `--directory <DIR>` | Data API project directory (default .) |  |
| `--config <FILE>` | project-relative development config |  |
| `--collection <FILE>` | saved API request collection | required |
| `--fixture <FILE>` | representative SQL fixture (default: configured seed) |  |
| `--scenario <NAME>` | one named collection scenario |  |
| `--baseline <FILE>` | previous explain JSON report for estimated plan comparison |  |
| `--check` | fail when query budgets or coverage checks fail |  |
| `--budget <FILE>` | version 1 query budget JSON (requires --check and --baseline) |  |

Examples:

```sh
gregale --json data-api explain --collection data-api.requests.json --fixture fixtures/before-upgrade/fixture.sql
```

### data-api fixture

Build representative migration rehearsal data

#### data-api fixture build

Export allowlisted and explicitly masked related rows

`gregale data-api fixture build --config <FILE> --output <DIR> [--database-url-env <ENV>] [--api-role <ROLE>]`

| Flag | Meaning | |
|---|---|---|
| `--config <FILE>` | JSON column allowlists, masks and root filters | required |
| `--output <DIR>` | new fixture package directory | required |
| `--database-url-env <ENV>` | environment variable holding the local or test PostgreSQL URL (default DATA_API_FIXTURE_DATABASE_URL) |  |
| `--api-role <ROLE>` | source API role used for contract introspection |  |

Examples:

```sh
gregale --json data-api fixture build --config fixture-policy.json --output fixtures/before-upgrade --api-role gregale_data_api
```

### data-api migration

Rehearse local database migrations

#### data-api migration guide

Generate reviewable client migration guidance offline

`gregale data-api migration guide --baseline <FILE> --current <FILE> [--requests <FILE>] [--dependencies <FILE>] [--coverage-policy <FILE>]`

| Flag | Meaning | |
|---|---|---|
| `--baseline <FILE>` | previous schema contract | required |
| `--current <FILE>` | candidate schema contract | required |
| `--requests <FILE>` | saved API request collection |  |
| `--dependencies <FILE>` | customer-declared consumer dependencies |  |
| `--coverage-policy <FILE>` | explicit verification mappings to review |  |

#### data-api migration preview

Rebuild a disposable database and report migration checks

`gregale data-api migration preview [--directory <DIR>] [--from-version <N>] [--fixture <FILE>] [--data-check <FILE>] --baseline <FILE> [--config <FILE>] [--output <FILE>] [--dependencies <FILE>] [--approval <FILE>] [--replay <FILE>] [--scenario <NAME>]`

| Flag | Meaning | |
|---|---|---|
| `--directory <DIR>` | Data API project directory (default .) |  |
| `--from-version <N>` | upgrade from an existing numbered migration version |  |
| `--fixture <FILE>` | SQL data fixture for the older schema (default: configured seed) |  |
| `--data-check <FILE>` | JSON manifest of post-migration SQL data assertions |  |
| `--baseline <FILE>` | read-only schema contract baseline | required |
| `--config <FILE>` | project-relative development config |  |
| `--output <FILE>` | committed types path (default: project config or client/src/database.types.ts) |  |
| `--dependencies <FILE>` | customer-declared consumer dependency manifest |  |
| `--approval <FILE>` | fingerprint-bound breaking change approvals |  |
| `--replay <FILE>` | saved request collection |  |
| `--scenario <NAME>` | one named replay scenario (requires --replay) |  |

Examples:

```sh
gregale --json data-api migration preview --baseline schema.json --replay data-api.requests.json
```

Reports API, types, client and replay checks.

### data-api onboard

Create and verify a local typed application with two-user isolation

`gregale data-api onboard [--template <NAME>] --path <DIR> --bundle <MANIFEST>`

| Flag | Meaning | |
|---|---|---|
| `--template <NAME>` | data-api-starter or data-api-teams | one of `data-api-starter` · `data-api-teams` |
| `--path <DIR>` | new starter project directory | required |
| `--bundle <MANIFEST>` | approved local data-api-bundle.json | required |

Examples:

```sh
gregale data-api onboard --path notes --bundle /path/to/data-api-bundle.json
```

### data-api install

Install or restore a verified Data API SDK bundle

`gregale data-api install [--bundle <MANIFEST>] [--directory <DIR>] [--client-directory <DIR>] [--base-url <HTTPS_URL>] [--restore] [--from <DIR>]`

| Flag | Meaning | |
|---|---|---|
| `--bundle <MANIFEST>` | approved local data-api-bundle.json |  |
| `--directory <DIR>` | project directory (default .) |  |
| `--client-directory <DIR>` | client directory relative to the project (default .) |  |
| `--base-url <HTTPS_URL>` | immutable artifact directory for future restoration |  |
| `--restore` | restore the committed artifact pin |  |
| `--from <DIR>` | local artifact directory for restoration |  |

Examples:

```sh
gregale data-api install --bundle /path/to/data-api-bundle.json
gregale data-api install --restore
```

### data-api dev

Run a disposable local Data API with test identities

`gregale data-api dev [--config <FILE>] [--directory <DIR>] [--output <FILE>] [--port <PORT>] [--check] [--check-types] [--validators] [--once] [--watch] [--baseline <FILE>] [--check-breaking] [--approval <FILE>] [--accepted-contracts <FILE>] [--replay <FILE>] [--scenario <NAME>]`

| Flag | Meaning | |
|---|---|---|
| `--config <FILE>` | project-relative development config (default: data-api.dev.json) |  |
| `--directory <DIR>` | project directory (default .) |  |
| `--output <FILE>` | types output relative to the project |  |
| `--port <PORT>` | loopback gateway port (default 8080; 0 selects a free port) |  |
| `--check` | run configured client checks |  |
| `--check-types` | compare committed types without rewriting them (requires --once) |  |
| `--validators` | generate runtime table and RPC argument validators alongside types |  |
| `--once` | verify and exit, removing disposable containers |  |
| `--watch` | watch migrations and refresh API/types (default true; use --watch=false to disable) |  |
| `--baseline <FILE>` | read-only saved contract (default: last successful local contract) |  |
| `--check-breaking` | reject breaking changes; requires an existing baseline |  |
| `--approval <FILE>` | reviewed compatibility approvals; requires --baseline and --check-breaking |  |
| `--accepted-contracts <FILE>` | project-relative reviewed runtime contract policy |  |
| `--replay <FILE>` | replay saved requests relative to the project (requires --once) |  |
| `--scenario <NAME>` | select one named scenario (requires --replay) |  |

Examples:

```sh
gregale data-api dev --check
gregale data-api dev --port 0 --check --once
```

### data-api create

Deploy a managed PostgREST Data API

`gregale data-api create --database <DATABASE> [--schema <SCHEMA>] [--read-only] [--scope <SCOPE>] [--issuer <HTTPS_URL>] [--jwks-url <HTTPS_URL>] [--audience <AUDIENCE>] [--origins <ORIGINS>] [--resume] [--public-read <SCHEMA.RELATION[,...]>] [--public-rpc <SCHEMA.FUNCTION[,...]>] [--aggregates] [--migrations <DIR>] [--release-command <COMMAND>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--database <DATABASE>` | ready managed database name or ID | required |
| `--schema <SCHEMA>` | exposed schema (api, or public with --read-only) |  |
| `--read-only` | use a SELECT-only public-schema binding and reject mutations |  |
| `--scope <SCOPE>` | environment scope |  |
| `--issuer <HTTPS_URL>` | application JWT issuer |  |
| `--jwks-url <HTTPS_URL>` | application JWKS URL |  |
| `--audience <AUDIENCE>` | application JWT audience |  |
| `--origins <ORIGINS>` | comma-separated browser origins |  |
| `--resume` | resume configuration and deployment of an existing app |  |
| `--public-read <SCHEMA.RELATION[,...]>` | relations readable without a token (RLS tables or security-invoker views) |  |
| `--public-rpc <SCHEMA.FUNCTION[,...]>` | approved STABLE or IMMUTABLE functions callable without a token by GET |  |
| `--aggregates` | enable aggregate queries; requires installed SQL deadline functions |  |
| `--migrations <DIR>` | single-app project: run this migration directory as the app&#39;s release task |  |
| `--release-command <COMMAND>` | single-app release command (default node migrations/release.mjs) |  |

Examples:

```sh
gregale data-api create shop-data --database shop-db --issuer https://id.example.com/ --jwks-url https://id.example.com/jwks.json --audience shop --migrations migrations
```

### data-api deploy

Deploy a single-app Data API with its migrations as the release task

`gregale data-api deploy --migrations <DIR> [--release-command <COMMAND>] [--scope <SCOPE>] [--timeout <DURATION>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--migrations <DIR>` | migration directory run with MIGRATION_DATABASE_URL | required |
| `--release-command <COMMAND>` | release command (default node migrations/release.mjs) |  |
| `--scope <SCOPE>` | environment scope |  |
| `--timeout <DURATION>` | deployment and release wait (default 15m) |  |

### data-api configure

Change access, aggregates, usage reporting or caps

`gregale data-api configure [--public-read <SCHEMA.RELATION[,...]>] [--no-public-read] [--public-rpc <SCHEMA.FUNCTION[,...]>] [--no-public-rpc] [--public-read-cache <DURATION|off>] [--usage-reporting <on|off>] [--monthly-caps <requests=N[,anonymous=N]|off>] [--aggregates <on|off>] [--pool <N|default>] [--server-key-concurrency <N|default>] [--server-key-rate-limit <N[,burst=M]|off>] [--scope <SCOPE>] [--no-restart] <name>`

| Flag | Meaning | |
|---|---|---|
| `--public-read <SCHEMA.RELATION[,...]>` | relations readable without a token |  |
| `--no-public-read` | disable anonymous reads |  |
| `--public-rpc <SCHEMA.FUNCTION[,...]>` | approved STABLE or IMMUTABLE functions callable without a token by GET |  |
| `--no-public-rpc` | disable anonymous RPC calls |  |
| `--public-read-cache <DURATION|off>` | cache canonical public reads and RPC calls at the edge (max 1h) |  |
| `--usage-reporting <on|off>` | report usage to the platform (on for new Data APIs) | one of `on` · `off` |
| `--monthly-caps <requests=N[,anonymous=N]|off>` | monthly owner caps; reached caps answer 429 until the next UTC month |  |
| `--aggregates <on|off>` | aggregate queries; on also enables the SQL deadline | one of `on` · `off` |
| `--pool <N|default>` | engine connections per instance, 1 to 8 (default 2) |  |
| `--server-key-concurrency <N|default>` | admission slots server keys may hold together (default: all but one) |  |
| `--server-key-rate-limit <N[,burst=M]|off>` | requests per second per server key; refusals answer 429 |  |
| `--scope <SCOPE>` | environment scope |  |
| `--no-restart` | store settings without a fresh restart |  |

Restarts the runtime fresh.

### data-api usage

Show reported requests, rows and bytes with month-to-date totals

`gregale data-api usage [--since <DURATION>] [--granularity <hour|day>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION>` | lookback such as 24h, 7d or 30d (default 7d, max 90d) |  |
| `--granularity <hour|day>` | bucket size (hour covers at most 48h) | one of `hour` · `day` |

Examples:

```sh
gregale data-api usage shop-data --since 30d
```

Per hour or per day.

### data-api storage

File storage governed by RLS on storage_objects

#### data-api storage sql

Print the storage_objects migration

`gregale data-api storage sql [--schema <SCHEMA>]`

| Flag | Meaning | |
|---|---|---|
| `--schema <SCHEMA>` | the Data API&#39;s first exposed schema (default api) |  |

Examples:

```sh
gregale data-api storage sql > migrations/0005_storage.sql
```

Includes the table, RLS, guard trigger and example policies.

#### data-api storage enable

Bind a bucket read-write and turn storage on

`gregale data-api storage enable --bucket <BUCKET> [--scope <SCOPE>] [--region <REGION>] [--max-object-bytes <N>] [--no-restart] <name>`

| Flag | Meaning | |
|---|---|---|
| `--bucket <BUCKET>` | bucket to store files in (created and bound when needed) | required |
| `--scope <SCOPE>` | environment scope |  |
| `--region <REGION>` | object-storage region for a new bucket |  |
| `--max-object-bytes <N>` | largest upload in bytes (default 25 MiB, at most 100 MiB) |  |
| `--no-restart` | store settings without a fresh restart |  |

Examples:

```sh
gregale data-api storage enable shop-data --bucket uploads
```

#### data-api storage status

Show whether storage is on and its bound buckets

`gregale data-api storage status [--scope <SCOPE>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | environment scope |  |

#### data-api storage disable

Turn storage off; buckets stay bound and keep their files

`gregale data-api storage disable [--scope <SCOPE>] [--no-restart] <name>`

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | environment scope |  |
| `--no-restart` | store settings without a fresh restart |  |

### data-api live

Live reads: results that stay current over a realtime connection

#### data-api live enable

Enable live queries, usage reporting and a live-only endpoint

`gregale data-api live enable [--scope <SCOPE>] [--no-restart] <name>`

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | environment scope of the Data API&#39;s database binding |  |
| `--no-restart` | store settings without a fresh restart |  |

Examples:

```sh
gregale data-api live enable shop-data
```

Creates a live-only realtime endpoint.

#### data-api live status

Show what live reads need and the WebSocket URL

`gregale data-api live status [--scope <SCOPE>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | environment scope of the Data API&#39;s database binding |  |

### data-api server-key

Manage server keys for trusted backends (RLS still applies)

#### data-api server-key create

Generate a key, store its hash in a free slot and show the key once

`gregale data-api server-key create [--read-only] [--relations <SCHEMA.RELATION[,...]>] [--rpcs <SCHEMA.FUNCTION[,...]>] [--no-subjects] [--replace-oldest] [--scope <SCOPE>] [--no-restart] <name>`

| Flag | Meaning | |
|---|---|---|
| `--read-only` | allow only GET and HEAD; RPCs need --rpcs |  |
| `--relations <SCHEMA.RELATION[,...]>` | relations the key may use, including embeddings |  |
| `--rpcs <SCHEMA.FUNCTION[,...]>` | approved RPCs the key may call |  |
| `--no-subjects` | refuse X-Gregale-Subject |  |
| `--replace-oldest` | replace the older slot when both are in use |  |
| `--scope <SCOPE>` | environment scope |  |
| `--no-restart` | store the hash without a fresh restart |  |

#### data-api server-key list

Show which key slots are in use

`gregale data-api server-key list [--scope <SCOPE>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | environment scope |  |

#### data-api server-key revoke

Remove a key slot

`gregale data-api server-key revoke --slot <a|b|all> [--scope <SCOPE>] [--no-restart] <name>`

| Flag | Meaning | |
|---|---|---|
| `--slot <a|b|all>` | slot to revoke | required; one of `a` · `b` · `all` |
| `--scope <SCOPE>` | environment scope |  |
| `--no-restart` | remove the hash without a fresh restart |  |

### data-api types

Generate TypeScript types or Python/Go clients from the contract

`gregale data-api types [--output <FILE>] [--check] [--snapshot] [--validators] [--language <LANGUAGE>] [--package <NAME>] [--snapshot-file <FILE>] [--timeout <DURATION>] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--output <FILE>` | generated TypeScript, Python, Go or snapshot output |  |
| `--check` | fail if the output file is stale |  |
| `--snapshot` | export a JSON baseline for data-api diff |  |
| `--validators` | include typed runtime table and RPC argument validators |  |
| `--language <LANGUAGE>` | client language (default typescript) | one of `typescript` · `python` · `go` |
| `--package <NAME>` | Go package name (default gregaledata) |  |
| `--snapshot-file <FILE>` | render Python or Go from a saved snapshot without contacting the API |  |
| `--timeout <DURATION>` | task wait deadline (default 2m) |  |

Examples:

```sh
gregale data-api types shop-data --language python --output shop_data.py
gregale data-api types --snapshot-file contract.json --language go --package shopdata --output shopdata/client.go
```

### data-api check

Check compatibility and committed types without rewriting files

`gregale data-api check --baseline <FILE> --types <FILE> [--current <FILE>] [--approval <FILE>] [--timeout <DURATION>] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--baseline <FILE>` | committed JSON contract baseline | required |
| `--types <FILE>` | committed TypeScript types to compare | required |
| `--current <FILE>` | saved current contract for an offline check; omit the app name |  |
| `--approval <FILE>` | reviewed breaking changes bound to both fingerprints |  |
| `--timeout <DURATION>` | entire check deadline (default 2m, maximum 1h) |  |

Examples:

```sh
gregale --json data-api check notes-data --baseline schema.json --types client/src/database.types.ts
gregale data-api check --current candidate.json --baseline schema.json --types client/src/database.types.ts
```

### data-api diff

Compare the current schema with a saved JSON baseline

`gregale data-api diff --baseline <FILE> [--current <FILE>] [--check] [--check-compatible] [--approval <FILE>] [--accept-contract <FILE>] [--dependencies <FILE>] [--timeout <DURATION>] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--baseline <FILE>` | JSON snapshot exported with types --snapshot | required |
| `--current <FILE>` | compare a saved current contract offline; omit the app name |  |
| `--check` | fail on unapproved breaking contract changes |  |
| `--check-compatible` | fail on unapproved breaking changes (alias of --check) |  |
| `--approval <FILE>` | reviewed changes bound to both contract fingerprints |  |
| `--accept-contract <FILE>` | write an owner runtime policy; requires zero breaking changes |  |
| `--dependencies <FILE>` | customer-declared saved-view and RPC dependencies for impact reporting |  |
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

### data-api replay

Verify saved authorization scenarios against the hosted API

`gregale data-api replay --collection <FILE> --identities <FILE> [--scenario <NAME>] [--refresh-token-env <ENV>] [--timeout <DURATION>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--collection <FILE>` | saved request collection | required |
| `--identities <FILE>` | JSON map of identities to JWT environment variables | required |
| `--scenario <NAME>` | select one named scenario |  |
| `--refresh-token-env <ENV>` | optional private refresh token environment variable |  |
| `--timeout <DURATION>` | replay deadline (default 5m, maximum 1h) |  |

Examples:

```sh
gregale data-api replay notes-data --collection data-api.requests.json --identities data-api.identities.json --scenario notes-rls
```

### data-api health

Check current Data API health and recent request failures

`gregale data-api health [--since <DURATION|RFC3339>] [--timeout <DURATION>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--since <DURATION|RFC3339>` | retained request log window (default 15m) |  |
| `--timeout <DURATION>` | entire report deadline (default 30s, maximum 1h) |  |

Examples:

```sh
gregale data-api health notes-data
gregale --json data-api health notes-data --since 15m
```

### data-api diagnostics

Summarize retained request logs and look up request IDs

`gregale data-api diagnostics [--file <FILE>] [--since <DURATION|RFC3339>] [--deployment <ID>] [--request-id <UUID>] [--sort <requests|p95|errors>] [--timeout <DURATION>] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--file <FILE>` | saved runtime NDJSON or JSON log envelopes; omit the app name |  |
| `--since <DURATION|RFC3339>` | request time lower bound (for example 15m) |  |
| `--deployment <ID>` | restrict hosted logs to a deployment |  |
| `--request-id <UUID>` | look up one gateway-generated request ID |  |
| `--sort <requests|p95|errors>` | query shape ranking (default requests; p95 uses gateway latency) |  |
| `--timeout <DURATION>` | read deadline (default 2m, maximum 1h) |  |

Examples:

```sh
gregale --json data-api diagnostics notes-data --since 15m
gregale data-api diagnostics --file request-logs.ndjson
```

### data-api status

Inspect serving contracts and durable refresh state

`gregale data-api status [--baseline <FILE>] [--access-token-env <ENV>] [--refresh-token-env <ENV>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--baseline <FILE>` | optional JSON snapshot to compare with the serving contract |  |
| `--access-token-env <ENV>` | application JWT environment variable |  |
| `--refresh-token-env <ENV>` | optional private refresh token environment variable |  |

Examples:

```sh
gregale --json data-api status notes-data
```

### data-api sync

Run migrations, refresh the API, export types and check the client

`gregale data-api sync [--check] [--resume-only] --config <FILE> [--timeout <DURATION>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--check` | check coordinated setup without pausing, migrating or restarting |  |
| `--resume-only` | reverify an owned paused refresh without rerunning migrations |  |
| `--config <FILE>` | JSON workflow with output, migrate and check argument arrays | required |
| `--timeout <DURATION>` | entire workflow deadline (default 20m, maximum 1h) |  |

Examples:

```sh
gregale data-api sync notes-data --config data-api.json
```


## ps

Show an app&#39;s live instances and their state

`gregale ps [<slug>] [--all]`

| Flag | Meaning | |
|---|---|---|
| `--all` | include the newest 100 retained history rows (parked rows expire after 30d by default) |  |


## queue

Inspect queues and manage queue bindings

`gregale queue [<subcommand>]`

### queue tail

Tail the wake queue

`gregale queue tail [--app <SLUG>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional; defaults to linked context) |  |

### queue send

Enqueue a wake request

`gregale queue send [--app <SLUG>] [--payload <J>] [--queue-name <QUEUE>] [--environment <ENV>] [--work-policy <NAME>] [--work-key <JSON>] [--work-fairness-key <JSON>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional; defaults to linked context) |  |
| `--payload <J>` | JSON payload (inline \| @file \| -) |  |
| `--queue-name <QUEUE>` | logical queue name |  |
| `--environment <ENV>` | registered project environment with an enabled queue binding |  |
| `--work-policy <NAME>` | named work policy for an unnamed queue |  |
| `--work-key <JSON>` | JSON scalar identifying related work |  |
| `--work-fairness-key <JSON>` | JSON scalar shared by related work keys |  |

### queue receive

Wait for the next queue row the platform delivers

`gregale queue receive [--app <SLUG>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional; defaults to linked context) |  |

### queue state

Show queue state

`gregale queue state [--app <SLUG>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional; defaults to linked context) |  |

### queue status

Show queue depth, scaling, bindings, and liveness

`gregale queue status [--app <SLUG>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional; defaults to linked context) |  |

### queue peek

Peek at the next wake

`gregale queue peek [--app <SLUG>] [--limit <N>] [--cursor <CURSOR>] [--before <CURSOR>] [--all] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional; defaults to linked context) |  |
| `--limit <N>` | page size (1..100, default 50) |  |
| `--cursor <CURSOR>` | opaque continuation cursor |  |
| `--before <CURSOR>` | alias for --cursor |  |
| `--all` | walk every page using --limit and --cursor |  |

### queue dead-letter

Inspect the dead-letter queue

`gregale queue dead-letter [--app <SLUG>] [--limit <N>] [--cursor <CURSOR>] [--before <CURSOR>] [--all] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional; defaults to linked context) |  |
| `--limit <N>` | page size (1..100, default 50) |  |
| `--cursor <CURSOR>` | opaque continuation cursor |  |
| `--before <CURSOR>` | alias for --cursor |  |
| `--all` | walk every page using --limit and --cursor |  |

### queue ack

Ack a wake

`gregale queue ack [--app <SLUG>] <slug> <row-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional; defaults to linked context) |  |

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

Inspect, replay or purge events that failed delivery

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

Replay one event, or all of them

`gregale dlq replay [--all] [--limit <N>] <app> [<event-id>]`

| Flag | Meaning | |
|---|---|---|
| `--all` | replay pending events |  |
| `--limit <N>` | maximum events (1..200) |  |

Use --all for every event.

### dlq purge

Purge one event, or all of them

`gregale dlq purge [--all] [--yes] [--limit <N>] <app> [<event-id>]`

| Flag | Meaning | |
|---|---|---|
| `--all` | purge all events (asks for typed confirmation) |  |
| `--yes` | skip the --all confirmation (for scripts) |  |
| `--limit <N>` | page size (1..200) |  |

Use --all for every event.


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

`gregale realtime delete <app> <endpoint-id> --yes`

| Flag | Meaning | |
|---|---|---|
| `--yes` | confirm the deletion | required |

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

### realtime send-principal

Send a message to every connection of one principal

`gregale realtime send-principal --principal <ID> [--data <DATA>] [--data-stdin] [--binary] [--delivery <MODE>] [--message-id <ID>] [--receipt] [--fallback-after-seconds <N>] [--notification-category <NAME>] [--notification-group <KEY>] [--notification-group-label <LABEL>] [--notification-priority <PRIORITY>] [--notification-ttl-seconds <N>] [--notification-collapse-key <KEY>] [--notification-not-before <TIME>] <app> <endpoint-id>`

| Flag | Meaning | |
|---|---|---|
| `--principal <ID>` | verified OIDC principal to target | required |
| `--data <DATA>` | UTF-8 message text (or --data-stdin) |  |
| `--data-stdin` | read the message bytes from stdin |  |
| `--binary` | send as a binary frame |  |
| `--delivery <MODE>` | live, or retained in the principal&#39;s durable inbox | one of `live` · `retained` |
| `--message-id <ID>` | stable message ID; required with --receipt or --delivery retained |  |
| `--receipt` | track client acknowledgements for one hour (live only) |  |
| `--fallback-after-seconds <N>` | request push or webhook fallback after N unacknowledged seconds (retained only) |  |
| `--notification-category <NAME>` | push preference category (default notifications) |  |
| `--notification-group <KEY>` | stable conversation, job or project group key |  |
| `--notification-group-label <LABEL>` | group name shown in summaries |  |
| `--notification-priority <PRIORITY>` | push priority | one of `low` · `normal` · `urgent` |
| `--notification-ttl-seconds <N>` | push lifetime, at most 259200 seconds |  |
| `--notification-collapse-key <KEY>` | replace queued alerts that share this key |  |
| `--notification-not-before <TIME>` | earliest push delivery, RFC3339, at most 48 hours ahead |  |

### realtime receipt

Show client acknowledgements for one sent message

`gregale realtime receipt <app> <endpoint-id> <message-id>`

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
| `--delivery <MODE>` | live (the default) | one of `live` |
| `--idempotency-key <KEY>` | stable retry key |  |

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

`gregale rollback [<subcommand>] <slug> [--interactive] [--to <deployment_id|vN>] [--expected-current <deployment_id|vN>] [--reason <TEXT>] [--acknowledge-releases] [--restore-databases] [--wait] [--no-wait] [--timeout <duration>] [--poll-interval <duration>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose and review a historical release, confirm, and follow rollback progress |  |
| `--to <deployment_id|vN>` | target deployment id or vN revision (e.g. v41) |  |
| `--expected-current <deployment_id|vN>` | exact completed serving deployment; requires --to |  |
| `--reason <TEXT>` | one-line reason of at most 256 bytes; requires --expected-current |  |
| `--acknowledge-releases` | roll back even though release commands ran after the target last served; rollback does not undo them |  |
| `--restore-databases` | also restore the managed database the crossed releases migrated, and move the app to that copy; the app pauses while it switches |  |
| `--wait` | wait until the rollback completes (default for a plain rollback; a checked rollback waits only with this flag) |  |
| `--no-wait` | return once a plain rollback is requested instead of waiting for the previous release to serve |  |
| `--timeout <duration>` | wait deadline (default 10m) |  |
| `--poll-interval <duration>` | poll interval (default 2s) |  |
| `--json` | machine-readable output |  |

Examples:

```sh
gregale rollback my-api --interactive
gregale rollback my-api
gregale rollback my-api --to v41
gregale rollback my-api --to v41 --expected-current v42 --wait
gregale rollback my-api --to v41 --restore-databases
```

### rollback status

Read an exact rollback operation

`gregale rollback status --operation <UUID> [--wait] [--timeout <duration>] [--poll-interval <duration>] [--json] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--operation <UUID>` | accepted rollback operation UUID | required |
| `--wait` | wait for completion with a committed audit receipt |  |
| `--timeout <duration>` | wait deadline (default 10m) |  |
| `--poll-interval <duration>` | poll interval (default 2s) |  |
| `--json` | print the operation receipt |  |

Waiting never submits another rollback.

### rollback switches

List rollbacks that restored or are restoring the database

`gregale rollback switches [--json] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--json` | machine-readable output |  |

### rollback switch

Show, follow or cancel a rollback that restores the database

`gregale rollback switch [--cancel] [--wait] [--timeout <duration>] [--poll-interval <duration>] [--json] <slug> <switch-id>`

| Flag | Meaning | |
|---|---|---|
| `--cancel` | cancel the switch; the app keeps its current release and database |  |
| `--wait` | follow the switch until it ends |  |
| `--timeout <duration>` | wait deadline (default 60m) |  |
| `--poll-interval <duration>` | poll interval (default 2s) |  |
| `--json` | machine-readable output |  |

Examples:

```sh
gregale rollback switch my-api 6f1c2b0e-7d2a-4f7e-9a51-3c8d2f0b9e41 --wait
```

A switch can be cancelled until it starts switching the release and the bindings.


## projects

Inspect and recover repository projects

`gregale projects [<subcommand>] <project-slug>`

### projects list

List projects in this account

### projects info

Show a project and its workloads

`gregale projects info <slug>`

### projects environments

Manage project environments, releases and promotions

Qualify binds probes to release, config and secret revisions.

#### projects environments list

List environments

`gregale projects environments list <project-slug>`

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

List release graphs and qualify changes with binding checks

`gregale projects environments release-sets <stage|stage-status|cancel-stage|prepare-rollback|rollback|prepare|verification-runs|verify|activate|prepare-deactivation|deactivate> [--before <CURSOR>] [--cursor <CURSOR>] [--all] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--before <CURSOR>` | alias for --cursor |  |
| `--cursor <CURSOR>` | opaque continuation cursor |  |
| `--all` | walk every page |  |
| `--limit <N>` | page size |  |

Covers activation, rollback and deactivation.

##### projects environments release-sets stage

Lease capacity for exact candidate services without switching

`gregale projects environments release-sets stage --file <FILE> [--ttl-seconds <N>] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--file <FILE>` | prepared forward or rollback manifest JSON | required |
| `--ttl-seconds <N>` | lease duration, 1–1800 seconds; repeating does not extend |  |

The capacity is billable; routes are not switched.

##### projects environments release-sets stage-status

Observe staging readiness, blockers and expiry

`gregale projects environments release-sets stage-status --stage <UUID> <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--stage <UUID>` | stage identity | required |

##### projects environments release-sets cancel-stage

Cancel temporary candidate capacity

`gregale projects environments release-sets cancel-stage --stage <UUID> <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--stage <UUID>` | stage identity | required |

##### projects environments release-sets prepare-rollback

Select a retained graph and freeze current routing and policies

`gregale projects environments release-sets prepare-rollback --file <FILE> <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--file <FILE>` | historical release ID and retention TTL JSON | required |

##### projects environments release-sets rollback

Recover a qualified historical graph atomically

`gregale projects environments release-sets rollback --file <FILE> <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--file <FILE>` | prepared recovery manifest JSON | required |

Records the recovery lineage.

##### projects environments release-sets prepare

Freeze the candidate graph, routing and policies as a manifest

`gregale projects environments release-sets prepare --file <FILE> <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--file <FILE>` | release selection or prepared manifest JSON | required |

Emits a JSON manifest of the enforced policies.

##### projects environments release-sets verification-runs

Inspect, resume or cancel durable graph qualification

`gregale projects environments release-sets verification-runs <list|diagnostics|status|resume|cancel>`

###### projects environments release-sets verification-runs list

List retained graph receipt roots

`gregale projects environments release-sets verification-runs list [--limit <N>] [--cursor <CURSOR>] [--deployment <UUID>] [--status <STATUS>] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (default 20; range 1–100) |  |
| `--cursor <CURSOR>` | next page cursor; reuse filters |  |
| `--deployment <UUID>` | exact deployment filter |  |
| `--status <STATUS>` | stored state or observed expiry |  |

###### projects environments release-sets verification-runs diagnostics

Explain blockers, public changes and recovery actions

`gregale projects environments release-sets verification-runs diagnostics <project> <environment> <run-id>`

###### projects environments release-sets verification-runs status

Inspect the frozen graph and member progress

`gregale projects environments release-sets verification-runs status <project> <environment> <run-id>`

###### projects environments release-sets verification-runs resume

Resume the original graph without duplicate work

`gregale projects environments release-sets verification-runs resume [--progress <MODE>] [--status-read-retries <N>] [--concurrency <N>] [--poll-interval <DURATION>] [--wait-timeout <DURATION>] <project> <environment> <run-id>`

| Flag | Meaning | |
|---|---|---|
| `--progress <MODE>` | durable stderr progress: auto, plain, ndjson or off |  |
| `--status-read-retries <N>` | transient durable status GET retries (default 3; range 0–8) |  |
| `--concurrency <N>` | shared graph probe budget (1–8) |  |
| `--poll-interval <DURATION>` | polling interval |  |
| `--wait-timeout <DURATION>` | remaining wait budget |  |

###### projects environments release-sets verification-runs cancel

Fence graph admissions and cancel unfinished owned work

`gregale projects environments release-sets verification-runs cancel <project> <environment> <run-id>`

##### projects environments release-sets verify

Qualify the prepared graph without changing routes

`gregale projects environments release-sets verify [--durable] [--run-id <UUID>] [--progress <MODE>] [--status-read-retries <N>] [--concurrency <N>] --file <FILE> [--dry-run] [--queue-publish] [--object-storage-write] [--wait-timeout <DURATION>] [--poll-interval <DURATION>] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--durable` | persist graph and member receipts for recovery |  |
| `--run-id <UUID>` | caller-selected creation recovery identity |  |
| `--progress <MODE>` | durable stderr progress: auto, plain, ndjson or off |  |
| `--status-read-retries <N>` | transient durable status GET retries (default 3; range 0–8) |  |
| `--concurrency <N>` | shared graph probe limit (default 2; range 1–8) |  |
| `--file <FILE>` | release selection or prepared manifest JSON | required |
| `--dry-run` | preview every member without tasks |  |
| `--queue-publish` | consent to planned safe queue messages |  |
| `--object-storage-write` | consent to planned disposable object canaries |  |
| `--wait-timeout <DURATION>` | graph qualification time budget |  |
| `--poll-interval <DURATION>` | probe polling interval |  |

##### projects environments release-sets activate

Atomically activate after checking every member and service recipient

`gregale projects environments release-sets activate --file <FILE> <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--file <FILE>` | release selection or prepared manifest JSON | required |

##### projects environments release-sets prepare-deactivation

Freeze the active graph and its fallbacks as a manifest

`gregale projects environments release-sets prepare-deactivation <project> <environment>`

Includes every 100% fallback; emits a qualification manifest.

##### projects environments release-sets deactivate

Atomically return to qualified fallback routes and record deactivation

`gregale projects environments release-sets deactivate --file <FILE> <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--file <FILE>` | prepared deactivation manifest JSON | required |

#### projects environments releases

List live workload deployments

`gregale projects environments releases`

#### projects environments qualify

Run health and smoke probes against the active release set

`gregale projects environments qualify --profile <FILE> <project-slug> <environment-slug>`

| Flag | Meaning | |
|---|---|---|
| `--profile <FILE>` | YAML probe profile defining every release-set workload | required |

Probes are GET requests to exact release-set deployments.

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

#### projects environments secrets

List or change secrets shared by an environment&#39;s services

`gregale projects environments secrets <set|rotate|unset> <project-slug> <environment-slug>`

Examples:

```sh
gregale projects environments secrets shop production
printf 'DATABASE_PASSWORD=%s\n' "$PW" | gregale projects environments secrets set shop production --from-stdin
```

Values are write-only.

##### projects environments secrets set

Set secrets shared by every service of an environment

`gregale projects environments secrets set [--from-stdin] [--file <PATH>] <project-slug> <environment-slug> [KEY=VALUE...]`

| Flag | Meaning | |
|---|---|---|
| `--from-stdin` | read KEY=VALUE pairs from stdin (one per line) |  |
| `--file <PATH>` | read KEY=VALUE pairs from a private file |  |

Services receive them at their next boot unless they set the key themselves.

##### projects environments secrets rotate

Replace the values of existing shared secrets

`gregale projects environments secrets rotate [--from-stdin] [--file <PATH>] <project-slug> <environment-slug> [KEY=VALUE...]`

| Flag | Meaning | |
|---|---|---|
| `--from-stdin` | read KEY=VALUE pairs from stdin (one per line) |  |
| `--file <PATH>` | read KEY=VALUE pairs from a private file |  |

##### projects environments secrets unset

Remove shared secrets

`gregale projects environments secrets unset <project-slug> <environment-slug> <KEY...>`

#### projects environments routes

Manage environment routes

`gregale projects environments routes`

#### projects environments policies

Manage environment policies

`gregale projects environments policies`

#### projects environments queues

Read or replace a stage workload&#39;s queue definitions

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

Consumer activation is unavailable. Set takes JSON with expected_revision and bindings; [] removes all definitions.

##### projects environments queues get

Read queue definitions and their workload revision as JSON

`gregale projects environments queues get <project> <stage> <workload>`

Examples:

```sh
gregale projects environments queues get shop staging shop-worker
```

##### projects environments queues set

Replace queue definitions from JSON

`gregale projects environments queues set [--file <PATH>] [--stdin] <project> <stage> <workload>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | JSON queue configuration file (choose --file or --stdin) |  |
| `--stdin` | read JSON queue configuration from stdin |  |

Examples:

```sh
gregale projects environments queues set shop staging shop-worker --file queues.json
```

The JSON contains expected_revision and bindings; [] removes all stage bindings.

#### projects environments gitops

Review Git definitions, adopt owned fields, and inspect reconciliation

`gregale projects environments gitops <status|explain|wait|bind|rebind|unbind|review|plan|promotion-plan|promotion-pr|releases|rollback-plan|rollback-pr|approve|adoption-preview|adopt|controls|override|remove-override>`

##### projects environments gitops status

Inspect the source and recent reconciliation attempts

`gregale projects environments gitops status <project> <environment>`

##### projects environments gitops explain

Explain recorded workload progress and safe next actions

`gregale projects environments gitops explain [--json] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--json` | Output the read-only assessment as JSON |  |

Examples:

```sh
gregale projects environments gitops explain shop production
```

##### projects environments gitops wait

Wait for recorded serving evidence for an exact merged Git commit

`gregale projects environments gitops wait --commit <SHA> [--timeout <DURATION>] [--poll-interval <DURATION>] [--json] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--commit <SHA>` | Exact merged lowercase Git commit to wait for | required |
| `--timeout <DURATION>` | Overall deadline (default 15m, maximum 24h) |  |
| `--poll-interval <DURATION>` | Status read delay (default 2s, from 1s to 1m) |  |
| `--json` | Emit one final read-only rollout receipt as JSON |  |

Examples:

```sh
gregale projects environments gitops wait shop production --commit MERGED_SHA --timeout 15m --json
```

##### projects environments gitops bind

Bind the verified project repository to a definition

`gregale projects environments gitops bind [--manifest-path <PATH>] [--provider <PROVIDER>] [--repository <GROUP/PROJECT>] [--approval-policy <POLICY>] [--ref <REF>] [--mode <MODE>] [--prune] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--manifest-path <PATH>` | Environment definition path in Git |  |
| `--provider <PROVIDER>` | github (default) or gitlab |  |
| `--repository <GROUP/PROJECT>` | GitLab.com repository path |  |
| `--approval-policy <POLICY>` | manual or protected_branch (GitHub only) |  |
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

##### projects environments gitops plan

Preview candidate field changes without approval or writes

`gregale projects environments gitops plan --commit <SHA> [--format <FORMAT>] [--fail-on-blocked] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--commit <SHA>` | Exact immutable commit SHA | required |
| `--format <FORMAT>` | json (default) or markdown |  |
| `--fail-on-blocked` | Exit 2 after printing ownership or unsupported-operation blockers |  |

Examples:

```sh
gregale projects environments gitops plan shop staging --commit COMMIT_SHA --format markdown --fail-on-blocked
```

##### projects environments gitops promotion-plan

Preview reviewed image promotion and export a target manifest proposal

`gregale projects environments gitops promotion-plan --from <ENV> --target-commit <SHA> [--format <FORMAT>] [--expected-plan-hash <HASH>] [--manifest <PATH>] [--fail-on-blocked] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--from <ENV>` | Source environment with a proven serving release | required |
| `--target-commit <SHA>` | Immutable commit containing the target manifest | required |
| `--format <FORMAT>` | json (default), markdown, patch or manifest |  |
| `--expected-plan-hash <HASH>` | Reviewed observation hash; required for patch or manifest export |  |
| `--manifest <PATH>` | Local target YAML/JSON file; required only for manifest export |  |
| `--fail-on-blocked` | Exit 2 after printing a blocked plan |  |

Examples:

```sh
gregale projects environments gitops promotion-plan shop production --from staging --target-commit COMMIT_SHA
```

##### projects environments gitops promotion-pr

Open a draft promotion PR or MR from a freshly checked reviewed plan

`gregale projects environments gitops promotion-pr --from <ENV> --target-commit <SHA> --expected-plan-hash <HASH> [--yes] [--json] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--from <ENV>` | Source environment with a proven serving release | required |
| `--target-commit <SHA>` | Reviewed current target branch commit | required |
| `--expected-plan-hash <HASH>` | Reviewed promotion observation hash | required |
| `--yes` | Create the draft proposal; default is a read-only preview |  |
| `--json` | Emit the proposal receipt as JSON |  |

Examples:

```sh
gregale projects environments gitops promotion-pr shop production --from staging --target-commit COMMIT_SHA --expected-plan-hash PLAN_HASH --yes
```

##### projects environments gitops releases

List retained GitOps serving releases and current rollback eligibility

`gregale projects environments gitops releases --target-commit <SHA> [--before <UUID>] [--json] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--target-commit <SHA>` | Immutable commit containing the target manifest | required |
| `--before <UUID>` | Next page cursor from the previous listing |  |
| `--json` | Emit the read-only recovery catalog as JSON |  |

Examples:

```sh
gregale projects environments gitops releases shop production --target-commit COMMIT_SHA
```

##### projects environments gitops rollback-plan

Preview a code rollback and export it as a Git proposal

`gregale projects environments gitops rollback-plan --to-release <UUID> --target-commit <SHA> [--format <FORMAT>] [--expected-plan-hash <HASH>] [--manifest <PATH>] [--fail-on-blocked] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--to-release <UUID>` | Retained GitOps serving release in this environment | required |
| `--target-commit <SHA>` | Immutable commit containing the target manifest | required |
| `--format <FORMAT>` | json (default), markdown, patch or manifest |  |
| `--expected-plan-hash <HASH>` | Reviewed observation hash; required for patch or manifest export |  |
| `--manifest <PATH>` | Local target YAML/JSON file; required only for manifest export |  |
| `--fail-on-blocked` | Exit 2 after printing a blocked plan |  |

Examples:

```sh
gregale projects environments gitops rollback-plan shop production --to-release RELEASE_UUID --target-commit COMMIT_SHA
```

Rolls back to a retained serving release.

##### projects environments gitops rollback-pr

Open a draft recovery PR or MR for a selected served release

`gregale projects environments gitops rollback-pr --to-release <UUID> --target-commit <SHA> --expected-plan-hash <HASH> [--yes] [--json] <project> <environment>`

| Flag | Meaning | |
|---|---|---|
| `--to-release <UUID>` | Explicit retained serving release in this environment | required |
| `--target-commit <SHA>` | Reviewed current target branch commit | required |
| `--expected-plan-hash <HASH>` | Reviewed rollback observation hash | required |
| `--yes` | Create the draft recovery proposal; default is a read-only preview |  |
| `--json` | Emit the recovery proposal receipt as JSON |  |

Examples:

```sh
gregale projects environments gitops rollback-pr shop production --to-release RELEASE_UUID --target-commit COMMIT_SHA --expected-plan-hash PLAN_HASH --yes
```

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

Promote workloads or choose environments interactively

`gregale projects environments promote [--interactive] [--from <ENV>] [--to <ENV>] [--sync-config] [--verify] [--migrate] [--workers] [--release-secrets-file <PATH>] [--release-secrets-request-id <ID>] [--override-freeze <REASON>] [--yes] [--idempotency-key <KEY>] [--wait] [--progress] [--timeout <SECONDS|DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose source and destination, review changes, confirm, and follow progress |  |
| `--from <ENV>` | source environment (required unless interactive) |  |
| `--to <ENV>` | target environment (required unless interactive) |  |
| `--sync-config` | copy source non-secret environment configuration to the target |  |
| `--verify` | require destination HTTP health (requires --sync-config) |  |
| `--migrate` | run one backward-compatible destination release command |  |
| `--workers` | promote production push workers together |  |
| `--release-secrets-file <PATH>` | complete production secret snapshots by workload (requires --sync-config --verify --yes) |  |
| `--release-secrets-request-id <ID>` | replay the printed encrypted staging request |  |
| `--override-freeze <REASON>` | promote into production during a deploy freeze; the reason is audited |  |
| `--yes` | confirm the promotion |  |
| `--idempotency-key <KEY>` | stable key for retrying this promotion |  |
| `--wait` | wait for the promotion to reach a terminal status |  |
| `--progress` | print promotion transitions while waiting (human output only) |  |
| `--timeout <SECONDS|DURATION>` | maximum wait for promotion completion (seconds, or a duration such as 10m) |  |

Examples:

```sh
gregale projects environments promote my-project --interactive
```

#### projects environments status

Inspect a promotion

`gregale projects environments status`

#### projects environments rollback

Roll back a promotion

`gregale projects environments rollback`

#### projects environments abort

Abort a running promotion

`gregale projects environments abort [--to <ENV>] [--generation <N>] [--request-id <ID>] [--wait] [--timeout <SECONDS|DURATION>] <project-slug> <promotion-id>`

| Flag | Meaning | |
|---|---|---|
| `--to <ENV>` | destination environment |  |
| `--generation <N>` | inspected promotion health generation |  |
| `--request-id <ID>` | stable 32-character lowercase hexadecimal request ID |  |
| `--wait` | wait for this decision to finish |  |
| `--timeout <SECONDS|DURATION>` | maximum local wait; server work continues |  |

After cutover, qualify the baseline before restoration.

#### projects environments recover

Retry a blocked restoration without rebuilding or extending retention

`gregale projects environments recover [--to <ENV>] [--generation <N>] [--request-id <ID>] [--wait] [--timeout <SECONDS|DURATION>] <project-slug> <promotion-id>`

| Flag | Meaning | |
|---|---|---|
| `--to <ENV>` | destination environment |  |
| `--generation <N>` | inspected promotion health generation |  |
| `--request-id <ID>` | stable 32-character lowercase hexadecimal request ID |  |
| `--wait` | wait for this decision to finish |  |
| `--timeout <SECONDS|DURATION>` | maximum local wait; server work continues |  |

#### projects environments recover-migration

Resume or resolve a promotion stopped by its migration task

`gregale projects environments recover-migration --to <ENV> --action <ACTION> --task-id <TASK> --generation <N> --database-state-checked --evidence-sha256 <HASH> [--schema-compatible] [--replacement-promotion <ID>] [--request-id <ID>] [--wait] [--timeout <SECONDS>] <project-slug> <promotion-id>`

| Flag | Meaning | |
|---|---|---|
| `--to <ENV>` | destination environment | required |
| `--action <ACTION>` | resume, or resolve through a verified repair promotion | required; one of `resume` · `resolve` |
| `--task-id <TASK>` | original migration task ID | required |
| `--generation <N>` | inspected promotion health generation | required |
| `--database-state-checked` | attest that the destination database was inspected | required |
| `--evidence-sha256 <HASH>` | SHA-256 of private inspection evidence (not uploaded) | required |
| `--schema-compatible` | attest that the expected expansion is present (resume only) |  |
| `--replacement-promotion <ID>` | verified repair promotion (resolve only) |  |
| `--request-id <ID>` | stable request ID (default: derived from the request) |  |
| `--wait` | wait for recovery confirmation |  |
| `--timeout <SECONDS>` | maximum wait in seconds or a duration |  |

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

Show how a source tree splits into deployable workloads

`gregale scan [<dir>] [--tarball <PATH>] [--path <DIR>] [--repo <OWNER/NAME>] [--repository <OWNER/NAME>] [--install-id <N>] [--production-branch <BRANCH>] [--project-slug <SLUG>] [--environment <SLUG>] [--exclude <SLUGS>] [--show-affected] [--explain] [--persist-exclude]`

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

Set, rotate and audit encrypted secrets for apps and environments

See also: [`env`](#env).

`gregale secrets [<subcommand>]`

### secrets requirements

Manage local required secret names without reading or changing values

#### secrets requirements check

Compare required names with a dotenv example offline

`gregale secrets requirements check [--from-env-file <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--from-env-file <PATH>` | dotenv example (default: .env.example at the requirements project root); compare names only |  |

Examples:

```sh
gregale secrets requirements check
gregale secrets requirements check --from-env-file config/example.env
```

Exits 1 on differences or a missing declaration.

#### secrets requirements set

Replace the project declaration with validated, deduplicated names

`gregale secrets requirements set [--dry-run] [--from-env-file <PATH>] [<KEY>...]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview current/proposed names and differences without writing |  |
| `--from-env-file <PATH>` | import key names from a local dotenv file; excludes positional names, never stores values |  |

Examples:

```sh
gregale secrets requirements set DATABASE_URL API_KEY --dry-run
gregale secrets requirements set --from-env-file .env.example --dry-run
```

#### secrets requirements add

Append required names, skipping duplicates

`gregale secrets requirements add [--dry-run] [--from-env-file <PATH>] [<KEY>...]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview added names without writing |  |
| `--from-env-file <PATH>` | merge key names from a local dotenv file; excludes positional names, never stores values |  |

Examples:

```sh
gregale secrets requirements add REDIS_URL --dry-run
gregale secrets requirements add --from-env-file .env.example --dry-run
```

#### secrets requirements remove

Remove declared names; unknown names fail without changes

`gregale secrets requirements remove [--dry-run] <KEY>...`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview removed names and explicit-empty outcome without writing |  |

Examples:

```sh
gregale secrets requirements remove OLD_API_KEY --dry-run
```

#### secrets requirements show

Show required names, declaration status, and source path

`gregale secrets requirements show`

#### secrets requirements clear

Remove the local declaration of required names

`gregale secrets requirements clear`

Without one, completeness is unknown.

### secrets defaults

Show or manage local saved secret comparison scopes

Examples:

```sh
gregale secrets defaults
gregale secrets defaults --help
```

#### secrets defaults set

Replace saved comparison scope names

`gregale secrets defaults set [--environments <SCOPE,...>]`

| Flag | Meaning | |
|---|---|---|
| `--environments <SCOPE,...>` | comma-separated scope names; 1–10 distinct names |  |

#### secrets defaults show

Show saved scopes and their source file

`gregale secrets defaults show`

#### secrets defaults clear

Remove only the local comparison defaults

`gregale secrets defaults clear`

### secrets check

Check required secrets and delivery evidence without values

`gregale secrets check [--app <slug>] [--scope <SCOPE>] [--required <KEY>]... [--required-file <PATH>] [--missing-template <PATH>] [--environments <SCOPE,...|saved>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked app) |  |
| `--scope <SCOPE>` | single environment scope (defaults to linked environment) |  |
| `--required <KEY>` | required secret key name (repeatable) |  |
| `--required-file <PATH>` | explicit JSON array of required keys; overrides project discovery |  |
| `--missing-template <PATH>` | create a new private dotenv file containing only missing required names with empty values |  |
| `--environments <SCOPE,...|saved>` | compare required names across explicit scopes or saved local scopes; excludes scope/template flags |  |

Examples:

```sh
gregale secrets check
gregale secrets check --app my-api --required DATABASE_URL
gregale secrets check --required-file .gregale/required-secrets.json
```

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

Suppress a secret destination and keep the sealed source

`gregale secrets refs unset --app <slug> --environment <ENV> <KEY>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--environment <ENV>` | registered project environment | required |

Examples:

```sh
gregale secrets refs unset --app my-api --environment production DATABASE_URL
```

Applies to a primary workload destination.

### secrets list

List sealed secrets

`gregale secrets list [--key <KEY>]... [--watch] [--interval <DURATION>] [--timeout <DURATION>] [--delivery-status <STATUS>] [--app <slug>] [--scope <SCOPE|__all__>] [--class <CLASS>] [--older-than <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--key <KEY>` | include an exact secret key name (repeatable) |  |
| `--watch` | follow metadata changes; JSON emits snapshot/change/unavailable events |  |
| `--interval <DURATION>` | watch poll interval (1s to 1h; default 5s) |  |
| `--timeout <DURATION>` | watch duration (zero until interrupted); expiry exits 124 |  |
| `--delivery-status <STATUS>` | filter runtime-start delivery; unknown includes missing or inconsistent current-version evidence | one of `delivered` · `pending` · `failed` · `unknown` |
| `--app <slug>` | app slug (defaults to linked project app) |  |
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

Set a sealed secret

`gregale secrets set [--wait-for-ack] [--timeout <DURATION>] [--dry-run] [--app <slug>] [--interactive] [--from-stdin] [--file <PATH>] [--scope <SCOPE>] [--class <CLASS>] [--restart] [--no-guard] [<slug>] [<KEY=VALUE>...]`

| Flag | Meaning | |
|---|---|---|
| `--wait-for-ack` | wait for application acknowledgements for all updated keys |  |
| `--timeout <DURATION>` | restart and acknowledgement wait limit after saving (default 2m; requires --wait-for-ack) |  |
| `--dry-run` | validate local input and preview keys and runtime options without API requests or changes |  |
| `--app <slug>` | app slug (defaults to linked project app) |  |
| `--interactive` | enter hidden values and confirm names, scope, and retention before saving |  |
| `--from-stdin` | read KEY=VALUE pairs from stdin |  |
| `--file <PATH>` | read a private KEY=VALUE file; excludes stdin, interactive input, and positional pairs |  |
| `--scope <SCOPE>` | env scope to write (defaults to linked project environment) |  |
| `--class <CLASS>` | retention: persistent by default; ephemeral values make every wake a slower cold start; omission keeps an existing class | one of `persistent` · `ephemeral` |
| `--restart` | restart the app and apply updated secrets now |  |
| `--no-guard` | do not open a config guard for this change |  |

Examples:

```sh
gregale secrets set --file secrets.env --dry-run
gregale secrets set --app my-api --interactive
gregale secrets set --app my-api DATABASE_URL="$DATABASE_URL"
printf '%s\n' "DATABASE_URL=$DATABASE_URL" | gregale secrets set --app my-api --from-stdin
gregale secrets set --app my-api DATABASE_URL="$DATABASE_URL" --restart
gregale secrets set --app my-api SESSION_TOKEN="$SESSION_TOKEN" --class ephemeral
```

### secrets unset

Remove named sealed secrets (alias: rm)

`gregale secrets unset [--dry-run] [--interactive] [--app <slug>] [--scope <SCOPE>] [--restart] [--wait-for-ack] [--timeout <DURATION>] [--no-guard] [<KEY>...]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | validate local input and preview keys and runtime options without API requests or changes |  |
| `--interactive` | choose a secret name, review scope and runtime options, and confirm removal |  |
| `--app <slug>` | app slug (defaults to linked project app) |  |
| `--scope <SCOPE>` | env scope to delete from (defaults to linked project environment) |  |
| `--restart` | restart the app so running instances drop the removed secret now |  |
| `--wait-for-ack` | wait until every active authorized runtime confirms it removed the secret |  |
| `--timeout <DURATION>` | maximum time to wait for runtime acknowledgements (requires --wait-for-ack) |  |
| `--no-guard` | do not open a config guard for this change |  |

Examples:

```sh
gregale secrets unset OLD_TOKEN UNUSED_KEY --dry-run
gregale secrets unset --app my-api --interactive
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

Audit secret update age without exposing values

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

Reports unknown timestamps too.

### secrets rotate

Rotate a secret and optionally wait for runtime application

`gregale secrets rotate [--dry-run] [--app <slug>] [--from-stdin] [--scope <SCOPE>] [--restart] [--wait-for-ack] [--timeout <DURATION>] [<KEY=VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | validate local input and preview keys and runtime options without API requests or changes |  |
| `--app <slug>` | app slug (defaults to linked project app) |  |
| `--from-stdin` | read one KEY=VALUE pair from stdin |  |
| `--scope <SCOPE>` | env scope to rotate (defaults to linked project environment) |  |
| `--restart` | restart the app and apply the rotated secret now |  |
| `--wait-for-ack` | wait until every active authorized runtime confirms it applied the secret; needs com.gregale.secret-reload-signal, even with --restart |  |
| `--timeout <DURATION>` | maximum time to wait for application acknowledgements (requires --wait-for-ack) |  |

Examples:

```sh
printf '%s\n' "DATABASE_URL=$DATABASE_URL" | gregale secrets rotate --app my-api --from-stdin --restart --wait-for-ack
printf '%s\n' "DATABASE_URL=$DATABASE_URL" | gregale secrets rotate --app my-api --from-stdin --scope production --restart --wait-for-ack --timeout 5m
```


## slo

Show an app&#39;s SLO panel for a time window

See also: [`slos`](#slos).

`gregale slo [<slug>] [--window <WINDOW>]`

| Flag | Meaning | |
|---|---|---|
| `--window <WINDOW>` | window (1h\|24h\|7d) | one of `1h` · `24h` · `7d` |


## status

Show Gregale platform status (not specific to your account)

`gregale status`


## tail

Watch invocations finish across your account in real time

See also: [`logs`](#logs).

`gregale tail [<slug>] [--app <slug>] [--include-stateless]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | filter to a single app slug (optional) |  |
| `--include-stateless` | also print stateless.advisory frames (default: hide) |  |


## trusted-publishers

Manage public keys trusted to sign an app&#39;s images

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

Show this month&#39;s usage, or daily, storage and summary views

See also: [`plan`](#plan), [`billing`](#billing), [`invoices`](#invoices), [`overage-cap`](#overage-cap).

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

Show or change non-secret local CLI settings

`gregale config [<subcommand>]`

Examples:

```sh
gregale config
gregale config list
gregale config --help
```

### config get

Show one effective setting

`gregale config get <api-base|json>`

### config set

Persist one non-secret setting

`gregale config set <api-base|json> <value>`

### config list

Show all effective settings


## wake-timeline

Show the step-by-step timeline of one app wake

`gregale wake-timeline [<slug>] <wake-id> [--app <SLUG>] [--since <RFC3339>] [--limit <N>] [--all] [--verbose]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (alternative to the leading slug positional) |  |
| `--since <RFC3339>` | RFC3339 timestamp |  |
| `--limit <N>` | page size (1..1000) |  |
| `--all` | walk every page |  |
| `--verbose` | show detailed restore phases in human output |  |


## throttle-suggestions

Suggest per-route rate limits and preview their effect

`gregale throttle-suggestions <slug> [--range <WINDOW>] [--dry-run] [--candidate-rps <N>] [--candidate-burst <N>]`

| Flag | Meaning | |
|---|---|---|
| `--range <WINDOW>` | observation window (5m\|15m\|1h\|6h\|24h\|7d\|15d) | one of `5m` · `15m` · `1h` · `6h` · `24h` · `7d` · `15d` |
| `--dry-run` | enable the dry-run preview pass (requires --candidate-rps) |  |
| `--candidate-rps <N>` | candidate rate-limit rps for the dry-run preview |  |
| `--candidate-burst <N>` | candidate burst for the dry-run preview |  |


## wake

Wake a parked app before traffic arrives

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


## wake-ahead

Wake the services an app calls while the app itself wakes

`gregale wake-ahead <slug> [on|off]`

Examples:

```sh
gregale wake-ahead my-api
gregale wake-ahead my-api on
gregale wake-ahead my-api off --json
```


## traffic

Split traffic between an app&#39;s deployments

`gregale traffic [<subcommand>]`

### traffic set

Set the traffic split for a deployment

`gregale traffic set [--app <SLUG>] --deployment <ID> --percent <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--deployment <ID>` | deployment id or vN revision to set the traffic split on | required |
| `--percent <N>` | traffic weight in [0, 100]; -1 = unset (server default 100) | required |

Examples:

```sh
gregale traffic set my-api --deployment v7 --percent 30
gregale traffic set --app my-api --deployment v7 --percent 30
```

### traffic promote

Promote a live deployment to 100% production traffic

`gregale traffic promote [--app <SLUG>] --deployment <ID> [--at <WHEN>] [--release-notify <CHANNEL,...>] [--if-serving <ID>] [--require-bindings] [--max-verification-age <DURATION>] [--allow-unsupported] [--require-application-ack] [--route-removal-mode <MODE>] [--route-readiness <PATH>] [--route-mapping <PATH>] [--route-owner-approval <PATH>] [--route-evidence-max-age <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--deployment <ID>` | deployment id or vN revision to promote | required |
| `--at <WHEN>` | start the release&#39;s rollout at this time instead of now: +2h, 09:00, &#34;2026-10-22 09:00&#34; (local) or RFC 3339 |  |
| `--release-notify <CHANNEL,...>` | Slack or email channels told when the scheduled release starts or cannot start (with --at) |  |
| `--if-serving <ID>` | require this deployment id or vN revision to remain at 100% traffic |  |
| `--require-bindings` | enforce a bindings check at the server&#39;s traffic write |  |
| `--max-verification-age <DURATION>` | maximum probe age (default 10m); requires --require-bindings |  |
| `--allow-unsupported` | waive unsupported queue/outbound probes; requires --require-bindings |  |
| `--require-application-ack` | require current application acknowledgements; requires --require-bindings |  |
| `--route-removal-mode <MODE>` | opt-in CLI gate: report or enforce; requires --app and --if-serving | one of `report` · `enforce` |
| `--route-readiness <PATH>` | readiness JSON for the serving deployment |  |
| `--route-mapping <PATH>` | reviewed successor mapping JSON |  |
| `--route-owner-approval <PATH>` | owner attestation bound to this exact change |  |
| `--route-evidence-max-age <DURATION>` | maximum evidence age (default and maximum 72h) |  |

Examples:

```sh
gregale traffic promote my-api --deployment v7
gregale traffic promote --app my-api --deployment v7 --if-serving v6
gregale traffic promote --app my-api --deployment v7 --at 09:00
```

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
| `--auth-header-env <ENV>` | environment variable holding the Authorization value (&#39;Bearer …&#39;) or one &#39;Name: value&#39; header |  |
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
| `--auth-header-env <ENV>` | environment variable holding the new Authorization value (&#39;Bearer …&#39;) or one &#39;Name: value&#39; header |  |
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

Mirror live traffic to a deployment and replay sanitized requests

`gregale mirror [<subcommand>]`

Pro and Scale plans only. Rules default to 5% and mirror only safe methods; bodies over 64 KiB are skipped, and raw bodies are never retained.

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

Cache an app&#39;s responses at the edge, or purge them

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

`gregale cache purge [--app <SLUG>] [--path <GLOB>] [--tag <TAG>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (defaults to linked project context) |  |
| `--path <GLOB>` | optional normalized request path glob |  |
| `--tag <TAG>` | optional cache tag |  |


## upload-cache

Inspect or clear saved state for resumable source uploads

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

Manage release webhooks for apps and your account

`gregale webhooks [<subcommand>]`

### webhooks list

List an app&#39;s webhooks

`gregale webhooks list [--app <slug>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (alternative to the positional) |  |

The app defaults to the linked one.

### webhooks add

Add a webhook

`gregale webhooks add --app <slug> --target-url <URL> [--event <EVENT>] [--retry-policy <POLICY>] [--delivery-format <FORMAT>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--target-url <URL>` | http(s) target URL (https recommended: deliveries are signed, not encrypted) | required |
| `--event <EVENT>` | event to deliver (repeat) |  |
| `--retry-policy <POLICY>` | default\|aggressive\|none |  |
| `--delivery-format <FORMAT>` | json\|cloudevents |  |

### webhooks info

Show one webhook

`gregale webhooks info --app <slug> <webhook-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### webhooks update

Update one webhook

`gregale webhooks update --app <slug> [--target-url <URL>] [--retry-policy <POLICY>] [--delivery-format <FORMAT>] [--enable] [--disable] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--target-url <URL>` | new http(s) target URL |  |
| `--retry-policy <POLICY>` | new retry policy | one of `default` · `aggressive` · `none` |
| `--delivery-format <FORMAT>` | new delivery format | one of `json` · `cloudevents` |
| `--enable` | enable the webhook |  |
| `--disable` | disable the webhook |  |

### webhooks rm

Delete one webhook

`gregale webhooks rm --app <slug> [--yes] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--yes` | accepted for consistency; rm does not prompt |  |

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

`gregale webhooks retry --app <slug> <webhook-id> <delivery-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

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
| `--event <EVENT>` | distinct release event (repeatable) | required; one of `deployment.live` · `deployment.failed` · `rollout.completed` · `rollout.aborted` · `environment.gitops.changed` |
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
| `--event <EVENT>` | replacement release event filter (repeatable) | one of `deployment.live` · `deployment.failed` · `rollout.completed` · `rollout.aborted` · `environment.gitops.changed` |
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

Replace the signing secret

`gregale webhooks account rotate-secret [--secret <VALUE>] [--from-stdin] <id>`

| Flag | Meaning | |
|---|---|---|
| `--secret <VALUE>` | signing secret (prefer --from-stdin) |  |
| `--from-stdin` | read the signing secret from stdin; mutually exclusive with --secret |  |

Read it with --from-stdin, or pass --secret.


## whoami

Show the authenticated account

`gregale whoami`


## completion

Generate shell completion scripts or install them interactively

`gregale completion [<subcommand>]`

### completion install

Choose a shell, review files, and confirm completion setup

`gregale completion install --interactive`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | guide shell completion installation | required |

Examples:

```sh
gregale completion install --interactive
```

### completion bash

Print the bash completion script

### completion zsh

Print the zsh completion script

### completion fish

Print the fish completion script

### completion powershell

Print the powershell completion snippet


## man

Print the CLI manual page, or one command&#39;s page

`gregale man <command>`
