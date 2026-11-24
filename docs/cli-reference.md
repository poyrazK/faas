# gregale CLI reference

Generated from the CLI's command manifest by `gregale man --markdown`. Do not edit by hand.

Local help: `gregale deploy --help` and `gregale app --help` show common tasks. Add `--all` to see every option grouped by purpose, or use `gregale help deploy --all`. This reference always lists every option.

App target: a command that takes `--app` and no other arguments also accepts the app as its first argument (`gregale alert list my-api` is `gregale alert list --app my-api`), and `app` verbs take the app after the verb (`gregale app scale my-api`), from `--app`, or from the linked checkout.

Earlier names: commands keep working under the names they had before the command tree (`gregale crons list` is `gregale cron list`); `gregale help --all --operator` lists them, and [The CLI command tree](cli-command-tree.md) maps each one.

Automation: put `--non-interactive` before the command to disable prompts and browser launches; use `--json` for structured output. Required confirmations must be supplied explicitly. With an agent key, put `--wait-for-approval` before the command to wait for a person to decide a request that needs approval, then retry it once, and `--agent-reason TEXT` (or `GREGALE_AGENT_REASON`) to tell that person what the request is for. Company projects: `gregale --org <slug> <command>` sends project commands to that organization's projects; command-local `--org` options keep their meaning. Connection selection: `gregale --profile <name> <command>`. Put this option before the command; command-local `--profile` options retain their documented meaning. Put `--debug-http` before the command for metadata-only HTTP diagnostics on stderr. Put `--http-timeout 90s` before the command to select a positive API request timeout; streams use it only while waiting for response headers. See [CLI configuration](cli-config.md) for connection profiles, environment precedence, HTTP timeouts and diagnostics.

API errors: when the server supplies a valid correlation header, text errors show `Request ID: <id>` and JSON errors include optional `request_id`. Include it when reporting a failure; [CLI configuration](cli-config.md#request-ids-on-api-errors) explains validation and retry behavior.

Read retries: put `--http-retries 3` before the command for up to three additional attempts on selected app, deployment and billing JSON reads. The default is zero; see [CLI configuration](cli-config.md#read-request-retries) for eligible requests, backoff and cancellation.

| Command | What it does |
|---|---|
| [`start`](#start) | Get your first app live with a few guided prompts |
| [`menu`](#menu) | Choose a common task and open its guided command |
| [`init`](#init) | Scaffold a project from a built-in template |
| [`deploy`](#deploy) | Deploy an app, function, or project |
| [`logs`](#logs) | Search an app&#39;s runtime logs and HTTP requests, or follow them |
| [`status`](#status) | Show what this checkout targets, or Gregale&#39;s platform status |
| [`open`](#open) | Open an app&#39;s URL in your browser |
| [`rollback`](#rollback) | Restore a previous deployment, or check an exact historical rollback |
| [`add`](#add) | Provision and bind managed resources to an app |
| [`link`](#link) | Link this checkout to a Gregale project |
| [`unlink`](#unlink) | Remove the linked project from this checkout |
| [`login`](#login) | Authenticate this machine |
| [`logout`](#logout) | Revoke the managed CLI session and remove the stored token |
| [`signup`](#signup) | Create a new account |
| [`whoami`](#whoami) | Show the authenticated account |
| [`completion`](#completion) | Generate shell completion scripts or install them interactively |
| [`version`](#version) | Print the CLI version |
| [`update`](#update) | Update the CLI to the newest release |
| [`man`](#man) | Print the CLI manual page, or one command&#39;s page |
| [`guide`](#guide) | Task pages, or search commands and docs for a task in your own words |
| [`release`](#release) | Inspect releases and control rollouts: canary, traffic, approvals |
| [`preview`](#preview) | Manage pull-request preview environments |
| [`project`](#project) | Inspect and recover repository projects |
| [`build`](#build) | Inspect builds, their provenance and SBOMs |
| [`github`](#github) | Connect GitHub and bind apps to repositories |
| [`registry`](#registry) | Manage registry credentials, signing keys and published images |
| [`app`](#app) | Inspect, change, scale and operate apps |
| [`invocation`](#invocation) | Run apps and functions, inspect calls and wait for results |
| [`function`](#function) | Generate typed clients for calls between a project&#39;s functions |
| [`env`](#env) | Set, list and sync an app&#39;s sealed variables |
| [`domain`](#domain) | Manage custom domains |
| [`edge`](#edge) | Manage edge rules, rule lists, caching and CORS |
| [`postgres`](#postgres) | Create, attach and restore managed PostgreSQL databases |
| [`bucket`](#bucket) | Manage object storage buckets |
| [`export`](#export) | Verify and compare saved JSON Lines exports offline |
| [`realtime`](#realtime) | Manage realtime endpoints, channels and connections |
| [`cron`](#cron) | Schedule requests and commands |
| [`job`](#job) | Manage jobs that run to completion |
| [`workflow`](#workflow) | Manage durable workflows |
| [`webhook`](#webhook) | Register webhooks and deliver events to them |
| [`metric`](#metric) | Show app metrics, request breakdowns and the service map |
| [`debug`](#debug) | Inspect requests, traces, issues, profiles and regressions |
| [`audit`](#audit) | Search your account&#39;s audit log |
| [`alert`](#alert) | Manage alert rules, channels, incidents, SLOs and checks |
| [`mcp`](#mcp) | Connect agents, host MCP servers, or call MCP tools |
| [`account`](#account) | Manage your account, MFA and data exports |
| [`org`](#org) | Manage orgs, members and invitations |
| [`key`](#key) | Manage your account&#39;s API keys |
| [`billing`](#billing) | Manage plan, payment, usage, invoices and spend cap |
| [`feature`](#feature) | See Gregale features and join early access |
| [`config`](#config) | Manage local CLI settings, profiles and presets |

## start

Get your first app live with a few guided prompts

`gregale start [path]`

Examples:

```sh
gregale start
gregale start ./my-api
```


## menu

Choose a common task and open its guided command

`gregale menu`

Examples:

```sh
gregale menu
```


## init

Scaffold a project from a built-in template

`gregale init [--template <NAME>] [--path <DIR>] [--deploy] [--name <SLUG>] [--agents] [--dry-run] [--secrets-file <PATH>] [--list] [--no-mcp]`

--agents sets up an existing project for coding agents: it adds a Gregale section to AGENTS.md, makes an existing CLAUDE.md import AGENTS.md, and points Gemini CLI at AGENTS.md in a project with a .gemini folder. Codex, Cursor and Antigravity read AGENTS.md on their own. A second run replaces the section in place.

| Flag | Meaning | |
|---|---|---|
| `--template <NAME>` | template name (required unless --agents) | one of `hello-node` · `hello-python` · `hello-go` · `hello-node-private` · `cron-example` · `function-node` · `function-python` · `function-go` · `function-node24` · `function-python313` · `function-rust` · `event-worker` · `queue-worker` · `s3-uploader` · `object-storage-node` · `object-storage-python` · `slack-bot` · `rest-api-postgres` · `rest-api-postgres-python` · `cron-worker` · `webhook-receiver` · `ai-chat` · `secret-reload-node` · `customer-platform` · `mcp-node` · `mcp-node-tasks` · `mcp-go` · `mcp-go-tasks` · `mcp-python` · `mcp-python-tasks` · `customer-operation-export` · `customer-operation-job-export` · `customer-operation-workflow-export` · `data-api` · `data-api-starter` · `data-api-teams` · `data-api-support-console` |
| `--path <DIR>` | target directory (required unless --agents, which defaults to .) |  |
| `--deploy` | deploy after scaffolding |  |
| `--name <SLUG>` | app slug used with --deploy, or the app --agents deploys (default: the linked app, else the directory name) |  |
| `--agents` | set up an existing project for coding agents instead of scaffolding one |  |
| `--dry-run` | with --agents, show the changes without writing them |  |
| `--secrets-file <PATH>` | seal KEY=VALUE pairs before the first deployment (requires --deploy) |  |
| `--list` | list available templates |  |
| `--no-mcp` | do not write .mcp.json, which offers Gregale&#39;s MCP server to agents opened in the project |  |

Examples:

```sh
gregale init --list
gregale init --template hello-node --path ./my-api
gregale init --agents --name my-api
```


## deploy

Deploy an app, function, or project

`gregale deploy [<subcommand>] [--atomic] [--add-services] [--verify] [--workers] [--stabilize] [--progressive] [--worker-health] [--migrate] [--release-secrets-file <PATH>] [--release-secrets-request-id <ID>] [--config-file <PATH>] [--image <REF>] [--tarball <PATH>] [--path <DIR>] [--source <auto|head|worktree>] [--worktree] [--repo <OWNER/NAME>] [--repository <OWNER/NAME>] [--install-id <N>] [--production-branch <BRANCH>] [--ref <REF>] [--source-branch <BRANCH>] [--github] [--pinned-sha <SHA>] [--pin-action] [--template <NAME>] [--dockerfile] [--issue-source-maps <PATH>] [--issue-source-map-directory <PATH>] [--runtime <RUNTIME>] [--handler <HANDLER>] [--name <SLUG>] [--size <SIZE>] [--profile <SIZE>] [--vcpu <N>] [--execution-mode <MODE>] [--restart-policy <POLICY>] [--startup-deadline-s <SECONDS>] [--max-retries <N>] [--function] [--app] [--only <SLUGS>] [--project] [--env <SLUG>] [--reason <text>] [--tag <TAG>] [--deployed-by <NAME>] [--pr-number <N>] [--exclude <SLUGS>] [--show-affected] [--persist-exclude] [--project-slug <SLUG>] [--prefix <PREFIX>] [--rollout <MODE>] [--override-freeze <REASON>] [--at <WHEN>] [--release-notify <CHANNEL,...>] [--canary-preset <PRESET>] [--canary-stages <STAGES>] [--safe] [--require-authn] [--no-require-authn] [--platform-tenant-required] [--no-platform-tenant-required] [--app-protocol <PROTOCOL>] [--traffic-percent <PERCENT>] [--no-traffic] [--rollback-on-5xx] [--queue-canary-binding <NAME>]... [--healthcheck-path <PATH>] [--readiness-path <PATH>] [--healthcheck-grpc] [--healthcheck-grpc-service <SERVICE>] [--disable-startup-cpu-boost] [--mcp-task-migration-override] [--no-triggers] [--wait] [--no-wait] [--create-only] [--timeout <DURATION>] [--idempotency-key <KEY>] [--resume-upload-id <ID>] [--secrets-file <PATH>] [--secret-scan <on|off>] [--diff] [--preview] [--plan] [--dry-run] [--explain-files] [--yes] [--strict] [--lenient] [--server-diff] [--doctor-strict] [--no-doctor]`

Source defaults to committed HEAD when origin exists; otherwise local files. Use --source=worktree to include local changes.

RAM/CPU: use --size SIZE (--profile SIZE after the command is deprecated). Connection: use --profile NAME before the command.

Projects: --project (every workload), --only SLUGS. Traffic: --rollout MODE.

Retry a project&#39;s failed workloads: gregale release attempt retry SESSION_ID

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
| `--template <NAME>` | scaffold from a built-in template | one of `hello-node` · `hello-python` · `hello-go` · `hello-node-private` · `cron-example` · `function-node` · `function-python` · `function-go` · `function-node24` · `function-python313` · `function-rust` · `event-worker` · `queue-worker` · `s3-uploader` · `object-storage-node` · `object-storage-python` · `slack-bot` · `rest-api-postgres` · `rest-api-postgres-python` · `cron-worker` · `webhook-receiver` · `ai-chat` · `secret-reload-node` · `customer-platform` · `mcp-node` · `mcp-node-tasks` · `mcp-go` · `mcp-go-tasks` · `mcp-python` · `mcp-python-tasks` · `customer-operation-export` · `customer-operation-job-export` · `customer-operation-workflow-export` · `data-api` · `data-api-starter` · `data-api-teams` · `data-api-support-console` |
| `--dockerfile` | build with the supplied Dockerfile inside --tarball |  |
| `--issue-source-maps <PATH>` | publish a portable manifest from the selected source; requires zero production traffic |  |
| `--issue-source-map-directory <PATH>` | deployed bundle directory corresponding to the manifest directory |  |
| `--runtime <RUNTIME>` | function runtime | one of `node22` · `python312` · `go124` · `go124-alpine` · `node24` · `python313` · `binary` |
| `--handler <HANDLER>` | function handler |  |
| `--name <SLUG>` | app slug (default: source directory name) |  |
| `--size <SIZE>` | RAM/CPU size for one app; excludes --repo and project deploys | one of `micro` · `small` · `medium` · `large` · `xlarge` |
| `--profile <SIZE>` | deprecated: use --size |  |
| `--vcpu <N>` | assert the guest vCPU count, one per GB of RAM (omit to use the size&#39;s) |  |
| `--execution-mode <MODE>` | app lifecycle mode | one of `request` · `service` · `worker` · `job` |
| `--restart-policy <POLICY>` | app restart policy | one of `no` · `on-failure` · `always` · `unless-stopped` |
| `--startup-deadline-s <SECONDS>` | maximum startup seconds (0 uses plan default) |  |
| `--max-retries <N>` | maximum restart attempts (0 uses plan default) |  |
| `--function` | deploy as a function; skip shape auto-detection |  |
| `--app` | deploy as an app; skip shape auto-detection |  |
| `--only <SLUGS>` | workloads to apply; retain unselected project workloads (comma-separated) |  |
| `--project` | deploy all detected workloads as one project (slug defaults from --name or source) |  |
| `--env <SLUG>` | deploy to a registered project environment (defaults to linked context) (also --environment) |  |
| `--reason <text>` | free-text deploy reason (≤280 chars) |  |
| `--tag <TAG>` | deployment annotation tag | one of `incident_recovery` · `hotfix` · `scheduled_maintenance` · `compliance_hold` · `partner_request` |
| `--deployed-by <NAME>` | operator label (auto-resolved from git config user.name) |  |
| `--pr-number <N>` | GitHub PR number (positive int; 0 = absent). CI paths stamp via the GitHub Action. |  |
| `--exclude <SLUGS>` | omit workloads (comma-separated slugs; cannot combine with --only) |  |
| `--show-affected` | show workloads that deploy, stay unchanged, or are removed |  |
| `--persist-exclude` | save --exclude slugs for future project deploys |  |
| `--project-slug <SLUG>` | kebab slug for the project (one-key provision) |  |
| `--prefix <PREFIX>` | name new project apps &lt;prefix&gt;&lt;service&gt; (overrides x-gregale-slug-prefix) |  |
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
| `--readiness-path <PATH>` | steady-state readiness HTTP path; failing instances leave routing |  |
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
| `--resume-upload-id <ID>` | require this existing resumable upload on retry |  |
| `--secrets-file <PATH>` | seal KEY=VALUE pairs before the first deployment |  |
| `--secret-scan <on|off>` | scan .env files before packing (default: on) | one of `on` · `off` |
| `--diff` | alias of --dry-run |  |
| `--preview` | preview local app defaults; no login needed |  |
| `--plan` | alias of --preview |  |
| `--dry-run` | check against remote state without deploying |  |
| `--explain-files` | with --dry-run, inspect local payload files and exclusion rules without login or remote calls |  |
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

Check local source, an image or project prerequisites before deploying

`gregale deploy check [--project] [--image <REF>] [--registry-user <USER>] [--registry-password-stdin] [--strict] [--app <SLUG>] [--project-slug <SLUG>] [--path <DIR>] [--source <MODE>] [--worktree] [--env <ENV>] [--from <ENV>] [--expected-state <SHA256>] [--atomic] [--add-services] [--verify] [--workers] [--worker-health] [--migrate] [--progressive] [--stabilize] [--sync-config] [--config-file <PATH>] [--secrets-file <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--project` | check the complete project&#39;s release prerequisites |  |
| `--image <REF>` | without --project: inspect this Linux/amd64 image without downloading layers |  |
| `--registry-user <USER>` | without --project: registry username; requires --registry-password-stdin |  |
| `--registry-password-stdin` | without --project: read the registry password or token from stdin |  |
| `--strict` | without --project: exit 1 on warnings |  |
| `--app <SLUG>` | without --project: app whose env and secret names count as declared (defaults to linked context) |  |
| `--project-slug <SLUG>` | with --project: project slug (defaults to linked context or selected directory) |  |
| `--path <DIR>` | selected source directory |  |
| `--source <MODE>` | with --project: local source policy (default auto) | one of `auto` · `head` · `worktree` |
| `--worktree` | with --project: check local working tree changes |  |
| `--env <ENV>` | with --project: destination environment (default production) (also --environment) |  |
| `--from <ENV>` | with --project: source environment for a promotion check |  |
| `--expected-state <SHA256>` | with --project: compare a previous check&#39;s state hash |  |
| `--atomic` | with --project: check atomic source release prerequisites |  |
| `--add-services` | with --project: check HTTP service addition prerequisites |  |
| `--verify` | with --project: check critical health verification prerequisites |  |
| `--workers` | with --project: check push worker release prerequisites |  |
| `--worker-health` | with --project: require worker delivery health during deployment |  |
| `--migrate` | with --project: check one backward-compatible migration |  |
| `--progressive` | with --project: check progressive rollout prerequisites |  |
| `--stabilize` | with --project: check post-success monitoring prerequisites |  |
| `--sync-config` | with --project: check synchronized promotion configuration |  |
| `--config-file <PATH>` | with --project: proposed non-secret paired configuration |  |
| `--secrets-file <PATH>` | with --project: proposed secret input; only key names leave this machine |  |

Examples:

```sh
gregale deploy check
gregale deploy check --image ghcr.io/acme/api:1.2 --strict
gregale deploy check --project --json
gregale deploy check --project --atomic --verify
gregale deploy check --project --project-slug shop --from staging --env production --sync-config --verify
```

Without --project, checks local source or an image, as `gregale doctor` does; with --project, checks the project&#39;s release prerequisites against current state.

Flags marked &#34;with --project&#34; are refused without --project.

Without --project, a variable the source reads but .env, .gregale/env.json and the app&#39;s env and secrets do not declare is an error; when the app&#39;s env cannot be read (no linked app or --app, or logged out), it is a warning.


## logs

Search an app&#39;s runtime logs and HTTP requests, or follow them

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
| `--follow` | stream new runtime lines, or new HTTP requests with --source http, until interrupted |  |
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
| `--explain` | summarize failures and error patterns; --json requires an unfiltered --deployment snapshot |  |
| `--archive` | read durable logs for one instance and UTC day |  |
| `--instance <ID>` | instance id for --archive (1..128 ASCII letters, numbers, dots, underscores, or hyphens) |  |
| `--date <DAY>` | UTC day for --archive: today, yesterday, or YYYY-MM-DD |  |

Examples:

```sh
gregale logs my-api --interactive
gregale logs my-api --follow
gregale logs my-api --follow --source http
gregale logs --project shop --follow
gregale logs my-api --since 1h --level error
gregale logs views save runtime-errors --since 1h --level error
gregale logs my-api --view runtime-errors
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

### logs view

Save, list and reuse log filters, locally or shared with the team

Examples:

```sh
gregale logs view save errors --since 1h --level error
gregale logs view save errors --shared --level error --app my-api
gregale logs view list --shared --app my-api
gregale logs view use errors --default
gregale logs my-api --view errors
```

Local views are what `logs views` manages; --shared stores a view on the server as `logs saved` does, so teammates and agents see it. Each verb takes the flags of the command it runs.

#### logs view list

List local or shared views

`gregale logs view list`

#### logs view show

Show one local view and its commands

`gregale logs view show <name>`

#### logs view save

Save filters as a local or shared view

`gregale logs view save <name>`

#### logs view delete

Delete a local or shared view

`gregale logs view delete <name>`

#### logs view rename

Rename a local view

`gregale logs view rename <old-name> <new-name>`

#### logs view copy

Copy a local view under a new name

`gregale logs view copy <source-name> <new-name>`

#### logs view update

Change a local view&#39;s filters

`gregale logs view update <name>`

#### logs view use

Make a local view the project&#39;s default

`gregale logs view use --default <name>`

| Flag | Meaning | |
|---|---|---|
| `--default` | save it as the project-local preferred view | required |

### logs views

List or manage reusable local log filters

`gregale logs views [copy|list|show|rename|update|delete|save] [--name <TEXT>] [--source <SOURCE>] [--details] [--app <SLUG>]`

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

### logs drain

Ship app runtime logs to an HTTP JSON or OTLP endpoint

#### logs drain list

List an app&#39;s log drains

`gregale logs drain list [--app <SLUG>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug |  |

Examples:

```sh
gregale logs drain list --app my-app
```

#### logs drain create

Add a log drain; the credential is read from an environment variable

`gregale logs drain create [--app <SLUG>] --url <URL> [--kind <KIND>] [--auth-header-env <ENV>] [--disabled] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug |  |
| `--url <URL>` | destination URL | required |
| `--kind <KIND>` | destination format (default http_json) | one of `http_json` · `otlp` |
| `--auth-header-env <ENV>` | environment variable holding the Authorization value (&#39;Bearer …&#39;) or one &#39;Name: value&#39; header |  |
| `--disabled` | create the drain disabled |  |

Examples:

```sh
LOG_TOKEN='Bearer …' gregale logs drain create --app my-app --url https://logs.example.com/ingest --auth-header-env LOG_TOKEN
```

#### logs drain get

Show one log drain (credential masked)

`gregale logs drain get [--app <SLUG>] --id <ID> [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug |  |
| `--id <ID>` | log drain id | required |

#### logs drain health

Show delivery health: queue, delivered, failed and last error

`gregale logs drain health [--app <SLUG>] --id <ID> [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug |  |
| `--id <ID>` | log drain id | required |

Examples:

```sh
gregale logs drain health --app my-app --id <drain-id>
```

#### logs drain update

Change a drain&#39;s URL or credential, or pause and resume it

`gregale logs drain update [--app <SLUG>] --id <ID> [--url <URL>] [--auth-header-env <ENV>] [--enable] [--disable] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug |  |
| `--id <ID>` | log drain id | required |
| `--url <URL>` | new destination URL |  |
| `--auth-header-env <ENV>` | environment variable holding the new Authorization value (&#39;Bearer …&#39;) or one &#39;Name: value&#39; header |  |
| `--enable` | resume delivery |  |
| `--disable` | pause delivery |  |

#### logs drain delete

Delete a log drain

`gregale logs drain delete [--app <SLUG>] --id <ID> [--yes] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug |  |
| `--id <ID>` | log drain id | required |
| `--yes` | confirm deletion without prompting |  |


## status

Show what this checkout targets, or Gregale&#39;s platform status

`gregale status [--app <SLUG>] [--platform]`

In a linked checkout (or with --app), shows the project, app, environment, state and account. Elsewhere, and with --platform, shows Gregale&#39;s platform status.

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | show this app instead of the linked one |  |
| `--platform` | show Gregale&#39;s platform status |  |

Examples:

```sh
gregale status
gregale status --app my-api
gregale status --platform
```


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

### open dashboard

Open the account dashboard in your browser

`gregale open dashboard [--stateless]`

| Flag | Meaning | |
|---|---|---|
| `--stateless` | open the stateless-advisory landing page instead of the account page |  |


## rollback

Restore a previous deployment, or check an exact historical rollback

`gregale rollback [<subcommand>] <slug> [--app <SLUG>] [--interactive] [--to <deployment_id|vN>] [--expected-current <deployment_id|vN>] [--reason <TEXT>] [--acknowledge-releases] [--restore-databases] [--wait] [--no-wait] [--timeout <duration>] [--poll-interval <duration>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional) |  |
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
gregale rollback --app my-api --to v41
gregale rollback my-api --to v41 --expected-current v42 --wait
gregale rollback my-api --to v41 --restore-databases
```

### rollback status

Read an exact rollback operation

`gregale rollback status [--operation <UUID>] [--wait] [--timeout <duration>] [--poll-interval <duration>] [--json] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--operation <UUID>` | accepted rollback operation UUID |  |
| `--wait` | wait for completion with a committed audit receipt |  |
| `--timeout <duration>` | wait deadline (default 10m) |  |
| `--poll-interval <duration>` | poll interval (default 2s) |  |
| `--json` | print the operation receipt |  |

Waiting never submits another rollback.

With a switch ID in place of --operation, it shows a rollback that restores the database (`gregale rollback status &lt;slug&gt; &lt;switch-id&gt;`).

### rollback switches

List rollbacks that restored or are restoring the database

`gregale rollback switches [--json] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--json` | machine-readable output |  |


## add

Provision and bind managed resources to an app

`gregale add [<subcommand>]`

### add function

Scaffold a function that declares its own trigger and settings

`gregale add function [--trigger <KIND>] [--lang <LANG>] [--schedule <CRON>] [--queue <NAME>] [--bucket <NAME>] [--provider <PROVIDER>] [--path <DIR>] [--dry-run] <name>`

| Flag | Meaning | |
|---|---|---|
| `--trigger <KIND>` | what calls the function (default http) | one of `http` · `cron` · `queue` · `bucket` · `webhook` |
| `--lang <LANG>` | handler language (default: inferred from the project, else node) | one of `node` · `python` |
| `--schedule <CRON>` | cron schedule for --trigger cron |  |
| `--queue <NAME>` | queue for --trigger queue (default: the function name) |  |
| `--bucket <NAME>` | bucket for --trigger bucket |  |
| `--provider <PROVIDER>` | webhook provider for --trigger webhook | one of `stripe` · `generic` |
| `--path <DIR>` | project directory |  |
| `--dry-run` | show the files without writing them |  |

Examples:

```sh
gregale add function digest --trigger cron --schedule "0 8 * * *"
gregale add function thumbs --trigger bucket --bucket uploads --lang python
gregale add function payments --trigger webhook --provider stripe
```

Writes functions/&lt;name&gt;/ with a handler whose config `gregale deploy` reads without running it, and a test beside it. Nothing is created remotely.

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
| `--access <MODE>` | credential access | one of `read_write` · `read_only` · `migration` · `data_api` · `session` |
| `--wait-timeout <DURATION>` | readiness timeout |  |

### add bucket

Provision or attach object storage and inject sealed S3 settings

`gregale add bucket --app <APP> [--env <SCOPE>] [--scope <SCOPE>] [--region <REGION>] [--public] [--serve-at <PATH>] [--permission <MODE>] [--label <LABEL>] [--prefix <PREFIX>] [--key-prefix <KEYS/>] [--wait-timeout <DURATION>] <name>`

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
| `--key-prefix <KEYS/>` | limit the binding to object keys under this prefix |  |
| `--wait-timeout <DURATION>` | readiness timeout |  |

### add ai

Set up the AI gateway for an app in one step

`gregale add ai --app <APP> --provider <PROVIDER> [--integration <ID>] [--name <NAME>] [--origin <URL>] [--region <REGION>] [--key-env <NAME>] [--key-stdin] [--key-file <PATH>] [--no-env] [--env <SCOPE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <APP>` | app slug | required |
| `--provider <PROVIDER>` | AI provider | required; one of `openai` · `anthropic` · `gemini` · `azure_openai` · `bedrock` · `vertex` |
| `--integration <ID>` | existing AI integration to bind instead of creating one |  |
| `--name <NAME>` | integration name (default the provider) |  |
| `--origin <URL>` | provider origin, for Azure OpenAI or an OpenAI-compatible API |  |
| `--region <REGION>` | provider region, for Bedrock and Vertex |  |
| `--key-env <NAME>` | environment variable holding the provider key (default the provider&#39;s usual one) |  |
| `--key-stdin` | read the provider key from stdin |  |
| `--key-file <PATH>` | file holding the provider key, such as a Vertex service-account key |  |
| `--no-env` | do not set the app&#39;s SDK settings |  |
| `--env <SCOPE>` | environment scope for the app&#39;s SDK settings (also --scope) |  |

Examples:

```sh
OPENAI_API_KEY=sk-... gregale add ai --app my-api --provider openai
gregale add ai --app my-api --provider vertex --region us-central1 --key-file key.json
gregale add ai --app my-api --provider azure_openai --origin https://my-resource.openai.azure.com --key-stdin
```

Creates an AI integration for the provider, or reuses one of the same name or --integration, stores the key from an environment variable, stdin or a file, and binds it to the app.

Sets GREGALE_AI_BASE_URL and, for OpenAI and Anthropic, the SDK&#39;s base URL and a placeholder key, so the app needs no code change. The app sees them on its next deploy or restart.


## link

Link this checkout to a Gregale project

`gregale link [<project-slug>] [--interactive] [--app <slug>] [--environment <environment>] [--env <environment>] [--no-gitignore]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose project, app, and environment, review defaults, and confirm saving |  |
| `--app <slug>` | workload/app slug for app-scoped commands; alone, links the app (and its project, if any) |  |
| `--environment <environment>` | default project environment scope |  |
| `--env <environment>` | alias for --environment |  |
| `--no-gitignore` | do not add .gregale/ to .gitignore |  |

Examples:

```sh
gregale link --interactive
gregale link my-project --interactive
gregale link my-project --app my-api --env staging
gregale link --app my-api
```


## unlink

Remove the linked project from this checkout

`gregale unlink`


## login

Authenticate this machine

`gregale login [--device] [--token <TOKEN>] [--token-stdin] [--insecure-storage]`

| Flag | Meaning | |
|---|---|---|
| `--device` | sign in with a code typed in a browser on another computer (SSH, headless) |  |
| `--token <TOKEN>` | use a pre-minted token (CI) |  |
| `--token-stdin` | read a pre-minted token from stdin (CI) |  |
| `--insecure-storage` | save the key to a plaintext file instead of the OS keychain (WSL, containers, SSH) |  |

Examples:

```sh
gregale login
gregale login --device
printf '%s' "$GREGALE_TOKEN" | gregale login --token-stdin
```


## logout

Revoke the managed CLI session and remove the stored token

`gregale logout`


## signup

Create a new account

`gregale signup [--email-only <EMAIL>] [--password-stdin]`

| Flag | Meaning | |
|---|---|---|
| `--email-only <EMAIL>` | send a one-time signup link to this email (no password prompt) |  |
| `--password-stdin` | read password from stdin (CI; mutually exclusive with --email-only) |  |


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


## version

Print the CLI version

`gregale version`


## update

Update the CLI to the newest release

`gregale update [--check] [--version <TAG>]`

Stable installs follow the newest stable release; prerelease installs follow the newest release.

The download is verified against the release&#39;s CLI-SHA256SUMS and must run before it replaces this binary.

An npm install prints the npm command instead of replacing files npm owns.

| Flag | Meaning | |
|---|---|---|
| `--check` | report whether a newer release exists without installing it |  |
| `--version <TAG>` | install this exact release tag, including an older one |  |

Examples:

```sh
gregale update
gregale update --check
gregale update --version v0.1.18
```


## man

Print the CLI manual page, or one command&#39;s page

`gregale man <command>`


## guide

Task pages, or search commands and docs for a task in your own words

`gregale guide [<task>|<question>] [--json]`

A question lists the matching commands, then docs sections from the docs shipped with this CLI and the live site. Preview pages show only when your account has that preview feature. GREGALE_DOCS_OFFLINE=1 skips the live site.

| Flag | Meaning | |
|---|---|---|
| `--json` | print the pages or the search results as JSON |  |

Examples:

```sh
gregale guide
gregale guide private-services
gregale guide "open an outbound tcp port"
```


## release

Inspect releases and control rollouts: canary, traffic, approvals

`gregale release [<subcommand>]`

### release list

List an app&#39;s deployments and manage named revision URLs

`gregale release list [--app <slug>] [--failed-only] [--limit <N>] [--before <CURSOR>] [--cursor <CURSOR>] [--all] [--wide]`

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
gregale release list my-api --limit 10
gregale release list --app my-api --wide
```

### release get

Print stages, terminal status, and failure guidance

`gregale release get [--app <SLUG>] <id|vN>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |

Examples:

```sh
gregale release get 00000000000000000000000000000001
gregale release get v42 --app my-api --json
```

### release stages

Print the closed 6-stage post-stream summary

`gregale release stages [--app <SLUG>] [--status] [--url] <id|vN>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--status` | include terminal status and timing |  |
| `--url` | print only the deployment preview URL |  |

Examples:

```sh
gregale release stages 00000000000000000000000000000001
gregale release stages v42 --app my-api --status
```

### release summary

Show the release diff and rollback target

`gregale release summary [--interactive] [--app <SLUG>] [<id|vN>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a release from app history and show its summary |  |
| `--app <SLUG>` | app slug (defaults to linked context or interactive picker) |  |

Examples:

```sh
gregale release summary --app my-api --interactive
gregale release summary v42 --app my-api
gregale release summary v42 --app my-api --json
```

### release attempt

get · cancel · retry

#### release attempt get

Inspect an atomic project attempt and available recovery actions

`gregale release attempt get ATTEMPT_ID`

#### release attempt cancel

Stop a progressive rollout and request guarded baseline restoration

`gregale release attempt cancel [--request-id <ID>] [--generation <N>] ATTEMPT_ID`

| Flag | Meaning | |
|---|---|---|
| `--request-id <ID>` | reuse this control identity after an ambiguous response |  |
| `--generation <N>` | require this inspected checkpoint generation |  |

#### release attempt retry

Rebuild confirmed failed workloads from a saved project deploy

`gregale release attempt retry [--tarball <PATH>] [--path <DIR>] [--source <auto|head|worktree>] [--worktree] [--yes] [--no-wait] [--timeout <SECONDS>] SESSION_ID`

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
gregale release attempt retry SESSION_ID --path . --source=worktree --yes
gregale release attempt retry SESSION_ID --tarball source.tar.gz --yes
```

### release logs

Print one deployment&#39;s build and deploy log

`gregale release logs [--app <SLUG>] [--limit <N>] <id|vN>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--limit <N>` | most recent log lines to print (1-5000) |  |

Examples:

```sh
gregale release logs v42 --app my-api
```

### release wait

Wait until a deployment is live (or safe rollout completes)

`gregale release wait [--app <SLUG>] [--for <CONDITION>] [--rollout] [--progress] [--timeout <SECONDS|DURATION>] <id|vN|latest>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug for vN or latest outside a linked project |  |
| `--for <CONDITION>` | wait for a condition instead (Ready, Progressing, RolledOut; TYPE=True\|False\|Unknown); exits 0 holds, 1 terminal, 3 timeout, 4 not found (preview) |  |
| `--rollout` | wait for safe rollout to reach 100% traffic |  |
| `--progress` | print rollout transitions while waiting (human output only) |  |
| `--timeout <SECONDS|DURATION>` | maximum wait (seconds, or a duration such as 10m) |  |

Examples:

```sh
gregale release wait <id|vN> --app my-api
gregale release wait 00000000000000000000000000000001
gregale release wait 00000000000000000000000000000001 --rollout --progress
```

### release verify

Verify an application response from an exact live deployment

`gregale release verify --app <SLUG> [--path <PATH>] [--project-id <UUID>] [--environment-id <UUID>] <deployment-uuid>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` |  | required |
| `--path <PATH>` | app-relative GET path (default /) |  |
| `--project-id <UUID>` | project identity for a named environment (looked up when omitted) |  |
| `--environment-id <UUID>` | environment identity for a named environment (looked up when omitted) |  |

Examples:

```sh
gregale release verify DEPLOYMENT_UUID --app my-api --path /
gregale --non-interactive --json release verify DEPLOYMENT_UUID --app my-api
```

Available on every plan with deployment-write access. Sends an owner-authorized GET; public access is not verified.

### release cancel

Cancel one pending deployment

`gregale release cancel [--app <slug>] [--reason <REASON>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to the linked project, or the deployment&#39;s own app) |  |
| `--reason <REASON>` | cancel reason (default user) | one of `user` · `auto_quota` · `auto_health` · `system` |

### release retry

Retry a failed deployment from a specific stage

`gregale release retry [--from <STAGE>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--from <STAGE>` | retry from this stage (defaults to the failing stage) | one of `source_download` · `dependency_restore` · `image_build` · `security_scan` · `snapshot_prepare` · `readiness` |

Choose the stage with --from.

A retry is a new deployment. Stage outputs are not kept between deployments, so a source build restarts from the original source; the build cache makes unchanged layers fast.

### release resume

Reconnect to a saved app or project deploy and finish pending checks

`gregale release resume [--app <SLUG>] [--timeout <SECONDS>] [SESSION_ID]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | select the most recent saved deploy for this app |  |
| `--timeout <SECONDS>` | maximum wait seconds (default 1200) |  |

Examples:

```sh
gregale release resume --app my-api
gregale release resume SESSION_ID --json
```

### release recover

Retry baseline health verification for a blocked progressive deploy

`gregale release recover [--request-id <ID>] [--generation <N>] ATTEMPT_ID`

| Flag | Meaning | |
|---|---|---|
| `--request-id <ID>` | reuse this control identity after an ambiguous response |  |
| `--generation <N>` | require this inspected checkpoint generation |  |

### release diff

Show what changed since the previous release

`gregale release diff [--against <ID|vN>] [--app <SLUG>] <id|vN>`

| Flag | Meaning | |
|---|---|---|
| `--against <ID|vN>` | release to compare with (default: the release that served before it) |  |
| `--app <SLUG>` | app slug, to resolve vN revisions |  |

Examples:

```sh
gregale release diff v42 --app my-api
gregale release diff v42 --against v38 --app my-api
```

Covers code, config keys, packages, runtime settings and the release command.

### release timeline

Show a release&#39;s rollout, gates, approvals and history

`gregale release timeline [--app <SLUG>] <id|vN>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug, to resolve vN revisions |  |

Examples:

```sh
gregale release timeline v42 --app my-api
gregale release timeline v42 --app my-api --json
```

### release resolve

Record a database check after a release command was lost

`gregale release resolve --note <TEXT> [--app <SLUG>] <id|vN>`

| Flag | Meaning | |
|---|---|---|
| `--note <TEXT>` | what you checked in the database | required |
| `--app <SLUG>` | app slug, to resolve vN revisions |  |

Examples:

```sh
gregale release resolve v41 --app my-api --note "migration 42 applied; schema matches"
```

Use it when a release command ended without an exit code: the next release command of the scope waits for this check.

### release hide

Hide one deployment from the list

`gregale release hide [--app <SLUG>] [--dry-run] [--yes] <id|vN>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug for revision resolution |  |
| `--dry-run` | preview cleanup without changing resources |  |
| `--yes` | confirm cleanup without prompting (alias: --force) |  |

### release prune

Hide obsolete deployments older than a cutoff

`gregale release prune --app <slug> [--older-than <D>] [--dry-run] [--yes]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--older-than <D>` | cutoff age (default 168h) |  |
| `--dry-run` | preview age/status candidates without changing resources |  |
| `--yes` | confirm cleanup without prompting (alias: --force) |  |

### release reorder

Change one pending deployment&#39;s queue priority

`gregale release reorder <id>`

### release runtime

Inspect runtime identity or preview a published runtime change

`gregale release runtime [--app <SLUG>] [--target <RELEASE_ID>] <ID|vN>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug for a vN revision |  |
| `--target <RELEASE_ID>` | published runtime release to preview without applying |  |

Examples:

```sh
gregale release runtime v42 --app my-function
gregale release runtime v42 --app my-function --target RELEASE_ID --json
```

### release rollback

Restore a previous deployment, or check an exact historical rollback

`gregale release rollback [status|switches] [--app <SLUG>] [--interactive] [--to <deployment_id|vN>] [--expected-current <deployment_id|vN>] [--reason <TEXT>] [--acknowledge-releases] [--restore-databases] [--wait] [--no-wait] [--timeout <duration>] [--poll-interval <duration>] [--json] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (same as the &lt;slug&gt; positional) |  |
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
gregale release rollback my-api --interactive
gregale release rollback my-api
gregale release rollback my-api --to v41
gregale release rollback --app my-api --to v41
gregale release rollback my-api --to v41 --expected-current v42 --wait
gregale release rollback my-api --to v41 --restore-databases
```

#### release rollback status

Show, follow or cancel a rollback that restores the database

`gregale release rollback status [--cancel] [--wait] [--timeout <duration>] [--poll-interval <duration>] [--operation <UUID>] [--json] <slug> <switch-id>`

| Flag | Meaning | |
|---|---|---|
| `--cancel` | cancel the switch; the app keeps its current release and database |  |
| `--wait` | follow the switch until it ends |  |
| `--timeout <duration>` | wait deadline (default 60m) |  |
| `--poll-interval <duration>` | poll interval (default 2s) |  |
| `--operation <UUID>` | read this rollback operation instead of a switch |  |
| `--json` | machine-readable output |  |

Examples:

```sh
gregale release rollback status my-api 6f1c2b0e-7d2a-4f7e-9a51-3c8d2f0b9e41 --wait
```

A switch can be cancelled until it starts switching the release and the bindings.

With --operation UUID in place of a switch ID, it reads a rollback operation, as `gregale rollback status` does.

#### release rollback switches

List rollbacks that restored or are restoring the database

`gregale release rollback switches [--json] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--json` | machine-readable output |  |

### release alias

Manage stable named URLs for immutable deployments

#### release alias list

List deployment aliases for an app

`gregale release alias list [--app <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; defaults to the linked project |  |

#### release alias set

Point an alias at an exact deployment revision

`gregale release alias set [--app <SLUG>] --name <NAME> --release <ID|vN>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; defaults to the linked project |  |
| `--name <NAME>` | lowercase DNS-label alias name | required |
| `--release <ID|vN>` | deployment ID or app revision (vN) (also --deployment) | required |

#### release alias delete

Remove an alias without deleting its deployment

`gregale release alias delete [--app <SLUG>] --name <NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; defaults to the linked project |  |
| `--name <NAME>` | lowercase DNS-label alias name | required |

### release contract

run · resolve

#### release contract run

Run planned schema cleanup after retained versions and work drain

`gregale release contract run [--request-id <ID>] [--generation <N>] ATTEMPT_ID`

| Flag | Meaning | |
|---|---|---|
| `--request-id <ID>` | reuse this contract identity after an ambiguous response |  |
| `--generation <N>` | require this inspected checkpoint generation |  |

#### release contract resolve

Record checks and a verified roll-forward after failed cleanup

`gregale release contract resolve [--replacement-attempt <ID>] [--task-id <ID>] [--generation <N>] [--request-id <ID>] [--evidence-hash <SHA256>] [--database-state-checked] [--execution-quiescent] ATTEMPT_ID`

| Flag | Meaning | |
|---|---|---|
| `--replacement-attempt <ID>` | verified corrected deployment attempt |  |
| `--task-id <ID>` | inspected original cleanup task |  |
| `--generation <N>` | inspected cleanup generation |  |
| `--request-id <ID>` | reuse after a lost recovery response |  |
| `--evidence-hash <SHA256>` | SHA256 of private SQL/execution evidence |  |
| `--database-state-checked` | attest database inspection/repair |  |
| `--execution-quiescent` | attest original cleanup execution has stopped |  |

### release canary

Advance canary stages, check their gates, or simulate a preset

#### release canary advance

Advance one observed stage with an optional audited profiling override

`gregale release canary advance [--expected-step <N>] [--profile-policy-revision <REV>] [--profile-override-reason <TEXT>] <deployment-id>`

| Flag | Meaning | |
|---|---|---|
| `--expected-step <N>` | observed current canary step |  |
| `--profile-policy-revision <REV>` | current policy revision for an explicit override |  |
| `--profile-override-reason <TEXT>` | audited reason for overriding only the profiling gate |  |

#### release canary simulate

Estimate per-stage canary success from the last hour

`gregale release canary simulate [--canary-preset <PRESET>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--canary-preset <PRESET>` | canary ladder preset | one of `slow` · `balanced` · `aggressive` · `1-10-50-100` · `adaptive` |

### release traffic

Split traffic between an app&#39;s deployments

#### release traffic set

Set the traffic split for a deployment

`gregale release traffic set [--app <SLUG>] --release <ID> --percent <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--release <ID>` | deployment id or vN revision to set the traffic split on (also --deployment) | required |
| `--percent <N>` | traffic weight in [0, 100]; -1 = unset (server default 100) | required |

Examples:

```sh
gregale release traffic set my-api --release v7 --percent 30
gregale release traffic set --app my-api --release v7 --percent 30
```

#### release traffic promote

Promote a live deployment to 100% production traffic

`gregale release traffic promote [--app <SLUG>] --release <ID> [--at <WHEN>] [--release-notify <CHANNEL,...>] [--if-serving <ID>] [--require-bindings] [--max-verification-age <DURATION>] [--allow-unsupported] [--require-application-ack] [--route-removal-mode <MODE>] [--route-readiness <PATH>] [--route-mapping <PATH>] [--route-owner-approval <PATH>] [--route-evidence-max-age <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug; only needed to resolve a vN revision outside a linked project |  |
| `--release <ID>` | deployment id or vN revision to promote (also --deployment) | required |
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
gregale release traffic promote my-api --release v7
gregale release traffic promote --app my-api --release v7 --if-serving v6
gregale release traffic promote --app my-api --release v7 --at 09:00
```

#### release traffic status

Show live deployment traffic weights for an app

`gregale release traffic status <slug>`

#### release traffic rollback

Undo a traffic split: all traffic back to the earlier release

`gregale release traffic rollback [--app <SLUG>] [--env <SCOPE>] [--to <ID>] [--reason <TEXT>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug |  |
| `--env <SCOPE>` | environment whose split to undo |  |
| `--to <ID>` | release to give all traffic; default: the largest share |  |
| `--reason <TEXT>` | reason recorded when a canary rollout is aborted |  |

Examples:

```sh
gregale release traffic rollback my-api
gregale release traffic rollback my-api --to v6
```

It acts on one environment: --env, or the only one the app is live in.

While a canary rollout runs there, this aborts it, as gregale rollouts recover --action abort does.

Otherwise the release with the largest share of the split gets 100%; --to names another live release, and a tie needs it.

It refuses if the traffic changes between reading and writing; run it again after checking release traffic status.

When one release already serves everything, use gregale rollback to go back to an earlier one.


## preview

Manage pull-request preview environments

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

### preview get

Inspect a preview and its latest deployment

`gregale preview get <preview-slug>`

Examples:

```sh
gregale preview get pr-42-my-api
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
| `--source-impact <PATH>` | route impact report from gregale api route impact |  |
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

`gregale preview customers [track|progress|migration] --report <PATH> [--by <consumer|tenant>] [--format <FORMAT>] [--out <PATH>]`

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
gregale preview customers track --roster customer-roster.json --mapping route-successors.json --release checkout=00000000-0000-4000-8000-000000000001
```

#### preview customers track

Compare a saved cohort with current route-customer telemetry

`gregale preview customers track --roster <PATH> --mapping <PATH> --release <APP=ID>... [--since <DURATION>] [--format <FORMAT>] [--out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--roster <PATH>` | version 1 customer roster JSON produced by preview customers | required |
| `--mapping <PATH>` | version 1 explicit old-to-successor route mapping JSON | required |
| `--release <APP=ID>` | immutable current deployment as APP=ID; repeat for each app (also --deployment) | required |
| `--since <DURATION>` | post-release observation window (duration or RFC3339 timestamp; default 14d) |  |
| `--format <FORMAT>` | text, Markdown, or CSV output (default text; --json emits machine-readable JSON) | one of `text` · `markdown` · `csv` |
| `--out <PATH>` | write the full machine-readable tracker to a new JSON file |  |

Examples:

```sh
gregale preview customers track --roster customer-roster.json --mapping route-successors.json --release checkout=00000000-0000-4000-8000-000000000001 --since 14d --format markdown
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
| `--contract-review <PATH>` | version 1 JSON from gregale api route migration review | required |
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

`gregale preview wait [--commit <SHA>] [--progress] [--open] [--timeout <SECONDS|DURATION>] <preview-slug>`

| Flag | Meaning | |
|---|---|---|
| `--commit <SHA>` | wait until the preview serves this commit (a full or abbreviated SHA), such as the pull request&#39;s head |  |
| `--progress` | print deployment transitions while waiting |  |
| `--open` | open the preview URL after it becomes ready |  |
| `--timeout <SECONDS|DURATION>` | maximum wait (seconds, or a duration such as 10m) |  |

Examples:

```sh
gregale preview wait pr-42-my-api --progress --open
```

### preview delete

Tear down a preview app

`gregale preview delete <preview-slug>`

Examples:

```sh
gregale preview delete pr-42-my-api
```


## project

Inspect and recover repository projects

`gregale project [<subcommand>] <project-slug>`

### project preview

Set or remove a pull request preview value on every service

#### project preview secret

Set or remove a pull request preview value on every service

`gregale project preview secret <set|unset>`

Values are write-only; previews receive them at their next deploy.

##### project preview secret set

Set KEY=VALUE pairs in every service&#39;s pr-preview scope

`gregale project preview secret set [--from-stdin] <project> [KEY=VALUE...]`

| Flag | Meaning | |
|---|---|---|
| `--from-stdin` | read KEY=VALUE pairs from stdin |  |

##### project preview secret unset

Remove keys from every service&#39;s pr-preview scope

`gregale project preview secret unset <project> <KEY...>`

### project list

List projects in this account

### project get

Show a project and its workloads

`gregale project get <slug>`

### project update

Update repository or production branch

`gregale project update [--repo <OWNER/NAME>] [--branch <BRANCH>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--repo <OWNER/NAME>` | GitHub repository owner/name; empty unbinds |  |
| `--branch <BRANCH>` | production branch |  |

### project delete

Preview or delete a project

`gregale project delete [--dry-run] [--yes] [--detach-services] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview affected state |  |
| `--yes` | confirm project deletion |  |
| `--detach-services` | delete even though its services call each other by name |  |

### project rollback

Roll back a recorded project deploy, callers first

`gregale project rollback [--dry-run] [--yes] [--history] [--targets] [--protect-deploy <UUID>] [--ttl <DURATION>] [--status <OPERATION_ID>] [--preview-resume <OPERATION_ID>] [--resume <OPERATION_ID>] [--pause <OPERATION_ID>] [--preview-undo <OPERATION_ID>] [--undo <OPERATION_ID>] [--all] [--to-deploy <UUID>] [--verify] [--verify-before-cutover] [--stabilize-for <DURATION>] [--verify-live-traffic] [--undo-on-health-failure] [--undo-on-live-traffic-failure] [--acknowledge-releases] [--plan-hash <SHA256>] [--request-id <UUID>] [--timeout <DURATION>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | print the rollback plan |  |
| `--yes` | start the server rollback and wait |  |
| `--history` | list recent project rollbacks and undos |  |
| `--targets` | list recorded deploys eligible for rollback preview |  |
| `--protect-deploy <UUID>` | retain a recorded deploy&#39;s rollback artifacts |  |
| `--ttl <DURATION>` | rollback artifact protection duration (default 168h) |  |
| `--status <OPERATION_ID>` | inspect the server rollback receipt |  |
| `--preview-resume <OPERATION_ID>` | preview the remaining steps of a paused rollback or undo |  |
| `--resume <OPERATION_ID>` | retry a blocked rollback or follow a running one |  |
| `--pause <OPERATION_ID>` | pause a running project rollback or undo |  |
| `--preview-undo <OPERATION_ID>` | preview the exact undo of a paused rollback |  |
| `--undo <OPERATION_ID>` | restore the original revisions from a paused rollback |  |
| `--all` | select each serving service and its current predecessor |  |
| `--to-deploy <UUID>` | restore the revisions recorded for a project deploy |  |
| `--verify` | verify configured HTTP health probes between cutovers |  |
| `--verify-before-cutover` | probe prepared revisions before moving project traffic |  |
| `--stabilize-for <DURATION>` | require healthy post-cutover probes for this duration |  |
| `--verify-live-traffic` | verify configured production 5xx budgets after cutover |  |
| `--undo-on-health-failure` | undo switched services on a confirmed health probe 5xx |  |
| `--undo-on-live-traffic-failure` | undo switched services on a sustained production 5xx breach |  |
| `--acknowledge-releases` | accept rolling back across release commands |  |
| `--plan-hash <SHA256>` | apply the plan printed by --dry-run, --preview-resume, or --preview-undo |  |
| `--request-id <UUID>` | reuse a rollback start request UUID |  |
| `--timeout <DURATION>` | wait per service (default 10m) |  |

By default, select the exact services and predecessors recorded for the latest serving project deploy.

Use --targets to list recent deploy IDs. Pass --to-deploy with --dry-run or --yes to restore the recorded revisions for that deploy.

Use --protect-deploy to retain the artifacts for a known-good deploy. --ttl defaults to 168h and accepts 1s to 168h. The --targets list shows expiry and retained layer bytes.

Dry run reports preflight blockers for every targeted service. The server warms and checks every target before moving traffic. The receipt shows preparation progress and any blockers.

The server continues after the CLI exits. Use --status with the operation ID to inspect progress from any authorized connection.

Run --dry-run and pass its plan hash to --yes --plan-hash with the same options to apply the reviewed plan. If the plan changed, review a fresh preview.

The CLI prints a request ID before submitting a rollback. To retry a start after a lost response, use --yes with the same --request-id and options.

Use --history to find recent rollback and undo receipts. The running operation appears first, followed by the most recent receipts, up to 20 in total.

Use --verify to check configured HTTP health probes after each cutover. A failed check pauses the operation. Fix the service and use --resume to recheck it before continuing.

Use --verify-before-cutover with --verify to probe all configured prepared target revisions at zero traffic before the first cutover. A failed probe or missing verification by the preparation deadline holds traffic.

Use --stabilize-for with --verify to require a 1s to 60s window of healthy probes after each cutover. The next cutover waits for the current service to pass.

Use --verify-live-traffic with --verify to check configured production 5xx budgets on restored services. A budget breach or insufficient evidence by the deadline holds the next cutover.

Use --undo-on-health-failure with --verify to undo switched services after a confirmed health probe 5xx. Use --status with the linked undo ID to follow it.

Use --undo-on-live-traffic-failure with --verify-live-traffic to undo switched services when a sustained production 5xx breach is confirmed again before undo.

Use --pause with a running operation ID to stop further cutovers. Gregale settles committed handoffs and releases unused preparations before reporting the pause.

Use --preview-resume with a paused operation ID to see completed and pending cutovers, blockers, and which verification will run. Pass its hash to --resume --plan-hash to continue the same operation only if the reviewed plan still matches.

Use --undo with a paused rollback ID to restore the original revisions for services that switched. Undo has its own operation ID. Use --status or --resume with that ID to follow it.

Use --preview-undo with a paused rollback ID to see the restored revisions and crossed release runs in reverse cutover order. Pass its plan hash to --undo --plan-hash with the same release acknowledgement to apply the reviewed undo.

Status and undo preview show the artifact hold for each revision that an undo can restore. The hold lasts while the rollback runs and for 24 hours after it pauses. An accepted undo takes over any active holds while it runs.

The receipt shows when HTTP routing committed, when gateways applied it, and when a successful request reached the restored revision.

### project health

Check serving health of every app in a project

`gregale project health <project>`

Same as `app health --project &lt;project&gt;`; takes the same flags.

### project wake

Wake every app in a project before traffic arrives

`gregale project wake <project>`

Same as `app wake --project &lt;project&gt;`; takes the same flags, such as --wait.

### project restart

Restart every app in a project

`gregale project restart <project>`

Same as `app restart --project &lt;project&gt;`; takes the same flags.

### project export

Write the project&#39;s services as a plain Compose file

`gregale project export [--format <FORMAT>] [--out <PATH>] <project>`

| Flag | Meaning | |
|---|---|---|
| `--format <FORMAT>` | output format (compose) |  |
| `--out <PATH>` | write to PATH instead of stdout |  |

Examples:

```sh
gregale project export shop --out compose.yaml
```

Env values stay sealed: each variable is referenced by name, as ${NAME}.

Gregale settings are written as x-gregale-* keys; other Compose tools ignore them, and gregale deploy reads them back.

### project import

Import .gregale/project.yaml into the root gregale.yaml project block

`gregale project import [--dry-run] [<dir>]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | print the new gregale.yaml without writing |  |

Runs locally; nothing is uploaded.

The existing gregale.yaml is kept as written; the block is appended and checked against the old file before anything changes.


## build

Inspect builds, their provenance and SBOMs

`gregale build [<subcommand>]`

### build status

Show the current status of one build

`gregale build status <id>`

### build list

List builds and discover build IDs

`gregale build list [--app <SLUG>] [--status <STATUS>] [--limit <N>] [--before <CURSOR>] [--cursor <CURSOR>] [--all] [--stream] [--output <PATH>] [--progress <MODE>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | filter to one app |  |
| `--status <STATUS>` | filter by lifecycle status | one of `queued` · `running` · `succeeded` · `failed` · `cancelled` |
| `--limit <N>` | page size (1..200) |  |
| `--before <CURSOR>` | alias for --cursor |  |
| `--cursor <CURSOR>` | opaque cursor from a prior page |  |
| `--all` | walk every page using --limit and --cursor |  |
| `--stream` | emit JSON Lines as pages arrive; stdout requires --json --all |  |
| `--output <PATH>` | publish a new JSON Lines file after all pages complete; requires --all |  |
| `--progress <MODE>` | export progress on stderr; requires --all --output | one of `off` · `plain` · `ndjson` |
| `--timeout <DURATION>` | overall list read budget including page requests and retries (positive duration) |  |

### build provenance

Show the build provenance attestation

`gregale build provenance <id>`

### build sbom

Show the build SBOM

`gregale build sbom <id>`


## github

Connect GitHub and bind apps to repositories

`gregale github [<subcommand>] <slug>`

### github connect

Connect a GitHub account for repo deploys

Examples:

```sh
gregale github connect
```

### github link

Bind an app to a visible GitHub repository

`gregale github link [--installation-id <ID>] --repo <OWNER/NAME> [--branch <BRANCH>] [--deploy-branches <MAPPINGS>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--installation-id <ID>` | GitHub App installation id (the app&#39;s, or the account&#39;s only one, when omitted) |  |
| `--repo <OWNER/NAME>` | GitHub repository OWNER/NAME | required |
| `--branch <BRANCH>` | production branch (defaults to the repository&#39;s default branch) |  |
| `--deploy-branches <MAPPINGS>` | branch=scope mappings |  |

Examples:

```sh
gregale github link my-api --repo acme/my-api --branch main
```

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

`gregale github repos [--installation-id <ID>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--installation-id <ID>` | GitHub App installation id (defaults to the app&#39;s, or the account&#39;s only one) |  |

Examples:

```sh
gregale github repos my-api
gregale github repos my-api --installation-id 12345
```

### github setup

Bind GitHub, configure previews, and write an Actions workflow

`gregale github setup [--repo <OWNER/NAME>] [--production-branch <BRANCH>] [--installation-id <ID>] [--deploy-branches <MAPPINGS>] [--pinned-sha <SHA>] [--pin-action] [--enable-action-updates] [--workflow <PATH>] [--preview] [--no-preview] [--preview-ttl-hours <HOURS>] [--preview-bots] [--no-preview-bots] [--preview-drafts] [--no-preview-drafts] [--preview-service-policy <POLICY>] [--root-dir <DIR>] [--ignore <PATHS>] [--rollout <MODE>] [--wait-for <WORKFLOW>] [--dry-run] [--force] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--repo <OWNER/NAME>` | GitHub repository OWNER/NAME (required for a dry run) |  |
| `--production-branch <BRANCH>` | production branch (default: current binding or the repository&#39;s default branch) |  |
| `--installation-id <ID>` | GitHub App installation id (the app&#39;s, or the account&#39;s only one, when omitted) |  |
| `--deploy-branches <MAPPINGS>` | comma-separated branch=environment mappings (default or registered environment) |  |
| `--pinned-sha <SHA>` | pin the generated deploy Action to this full 40-character commit SHA (default: immutable SHA embedded in the CLI release) |  |
| `--pin-action` | resolve the current v0 deploy Action tag to its commit SHA |  |
| `--enable-action-updates` | add a weekly GitHub Actions Dependabot updater |  |
| `--workflow <PATH>` | workflow path relative to repository root |  |
| `--preview` | enable pull-request previews |  |
| `--no-preview` | disable pull-request previews |  |
| `--preview-ttl-hours <HOURS>` | preview lease in hours (1-720) |  |
| `--preview-bots` | preview pull requests opened by bots such as Dependabot |  |
| `--no-preview-bots` | skip previews for pull requests opened by bots (the default) |  |
| `--preview-drafts` | preview draft pull requests (the default) |  |
| `--no-preview-drafts` | preview a draft pull request only once it is ready for review |  |
| `--preview-service-policy <POLICY>` | preview-to-production service calls: deny\|allow_marked | one of `deny` · `allow_marked` |
| `--root-dir <DIR>` | repository-relative source root for the root workload |  |
| `--ignore <PATHS>` | comma-separated ignored change paths |  |
| `--rollout <MODE>` | production rollout mode: standard\|safe | one of `standard` · `safe` |
| `--wait-for <WORKFLOW>` | deploy a branch push only after this GitHub Actions workflow (its name: field) succeeds for the commit |  |
| `--dry-run` | show generated files without writing or changing remote state |  |
| `--force` | overwrite an existing workflow file |  |

Examples:

```sh
gregale github setup my-api --repo acme/my-api --dry-run
gregale github setup my-api --repo acme/my-api --preview --preview-ttl-hours 72
gregale github setup my-api --repo acme/my-api --wait-for CI
```

### github disconnect

Remove the app&#39;s GitHub repository binding

`gregale github disconnect [--yes] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | confirm removing the repository binding |  |


## registry

Manage registry credentials, signing keys and published images

`gregale registry [<subcommand>]`

### registry deploy

Deploy an image after CI publishes its immutable digest

`gregale registry deploy --app <slug> --image <REF> [--scope <SLUG>] [--env <SLUG>] [--wait] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--image <REF>` | published digest-pinned image reference | required |
| `--scope <SLUG>` | deployment scope |  |
| `--env <SLUG>` | registered project environment (also --environment) |  |
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

### registry delete

Remove a registry credential

`gregale registry delete --app <slug> --registry <host>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--registry <host>` | registry host | required |


## app

Inspect, change, scale and operate apps

`gregale app [<subcommand>]`

### app list

List your apps

`gregale app list [--selector|-l <SELECTOR>] [--dry-run] [--quiet|-q] [--yes]`

| Flag | Meaning | |
|---|---|---|
| `--selector|-l <SELECTOR>` | only apps whose labels match (preview) |  |
| `--dry-run` | preview app deletion without changing resources |  |
| `--quiet|-q` | delete one app without prompting |  |
| `--yes` | confirm app deletion without prompting |  |

Examples:

```sh
gregale app list
gregale app list --json
gregale app list -l team=billing
gregale app delete my-api --dry-run
gregale app delete my-api --yes
```

### app delete

Delete an app; restore it within the deletion grace window

`gregale app delete [--dry-run] [--yes] [--quiet] [-q] [--delete-bindings] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview deletion without changing resources |  |
| `--yes` | confirm deletion without prompting; required in non-interactive and JSON mode |  |
| `--quiet` | confirm deletion without prompting (alias of --yes) |  |
| `-q` | confirm deletion without prompting (alias of --yes) |  |
| `--delete-bindings` | revoke the app&#39;s managed PostgreSQL bindings first (deleting the app otherwise refuses while any is live) |  |

Also accepted as delete.

### app restore

Restore an app during its deletion grace window

`gregale app restore <slug>`

### app purge

Permanently remove a deleted app now and free its name

`gregale app purge [--yes] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | purge without prompting; required in non-interactive and JSON mode |  |

Examples:

```sh
gregale app delete api --yes && gregale app purge api --yes
```

A deleted app keeps its name for seven days so it can be restored; purge ends that window at once, so a redeploy (for example the same Compose file under a new project) can reuse the name.

Purging cannot be undone. Delete the app&#39;s object storage buckets first.

### app instances

Show an app&#39;s live instances and their state

`gregale app instances [--all] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--all` | include the newest 100 retained history rows (parked rows expire after 30d by default) |  |

### app park

Stop all of an app&#39;s instances until its next request

`gregale app park <slug>`

Examples:

```sh
gregale app park my-api
```

A min-instances floor stays off, and unbilled, until the next request.

### app wake

Wake a parked app before traffic arrives

`gregale app wake [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the requested wake to reach running |  |
| `--timeout <DURATION>` | maximum time to wait for the requested wake (default 1m) |  |
| `--poll-interval <DURATION>` | interval between instance status checks (default 250ms) |  |

Examples:

```sh
gregale app wake my-api
gregale app wake --wait --timeout 2m my-api
```

### app wake-ahead

Wake the services an app calls while the app itself wakes

`gregale app wake-ahead <slug> [on|off]`

Examples:

```sh
gregale app wake-ahead my-api
gregale app wake-ahead my-api on
gregale app wake-ahead my-api off --json
```

### app wake-timeline

Show the step-by-step timeline of one app wake

`gregale app wake-timeline [--app <SLUG>] [--since <RFC3339>] [--limit <N>] [--all] [--verbose] [<slug>] <wake-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (alternative to the leading slug positional) |  |
| `--since <RFC3339>` | RFC3339 timestamp |  |
| `--limit <N>` | page size (1..1000) |  |
| `--all` | walk every page |  |
| `--verbose` | show detailed restore phases in human output |  |

### app binding

Inspect an app&#39;s resource bindings and credential rotation

`gregale app binding [call|probe-policy|verification|release-policy|service|check|object-storage|verify] [--release <ID|vN>] [--require-complete] [--env <SCOPE>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--release <ID|vN>` | inspect exact deployment service bindings, including frozen environment routes (also --deployment) |  |
| `--require-complete` | fail if binding metadata, verification, runtime freshness or refresh progress is incomplete |  |
| `--env <SCOPE>` | filter resource bindings by environment scope; app-wide bindings remain included (also --scope) |  |

#### app binding call

Invoke a path on one live deployment over the private binding

`gregale app binding call [--target-deployment <ID>] [--release <ID>] [--caller-deployment <ID|vN>] --path <PATH> [--expect-status <CODE>] [--poll-interval <D>] [--wait-timeout <D>] <app> <service>`

| Flag | Meaning | |
|---|---|---|
| `--target-deployment <ID>` | exact live target deployment UUID to invoke (or use --deployment) |  |
| `--release <ID>` | alias for --target-deployment (also --deployment) |  |
| `--caller-deployment <ID|vN>` | exact live caller deployment, including zero-traffic candidates |  |
| `--path <PATH>` | absolute path on the target service | required |
| `--expect-status <CODE>` | require this exact HTTP status; default accepts any 2xx response |  |
| `--poll-interval <D>` | status polling interval while the smoke task runs |  |
| `--wait-timeout <D>` | maximum time to wait for the smoke task |  |

Examples:

```sh
gregale app binding call public-api billing --caller-deployment v12 --target-deployment TARGET_UUID --path /ready --expect-status 200
```

#### app binding probe-policy

Configure or remove an outbound integration probe

`gregale app binding probe-policy <set>`

##### app binding probe-policy set

Configure or remove an outbound integration probe

`gregale app binding probe-policy set [--path <PATH>] [--method <METHOD>] [--expect-status <STATUS>] [--delete] <integration-id>`

| Flag | Meaning | |
|---|---|---|
| `--path <PATH>` | provider path declared safe to probe |  |
| `--method <METHOD>` | GET or HEAD (default GET) |  |
| `--expect-status <STATUS>` | expected successful response status (default 200) |  |
| `--delete` | remove probe configuration |  |

Examples:

```sh
gregale app binding probe-policy set INTEGRATION_ID --path /health --method GET --expect-status 200
```

#### app binding verification

Inspect, resume or cancel a durable binding verification run

`gregale app binding verification <list|diagnostics|status|resume|cancel>`

##### app binding verification list

List retained receipt summaries without requalifying evidence

`gregale app binding verification list [--limit <N>] [--cursor <CURSOR>] [--release <UUID>] [--status <STATUS>] [--scope <SCOPE>] [--include-members] <app>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (default 20; range 1–100) |  |
| `--cursor <CURSOR>` | next page cursor; reuse filters |  |
| `--release <UUID>` | exact deployment filter (also --deployment) |  |
| `--status <STATUS>` | stored state or observed expiry |  |
| `--scope <SCOPE>` | deployment scope |  |
| `--include-members` | include graph members with root linkage |  |

##### app binding verification diagnostics

Explain blockers, public changes and recovery actions

`gregale app binding verification diagnostics <app> <run-id>`

##### app binding verification status

Read the original plan and current resource progress

`gregale app binding verification status <app> <run-id>`

##### app binding verification resume

Continue the original review without renewing deadlines

`gregale app binding verification resume [--progress <MODE>] [--status-read-retries <N>] [--concurrency <N>] [--poll-interval <DURATION>] [--wait-timeout <DURATION>] <app> <run-id>`

| Flag | Meaning | |
|---|---|---|
| `--progress <MODE>` | durable stderr progress: auto, plain, ndjson or off |  |
| `--status-read-retries <N>` | transient durable status GET retries (default 3; range 0–8) |  |
| `--concurrency <N>` | concurrent probe limit (default 2; range 1–8) |  |
| `--poll-interval <DURATION>` | probe polling interval |  |
| `--wait-timeout <DURATION>` | wait budget bounded by the original deadline |  |

##### app binding verification cancel

Block new admission and cancel unfinished owned work

`gregale app binding verification cancel <app> <run-id>`

#### app binding release-policy

Require fresh binding evidence for traffic increases in a scope

`gregale app binding release-policy <get|set>`

##### app binding release-policy get

Read the stored release policy

`gregale app binding release-policy get [--scope <SCOPE>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | deployment scope (default default) |  |

##### app binding release-policy set

Replace the release policy using its current revision

`gregale app binding release-policy set [--scope <SCOPE>] [--mode <off|enforce>] [--require-verification] [--max-age <DURATION>] [--require-application-ack] [--object-storage-require <PREFIX=read|write|read_write>] [--clear-object-storage-requirements] [--queue-require <NAME=consumer_readiness>] [--clear-queue-requirements] --expected-revision <N> [--reason <TEXT>] <app>`

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
gregale app binding release-policy set public-api --scope production --require-verification --max-age 10m --expected-revision 0
```

#### app binding service

Configure standalone service bindings

`gregale app binding service <explain|export|check|configure|apply>`

##### app binding service explain

Explain service authorization, optionally for one deployment

`gregale app binding service explain [--release <ID>] [--env <ENV>] [--method <METHOD>] [--path <PATH>] <caller> <target>`

| Flag | Meaning | |
|---|---|---|
| `--release <ID>` | exact caller deployment UUID; use server routing and effective policy (also --deployment) |  |
| `--env <ENV>` | assert deployment environment; requires --deployment (also --scope) |  |
| `--method <METHOD>` | HTTP method (default GET) |  |
| `--path <PATH>` | origin-form request path (default /) |  |

Exits 0 when allowed, 1 when denied, and 2 when indeterminate or on error.

##### app binding service export

Export live service settings as a reusable policy

`gregale app binding service export [--out <PATH>] <app>`

| Flag | Meaning | |
|---|---|---|
| `--out <PATH>` | write to a new private file (default stdout) |  |

##### app binding service check

Compare live settings with a policy; exits 0 match, 1 drift, 2 error

`gregale app binding service check --file <PATH> <app>`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | JSON service binding settings policy | required |

##### app binding service configure

Patch service targets, permissions, reliability and URL transport

`gregale app binding service configure [--plan-out <PATH>] --file <PATH> [--dry-run] <app>`

| Flag | Meaning | |
|---|---|---|
| `--plan-out <PATH>` | save a reviewed plan without changes; destination must be new |  |
| `--file <PATH>` | JSON service binding settings patch | required |
| `--dry-run` | show current and proposed binding settings without changes |  |

Examples:

```sh
gregale app binding service configure public-api --file policy.json --dry-run
```

##### app binding service apply

Apply a saved service binding plan if reviewed settings are unchanged

`gregale app binding service apply --plan <PATH> <app>`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | saved service binding plan | required |

#### app binding check

Evaluate recorded binding evidence and runtime freshness for CI

`gregale app binding check [--interactive] [--app <SLUG>] [--scope <SCOPE>] [--max-verification-age <DURATION>] [--release <ID|vN>] [--allow-unsupported] [--require-application-ack] [--release-policy] [--object-storage-require <PREFIX=read|write|read_write>] [--queue-require <NAME=consumer_readiness>] [--require-bindings] [--ignore-manifest] [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] [<app>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a live deployment and readiness options |  |
| `--app <SLUG>` | app slug (linked app or picker in interactive mode) |  |
| `--scope <SCOPE>` | require the selected deployment to use this scope (default its current scope) |  |
| `--max-verification-age <DURATION>` | maximum age of passed probe evidence (default 10m) |  |
| `--release <ID|vN>` | exact live deployment whose evidence must pass, including zero-traffic candidates (also --deployment) |  |
| `--allow-unsupported` | waive connectivity coverage for active queue and outbound bindings |  |
| `--require-application-ack` | require current PostgreSQL/object-storage application acknowledgements |  |
| `--release-policy` | apply stored scope requirements and confirm the policy after every inventory evaluation (read-only) |  |
| `--object-storage-require <PREFIX=read|write|read_write>` | require recorded storage operation evidence (repeatable; no writes) |  |
| `--queue-require <NAME=consumer_readiness>` | require recorded push queue readiness (repeatable; connectivity remains partial) |  |
| `--require-bindings` | fail when no active binding is in scope (default: warn and pass) |  |
| `--ignore-manifest` | do not require the databases ./gregale.yaml declares for the app |  |
| `--wait` | poll read-only inventory while probes, refreshes or application acknowledgements are pending |  |
| `--timeout <DURATION>` | maximum waiting or policy-aware preflight time (default 5m) |  |
| `--poll-interval <DURATION>` | inventory polling interval with --wait (default 1s) |  |

Examples:

```sh
gregale app binding check --app my-api --interactive
gregale app binding check my-api --max-verification-age 10m --json
gregale app binding check my-api --scope production
gregale app binding check my-api --release v12 --release-policy --wait --json
gregale app binding check my-api --release v12 --max-verification-age 10m --json
```

#### app binding object-storage

Manage app-to-bucket compute bindings

`gregale app binding object-storage <list|rotate|revoke>`

##### app binding object-storage list

List safe binding metadata

`gregale app binding object-storage list <app> <bucket>`

Examples:

```sh
gregale app binding object-storage list my-api assets
```

##### app binding object-storage rotate

Rotate a binding credential and optionally wait for retirement

`gregale app binding object-storage rotate [--wait] [--wait-timeout <DURATION>] [--poll-interval <DURATION>] <app> <bucket> <binding-id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the previous credential to retire |  |
| `--wait-timeout <DURATION>` | maximum time to wait for rotation (default 5m) |  |
| `--poll-interval <DURATION>` | status polling interval while waiting (default 1s) |  |

Examples:

```sh
gregale app binding object-storage rotate my-api assets BINDING_ID --wait
```

##### app binding object-storage revoke

Revoke a binding credential

`gregale app binding object-storage revoke <app> <bucket> <binding-id>`

Examples:

```sh
gregale app binding object-storage revoke my-api assets BINDING_ID
```

#### app binding verify

Check a private route or test a PostgreSQL or storage binding

`gregale app binding verify [--all] [--require-complete] [--release-policy] [--durable] [--run-id <UUID>] [--progress <MODE>] [--status-read-retries <N>] [--concurrency <N>] [--dry-run] [--postgres <ENVIRONMENT_KEY>] [--outbound <INTEGRATION_ID>] [--object-storage <PREFIX>] [--queue <BINDING>] [--queue-publish] [--object-storage-write] [--release <ID|vN>] [--poll-interval <D>] [--wait-timeout <D>] <app> [<service>]`

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
| `--release <ID|vN>` | exact live deployment to verify, including zero-traffic candidates (also --deployment) |  |
| `--poll-interval <D>` | status polling interval while the canary runs |  |
| `--wait-timeout <D>` | maximum time to wait for canaries; bounds the entire --release-policy workflow |  |

Examples:

```sh
gregale app binding verify my-api billing
gregale app binding verify my-api --all
gregale app binding verify my-api --release v12 --all
gregale app binding verify my-api --all --release v12 --dry-run --json
gregale app binding verify my-api --postgres DATABASE_URL
gregale app binding verify my-api --object-storage GREGALE_S3_ASSETS
gregale app binding verify my-api --release v12 --release-policy --dry-run --json
gregale app binding verify my-api --release v12 --release-policy --object-storage-write --json
```

### app deploy-token

Manage per-app deploy tokens for CI

When MFA is enabled, browser token creation or rotation needs verification from the last 5 minutes. CLI bearer credentials retain their configured scopes.

#### app deploy-token list

List an app&#39;s deploy tokens

`gregale app deploy-token list --app <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app the tokens deploy | required |

#### app deploy-token create

Mint a deploy token (shown once)

`gregale app deploy-token create [--agent] [--scopes <SCOPES>] --app <SLUG> [--label <LABEL>] [--expires-at <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--agent` | mint an app-bound headless MCP credential |  |
| `--scopes <SCOPES>` | comma-separated app-agent scopes (requires --agent) |  |
| `--app <SLUG>` | app the token deploys | required |
| `--label <LABEL>` | label shown in the list, for example the CI system |  |
| `--expires-at <RFC3339>` | expiry as an RFC3339 time (default 90 days) |  |

Examples:

```sh
gregale app deploy-token create --app my-api --label github-actions
```

#### app deploy-token rotate

Replace a deploy token; the old one stops working

`gregale app deploy-token rotate --app <SLUG> [--label <LABEL>] [--expires-at <RFC3339>] <token-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app the token deploys | required |
| `--label <LABEL>` | label for the replacement (default: the old label) |  |
| `--expires-at <RFC3339>` | expiry as an RFC3339 time (default 90 days) |  |

#### app deploy-token delete

Revoke a deploy token

`gregale app deploy-token delete --app <SLUG> <token-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app the token deploys | required |

### app streaming-cap

Show app streaming classification

`gregale app streaming-cap <slug>`

### app get

Show an app&#39;s settings and status

`gregale app get [<app>]`

### app update

Change an app&#39;s settings

`gregale app update [--concurrency] [--env <SLUG>] [--visibility <public|internal>] [--size <SIZE>] [--profile <SIZE>] [--ram <MB>] [--cpu-millicores <250|500|1000>] [--max-concurrency <N>] [--concurrency-overflow <value>] [--max-queue-depth <N>] [--max-queue-wait <DURATION>] [--max-queue-wait-ms <N>] [--wake-max-queue-depth <N>] [--wake-max-queue-wait-seconds <N>] [--idle <SEC>] [--request-timeout <SEC>] [--function-concurrency <N>] [--revision-pin-ttl <DURATION>] [--require-signed <true|false>] [--security-policy <value>] [--basic-user <USER>] [--basic-pass <PASS>] [--min <N>] [--autoscale-target-rps <N>] [--autoscale-target-cpu-pct <1..100>] [--warm-snapshot] [--no-warm-snapshot] [--warm-snapshot-min-requests <N>] [--warm-snapshot-min-ms <MS>] [--warm-pool-size <N>] [--eviction-priority <best_effort|reserved>] [--require-authn] [--no-require-authn] [--platform-tenant-required] [--no-platform-tenant-required] [--maintenance] [--no-maintenance] [--streaming-enabled] [--no-streaming-enabled] [--websocket-enabled] [--no-websocket] [--route-metrics] [--no-route-metrics] [--consumer-auth-mode <optional|required>] [--only-declared-routes] [--no-only-declared-routes] [--head-wakes[=true|false]] [--ports <[NAME=]tcp|udp:PORT[:internal],...>] [--crawler-policy <wake|cached|block>] [--pre-auth <off|observe|enforce>] [--pre-auth-rps <N>] [--pre-auth-burst <N>] [--release-policy <safe|fast|immediate>] [--release-probe-paths <PATH,PATH>] [--health-path <PATH>] [--health-path-wakes] [--no-health-path-wakes] [--request-rate-limit-rps <N>] [--request-rate-limit-burst <N>] [--cors-default] [--no-cors-default] [--cors-default-origins <ORIGINS>] [--session-affinity] [--no-session-affinity] [--version-affinity-cookie <NAME>] [--version-affinity-managed-cookie] [--no-version-affinity-managed-cookie] [--robots-txt-file <PATH>] [--default-robots-txt] [--favicon-file <PATH>] [--no-favicon] [--app-protocol <http1|http2|grpc>] [--public-auth <open|bearer|basic|ip_allowlist|internal_only>] [--ip-allowlist <CIDR>]... [--overflow-node <NAME>] [<app>]`

| Flag | Meaning | |
|---|---|---|
| `--concurrency` | print only the per-VM concurrency bound for the app&#39;s plan |  |
| `--env <SLUG>` | read or edit this project environment (also --environment) |  |
| `--visibility <public|internal>` | set public edge exposure | one of `public` · `internal` |
| `--size <SIZE>` | set RAM/CPU resource size | one of `micro` · `small` · `medium` · `large` · `xlarge` |
| `--profile <SIZE>` | deprecated: use --size |  |
| `--ram <MB>` | set RAM in MB |  |
| `--cpu-millicores <250|500|1000>` | cap CPU at 250 or 500 millicores; 1000 (the default) leaves it uncapped | one of `250` · `500` · `1000` |
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
| `--only-declared-routes` | reject undeclared paths before waking the app (OpenAPI or explicit route list); the edge-answered health path stays reachable |  |
| `--no-only-declared-routes` | disable the declared-route pre-wake gate |  |
| `--head-wakes[=true|false]` | wake a parked app for HEAD / | one of `true` · `false` |
| `--ports <[NAME=]tcp|udp:PORT[:internal],...>` | declare TCP/UDP listeners (unnamed ones are named tcp-PORT / udp-PORT); none clears |  |
| `--crawler-policy <wake|cached|block>` | monitor/crawler wake policy | one of `wake` · `cached` · `block` |
| `--pre-auth <off|observe|enforce>` | per-source pre-auth limit mode; enforce first prints the 24h readiness check | one of `off` · `observe` · `enforce` |
| `--pre-auth-rps <N>` | pre-auth requests per second per source |  |
| `--pre-auth-burst <N>` | pre-auth burst per source |  |
| `--release-policy <safe|fast|immediate>` | default rollout for production releases | one of `safe` · `fast` · `immediate` |
| `--release-probe-paths <PATH,PATH>` | GET paths (at most 3) the default rollout probe checks instead of /; empty restores / |  |
| `--health-path <PATH>` | set the monitor-facing health path |  |
| `--health-path-wakes` | allow health probes to wake the app |  |
| `--no-health-path-wakes` | answer health probes without waking the app |  |
| `--request-rate-limit-rps <N>` | app-wide request rate limit per second; 0 restores the plan default |  |
| `--request-rate-limit-burst <N>` | app-wide request burst; 0 restores the plan default |  |
| `--cors-default` | answer CORS for --cors-default-origins when no CORS edge rule matches |  |
| `--no-cors-default` | turn the default CORS answer off |  |
| `--cors-default-origins <ORIGINS>` | comma-separated origins for the default CORS answer; empty clears |  |
| `--session-affinity` | route a client to the same running instance (best effort) |  |
| `--no-session-affinity` | turn session affinity off |  |
| `--version-affinity-cookie <NAME>` | cookie that pins a client to a release; empty turns it off |  |
| `--version-affinity-managed-cookie` | let Gregale set the version-affinity cookie |  |
| `--no-version-affinity-managed-cookie` | stop Gregale setting the version-affinity cookie |  |
| `--robots-txt-file <PATH>` | serve this file as /robots.txt at the edge |  |
| `--default-robots-txt` | restore the platform&#39;s allow-all /robots.txt |  |
| `--favicon-file <PATH>` | serve this icon as /favicon.ico at the edge |  |
| `--no-favicon` | remove the app&#39;s edge favicon |  |
| `--app-protocol <http1|http2|grpc>` | set the wire-protocol selector | one of `http1` · `http2` · `grpc` |
| `--public-auth <open|bearer|basic|ip_allowlist|internal_only>` | set public URL authentication; internal_only admits Gregale internal services, ip_allowlist is Pro+ | one of `open` · `bearer` · `basic` · `ip_allowlist` · `internal_only` |
| `--ip-allowlist <CIDR>` | allow a CIDR through the public URL; repeat for multiple ranges; requires --public-auth ip_allowlist |  |
| `--overflow-node <NAME>` | set or clear the preferred overflow compute node |  |

### app health

Check serving health and readiness

`gregale app health [--project <PROJECT>] [--check] [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] [--history] [--limit <N>] [--cursor <CURSOR>] [--env <SLUG>] [--explain] [--errors] [<app>]`

| Flag | Meaning | |
|---|---|---|
| `--project <PROJECT>` | check every app of this project instead of one |  |
| `--check` | single read; exit 0 only for a fresh healthy default-scope assessment; excludes wait and history options |  |
| `--wait` | wait for a fresh healthy default-scope assessment |  |
| `--timeout <DURATION>` | maximum wait (default 2m; requires --wait) |  |
| `--poll-interval <DURATION>` | poll interval (default 2s; 1s to 1h; requires --wait) |  |
| `--history` | show recorded health changes; excludes --wait |  |
| `--limit <N>` | history page size |  |
| `--cursor <CURSOR>` | history cursor (also --before) |  |
| `--env <SLUG>` | assess this project environment instead of the default scope; excludes history options (also --environment) |  |
| `--explain` | explain health from runtime, release, data and scaling (what inspect shows); stands alone |  |
| `--errors` | explain the latest failed deployment (what inspect --errors shows); stands alone |  |

Examples:

```sh
gregale app health my-api --wait --timeout 2m
```

### app scale

Preview or change resources and settings

`gregale app scale [--interactive] [--plan] [--out <PATH>] [--apply <PATH>] [--confirm] [--env <SLUG>] [--size <SIZE>] [--profile <SIZE>] [--ram <MB>] [--cpu-millicores <250|500|1000>] [--max-concurrency <N>] [--concurrency-overflow <POLICY>] [--max-queue-depth <N>] [--max-queue-wait <DURATION>] [--max-queue-wait-ms <MS>] [--wake-max-queue-depth <N>] [--wake-max-queue-wait-seconds <SECONDS>] [--idle <SECONDS>] [--request-timeout <SECONDS>] [--min <N>] [--autoscale-target-rps <N>] [--autoscale-target-cpu-pct <1..100>] [--warm-snapshot] [--no-warm-snapshot] [--warm-snapshot-min-requests <N>] [--warm-snapshot-min-ms <MS>] [--warm-pool-size <N>] [--require-authn] [--no-require-authn] [--head-wakes[=true|false]] [--crawler-policy <POLICY>] [--health-path <PATH>] [--health-path-wakes] [--no-health-path-wakes] [--app-protocol <PROTOCOL>] [<app>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose settings with a preview and confirmation; accepts only --environment |  |
| `--plan` | show changes and supported plan effects without applying them |  |
| `--out <PATH>` | write a reusable plan JSON to a new file (requires --plan) |  |
| `--apply <PATH>` | apply a saved scale plan JSON file |  |
| `--confirm` | confirm applying the saved plan (requires --apply) |  |
| `--env <SLUG>` | edit desired workload settings in a project environment (also --environment) |  |
| `--size <SIZE>` | RAM/CPU resource size | one of `micro` · `small` · `medium` · `large` · `xlarge` |
| `--profile <SIZE>` | deprecated: use --size |  |
| `--ram <MB>` | RAM in MB |  |
| `--cpu-millicores <250|500|1000>` | cap CPU at 250 or 500 millicores; 1000 (the default) leaves it uncapped | one of `250` · `500` · `1000` |
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
gregale app scale my-api --interactive
gregale app scale my-api --plan --ram 512 --out scale-change.json
gregale app scale my-api --apply scale-change.json --confirm
```

### app scaling

Read, guard or recover scaling configuration; prints JSON

`gregale app scaling [--env <ENV>] [--receipt <UUID>] [--request-id <UUID>] [--expected-revision <REVISION>] [--expected-app-id <UUID>] [--expected-environment-id <UUID>] [--ram <MB>] [--cpu-millicores <MILLICORES>] [--max-concurrency <COUNT>] [--min <COUNT>] [<app>]`

| Flag | Meaning | |
|---|---|---|
| `--env <ENV>` | named project environment (also --environment) |  |
| `--receipt <UUID>` | recover a request receipt without writing |  |
| `--request-id <UUID>` | stable UUID reused with the original write body |  |
| `--expected-revision <REVISION>` | reviewed scaling revision |  |
| `--expected-app-id <UUID>` | immutable app UUID required for writes |  |
| `--expected-environment-id <UUID>` | immutable environment UUID required for named writes |  |
| `--ram <MB>` | RAM in MB |  |
| `--cpu-millicores <MILLICORES>` | CPU cap: 250 or 500 millicores, or 1000 for none |  |
| `--max-concurrency <COUNT>` | most instances the app runs at once |  |
| `--min <COUNT>` | minimum warm instance count, including zero |  |

Examples:

```sh
gregale app scaling my-api
gregale app scaling my-api --expected-app-id 612e5aba-cd70-49e7-b29a-e5f73c29dcf2 --expected-revision 3 --request-id 4ed5f921-9eae-4bd0-a7ab-f851d38f745b --ram 512
gregale app scaling my-api --receipt 4ed5f921-9eae-4bd0-a7ab-f851d38f745b
```

### app costs

Show this app&#39;s attributed usage costs and source coverage

`gregale app costs [--month <YYYY-MM>] [--json] [<app>]`

| Flag | Meaning | |
|---|---|---|
| `--month <YYYY-MM>` | UTC usage month (defaults to current) |  |
| `--json` | Print the machine-readable app cost report |  |

Examples:

```sh
gregale app costs my-api
gregale app costs my-api --month 2026-10 --json
```

### app rename

Rename an app; the old hostname stops routing

`gregale app rename [<app>] <new-slug>`

Examples:

```sh
gregale app rename my-api my-api-v2
```

### app restart

Restart from a snapshot or fresh configuration

`gregale app restart [status] [--project <PROJECT>] [--fresh] [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--project <PROJECT>` | restart every app of this project instead of one |  |
| `--fresh` | cold-boot replacements with current runtime configuration |  |
| `--wait` | wait for processing; requires --fresh |  |
| `--timeout <DURATION>` | client deadline (default 10m) |  |
| `--poll-interval <DURATION>` | status polling interval (default 2s) |  |
| `--json` | print the accepted ID or last observed restart receipt |  |

#### app restart status

Follow an accepted fresh restart without submitting another request

`gregale app restart status --wake-id <UUID> [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] [--json] [<app>]`

| Flag | Meaning | |
|---|---|---|
| `--wake-id <UUID>` | accepted fresh restart UUID | required |
| `--wait` | wait for processing completion, separately from application health |  |
| `--timeout <DURATION>` | client deadline (default 10m) |  |
| `--poll-interval <DURATION>` | status polling interval (default 2s) |  |
| `--json` | print the last observed restart receipt |  |

### app egress-allowlist

Inspect or update the outbound CIDR allowlist

#### app egress-allowlist show

Show the current app egress policy

`gregale app egress-allowlist show [<app>]`

#### app egress-allowlist add

Add one allowed destination

`gregale app egress-allowlist add [<app>] <cidr>`

#### app egress-allowlist remove

Remove one allowed destination

`gregale app egress-allowlist remove [<app>] <cidr>`

#### app egress-allowlist clear

Clear the configured egress policy

`gregale app egress-allowlist clear [<app>]`

### app egress-ports

Inspect or update the extra outbound TCP ports (Pro/Scale)

#### app egress-ports show

Show the current app egress policy

`gregale app egress-ports show [<app>]`

#### app egress-ports add

Add one allowed destination

`gregale app egress-ports add [<app>] <port>`

#### app egress-ports remove

Remove one allowed destination

`gregale app egress-ports remove [<app>] <port>`

#### app egress-ports clear

Clear the configured egress policy

`gregale app egress-ports clear [<app>]`

### app network

Inspect networking or manage private-network attachments

#### app network show

Show network configuration and captured upstream telemetry

`gregale app network show [<app>]`

#### app network doctor

Check observed network health without sending probe traffic

`gregale app network doctor [<app>]`

### app traffic-status

Show policy convergence and fresh gateway traffic wiring

`gregale app traffic-status [<app>]`


## invocation

Run apps and functions, inspect calls and wait for results

`gregale invocation [<subcommand>]`

### invocation run

Call an app or function once to smoke-test it

`gregale invocation run [--async] [--wait] [--timeout <DURATION>] [--interval <DURATION>] [--idempotency-key <KEY>] [--payload <J|@file|->] [--method <METHOD>] [--path <PATH>] [--on-success-webhook <ID>] [--on-failure-webhook <ID>] [--work-policy <NAME>] [--work-key <JSON>] [--work-fairness-key <JSON>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--async` | enqueue once and return status_url unless --wait |  |
| `--wait` | wait for the invocation result; implies --async |  |
| `--timeout <DURATION>` | local wait duration (default 0: until Ctrl-C); requires --wait |  |
| `--interval <DURATION>` | time between status checks (default 1s); requires --wait |  |
| `--idempotency-key <KEY>` | stable key for retrying the same submission within server retention; requires --async or --wait |  |
| `--payload <J|@file|->` | JSON payload (inline \| @file \| -) |  |
| `--method <METHOD>` | HTTP method (default POST, or the function handler&#39;s) |  |
| `--path <PATH>` | URL path (default /, or the function handler&#39;s) |  |
| `--on-success-webhook <ID>` | app webhook id for completed invocation callbacks |  |
| `--on-failure-webhook <ID>` | app webhook id for failed or dead-lettered callbacks |  |
| `--work-policy <NAME>` | named app work policy for async invocation |  |
| `--work-key <JSON>` | JSON scalar application key for async invocation |  |
| `--work-fairness-key <JSON>` | JSON scalar fairness group for async invocation |  |

Examples:

```sh
gregale invocation run api --wait --timeout 2m --payload @request.json
```

Reuse an idempotency key only with the same async request within the server&#39;s 24-hour replay window. Use a new key for new work.

### invocation watch

Watch async invocations finish across your account in real time

`gregale invocation watch [--app <slug>] [--include-stateless] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | filter to a single app slug (optional) |  |
| `--include-stateless` | also print stateless.advisory frames (default: hide) |  |

This shows invocation results, not app output or HTTP requests; for those, run `gregale logs --follow`.

### invocation list

List invocations

`gregale invocation list [--limit <N>] [--cursor <CURSOR>] [--before <CURSOR>] [--all] [--stream] [--output <PATH>] [--progress <MODE>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1-100, default 50) |  |
| `--cursor <CURSOR>` | opaque cursor from a prior page |  |
| `--before <CURSOR>` | alias for --cursor |  |
| `--all` | walk every page using --limit and --cursor |  |
| `--stream` | emit JSON Lines as pages arrive; stdout requires --json --all |  |
| `--output <PATH>` | publish a new JSON Lines file after all pages complete; requires --all |  |
| `--progress <MODE>` | export progress on stderr; requires --all --output | one of `off` · `plain` · `ndjson` |
| `--timeout <DURATION>` | overall list read budget including page requests and retries (positive duration) |  |

### invocation get

Show or recover one invocation

`gregale invocation get [--replay] [--replay-keyed] <id>`

| Flag | Meaning | |
|---|---|---|
| `--replay` | re-issue failed unkeyed work |  |
| `--replay-keyed` | recover failed keyed work in its captured policy lane |  |

### invocation wait

Wait for one invocation to finish (exit 124 on timeout, 130 on Ctrl-C)

`gregale invocation wait [--timeout <D>] [--interval <D>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--timeout <D>` | stop waiting without canceling the invocation (0 waits indefinitely) |  |
| `--interval <D>` | time between status checks (default 1s) |  |


## function

Generate typed clients for calls between a project&#39;s functions

`gregale function [<subcommand>]`

### function types

Write each function&#39;s typed client from its project&#39;s schemas

`gregale function types [--path <DIR>] [--check]`

| Flag | Meaning | |
|---|---|---|
| `--path <DIR>` | functions project directory (default: the current one) |  |
| `--check` | write nothing; exit 1 when a generated file is missing or out of date |  |

Examples:

```sh
gregale function types
gregale function types --check
```

Node functions get gregale-functions.d.ts and Python functions get gregale_functions.py in their root, typed for the functions they may call.

gregale deploy refreshes files that already exist when a schema changed; --check fails without writing, for CI.


## env

Set, list and sync an app&#39;s sealed variables

`gregale env [<subcommand>]`

### env set

Set a sealed secret

`gregale env set [--wait-for-ack] [--timeout <DURATION>] [--dry-run] [--app <slug>] [--interactive] [--from-stdin] [--file <PATH>] [--env <SCOPE>] [--class <CLASS>] [--restart] [--no-guard] [--plain] [<slug>] [<KEY=VALUE>...]`

| Flag | Meaning | |
|---|---|---|
| `--wait-for-ack` | wait for application acknowledgements for all updated keys |  |
| `--timeout <DURATION>` | restart and acknowledgement wait limit after saving (default 2m; requires --wait-for-ack) |  |
| `--dry-run` | validate local input and preview keys and runtime options without API requests or changes |  |
| `--app <slug>` | app slug (defaults to linked project app) |  |
| `--interactive` | enter hidden values and confirm names, scope, and retention before saving |  |
| `--from-stdin` | read KEY=VALUE pairs from stdin |  |
| `--file <PATH>` | read a private KEY=VALUE file; excludes stdin, interactive input, and positional pairs |  |
| `--env <SCOPE>` | env scope to write (defaults to linked project environment) (also --scope) |  |
| `--class <CLASS>` | retention: persistent by default; ephemeral values make every wake a slower cold start; omission keeps an existing class | one of `persistent` · `ephemeral` |
| `--restart` | restart the app and apply updated secrets now |  |
| `--no-guard` | do not open a config guard for this change |  |
| `--plain` | store KEY=VALUE as a readable, non-secret variable (shown by env list) |  |

Examples:

```sh
gregale env set --file secrets.env --dry-run
gregale env set --app my-api --interactive
gregale env set --app my-api DATABASE_URL="$DATABASE_URL"
printf '%s\n' "DATABASE_URL=$DATABASE_URL" | gregale env set --app my-api --from-stdin
gregale env set --app my-api DATABASE_URL="$DATABASE_URL" --restart
gregale env set --app my-api SESSION_TOKEN="$SESSION_TOKEN" --class ephemeral
```

### env unset

Remove named sealed secrets (alias: rm)

`gregale env unset [--dry-run] [--interactive] [--app <slug>] [--env <SCOPE>] [--restart] [--wait-for-ack] [--timeout <DURATION>] [--no-guard] [<KEY>...]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | validate local input and preview keys and runtime options without API requests or changes |  |
| `--interactive` | choose a secret name, review scope and runtime options, and confirm removal |  |
| `--app <slug>` | app slug (defaults to linked project app) |  |
| `--env <SCOPE>` | env scope to delete from (defaults to linked project environment) (also --scope) |  |
| `--restart` | restart the app so running instances drop the removed secret now |  |
| `--wait-for-ack` | wait until every active authorized runtime confirms it removed the secret |  |
| `--timeout <DURATION>` | maximum time to wait for runtime acknowledgements (requires --wait-for-ack) |  |
| `--no-guard` | do not open a config guard for this change |  |

Examples:

```sh
gregale env unset OLD_TOKEN UNUSED_KEY --dry-run
gregale env unset --app my-api --interactive
gregale env unset --app my-api OLD_API_KEY
gregale env unset --app my-api OLD_API_KEY --env staging
gregale env unset --app my-api OLD_API_KEY --wait-for-ack
gregale env unset --app my-api OLD_API_KEY --restart
```

### env list

List sealed secrets

`gregale env list [--key <KEY>]... [--follow] [--interval <DURATION>] [--timeout <DURATION>] [--delivery-status <STATUS>] [--app <slug>] [--env <SCOPE|__all__>] [--class <CLASS>] [--older-than <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--key <KEY>` | include an exact secret key name (repeatable) |  |
| `--follow` | follow metadata changes; JSON emits snapshot/change/unavailable events (also --watch) |  |
| `--interval <DURATION>` | watch poll interval (1s to 1h; default 5s) |  |
| `--timeout <DURATION>` | watch duration (zero until interrupted); expiry exits 124 |  |
| `--delivery-status <STATUS>` | filter runtime-start delivery; unknown includes missing or inconsistent current-version evidence | one of `delivered` · `pending` · `failed` · `unknown` |
| `--app <slug>` | app slug (defaults to linked project app) |  |
| `--env <SCOPE|__all__>` | env scope filter (defaults to linked project environment) (also --scope) |  |
| `--class <CLASS>` | filter by snapshot-retention class | one of `persistent` · `ephemeral` |
| `--older-than <DURATION>` | filter to secrets not updated within a duration (for example 90d or 2160h); unknown timestamps are excluded |  |

Examples:

```sh
gregale env list --app my-api
gregale env list --app my-api --env __all__
gregale env list --app my-api --class ephemeral
gregale env list --app my-api --older-than 90d
```

Delivery counts describe runtime-start delivery; runtime reload and application acknowledgement are separate evidence in each row, and delivery does not confirm that the application adopted a value.

### env rotate

Rotate a secret and optionally wait for runtime application

`gregale env rotate [--dry-run] [--app <slug>] [--from-stdin] [--env <SCOPE>] [--restart] [--wait-for-ack] [--timeout <DURATION>] [<KEY=VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | validate local input and preview keys and runtime options without API requests or changes |  |
| `--app <slug>` | app slug (defaults to linked project app) |  |
| `--from-stdin` | read one KEY=VALUE pair from stdin |  |
| `--env <SCOPE>` | env scope to rotate (defaults to linked project environment) (also --scope) |  |
| `--restart` | restart the app and apply the rotated secret now |  |
| `--wait-for-ack` | wait until every active authorized runtime confirms it applied the secret; needs com.gregale.secret-reload-signal, even with --restart |  |
| `--timeout <DURATION>` | maximum time to wait for application acknowledgements (requires --wait-for-ack) |  |

Examples:

```sh
printf '%s\n' "DATABASE_URL=$DATABASE_URL" | gregale env rotate --app my-api --from-stdin --restart --wait-for-ack
printf '%s\n' "DATABASE_URL=$DATABASE_URL" | gregale env rotate --app my-api --from-stdin --env production --restart --wait-for-ack --timeout 5m
```

### env audit

Audit secret update age without exposing values

`gregale env audit --older-than <DURATION> [--fail-on-stale]`

| Flag | Meaning | |
|---|---|---|
| `--older-than <DURATION>` | required threshold based on when Gregale last updated the value, not provider rotation time (for example 90d or 2160h) | required |
| `--fail-on-stale` | exit non-zero when any secret exceeds the age threshold |  |

Examples:

```sh
gregale env audit --older-than 90d
gregale env audit --older-than 90d --fail-on-stale --json
```

Reports unknown timestamps too.

### env check

Check required secrets and delivery evidence without values

`gregale env check [--app <slug>] [--env <SCOPE>] [--required <KEY>]... [--required-file <PATH>] [--missing-template <PATH>] [--environments <SCOPE,...|saved>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked app) |  |
| `--env <SCOPE>` | single environment scope (defaults to linked environment) (also --scope) |  |
| `--required <KEY>` | required secret key name (repeatable) |  |
| `--required-file <PATH>` | explicit JSON array of required keys; overrides project discovery |  |
| `--missing-template <PATH>` | create a new private dotenv file containing only missing required names with empty values |  |
| `--environments <SCOPE,...|saved>` | compare required names across explicit scopes or saved local scopes; excludes scope/template flags |  |

Examples:

```sh
gregale env check
gregale env check --app my-api --required DATABASE_URL
gregale env check --required-file .gregale/required-secrets.json
```

### env ref

Manage destination-to-source names in a registered environment

#### env ref list

List reference names and shared environment-key quota

`gregale env ref list --app <slug> --env <ENV>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--env <ENV>` | registered project environment (also --environment) | required |

Examples:

```sh
gregale env ref list --app my-api --env production
```

#### env ref set

Select an existing scoped secret; respects Git field ownership

`gregale env ref set --app <slug> --env <ENV> <KEY=secret:NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--env <ENV>` | registered project environment (also --environment) | required |

Examples:

```sh
gregale env ref set --app my-api --env production DATABASE_URL=secret:DATABASE_PRIMARY
```

#### env ref unset

Suppress a secret destination and keep the sealed source

`gregale env ref unset --app <slug> --env <ENV> <KEY>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--env <ENV>` | registered project environment (also --environment) | required |

Examples:

```sh
gregale env ref unset --app my-api --env production DATABASE_URL
```

Applies to a primary workload destination.

### env requirement

Manage local required secret names without reading or changing values

#### env requirement check

Compare required names with a dotenv example offline

`gregale env requirement check [--from-env-file <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--from-env-file <PATH>` | dotenv example (default: .env.example at the requirements project root); compare names only |  |

Examples:

```sh
gregale env requirement check
gregale env requirement check --from-env-file config/example.env
```

Exits 1 on differences or a missing declaration.

#### env requirement set

Replace the project declaration with validated, deduplicated names

`gregale env requirement set [--dry-run] [--from-env-file <PATH>] [<KEY>...]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview current/proposed names and differences without writing |  |
| `--from-env-file <PATH>` | import key names from a local dotenv file; excludes positional names, never stores values |  |

Examples:

```sh
gregale env requirement set DATABASE_URL API_KEY --dry-run
gregale env requirement set --from-env-file .env.example --dry-run
```

#### env requirement create

Append required names, skipping duplicates

`gregale env requirement create [--dry-run] [--from-env-file <PATH>] [<KEY>...]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview added names without writing |  |
| `--from-env-file <PATH>` | merge key names from a local dotenv file; excludes positional names, never stores values |  |

Examples:

```sh
gregale env requirement create REDIS_URL --dry-run
gregale env requirement create --from-env-file .env.example --dry-run
```

#### env requirement delete

Remove declared names; unknown names fail without changes

`gregale env requirement delete [--dry-run] <KEY>...`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview removed names and explicit-empty outcome without writing |  |

Examples:

```sh
gregale env requirement delete OLD_API_KEY --dry-run
```

#### env requirement get

Show required names, declaration status, and source path

`gregale env requirement get`

#### env requirement clear

Remove the local declaration of required names

`gregale env requirement clear`

Without one, completeness is unknown.

### env pull

Pull sealed-secret keys to a .env skeleton (values blank)

`gregale env pull [--dry-run] [--app <slug>] [--env <SCOPE>] [-o <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview missing key skeletons and preserved local keys without writing files |  |
| `--app <slug>` | app slug (defaults to linked context) |  |
| `--env <SCOPE>` | env scope (defaults to linked project environment) (also --scope) |  |
| `-o <PATH>` | output file (default .env) |  |

Examples:

```sh
gregale env pull --app my-api
gregale env pull --app my-api --env staging
gregale env pull --app my-api --dry-run --json
```

### env push

Push KEY=VALUE pairs to sealed secrets

`gregale env push [--wait-for-ack] [--timeout <DURATION>] [--dry-run] [--app <slug>] [--env <SCOPE>] [-f <PATH>] [--from-stdin] [--restart] [--secret-scan <MODE>] [--no-guard]`

| Flag | Meaning | |
|---|---|---|
| `--wait-for-ack` | wait for application acknowledgements for uploaded keys |  |
| `--timeout <DURATION>` | restart and acknowledgement wait limit after uploading (default 2m; requires --wait-for-ack) |  |
| `--dry-run` | preview key changes, scan findings, and quota without uploading or restarting |  |
| `--app <slug>` | app slug (defaults to linked context) |  |
| `--env <SCOPE>` | env scope (defaults to linked project environment) (also --scope) |  |
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

`gregale env history [--app <slug>] [--env <SCOPE>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked context) |  |
| `--env <SCOPE>` | env scope (defaults to linked project environment) (also --scope) |  |
| `--limit <N>` | revisions to show (1-100, default 20) |  |

Examples:

```sh
gregale env history --app my-api
gregale env history --app my-api --env staging --json
```

### env rollback

Restore a scope&#39;s variables to a config revision

`gregale env rollback [--app <slug>] [--env <SCOPE>] [--yes] [--no-guard] <revision>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked context) |  |
| `--env <SCOPE>` | env scope (defaults to linked project environment) (also --scope) |  |
| `--yes` | skip the typed confirmation |  |
| `--no-guard` | do not open a config guard for the rollback |  |

Examples:

```sh
gregale env rollback 12 --app my-api
gregale env rollback 12 --app my-api --yes
```

Applied as one guarded change; differing secrets are listed, not restored.


## domain

Manage custom domains

`gregale domain [<subcommand>]`

### domain default

Set a verified domain as the app default

#### domain default set

Set a verified domain as the app default

`gregale domain default set <domain>`

### domain plan

Review declared domain bindings without changing them

`gregale domain plan --file <PATH> [--out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | domain manifest YAML or JSON | required |
| `--out <PATH>` | save a new reviewed JSON plan |  |

### domain apply

Atomically apply a reviewed domain plan

`gregale domain apply --plan <PATH> [--confirm] [--rollback-out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | reviewed plan JSON | required |
| `--confirm` | confirm every reviewed binding change |  |
| `--rollback-out <PATH>` | save previous bindings as a new manifest |  |

### domain move

Move a verified domain to a serving app or environment

`gregale domain move --app <SLUG> [--env <ENV>] [--dry-run] [--confirm] [--rollback-out <PATH>] <domain>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | destination app slug | required |
| `--env <ENV>` | destination environment; omitted selects default routing (also --environment) |  |
| `--dry-run` | review without applying |  |
| `--confirm` | confirm the move |  |
| `--rollback-out <PATH>` | save previous binding as a new manifest |  |

Keeps TXT proof, certificate state and wildcard issuance budgets. Use --dry-run to review or --confirm to commit.

### domain setup

Guide DNS setup and wait for verification and TLS

`gregale domain setup --app <SLUG> [--env <SLUG>] [--timeout <DURATION>] [--poll-interval <DURATION>] <domain>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app to attach to | required |
| `--env <SLUG>` | project environment to route to (also --environment) |  |
| `--timeout <DURATION>` | verification wait deadline (default 10m, maximum 1h) |  |
| `--poll-interval <DURATION>` | verification interval (5s..1m, default 10s) |  |

Examples:

```sh
gregale domain setup api.example.com --app my-api
```

Offers a default-domain change at the end.

### domain list

List custom domain bindings

`gregale domain list [--app <SLUG>] [--selector|-l <SELECTOR>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | only this app&#39;s domains |  |
| `--selector|-l <SELECTOR>` | only domains of apps whose labels match (preview) |  |

### domain create

Bind a custom domain to an app or project environment

`gregale domain create [--domain <DOMAIN>] --app <SLUG> [--env <SLUG>] [<domain>]`

| Flag | Meaning | |
|---|---|---|
| `--domain <DOMAIN>` | domain to attach (or the first argument) |  |
| `--app <SLUG>` | app slug to attach to | required |
| `--env <SLUG>` | project environment to route this domain to (also --environment) |  |

### domain delete

Remove a custom domain binding

`gregale domain delete [--yes] <domain>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | confirm removal without prompting |  |

### domain verify

Check DNS and certificate verification status

`gregale domain verify [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] <domain>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for verified ownership, correct routing, and HTTPS |  |
| `--timeout <DURATION>` | maximum wait (default 10m, maximum 1h) |  |
| `--poll-interval <DURATION>` | check interval (5s..1m, default 5s) |  |

Exits nonzero while pending.

Checks ownership and routing immediately; --wait waits for HTTPS readiness.

### domain get

Show a domain&#39;s cert details

`gregale domain get <domain>`

### domain status

Show TLS status for all domains, or one domain&#39;s conditions

`gregale domain status [<domain>]`

Examples:

```sh
gregale domain status
gregale domain status shop.example.com
```

With a domain, prints its Ready, Verified and CertificateReady conditions (preview).

### domain host

Choose which names below a wildcard domain get certificates

With an allowlist, the edge refuses certificates for every other name, so random names cannot use up the wildcard&#39;s issuance budget. Routing is unchanged.

#### domain host set

Allow only these names (replaces the list)

`gregale domain host set <*.domain> <name>...`

Examples:

```sh
gregale domain host set '*.example.com' app api eu.shop
```

#### domain host get

Show the allowlist

`gregale domain host get <*.domain>`

#### domain host clear

Remove the allowlist so every name may get a certificate

`gregale domain host clear <*.domain>`


## edge

Manage edge rules, rule lists, caching and CORS

`gregale edge [<subcommand>]`

### edge rule

Route, rewrite, protect or rate-limit an app&#39;s requests at the edge

`gregale edge rule [waf|attack-mode|recipe|list|summary|trace|create|get|update|delete|history|recipes|suggest|stats|review|events|rollback] [--app <slug>] [--kind <value>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (for subcommands that take one) |  |
| `--kind <value>` | rule kind | one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` · `respond` · `retry` · `circuit_breaker` · `waf` · `async` · `challenge` |

#### edge rule waf

review · update

`gregale edge rule waf <review|update>`

##### edge rule waf review

Group WAF detections by rule and path to find false positives

`gregale edge rule waf review [--app <slug>] [--rule <ID>] [--since <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project context) |  |
| `--rule <ID>` | only this kind=waf rule id |  |
| `--since <DURATION>` | how far back, e.g. 24h or 7d (default 7d, clamped to the plan window) |  |

Examples:

```sh
gregale edge rule waf review --app my-api
gregale edge rule waf review --app my-api --rule RULE_ID --since 24h
```

Each group is assessed: likely_false_positive (a documented false positive, or many clients flagged on one path), likely_attack (clients flagged on many paths), excluded or review.

Detections are sampled, so counts are lower bounds.

##### edge rule waf update

Add or remove CRS rule IDs on a kind=waf rule&#39;s exclusions

`gregale edge rule waf update [--add <IDS>] [--remove <IDS>] <rule-id>`

| Flag | Meaning | |
|---|---|---|
| `--add <IDS>` | CRS rule IDs to exclude, comma-separated |  |
| `--remove <IDS>` | CRS rule IDs to stop excluding, comma-separated |  |

Examples:

```sh
gregale edge rule waf update RULE_ID --add 942190
gregale edge rule waf update RULE_ID --remove 942100
```

Keeps the rest of the rule&#39;s WAF action and its penalty.

#### edge rule attack-mode

Switch attack mode on, off, or show it

`gregale edge rule attack-mode [set|get] [--app <slug>] [--for <DURATION>] [on|off|status]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project context) |  |
| `--for <DURATION>` | with on: how long attack mode stays on (default 1h, 15m to 24h) |  |

Examples:

```sh
gregale edge rule attack-mode set on --app my-api --for 1h
gregale edge rule attack-mode set --app my-api
gregale edge rule attack-mode set off --app my-api
```

Attack mode challenges every visitor to the app for a while (15m to 24h); clients that are not browsers need a verified API key or JWT.

##### edge rule attack-mode set

Switch attack mode on, off, or show it

`gregale edge rule attack-mode set [--app <slug>] [--for <DURATION>] [on|off|status]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project context) |  |
| `--for <DURATION>` | with on: how long attack mode stays on (default 1h, 15m to 24h) |  |

Examples:

```sh
gregale edge rule attack-mode set on --app my-api --for 1h
gregale edge rule attack-mode set --app my-api
gregale edge rule attack-mode set off --app my-api
```

Attack mode challenges every visitor to the app for a while (15m to 24h); clients that are not browsers need a verified API key or JWT.

##### edge rule attack-mode get

Switch attack mode on, off, or show it

`gregale edge rule attack-mode get [--app <slug>] [--for <DURATION>] [on|off|status]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project context) |  |
| `--for <DURATION>` | with on: how long attack mode stays on (default 1h, 15m to 24h) |  |

Examples:

```sh
gregale edge rule attack-mode set on --app my-api --for 1h
gregale edge rule attack-mode set --app my-api
gregale edge rule attack-mode set off --app my-api
```

Attack mode challenges every visitor to the app for a while (15m to 24h); clients that are not browsers need a verified API key or JWT.

#### edge rule recipe

Add a protection recipe&#39;s rule to an app in log mode

`gregale edge rule recipe [apply] [--app <slug>] [--host <HOST>] [--set <NAME=VALUE>] [--enforce] [--alert-webhook-url <URL>] [--alert-webhook-secret-stdin] [--alert-webhook-secret <VALUE>] [--alert-threshold <N>] <recipe>`

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
gregale edge rule recipe apply block-unverified-bots --app my-api
gregale edge rule recipe apply allow-ips-only --app my-api --set cidrs=203.0.113.7,198.51.100.0/24
```

Use --enforce to enforce it immediately.

##### edge rule recipe apply

Add a protection recipe&#39;s rule to an app in log mode

`gregale edge rule recipe apply [--app <slug>] [--host <HOST>] [--set <NAME=VALUE>] [--enforce] [--alert-webhook-url <URL>] [--alert-webhook-secret-stdin] [--alert-webhook-secret <VALUE>] [--alert-threshold <N>] <recipe>`

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
gregale edge rule recipe apply block-unverified-bots --app my-api
gregale edge rule recipe apply allow-ips-only --app my-api --set cidrs=203.0.113.7,198.51.100.0/24
```

Use --enforce to enforce it immediately.

#### edge rule list

List edge rules

`gregale edge rule list [--app <slug>] [--kind <value>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | filter to a single app slug |  |
| `--kind <value>` | filter to a single kind | one of `route` · `rewrite` · `redirect` · `headers` · `cors` · `jwt` · `ip` · `validate` · `limit` · `geo` · `maintenance` · `throttle` · `budget` · `cache` · `respond` · `retry` · `circuit_breaker` · `waf` · `async` · `challenge` |

#### edge rule summary

Show what the edge rejected for an app

`gregale edge rule summary --app <slug> [--range <WINDOW>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--range <WINDOW>` | time window (default 1h) | one of `5m` · `15m` · `1h` · `6h` · `24h` · `7d` · `15d` |

Includes pre-auth blocks, validation failures and gate rejections by status.

#### edge rule trace

Simulate composed edge-rule outcomes and request policies

`gregale edge rule trace [--config <file|->] [--app <slug>] [--url <URL>] [--method <method>] [--client-ip <IP>] [--country <CC>] [--asn <AS>] [--verified-bot] [--verified-ai-crawler] [--header <Name:Value>] [--body-file <path|->] [--proposal <file|->] [--add-rule <JSON|@FILE>] [--remove-rule <ID>]`

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
gregale edge rule trace --app my-api --url https://api.example.com/admin --add-rule '{"kind":"maintenance","match_host":"api.example.com","match_path":"/admin/*","action":{"maintenance":{"message":"migrating"}}}'
```

Covers budget, throttle, retry, circuit-breaker and async-route policy. --config loads reusable JSON scenarios; see the edge-rule-trace docs.

#### edge rule create

Add an edge rule

`gregale edge rule create --app <slug> --kind <KIND> --match-host <HOST> [--match-path <PATH>] [--match-method <METHOD>] [--match-header <Name=Value>] [--priority <N>] [--enabled[=true|false]] [--throttle-requests-per-second <RPS>] [--throttle-burst <N>] [--throttle-key-by <KEY>] [--redirect-status <CODE>] [--redirect-to <URL>] [--template] [--rewrite-from <PATH>] [--rewrite-to <PATH>] [--route-target-slug <slug>] [--cache-max-age-seconds <N>] [--cache-stale-while-revalidate-seconds <N>] [--budget-ms <MS>] [--retry-max-attempts <N>] [--circuit-failure-threshold <RATIO>] [--circuit-open-seconds <N>] [--respond-status <CODE>] [--respond-body <JSON>] [--ip-allow <CIDR>] [--ip-deny <CIDR>] [--geo-allow <CC>] [--geo-deny <CC>] [--jwt-issuer <ISSUER>] [--jwt-jwks-url <URL>] [--on-success-webhook <ID>] [--on-failure-webhook <ID>] [--async-max-attempts <N>] [--async-retry-base-seconds <N>] [--async-retry-max-seconds <N>] [--async-retry-jitter-seconds <N>] [--async-max-age-seconds <N>] [--waf-mode <MODE>] [--waf-paranoia-level <N>] [--waf-anomaly-threshold <N>] [--waf-exclude-rules <IDS>] [--waf-inspect-body-bytes <BYTES>] [--challenge-difficulty <BITS>] [--challenge-pass-ttl <SECONDS>] [--challenge-max-difficulty <BITS>] [--validate-schema <JSON|@FILE|->] [--validate-mode <MODE>] [--validate-content-type <TYPE>] [--validate-max-body-bytes <N>] [--validate-apply-while-streaming] [--validate-reject-unknown-fields] [--validate-path-template <TEMPLATE>] [--validate-path-schema <JSON|@FILE|->] [--validate-query-schema <JSON|@FILE|->] [--validate-headers-schema <JSON|@FILE|->] [--cors-allow-credentials] [--cors-max-age-seconds <SECONDS>] [--jwt-platform-tenant-external-ref-claim <CLAIM>] [--limit-max-body-bytes <BYTES>] [--limit-max-body-bytes-streaming <BYTES>] [--throttle-jwt-claim <CLAIM>] [--throttle-max-keys-per-rule <N>] [--throttle-missing-key-policy <POLICY>] [--throttle-key-field <FIELD>] [--throttle-count-status <CODE>] [--cache-stale-if-error-seconds <SECONDS>] [--total-deadline-ms <MS>] [--budget-allow-override-header <HEADER>] [--retry-allow-non-idempotent] [--retry-min-remaining-ms <MS>] [--retry-backoff-ms <MS>] [--retry-budget-percent <PERCENT>] [--retry-budget-min-retries <N>] [--circuit-min-requests <N>] [--circuit-window-seconds <SECONDS>] [--circuit-max-open-seconds <SECONDS>] [--maintenance-retry-after-seconds <SECONDS>] [--maintenance-message <TEXT>] [--match <JSON|@FILE|->] [--mode <MODE>] [--penalty-for <DURATION>] [--penalty-after <N>] [--penalty-within <DURATION>] [--cache-methods <METHOD>]... [--cache-vary-on <HEADER>]... [--cors-allow-header <HEADER>]... [--cors-allow-method <METHOD>]... [--cors-allow-origin <ORIGIN>]... [--cors-expose-header <HEADER>]... [--description <TEXT>] [--expires-at <RFC3339>] [--expires-in <DURATION>] [--headers-request-add <NAME:VALUE>]... [--headers-request-remove <NAME>]... [--headers-request-set <NAME:VALUE>]... [--headers-response-add <NAME:VALUE>]... [--headers-response-remove <NAME>]... [--headers-response-set <NAME:VALUE>]... [--jwt-algorithm <ALG>]... [--jwt-audience <AUDIENCE>]... [--jwt-require-exp] [--jwt-required-claim <NAME=VALUE>]... [--name <NAME>] [--redirect-header <NAME:VALUE>]...`

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
gregale edge rule create --app my-api --kind throttle --match-host my-api.gregale.app --match-path /search --throttle-requests-per-second 5 --throttle-burst 10
gregale edge rule create --app my-api --kind throttle --match-host my-api.gregale.app --match-path /login --throttle-requests-per-second 0.1 --throttle-burst 5 --throttle-key-by composite --throttle-key-field ip --throttle-key-field header:x-username --throttle-count-status 401
gregale edge rule create --app my-api --kind redirect --match-host my-api.gregale.app --match-path /old --redirect-status 308 --redirect-to https://my-api.gregale.app/new
gregale edge rule create --app my-api --kind cache --match-host my-api.gregale.app --match-path /catalog --cache-max-age-seconds 60
gregale edge rule create --app my-site --kind challenge --match-host @app --match-path '/*' --match '{"field":"verified_bot","op":"missing"}'
gregale edge rule create --app my-api --kind budget --match-host my-api.gregale.app --match-path /reports --budget-ms 20000
gregale edge rule create --app my-api --kind validate --match-host api.example.com --validate-schema @schema.json --validate-content-type application/json --validate-mode block
cat schema.json | gregale edge rule create --app my-api --kind validate --match-host api.example.com --validate-schema -
gregale edge rule create --app my-api --kind validate --match-host api.example.com --match-path '/users/?*' --match-method GET --validate-path-template '/users/{id}' --validate-path-schema '{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}' --validate-query-schema @query.json
```

#### edge rule get

Show one edge rule

`gregale edge rule get <id>`

#### edge rule update

Update one edge rule

`gregale edge rule update [--app <SLUG>] [--match-host <HOST>] [--match-path <PATH>] [--match-method <METHOD>]... [--match-header <NAME=VALUE>]... [--clear-match-headers] [--priority <N>] [--enable] [--disable] [--kind <KIND>] [--route-target-slug <SLUG>] [--rewrite-from <PATH>] [--rewrite-to <PATH>] [--redirect-status <CODE>] [--redirect-to <URL>] [--template] [--redirect-header <NAME:VALUE>]... [--headers-request-add <NAME:VALUE>]... [--headers-request-set <NAME:VALUE>]... [--headers-request-remove <NAME>]... [--headers-response-add <NAME:VALUE>]... [--headers-response-set <NAME:VALUE>]... [--headers-response-remove <NAME>]... [--cors-allow-origin <ORIGIN>]... [--cors-allow-method <METHOD>]... [--cors-allow-header <HEADER>]... [--cors-expose-header <HEADER>]... [--cors-allow-credentials] [--cors-max-age-seconds <SECONDS>] [--jwt-issuer <ISSUER>] [--jwt-jwks-url <URL>] [--jwt-audience <AUDIENCE>]... [--jwt-algorithm <ALG>]... [--jwt-required-claim <NAME=VALUE>]... [--jwt-platform-tenant-external-ref-claim <CLAIM>] [--ip-allow <CIDR>]... [--ip-deny <CIDR>]... [--geo-allow <CC>]... [--geo-deny <CC>]... [--limit-max-body-bytes <BYTES>] [--limit-max-body-bytes-streaming <BYTES>] [--throttle-requests-per-second <RPS>] [--throttle-burst <N>] [--throttle-key-by <KEY>] [--throttle-jwt-claim <CLAIM>] [--throttle-max-keys-per-rule <N>] [--throttle-missing-key-policy <POLICY>] [--throttle-key-field <FIELD>] [--throttle-count-status <CODE>] [--cache-max-age-seconds <SECONDS>] [--cache-stale-while-revalidate-seconds <SECONDS>] [--cache-stale-if-error-seconds <SECONDS>] [--cache-vary-on <HEADER>]... [--cache-methods <METHOD>]... [--budget-ms <MS>] [--total-deadline-ms <MS>] [--budget-allow-override-header <HEADER>] [--retry-max-attempts <N>] [--retry-allow-non-idempotent] [--retry-min-remaining-ms <MS>] [--retry-backoff-ms <MS>] [--retry-budget-percent <PERCENT>] [--retry-budget-min-retries <N>] [--circuit-failure-threshold <RATIO>] [--circuit-min-requests <N>] [--circuit-window-seconds <SECONDS>] [--circuit-open-seconds <SECONDS>] [--circuit-max-open-seconds <SECONDS>] [--maintenance-retry-after-seconds <SECONDS>] [--maintenance-message <TEXT>] [--respond-status <CODE>] [--respond-body <JSON>] [--on-success-webhook <ID>] [--on-failure-webhook <ID>] [--async-max-attempts <N>] [--async-retry-base-seconds <N>] [--async-retry-max-seconds <N>] [--async-retry-jitter-seconds <N>] [--async-max-age-seconds <N>] [--waf-mode <MODE>] [--waf-paranoia-level <N>] [--waf-anomaly-threshold <N>] [--waf-exclude-rules <IDS>] [--waf-inspect-body-bytes <BYTES>] [--challenge-difficulty <BITS>] [--challenge-pass-ttl <SECONDS>] [--challenge-max-difficulty <BITS>] [--validate-schema <JSON|@FILE|->] [--validate-mode <MODE>] [--validate-content-type <TYPE>] [--validate-max-body-bytes <N>] [--validate-apply-while-streaming] [--validate-reject-unknown-fields] [--validate-path-template <TEMPLATE>] [--validate-path-schema <JSON|@FILE|->] [--validate-query-schema <JSON|@FILE|->] [--validate-headers-schema <JSON|@FILE|->] [--match <JSON|@FILE|->] [--clear-match] [--mode <MODE>] [--penalty-for <DURATION>] [--penalty-after <N>] [--penalty-within <DURATION>] [--clear-penalty] [--clear-expiry] [--description <TEXT>] [--expires-at <RFC3339>] [--expires-in <DURATION>] [--jwt-require-exp] [--name <NAME>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app the rule belongs to (optional; checked before updating) |  |
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
gregale edge rule update RULE_ID --kind validate --validate-schema @schema.json --validate-mode block
gregale edge rule update RULE_ID --kind validate --validate-mode observe
```

#### edge rule delete

Delete one edge rule

`gregale edge rule delete [--yes] [--app <slug>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | skip the typed confirmation (alias: --quiet) |  |
| `--app <slug>` | require the rule to belong to this app |  |

#### edge rule history

List recorded versions of an app&#39;s edge rules

`gregale edge rule history --app <slug> [--version <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--version <N>` | show the rules recorded in this version |  |

--version N shows that version&#39;s rules.

#### edge rule recipes

List protection recipes: ready-made rules that start in log mode

`gregale edge rule recipes`

#### edge rule suggest

Suggest protection recipes or per-route rate limits from traffic

`gregale edge rule suggest [--app <slug>] [--rate-limits] [--range <WINDOW>] [--dry-run] [--candidate-rps <N>] [--candidate-burst <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project context) |  |
| `--rate-limits` | suggest per-route rate limits instead of protection recipes |  |
| `--range <WINDOW>` | rate limits: observation window (5m\|15m\|1h\|6h\|24h\|7d\|15d) | one of `5m` · `15m` · `1h` · `6h` · `24h` · `7d` · `15d` |
| `--dry-run` | rate limits: preview --candidate-rps against observed traffic |  |
| `--candidate-rps <N>` | rate limits: candidate rps for the dry-run preview |  |
| `--candidate-burst <N>` | rate limits: candidate burst for the dry-run preview |  |

Examples:

```sh
gregale edge rule suggest --app my-api
gregale edge rule suggest --app my-api --rate-limits --range 24h
gregale edge rule suggest --app my-api --rate-limits --dry-run --candidate-rps 50
```

Prints the command to add each.

With --rate-limits, suggests per-route rate limits and can preview a candidate limit against observed traffic.

#### edge rule stats

Show match counts per rule

`gregale edge rule stats --app <slug> [--window <WINDOW>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--window <WINDOW>` | window | one of `1h` · `24h` · `7d` |

Enforced rules count matches; log-mode rules count logged requests.

#### edge rule review

Show which log-mode rules are ready to enforce

`gregale edge rule review [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to linked project context) |  |

Examples:

```sh
gregale edge rule review --app my-api
```

Also lists rules still collecting or quiet for 7 days, with the command for each.

#### edge rule events

Show sampled requests that rules matched, newest first

`gregale edge rule events --app <slug> [--rule <ID>] [--request-id <ID>] [--outcome <OUTCOME>] [--since <DURATION>] [--limit <N>] [--cursor <CURSOR>]`

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
gregale edge rule events --app my-api --outcome logged --since 7d
gregale edge rule events --app my-api --outcome blocked
gregale edge rule events --app my-site --request-id REQUEST_ID
```

Includes kind=waf detections; up to 10 of each per rule per minute, kept 7 days.

#### edge rule rollback

Restore an app&#39;s edge rules to a recorded version

`gregale edge rule rollback --app <slug> --to <N> [--yes]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--to <N>` | version to restore | required |
| `--yes` | skip the typed confirmation (alias: --quiet) |  |

Examples:

```sh
gregale edge rule rollback --app my-api --to 12
```

The restore is recorded as a new version.

### edge rule-list

Manage reusable IP, country, host and text lists for edge rules

#### edge rule-list list

List the account&#39;s edge-rule lists

`gregale edge rule-list list`

#### edge rule-list get

Show one list with its items

`gregale edge rule-list get <name>`

#### edge rule-list create

Create a list

`gregale edge rule-list create --kind <KIND> [--item <VALUE>] [--items-file <path|->] [--description <TEXT>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--kind <KIND>` | list kind | required; one of `ip` · `country` · `host` · `string` · `asn` |
| `--item <VALUE>` | list item (repeat) |  |
| `--items-file <path|->` | file with one item per line (# comments allowed), or - for stdin |  |
| `--description <TEXT>` | free-text description |  |

Examples:

```sh
gregale edge rule-list create office-ips --kind ip --item 203.0.113.0/24 --item 2001:db8::1
gregale edge rule-list create blocked --kind country --items-file countries.txt
gregale edge rule create --app my-api --kind throttle --match-host api.example.com --throttle-requests-per-second 5 --throttle-burst 10 --match '{"not":{"field":"client_ip","op":"in_list","list":"office-ips"}}'
```

#### edge rule-list update

Edit a list; referencing rules pick up the change within seconds

`gregale edge rule-list update [--add <VALUE>] [--remove <VALUE>] [--replace-file <path|->] [--description <TEXT>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--add <VALUE>` | item to add (repeat) |  |
| `--remove <VALUE>` | item to remove (repeat) |  |
| `--replace-file <path|->` | replace every item from a file, or - for stdin |  |
| `--description <TEXT>` | new description |  |

#### edge rule-list delete

Delete a list no rule references

`gregale edge rule-list delete [--yes] <name>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | accepted for consistency with edge rule delete; does not prompt |  |

### edge cache

Cache an app&#39;s responses at the edge, or purge them

#### edge cache purge

Purge cached responses for one app

`gregale edge cache purge [--app <SLUG>] [--path <GLOB>] [--tag <TAG>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug (defaults to linked project context) |  |
| `--path <GLOB>` | optional normalized request path glob |  |
| `--tag <TAG>` | optional cache tag |  |

#### edge cache set

Cache GET responses for a route

`gregale edge cache set --method <GET|HEAD> <path> for <duration>`

| Flag | Meaning | |
|---|---|---|
| `--method <GET|HEAD>` | GET: cache GET responses for a route (with it: --app, --host, --stale-while-revalidate, --stale-if-error, --vary-on, --priority); HEAD: cache HEAD responses for a route (with it: --app, --host, --stale-while-revalidate, --stale-if-error, --vary-on, --priority) | required |

### edge cors

Configure CORS for an app

#### edge cors allow

Attach a CORS rule to an app

`gregale edge cors allow [--method <VERB>] [--credentials] [--max-age <N>] [--host <HOST>] <slug> <origin> [<origin>...]`

| Flag | Meaning | |
|---|---|---|
| `--method <VERB>` | allowed method (repeat) |  |
| `--credentials` | enable Access-Control-Allow-Credentials |  |
| `--max-age <N>` | Access-Control-Max-Age in seconds (default 600) |  |
| `--host <HOST>` | match host (default: the app&#39;s first verified custom domain) |  |

Examples:

```sh
gregale edge cors allow my-api https://app.example.com --method GET --method POST
```

#### edge cors list

List an app&#39;s CORS rules

`gregale edge cors list [<slug>]`

The app defaults to the linked one.

#### edge cors delete

Delete a CORS rule by id

`gregale edge cors delete [<slug>] <rule-id>`

#### edge cors get

Show per-app default CORS + active rules (defaults to linked context)

`gregale edge cors get [<slug>]`


## postgres

Create, attach and restore managed PostgreSQL databases

`gregale postgres [<subcommand>]`

### postgres overage

Show or change compute overage past the monthly allowance

`gregale postgres overage <get|set> [on|off]`

Examples:

```sh
gregale postgres overage get
gregale postgres overage set on
```

With overage on, databases keep running up to twice the allowance.

#### postgres overage get

Show whether databases keep running past the monthly compute allowance

`gregale postgres overage get`

Examples:

```sh
gregale postgres overage get
```

With overage on, databases keep running up to twice the allowance.

#### postgres overage set

Turn compute overage on or off (on: databases run up to twice the allowance)

`gregale postgres overage set [on|off]`

Examples:

```sh
gregale postgres overage set on
```

With overage on, databases keep running up to twice the allowance.

### postgres compute-policy

Durably switch between idle suspension and always-on compute

#### postgres compute-policy set

Durably switch between idle suspension and always-on compute

`gregale postgres compute-policy set --scale-to-zero <BOOL> --request-id <UUID> <database>`

| Flag | Meaning | |
|---|---|---|
| `--scale-to-zero <BOOL>` | true for idle suspension, false for always-on | required; one of `true` · `false` |
| `--request-id <UUID>` | stable request UUID; reuse after uncertain responses | required |

#### postgres compute-policy status

Read compute policy change progress

`gregale postgres compute-policy status <database> <request_uuid>`

### postgres resize

Durably resize compute; clients may disconnect

`gregale postgres resize [status] --class <CLASS> --request-id <UUID> <database>`

| Flag | Meaning | |
|---|---|---|
| `--class <CLASS>` | target service class | required; one of `development` · `burstable` · `production` |
| `--request-id <UUID>` | stable request UUID; reuse after uncertain responses | required |

#### postgres resize status

Read compute resize progress

`gregale postgres resize status <database> <request_uuid>`

### postgres list

List managed PostgreSQL databases

`gregale postgres list [--selector|-l <SELECTOR>]`

| Flag | Meaning | |
|---|---|---|
| `--selector|-l <SELECTOR>` | only databases whose labels match (preview) |  |

### postgres capabilities

Show plan and region PostgreSQL feature support

`gregale postgres capabilities [--region <REGION>]`

| Flag | Meaning | |
|---|---|---|
| `--region <REGION>` | region (defaults to configured region) |  |

Examples:

```sh
gregale postgres capabilities --json
```

### postgres usage

Show monthly managed PostgreSQL usage and guardrail state

### postgres create

Create a managed PostgreSQL database

`gregale postgres create --region <REGION> [--postgres-major <N>] [--class <CLASS>] [--availability <MODE>] [--scale-to-zero] [--storage-bytes <N>] [--restore-window-seconds <N>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--region <REGION>` | provider-neutral region | required |
| `--postgres-major <N>` | PostgreSQL major version |  |
| `--class <CLASS>` | service class | one of `development` · `burstable` · `production` |
| `--availability <MODE>` | availability mode | one of `single_zone` · `high_availability` |
| `--scale-to-zero` | suspend compute when idle |  |
| `--storage-bytes <N>` | storage limit in bytes |  |
| `--restore-window-seconds <N>` | point-in-time restore window |  |

Examples:

```sh
gregale postgres create orders --region us-east-2 --class development --scale-to-zero
```

### postgres get

Show one managed PostgreSQL database

`gregale postgres get <database>`

### postgres delete

Delete a managed PostgreSQL database; it can be undone for 7 days

`gregale postgres delete [--purge] [--yes] <database>`

| Flag | Meaning | |
|---|---|---|
| `--purge` | delete at once with its restore history; cannot be undone |  |
| `--yes` | skip the typed confirmation (alias: --quiet) |  |

Examples:

```sh
gregale postgres delete orders
gregale postgres delete orders --purge
```

The database stops at once; `gregale postgres recover` brings it back until its restorable_until.

A deleted database still counts toward the plan&#39;s database limit until it is purged.

### postgres recover

Bring back a deleted managed PostgreSQL database

`gregale postgres recover <database>`

Examples:

```sh
gregale postgres recover orders
```

Works until the database&#39;s restorable_until, 7 days after the delete; a purged database is gone.

### postgres restore

Restore a database to a new database

`gregale postgres restore --name <NAME> --point-in-time <TIMESTAMP> [--switch <APP>] [--env <SCOPE>] [--yes] [--no-wait] [--timeout <DURATION>] [--poll-interval <DURATION>] <database>`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | name for the restored database | required |
| `--point-in-time <TIMESTAMP>` | RFC3339 restore timestamp | required |
| `--switch <APP>` | move this app to the copy, keeping its code |  |
| `--env <SCOPE>` | with --switch: environment whose bindings move |  |
| `--yes` | with --switch: switch without prompting |  |
| `--no-wait` | with --switch: return once the switch starts |  |
| `--timeout <DURATION>` | with --switch: wait deadline |  |
| `--poll-interval <DURATION>` | with --switch: poll interval |  |

Examples:

```sh
gregale postgres restore orders --name orders-before-import --point-in-time 2026-10-30T09:00:00Z
gregale postgres restore orders --name orders-0900 --point-in-time 2026-10-30T09:00:00Z --switch my-api
```

With --switch the app moves to the copy without changing its code: it stops serving while it is fenced and drained, then cold starts on the copy.

Writes after the restore point stay only in the source database, which keeps every write.

Follow or cancel the switch with `gregale release rollback status &lt;app&gt; &lt;switch-id&gt;`.

### postgres branch

Make, list and delete copy-on-write branches of a database

Examples:

```sh
gregale postgres branch orders pr-42 --ttl 2h
gregale postgres attach pr-42 my-api-pr-42 --env DATABASE_URL
gregale postgres branch delete orders pr-42 --yes
```

A branch is a restore: a new database that shares its source&#39;s storage until written to. `gregale postgres branch &lt;database&gt; &lt;name&gt;` is `branch create`.

It counts toward the plan&#39;s database limit like any restore, and may take the one restore slot past it.

With --ttl the branch&#39;s bindings are revoked and it is purged when the TTL ends; the source is never touched.

#### postgres branch create

Branch a database from its latest point or a chosen time

`gregale postgres branch create [--since <live|TIMESTAMP>] [--ttl <DURATION>] <database> <name>`

| Flag | Meaning | |
|---|---|---|
| `--since <live|TIMESTAMP>` | live (default) or an RFC3339 time inside the source&#39;s restore range (also --from) |  |
| `--ttl <DURATION>` | purge the branch this long after it is created, from 5m to 30d (e.g. 2h, 7d) |  |

#### postgres branch list

List a database&#39;s branches

`gregale postgres branch list <database>`

#### postgres branch delete

Purge a branch; refuses databases that are not its branches

`gregale postgres branch delete [--yes] <database> <name>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | skip the typed confirmation (alias: --quiet) |  |

### postgres bindings

Manage app database bindings

#### postgres bindings list

List a database&#39;s bindings

`gregale postgres bindings list <database>`

#### postgres bindings create

Bind a database to an app by app ID (attach takes slugs)

`gregale postgres bindings create --app <APP_ID> --env <SCOPE> --environment-key <KEY> [--access <MODE>] <database_id>`

| Flag | Meaning | |
|---|---|---|
| `--app <APP_ID>` | app ID | required |
| `--env <SCOPE>` | environment scope (also --scope) | required |
| `--environment-key <KEY>` | environment variable name | required |
| `--access <MODE>` | credential access | one of `read_write` · `read_only` · `migration` · `data_api` · `session` |

#### postgres bindings get

Show one binding

`gregale postgres bindings get <id>`

#### postgres bindings delete

Delete a binding

`gregale postgres bindings delete <id>`

#### postgres bindings rotate

Rotate a binding, optionally waiting for the old credential

`gregale postgres bindings rotate [--wait] [--wait-timeout <DURATION>] [--poll-interval <DURATION>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the previous credential to retire |  |
| `--wait-timeout <DURATION>` | maximum time to wait for rotation (default 15m) |  |
| `--poll-interval <DURATION>` | status polling interval while waiting (default 1s) |  |

Examples:

```sh
gregale postgres bindings rotate BINDING_ID --wait
```

Waits until the previous credential retires.

### postgres attach

Attach a database to an app

`gregale postgres attach [--scope <SCOPE>] [--env <KEY>] [--access <MODE>] <database> <app>`

| Flag | Meaning | |
|---|---|---|
| `--scope <SCOPE>` | environment scope (defaults to linked project environment, otherwise default) |  |
| `--env <KEY>` | connection environment variable |  |
| `--access <MODE>` | credential access | one of `read_write` · `read_only` · `migration` · `data_api` · `session` |

Examples:

```sh
gregale postgres attach orders my-api --env DATABASE_URL --access read_write
```

### postgres query

Run one SQL query and print rows; read-only by default

`gregale postgres query [--file <PATH>] [--write] [--read-only] [--max-rows <N>] [--timeout <D>] [--app <SLUG>] [--env <KEY>] [--access <MODE>] [--wait-timeout <D>] <database> [<query>]`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | read the query from PATH, or - for stdin |  |
| `--write` | allow writes and several statements (commits) |  |
| `--read-only` | refuse writes (the default) |  |
| `--max-rows <N>` | rows kept per result, 1..10000 (default 500) |  |
| `--timeout <D>` | statement timeout (default 30s) |  |
| `--app <SLUG>` | bound app whose credentials to use (default: the best ready binding) |  |
| `--env <KEY>` | binding environment key |  |
| `--access <MODE>` | binding access (default prefers read_only for reads, read_write for --write) | one of `read_only` · `session` · `read_write` · `migration` |
| `--wait-timeout <D>` | maximum time to wait for each task VM to start |  |

Examples:

```sh
gregale postgres sql orders "select status, count(*) from orders group by 1"
gregale postgres sql orders --file audit.sql --json
gregale postgres sql orders --write "update orders set status = 'void' where id = 42"
```

Uses the same tunnel as `postgres connect` (credentials never print) and needs no local psql.

Read-only by default: one statement in a READ ONLY transaction that is always rolled back, on a read_only binding when there is one.

--write runs the text as a script on a writable binding; it commits. To try a destructive change first, run it on a copy-on-write branch: `gregale postgres branch &lt;database&gt; &lt;name&gt; --ttl 2h`.

--json prints typed rows: numbers, booleans and json stay typed; NULL is null; other values are PostgreSQL text.


## bucket

Manage object storage buckets

`gregale bucket [<subcommand>]`

### bucket multipart

Inspect owned multipart upload sessions and parts

#### bucket multipart get

Inspect a multipart session returned by an upload

`gregale bucket multipart get <app> <bucket-id> <upload-id>`

#### bucket multipart cancel

Abort an unfinished multipart session, freeing the bucket it holds

`gregale bucket multipart cancel <app> <bucket-id> <upload-id>`

#### bucket multipart list

List multipart sessions

`gregale bucket multipart list [--limit <N>] [--cursor <ID>] <app> <bucket-id>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1..1000) |  |
| `--cursor <ID>` | next page cursor |  |

#### bucket multipart parts

List uploaded multipart parts

`gregale bucket multipart parts [--limit <N>] [--part-number-marker <N>] <app> <bucket-id> <upload-id>`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1..1000) |  |
| `--part-number-marker <N>` | last part from the previous page |  |

### bucket deletion

Create or inspect durable object deletions

#### bucket deletion create

Delete current data or an owned version; retry with the same UUID

`gregale bucket deletion create <app> <bucket-id> <key> <request-uuid> [version-id|null]`

#### bucket deletion get

Show a persisted deletion receipt

`gregale bucket deletion get <app> <bucket-id> <request-id>`

### bucket inventory

Start, inspect or cancel a fenced capacity inventory

#### bucket inventory get

Show reconciliation progress and reclaimed capacity

`gregale bucket inventory get <app> <bucket-id> <job-id>`

#### bucket inventory start

Pause writes and request capacity reconciliation

`gregale bucket inventory start <app> <bucket-id>`

#### bucket inventory cancel

Cancel reconciliation and resume writes

`gregale bucket inventory cancel <app> <bucket-id> <job-id>`

### bucket copy-source

Manage copy-only owned source grants

#### bucket copy-source list

List source grants for a destination credential

`gregale bucket copy-source list <app> <bucket-id> <credential-id>`

#### bucket copy-source grant

Allow copying an owned source bucket or prefix

`gregale bucket copy-source grant <app> <bucket-id> <credential-id> <source-bucket-id> [prefix]`

#### bucket copy-source revoke

Prevent new copy dispatch from a source

`gregale bucket copy-source revoke <app> <bucket-id> <credential-id> <source-bucket-id>`

### bucket usage

Account object storage observations, safety policy and billing state

### bucket list

List an app&#39;s object-storage buckets and their IDs

`gregale bucket list <app>`

### bucket delete

Delete an empty bucket (including retained versions)

`gregale bucket delete [--yes] <app> <bucket-id>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | skip the typed confirmation (alias: --quiet) |  |

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

### bucket sign

Mint a short-lived signed URL to download or upload one object

`gregale bucket sign [--method <METHOD>] [--expires <DURATION>] [--size-bytes <N>] [--content-type <TYPE>] <app> <bucket-id> <key>`

| Flag | Meaning | |
|---|---|---|
| `--method <METHOD>` | GET to download (default), PUT to upload | one of `GET` · `PUT` |
| `--expires <DURATION>` | URL lifetime, at most 15m (default 5m) |  |
| `--size-bytes <N>` | exact upload size; required with --method PUT |  |
| `--content-type <TYPE>` | upload content type |  |

Examples:

```sh
gregale bucket sign my-api 4f2c… v1/stream/segment.gz --expires 15m
gregale bucket sign my-api 4f2c… avatars/42.png --method PUT --size-bytes 18231 --content-type image/png
```

Send the URL with exactly the printed headers and no Gregale credentials; it needs no S3 SDK.

A PUT URL writes once, and each URL reserves storage when issued, so do not retry signing automatically.

### bucket download

Download an object to a file after a complete transfer

`gregale bucket download [--version-id <VERSION>] [--force] [--timeout <DURATION>] <app> <bucket-id> <key> <file>`

| Flag | Meaning | |
|---|---|---|
| `--version-id <VERSION>` | download this owned immutable version |  |
| `--force` | replace destination after a complete transfer |  |
| `--timeout <DURATION>` | transfer deadline (default 30m) |  |

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


## export

Verify and compare saved JSON Lines exports offline

`gregale export [<subcommand>]`

### export verify

Check a file against its saved completion receipt

`gregale export verify --receipt <RECEIPT> <FILE>`

| Flag | Meaning | |
|---|---|---|
| `--receipt <RECEIPT>` | saved JSON completion receipt | required |

Examples:

```sh
gregale export verify builds.jsonl --receipt builds.receipt.json
gregale --json export verify invocations.jsonl --receipt invocations.receipt.json
```

Runs offline without login. Compares SHA-256, exact bytes and JSON Lines record count; the receipt path is ignored.

Exit 0 means all three checks matched; exit 1 means mismatch or invalid input. A valid comparison prints verified true or false; invalid input reports an error without a result.

A receipt is an integrity reference, not a signature or a point-in-time API snapshot.

### export diff

Compare saved JSON Lines records by a unique key

`gregale export diff [--key <FIELD>] [--explain] [--ignore-path <POINTER>]... [--fail-on-change] <BEFORE> <AFTER>`

| Flag | Meaning | |
|---|---|---|
| `--key <FIELD>` | unique top-level string or number field (default id) |  |
| `--explain` | show changed field paths and kinds without field values |  |
| `--ignore-path <POINTER>` | ignore an exact JSON Pointer object field (repeatable) |  |
| `--fail-on-change` | exit 1 when added, removed or changed records exist |  |

Examples:

```sh
gregale export diff before.jsonl after.jsonl
gregale --json export diff before.jsonl after.jsonl --key id --fail-on-change
gregale --json export diff before.jsonl after.jsonl --explain
gregale --json export diff before.jsonl after.jsonl --ignore-path /updated_at --explain --fail-on-change
```

Runs offline. Reports sorted added, removed and changed IDs and counts without row bodies. Duplicate record keys or object fields fail before output.

Row and object-field order are ignored; array order is preserved. Numbers compare as exact decimal values without floating-point conversion.

Comparisons exit 0 unless --fail-on-change is enabled and changes exist, then exit 1 with a result. Invalid input reports an error without a result.

--explain adds JSON Pointer field paths and added/removed/changed kinds for changed IDs, without field values. Arrays and added/removed objects are reported at their parent field; the default report is unchanged.

--ignore-path excludes exact object fields from comparison and explanations, and lists sorted unique ignored_paths in the report. The selected key cannot be ignored; array elements cannot be selected. Added and removed records remain visible.


## realtime

Manage realtime endpoints, channels and connections

`gregale realtime [<subcommand>]`

### realtime drain

Close a bounded, filtered set of live connections

`gregale realtime drain [status] --reason <TEXT> [--channel <CHANNEL>] [--principal <PRINCIPAL>] [--connection-id <ID>] [--limit <N>] [--dry-run] [--allow-partial] [--wait] [--timeout <DURATION>] <app> <endpoint-id>`

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

#### realtime drain status

Show or wait for a durable realtime drain

`gregale realtime drain status [--wait] [--timeout <DURATION>] <app> <endpoint-id> <operation-id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | wait for the drain to reach a terminal state |  |
| `--timeout <DURATION>` | maximum time to wait with --wait |  |

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
| `--auth-algorithm <ALG>` | OIDC signing algorithm, required for oidc_jwt (repeatable) |  |
| `--auth-claim <KEY=VALUE>` | required OIDC claim as KEY=VALUE (repeatable) |  |
| `--allowed-origin <ORIGIN>` | exact browser origin (repeatable) |  |
| `--max-connections <N>` | per-endpoint connection cap (0 inherits the default) |  |
| `--max-message-bytes <N>` | decoded message size cap (0 inherits the default) |  |
| `--max-connection-age-seconds <SECONDS>` | connection age cap (0 inherits the default) |  |
| `--enabled[=true|false]` | create enabled or disabled | one of `true` · `false` |

### realtime update

Update endpoint callback, auth, or connection policy

`gregale realtime update [--callback-url <URL>] [--callback-auth-token-stdin] [--callback-auth-token <TOKEN>] [--connect-path <PATH>] [--message-path <PATH>] [--disconnect-path <PATH>] [--auth-token-stdin] [--auth-token <TOKEN>] [--auth-mode <MODE>] [--auth-issuer <URL>] [--auth-jwks-url <URL>] [--auth-audience <AUDIENCE>]... [--auth-algorithm <ALG>]... [--auth-claim <KEY=VALUE>]... [--clear-auth-claims] [--allowed-origin <ORIGIN>]... [--clear-allowed-origins] [--max-connections <N>] [--max-message-bytes <N>] [--max-connection-age-seconds <SECONDS>] [--enable] [--disable] [--enabled[=true|false]] <app> <endpoint-id>`

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
| `--enabled[=true|false]` | --enabled or --enabled=false, as on create | one of `true` · `false` |

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

### realtime send

Send a message to one live connection

`gregale realtime send [--data <DATA>] [--data-stdin] [--binary] [--principal <PRINCIPAL>] <app> <endpoint-id> <connection-id>`

| Flag | Meaning | |
|---|---|---|
| `--data <DATA>` | message text (or --data-stdin) |  |
| `--data-stdin` | read the message from stdin |  |
| `--binary` | send as a binary frame |  |
| `--principal <PRINCIPAL>` | send to every connection of one principal (with it, 11 more flags: see gregale realtime send --principal &lt;PRINCIPAL&gt; --help) |  |

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


## cron

Schedule requests and commands

`gregale cron [<subcommand>]`

### cron run

Fire one cron immediately or choose and follow a manual request

`gregale cron run [get] [--interactive] [--wait] [--app <SLUG>] [--timeout <DURATION>] [<cron-id>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a task, review and confirm one manual run, then follow its request |  |
| `--wait` | follow the run; exit 0 only if it succeeds (1 failed or cancelled, 3 timeout) |  |
| `--app <SLUG>` | app slug for interactive selection (linked app or picker by default) |  |
| `--timeout <DURATION>` | maximum follow duration with --wait or --interactive (default 2m) |  |

Examples:

```sh
gregale cron run --app my-api --interactive
gregale cron run <cron-id>
gregale cron run <cron-id> --wait
```

#### cron run get

Read or follow an existing manual fire request

`gregale cron run get [--wait] [--timeout <DURATION>] <request-id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | follow the existing request until terminal status |  |
| `--timeout <DURATION>` | maximum wait duration (default 2m; requires --wait) |  |

Examples:

```sh
gregale cron run get REQUEST_ID --wait --timeout 5m
```

### cron list

List cron rules

`gregale cron list [--app <slug>] [--selector|-l <SELECTOR>] [--one-off]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to the linked app; optional with -l) |  |
| `--selector|-l <SELECTOR>` | only crons whose labels match (preview) |  |
| `--one-off` | list one-off requests (--at or --in) instead of cron rules |  |

### cron create

Schedule a POST to an app path, or a deployment command

`gregale cron create [--at <RFC3339>] [--in <DURATION>] [--interactive] [--app <slug>] [--schedule <EXPR>] [--path <PATH>] [--command <EXEC>] [--arg <ARG>] [--shell] [--timeout-seconds <N>] [--max-output-bytes <N>] [--timezone <TZ>] [--skip-if-running] [--retry-max] [--retry-backoff-seconds] [--schedule-policy <JSON>] [--failure-rules <JSON>]`

| Flag | Meaning | |
|---|---|---|
| `--at <RFC3339>` | send one request at this RFC3339 time instead of on a schedule |  |
| `--in <DURATION>` | send one request after this delay (such as 30m) instead of on a schedule |  |
| `--interactive` | choose an HTTP path, schedule, and timezone, preview run times, and confirm creation |  |
| `--app <slug>` | app slug (defaults to linked context; interactive mode can also pick one) |  |
| `--schedule <EXPR>` | five-field cron expression (required unless interactive) |  |
| `--path <PATH>` | path the cron sends a POST to (mutually exclusive with --command) |  |
| `--command <EXEC>` | executable for a deployment command cron (preview: needs app tasks) |  |
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
gregale cron create --app my-api --interactive
gregale cron create --app my-api --at 2030-01-01T09:00:00Z --path /report
gregale cron create --app my-api --in 30m --path /reminders
```

With --at or --in, add schedules one request instead of a recurring cron and takes the flags of `gregale delayed-task add` (--payload, --method, --header, retries); --schedule and the command flags belong to recurring crons.

### cron get

Show one cron rule

`gregale cron get [--one-off] [--app <SLUG>] [--next] <id>`

| Flag | Meaning | |
|---|---|---|
| `--one-off` | show a one-off request (--at or --in) instead of a cron rule |  |
| `--app <SLUG>` | with --one-off, require the request to belong to this app |  |
| `--next` | show upcoming expression times across app tasks |  |

### cron update

Update one cron rule

`gregale cron update [--interactive] [--app <SLUG>] [--schedule <EXPR>] [--path <PATH>] [--timezone <TZ>] [--enable] [--disable] [--skip-if-running] [--allow-overlap] [--retry-max] [--retry-backoff-seconds <N>] [--schedule-policy <JSON>] [--failure-rules <JSON>] [--clear-schedule-policy] [--clear-failure-rules] [<id>]`

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
| `--clear-schedule-policy` | remove the schedule policy |  |
| `--clear-failure-rules` | remove the failure rules |  |

Examples:

```sh
gregale cron update --app my-api --interactive
```

### cron delete

Delete one cron rule

`gregale cron delete [--yes] <id>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | accepted for scripts; crons rm does not prompt |  |

### cron runs

Show execution history

`gregale cron runs [--interactive] [--app <SLUG>] [--cursor <CURSOR>] [--limit <N>] [--run <TASK-ID>] [<id>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a task, browse runs, and inspect command output |  |
| `--app <SLUG>` | app slug for interactive selection (linked app or picker by default) |  |
| `--cursor <CURSOR>` | pagination cursor for older runs (also --before) |  |
| `--limit <N>` | max runs to show (1..100) |  |
| `--run <TASK-ID>` | show details and captured output for one command run |  |

Examples:

```sh
gregale cron runs --app my-api --interactive
gregale cron runs <id>
```

### cron occurrences

Inspect scheduled occurrence decisions

`gregale cron occurrences [--before <ID>] [--cursor <ID>] [--all] [--limit <N>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--before <ID>` | alias for --cursor |  |
| `--cursor <ID>` | opaque continuation cursor |  |
| `--all` | walk every page using --limit and --cursor |  |
| `--limit <N>` | max occurrence decisions (1..200) |  |

### cron cancel

Cancel a command-cron run, or a one-off request

`gregale cron cancel [--app <SLUG>] <cron-id> <run-id> | <request-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | with a one-off request, require it to belong to this app |  |

With one id, cancels a one-off request made with --at or --in; --app then requires it to belong to that app.


## job

Manage jobs that run to completion

`gregale job [<subcommand>]`

Examples:

```sh
gregale job
gregale job overview nightly-report
gregale job --help
```

### job artifact

download · get

#### job artifact download

Download and verify current managed artifacts

`gregale job artifact download [--task <N>] [--out <PATH>] [--all] [--all-tasks] [--include <GLOB>] [--exclude <GLOB>] [--dry-run] [--resume] [--out-dir <DIR>] [<name>] [<run-id|latest>] [<task-index>] [<artifact-name>]`

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
gregale job artifact download --task 2 report.csv --out report.csv
gregale job artifact download nightly-report latest 0 report.csv --out report.csv
```

#### job artifact get

Verify a managed result and get a signed URL

`gregale job artifact get [--interactive] [<name>] [<run-id|latest>] [<task-index>] [<artifact-name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a task and managed output artifact |  |

Examples:

```sh
gregale job artifact get nightly-report latest 0 report.csv
gregale job artifact get --interactive
gregale job artifact get <name> <run-id> <task-index> <artifact-name>
```

### job replay

Replay unsuccessful tasks of a Job run

`gregale job replay [--dry-run] [--results] [--include <GLOB>] [--exclude <GLOB>] [--all-artifacts] [--wait] [--wait-timeout <DURATION>] [--poll-interval <DURATION>] [--interactive] [<name>] [<run-id|latest>]`

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
gregale job replay nightly-report latest --dry-run
gregale job replay --results
gregale job replay
gregale job replay nightly-report latest --wait
gregale job replay nightly-report RUN_ID --wait
gregale job replay --interactive
```

An omitted Job uses local defaults; an omitted run uses the latest.

### job resume

Recover a saved Job run: observe, read failures or collect results

`gregale job resume [--dry-run] [--run <UUID|PREFIX>] [--list] [--overview] [--current-connection] [--forget <UUID>] [--timeout <DURATION>] [--poll-interval <DURATION>] [--job <NAME>] [--saved-job] [--latest] [--logs] [--results]`

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
gregale job resume --list
gregale job resume --job nightly-report --overview
gregale job resume --job nightly-report --logs
gregale job resume --job nightly-report --results
gregale job resume --run RUN_ID
```

### job list

List jobs in this account

`gregale job list [--limit <N>] [--offset <N>] [--all] [--selector|-l <SELECTOR>]`

| Flag | Meaning | |
|---|---|---|
| `--limit <N>` | page size (1..200, default 50) |  |
| `--offset <N>` | starting offset (&gt;= 0) |  |
| `--all` | walk every page using --limit and --offset |  |
| `--selector|-l <SELECTOR>` | only jobs whose labels match (preview) |  |

### job create

Create a new job

`gregale job create [--interactive] [--image <REF>] [--command <ARGV>] [--ram <MB>] [--timeout <SECONDS>] [--parallelism <N>] [--retries <N>] [--schedule <EXPR>] [--timezone <TZ>] [--schedule-policy <JSON>] [--failure-rules <JSON>] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a name, image, command, resources, and optional recurring schedule |  |
| `--image <REF>` | OCI image (required unless interactive) |  |
| `--command <ARGV>` | comma-separated entrypoint (e.g. /bin/sh,-c,echo hi); escape a comma inside an argument with a backslash, pass a JSON array, or give the command after -- |  |
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
gregale job create --interactive
```

### job get

Inspect Job readiness (saved Job by default)

`gregale job get [--check] [--wait-ready] [--timeout <DURATION>] [--poll-interval <DURATION>] [--next] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--check` | exit 0 only for an active Job with a ready image; excludes --wait-ready |  |
| `--wait-ready` | wait for selected image preparation |  |
| `--timeout <DURATION>` | maximum image wait (default 5m) |  |
| `--poll-interval <DURATION>` | polling interval (default 2s) |  |
| `--next` | show upcoming recurring Job times and readiness |  |

Examples:

```sh
gregale job get
gregale job get --check
gregale job get --wait-ready
gregale job get my-job --wait-ready --timeout 5m
```

### job update

Update one job

`gregale job update [--interactive] [--image <REF>] [--command <ARGV>] [--ram <MB>] [--timeout <SECONDS>] [--parallelism <N>] [--retries <N>] [--pause] [--resume] [--schedule <EXPR>] [--timezone <TZ>] [--unschedule] [--schedule-policy <JSON>] [--failure-rules <JSON>] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a Job, edit resources and schedule, and confirm changes |  |
| `--image <REF>` | new OCI image |  |
| `--command <ARGV>` | new comma-separated entrypoint (e.g. /bin/sh,-c,echo hi); escape a comma inside an argument with a backslash, pass a JSON array, or give the command after -- |  |
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
gregale job update --interactive
```

### job delete

Soft-delete one job

`gregale job delete [--interactive] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a Job, review configuration, and confirm removal |  |

Examples:

```sh
gregale job delete --interactive
```

### job run

Dispatch a new run (fan-out N tasks)

`gregale job run [--saved-job] [--wait-ready] [--image-wait-timeout <DURATION>] [--image-poll-interval <DURATION>] [--interactive] [--wait] [--results] [--out-dir <DIR>] [--all-artifacts] [--include <GLOB>] [--exclude <GLOB>] [--wait-timeout <DURATION>] [--poll-interval <DURATION>] [--dry-run] [--tasks <N>] [--retries <N>] [--timeout <SECONDS>] [--input <ID=REF>] [--input-manifest-uri <URI>] [--input-manifest-sha256 <DIGEST>] [--parallelism <N>] [--flexible] [--eligible-at <RFC3339>] [--latest-start-at <RFC3339>] [--fail-fast] [--failure-rules <JSON>] [<job-name>]`

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
gregale job run --interactive
gregale job run <job-name>
gregale job run nightly-report --tasks 10 --wait --wait-timeout 10m
gregale job run nightly-report --tasks 10 --out-dir ./results
gregale job run nightly-report --tasks 10 --results
gregale job run --dry-run
gregale job run --saved-job --dry-run
gregale job run --wait-ready --results
```

### job runs

List runs (saved Job by default)

`gregale job runs [--failed-only] [--status <STATUS>] [--limit <N>] [--offset <N>] [--all] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--failed-only` | show failed and dead-letter runs in fetched history; use --all for subsequent pages |  |
| `--status <STATUS>` | filter fetched history by aggregate status; use --all to scan subsequent pages | one of `queued` · `running` · `succeeded` · `failed` · `cancelled` · `dead_letter` |
| `--limit <N>` | page size (1..200, default 50) |  |
| `--offset <N>` | starting offset (&gt;= 0) |  |
| `--all` | walk every page using --limit and --offset |  |

Examples:

```sh
gregale job runs
gregale job runs --failed-only --all
```

### job overview

Summarize Job readiness, latest run and next commands

`gregale job overview [--check] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--check` | exit 0 only for an active Job, ready image, and succeeded latest run |  |

Examples:

```sh
gregale job overview
gregale job overview --check
gregale job overview nightly-report --json
```

Uses the saved Job by default.

### job status

Read one exact or latest Job run summary

`gregale job status [--check] [--conditions] [<name>] [<run-id|latest>]`

| Flag | Meaning | |
|---|---|---|
| `--check` | exit 0 only when the recorded run status is succeeded |  |
| `--conditions` | print the run&#39;s Succeeded and Progressing conditions instead (preview) |  |

Examples:

```sh
gregale job status
gregale job status nightly-report
gregale job status nightly-report RUN_ID --check
gregale job status nightly-report latest
gregale --json jobs status nightly-report RUN_ID
```

Uses the saved Job and its latest run by default.

### job occurrences

Inspect recurring schedule decisions

`gregale job occurrences [--interactive] [--before <ID>] [--cursor <ID>] [--all] [--limit <N>] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a Job and browse scheduled occurrence decisions |  |
| `--before <ID>` | alias for --cursor |  |
| `--cursor <ID>` | opaque continuation cursor |  |
| `--all` | walk every page using --limit and --cursor |  |
| `--limit <N>` | max occurrence decisions (1..200) |  |

Examples:

```sh
gregale job occurrences --interactive
```

### job wait

Follow a Job run (saved Job and latest by default)

`gregale job wait [--for <CONDITION>] [--interactive] [--out-dir <DIR>] [--include <GLOB>] [--exclude <GLOB>] [--timeout <DURATION>] [--poll-interval <DURATION>] [<name>] [<run-id|latest>]`

| Flag | Meaning | |
|---|---|---|
| `--for <CONDITION>` | wait for a run condition instead (Succeeded, Progressing; TYPE=True\|False\|Unknown); exits 0 holds, 1 terminal, 3 timeout, 4 not found (preview) |  |
| `--interactive` | choose a Job and run |  |
| `--out-dir <DIR>` | collect artifacts after success, verifying matching existing files; non-interactive target only |  |
| `--include <GLOB>` | repeatable artifact-name glob for collection; requires --out-dir; quote patterns |  |
| `--exclude <GLOB>` | repeatable artifact-name glob to omit; requires --out-dir; exclusions take precedence |  |
| `--timeout <DURATION>` | maximum wait duration (default 10m) |  |
| `--poll-interval <DURATION>` | polling interval (default 2s) |  |

Examples:

```sh
gregale job wait
gregale job wait nightly-report
gregale job wait nightly-report latest --timeout 10m
gregale job wait --interactive
gregale job wait my-job RUN_ID --timeout 10m --json
```

### job cancel

Cancel a run

`gregale job cancel [--dry-run] [--interactive] [<name>] [<run-id>]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | inspect target status, task counts, and cancellation effects without cancelling |  |
| `--interactive` | choose an unfinished run, review progress, and confirm cancellation |  |

Examples:

```sh
gregale job cancel nightly-report RUN_ID --dry-run
gregale job cancel --interactive
```

### job defaults

Show or manage non-secret project-local Job defaults

Examples:

```sh
gregale job defaults
gregale job defaults show
gregale job defaults --help
```

#### job defaults set

Replace saved Job defaults

`gregale job defaults set [--dry-run] [--tasks <N>] [--results] [--wait-ready] [--include <GLOB>] [--exclude <GLOB>] <name>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview current/proposed settings without saving or submitting |  |
| `--tasks <N>` | default positive task count |  |
| `--results` | collect results by default |  |
| `--wait-ready` | wait for image preparation before submitting by default |  |
| `--include <GLOB>` | repeatable saved artifact-name glob; quote patterns |  |
| `--exclude <GLOB>` | repeatable saved artifact-name glob to omit |  |

#### job defaults update

Change saved Job defaults while preserving omitted settings

`gregale job defaults update [--dry-run] [--tasks <N>] [--clear-tasks] [--results] [--wait-ready] [--include <GLOB>] [--exclude <GLOB>] [--all-artifacts] [--add-include <GLOB>]... [--remove-include <GLOB>]... [--add-exclude <GLOB>]... [--remove-exclude <GLOB>]... [<name>]`

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
gregale job defaults update --tasks 3 --dry-run
gregale job defaults update --clear-tasks --dry-run
gregale job defaults update --results=false
gregale job defaults update --all-artifacts
```

#### job defaults show

Show saved Job settings, their path, and local recovery readiness

`gregale job defaults show`

#### job defaults clear

Remove saved Job defaults

`gregale job defaults clear [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | show settings and inheritance effects without removing the file |  |

Examples:

```sh
gregale job defaults clear --dry-run
```

### job results

Wait for success and collect outputs

`gregale job results [--out-dir <DIR>] [--all-artifacts] [--dry-run] [--timeout <DURATION>] [--poll-interval <DURATION>] [--include <GLOB>] [--exclude <GLOB>] [<name>] [<run-id|latest>]`

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
gregale job results nightly-report
gregale job results nightly-report latest --out-dir ./results
gregale job results nightly-report latest --out-dir ./results --dry-run
```

Output files that already exist are verified, not replaced.

### job tasks

List tasks (saved Job and latest run by default)

`gregale job tasks [--all] [--limit <N>] [--offset <N>] [--unsuccessful-only] [--failed-only] [--artifacts-only] [--include <GLOB>] [--exclude <GLOB>] [--interactive] [<name>] [<run-id|latest>]`

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
gregale job tasks --unsuccessful-only
gregale job tasks
gregale job tasks --failed-only
gregale job tasks --artifacts-only
gregale job tasks nightly-report latest --all
gregale job tasks nightly-report latest --failed-only
gregale job tasks nightly-report latest
gregale job tasks --interactive
```

### job attempts

List retained attempts for one task in an exact or latest run

`gregale job attempts [--task <N>] [--unsuccessful-only] [--failed-only] [--all] [--limit <N>] [--offset <N>] [--interactive] [<name>] [<run-id|latest>] [<task-index>]`

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
gregale job attempts --task 2 --unsuccessful-only --all
gregale job attempts --task 2 --failed-only --all
gregale job attempts nightly-report latest 0 --all --failed-only
gregale job attempts nightly-report latest 0 --all
gregale job attempts nightly-report latest 0
gregale job attempts --interactive
```

### job retry

Retry one failed task

`gregale job retry [--dry-run] [--results] [--include <GLOB>] [--exclude <GLOB>] [--all-artifacts] [--task <N>] [--wait] [--wait-timeout <DURATION>] [--poll-interval <DURATION>] [--interactive] [<name>] [<run-id>] [<task-index>]`

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
gregale job retry --task 2 --dry-run
gregale job retry --task 2 --results
gregale job retry --task 2
gregale job retry nightly-report latest --task 2 --wait
gregale job retry nightly-report RUN_ID 0 --wait
gregale job retry --interactive
```

With --task, uses the saved Job and its newest run.

### job artifacts

List recorded task artifacts and managed download commands

`gregale job artifacts [--task <N>] [--attempt <N>] [--include <GLOB>] [--exclude <GLOB>] [<name>] [<run-id|latest>] [<task-index>]`

| Flag | Meaning | |
|---|---|---|
| `--task <N>` | task index; omitted Job uses local defaults, omitted run uses latest |  |
| `--attempt <N>` | retained attempt number (metadata only) |  |
| `--include <GLOB>` | repeatable artifact-name glob; quote patterns |  |
| `--exclude <GLOB>` | repeatable artifact-name glob to omit; exclusions take precedence |  |

Examples:

```sh
gregale job artifacts --task 2
gregale job artifacts --task 2 --attempt 1
gregale job artifacts nightly-report latest 0
```

### job logs

Read unsuccessful-task logs by default, or select one task

`gregale job logs [--follow] [--failed-only] [--unsuccessful-only] [--task <N>] [--attempt <N>] [--interactive] [--max-bytes <N>] [<name>] [<run-id|latest>] [<task-index>]`

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
gregale job logs
gregale job logs nightly-report latest 0 --follow
gregale job logs nightly-report
gregale job logs --failed-only
gregale job logs --task 0
gregale job logs nightly-report --task 0
gregale job logs nightly-report latest 0
gregale job logs --interactive
```

### job registry

Manage private registry credentials for one job

#### job registry list

List registry credentials for the job

`gregale job registry list <job>`

#### job registry set

Store a registry credential for the job

`gregale job registry set [--interactive] --registry <HOST> --user <USER> [--password-stdin] [--password <PASSWORD>] [<job>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a Job, enter a hidden token, and review add or replace |  |
| `--registry <HOST>` | registry host | required |
| `--user <USER>` | registry user | required |
| `--password-stdin` | read the password from stdin |  |
| `--password <PASSWORD>` | registry password (prefer --password-stdin) |  |

Examples:

```sh
gregale job registry set --interactive
```

#### job registry rm

Remove a registry credential from the job

`gregale job registry rm [--interactive] [--registry <HOST>] [<job>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a credential, review metadata, and confirm removal |  |
| `--registry <HOST>` | registry host (required unless interactive) |  |

Examples:

```sh
gregale job registry rm --interactive
```


## workflow

Manage durable workflows

`gregale workflow [<subcommand>]`

### workflow stored

Build, monitor and control stored workflows

Examples:

```sh
gregale workflow stored init --list
gregale workflow stored check --app billing --scenarios scenarios.yaml
gregale workflow stored diff --app billing --name approval-flow
gregale workflow stored init --template scheduled-report --path reports
gregale workflow stored runs --app billing --name approval-flow --status failed
gregale workflow stored diagnose --app billing --name approval-flow --run RUN_ID
gregale workflow stored recovery-preview --app billing --name approval-flow --since 2026-10-01T00:00:00Z --until 2026-10-02T00:00:00Z
gregale workflow stored recover --app billing --name approval-flow --run RUN_ID:COUNT --idempotency-key recovery-batch-1
gregale workflow stored list --app billing
gregale workflow stored get --app billing --name paid-invoice
gregale workflow stored health --app billing --name paid-invoice
gregale workflow stored pause --app billing --name paid-invoice --expected-version VERSION
gregale workflow stored resume --app billing --name paid-invoice --expected-version VERSION
gregale workflow stored revisions list --app billing --name paid-invoice
gregale workflow stored revisions show --app billing --name paid-invoice --revision 42
gregale workflow stored validate --app billing --file stored_workflow.yaml
gregale workflow stored simulate --app billing --file stored_workflow.yaml --input-file sample.json
gregale workflow stored apply --app billing --file stored_workflow.yaml --expected-version 0
gregale workflow stored publish --app billing --name paid-invoice --expected-version VERSION
gregale workflow stored restore --app billing --name paid-invoice --revision 42 --expected-version VERSION
gregale workflow stored delete --app billing --name paid-invoice --expected-version VERSION --yes
```

VERSION is the &#34;version&#34; returned by the previous write (or shown by get/list). Versions come from one counter shared by every stored workflow in the app, so a new draft saved with --expected-version 0 usually returns a version greater than 1.

Revision numbers in `revisions list` (&#34;revision&#34;, also &#34;version&#34;) are what --revision takes. Resume restores the published trigger.enabled; it does not enable a definition with trigger.enabled: false.

#### workflow stored recovery

health · suppress · create · cancel · run · evidence

`gregale workflow stored recovery <health|suppress|create|cancel|run|evidence>`

##### workflow stored recovery health

Inspect recovery execution attention and explicit action hints

`gregale workflow stored recovery health --app <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |

##### workflow stored recovery suppress

Stop one queued automatic retry

`gregale workflow stored recovery suppress --app <SLUG> --review <UUID> --action <UUID> --policy-version <N> --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--review <UUID>` | review ID | required |
| `--action <UUID>` | queued action ID | required |
| `--policy-version <N>` | observed policy version | required |
| `--reason <TEXT>` | why the retry is stopped | required |

##### workflow stored recovery create

Submit a reviewed stored workflow run plan as a durable server job

`gregale workflow stored recovery create --plan <PATH> [--rate <N>] [--execution-deadline <DURATION>] [--execution-stall <DURATION>] [--reason <TEXT>] [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | reviewed run plan | required |
| `--rate <N>` | maximum recoveries per second (1..100) |  |
| `--execution-deadline <DURATION>` | advisory execution deadline (zero disables) |  |
| `--execution-stall <DURATION>` | no-progress warning threshold (default 15m) |  |
| `--reason <TEXT>` | optional operator reason |  |
| `--dry-run` | with --job: review recovery failures and save an explicit retry plan (with it: --app, --position, --plan-out, --key) |  |

##### workflow stored recovery cancel

Review or cancel one admitted recovery generation

`gregale workflow stored recovery cancel --app <SLUG> --job <UUID> --position <N> --resume-count <N> --reason <TEXT> --yes [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--job <UUID>` | native recovery job | required |
| `--position <N>` | frozen item position | required |
| `--resume-count <N>` | reviewed admitted generation | required |
| `--reason <TEXT>` | operator reason | required |
| `--yes` | confirm exact generation cancellation | required |
| `--dry-run` | show the recovery generation it would cancel |  |

##### workflow stored recovery run

Execute a saved recovery plan with a durable per-attempt journal

`gregale workflow stored recovery run --plan <PATH> --journal <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | reviewed recovery plan file | required |
| `--journal <PATH>` | new journal file for this execution | required |

##### workflow stored recovery evidence

Diagnose or repair verified automatic retry evidence

`gregale workflow stored recovery evidence --app <SLUG> --review <UUID> --action <UUID> [--parent] [--repair] [--request-id <UUID>] [--reason <TEXT>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--review <UUID>` | automatic retry review | required |
| `--action <UUID>` | automatic retry action | required |
| `--parent` | inspect or repair pending retry parent evidence |  |
| `--repair` | save a verified terminal outcome |  |
| `--request-id <UUID>` | stable repair request identifier |  |
| `--reason <TEXT>` | operator repair reason |  |

#### workflow stored failure

list · policy · resume

`gregale workflow stored failure <list|policy|resume>`

##### workflow stored failure list

List captured initial workflow failures

`gregale workflow stored failure list --app <SLUG> [--name <NAME>] [--cursor <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | workflow filter |  |
| `--cursor <CURSOR>` | next page cursor (also --after) |  |
| `--limit <N>` | page size (at most 100) |  |

##### workflow stored failure policy

Inspect failure pauses and history, or configure monitoring

`gregale workflow stored failure policy --app <SLUG> --name <NAME> [--enabled <BOOL>] [--expected-version <N>] [--failure-threshold <N>] [--min-completed-runs <N>] [--window-seconds <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--enabled <BOOL>` | enable or disable monitoring; required when configuring |  |
| `--expected-version <N>` | current failure policy version; required when configuring |  |
| `--failure-threshold <N>` | terminal failures needed to pause (1..10000, default 3) |  |
| `--min-completed-runs <N>` | minimum completed outcomes (1..10000, default 5) |  |
| `--window-seconds <N>` | observation window (60..86400, default 300) |  |

Also previews a resume.

##### workflow stored failure resume

Preview retained work and explicitly clear a failure pause

`gregale workflow stored failure resume --app <SLUG> --name <NAME> --expected-generation <N> [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--expected-generation <N>` | current failure pause generation | required |
| `--dry-run` | preview the failure and save a reviewed first recovery plan (with it: --failure, --policy, --plan-out, --key) |  |

#### workflow stored policy

Inspect or set required server publishing checks

`gregale workflow stored policy <publish>`

##### workflow stored policy publish

Inspect or set required server publishing checks

`gregale workflow stored policy publish --app <SLUG> [--mode <MODE>] [--expected-version <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--mode <MODE>` | set publishing requirement (admin scope) | one of `optional` · `scenarios` · `coverage` |
| `--expected-version <N>` | current policy version (required when setting mode) |  |

#### workflow stored event

Execute a reviewed event plan with fresh receipt checks and a journal

`gregale workflow stored event <recover>`

##### workflow stored event recover

Execute a reviewed event plan with fresh receipt checks and a journal

`gregale workflow stored event recover --plan <PATH> --journal <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | reviewed event plan | required |
| `--journal <PATH>` | new execution journal | required |

#### workflow stored event-recovery

Submit a reviewed stored workflow event plan as a durable server job

`gregale workflow stored event-recovery <start>`

##### workflow stored event-recovery start

Submit a reviewed stored workflow event plan as a durable server job

`gregale workflow stored event-recovery start --plan <PATH> [--rate <N>] [--reason <TEXT>] [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | reviewed event plan | required |
| `--rate <N>` | maximum recoveries per second (1..100) |  |
| `--reason <TEXT>` | optional operator reason |  |
| `--dry-run` | save the selected routing actions for review (with it: --app, --name, --source, --event, --idempotency-key, --out) |  |

#### workflow stored failure-recovery

review · evidence · list

`gregale workflow stored failure-recovery <review|evidence|list>`

##### workflow stored failure-recovery review

Prepare, inspect, submit or cancel a durable first recovery review

`gregale workflow stored failure-recovery review --app <SLUG> [--failure <UUID>] [--expected-policy-version <N>] [--review <UUID>] [--decision <DECISION>] [--reason <TEXT>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--failure <UUID>` | captured initial failure |  |
| `--expected-policy-version <N>` | saved policy version |  |
| `--review <UUID>` | durable review |  |
| `--decision <DECISION>` | submit or cancel |  |
| `--reason <TEXT>` | operator reason |  |

##### workflow stored failure-recovery evidence

Diagnose or repair verified initial recovery evidence

`gregale workflow stored failure-recovery evidence --app <SLUG> --review <UUID> [--repair] [--request-id <UUID>] [--reason <TEXT>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--review <UUID>` | initial recovery review | required |
| `--repair` | save a verified terminal outcome |  |
| `--request-id <UUID>` | stable repair request identifier |  |
| `--reason <TEXT>` | operator repair reason |  |

##### workflow stored failure-recovery list

List retained durable first recovery reviews

`gregale workflow stored failure-recovery list --app <SLUG> [--cursor <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--cursor <UUID>` | next page cursor (also --after) |  |
| `--limit <N>` | page size |  |

#### workflow stored recovery-policy

Inspect, preview or update recovery policy

`gregale workflow stored recovery-policy <evaluate|history|get|set>`

##### workflow stored recovery-policy evaluate

Compare a proposed recovery policy with recent history

`gregale workflow stored recovery-policy evaluate --app <SLUG> --file <PATH> [--workflow <NAME>] [--since <DURATION>] [--created-after <RFC3339>] [--created-before <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--file <PATH>` | proposed policy JSON with expected_version | required |
| `--workflow <NAME>` | only sample this workflow |  |
| `--since <DURATION>` | lookback, at most 30 days (default 7d) |  |
| `--created-after <RFC3339>` | inclusive start; excludes --since |  |
| `--created-before <RFC3339>` | inclusive end |  |

##### workflow stored recovery-policy history

Inspect policy decisions and export prepared plans

`gregale workflow stored recovery-policy history --app <SLUG> [--review <UUID>] [--plan-out <PATH>] [--cursor <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--review <UUID>` | retained review |  |
| `--plan-out <PATH>` | export selected prepared plan |  |
| `--cursor <UUID>` | review pagination cursor (also --after) |  |
| `--limit <N>` | page limit |  |

##### workflow stored recovery-policy get

Inspect, preview or update recovery policy

`gregale workflow stored recovery-policy get --app <SLUG> [--file <PATH>] [--preview] [--queue] [--control <pause|resume>] [--reason <TEXT>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--file <PATH>` | policy JSON with expected_version |  |
| `--preview` | preview --file without changing policy |  |
| `--queue` | inspect automatic retry queue |  |
| `--control <pause|resume>` | control automatic execution |  |
| `--reason <TEXT>` | operator control reason |  |

##### workflow stored recovery-policy set

Inspect, preview or update recovery policy

`gregale workflow stored recovery-policy set --app <SLUG> [--file <PATH>] [--preview] [--queue] [--control <pause|resume>] [--reason <TEXT>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--file <PATH>` | policy JSON with expected_version |  |
| `--preview` | preview --file without changing policy |  |
| `--queue` | inspect automatic retry queue |  |
| `--control <pause|resume>` | control automatic execution |  |
| `--reason <TEXT>` | operator control reason |  |

#### workflow stored recovery-response

apply · status · check

`gregale workflow stored recovery-response <apply|status|check>`

##### workflow stored recovery-response apply

Apply saved actions with durable receipts

`gregale workflow stored recovery-response apply --plan <PATH> --yes [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | saved response plan | required |
| `--yes` | confirm reviewed actions | required |
| `--dry-run` | save the cancellation and retry actions for review (with it, 8 more flags: see gregale workflow stored recovery-response apply --dry-run --help) |  |

##### workflow stored recovery-response status

Reconcile retained action receipts and execution outcomes

`gregale workflow stored recovery-response status --plan <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | saved response plan | required |

##### workflow stored recovery-response check

Recheck saved actions without applying

`gregale workflow stored recovery-response check --plan <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | saved response plan | required |

#### workflow stored live

Start, observe and check saved cases against real execution

`gregale workflow stored live <check|run|list|start|get|wait>`

##### workflow stored live check

Require passing suite evidence for a candidate target

`gregale workflow stored live check --app <SLUG> --name <NAME> [--policy <FILE>] [--baseline <UUID>] [--baseline-name <NAME>] --candidate <UUID> --expected-version <N> --expected-published-version <N> --expected-deployment-id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--policy <FILE>` | versioned required-case coverage policy JSON |  |
| `--baseline <UUID>` | retained baseline suite UUID; exclusive with baseline-name |  |
| `--baseline-name <NAME>` | resolve a named baseline; exclusive with baseline |  |
| `--candidate <UUID>` | distinct retained candidate suite UUID | required |
| `--expected-version <N>` | expected candidate saved stored workflow version | required |
| `--expected-published-version <N>` | expected candidate published revision | required |
| `--expected-deployment-id <UUID>` | expected candidate live default deployment UUID | required |

Read-only.

##### workflow stored live run

Run selected live cases sequentially with resumable private progress

`gregale workflow stored live run [--app <SLUG>] [--name <NAME>] [--case <NAME>] [--expected-version <N>] [--expected-published-version <N>] [--expected-deployment-id <UUID>] [--idempotency-key <KEY>] [--journal <PATH>] [--resume <PATH>] [--share] --confirm-real-actions [--timeout <DURATION>] [--poll-interval <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug for a new suite |  |
| `--name <NAME>` | stored workflow name for a new suite |  |
| `--case <NAME>` | explicit case selection; repeat in execution order, at most 32 |  |
| `--expected-version <N>` | saved stored workflow version for a new suite |  |
| `--expected-published-version <N>` | published revision for a new suite |  |
| `--expected-deployment-id <UUID>` | live default deployment UUID for a new suite |  |
| `--idempotency-key <KEY>` | stable suite key for a new suite |  |
| `--journal <PATH>` | new private progress file; required for a new suite |  |
| `--resume <PATH>` | continue the original selection from this suite journal |  |
| `--share` | save a new suite in shared dashboard history; persisted for resume |  |
| `--confirm-real-actions` | confirm execution of remaining selected cases | required |
| `--timeout <DURATION>` | total invocation deadline (default 5m, up to 1h) |  |
| `--poll-interval <DURATION>` | poll interval (default 3s, 1s to 1m) |  |

##### workflow stored live list

Read current target IDs and recent live verifications

`gregale workflow stored live list --app <SLUG> --name <NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |

##### workflow stored live start

Explicitly start one saved case against real handlers

`gregale workflow stored live start --app <SLUG> --name <NAME> --case <NAME> --expected-version <N> --expected-published-version <N> --expected-deployment-id <UUID> --idempotency-key <KEY> --confirm-real-actions [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--case <NAME>` | saved case with live expectations | required |
| `--expected-version <N>` | current saved stored workflow version | required |
| `--expected-published-version <N>` | published stored workflow revision | required |
| `--expected-deployment-id <UUID>` | live default deployment UUID | required |
| `--idempotency-key <KEY>` | stable key; retry only with the original start flags | required |
| `--confirm-real-actions` | confirm this case can perform real actions | required |
| `--wait` | wait and require a current passing assertion verdict |  |
| `--timeout <DURATION>` | maximum wait (default 5m, up to 1h) |  |
| `--poll-interval <DURATION>` | poll interval (default 3s, 1s to 1m) |  |

##### workflow stored live get

Read one verification without requiring a verdict

`gregale workflow stored live get --app <SLUG> --name <NAME> --run <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--run <UUID>` | existing live verification run UUID | required |

##### workflow stored live wait

Observe a run, check assertions and require a passing verdict

`gregale workflow stored live wait --app <SLUG> --name <NAME> --run <UUID> [--timeout <DURATION>] [--poll-interval <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--run <UUID>` | existing live verification run UUID | required |
| `--timeout <DURATION>` | maximum wait (default 5m, up to 1h) |  |
| `--poll-interval <DURATION>` | poll interval (default 3s, 1s to 1m) |  |

#### workflow stored baseline

Manage named baselines and inspect audit history

`gregale workflow stored baseline <retire|list|get|history|promote>`

##### workflow stored baseline retire

Permanently retire a baseline with audit history

`gregale workflow stored baseline retire --app <SLUG> --name <NAME> --baseline-name <NAME> --retirement-id <UUID> --expected-revision <N> --confirm-retirement`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--baseline-name <NAME>` | named release track, such as production | required |
| `--retirement-id <UUID>` | stable retirement UUID; preserve for retries | required |
| `--expected-revision <N>` | current positive baseline revision | required |
| `--confirm-retirement` | confirm permanent retirement; release slot and evidence pin | required |

Protected by revision checks.

##### workflow stored baseline list

Find active baselines and read retained evidence health

`gregale workflow stored baseline list --app <SLUG> --name <NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |

Never executes anything.

##### workflow stored baseline get

Read the current immutable promotion receipt

`gregale workflow stored baseline get --app <SLUG> --name <NAME> --baseline-name <NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--baseline-name <NAME>` | named release track, such as production | required |

##### workflow stored baseline history

Read up to 100 recent promotions

`gregale workflow stored baseline history --app <SLUG> --name <NAME> --baseline-name <NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--baseline-name <NAME>` | named release track, such as production | required |

##### workflow stored baseline promote

Promote passing evidence to a baseline with revision checks

`gregale workflow stored baseline promote --app <SLUG> --name <NAME> --baseline-name <NAME> --suite <UUID> --promotion-id <UUID> --expected-revision <N> --expected-version <N> --expected-published-version <N> --expected-deployment-id <UUID> --policy <FILE>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--baseline-name <NAME>` | named release track, such as production | required |
| `--suite <UUID>` | passing retained suite UUID | required |
| `--promotion-id <UUID>` | stable promotion UUID; preserve for retries | required |
| `--expected-revision <N>` | current baseline revision; explicit 0 creates | required |
| `--expected-version <N>` | expected candidate saved version | required |
| `--expected-published-version <N>` | expected published revision | required |
| `--expected-deployment-id <UUID>` | expected candidate deployment UUID | required |
| `--policy <FILE>` | version 2 coverage and age policy JSON | required |

Never executes workflows.

#### workflow stored diff

Compare draft and published definition fields without exposing values

`gregale workflow stored diff --app <SLUG> --name <NAME>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |

#### workflow stored check

Check simulation scenarios and expected step outcomes

`gregale workflow stored check --app <SLUG> --scenarios <PATH> [--require-coverage] [--suggest] [--suggest-out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--scenarios <PATH>` | scenario suite YAML or JSON file | required |
| `--require-coverage` | fail on unexcluded coverage hints |  |
| `--suggest` | write coverage scenario starters to a new file |  |
| `--suggest-out <PATH>` | new suggestion file (requires --suggest; defaults beside the suite) |  |

#### workflow stored init

Create a local stored workflow starter with simulation samples

`gregale workflow stored init [--template <NAME>] [--path <DIR>] [--list]`

| Flag | Meaning | |
|---|---|---|
| `--template <NAME>` | stored workflow starter name |  |
| `--path <DIR>` | new directory (defaults to starter name) |  |
| `--list` | list starters without creating files |  |

#### workflow stored list

List stored workflow versions and ownership for an app

`gregale workflow stored list --app <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |

#### workflow stored get

Inspect stored workflow state or export a definition

`gregale workflow stored get --app <SLUG> --name <NAME> [--definition-out <PATH>] [--published]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--definition-out <PATH>` | export the selected definition as JSON to a new file |  |
| `--published` | export the published definition instead of the draft |  |

#### workflow stored runs

List run history for one stored workflow

`gregale workflow stored runs --app <SLUG> --name <NAME> [--status <STATUS>] [--limit <N>] [--offset <N>] [--created-after <RFC3339>] [--created-before <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--status <STATUS>` | pending, running, awaiting_event, succeeded, failed or dead |  |
| `--limit <N>` | page size (1..100, default 50) |  |
| `--offset <N>` | page offset |  |
| `--created-after <RFC3339>` | inclusive RFC3339 creation-time start |  |
| `--created-before <RFC3339>` | inclusive RFC3339 creation-time end |  |

#### workflow stored diagnose

Diagnose a run belonging to the selected stored workflow

`gregale workflow stored diagnose --app <SLUG> --name <NAME> --run <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--run <UUID>` | workflow run UUID | required |

#### workflow stored history

Inspect retained recovery automation history

`gregale workflow stored history --app <SLUG> [--workflow <NAME>] [--run <UUID>] [--policy-version <N>] [--created-after <RFC3339>] [--created-before <RFC3339>] [--cursor <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--workflow <NAME>` | native workflow filter |  |
| `--run <UUID>` | native run filter |  |
| `--policy-version <N>` | policy version filter |  |
| `--created-after <RFC3339>` | inclusive start |  |
| `--created-before <RFC3339>` | inclusive end |  |
| `--cursor <CURSOR>` | next page cursor (also --after) |  |
| `--limit <N>` | page size (default 25, max 100) |  |

#### workflow stored health

Inspect app recovery health or named stored workflow run reliability

`gregale workflow stored health [--stall-after-seconds <SECONDS>] --app <SLUG> [--name <NAME>] [--created-after <RFC3339>] [--created-before <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--stall-after-seconds <SECONDS>` | app recovery evaluation threshold (default 300) |  |
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name |  |
| `--created-after <RFC3339>` | inclusive RFC3339 window start (max 30 days) |  |
| `--created-before <RFC3339>` | inclusive RFC3339 window end |  |

#### workflow stored report

Summarize workflow recovery outcomes over a time window

`gregale workflow stored report --app <SLUG> [--workflow <NAME>] [--policy-version <N>] [--since <DURATION>] [--created-after <RFC3339>] [--created-before <RFC3339>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--workflow <NAME>` | workflow name filter |  |
| `--policy-version <N>` | policy version filter |  |
| `--since <DURATION>` | lookback, at most 30 days (default 7d) |  |
| `--created-after <RFC3339>` | inclusive start; excludes --since |  |
| `--created-before <RFC3339>` | inclusive end |  |

#### workflow stored recover

Resume selected failed runs with per-run generation checks

`gregale workflow stored recover [--plan-out <PATH>] --app <SLUG> --name <NAME> --run <UUID:COUNT> --idempotency-key <KEY>`

| Flag | Meaning | |
|---|---|---|
| `--plan-out <PATH>` | save reviewed selection to a new plan file without resuming |  |
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--run <UUID:COUNT>` | run UUID and original expected resume count; repeatable, at most 25 | required |
| `--idempotency-key <KEY>` | stable batch key; reuse for interrupted retries | required |

Reports each run&#39;s outcome.

#### workflow stored pause

Stop future scheduled and event-triggered admissions

`gregale workflow stored pause --app <SLUG> --name <NAME> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--expected-version <N>` | current stored workflow version | required |

#### workflow stored resume

Resume automatic scheduled and event-triggered admissions

`gregale workflow stored resume --app <SLUG> --name <NAME> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--expected-version <N>` | current stored workflow version | required |

#### workflow stored revisions

Inspect immutable published snapshots

`gregale workflow stored revisions <list|show>`

##### workflow stored revisions list

List published revisions for a stored workflow

`gregale workflow stored revisions list --app <SLUG> --name <NAME> [--limit <N>] [--offset <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--limit <N>` | page size (1..100, default 50) |  |
| `--offset <N>` | number of revisions to skip |  |

##### workflow stored revisions show

Inspect a revision or export its definition

`gregale workflow stored revisions show --app <SLUG> --name <NAME> --revision <N> [--definition-out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--revision <N>` | published revision number | required |
| `--definition-out <PATH>` | export the definition as JSON to a new file |  |

#### workflow stored restore

Restore a published revision as a draft

`gregale workflow stored restore --app <SLUG> --name <NAME> --revision <N> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--revision <N>` | published revision number to restore | required |
| `--expected-version <N>` | current version; use 0 if deleted | required |

#### workflow stored delete

Delete a stored workflow using its current version

`gregale workflow stored delete --app <SLUG> --name <NAME> --expected-version <N> --yes [--restore-manifest]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--expected-version <N>` | current stored workflow version | required |
| `--yes` | required explicit confirmation of stored workflow deletion | required |
| `--restore-manifest` | allow the current YAML definition to own this stored workflow again |  |

#### workflow stored validate

Validate a stored workflow definition without saving it

`gregale workflow stored validate --app <SLUG> --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--file <PATH>` | YAML or JSON definition file | required |

#### workflow stored simulate

Trace a stored workflow with sample input and mocked outputs

`gregale workflow stored simulate --app <SLUG> --file <PATH> [--input-file-id <PATH=FILE_UUID>] [--input-file <PATH>] [--mock-outputs-file <PATH>] [--mock-item-outputs-file <PATH>] [--mock-attempts-file <PATH>] [--mock-item-attempts-file <PATH>] [--require-complete]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--file <PATH>` | YAML or JSON definition file | required |
| `--input-file-id <PATH=FILE_UUID>` | retained data file below context.input_dir (repeatable) |  |
| `--input-file <PATH>` | sample workflow input JSON file |  |
| `--mock-outputs-file <PATH>` | JSON object of action outputs keyed by step name |  |
| `--mock-item-outputs-file <PATH>` | JSON object of for_each output arrays keyed by step name |  |
| `--mock-attempts-file <PATH>` | JSON object of ordered attempt outcomes keyed by step name |  |
| `--mock-item-attempts-file <PATH>` | JSON object mapping loops and zero-based item indexes to attempt outcomes |  |
| `--require-complete` | fail if mocks leave steps unresolved |  |

Does not run steps.

#### workflow stored apply

Save a stored workflow definition as a draft

`gregale workflow stored apply --app <SLUG> --file <PATH> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--file <PATH>` | YAML or JSON definition file | required |
| `--expected-version <N>` | current version; use 0 for a new draft | required |

#### workflow stored publish

Publish the current stored workflow draft

`gregale workflow stored publish --app <SLUG> --name <NAME> --expected-version <N> [--take-over-manifest] [--scenarios <PATH>] [--require-coverage]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--name <NAME>` | stored workflow name | required |
| `--expected-version <N>` | current version of the draft | required |
| `--take-over-manifest` | explicitly take over YAML ownership |  |
| `--scenarios <PATH>` | check scenarios against the saved draft before publishing |  |
| `--require-coverage` | require coverage before publishing (requires --scenarios) |  |

### workflow recover

Recover a saved Workflow run: observe or read diagnostics

`gregale workflow recover [--dry-run] [--run <UUID|PREFIX>] [--list] [--overview] [--current-connection] [--forget <UUID>] [--timeout <DURATION>] [--poll-interval <DURATION>] [--saved-workflow] [--latest] [--app <SLUG>] [--workflow <NAME>] [--diagnose]`

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
gregale workflow recover --saved-workflow --latest --dry-run
gregale workflow recover --list
gregale workflow recover --workflow order-processing --overview
gregale workflow recover --workflow order-processing --diagnose
gregale workflow recover --run RUN_ID
```

### workflow schedule

history · replay · simulate

#### workflow schedule history

Inspect recurring workflow admission history

`gregale workflow schedule history --app <SLUG> [--platform-tenant-id <UUID>] [--cursor <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |
| `--platform-tenant-id <UUID>` | filter by tenant UUID |  |
| `--cursor <UUID>` | next cursor from the previous page |  |
| `--limit <N>` | maximum occurrences (1-200) |  |

#### workflow schedule replay

Replay eligible skipped occurrences with the current workflow

`gregale workflow schedule replay --app <SLUG> --occurrence-id <UUID> [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--occurrence-id <UUID>` | skipped occurrence UUID (repeat up to 20 times) | required |
| `--dry-run` | check skipped occurrences without replaying them |  |

Examples:

```sh
gregale workflow schedule replay --app billing --occurrence-id <uuid>
```

Normal quota applies.

#### workflow schedule simulate

Simulate upcoming fires and the next catch-up decision

`gregale workflow schedule simulate --app <SLUG> --workflow <NAME> [--at <RFC3339>] [--since <RFC3339>] [--count <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--workflow <NAME>` | schedule workflow name | required |
| `--at <RFC3339>` | hypothetical evaluator time |  |
| `--since <RFC3339>` | simulated previous evaluation time |  |
| `--count <N>` | upcoming fires to return (1..20, default 5) |  |

Examples:

```sh
gregale workflow schedule simulate --app billing --workflow nightly
gregale workflow schedule simulate --app billing --workflow nightly --at 2027-03-28T00:00:00Z --count 8
```

### workflow defaults

Show or manage project-local workflow recovery selectors

Examples:

```sh
gregale workflow defaults
gregale workflow defaults --help
```

#### workflow defaults set

Save a preferred workflow name and recorded app locally

`gregale workflow defaults set --app <SLUG> [--dry-run] <name>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | recorded app slug for recovery; target existence is not checked | required |
| `--dry-run` | preview destination, before/after selectors, and proposed recovery readiness without saving |  |

Examples:

```sh
gregale workflow defaults set order-processing --app payments-api
gregale workflow defaults set order-processing --app payments-api --dry-run
```

#### workflow defaults show

Show saved workflow and app selectors and recovery readiness

`gregale workflow defaults show`

Includes the preference file path.

#### workflow defaults update

Change saved workflow selectors while preserving omitted settings

`gregale workflow defaults update [--app <SLUG>] [--dry-run] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | replace the saved recorded app selector |  |
| `--dry-run` | preview updated selectors and local recovery readiness without saving |  |

Examples:

```sh
gregale workflow defaults update --app billing-api --dry-run
gregale workflow defaults update nightly-report --dry-run
```

#### workflow defaults clear

Clear workflow recovery preferences without deleting run records

`gregale workflow defaults clear [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview saved selectors and removal effects without deleting files |  |

Examples:

```sh
gregale workflow defaults clear --dry-run
```

### workflow list

List workflow runs for an app

`gregale workflow list [--app <SLUG>] [--limit <N>] [--offset <N>] [--all] [--status <STATUS>] [--workflow-name <NAME>] [--created-after <RFC3339>] [--created-before <RFC3339>]`

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
gregale workflow list --workflow-name nightly-report --all
gregale workflow list --app billing --workflow-name paid-invoice --status failed
gregale workflow list --app billing --created-after 2026-10-01T00:00:00Z --created-before 2026-10-05T23:59:59Z
```

### workflow schedules

Inspect recurring workflow schedules and preview their next fires

`gregale workflow schedules [preview] --app <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | application slug | required |

#### workflow schedules preview

Simulate upcoming fires and the next catch-up decision

`gregale workflow schedules preview --app <SLUG> --workflow <NAME> [--at <RFC3339>] [--since <RFC3339>] [--count <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--workflow <NAME>` | schedule workflow name | required |
| `--at <RFC3339>` | hypothetical evaluator time |  |
| `--since <RFC3339>` | simulated previous evaluation time |  |
| `--count <N>` | upcoming fires to return (1..20, default 5) |  |

Examples:

```sh
gregale workflow schedule simulate --app billing --workflow nightly
gregale workflow schedule simulate --app billing --workflow nightly --at 2027-03-28T00:00:00Z --count 8
```

### workflow run

Trigger a new workflow run

`gregale workflow run [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] [--app <slug>] [--input <JSON>] [--input-file <PATH|->] [--idempotency-key <KEY>] <workflow-name>`

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
gregale workflow run nightly-report --input-file input.json --wait
gregale workflow run nightly-report --input-file input.json
gregale workflow run nightly-report --input-file -
```

### workflow wait

Wait for an exact or latest workflow run to succeed

`gregale workflow wait [--app <SLUG>] [--workflow-name <NAME>] [--timeout <DURATION>] [--poll-interval <DURATION>] <run_id|latest>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app for latest (defaults to linked project app) |  |
| `--workflow-name <NAME>` | exact workflow name for latest (inferred when the app&#39;s recent runs share one) |  |
| `--timeout <DURATION>` | maximum wait (default 5m; positive) |  |
| `--poll-interval <DURATION>` | read interval (default 2s; 1s to 1h) |  |

Examples:

```sh
gregale workflow wait RUN_ID --timeout 5m
gregale workflow wait latest --workflow-name billing --timeout 5m
```

Exits nonzero on failure or timeout.

### workflow status

Show details of a workflow run

`gregale workflow status [--check] [--interactive] [--app <slug>] [--workflow-name <NAME>] [<run_id|latest>]`

| Flag | Meaning | |
|---|---|---|
| `--check` | exit 0 only for a succeeded, non-cancelled run (explicit read only) |  |
| `--interactive` | choose a run for inspection |  |
| `--app <slug>` | app for latest or interactive selection (defaults to linked project app) |  |
| `--workflow-name <NAME>` | exact workflow name for latest (inferred when the app&#39;s recent runs share one) |  |

Examples:

```sh
gregale workflow status RUN_ID --check
gregale workflow status latest --workflow-name nightly-report
gregale workflow status --interactive
gregale workflow status --app billing --interactive
gregale workflow status <run_id>
```

### workflow diagnose

Inspect queue reasons and preview recovery without changing the run

`gregale workflow diagnose [--app <SLUG>] [--workflow-name <NAME>] <run_id|latest>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug for latest (defaults to linked project app) |  |
| `--workflow-name <NAME>` | exact workflow name for latest (inferred when the app&#39;s recent runs share one) |  |

Examples:

```sh
gregale workflow diagnose latest --workflow-name nightly-report
gregale workflow diagnose <run_id>
gregale --json workflows diagnose <run_id>
```

### workflow steps

List steps for an exact or latest workflow run

`gregale workflow steps [--failed-only] [--app <SLUG>] [--workflow-name <NAME>] <run_id|latest>`

| Flag | Meaning | |
|---|---|---|
| `--failed-only` | show only failed steps with inspection commands |  |
| `--app <SLUG>` | app slug for latest (defaults to linked project app) |  |
| `--workflow-name <NAME>` | exact workflow name for latest (inferred when the app&#39;s recent runs share one) |  |

Examples:

```sh
gregale workflow steps latest --workflow-name billing --failed-only
gregale workflow steps latest --workflow-name nightly-report
```

### workflow attempts

List a step&#39;s retry attempts and managed effect delivery

`gregale workflow attempts [--failed-only] [--app <SLUG>] [--workflow-name <NAME>] <run_id|latest> <step_name>`

| Flag | Meaning | |
|---|---|---|
| `--failed-only` | show only failed attempts |  |
| `--app <SLUG>` | app for latest (defaults to linked project app) |  |
| `--workflow-name <NAME>` | exact workflow name for latest (inferred when the app&#39;s recent runs share one) |  |

Examples:

```sh
gregale workflow attempts latest charge-card --workflow-name billing --failed-only
gregale workflow attempts latest charge-card --workflow-name billing
```

### workflow retry

Retry one safely resumable failed HTTP step

`gregale workflow retry [--interactive] [--app <slug>] [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] [<run_id>] [<step_name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a run; preview and confirm changes for retry or cancel |  |
| `--app <slug>` | app for interactive run selection (otherwise use linked app or picker) |  |
| `--wait` | follow the retried run to success (explicit run and step only) |  |
| `--timeout <DURATION>` | wait deadline after retry acceptance (default 5m; requires --wait) |  |
| `--poll-interval <DURATION>` | wait read interval (default 2s; 1s to 1h; requires --wait) |  |

Examples:

```sh
gregale workflow retry --app billing --interactive
gregale workflow retry <run_id> <step_name>
gregale workflow retry RUN_ID STEP_NAME --wait
```

### workflow resume

Resume eligible failed actions in a workflow run

`gregale workflow resume [--wait] [--timeout <DURATION>] [--poll-interval <DURATION>] --expected-resume-count <N> [--idempotency-key <KEY>] <run_id>`

| Flag | Meaning | |
|---|---|---|
| `--wait` | follow the resumed run to success without another resume request |  |
| `--timeout <DURATION>` | wait deadline after resume acceptance (default 5m; requires --wait) |  |
| `--poll-interval <DURATION>` | wait read interval (default 2s; 1s to 1h; requires --wait) |  |
| `--expected-resume-count <N>` | current resume_count shown by workflows status | required |
| `--idempotency-key <KEY>` | stable key for retrying the same resume request |  |

Examples:

```sh
gregale workflow resume RUN_ID --expected-resume-count 1 --wait
```

### workflow resumes

List continuation history for an exact or latest workflow run

`gregale workflow resumes [--app <SLUG>] [--workflow-name <NAME>] <run_id|latest>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app for latest (defaults to linked project app) |  |
| `--workflow-name <NAME>` | exact workflow name for latest (inferred when the app&#39;s recent runs share one) |  |

Examples:

```sh
gregale workflow resumes latest --workflow-name billing
```

### workflow cancel

Cancel an active workflow run

`gregale workflow cancel [--interactive] [--app <slug>] [--dry-run] [--queued] [<run_id>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a run; preview and confirm changes for retry or cancel |  |
| `--app <slug>` | app for interactive run selection (otherwise use linked app or picker) |  |
| `--dry-run` | with --queued: show the runs it would cancel (with it: --workflow-name, --run-id) |  |
| `--queued` | cancel selected runs that are still pending and never started (with it: --workflow-name, --run-id, --yes) |  |

Examples:

```sh
gregale workflow cancel --app billing --interactive
gregale workflow cancel <run_id>
```

### workflow events

Send external event to a workflow run

`gregale workflow events [--payload <JSON>] [--payload-file <PATH|->] <run_id> <event_name>`

| Flag | Meaning | |
|---|---|---|
| `--payload <JSON>` | JSON event payload (default {}; excludes --payload-file) |  |
| `--payload-file <PATH|->` | read one JSON value from a file, or - for stdin; request maximum 1 MiB; excludes --payload |  |

Examples:

```sh
gregale workflow events RUN_ID approved --payload-file approval.json
gregale workflow events RUN_ID approved --payload-file -
```


## webhook

Register webhooks and deliver events to them

`gregale webhook [<subcommand>]`

### webhook secret

Rotate the webhook signing secret

#### webhook secret rotate

Rotate the webhook signing secret

`gregale webhook secret rotate --app <slug> [--secret <VALUE>] [--from-stdin] <webhook-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--secret <VALUE>` | replacement HMAC-SHA256 secret |  |
| `--from-stdin` | read the replacement secret from stdin |  |

### webhook account

Manage one release receiver across all account apps

#### webhook account secret

Replace the signing secret

`gregale webhook account secret <rotate>`

##### webhook account secret rotate

Replace the signing secret

`gregale webhook account secret rotate [--secret <VALUE>] [--from-stdin] <id>`

| Flag | Meaning | |
|---|---|---|
| `--secret <VALUE>` | signing secret (prefer --from-stdin) |  |
| `--from-stdin` | read the signing secret from stdin; mutually exclusive with --secret |  |

Read it with --from-stdin, or pass --secret.

#### webhook account list

List account release receivers

`gregale webhook account list`

#### webhook account create

Create a receiver; omitted signing secret is generated and shown once

`gregale webhook account create --target-url <URL> --event <EVENT>... [--secret <VALUE>] [--from-stdin] [--retry-policy <POLICY>] [--delivery-format <FORMAT>]`

| Flag | Meaning | |
|---|---|---|
| `--target-url <URL>` | HTTPS receiver URL | required |
| `--event <EVENT>` | distinct account event (repeatable) | required; one of `deployment.live` · `deployment.failed` · `rollout.completed` · `rollout.aborted` · `environment.gitops.changed` · `agent.approval.requested` · `agent.approval.approved` · `agent.approval.denied` · `execution.finished` · `execution.budget.threshold` · `execution.pipeline.finished` |
| `--secret <VALUE>` | signing secret (prefer --from-stdin) |  |
| `--from-stdin` | read the signing secret from stdin; mutually exclusive with --secret |  |
| `--retry-policy <POLICY>` | delivery retry policy | one of `default` · `aggressive` · `none` |
| `--delivery-format <FORMAT>` | delivery envelope | one of `json` · `cloudevents` |

#### webhook account get

Show one receiver with a masked signing secret

`gregale webhook account get <id>`

#### webhook account update

Update a receiver

`gregale webhook account update [--target-url <URL>] [--event <EVENT>]... [--enable] [--disable] [--secret <VALUE>] [--from-stdin] [--retry-policy <POLICY>] [--delivery-format <FORMAT>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--target-url <URL>` | replacement HTTPS receiver URL |  |
| `--event <EVENT>` | replacement account event filter (repeatable) | one of `deployment.live` · `deployment.failed` · `rollout.completed` · `rollout.aborted` · `environment.gitops.changed` · `agent.approval.requested` · `agent.approval.approved` · `agent.approval.denied` · `execution.finished` · `execution.budget.threshold` · `execution.pipeline.finished` |
| `--enable` | enable the receiver; mutually exclusive with --disable |  |
| `--disable` | disable the receiver; mutually exclusive with --enable |  |
| `--secret <VALUE>` | signing secret (prefer --from-stdin) |  |
| `--from-stdin` | read the signing secret from stdin; mutually exclusive with --secret |  |
| `--retry-policy <POLICY>` | delivery retry policy | one of `default` · `aggressive` · `none` |
| `--delivery-format <FORMAT>` | delivery envelope | one of `json` · `cloudevents` |

#### webhook account delete

Delete a receiver

`gregale webhook account delete [--yes] <id>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | accepted for consistency; rm does not prompt |  |

#### webhook account deliveries

Page through receiver deliveries

`gregale webhook account deliveries [--page-size <N>] [--page-token <CURSOR>] <id>`

| Flag | Meaning | |
|---|---|---|
| `--page-size <N>` | delivery page size (1..100, default 50) |  |
| `--page-token <CURSOR>` | opaque delivery cursor |  |

#### webhook account retry

Retry one failed delivery

`gregale webhook account retry <id> <delivery-id>`

### webhook list

List an app&#39;s webhooks

`gregale webhook list [--app <slug>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (alternative to the positional) |  |

The app defaults to the linked one.

### webhook create

Add a webhook

`gregale webhook create --app <slug> --target-url <URL> [--event <EVENT>] [--retry-policy <POLICY>] [--delivery-format <FORMAT>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--target-url <URL>` | http(s) target URL (https recommended: deliveries are signed, not encrypted) | required |
| `--event <EVENT>` | event to deliver (repeat) |  |
| `--retry-policy <POLICY>` | default\|aggressive\|none |  |
| `--delivery-format <FORMAT>` | json\|cloudevents |  |

### webhook get

Show one webhook

`gregale webhook get --app <slug> <webhook-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |

### webhook update

Update one webhook

`gregale webhook update --app <slug> [--target-url <URL>] [--retry-policy <POLICY>] [--delivery-format <FORMAT>] [--enable] [--disable] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--target-url <URL>` | new http(s) target URL |  |
| `--retry-policy <POLICY>` | new retry policy | one of `default` · `aggressive` · `none` |
| `--delivery-format <FORMAT>` | new delivery format | one of `json` · `cloudevents` |
| `--enable` | enable the webhook |  |
| `--disable` | disable the webhook |  |

### webhook delete

Delete one webhook

`gregale webhook delete --app <slug> [--yes] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |
| `--yes` | accepted for consistency; rm does not prompt |  |

### webhook deliveries

Show the delivery ledger

`gregale webhook deliveries --app <SLUG> [--status <STATUS>] [--limit <N>] [--page-size <N>] [--cursor <CURSOR>] [--page-token <CURSOR>] [--all] <id>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--status <STATUS>` | filter delivery status | one of `pending` · `in_flight` · `succeeded` · `failed` · `dead` |
| `--limit <N>` | page size (1..100, default 50) |  |
| `--page-size <N>` | alias for --limit |  |
| `--cursor <CURSOR>` | opaque continuation cursor |  |
| `--page-token <CURSOR>` | alias for --cursor |  |
| `--all` | walk every page using --limit and --cursor |  |

### webhook retry

Retry a failed delivery

`gregale webhook retry --app <slug> <webhook-id> <delivery-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug | required |


## metric

Show app metrics, request breakdowns and the service map

`gregale metric [<subcommand>] [<slug>] [--range <WINDOW>] [--account] [--custom <NAME>]`

| Flag | Meaning | |
|---|---|---|
| `--range <WINDOW>` | window (5m\|15m\|1h\|6h\|24h\|7d\|15d) | one of `5m` · `15m` · `1h` · `6h` · `24h` · `7d` · `15d` |
| `--account` | account-wide roll-up |  |
| `--custom <NAME>` | history of one pushed custom metric (ranges 1h\|6h\|24h\|7d\|15d) |  |

### metric requests

Break down past requests by route, country, referrer or status

`gregale metric requests [--since <WINDOW>] [--until <TIMESTAMP>] [--by <DIMENSION>] [--range <WINDOW>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--since <WINDOW>` | lookback window |  |
| `--until <TIMESTAMP>` | exclusive RFC3339 end |  |
| `--by <DIMENSION>` | grouping dimension | one of `route` · `country` · `referrer_host` · `ua_family` · `status` · `consumer_id` |
| `--range <WINDOW>` | lookback window; same as --since (gregale metric --range) |  |


## debug

Inspect requests, traces, issues, profiles and regressions

`gregale debug [<subcommand>] [flags] <slug> [<request-id>]`

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

`gregale debug regressions [watch|ack|acknowledge|dismiss|resolve|reopen|rollback] [--since <DURATION>] [--all] [<slug>]`

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

`gregale debug regressions ack --release <UUID> --route <PATH> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--release <UUID>` | regression deployment UUID (also --deployment-id) | required |
| `--route <PATH>` | regression route | required |

#### debug regressions acknowledge

Acknowledge a regression

`gregale debug regressions acknowledge --release <UUID> --route <PATH> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--release <UUID>` | regression deployment UUID (also --deployment-id) | required |
| `--route <PATH>` | regression route | required |

#### debug regressions dismiss

Dismiss a regression

`gregale debug regressions dismiss --release <UUID> --route <PATH> [--dismissed-until <RFC3339>] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--release <UUID>` | regression deployment UUID (also --deployment-id) | required |
| `--route <PATH>` | regression route | required |
| `--dismissed-until <RFC3339>` | dismissal expiry (RFC3339; default 24h) |  |

#### debug regressions resolve

Resolve a regression

`gregale debug regressions resolve --release <UUID> --route <PATH> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--release <UUID>` | regression deployment UUID (also --deployment-id) | required |
| `--route <PATH>` | regression route | required |

#### debug regressions reopen

Reopen a regression

`gregale debug regressions reopen --release <UUID> --route <PATH> <slug>`

| Flag | Meaning | |
|---|---|---|
| `--release <UUID>` | regression deployment UUID (also --deployment-id) | required |
| `--route <PATH>` | regression route | required |

#### debug regressions rollback

Roll back an app from the regressions view

`gregale debug regressions rollback [--to <ID>] --yes <slug>`

| Flag | Meaning | |
|---|---|---|
| `--to <ID>` | target superseded deployment ID |  |
| `--yes` | confirm the rollback | required |


## audit

Search your account&#39;s audit log

`gregale audit [<subcommand>] [<id>]`

### audit list

List audit events

`gregale audit list [--kind-prefix <PREFIX>] [--app <SLUG>] [--app-id <UUID>] [--since <RFC3339>] [--limit <N>] [--include-anonymous] [--verbose]`

| Flag | Meaning | |
|---|---|---|
| `--kind-prefix <PREFIX>` | filter by kind prefix |  |
| `--app <SLUG>` | filter by app slug |  |
| `--app-id <UUID>` | filter by app UUID |  |
| `--since <RFC3339>` | RFC3339 lower bound |  |
| `--limit <N>` | maximum rows (1..100; default 50) |  |
| `--include-anonymous` | include rows without a subject |  |
| `--verbose` | expand stateless advisory rows |  |

### audit get

Show one audit event

`gregale audit get <id>`


## alert

Manage alert rules, channels, incidents, SLOs and checks

`gregale alert [<subcommand>] [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug |  |

### alert maintenance

list · create · cancel

#### alert maintenance list

List maintenance windows including expired and cancelled history

`gregale alert maintenance list --app <SLUG> [--env <SCOPE>] [--cursor <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--env <SCOPE>` | environment (default default) (also --scope) |  |
| `--cursor <CURSOR>` | history cursor (also --before) |  |
| `--limit <N>` | page size 1–50 |  |

#### alert maintenance create

Silence MCP readiness notifications while still recording incidents

`gregale alert maintenance create --app <SLUG> [--env <SCOPE>] [--since <TIME>] [--until <TIME>] [--duration <DURATION>] --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--env <SCOPE>` | exact environment (default default) (also --scope) |  |
| `--since <TIME>` | RFC3339 start (default now) (also --start) |  |
| `--until <TIME>` | RFC3339 end; mutually exclusive with duration (also --end) |  |
| `--duration <DURATION>` | duration from start up to 720h |  |
| `--reason <TEXT>` | maintenance reason | required |

#### alert maintenance cancel

End a window and retain cancellation attribution

`gregale alert maintenance cancel --app <SLUG> --id <UUID> --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--id <UUID>` | maintenance window | required |
| `--reason <TEXT>` | cancellation reason | required |

### alert pagerduty

get · set · disable · ack-sync

#### alert pagerduty get

Read masked PagerDuty settings

`gregale alert pagerduty get --app <SLUG> --rule <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |

#### alert pagerduty set

Use PagerDuty as primary for unattempted incidents

`gregale alert pagerduty set --app <SLUG> --rule <UUID> --expected-version <N> --key-env <ENV> [--region <us|eu>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--expected-version <N>` | current reminder defaults version | required |
| `--key-env <ENV>` | environment variable containing the integration key | required |
| `--region <us|eu>` | PagerDuty Events API region (default us) |  |

#### alert pagerduty disable

Restore webhook primary for unattempted incidents

`gregale alert pagerduty disable --app <SLUG> --rule <UUID> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--expected-version <N>` | current reminder defaults version | required |

#### alert pagerduty ack-sync

Sync signed PagerDuty acknowledgements into readiness reminders

`gregale alert pagerduty ack-sync <get|set|disable|events>`

##### alert pagerduty ack-sync get

Read masked webhook configuration

`gregale alert pagerduty ack-sync get --app <SLUG> --rule <UUID> [--destination <primary|fallback>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--destination <primary|fallback>` | native PagerDuty receiver |  |

##### alert pagerduty ack-sync set

Bind a service and webhook subscription signing secret

`gregale alert pagerduty ack-sync set --app <SLUG> --rule <UUID> --expected-version <N> [--destination <primary|fallback>] [--expected-pagerduty-version <N>] [--expected-fallback-version <N>] --service-id <ID> --secret-env <ENV> [--sync-unacknowledgements]`

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

##### alert pagerduty ack-sync disable

Revoke webhook signing credentials immediately

`gregale alert pagerduty ack-sync disable --app <SLUG> --rule <UUID> --expected-version <N> [--destination <primary|fallback>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--expected-version <N>` | current reminder defaults version | required |
| `--destination <primary|fallback>` | native PagerDuty receiver; omission selects the original callback binding |  |

##### alert pagerduty ack-sync events

Page through durable provider acknowledgement receipts

`gregale alert pagerduty ack-sync events --app <SLUG> --rule <UUID> [--cursor <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--cursor <CURSOR>` | opaque event cursor (also --before) |  |
| `--limit <N>` | page size 1–50 |  |

### alert secret

Rotate the alert&#39;s webhook secret

#### alert secret rotate

Rotate the alert&#39;s webhook secret

`gregale alert secret rotate [--app <slug>] [--from-stdin] <alert-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to the linked app) |  |
| `--from-stdin` | read the replacement secret from stdin |  |

### alert channel

Manage where alerts are sent: Slack, PagerDuty or email

#### alert channel list

List channels with their last delivery

`gregale alert channel list`

#### alert channel create

Add a channel; Slack URLs and PagerDuty keys are read from stdin

`gregale alert channel create --name <NAME> --kind <KIND> [--secret-stdin] [--pagerduty-region <REGION>] [--email <EMAIL>]`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | channel name, unique per account | required |
| `--kind <KIND>` | destination type | required; one of `slack` · `pagerduty` · `email` |
| `--secret-stdin` | read the Slack webhook URL or PagerDuty routing key from stdin |  |
| `--pagerduty-region <REGION>` | PagerDuty service region | one of `us` · `eu` |
| `--email <EMAIL>` | your account email (kind email) |  |

#### alert channel update

Rename a channel or replace its destination; read from stdin

`gregale alert channel update [--name <NAME>] [--secret-stdin] [--pagerduty-region <REGION>] <channel-id>`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | new channel name, unique per account |  |
| `--secret-stdin` | read the new Slack webhook URL or PagerDuty routing key from stdin |  |
| `--pagerduty-region <REGION>` | PagerDuty service region of the new key | one of `us` · `eu` |

#### alert channel test

Send a marked test notification

`gregale alert channel test <channel-id>`

#### alert channel delete

Delete a channel

`gregale alert channel delete <channel-id>`

### alert list

List alert rules

`gregale alert list [--app <slug>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to the linked app) |  |

### alert create

Add an alert rule

`gregale alert create [--app <slug>] --name <NAME> [--metric <METRIC>] [--comparison <OP>] [--threshold <N>] [--window-spec <WINDOW>] [--event-subscription-id <UUID>] [--edge-rule-id <ID>] [--mcp-readiness-scope <SCOPE>] [--failure-source <SOURCE>] [--channel <CHANNEL_ID>]... [--custom-metric <NAME>] [--metric-label <KEY=VALUE>]... [--log-query <FILTERS>] [--saved-search <NAME>] [--slo <SLO_ID>] [--synthetic-check <CHECK_ID>] [--webhook-url <URL>] [--action <ACTION>] [--post-deploy-rollback-window <duration>] [--webhook-secret-stdin] [--webhook-secret <VALUE>] [--notify-resolved]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to the linked app) |  |
| `--name <NAME>` | rule name (3..120 chars) | required |
| `--metric <METRIC>` | metric (mcp_tasks_metrics_stale includes the min=0 observer heartbeat) | one of `mcp_protocol_error_rate_pct` · `mcp_protocol_execution_p95_ms` · `route_insights_detected` · `route_monitor_incidents` · `route_health_holds` · `ai_budget_alerts` · `oom_kills` · `event_recovery_notification_admission_overdue_jobs` · `event_recovery_notification_admission_dead_jobs` · `event_recovery_notification_admission_unknown_jobs` · `event_recovery_notification_admission_no_receivers_jobs` · `event_recovery_notification_execution_overdue_jobs` · `event_recovery_notification_execution_dead_jobs` · `event_recovery_notification_execution_unknown_jobs` · `event_recovery_notification_execution_no_receivers_jobs` · `event_retention_expiring_receipts` · `event_storage_utilization_pct` · `event_execution_dead_letters` · `event_execution_dead_letter_rate_per_second` · `event_handler_failure_pct` · `event_completion_latency_p95_seconds` · `event_recovery_stalled_jobs` · `event_recovery_expiring_jobs` · `event_recovery_execution_waiting_jobs` · `event_recovery_execution_prolonged_wait_jobs` · `event_recovery_execution_unknown_jobs` · `event_recovery_execution_retention_risk_jobs` · `event_recovery_capacity_wait_jobs` · `event_pending_recipients` · `event_oldest_pending_seconds` · `event_retry_rate_per_second` · `event_terminal_failure_pct` · `event_routing_latency_p95_seconds` · `event_paused_seconds` · `event_drain_rate_per_second` · `error_rate_pct` · `latency_p50_ms` · `latency_p95_ms` · `latency_p99_ms` · `cold_start_pct` · `request_count` · `failed_invocations` · `api_up` · `account_spend_eur` · `deployment_failed` · `cert_expiry_seconds` · `cert_issuance_failed` · `queue_depth` · `queue_backlog` · `queue_oldest_age_seconds` · `pre_auth_target_threshold` · `pre_auth_target_signal_gap_pct` · `pre_auth_pressure` · `edge_validation_failures` · `edge_rejections` · `edge_waf_detections` · `workflow_failures` · `workflow_schedule_quota_skips` · `workflow_pending_age_seconds` · `workflow_waiting_age_seconds` · `workflow_due_age_seconds` · `log_error_lines` · `log_warn_lines` · `edge_rule_hits` · `edge_rule_log_hits` · `edge_rule_activity` · `new_error_fingerprint` · `cold_wake_rate_pct` · `daily_cost_cents` · `slo_burn_rate` · `slo_budget_burn` · `slo_budget_remaining_pct` · `synthetic_check_consecutive_failures` · `synthetic_check_latency_p95_ms` · `canary_stuck_step` · `safedeploy_audit_emit_failing` · `deployment_audit_gc_failing` · `canary_fleet_in_flight_high` · `mcp_tasks_oldest_age_seconds` · `mcp_tasks_metrics_stale` · `mcp_task_promotion_hold_age_seconds` · `mcp_policy_unconverged_gateways` · `mcp_policy_unverified` · `mcp_readiness_unhealthy` · `mcp_catalog_drift` · `mcp_catalog_monitor_unavailable` · `mcp_oauth_reauthorization_required` · `mcp_oauth_renewal_failed` · `mcp_oauth_retirement_expiring` · `mcp_oauth_issuance_unknown` · `custom_metric` · `log_search_matches` |
| `--comparison <OP>` | gt\|gte\|lt\|lte\|above_baseline\|below_baseline |  |
| `--threshold <N>` | threshold value |  |
| `--window-spec <WINDOW>` | 5m\|15m\|1h\|6h\|24h\|7d\|15d |  |
| `--event-subscription-id <UUID>` | Subscription UUID for event consumer metrics |  |
| `--edge-rule-id <ID>` | edge rule for edge_rule_hits / edge_rule_log_hits (empty = all rules) |  |
| `--mcp-readiness-scope <SCOPE>` | readiness environment; omitted means all environments (immutable) |  |
| `--failure-source <SOURCE>` | any\|cron\|queue\|delayed_task\|async_invoke\|inbound_webhook |  |
| `--channel <CHANNEL_ID>` | notification channel id (repeatable); with one, the webhook is optional |  |
| `--custom-metric <NAME>` | Pushed custom metric to watch; required with --metric custom_metric |  |
| `--metric-label <KEY=VALUE>` | with --custom-metric: watch the series with this label (repeatable) |  |
| `--log-query <FILTERS>` | Log search filters to count, e.g. level=error&amp;q=timeout; with --metric log_search_matches |  |
| `--saved-search <NAME>` | Copy a saved log search&#39;s filters; with --metric log_search_matches |  |
| `--slo <SLO_ID>` | SLO id for slo_budget_burn or slo_budget_remaining_pct |  |
| `--synthetic-check <CHECK_ID>` | synthetic check id for synthetic_check_* metrics |  |
| `--webhook-url <URL>` | http(s) webhook URL (https recommended: deliveries are signed, not encrypted); required unless --channel |  |
| `--action <ACTION>` | alert action | one of `webhook` · `rollback` · `demote` · `promote` |
| `--post-deploy-rollback-window <duration>` | completed-release rollback window (0 off; up to 1h) |  |
| `--webhook-secret-stdin` | read the webhook signing secret from stdin (this or --webhook-secret is required) |  |
| `--webhook-secret <VALUE>` | webhook signing secret (prefer --webhook-secret-stdin) |  |
| `--notify-resolved` | also send the webhook a signed alert.resolved event when the rule recovers |  |

Examples:

```sh
printf '%s\n' "$WEBHOOK_SECRET" | gregale alert create --app my-api --name p95-latency --metric latency_p95_ms --comparison gt --threshold 800 --window-spec 15m --webhook-url https://hooks.example.com/gregale --webhook-secret-stdin
printf '%s\n' "$WEBHOOK_SECRET" | gregale alert create --app mcp-worker --name mcp-oldest-task-age --metric mcp_tasks_oldest_age_seconds --comparison gt --threshold 300 --window-spec 5m --webhook-url https://hooks.example.com/gregale --webhook-secret-stdin
printf '%s\n' "$WEBHOOK_SECRET" | gregale alert create --app mcp-worker --name mcp-task-metrics-stale --metric mcp_tasks_metrics_stale --comparison gt --threshold 0 --window-spec 5m --webhook-url https://hooks.example.com/gregale --webhook-secret-stdin
```

### alert get

Show one alert rule and its last delivery

`gregale alert get [--app <slug>] <alert-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to the linked app) |  |

### alert deliveries

List a rule&#39;s webhook deliveries, newest first

`gregale alert deliveries [--app <slug>] [--interactive] [--limit <N>] [--include-test] [<alert-id>]`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (linked app or picker in interactive mode) |  |
| `--interactive` | choose a rule and delivery options |  |
| `--limit <N>` | max deliveries (1..100, default 20) |  |
| `--include-test` | include test deliveries |  |

Examples:

```sh
gregale alert deliveries --app my-api --interactive
```

### alert update

Update one alert rule

`gregale alert update [--interactive] [--app <slug>] [--name <NAME>] [--enabled] [--metric <METRIC>] [--comparison <OP>] [--threshold <N>] [--window-spec <WINDOW>] [--cooldown-minutes <N>] [--webhook-url <URL>] [--action <ACTION>] [--post-deploy-rollback-window <duration>] [--webhook-secret-stdin] [--notify-resolved] [<alert-id>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a rule, edit current settings, and confirm changes |  |
| `--app <slug>` | app slug (linked app or picker in interactive mode) |  |
| `--name <NAME>` | rule name (3..120 chars) |  |
| `--enabled` | turn the rule on or off (--enabled=false pauses it) |  |
| `--metric <METRIC>` | metric | one of `mcp_protocol_error_rate_pct` · `mcp_protocol_execution_p95_ms` · `route_insights_detected` · `route_monitor_incidents` · `route_health_holds` · `ai_budget_alerts` · `oom_kills` · `event_recovery_notification_admission_overdue_jobs` · `event_recovery_notification_admission_dead_jobs` · `event_recovery_notification_admission_unknown_jobs` · `event_recovery_notification_admission_no_receivers_jobs` · `event_recovery_notification_execution_overdue_jobs` · `event_recovery_notification_execution_dead_jobs` · `event_recovery_notification_execution_unknown_jobs` · `event_recovery_notification_execution_no_receivers_jobs` · `event_retention_expiring_receipts` · `event_storage_utilization_pct` · `event_execution_dead_letters` · `event_execution_dead_letter_rate_per_second` · `event_handler_failure_pct` · `event_completion_latency_p95_seconds` · `event_recovery_stalled_jobs` · `event_recovery_expiring_jobs` · `event_recovery_execution_waiting_jobs` · `event_recovery_execution_prolonged_wait_jobs` · `event_recovery_execution_unknown_jobs` · `event_recovery_execution_retention_risk_jobs` · `event_recovery_capacity_wait_jobs` · `event_pending_recipients` · `event_oldest_pending_seconds` · `event_retry_rate_per_second` · `event_terminal_failure_pct` · `event_routing_latency_p95_seconds` · `event_paused_seconds` · `event_drain_rate_per_second` · `error_rate_pct` · `latency_p50_ms` · `latency_p95_ms` · `latency_p99_ms` · `cold_start_pct` · `request_count` · `failed_invocations` · `api_up` · `account_spend_eur` · `deployment_failed` · `cert_expiry_seconds` · `cert_issuance_failed` · `queue_depth` · `queue_backlog` · `queue_oldest_age_seconds` · `pre_auth_target_threshold` · `pre_auth_target_signal_gap_pct` · `pre_auth_pressure` · `edge_validation_failures` · `edge_rejections` · `edge_waf_detections` · `workflow_failures` · `workflow_schedule_quota_skips` · `workflow_pending_age_seconds` · `workflow_waiting_age_seconds` · `workflow_due_age_seconds` · `log_error_lines` · `log_warn_lines` · `edge_rule_hits` · `edge_rule_log_hits` · `edge_rule_activity` · `new_error_fingerprint` · `cold_wake_rate_pct` · `daily_cost_cents` · `slo_burn_rate` · `slo_budget_burn` · `slo_budget_remaining_pct` · `synthetic_check_consecutive_failures` · `synthetic_check_latency_p95_ms` · `canary_stuck_step` · `safedeploy_audit_emit_failing` · `deployment_audit_gc_failing` · `canary_fleet_in_flight_high` · `mcp_tasks_oldest_age_seconds` · `mcp_tasks_metrics_stale` · `mcp_task_promotion_hold_age_seconds` · `mcp_policy_unconverged_gateways` · `mcp_policy_unverified` · `mcp_readiness_unhealthy` · `mcp_catalog_drift` · `mcp_catalog_monitor_unavailable` · `mcp_oauth_reauthorization_required` · `mcp_oauth_renewal_failed` · `mcp_oauth_retirement_expiring` · `mcp_oauth_issuance_unknown` · `custom_metric` · `log_search_matches` |
| `--comparison <OP>` | gt\|gte\|lt\|lte\|above_baseline\|below_baseline |  |
| `--threshold <N>` | threshold value |  |
| `--window-spec <WINDOW>` | 5m\|15m\|1h\|6h\|24h\|7d\|15d |  |
| `--cooldown-minutes <N>` | minutes between repeat notifications of one breach |  |
| `--webhook-url <URL>` | http(s) webhook URL |  |
| `--action <ACTION>` | alert action | one of `webhook` · `rollback` · `demote` · `promote` |
| `--post-deploy-rollback-window <duration>` | completed-release rollback window (0 off; up to 1h) |  |
| `--webhook-secret-stdin` | read the replacement webhook secret from stdin |  |
| `--notify-resolved` | send the webhook alert.resolved events (=false turns them off) |  |

Examples:

```sh
gregale alert update --app my-api --interactive
```

### alert delete

Delete one alert rule

`gregale alert delete [--app <slug>] [--yes] <alert-id>`

| Flag | Meaning | |
|---|---|---|
| `--app <slug>` | app slug (defaults to the linked app) |  |
| `--yes` | accepted for consistency; rm does not prompt |  |

### alert preset

Browse or enable catalog alert presets

#### alert preset list

List the global alert preset catalog

`gregale alert preset list`

#### alert preset enable

Create an app alert from a preset or choose one interactively

`gregale alert preset enable [--interactive] [--app <slug>] [--webhook-url <URL>] [--channel <CHANNEL_ID>]... [--webhook-secret-stdin] [--webhook-secret <VALUE>] [--action <ACTION>] [--cooldown-minutes <N>] [--enabled] [<preset-name>]`

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
gregale alert preset enable --app my-api --interactive
```


## mcp

Connect agents, host MCP servers, or call MCP tools

`gregale mcp [<subcommand>]`

### mcp connect

Add Gregale&#39;s hosted MCP server to your agents

`gregale mcp connect [--client <NAME>] [--project] [--toolsets <LIST>] [--headless] [--force] [--dry-run] [--yes] [--check] [--ci] [--repo <OWNER/NAME>] [--scopes <LIST>] [--approvals <LIST>]`

| Flag | Meaning | |
|---|---|---|
| `--client <NAME>` | claude-code, codex, cursor, vscode, windsurf, antigravity or claude-desktop; repeatable (default: every client found) |  |
| `--project` | write this project&#39;s .mcp.json instead of the user-wide config |  |
| `--toolsets <LIST>` | MCP toolsets to list, comma-separated (default core) |  |
| `--headless` | mint an agent key for an agent with no person at a browser |  |
| `--force` | replace an existing gregale entry that differs |  |
| `--dry-run` | show what would be written |  |
| `--yes` | connect every client found without asking |  |
| `--check` | only check why an agent does not see Gregale; changes nothing |  |
| `--ci` | set up an agent in GitHub Actions: key, repository secret, workflow |  |
| `--repo <OWNER/NAME>` | GitHub repository for --ci (default: the current clone&#39;s) |  |
| `--scopes <LIST>` | agent key scopes for --ci (default agent,deploy:write) |  |
| `--approvals <LIST>` | kinds of action the --ci key needs approval for (default every kind; none) |  |

Examples:

```sh
gregale mcp connect
gregale mcp connect --client codex --toolsets core,operate
gregale mcp connect --client claude-code --project
gregale mcp connect --headless --yes
gregale mcp connect --check
gregale mcp connect --ci --repo acme/api
```

With no --client, connects the clients it finds on this machine, asking for each one in a terminal.

The agent signs in through your browser the first time it connects; nothing secret is written. --headless mints an agent key instead and the config reads it from GREGALE_AGENT_KEY.

Claude Code is configured with claude mcp add; Codex&#39;s ~/.codex/config.toml gets only its [mcp_servers.gregale] table; Cursor, VS Code, Windsurf and Antigravity get a gregale entry in their JSON config, keeping everything else; Antigravity signs in only through the browser, so --headless skips it. Claude Desktop adds remote servers only in its settings, so connect prints those steps.

--check changes nothing: it checks the hosted endpoint, each client&#39;s gregale entry, this directory&#39;s .mcp.json, the agent key in GREGALE_AGENT_KEY, your connected agents and approval requests waiting for you, and names the fix for each problem. It exits 1 when a check fails.

### mcp server

Scaffold, deploy and check the MCP servers you host

#### mcp server invocation

Run one journaled registered-tool invocation; arguments come on stdin

`gregale mcp server invocation <run>`

##### mcp server invocation run

Run one journaled registered-tool invocation; arguments come on stdin

`gregale mcp server invocation run --id <ID> [--token-env <ENV>] [--delegated]`

| Flag | Meaning | |
|---|---|---|
| `--id <ID>` | queued invocation UUID | required |
| `--token-env <ENV>` | named MCP_* application OAuth token |  |
| `--delegated` | use the current connection exact native invocation grant |  |

#### mcp server export

Emit remote MCP connection JSON without credentials

`gregale mcp server export [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<slug>]`

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
gregale mcp server export --app my-mcp --name my-mcp
```

#### mcp server approval

Show or choose the channels that get agent approval requests

`gregale mcp server approval <channels>`

##### mcp server approval channels

Show or choose the channels that get agent approval requests

`gregale mcp server approval channels [--set <NAME,NAME>] [--clear]`

| Flag | Meaning | |
|---|---|---|
| `--set <NAME,NAME>` | replace with these comma-separated notification channel names |  |
| `--clear` | remove every channel; requests go to email only |  |

Examples:

```sh
gregale mcp server approval channels
gregale mcp server approval channels --set ops-slack
gregale mcp server approval channels --clear
```

These notification channels also get your agents&#39; approval requests. Without flags, shows them. Each request still emails the person who connected the agent.

Changing them needs an admin credential; an agent key cannot.

#### mcp server monitor

Manage reviewed catalog roles and OAuth connections

`gregale mcp server monitor <check|setup|disable|status|run|pause|resume|restart|stop|connect|reconnect|disconnect>`

##### mcp server monitor check

provider-check catalog observer

`gregale mcp server monitor check [--app <VALUE>] [--role <VALUE>] [--callback-port <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | managed OAuth role |  |
| `--callback-port <VALUE>` | override a dynamic loopback callback port (0–65535) |  |
| `--timeout <VALUE>` | browser authorization deadline |  |

##### mcp server monitor setup

setup catalog observer

`gregale mcp server monitor setup [--env <VALUE>] [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--env <VALUE>` | connection environment (default when omitted) (also --scope) |  |
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

##### mcp server monitor disable

disable catalog observer

`gregale mcp server monitor disable [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

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

##### mcp server monitor status

status catalog observer

`gregale mcp server monitor status [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

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

##### mcp server monitor run

run catalog observer

`gregale mcp server monitor run [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

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

##### mcp server monitor pause

pause catalog observer

`gregale mcp server monitor pause [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

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

##### mcp server monitor resume

resume catalog observer

`gregale mcp server monitor resume [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

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

##### mcp server monitor restart

restart catalog observer

`gregale mcp server monitor restart [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

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

##### mcp server monitor stop

stop catalog observer

`gregale mcp server monitor stop [--app <VALUE>] [--role <VALUE>] [--all-roles] [--oauth-revocation-endpoint <VALUE>] [--oauth-revocation-auth-method <VALUE>] [--oauth-profile <VALUE>] [--oauth-token-auth-method <VALUE>] [--oauth-browser-redirect-uri <VALUE>] [--oauth-callback-port <VALUE>] [--oauth-resource <VALUE>] [--oauth-issuer <VALUE>] [--oauth-discovery-url <VALUE>] [--oauth-guided] [--oauth-authorization-endpoint <VALUE>] [--oauth-scopes <VALUE>] [--oauth-token-endpoint <VALUE>] [--oauth-client-id <VALUE>] [--oauth-refresh-secret-ref <VALUE>] [--oauth-client-secret-ref <VALUE>] [--managed] [--secret-ref <VALUE>] [--baseline <VALUE>] [--freshness-seconds <VALUE>] [--token-env <VALUE>] [--endpoint <VALUE>] [--interval <VALUE>] [--timeout <VALUE>]`

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

##### mcp server monitor connect

connect catalog observer

`gregale mcp server monitor connect [--app <VALUE>] [--role <VALUE>] [--callback-port <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | managed OAuth role |  |
| `--callback-port <VALUE>` | override a dynamic loopback callback port (0–65535) |  |
| `--timeout <VALUE>` | browser authorization deadline |  |

##### mcp server monitor reconnect

reconnect catalog observer

`gregale mcp server monitor reconnect [--app <VALUE>] [--role <VALUE>] [--callback-port <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | managed OAuth role |  |
| `--callback-port <VALUE>` | override a dynamic loopback callback port (0–65535) |  |
| `--timeout <VALUE>` | browser authorization deadline |  |

##### mcp server monitor disconnect

disconnect catalog observer

`gregale mcp server monitor disconnect [--app <VALUE>] [--role <VALUE>] [--callback-port <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <VALUE>` | HTTP MCP app slug |  |
| `--role <VALUE>` | managed OAuth role |  |
| `--callback-port <VALUE>` | override a dynamic loopback callback port (0–65535) |  |
| `--timeout <VALUE>` | browser authorization deadline |  |

#### mcp server retirements

Inspect and recover durable OAuth grant retirement

`gregale mcp server retirements <ack|list|retry>`

##### mcp server retirements ack

Acknowledge manual provider cleanup

`gregale mcp server retirements ack --id <ID> [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--id <ID>` | retirement UUID | required |
| `--timeout <DURATION>` | API deadline |  |

##### mcp server retirements list

List retirement history

`gregale mcp server retirements list [--offset <N>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--offset <N>` | history page offset |  |
| `--timeout <DURATION>` | API deadline |  |

##### mcp server retirements retry

Retry provider retirement

`gregale mcp server retirements retry --id <ID> [--allow-active-grant-revocation] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--id <ID>` | retirement UUID | required |
| `--allow-active-grant-revocation` | allow active grant revocation |  |
| `--timeout <DURATION>` | API deadline |  |

#### mcp server tasks

Configure, release and inspect durable task workers

`gregale mcp server tasks <deploy|review|run|audit|bind|rehearse|configure|setup|status>`

##### mcp server tasks deploy

Review, deploy and resume a gated web and worker release

`gregale mcp server tasks deploy [--resume] --plan <PATH> [--state <PATH>] [plan|run|status|recover|restore|quarantine|retire]`

| Flag | Meaning | |
|---|---|---|
| `--resume` | resume a healthy failed rollout (recover only) |  |
| `--plan <PATH>` | native deployment plan JSON | required |
| `--state <PATH>` | persistent release journal outside source directories; required except for plan |  |

Examples:

```sh
gregale mcp server tasks deploy plan --plan release.json --json
gregale mcp server tasks deploy --plan release.json --state ./release-state.json
gregale mcp server tasks deploy status --plan release.json --state ./release-state.json
```

plan performs offline source review without --state; all other actions require --state. Review does not run migrations, execute Task code or certify deployed bindings.

##### mcp server tasks review

Prepare, inspect or decide on a durable Task stack review

`gregale mcp server tasks review [--plan <PATH>] [--id <ID>] [--reason <TEXT>] [--intent-sha256 <SHA256>] <prepare|get|approve|reject|revoke>`

| Flag | Meaning | |
|---|---|---|
| `--plan <PATH>` | local native plan required by prepare/get/approve |  |
| `--id <ID>` | stable intent ID; generated on prepare if omitted |  |
| `--reason <TEXT>` | reviewable purpose required by prepare |  |
| `--intent-sha256 <SHA256>` | exact digest required by owner decisions |  |

Examples:

```sh
gregale mcp server tasks review prepare --plan release.json --reason 'Review reports worker upgrade'
gregale mcp server tasks review get --id INTENT --plan release.json
gregale mcp server tasks review approve --id INTENT --plan release.json --intent-sha256 DIGEST
```

Records the owner decision on the pinned review. Approval covers the pinned review only and grants no execution authority. Prepare, get and approve re-review --plan; get invalidates observed drift. Reject/revoke use the retained review and need no source files.

##### mcp server tasks run

Authorize, inspect or plan recovery for a guarded Task release

`gregale mcp server tasks run --id <ID> [--operation-id <ID>] [--handover-id <ID>] [--qualification-sha256 <SHA256>] [--allow-local-checks] [--plan <PATH>] [--bindings-file <PATH>] [--intent-sha256 <SHA256>] [--allow-tool-execution] <authorize|apply|get|guard|inspect|plan-recovery|retire-qualify|retire-approve|retire-revoke|retire>`

| Flag | Meaning | |
|---|---|---|
| `--id <ID>` | exact reviewed intent identity | required |
| `--operation-id <ID>` | immutable owner-selected execution identity |  |
| `--handover-id <ID>` | settled recovery identity for retirement |  |
| `--qualification-sha256 <SHA256>` | separate retirement qualification digest |  |
| `--allow-local-checks` | allow reviewed local checks for retire-qualify |  |
| `--plan <PATH>` | owner-reviewed plan, authorize only |  |
| `--bindings-file <PATH>` | private owner binding JSON, authorize only |  |
| `--intent-sha256 <SHA256>` | owner-reviewed digest, authorize only |  |
| `--allow-tool-execution` | explicit consent to reviewed code, migrations and release effects |  |

Execution authority is separate from review approval. Only the requesting key can apply. Inspect and plan-recovery use native GET APIs and require no local files or bindings. Recovery plans include lifecycle progress and the next step, never grant execution, and never resume fenced executors or repeat committed recovery work. Retirement requires retained restore/quarantine results, separate owner qualification/approval and execution consent. Pending retirement only reconciles shutdown.

##### mcp server tasks audit

Inspect or decide on retained hosted Task source

`gregale mcp server tasks audit --id <ID> [--review-sha256 <SHA256>] [--intent-sha256 <SHA256>] <get|check|approve|reject|revoke>`

| Flag | Meaning | |
|---|---|---|
| `--id <ID>` | retained hosted source review identity | required |
| `--review-sha256 <SHA256>` | exact review fingerprint for checks and decisions |  |
| `--intent-sha256 <SHA256>` | exact intent digest for owner decisions |  |

Examples:

```sh
gregale mcp server tasks audit get --id INTENT
gregale mcp server tasks audit approve --id INTENT --intent-sha256 DIGEST --review-sha256 REVIEW
```

Source is retained for the original fifteen-minute deadline. Review metadata is readable by its requester and owner; decisions require an unscoped personal owner. Approval binds the exact source review and grants no credential or execution authority. Checks permanently invalidate observed drift.

##### mcp server tasks bind

Qualify or inspect hosted Task bindings

`gregale mcp server tasks bind [--id <INTENT>] [--intent-sha256 <DIGEST>] [--review-sha256 <DIGEST>] [--namespace <NAMESPACE>] [--web-database-ref <REF>] [--worker-database-ref <REF>] [--migration-database-ref <REF>] [--observer-database-ref <REF>] [--binding-sha256 <DIGEST>] <qualify|get|revoke>`

| Flag | Meaning | |
|---|---|---|
| `--id <INTENT>` | Retained source intent identity |  |
| `--intent-sha256 <DIGEST>` | Exact approved intent digest |  |
| `--review-sha256 <DIGEST>` | Exact approved review digest |  |
| `--namespace <NAMESPACE>` | Task queue namespace |  |
| `--web-database-ref <REF>` | Web native managed PostgreSQL reference |  |
| `--worker-database-ref <REF>` | Worker native managed PostgreSQL reference |  |
| `--migration-database-ref <REF>` | Worker migration-only reference |  |
| `--observer-database-ref <REF>` | Observer read-only reference |  |
| `--binding-sha256 <DIGEST>` | Exact binding fingerprint for revocation |  |

Examples:

```sh
gregale mcp server tasks bind get --id INTENT
gregale mcp server tasks bind revoke --id INTENT --binding-sha256 DIGEST
```

Owner-selected native managed PostgreSQL references only. Qualification runs bounded read-only catalog probes once per source intent; evidence expires within one minute or on drift. Credentials remain private and execution requires separate owner authorization.

##### mcp server tasks rehearse

Plan or approve an isolated hosted Task rehearsal

`gregale mcp server tasks rehearse --id <INTENT> [--intent-sha256 <DIGEST>] [--review-sha256 <DIGEST>] [--binding-sha256 <DIGEST>] [--plan-sha256 <DIGEST>] <plan|get|approve|revoke>`

| Flag | Meaning | |
|---|---|---|
| `--id <INTENT>` | Retained source intent identity | required |
| `--intent-sha256 <DIGEST>` | Exact retained intent digest for plan |  |
| `--review-sha256 <DIGEST>` | Exact approved review digest for plan |  |
| `--binding-sha256 <DIGEST>` | Exact passing binding digest for plan |  |
| `--plan-sha256 <DIGEST>` | Exact rehearsal plan digest for a decision |  |

An unscoped personal administrator API key is required. A plan reserves a disposable target identity and expires with the passing binding evidence. Approval records one decision and creates no runtime resources.

##### mcp server tasks configure

Preview or apply non-secret role settings before first deployment

`gregale mcp server tasks configure --app <SLUG> --role <ROLE> --namespace <NAME> [--worker-app <SLUG>] [--apply]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | reserved task app slug | required |
| `--role <ROLE>` | task process role | required; one of `web` · `worker` · `observer` |
| `--namespace <NAME>` | shared stable queue namespace | required |
| `--worker-app <SLUG>` | metrics destination for worker or observer |  |
| `--apply` | write the previewed non-secret settings |  |

Examples:

```sh
gregale mcp server tasks configure --app reports-worker --role worker --namespace reports --worker-app reports-worker
gregale mcp server tasks configure --app reports-observer --role observer --namespace reports --worker-app reports-worker --apply
```

##### mcp server tasks setup

Preview or apply the task-backlog scaling policy

`gregale mcp server tasks setup [--app <SLUG>] [--min <N>] [--max <N>] [--target <N>] [--observer-app <SLUG>] [--apply]`

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
gregale mcp server tasks setup --app mcp-worker
gregale mcp server tasks setup --app mcp-worker --observer-app mcp-observer --min 0 --apply
```

##### mcp server tasks status

Show task scaling policy and custom metric freshness

`gregale mcp server tasks status [--app <SLUG>] [--details] [--require-fresh] [--max-oldest-age <DURATION>] [--max-without-compatible-worker <N>] [--sustain-for <DURATION>] [--poll-interval <DURATION>]`

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
gregale mcp server tasks status --app mcp-worker --json
```

#### mcp server approvals

get · set

`gregale mcp server approvals <get|set>`

##### mcp server approvals get

Show or choose the channels that get agent approval requests

`gregale mcp server approvals get [--set <NAME,NAME>] [--clear]`

| Flag | Meaning | |
|---|---|---|
| `--set <NAME,NAME>` | replace with these comma-separated notification channel names |  |
| `--clear` | remove every channel; requests go to email only |  |

Examples:

```sh
gregale mcp server approval channels
gregale mcp server approval channels --set ops-slack
gregale mcp server approval channels --clear
```

These notification channels also get your agents&#39; approval requests. Without flags, shows them. Each request still emails the person who connected the agent.

Changing them needs an admin credential; an agent key cannot.

##### mcp server approvals set

Show or choose the channels that get agent approval requests

`gregale mcp server approvals set [--set <NAME,NAME>] [--clear]`

| Flag | Meaning | |
|---|---|---|
| `--set <NAME,NAME>` | replace with these comma-separated notification channel names |  |
| `--clear` | remove every channel; requests go to email only |  |

Examples:

```sh
gregale mcp server approval channels
gregale mcp server approval channels --set ops-slack
gregale mcp server approval channels --clear
```

These notification channels also get your agents&#39; approval requests. Without flags, shows them. Each request still emails the person who connected the agent.

Changing them needs an admin credential; an agent key cannot.

#### mcp server tool-connection

Manage owner-approved execution OAuth connections

`gregale mcp server tool-connection <create|connect|reconnect|get|list|revoke>`

##### mcp server tool-connection create

Create an immutable execution connection from reviewed provider config

`gregale mcp server tool-connection create --id <UUID> --config <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | caller-retained connection UUID | required |
| `--config <PATH>` | owner-reviewed execution OAuth JSON configuration | required |

##### mcp server tool-connection connect

Authorize a new guided execution connection through browser PKCE

`gregale mcp server tool-connection connect --id <UUID> [--callback-port <PORT>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | exact new guided connection UUID | required |
| `--callback-port <PORT>` | IPv4 loopback callback port; 0 chooses a dynamic port |  |
| `--timeout <DURATION>` | browser authorization deadline, at most 5m |  |

##### mcp server tool-connection reconnect

Replace a guided connection with a new identity and new authorization

`gregale mcp server tool-connection reconnect --id <UUID> --from <UUID> [--callback-port <PORT>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | exact new guided connection UUID | required |
| `--from <UUID>` | old guided connection to replace and locally revoke | required |
| `--callback-port <PORT>` | IPv4 loopback callback port; 0 chooses a dynamic port |  |
| `--timeout <DURATION>` | browser authorization deadline, at most 5m |  |

##### mcp server tool-connection get

Read execution connection state without credentials

`gregale mcp server tool-connection get --id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | exact connection UUID | required |

##### mcp server tool-connection list

List retained execution connections

`gregale mcp server tool-connection list [--cursor <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--cursor <CURSOR>` | opaque continuation |  |
| `--limit <N>` | page size 1-20 |  |

##### mcp server tool-connection revoke

Revoke execution authority without deleting its identity

`gregale mcp server tool-connection revoke --id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | exact connection UUID | required |

#### mcp server tool-publication

Review private candidate test authority and publication receipts

`gregale mcp server tool-publication <get|approve|revoke>`

##### mcp server tool-publication get

Inspect publication and candidate test approval metadata

`gregale mcp server tool-publication get --id <SHA256>`

| Flag | Meaning | |
|---|---|---|
| `--id <SHA256>` | exact publication identity | required |

##### mcp server tool-publication approve

Approve candidate tests with a sealed token or an OAuth connection

`gregale mcp server tool-publication approve --id <SHA256> --request-sha256 <SHA256> [--credential-secret-ref <REF>] [--credential-connection-id <UUID>] [--approve-readiness-roles]`

| Flag | Meaning | |
|---|---|---|
| `--id <SHA256>` | exact publication identity | required |
| `--request-sha256 <SHA256>` | reviewed candidate test request digest | required |
| `--credential-secret-ref <REF>` | owner-selected secret:MCP_* test token reference; exclusive with connection |  |
| `--credential-connection-id <UUID>` | owner-approved execution OAuth connection UUID; exclusive with secret reference |  |
| `--approve-readiness-roles` | approve candidate use of the reviewed policy&#39;s separate role credentials |  |

##### mcp server tool-publication revoke

Revoke pending or running candidate test authority

`gregale mcp server tool-publication revoke --id <SHA256> --request-sha256 <SHA256>`

| Flag | Meaning | |
|---|---|---|
| `--id <SHA256>` | exact publication identity | required |
| `--request-sha256 <SHA256>` | reviewed candidate test request digest | required |

#### mcp server tool-grant

Delegate one exact published tool to an existing agent connection

`gregale mcp server tool-grant <create|get|list|revoke>`

##### mcp server tool-grant create

Create an immutable expiring tool grant as the personal owner

`gregale mcp server tool-grant create --id <UUID> --connection-key <UUID> --version <SHA256> --tool <NAME> --expires-at <TIME> [--allow-invocation] [--credential-secret-ref <REF>] [--credential-connection-id <UUID>]`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | caller-retained grant UUID | required |
| `--connection-key <UUID>` | existing native agent connection key UUID | required |
| `--version <SHA256>` | exact publication version | required |
| `--tool <NAME>` | exact published tool name | required |
| `--expires-at <TIME>` | absolute RFC3339 expiry within 30 days | required |
| `--allow-invocation` | allow exact invocation within separate connection consent |  |
| `--credential-secret-ref <REF>` | existing secret:MCP_* token reference for a private invocation grant |  |
| `--credential-connection-id <UUID>` | owner-approved execution OAuth connection UUID; exclusive with secret reference |  |

##### mcp server tool-grant get

Read a retained tool grant

`gregale mcp server tool-grant get --id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | exact grant UUID | required |

##### mcp server tool-grant list

List the personal account&#39;s retained tool grants

`gregale mcp server tool-grant list [--cursor <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--cursor <CURSOR>` | opaque continuation |  |
| `--limit <N>` | page size 1-20 |  |

##### mcp server tool-grant revoke

Revoke a grant without deleting its identity

`gregale mcp server tool-grant revoke --id <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--id <UUID>` | exact grant UUID | required |

#### mcp server init

Create an MCP starter in Node, Go or Python

`gregale mcp server init --path <DIR> [--language <LANGUAGE>] [--runtime <RUNTIME>] [--auth <MODE>] [--issuer <URL>] [--jwks-url <URL>] [--resource <URL>] [--scopes <SCOPES>] [--tasks] [--name <SLUG>]`

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
gregale mcp server init --path ./my-mcp
gregale mcp server init --path ./my-mcp --runtime go
gregale mcp server init --path ./my-mcp --runtime python
gregale mcp server init --path ./private-mcp --auth external-oauth --issuer https://identity.example.com --jwks-url https://identity.example.com/jwks --resource https://mcp.example.com/mcp --scopes mcp:tools
gregale mcp server init --path ./reports --tasks --name reports
```

#### mcp server import

Generate Node MCP tools from selected public API operations

`gregale mcp server import --spec <FILE> --path <DIR> --server-url <URL> --operations <IDS>`

| Flag | Meaning | |
|---|---|---|
| `--spec <FILE>` | OpenAPI JSON/YAML file, or - for stdin | required |
| `--path <DIR>` | empty destination directory | required |
| `--server-url <URL>` | explicit public HTTPS API base URL | required |
| `--operations <IDS>` | comma-separated exact operation IDs | required |

Examples:

```sh
gregale mcp server import --spec api.yaml --operations listReports,getReport --server-url https://api.example.com/v1 --path ./report-tools
```

#### mcp server capabilities

Show the built-in starter contract and runtime differences

`gregale mcp server capabilities [--runtime <RUNTIME>] [--markdown]`

| Flag | Meaning | |
|---|---|---|
| `--runtime <RUNTIME>` | filter to one starter runtime | one of `node` · `go` · `python` |
| `--markdown` | render the generated starter capability reference |  |

Examples:

```sh
gregale mcp server capabilities --json
gregale mcp server capabilities --runtime python
```

#### mcp server access

Audit MCP access-policy drift and admission evidence

`gregale mcp server access <audit|history|explain>`

##### mcp server access audit

Compare source and role policy with stored account gateway rules

`gregale mcp server access audit --app <SLUG> [--path <DIR>] --auth-placement <PLACEMENT> [--algorithms <LIST>] [--release-policy <PATH>] [--timeout <DURATION>]`

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
gregale mcp server access audit --app my-mcp --path ./my-mcp --auth-placement gateway --json
gregale mcp server access audit --app my-mcp --auth-placement guest-only --release-policy release.json
```

##### mcp server access history

history MCP access evidence

`gregale mcp server access history [--release <VALUE>] [--limit <VALUE>] [--cursor <VALUE>] [--outcome <VALUE>] [--evidence <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--release <VALUE>` | deployment id to inspect (promotion or recovery target) (also --deployment) |  |
| `--limit <VALUE>` | MCP receipts per page, 1–50 |  |
| `--cursor <VALUE>` | next cursor from a previous page |  |
| `--outcome <VALUE>` | filter accepted or rejected receipts |  |
| `--evidence <VALUE>` | inspect an exact evidence UUID instead of deployment history |  |
| `--timeout <VALUE>` | overall read deadline, at most 2m |  |

##### mcp server access explain

explain MCP access evidence

`gregale mcp server access explain [--evidence <VALUE>] [--app <VALUE>] [--timeout <VALUE>]`

| Flag | Meaning | |
|---|---|---|
| `--evidence <VALUE>` | retained admission evidence UUID |  |
| `--app <VALUE>` | optional application slug for current convergence |  |
| `--timeout <VALUE>` | overall read deadline, at most 2m |  |

#### mcp server health

Inspect account MCP connection health

`gregale mcp server health [--app <SLUG>] [--offset <N>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | optional app filter |  |
| `--offset <N>` | issue page offset |  |
| `--timeout <DURATION>` | API deadline |  |

#### mcp server diagnostics

Summarize retained MCP transport failures

`gregale mcp server diagnostics --app <SLUG> [--route <PATH>] [--since <DURATION>] [--cursor <CURSOR>] [--limit <N>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | app slug | required |
| `--route <PATH>` | retained route label |  |
| `--since <DURATION>` | telemetry window |  |
| `--cursor <CURSOR>` | next page cursor |  |
| `--limit <N>` | rows per page |  |
| `--timeout <DURATION>` | API deadline |  |

#### mcp server validate

Validate MCP protocol and catalog compatibility

`gregale mcp server validate --url <URL> [--app <SLUG>] [--release <ID>] [--endpoint-path <PATH>] [--token-env <ENV>] [--protocol-version <VERSION>] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | MCP endpoint | required |
| `--app <SLUG>` | optional app for exact-instance probes |  |
| `--release <ID>` | deployment to qualify (also --deployment) |  |
| `--endpoint-path <PATH>` | configured MCP path |  |
| `--token-env <ENV>` | MCP token environment variable |  |
| `--protocol-version <VERSION>` | selected protocol version |  |
| `--timeout <DURATION>` | overall deadline |  |

#### mcp server test

Run reviewed MCP application smoke fixtures

`gregale mcp server test --url <URL> [--fixtures <PATH>] [--token-env <ENV>] [--allow-tool-execution] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | MCP endpoint | required |
| `--fixtures <PATH>` | fixture JSON file |  |
| `--token-env <ENV>` | MCP token environment variable |  |
| `--allow-tool-execution` | execute reviewed tool fixtures once |  |
| `--timeout <DURATION>` | overall deadline |  |

#### mcp server deploy

Pin the candidate catalog, verify MCP discovery and promote

`gregale mcp server deploy [--path <DIR>] --name <SLUG> [--size <SIZE>] [--profile <SIZE>] [--token-env <ENV>] [--secrets-file <PATH>] [--release-policy <PATH>] [--validate-url <URL>] [--fixtures <PATH>] [--allow-tool-execution] [--access-audit <PLACEMENT>] [--access-audit-algorithms <LIST>] [--idempotency-key <KEY>] [--expected-serving-deployment <ID>] [--expected-app-id <ID>] [--require-catalog-pin] [--publication-id <SHA256>] [--require-reviewed-tools] [--mcp-task-migration-override] [--timeout <SECONDS>]`

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
| `--expected-serving-deployment <ID>` | reviewed serving deployment; empty requires no serving release |  |
| `--expected-app-id <ID>` | reviewed immutable app identity |  |
| `--require-catalog-pin` | require candidate catalog integrity support |  |
| `--publication-id <SHA256>` | native private publication requiring owner-approved platform tests |  |
| `--require-reviewed-tools` | require runtime tools to match configured tool_scopes before fixtures and promotion |  |
| `--mcp-task-migration-override` | allow this deployment to replace incompatible MCP task workers once |  |
| `--timeout <SECONDS>` | deployment and verification wait timeout in seconds (default 1200) |  |

Examples:

```sh
gregale mcp server deploy --path ./my-mcp --name my-mcp --size small
```

#### mcp server overview

Inspect replicas, Task workers, observers and promotion holds

`gregale mcp server overview --app <SLUG> [--worker-app <SLUG>] [--observer-app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--release-policy <PATH>] [--timeout <DURATION>]`

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
gregale mcp server overview --app my-mcp
gregale mcp server overview --app reports-web --worker-app reports-worker --observer-app reports-observer --json
gregale mcp server overview --app my-mcp --release-policy release.json
```

#### mcp server status

Qualify serving replicas and watch health or catalog changes

`gregale mcp server status --app <SLUG> [--endpoint <PATH>] [--token-env <ENV>] [--release-policy <PATH>] [--timeout <DURATION>] [--follow] [--interval <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | Gregale MCP app slug | required |
| `--endpoint <PATH>` | literal MCP endpoint path (default /mcp) |  |
| `--token-env <ENV>` | MCP client bearer token environment variable |  |
| `--release-policy <PATH>` | role-specific catalog baselines and token environments |  |
| `--timeout <DURATION>` | overall replica qualification deadline (default 10s) |  |
| `--follow` | poll until interrupted and report status changes (also --watch) |  |
| `--interval <DURATION>` | poll interval with --watch (default 30s) |  |

Examples:

```sh
gregale mcp server status --app my-mcp
gregale mcp server status --app my-mcp --token-env MCP_READER_TOKEN
gregale mcp server status --app my-mcp --release-policy release.json --json
gregale mcp server status --app my-mcp --follow --interval 15s --json
```

#### mcp server doctor

Check discovery, Origin rejection, compatibility and streaming

`gregale mcp server doctor [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [--stack] [--hosting] [--path <DIR>] [--worker-app <SLUG>] [--observer-app <SLUG>] [--release-policy <PATH>] [<app>]`

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
gregale mcp server doctor --app my-mcp --legacy --stream-tool stream_demo
gregale mcp server doctor --stack --app reports-web --worker-app reports-worker --path ./reports/web --json
```

The streaming check is optional.

#### mcp server watch

Watch caller-visible MCP catalog definitions for drift

`gregale mcp server watch [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] --baseline <PATH> [--interval <DURATION>] [--tools] [--resources] [--prompts]`

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
gregale mcp server watch --app my-mcp --baseline gregale-mcp.lock.json
```

#### mcp server lock

Capture MCP catalog definitions without invoking anything

`gregale mcp server lock [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--out <PATH>] [--force] [--timeout <DURATION>]`

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
gregale mcp server lock --app my-mcp --out baseline.json
```

Does not read resources, render prompts or invoke tools.

#### mcp server diff

Compare local MCP catalogs and report changes needing review

`gregale mcp server diff --before <PATH> --after <PATH> [--check] [--strict-catalog]`

| Flag | Meaning | |
|---|---|---|
| `--before <PATH>` | baseline MCP contract snapshot | required |
| `--after <PATH>` | candidate MCP contract snapshot | required |
| `--check` | fail on breaking changes or changes needing review |  |
| `--strict-catalog` | require review when a caller gains visibility of any catalog definition |  |

Examples:

```sh
gregale mcp server diff --before baseline.json --after candidate.json --check --json
```

### mcp client

tools · call · complete · prompts · render · resources · read · watch · status · wait · respond · cancel

#### mcp client tools

Discover tool schemas without invoking tools

`gregale mcp client tools [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<app>]`

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
gregale mcp client tools --app my-mcp
```

#### mcp client call

Execute one discovered tool; opt in to input requests or durable tasks

`gregale mcp client call [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [--interactive] [--input-responses-file <PATH>] [--tasks] [--task-submission-key <UUID>] [--task-receipt <FILE>] [--retry-submission] [--defer-form-input] [--wait] [<slug>]`

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
| `--task-submission-key <UUID>` | saved UUIDv7 for retrying a Task submission (requires --tasks) |  |
| `--task-receipt <FILE>` | save a private Task receipt before submission (requires --tasks) |  |
| `--retry-submission` | retry the original Task submission using an existing --task-receipt |  |
| `--defer-form-input` | allow a keyed Task to request forms for a later wait |  |
| `--wait` | wait for a task result; request cancellation on timeout |  |

Examples:

```sh
gregale mcp client call --app my-mcp --tool add --arguments '{"a":7,"b":5}'
gregale mcp client call --app my-mcp --tool report_preview --wait
gregale mcp client call --app my-mcp --tool report_preview --tasks
```

#### mcp client complete

Request bounded suggestions for a prompt or resource-template argument

`gregale mcp client complete [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--prompt <NAME>] [--resource-template <URI-TEMPLATE>] --argument <NAME> --value <TEXT> [--context <JSON>] [--context-file <PATH>] [--timeout <DURATION>]`

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
gregale mcp client complete --app my-mcp --prompt summarize --argument style --value exec
gregale mcp client complete --app my-mcp --resource-template 'customer://records/{recordId}' --argument recordId --value example-
```

#### mcp client prompts

Discover prompt definitions without rendering them

`gregale mcp client prompts [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<slug>]`

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
gregale mcp client prompts --app my-mcp
```

#### mcp client render

Render one explicitly selected prompt

`gregale mcp client render [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<slug>]`

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
gregale mcp client render --app my-mcp --prompt summarize --arguments '{"text":"weekly report"}'
```

#### mcp client resources

Discover resource and template definitions without reading contents

`gregale mcp client resources [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<app>]`

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
gregale mcp client resources --app my-mcp
```

#### mcp client read

Read one explicitly selected resource URI

`gregale mcp client read [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [<app>]`

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
gregale mcp client read --app my-mcp --uri 'file:///reports/current'
```

#### mcp client watch

Watch a resource for content updates or a Task receipt for status

`gregale mcp client watch [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--uri <URI>] [--task-receipt <FILE>] [--interval <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--uri <URI>` | resource URI to read and watch |  |
| `--task-receipt <FILE>` | private Task receipt for a metadata-only watch (repeatable) |  |
| `--interval <DURATION>` | poll and stream reconciliation interval (default 30s) |  |

Examples:

```sh
gregale mcp client watch --app my-mcp --uri 'file:///reports/current'
gregale mcp client watch --app my-mcp --token-env MCP_TOKEN --task-receipt ./task.json
```

#### mcp client status

Read the status or result of a previously returned task handle

`gregale mcp client status [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [--task-id <ID>] [--metadata-only] [--read-result] [--task-submission-key <UUID>] [--task-receipt <FILE>] [--receipt-dir <DIR>] [--write-input-template <FILE>] [<slug>]`

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
| `--metadata-only` | show Task metadata and next action |  |
| `--read-result` | read a completed Task result from a private receipt |  |
| `--task-submission-key <UUID>` | find a retained Task by saved UUIDv7 |  |
| `--task-receipt <FILE>` | find a retained Task from a private receipt (repeatable with --metadata-only) |  |
| `--receipt-dir <DIR>` | list private Task receipts in one directory (with --metadata-only) |  |
| `--write-input-template <FILE>` | write pending forms to a new private file |  |

Examples:

```sh
gregale mcp client status --app my-mcp --task-id 786512e2-9e0d-44bd-8f29-789f320fe840
```

#### mcp client wait

Resume waiting for a task to finish

`gregale mcp client wait [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [--task-id <ID>] [--task-receipt <FILE>] [--interactive] [--input-responses-file <PATH>] [--reviewed-input-file <FILE>] [--cancel-on-timeout] [<slug>]`

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
| `--task-receipt <FILE>` | resume a retained Task from a private receipt instead of supplying the Task ID |  |
| `--interactive` | answer modern MCP task input forms in the terminal |  |
| `--input-responses-file <PATH>` | JSON file with elicitation responses keyed by request ID |  |
| `--reviewed-input-file <FILE>` | answer forms from a reviewed private Task input template |  |
| `--cancel-on-timeout` | request cancellation when the resumed wait times out |  |

Examples:

```sh
gregale mcp client wait --app my-mcp --task-id 786512e2-9e0d-44bd-8f29-789f320fe840
gregale mcp client wait --app my-mcp --task-id 786512e2-9e0d-44bd-8f29-789f320fe840 --interactive
```

#### mcp client respond

Submit reviewed Task input and return a status snapshot

`gregale mcp client respond [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] --task-receipt <FILE> --reviewed-input-file <FILE> [--timeout <DURATION>] [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--url <URL>` | full MCP endpoint URL |  |
| `--app <SLUG>` | resolve a Gregale app&#39;s public endpoint |  |
| `--endpoint <PATH>` | endpoint path with --app (default /mcp) |  |
| `--token-env <ENV>` | environment variable containing an MCP client token |  |
| `--task-receipt <FILE>` | private receipt for the retained Task | required |
| `--reviewed-input-file <FILE>` | answer forms from a reviewed private Task input template | required |
| `--timeout <DURATION>` | total request timeout (default 30s) |  |

Examples:

```sh
gregale mcp client respond --app my-mcp --token-env MCP_TOKEN --task-receipt ./task.json --reviewed-input-file ./reviewed-input.json
```

#### mcp client cancel

Request cooperative cancellation of a previously returned task

`gregale mcp client cancel [--url <URL>] [--app <SLUG>] [--endpoint <PATH>] [--token-env <ENV>] [--legacy] [--tool <NAME>] [--uri <URI>] [--prompt <NAME>] [--stream-tool <NAME>] [--arguments <JSON>] [--arguments-file <PATH>] [--name <NAME>] [--timeout <DURATION>] [--task-id <ID>] [--task-receipt <FILE>] [<slug>]`

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
| `--task-receipt <FILE>` | cancel a retained Task from a private receipt instead of supplying the Task ID |  |

Examples:

```sh
gregale mcp client cancel --app my-mcp --task-id 786512e2-9e0d-44bd-8f29-789f320fe840
gregale mcp client cancel --app my-mcp --task-receipt ./task.json --token-env MCP_TOKEN
```

### mcp readiness

Check deployment public MCP readiness without executing tools

`gregale mcp readiness [notification|reminder|attention|pagerduty|fallback|maintenance|incidents|policy] --app <SLUG> --release <UUID> [--endpoint <PATH>] [--token-env <ENV>] [--baseline <PATH>] [--record] [--role <NAME>] [--legacy] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--release <UUID>` | deployment UUID (also --deployment) | required |
| `--endpoint <PATH>` | MCP path (default /mcp) |  |
| `--token-env <ENV>` | dedicated scoped probe token variable |  |
| `--baseline <PATH>` | expected role-visible MCP catalog lock |  |
| `--record` | run and retain a server-side policy-bound probe |  |
| `--role <NAME>` | configured probe role with --record (default default) |  |
| `--legacy` | use the legacy protocol |  |
| `--timeout <DURATION>` | overall deadline up to 30s |  |

Examples:

```sh
gregale mcp readiness --app my-mcp --release <uuid> --token-env MCP_PROBE_TOKEN --baseline gregale-mcp.lock.json --json
```

#### mcp readiness notification

Inspect readiness notification queues and recover blocked delivery

`gregale mcp readiness notification <list|retry|abandon>`

##### mcp readiness notification list

Show queue heads, age and delivery blockers

`gregale mcp readiness notification list --app <SLUG> [--env <SCOPE>] [--cursor <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--env <SCOPE>` | environment; includes all-environment rules (also --scope) |  |
| `--cursor <UUID>` | rule UUID cursor (also --after) |  |
| `--limit <N>` | page size 1–50 |  |

##### mcp readiness notification retry

Make a failed head eligible for the next evaluator pass

`gregale mcp readiness notification retry [--destination <primary|fallback>] --app <SLUG> --event <UUID> --expected-version <N> --request-id <UUID> --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--destination <primary|fallback>` | receiver queue; omit for the whole event |  |
| `--app <SLUG>` | owning app slug | required |
| `--event <UUID>` | head event | required |
| `--expected-version <N>` | current head delivery version | required |
| `--request-id <UUID>` | stable request ID; reuse on retries | required |
| `--reason <TEXT>` | operator reason | required |

##### mcp readiness notification abandon

Permanently stop a head notification and allow the queue to advance

`gregale mcp readiness notification abandon [--destination <primary|fallback>] --app <SLUG> --event <UUID> --expected-version <N> --request-id <UUID> --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--destination <primary|fallback>` | receiver queue; omit for the whole event |  |
| `--app <SLUG>` | owning app slug | required |
| `--event <UUID>` | head event | required |
| `--expected-version <N>` | current head delivery version | required |
| `--request-id <UUID>` | stable request ID; reuse on retries | required |
| `--reason <TEXT>` | operator reason | required |

#### mcp readiness reminder

Configure reminder defaults for future incidents

`gregale mcp readiness reminder <get|set|disable>`

##### mcp readiness reminder get

Read the rule reminder defaults

`gregale mcp readiness reminder get --app <SLUG> --rule <UUID>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |

##### mcp readiness reminder set

Set reminders inherited by new incidents

`gregale mcp readiness reminder set --app <SLUG> --rule <UUID> --expected-version <N> --interval-seconds <N> --max-reminders <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--expected-version <N>` | current reminder defaults version | required |
| `--interval-seconds <N>` | reminder interval, 60–86400 seconds | required |
| `--max-reminders <N>` | per-incident reminder budget, 1–10 | required |

##### mcp readiness reminder disable

Disable reminders for future incidents

`gregale mcp readiness reminder disable --app <SLUG> --rule <UUID> --expected-version <N>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--expected-version <N>` | current reminder defaults version | required |

#### mcp readiness attention

Acknowledge readiness incidents and configure bounded reminders

`gregale mcp readiness attention <reopen|list|history|ack|configure>`

##### mcp readiness attention reopen

Release acknowledgement and preserve its history

`gregale mcp readiness attention reopen --app <SLUG> --incident <UUID> --expected-version <N> --request-id <UUID> --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--incident <UUID>` | incident | required |
| `--expected-version <N>` | current attention version | required |
| `--request-id <UUID>` | stable decision UUID; reuse on retries | required |
| `--reason <TEXT>` | operator reason | required |

##### mcp readiness attention list

List open incidents and attention state

`gregale mcp readiness attention list --app <SLUG> [--env <SCOPE>] [--cursor <UUID>] [--limit <N>] [--include-resolved]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--env <SCOPE>` | environment; includes all-environment incidents (also --scope) |  |
| `--cursor <UUID>` | incident UUID cursor (also --after) |  |
| `--limit <N>` | page size 1–50 |  |
| `--include-resolved` | include resolved incident attention |  |

##### mcp readiness attention history

List retained acknowledgement and policy decisions

`gregale mcp readiness attention history --app <SLUG> --incident <UUID> [--cursor <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--incident <UUID>` | incident | required |
| `--cursor <CURSOR>` | attention history cursor (also --before) |  |
| `--limit <N>` | page size 1–50 |  |

##### mcp readiness attention ack

Record ownership without changing readiness

`gregale mcp readiness attention ack --app <SLUG> --incident <UUID> --expected-version <N> --request-id <UUID> --reason <TEXT> [--expires-at <TIME>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--incident <UUID>` | incident | required |
| `--expected-version <N>` | current attention version | required |
| `--request-id <UUID>` | stable decision UUID; reuse on retries | required |
| `--reason <TEXT>` | operator reason | required |
| `--expires-at <TIME>` | optional absolute RFC3339 expiry |  |

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

`gregale mcp readiness pagerduty acknowledgements events --app <SLUG> --rule <UUID> [--cursor <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--rule <UUID>` | readiness alert rule | required |
| `--cursor <CURSOR>` | opaque event cursor (also --before) |  |
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

#### mcp readiness maintenance

Silence MCP readiness notifications (alert rules still deliver)

`gregale mcp readiness maintenance <list|create|cancel>`

##### mcp readiness maintenance list

List maintenance windows including expired and cancelled history

`gregale mcp readiness maintenance list --app <SLUG> [--env <SCOPE>] [--cursor <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--env <SCOPE>` | environment (default default) (also --scope) |  |
| `--cursor <CURSOR>` | history cursor (also --before) |  |
| `--limit <N>` | page size 1–50 |  |

##### mcp readiness maintenance create

Silence MCP readiness notifications while still recording incidents

`gregale mcp readiness maintenance create --app <SLUG> [--env <SCOPE>] [--since <TIME>] [--until <TIME>] [--duration <DURATION>] --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--env <SCOPE>` | exact environment (default default) (also --scope) |  |
| `--since <TIME>` | RFC3339 start (default now) (also --start) |  |
| `--until <TIME>` | RFC3339 end; mutually exclusive with duration (also --end) |  |
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

`gregale mcp readiness incidents --app <SLUG> [--env <SCOPE>] [--cursor <CURSOR>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--env <SCOPE>` | environment (default default; empty selects all) (also --scope) |  |
| `--cursor <CURSOR>` | timeline cursor (also --before) |  |
| `--limit <N>` | page size 1–50 |  |

#### mcp readiness policy

Manage environment readiness policies

`gregale mcp readiness policy <history|diff|restore|get|set>`

##### mcp readiness policy history

List environment policy revisions

`gregale mcp readiness policy history --app <SLUG> [--env <SCOPE>] [--before-revision <UUID>] [--limit <N>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--env <SCOPE>` | environment (default default) (also --scope) |  |
| `--before-revision <UUID>` | history page cursor |  |
| `--limit <N>` | page size 1–50 (default 20) |  |

##### mcp readiness policy diff

Compare historical policy configuration

`gregale mcp readiness policy diff --app <SLUG> [--env <SCOPE>] --from-revision <UUID> [--to-revision <UUID>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--env <SCOPE>` | environment (default default) (also --scope) |  |
| `--from-revision <UUID>` | historical revision | required |
| `--to-revision <UUID>` | comparison revision (default current) |  |

##### mcp readiness policy restore

Restore as a new revision requiring fresh evidence

`gregale mcp readiness policy restore --app <SLUG> [--env <SCOPE>] --revision <UUID> --expected-revision <UUID> --reason <TEXT>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--env <SCOPE>` | environment (default default) (also --scope) |  |
| `--revision <UUID>` | historical revision | required |
| `--expected-revision <UUID>` | current policy revision CAS | required |
| `--reason <TEXT>` | audit reason | required |

##### mcp readiness policy get

Read an environment policy

`gregale mcp readiness policy get --app <SLUG> [--env <SCOPE>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--env <SCOPE>` | environment (default default) (also --scope) |  |

##### mcp readiness policy set

Save a reviewed environment policy with revision CAS

`gregale mcp readiness policy set --app <SLUG> [--env <SCOPE>] --file <PATH>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | owning app slug | required |
| `--env <SCOPE>` | environment (default default) (also --scope) |  |
| `--file <PATH>` | reviewed policy JSON | required |

### mcp observer

Validate observer credentials, namespace and metric readiness

#### mcp observer setup

Inspect the observer and optionally ensure minimum one replica

`gregale mcp observer setup --app <SLUG> --worker-app <SLUG> [--config <PATH>] [--apply]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | observer worker app slug | required |
| `--worker-app <SLUG>` | task worker app slug | required |
| `--config <PATH>` | local MCP source config (default gregale-mcp.json) |  |
| `--apply` | ensure at least one configured observer replica |  |

#### mcp observer status

Read observer configuration and heartbeat freshness

`gregale mcp observer status [--app <SLUG>] --worker-app <SLUG> [--config <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | observer slug (defaults to linked observer) |  |
| `--worker-app <SLUG>` | task worker app slug | required |
| `--config <PATH>` | optional local MCP source config |  |


## account

Manage your account, MFA and data exports

`gregale account [<subcommand>]`

### account mfa

Set up and manage multi-factor authentication

MFA actions open a signed-in browser form. Enter codes and passwords there. No CLI API key is needed; --json returns the URL and requires_browser. Legacy code, password and QR-output arguments are accepted for compatibility but are not submitted or exported.

#### account mfa enroll

Open authenticator setup in the browser

`gregale account mfa enroll [--qr-out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--qr-out <PATH>` | legacy option; the browser displays the QR code |  |

#### account mfa confirm

Open enrollment confirmation in the browser

`gregale account mfa confirm [--code <CODE>] [<6-digit-code>]`

| Flag | Meaning | |
|---|---|---|
| `--code <CODE>` | legacy option; enter the code in the browser |  |

#### account mfa verify

Open authenticator verification in the browser

`gregale account mfa verify [--code <CODE>] [<6-digit-code>]`

| Flag | Meaning | |
|---|---|---|
| `--code <CODE>` | legacy option; enter the code in the browser |  |

#### account mfa recover

Open recovery in the browser

`gregale account mfa recover [--code <CODE>] [<recovery-code>]`

| Flag | Meaning | |
|---|---|---|
| `--code <CODE>` | legacy option; enter the code in the browser |  |

#### account mfa disable

Open MFA removal in the browser

`gregale account mfa disable [--password <PASSWORD>] [--recovery-code <CODE>]`

| Flag | Meaning | |
|---|---|---|
| `--password <PASSWORD>` | legacy option; enter the password in the browser |  |
| `--recovery-code <CODE>` | legacy option; enter the recovery code in the browser |  |

#### account mfa rotate

Open new recovery codes in the browser

`gregale account mfa rotate`

Your current recovery codes stop working.

### account dpa

Print the Data Processing Agreement template

`gregale account dpa [get] [-o <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `-o <PATH>` | write the DPA template to a file |  |

Placeholders are filled when the agreement is signed.

#### account dpa get

Print the Data Processing Agreement template

`gregale account dpa get [-o <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `-o <PATH>` | write the DPA template to a file |  |

Placeholders are filled when the agreement is signed.

### account health

Show what may stop your account from working

Examples:

```sh
gregale account health get
gregale account health get --json
```

Lists restrictions, problems that keep recurring, failed deployments, custom-domain problems and refused API requests, like the dashboard&#39;s account health page.

#### account health get

Show what may stop your account from working

`gregale account health get`

Examples:

```sh
gregale account health get
gregale account health get --json
```

Lists restrictions, problems that keep recurring, failed deployments, custom-domain problems and refused API requests, like the dashboard&#39;s account health page.

### account export

Export account data (GDPR)

`gregale account export [-o <PATH>] [--no-secrets] [--force]`

| Flag | Meaning | |
|---|---|---|
| `-o <PATH>` | output file |  |
| `--no-secrets` | exclude the sealed-secret ciphertext slice |  |
| `--force` | replace the output file if it exists |  |

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

### account update

Set your display name

`gregale account update [--name <NAME>] [--clear-name]`

| Flag | Meaning | |
|---|---|---|
| `--name <NAME>` | display name shown to org members (at most 80 characters) |  |
| `--clear-name` | remove the display name |  |

### account sessions

List or sign out browser sessions

#### account sessions list

List signed-in browsers

`gregale account sessions list`

#### account sessions revoke

Sign out one browser session, or all of them

`gregale account sessions revoke [--all] [--yes] [SESSION_ID]`

| Flag | Meaning | |
|---|---|---|
| `--all` | sign out every browser |  |
| `--yes` | with --all, sign out without prompting |  |

API keys keep working.

### account email

Verify or change your sign-in email

#### account email verify

Send a new email verification link

`gregale account email verify`

#### account email update

Open the sign-in email change form in the browser

`gregale account email update`

A confirmation link goes to the new address.

### account password

Change your password

#### account password update

Open the password change form in the browser

`gregale account password update`

### account provider

Show how you can sign in

#### account provider list

List your password, two-factor and linked Google/GitHub sign-ins

`gregale account provider list`

### account activity

Show sign-ins and security changes

#### account activity list

List sign-ins, credential and security changes of the last 90 days

`gregale account activity list`

Shows the newest 100 events.


## org

Manage orgs, members and invitations

`gregale org [<subcommand>]`

### org agent

Show or set whether agents may act in the org

#### org agent policy

Show or set whether agents may act in the org

`gregale org agent policy <get|set>`

The policy also caps the capabilities agents may hold beyond inspection.

##### org agent policy get

Show the org&#39;s agent policy

`gregale org agent policy get --org <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |

##### org agent policy set

Allow or deny agents and cap their capabilities

`gregale org agent policy set --org <SLUG> --agents <allow|deny> [--capabilities <LIST>] [--approvals <LIST>] [--approval-channels <NAMES>] [--max-budget <USD>] [--clear-max-budget]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--agents <allow|deny>` | allow or deny agents in the org | required |
| `--capabilities <LIST>` | comma-separated capabilities agents may get: deploy:write, secrets:request, storage:manage, postgres:manage; empty for read-only |  |
| `--approvals <LIST>` | kinds of action every agent must have a person approve: delete, production, credentials, billing, all or none |  |
| `--approval-channels <NAMES>` | comma-separated names of your notification channels that also get the org&#39;s agents&#39; approval requests; empty for none |  |
| `--max-budget <USD>` | ceiling on every agent&#39;s monthly budget for always-on capacity, in US dollars |  |
| `--clear-max-budget` | remove the ceiling on agents&#39; budgets |  |

Examples:

```sh
gregale org agent policy set --org acme --agents allow --capabilities deploy:write,secrets:request
gregale org agent policy set --org acme --agents deny
```

Owners and admins only. Without --capabilities, an allowed policy keeps its current capabilities.

Revokes, at once, the agent connections the new policy no longer allows.

### org support

See and revoke support access to the org&#39;s apps; reject requests

Support access lets Gregale support deploy the production branch, retry, cancel, roll back and restart the org&#39;s apps for a limited time. It never reaches secrets, configuration, members, keys or billing.

Granting and approving need a signed-in browser: use the dashboard&#39;s Support access page.

#### org support list

List support grants and open requests

`gregale org support list --org <SLUG> [--json]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--json` | Print JSON |  |

Examples:

```sh
gregale org support list --org acme
```

#### org support revoke

End a support grant now

`gregale org support revoke --org <SLUG> GRANT_ID`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |

Examples:

```sh
gregale org support revoke --org acme 3f2a…
```

Owners and admins only. An API key may revoke: taking access away is always safe.

#### org support reject

Reject a support access request

`gregale org support reject --org <SLUG> REQUEST_ID`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |

Owners and admins only.

#### org support approve

Print the dashboard page that grants or approves support access

`gregale org support approve --org <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |

### org invitation

Manage org invitations

#### org invitation accept

Accept an invitation

`gregale org invitation accept <token>`

#### org invitation peek

Look up an invitation by token

`gregale org invitation peek <token>`

#### org invitation list

List pending invitations

`gregale org invitation list --org <SLUG> [--limit <N>] [--all]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--limit <N>` | max rows (1..200) |  |
| `--all` | list invitations in every state |  |

#### org invitation revoke

Revoke a pending invitation

`gregale org invitation revoke --org <SLUG> --invitation <ID>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--invitation <ID>` | invitation ID | required |

### org key

Manage org API keys

#### org key list

List org API keys

`gregale org key list --org <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |

#### org key create

Create an org API key; the plaintext is shown once

`gregale org key create --org <SLUG> [--label <TEXT>] [--scopes <SCOPES>] [--projects <SLUG,...>] [--approvals <LIST>] <label>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--label <TEXT>` | key label (same as the positional label; kept for scripts) |  |
| `--scopes <SCOPES>` | comma-separated scopes (default admin) |  |
| `--projects <SLUG,...>` | limit an agent key to these projects (comma-separated slugs) |  |
| `--approvals <LIST>` | kinds of action an agent key must have a person approve: delete, production, credentials, billing, all or none (default all) |  |

#### org key get

Show one org API key

`gregale org key get --org <SLUG> <key-id>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |

#### org key delete

Revoke an org API key

`gregale org key delete --org <SLUG> <key-id>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |

#### org key rotate

Rotate an org API key; the new plaintext is shown once

`gregale org key rotate --org <SLUG> [--label <TEXT>] <key-id>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--label <TEXT>` | new label (empty keeps the current one) |  |

### org members

Manage org members

#### org members update

Change a member&#39;s role

`gregale org members update --org <SLUG> --user <USER-ID> --role <ROLE>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--user <USER-ID>` | member user ID | required |
| `--role <ROLE>` | new role (owner is rejected) | required; one of `admin` · `developer` · `viewer` · `billing` |

#### org members list

List org members

`gregale org members list <slug>`

#### org members invite

Invite a member by email

`gregale org members invite --org <SLUG> --email <ADDR> [--role <ROLE>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--email <ADDR>` | invitee email | required |
| `--role <ROLE>` | role (default developer; owner is rejected) | one of `admin` · `developer` · `viewer` · `billing` |

#### org members delete

Remove a member

`gregale org members delete --org <SLUG> --user <USER-ID>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--user <USER-ID>` | member user ID | required |

### org owner

Transfer org ownership

#### org owner set

Transfer org ownership

`gregale org owner set --org <SLUG> --to <USER-ID>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--to <USER-ID>` | user ID of the new owner | required |

### org usage

Show seat usage

`gregale org usage --org <SLUG>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |

### org list

List orgs

### org create

Create an org

`gregale org create [--slug <SLUG>] --name <TEXT> [<slug>]`

| Flag | Meaning | |
|---|---|---|
| `--slug <SLUG>` | org slug, or the first argument (lowercase alphanumeric + dashes) |  |
| `--name <TEXT>` | display name | required |

### org get

Show one org

`gregale org get <slug>`

### org activity

Show the global infrastructure timeline

`gregale org activity --org <SLUG> [--before <CURSOR>] [--cursor <CURSOR>] [--all] [--kind-prefix <PREFIX>] [--actor-type <TYPE>] [--app-id <UUID>] [--limit <N>]`

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

### org delete

Delete one org

`gregale org delete [--yes] <slug>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | skip the confirmation prompt (aliases: -q, --quiet) |  |

### org leave

Leave an org you are a member of

`gregale org leave [--yes] ORG_SLUG`

| Flag | Meaning | |
|---|---|---|
| `--yes` | leave without prompting |  |

Owners transfer ownership first. Personal orgs cannot be left.

### org me

Show current org membership

### org update

Update org metadata

`gregale org update --org <SLUG> [--name <TEXT>] [--plan <PLAN>]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | organization slug | required |
| `--name <TEXT>` | new display name (1..120 chars) |  |
| `--plan <PLAN>` | new plan | one of `free` · `hobby` · `pro` · `scale` |


## key

Manage your account&#39;s API keys

`gregale key [<subcommand>]`

### key grace-window

Read or update the rotation grace window

`gregale key grace-window [set] [--reset] [--days <N>]`

| Flag | Meaning | |
|---|---|---|
| `--reset` | clear the account override and use the plan default |  |
| `--days <N>` | new grace window in days (0 to 30) |  |

#### key grace-window set

Read or update the rotation grace window

`gregale key grace-window set [--reset] [--days <N>]`

| Flag | Meaning | |
|---|---|---|
| `--reset` | clear the account override and use the plan default |  |
| `--days <N>` | new grace window in days (0 to 30) |  |

### key list

List API keys

### key create

Mint a new API key

`gregale key create [--agent] [--scopes <SCOPE,...>] [--projects <SLUG,...>] [--approvals <LIST>] <label>`

| Flag | Meaning | |
|---|---|---|
| `--agent` | a key for a coding agent: adds the agent scope so risky requests ask a person and always-on cost is held to an agent budget (default scopes agent,apps:read,deploy:write) |  |
| `--scopes <SCOPE,...>` | comma-separated API key scopes; omit for full admin access |  |
| `--projects <SLUG,...>` | limit an agent key to these projects (comma-separated slugs) |  |
| `--approvals <LIST>` | kinds of action an agent key must have a person approve: delete, production, credentials, billing, all or none (default all) |  |

Examples:

```sh
gregale key create coding-agent --agent
gregale key create agent-runner --scopes runs:write
gregale key create ci-agent --scopes agent,deploy:write --projects api,web
gregale key create sandbox-agent --scopes agent,deploy:write --approvals none
```

### key delete

Revoke an API key

`gregale key delete <id>`

### key rotate

Rotate an API key

`gregale key rotate [--revoke-old] [--grace-days <N>] <key-id>`

| Flag | Meaning | |
|---|---|---|
| `--revoke-old` | revoke the old key now instead of after the grace window (use when the key leaked) |  |
| `--grace-days <N>` | days the old key keeps working, for this rotation only (0 to 30) |  |

Examples:

```sh
gregale key rotate 7f8c2a1e-6d3b-4c55-9a7e-0b1d2c3e4f5a
gregale key rotate 7f8c2a1e-6d3b-4c55-9a7e-0b1d2c3e4f5a --revoke-old
```


## billing

Manage plan, payment, usage, invoices and spend cap

`gregale billing [<subcommand>]`

Examples:

```sh
gregale billing export --month 2026-09 --out invoices.zip
gregale billing export --month 2026-09 --format csv --out invoices.csv
```

### billing plan

Change your subscription plan

`gregale billing plan [--yes] <plan>`

| Flag | Meaning | |
|---|---|---|
| `--yes` | confirm a plan downgrade without prompting |  |

Examples:

```sh
gregale billing plan pro
```

Plans: free, hobby, pro and scale. Paid upgrades open the billing provider&#39;s checkout.

### billing usage

Show this month&#39;s usage, or daily, storage and summary views

`gregale billing usage [daily|storage|summary] [--month <YYYY-MM>]`

| Flag | Meaning | |
|---|---|---|
| `--month <YYYY-MM>` | month (YYYY-MM); default: current month |  |

#### billing usage daily

Per-day breakdown

`gregale billing usage daily [--day <YYYY-MM-DD>]`

| Flag | Meaning | |
|---|---|---|
| `--day <YYYY-MM-DD>` | day (YYYY-MM-DD); default: today UTC |  |

#### billing usage storage

Per-app storage bytes

`gregale billing usage storage [--day <YYYY-MM-DD>]`

| Flag | Meaning | |
|---|---|---|
| `--day <YYYY-MM-DD>` | day (YYYY-MM-DD); default: today UTC |  |

#### billing usage summary

Account roll-up

`gregale billing usage summary [--month <YYYY-MM>]`

| Flag | Meaning | |
|---|---|---|
| `--month <YYYY-MM>` | month (YYYY-MM); default: current month |  |

### billing invoices

List issued invoices

### billing spend-cap

Set the monthly spend cap for usage beyond your plan

`gregale billing spend-cap [--clear] <cents>`

| Flag | Meaning | |
|---|---|---|
| `--clear` | remove the cap (Free accounts only; a paid plan keeps one) |  |

A paid plan always keeps a cap, at most Hobby $75, Pro $200 or Scale $1,000; ask support for a higher one.

### billing egress-overage

Show or set billing for egress above the monthly allowance

`gregale billing egress-overage [get|set] [--org <SLUG>] [on|off]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | Use the election of an organization that owns its resources |  |

Examples:

```sh
gregale billing egress-overage
gregale billing egress-overage on
gregale billing egress-overage off --org acme
```

#### billing egress-overage get

Show or set billing for egress above the monthly allowance

`gregale billing egress-overage get [--org <SLUG>] [on|off]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | Use the election of an organization that owns its resources |  |

Examples:

```sh
gregale billing egress-overage get
gregale billing egress-overage get on
gregale billing egress-overage get off --org acme
```

#### billing egress-overage set

Show or set billing for egress above the monthly allowance

`gregale billing egress-overage set [--org <SLUG>] [on|off]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | Use the election of an organization that owns its resources |  |

Examples:

```sh
gregale billing egress-overage set
gregale billing egress-overage set on
gregale billing egress-overage set off --org acme
```

### billing payment-method

Show the card on file

`gregale billing payment-method [get] [--print] [--no-open]`

| Flag | Meaning | |
|---|---|---|
| `--print` | print the card summary and portal URL without opening a browser |  |
| `--no-open` | alias of --print |  |

#### billing payment-method get

Show the card on file

`gregale billing payment-method get [--print] [--no-open]`

| Flag | Meaning | |
|---|---|---|
| `--print` | print the card summary and portal URL without opening a browser |  |
| `--no-open` | alias of --print |  |

### billing portal

Open the active billing provider&#39;s portal

`gregale billing portal [open] [--print] [--no-open]`

| Flag | Meaning | |
|---|---|---|
| `--print` | print the portal URL without opening a browser |  |
| `--no-open` | alias of --print |  |

#### billing portal open

Open the active billing provider&#39;s portal

`gregale billing portal open [--print] [--no-open]`

| Flag | Meaning | |
|---|---|---|
| `--print` | print the portal URL without opening a browser |  |
| `--no-open` | alias of --print |  |

### billing retry

Retry failed payment when supported; Polar uses the portal

### billing cancel

Cancel the subscription at period end

`gregale billing cancel [--yes]`

| Flag | Meaning | |
|---|---|---|
| `--yes` | confirm the cancellation without prompting; required with --json or without a terminal |  |

### billing status

Show subscription status

### billing costs

Explain retained usage costs and source coverage

`gregale billing costs [--month <YYYY-MM>] [--org <SLUG>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--month <YYYY-MM>` | UTC usage month (defaults to current) |  |
| `--org <SLUG>` | Read the costs of an organization that owns its resources |  |
| `--json` | Print the machine-readable cost report |  |

Examples:

```sh
gregale billing costs --month 2026-10 --json
gregale billing costs --org acme
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

### billing budgets

Manage monitored alerts, preview stopping and budget drafts

#### billing budgets list

List account budget policies and monitoring status

`gregale billing budgets list [--org <SLUG>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | Use the budgets of an organization that owns its resources |  |
| `--json` | Print JSON |  |

Examples:

```sh
gregale billing budgets list --org acme
```

#### billing budgets get

Read a budget, including a deletion tombstone

`gregale billing budgets get [--org <SLUG>] [--json] ID`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | Use the budgets of an organization that owns its resources |  |
| `--json` | Print JSON |  |

#### billing budgets create

Save a draft or activate monitored alerts or preview stopping

`gregale billing budgets create [--file <PATH>] [--org <SLUG>] [--key <KEY>] [--json]`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | Budget spec JSON file (required) |  |
| `--org <SLUG>` | Use the budgets of an organization that owns its resources |  |
| `--key <KEY>` | Stable operation key for retries |  |
| `--json` | Print JSON |  |

Examples:

```sh
gregale billing budgets create --file budget.json --key previews-october --json
```

#### billing budgets update

Replace a budget policy at its expected revision

`gregale billing budgets update [--file <PATH>] [--expected-revision <N>] [--org <SLUG>] [--key <KEY>] [--json] ID`

| Flag | Meaning | |
|---|---|---|
| `--file <PATH>` | Budget spec JSON file (required) |  |
| `--expected-revision <N>` | Current revision (required) |  |
| `--org <SLUG>` | Use the budgets of an organization that owns its resources |  |
| `--key <KEY>` | Stable operation key for retries |  |
| `--json` | Print JSON |  |

#### billing budgets delete

Tombstone a policy and retain its audit

`gregale billing budgets delete [--expected-revision <N>] [--org <SLUG>] [--key <KEY>] [--json] ID`

| Flag | Meaning | |
|---|---|---|
| `--expected-revision <N>` | Current revision (required) |  |
| `--org <SLUG>` | Use the budgets of an organization that owns its resources |  |
| `--key <KEY>` | Stable operation key for retries |  |
| `--json` | Print JSON |  |

#### billing budgets history

Page through immutable policy revisions

`gregale billing budgets history [--after-revision <N>] [--limit <N>] [--org <SLUG>] [--json] ID`

| Flag | Meaning | |
|---|---|---|
| `--after-revision <N>` | Continue after this revision |  |
| `--limit <N>` | Page size (1..100) |  |
| `--org <SLUG>` | Use the budgets of an organization that owns its resources |  |
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

### billing export

Export a partial FOCUS 1.4 invoice projection

`gregale billing export [--month <YYYY-MM>] [--format <FORMAT>] [--out <PATH>]`

| Flag | Meaning | |
|---|---|---|
| `--month <YYYY-MM>` | invoice period-end month (required) |  |
| `--format <FORMAT>` | export encoding (default zip with CSV and metadata) | one of `zip` · `csv` · `metadata` |
| `--out <PATH>` | new output file (required for zip); - writes stdout |  |


## feature

See Gregale features and join early access

`gregale feature [<subcommand>]`

### feature list

Show feature maturity, plan availability and early access

`gregale feature list [--early-access] [--org <SLUG>]`

| Flag | Meaning | |
|---|---|---|
| `--early-access` | list only early-access features you can join or have joined |  |
| `--org <SLUG>` | with --early-access, list the organization&#39;s early access |  |

Examples:

```sh
gregale feature list
gregale feature list --early-access
gregale feature list --early-access --org acme
```

Signed in, the list ends with early-access features you can join or have joined; join one with `gregale feature join &lt;key&gt;`.

### feature join

Join a preview

`gregale feature join [--org <SLUG>] <key>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | act for this organization (owner or admin to join or leave) |  |

Examples:

```sh
gregale feature join change_timeline
gregale feature join change_timeline --org acme
```

### feature leave

Leave a preview

`gregale feature leave [--org <SLUG>] <key>`

| Flag | Meaning | |
|---|---|---|
| `--org <SLUG>` | act for this organization (owner or admin to join or leave) |  |


## config

Manage local CLI settings, profiles and presets

`gregale config [<subcommand>]`
Examples:

```sh
gregale config
gregale config list
gregale config --help
```

### config profile

Manage named connections to Gregale and their credentials

Examples:

```sh
gregale config profile
gregale config profile list
gregale config profile --help
```

#### config profile create

Add a connection without changing the active profile

`gregale config profile create <name> <api-url>`

#### config profile list

List connections and the active profile

`gregale config profile list`

#### config profile check

Verify the selected API connection and account identity

`gregale config profile check [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--timeout <DURATION>` | maximum request duration (default 10s) |  |

Examples:

```sh
gregale config profile check
gregale --profile staging profile check --timeout 5s --json
```

#### config profile use

Select the default connection or choose and check one interactively

`gregale config profile use [--interactive] [--timeout <DURATION>] [<name>]`

| Flag | Meaning | |
|---|---|---|
| `--interactive` | choose a saved connection, check its identity, and confirm switching |  |
| `--timeout <DURATION>` | interactive connection check deadline (default 10s) |  |

Examples:

```sh
gregale config profile use staging
gregale config profile use --interactive
```

#### config profile delete

Remove an inactive connection and its credentials

`gregale config profile delete <name>`

### config preset

List or manage local target presets

Examples:

```sh
gregale config preset
gregale config preset list
gregale config preset --help
```

#### config preset create

Save current linked targets as a named local preset

`gregale config preset create [--replace] <name>`

| Flag | Meaning | |
|---|---|---|
| `--replace` | explicitly replace an existing preset |  |

#### config preset apply

Validate and apply a preset for the currently linked project

`gregale config preset apply [--dry-run] <name>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | validate and preview without saving linked defaults |  |

#### config preset copy

Copy a local preset without changing the active context

`gregale config preset copy <source-name> <new-name>`

Examples:

```sh
gregale config preset copy staging staging-eu
```

#### config preset rename

Rename a local preset while preserving its targets and active context

`gregale config preset rename <old-name> <new-name>`

Examples:

```sh
gregale config preset rename staging staging-eu
```

#### config preset update

Change selected preset targets without changing active context

`gregale config preset update [--app <SLUG>] [--env <SLUG>] [--clear-app] [--clear-environment] [--dry-run] <name>`

| Flag | Meaning | |
|---|---|---|
| `--app <SLUG>` | validate and replace only the preset app |  |
| `--env <SLUG>` | validate and replace only the preset environment (also --environment) |  |
| `--clear-app` | remove only the preset app |  |
| `--clear-environment` | remove only the preset environment |  |
| `--dry-run` | validate and preview without saving the preset |  |

#### config preset list

List preset targets and their source file

`gregale config preset list`

#### config preset get

Show one saved target preset

`gregale config preset get <name>`

#### config preset delete

Remove a local preset without changing linked targets

`gregale config preset delete [--dry-run] <name>`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | preview targets and source path without deleting |  |

Examples:

```sh
gregale config preset delete staging --dry-run
```

### config upload-cache

Inspect or clear saved state for resumable source uploads

#### config upload-cache prune

Remove stale and excess state safely

`gregale config upload-cache prune [--older-than <D>] [--max-entries <N>] [--dry-run]`

| Flag | Meaning | |
|---|---|---|
| `--older-than <D>` | maximum recovery-state age |  |
| `--max-entries <N>` | maximum recovery records to retain |  |
| `--dry-run` | show actions without deleting files |  |

#### config upload-cache list

List resumable, stale, and orphaned cache entries

`gregale config upload-cache list`

### config context

get · set

#### config context get

Show linked targets, configuration sources, and missing defaults

`gregale config context get [--local-config] [--problems-only] [--check] [--verify] [--timeout <DURATION>]`

| Flag | Meaning | |
|---|---|---|
| `--local-config` | offline project-root summary of preferences, required names, presets, and recovery records; invalid files fail |  |
| `--problems-only` | show only invalid local files, broken references, and recovery issues; requires --local-config |  |
| `--check` | fail when a linked project or default app is missing; does not verify remote resources |  |
| `--verify` | read authenticated account and verify configured linked project/app/environment |  |
| `--timeout <DURATION>` | overall verification deadline (default 10s; requires --verify) |  |

Examples:

```sh
gregale config context get
gregale config context get --check --json
gregale config context get --local-config
```

#### config context set

Update selected linked defaults while preserving omitted settings

`gregale config context set [--dry-run] [--app <SLUG>] [--env <SLUG>] [--clear-app] [--clear-environment]`

| Flag | Meaning | |
|---|---|---|
| `--dry-run` | validate and display proposed defaults without saving |  |
| `--app <SLUG>` | validate and replace the default app in the linked project |  |
| `--env <SLUG>` | validate and replace the default project environment (also --environment) |  |
| `--clear-app` | remove only the default app locally |  |
| `--clear-environment` | remove only the default environment locally |  |

Examples:

```sh
gregale config context set --env staging
gregale config context set --app payments-api
gregale config context set --clear-environment
```

### config get

Show one effective setting

`gregale config get <api-base|json>`

### config set

Persist one non-secret setting

`gregale config set <api-base|json> <value>`

### config list

Show all effective settings

### config explain

Show where a CLI target or project setting comes from

`gregale config explain <key>`

Examples:

```sh
gregale config explain app
gregale config explain services.api.visibility
```

Keys: project, app, environment, service_policy, services.&lt;name&gt;.service_policy, services.&lt;name&gt;.visibility, services.&lt;name&gt;.depends_on, api-base, json.

Lists every layer in precedence order (flag, .gregale/project.json, gregale.yaml `project:`, Compose x-gregale-*, .gregale/project.yaml) and marks the one that wins.

Runs locally; nothing is uploaded.

