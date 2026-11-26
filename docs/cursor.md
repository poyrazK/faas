# Deploy with Cursor

Cursor's agent can deploy, check and roll back your apps on Gregale by
running the `gregale` CLI in its terminal, the same commands you would run
yourself. This page sets that up and shows what to ask for. The CLI's
automation contract, scoped keys and approvals are on
[Use Gregale from coding agents](agents.md).

## Install the CLI and sign in

```sh
curl -fsSL https://get.gregale.dev | sh
gregale login
```

`gregale login` opens your browser once, and the agent uses that login for
every `gregale` command it runs. Where nobody can open a browser, put a
scoped key in the agent's environment as `GREGALE_TOKEN` instead; see
[Give the agent its own key](agents.md#give-the-agent-its-own-key).

## Tell Cursor how the project deploys

Cursor reads `AGENTS.md` in the project root. Projects made with
`gregale init` include one with Gregale's deploy, check and rollback steps.

For an existing project, run this in its root:

```sh
gregale init --agents --name my-api
```

It adds a Gregale section to `AGENTS.md`; see
[Project instructions for agents](agents.md#project-instructions-for-agents).

## Deploy

Ask in your own words, for example:

> Deploy this project to Gregale as my-api. Check what Gregale detects first,
> and verify the app answers after.

The agent then runs commands like these, asking you before each one unless
you let it run commands on its own:

```sh
gregale deploy --dry-run
gregale --non-interactive --json deploy --name my-api
gregale --non-interactive --json release verify <deployment-id> --app my-api --path /
```

`--dry-run` shows the framework, start command and port Gregale detects, and
warns about problems such as a server bound to `localhost`. When a deploy
fails, the error names the cause, and the agent can read more with:

```sh
gregale --non-interactive --json app health my-api --errors
gregale logs my-api --since 15m
```

A new app can be private: its URL answers `401` until you open it. Ask the
agent to run `gregale app update my-api --no-require-authn` only if the app
should be public.

## Roll back

> The last deploy of my-api broke the checkout page. Roll it back.

```sh
gregale --non-interactive --json rollback my-api
```

The command returns when the previous release serves again. See
[Safe releases](releases.md).

## The MCP server (preview)

Gregale's hosted MCP server, in preview, gives Cursor Gregale's tools without
the CLI.
[Add Gregale to Cursor](https://cursor.com/en/install-mcp?name=gregale&config=eyJ1cmwiOiJodHRwczovL2FwaS5ncmVnYWxlLmRldi9tY3AifQ==)
in one click, or run `gregale mcp connect --client cursor` to add it to
`~/.cursor/mcp.json`, keeping your other servers. To add it yourself:

```json
{
  "mcpServers": {
    "gregale": {
      "url": "https://api.gregale.dev/mcp"
    }
  }
}
```

Your browser opens to sign in to Gregale the first time Cursor connects. See
[Connect an agent to Gregale](agents.md#connect-an-agent-to-gregale).

## What Gregale learns

The CLI tells Gregale that it runs under Cursor, so Gregale can see which
agents its customers use. It sends nothing else about the session. Set
`DO_NOT_TRACK=1` to leave it out.
