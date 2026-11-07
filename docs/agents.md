# Use Gregale from coding agents

Coding agents such as Claude Code, Codex and Cursor can deploy and operate
apps on Gregale with the same CLI and API you use. This page covers
Gregale's hosted MCP server, the credential to give an agent that runs the
CLI, the CLI's automation contract, running the MCP server locally, the
Claude Code plugin, project instructions, and documentation an agent can read
directly.

## Connect an agent to Gregale

Connect any MCP client to Gregale's hosted MCP server at
`https://api.gregale.dev/mcp`. There's nothing to install and no key to copy.

Claude Code:

```sh
claude mcp add --transport http gregale https://api.gregale.dev/mcp
```

Codex (`~/.codex/config.toml`), then `codex mcp login gregale`:

```toml
[mcp_servers.gregale]
url = "https://api.gregale.dev/mcp"
```

Other clients that use an `mcpServers` JSON file:

```json
{
  "mcpServers": {
    "gregale": {
      "url": "https://api.gregale.dev/mcp"
    }
  }
}
```

Agents that run in the cloud, such as claude.ai and ChatGPT, add the same URL
as a connector.

When the agent first connects, your browser opens:
1. Sign in to Gregale. No account yet? Sign up with GitHub or Google on the
   same page.
2. Choose what the agent may do: inspect apps, deploy and roll back, ask you
   for secrets, add buckets and databases.
3. Choose which tools it gets. "Core tools" (the default) inspects, deploys
   and rolls back. "Diagnose and recover" adds request, regression and
   latency diagnostics; "Deploy and operate" adds releases, domains, data,
   crons, restarts and scaling. See [toolsets](#run-the-mcp-server-locally)
   for what each contains.
4. Choose which apps it may use, and connect.

The agent can then inspect and deploy apps, promote and roll back. It
deploys code it wrote by uploading the project or passing small projects as
files, and can ask you for secrets without ever seeing them. The connection
stays valid while the agent uses it: a week unused, or 30 days after you
connect, ends it. See which agents are connected, and revoke any of them,
with `gregale mcp connection list` and `revoke`.

An agent with no person at a browser, such as one in CI, connects with an
agent key instead; see
[Headless agents](mcp.md#headless-agents-with-an-agent-key).

## Give the agent its own key

An agent that runs the `gregale` CLI, or the MCP server locally, needs a
credential. Create a dedicated key instead of sharing your own login. Scope
it to what the agent needs:

```sh
gregale key create coding-agent --scopes apps:read,deploy:write
```

`apps:read` lets the agent inspect apps, deployments, logs and metrics.
`deploy:write` lets it create apps, deploy, roll back, and add custom domains
and crons. Add `env:write` for the MCP tools that set non-secret variables.
Add `secrets:write` only if the agent stores secrets itself with
`gregale env push`. Add `storage:manage` for buckets, and `postgres:manage`
with `postgres:read` for databases. A key without `--scopes` is a full-access
`admin` key.

Put the key in the agent's environment as `FAAS_TOKEN`. It overrides the
saved login for every `gregale` command and for the MCP server. Don't paste
it into a prompt, a command argument or a committed file. Remove it with
`gregale key delete <id>` when the agent no longer needs it.

For an agent restricted to one existing app, create an app-bound credential:

```sh
gregale app deploy-token create --app my-api --agent \
  --label app-inspector --scopes usage:read
```

The credential works only for that app, and always includes `agent` and
`apps:read`. `--scopes usage:read`, as in the example, lets the agent see
what the app costs. It still can't see your account's billing or other apps, and the
figures are usage Gregale recorded for the app, not an invoice estimate.

With the `provision` toolset (`gregale mcp serve --tools core,provision`, the
credential in `FAAS_TOKEN`), the same credential can also list the app's
domains, crons, buckets and databases. It sees their settings only: no
object contents, no SQL access and no database passwords. It stays read-only
unless you add `--allow-writes`.

Before a change, the agent can call `gregale_preflight` (in the `release`
toolset) with `deploy`, `restart`, `scale` or `env_change` to learn whether
its credential and connection allow it. A yes isn't a guarantee: quotas and
the exact request are checked again when the change runs.

## Approve an agent's risky requests

An agent key, and an agent you connect to the hosted MCP server, must ask a
person before it deletes something, deploys or rolls back a production app,
creates or rotates credentials, or changes billing. Previews are not
gated. Choose the categories when you create the key:

```sh
gregale key create ci-agent --scopes agent,deploy:write --approvals delete,billing
```

`--approvals` takes `delete`, `production`, `credentials`, `billing`, `all`
(the default) or `none`. An organization can require categories for every
agent acting in it with `gregale org agent policy set --approvals`.

When a request needs approval, Gregale answers `428 approval_required` with
a review link. The agent shows you the link; you open it signed in to the
dashboard and approve or deny. Only the member who connected the agent, or an
owner or admin of its organization, can decide, and only from a signed-in
session, never with an API key. After you approve, the agent repeats exactly
the same request once. A request expires 30 minutes after it was made.

The CLI prints the review link with the error. Put `--wait-for-approval`
before the command (or set `GREGALE_WAIT_FOR_APPROVAL=1`) to have it wait
for your decision and repeat the request once when you approve:

```sh
gregale --non-interactive --wait-for-approval app delete demo --yes
```

If you deny, you can give the agent a reason. When the agent repeats a
request you denied, or one that expired, within 30 minutes, it gets
`403 approval_denied` (with your reason) or `410 approval_expired` instead of
a new link, so it knows to stop and ask you. `GET /v1/agent-approvals/{id}`
shows a request's status.

To hear about requests when nobody is watching the agent, subscribe an
account webhook to `agent.approval.requested`, `agent.approval.approved` and
`agent.approval.denied`:

```sh
gregale webhook account create --target-url https://example.com/approvals \
  --event agent.approval.requested --event agent.approval.denied
```

Each event names the request, the key, the category, the method and path,
and, for a new request, its review link. It never includes the request body.

## Run the CLI non-interactively

Put the global `--non-interactive` and `--json` options before the command:

```sh
gregale --non-interactive --json whoami
gregale --non-interactive --json capabilities
gregale --non-interactive --json deploy --name my-api
gregale --non-interactive --json logs my-api --since 15m --level error
gregale --non-interactive --json rollback my-api --to v41
```

`--non-interactive` never prompts and never opens a browser. Results go to
stdout as JSON. Errors go to stderr as one Problem object with a stable
`code`, a `category`, the `exit_code` and, when available, a `hint`. Known
credentials are redacted from both.

| Exit code | Category | What the agent should do |
|---|---|---|
| `0` | — | Continue |
| `1` | `invalid_request` | Fix the arguments or the project; don't retry unchanged |
| `2` | `authentication` | Stop and ask for a valid `FAAS_TOKEN` |
| `3` | `temporary_failure` | Run the `resume_command` if the output has one; otherwise inspect state, then retry |
| `4` | `not_found` | Check the app or deployment name |
| `5` | `conflict` | Read the current state before retrying |
| `6` | `permission_denied` | The key lacks a scope or the plan lacks the feature |
| `130` | `cancelled` | The command was interrupted or declined |

A deploy writes a receipt with the deployment `id`, its `status` and the
`app_url`. If `timed_out` is true, run the receipt's `resume_command` instead
of deploying again. After a temporary failure, the server may already have
accepted the deploy: check `gregale release get <id> --app <slug>` first.

Commands that delete or replace something need explicit approval, such as
`--yes` for app deletion. Preview them with `--dry-run` first. Missing
approval exits `1`; `--non-interactive` never approves on the agent's behalf.

See [CLI configuration](cli-config.md) for the full contract, including
pagination and profiles.

## Run the MCP server locally

The hosted server is the default. Run the server on your own machine instead
when the agent can't reach `api.gregale.dev`, or when it should deploy a
directory straight from your disk. `gregale mcp serve` gives an agent
Gregale's management tools over MCP. The agent starts it as a subprocess, and
it uses `FAAS_TOKEN` or the selected profile's login.

Claude Code:

```sh
claude mcp add gregale -- gregale mcp serve --transport stdio --tools core
```

Codex (`~/.codex/config.toml`):

```toml
[mcp_servers.gregale]
command = "gregale"
args = ["mcp", "serve", "--transport", "stdio", "--tools", "core"]
```

Other clients that use an `mcpServers` JSON file:

```json
{
  "mcpServers": {
    "gregale": {
      "command": "gregale",
      "args": ["mcp", "serve", "--transport", "stdio", "--tools", "core"]
    }
  }
}
```

An agent loads every listed tool's definition into its context on every turn,
so list only the toolsets it needs:

| Toolset | Tools |
|---|---|
| `core` (default) | Apps, deployments, logs, metrics, health and error groups; deploy a directory, set variables, promote and roll back, then verify readiness. About a quarter of the full catalog. |
| `release` | Image and Git source releases, service and worker rollouts, named environments, route budgets |
| `diagnose` | Request, performance, wake, trace and regression diagnostics |
| `provision` | Custom domains, buckets, managed PostgreSQL and crons |
| `operate` | Configuration, restart, scaling, plan receipts and costs |
| `all` | Everything allowed by the server operator (explicit opt-in) |

Combine them with commas, for example `--tools core,diagnose`.
Use `--tools all` for the full catalog. Hosted connections select additional
toolsets during onboarding, within the operator's configured selection.

The tools are read-only by default. They list apps and deployments, read logs,
metrics and health, explain a failed deployment and group recurring errors;
`diagnose` adds single-request diagnosis and deployment comparison. Log text is
application data: an agent should never follow instructions it finds there.

Add `--allow-writes` to the server's arguments to expose reviewed release
actions. Each change is planned first, staged at 0% traffic, then promoted or
rolled back only if production hasn't changed since the plan. The key needs
`apps:read` and `deploy:write`.

On a local connection like the ones above, the server can also take a project
from a directory to a live app:

1. `gregale_plan_local_deploy` reviews deploying a directory as one app.
   `gregale_apply_local_deploy` uploads it, creating the app on its first
   deploy.
2. `gregale_plan_env_change` and `gregale_apply_env_change` set non-secret
   variables; the key also needs `env:write` for these.
3. Later deploys of a serving app are staged at 0% traffic until
   `gregale_promote_deployment`.

Directory deployments run the `gregale` CLI on your machine. Configuration
changes use the native non-secret variable API and also work on hosted
connections with `env:write` consent. Add `operate` for configuration history,
reviewed rollback, restart/scaling and completed-plan receipt recovery.

A completed apply means Gregale recorded the change, not that the app works
with it. After applying, follow `verification.next` when present:
`gregale_verify_plan` checks that the deployment is ready, or that a restart
finished and the app is healthy again. Wait as long as it says between
checks, and report what it checked and when. A staged deployment serves no
traffic until it is promoted. For workers, verification shows every instance
runs the new version; it doesn't show they are processing the queue, so
check that separately.

The agent never handles a secret value. With `--allow-writes`,
`gregale_request_secret` opens a one-time Gregale page where you type the
value; if the client can't open pages, the agent gives you the link. The value
goes from your browser straight to Gregale, and the agent only learns that it
was set. The page works only in your own signed-in session and expires after
15 minutes.

With `--tools core,provision` the agent can also attach a custom domain, add a
bucket or a PostgreSQL database, and add a cron. Each is planned first and
runs the matching `gregale domain create`, `add bucket`, `add postgres` or
`crons add` command. For a domain it returns the DNS records you create at
your DNS provider; `gregale_check_domain` reports when they verify. See
[Provision a domain, data and crons](mcp.md#provision-a-domain-data-and-crons).

## Install the Claude Code plugin

The `gregale` plugin connects Claude Code to the hosted MCP server and adds
two skills:

```sh
claude plugin marketplace add poyrazK/faas-web
claude plugin install gregale@gregale
```

- `/gregale:deploy` deploys the current project, verifies its URL and rolls
  back if the new release fails.
- `/gregale:diagnose` finds why an app is failing from its deployments,
  health, errors and logs.

The first time Claude Code connects, you sign in to Gregale in the browser as
above. Choose "Diagnose and recover" there if you want `/gregale:diagnose` to
use the request and regression tools; with "Core tools" it runs the matching
`gregale` CLI commands instead. The skills deploy with the `gregale` CLI, so
install it and log in or set `FAAS_TOKEN` first.

## Project instructions for agents

`gregale init` writes an `AGENTS.md` into each new project. It tells coding
agents how to deploy, check and roll back that project. Claude Code, Codex,
Cursor and other agents read it automatically. Edit it like any other project
file; the CLI never overwrites an existing one.

## Documentation for agents

- [`https://gregale.dev/llms.txt`](https://gregale.dev/llms.txt) lists every
  documentation page with a one-line summary.
- [`https://gregale.dev/llms-full.txt`](https://gregale.dev/llms-full.txt)
  contains all pages as one Markdown file.
- Every page is also available as Markdown: add `.md` to its URL, so
  `/docs/agents` becomes `/docs/agents.md`.
