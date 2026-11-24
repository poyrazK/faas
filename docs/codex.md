# Deploy with Codex

Codex can deploy, check and roll back your apps on Gregale by running the
`gregale` CLI, the same commands you would run yourself. This page sets that
up and shows what to ask for. The CLI's automation contract, scoped keys and
approvals are on [Use Gregale from coding agents](agents.md).

## Install the CLI and sign in

```sh
curl -fsSL https://get.gregale.dev | sh
gregale login
```

`gregale login` opens your browser once; run it yourself, in your own
terminal. Codex then uses that login for every `gregale` command it runs.
Where nobody can open a browser, put a scoped key in Codex's environment as
`GREGALE_TOKEN` instead; see
[Give the agent its own key](agents.md#give-the-agent-its-own-key).

The CLI needs to reach Gregale over the network. If Codex runs commands in
its `workspace-write` sandbox without network access, allow it in
`~/.codex/config.toml`:

```toml
[sandbox_workspace_write]
network_access = true
```

## Tell Codex how the project deploys

Codex reads `AGENTS.md`. Projects made with `gregale init` include one with
Gregale's deploy, check and rollback steps.

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

Codex then runs commands like these:

```sh
gregale deploy --dry-run
gregale --non-interactive --json deploy --name my-api
gregale --non-interactive --json release verify <deployment-id> --app my-api --path /
```

`--dry-run` shows the framework, start command and port Gregale detects, and
warns about problems such as a server bound to `localhost`. When a deploy
fails, the error names the cause, and Codex can read more with:

```sh
gregale --non-interactive --json app health my-api --errors
gregale logs my-api --since 15m
```

A new app can be private: its URL answers `401` until you open it. If the app
should be public, such as a site people open in a browser, ask Codex to add
`access: public` to its `gregale.yaml`; every deploy, including through the
MCP server, keeps it public ([declare access](auth.md#declare-access-in-gregaleyaml)).

## Roll back

> The last deploy of my-api broke the checkout page. Roll it back.

```sh
gregale --non-interactive --json rollback my-api
```

The command returns when the previous release serves again. See
[Safe releases](releases.md).

## The MCP server (preview)

Gregale's hosted MCP server, in preview, gives Codex Gregale's tools without
the CLI. `gregale mcp connect --client codex` adds it, or add it yourself to
`~/.codex/config.toml` and sign in with `codex mcp login gregale`:

```toml
[mcp_servers.gregale]
url = "https://api.gregale.dev/mcp"
```

See [Connect an agent to Gregale](agents.md#connect-an-agent-to-gregale).

## What Gregale learns

The CLI tells Gregale that it runs under Codex, so Gregale can see which
agents its customers use. It sends nothing else about the session. Set
`DO_NOT_TRACK=1` to leave it out.
