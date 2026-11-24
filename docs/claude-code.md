# Deploy with Claude Code

Claude Code can deploy, check and roll back your apps on Gregale by running
the `gregale` CLI, the same commands you would run yourself. This page sets
that up and shows what to ask for. The CLI's automation contract, scoped keys
and approvals are on [Use Gregale from coding agents](agents.md).

## Install the CLI and sign in

```sh
curl -fsSL https://get.gregale.dev | sh
gregale login
```

`gregale login` opens your browser once, and Claude Code uses that login for
every `gregale` command it runs. Where nobody can open a browser, such as a
container or CI, put a scoped key in Claude Code's environment as
`GREGALE_TOKEN` instead; see
[Give the agent its own key](agents.md#give-the-agent-its-own-key).

## Tell Claude Code how the project deploys

Projects made with `gregale init` include an `AGENTS.md` with Gregale's
deploy, check and rollback steps.

For an existing project, run this in its root:

```sh
gregale init --agents --name my-api
```

It adds a Gregale section to `AGENTS.md`; see
[Project instructions for agents](agents.md#project-instructions-for-agents).
It also adds `@AGENTS.md` to an existing `CLAUDE.md`.

Claude Code reads `CLAUDE.md`; recent versions also read `AGENTS.md` when a
project has no `CLAUDE.md`. If the project has a `CLAUDE.md`,
`gregale init --agents` adds this line at the top of it; on a project without
one, add it yourself to be sure on any version:

```text
@AGENTS.md
```

## Deploy

Ask in your own words, for example:

> Deploy this project to Gregale as my-api. Check what Gregale detects first,
> and verify the app answers after.

Claude Code then runs commands like these, asking you before each one unless
you have allowed it:

```sh
gregale deploy --dry-run
gregale --non-interactive --json deploy --name my-api
gregale --non-interactive --json release verify <deployment-id> --app my-api --path /
```

`--dry-run` shows the framework, start command and port Gregale detects, and
warns about problems such as a server bound to `localhost`. When a deploy
fails, the error names the cause, and Claude Code can read more with:

```sh
gregale --non-interactive --json app health my-api --errors
gregale logs my-api --since 15m
```

A new app can be private: its URL answers `401` until you open it. If the app
should be public, such as a site people open in a browser, ask Claude Code to
add `access: public` to its `gregale.yaml`; every deploy, including through the
MCP server, keeps it public ([declare access](auth.md#declare-access-in-gregaleyaml)).

## Roll back

> The last deploy of my-api broke the checkout page. Roll it back.

```sh
gregale --non-interactive --json rollback my-api
```

The command returns when the previous release serves again. See
[Safe releases](releases.md).

## The Gregale plugin (preview)

The `gregale` plugin adds two skills, `/gregale:deploy` and
`/gregale:diagnose`, and connects Claude Code to Gregale's hosted MCP server,
which is in preview:

```sh
claude plugin marketplace add poyrazK/faas-web
claude plugin install gregale@gregale
```

To add only the MCP server, run `gregale mcp connect --client claude-code`.
See [Install the Claude Code plugin](agents.md#install-the-claude-code-plugin).

## What Gregale learns

The CLI tells Gregale that it runs under Claude Code, so Gregale can see
which agents its customers use. It sends nothing else about the session. Set
`DO_NOT_TRACK=1` to leave it out.
