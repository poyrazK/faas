# Use Gregale from coding agents

Coding agents such as Claude Code, Codex and Cursor can deploy and operate
apps on Gregale with the same CLI and API you use. This page covers the
credential to give an agent, the CLI's automation contract, the management
MCP server, the Claude Code plugin, project instructions, and documentation
an agent can read directly.

## Give the agent its own key

Create a dedicated key instead of sharing your own login. Scope it to what the
agent needs:

```sh
gregale keys add coding-agent --scopes apps:read,deploy:write
```

`apps:read` lets the agent inspect apps, deployments, logs and metrics.
`deploy:write` lets it create apps, deploy and roll back. Add `secrets:write`
only if the agent should set environment variables: `gregale env push` stores
them through the secrets API. A key without `--scopes` is a full-access
`admin` key.

Put the key in the agent's environment as `FAAS_TOKEN`. It overrides the
saved login for every `gregale` command and for the MCP server. Don't paste
it into a prompt, a command argument or a committed file. Remove it with
`gregale keys rm <id>` when the agent no longer needs it.

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
| `3` | `temporary_failure` | Inspect state, then retry |
| `4` | `not_found` | Check the app or deployment name |
| `5` | `conflict` | Read the current state before retrying |
| `6` | `permission_denied` | The key lacks a scope or the plan lacks the feature |
| `130` | `cancelled` | The command was interrupted or declined |

A deploy writes a receipt with the deployment `id`, its `status` and the
`app_url`. If `timed_out` is true, run the receipt's `resume_command` instead
of deploying again. After a temporary failure, the server may already have
accepted the deploy: check `gregale deploys status <id> --app <slug>` first.

Commands that delete or replace something need explicit approval, such as
`--yes` for app deletion. Preview them with `--dry-run` first. Missing
approval exits `1`; `--non-interactive` never approves on the agent's behalf.

See [CLI configuration](cli-config.md) for the full contract, including
pagination and profiles.

## Connect the management MCP server

`gregale mcp serve` gives an agent Gregale's management tools over MCP. The
agent starts it as a subprocess, and it uses `FAAS_TOKEN` or the selected
profile's login.

Claude Code:

```sh
claude mcp add gregale -- gregale mcp serve --transport stdio
```

Codex (`~/.codex/config.toml`):

```toml
[mcp_servers.gregale]
command = "gregale"
args = ["mcp", "serve", "--transport", "stdio"]
```

Other clients that use an `mcpServers` JSON file:

```json
{
  "mcpServers": {
    "gregale": {
      "command": "gregale",
      "args": ["mcp", "serve", "--transport", "stdio"]
    }
  }
}
```

The tools are read-only by default. They list apps and deployments, read
logs, metrics and health, explain a failed deployment, diagnose a single
request, group recurring errors, and compare two deployments. Log text is
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
   variables; the key also needs `secrets:write` for these.
3. Later deploys of a serving app are staged at 0% traffic until
   `gregale_promote_deployment`.

These tools run the `gregale` CLI on your machine, so they do exactly what the
matching commands do. Secrets stay in the CLI: the agent asks you to run
`gregale secrets set` and never handles the value.

## Install the Claude Code plugin

The `gregale` plugin bundles the MCP server with two skills:

```sh
claude plugin marketplace add poyrazK/faas-web
claude plugin install gregale@gregale
```

- `/gregale:deploy` deploys the current project, verifies its URL and rolls
  back if the new release fails.
- `/gregale:diagnose` finds why an app is failing from its deployments,
  health, errors and logs.

The plugin runs the `gregale` CLI, so install it and log in or set
`FAAS_TOKEN` first.

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
