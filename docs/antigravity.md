# Deploy with Antigravity

Antigravity, Google's coding agent, can deploy, check and roll back your apps
on Gregale by running the `gregale` CLI, the same commands you would run
yourself. This page covers both the Antigravity app and its terminal agent,
`agy`. The CLI's automation contract, scoped keys and approvals are on
[Use Gregale from coding agents](agents.md).

Antigravity replaced Gemini CLI for free, Google AI Pro and Google AI Ultra
accounts in June 2026. With a Gemini Code Assist Standard or Enterprise
license, Gemini CLI still works; see [Deploy with Gemini CLI](gemini-cli.md).

## Install the CLI and sign in

```sh
curl -fsSL https://get.gregale.dev | sh
gregale login
```

`gregale login` opens your browser once, and Antigravity uses that login for
every `gregale` command it runs. Where nobody can open a browser, put a
scoped key in the environment Antigravity runs commands in as `GREGALE_TOKEN`
instead; see [Give the agent its own key](agents.md#give-the-agent-its-own-key).

## Tell Antigravity how the project deploys

Projects made with `gregale init` include an `AGENTS.md` with Gregale's
deploy, check and rollback steps.

For an existing project, run this in its root:

```sh
gregale init --agents --name my-api
```

It adds a Gregale section to `AGENTS.md`; see
[Project instructions for agents](agents.md#project-instructions-for-agents).
Antigravity reads `AGENTS.md` and `GEMINI.md` in the folder you open and in
every parent folder up to the repository root, and keeps them in every
conversation, so nothing else needs setting.

## Deploy

Ask in your own words, for example:

> Deploy this project to Gregale as my-api. Check what Gregale detects first,
> and verify the app answers after.

Antigravity then runs commands like these, asking you before each one unless
you have allowed it:

```sh
gregale deploy --dry-run
gregale --non-interactive --json deploy --name my-api
gregale --non-interactive --json release verify <deployment-id> --app my-api --path /
```

`--dry-run` shows the framework, start command and port Gregale detects, and
warns about problems such as a server bound to `localhost`. When a deploy
fails, the error names the cause, and Antigravity can read more with:

```sh
gregale --non-interactive --json app health my-api --errors
gregale logs my-api --since 15m
```

A new app can be private: its URL answers `401` until you open it. Ask
Antigravity to run `gregale app update my-api --no-require-authn` only if the
app should be public.

## Roll back

> The last deploy of my-api broke the checkout page. Roll it back.

```sh
gregale --non-interactive --json rollback my-api
```

The command returns when the previous release serves again. See
[Safe releases](releases.md).

## The MCP server (preview)

Gregale's hosted MCP server, in preview, gives Antigravity Gregale's tools
without the CLI. Add it for every project:

```sh
gregale mcp connect --client antigravity
```

This adds a `gregale` entry to `~/.gemini/config/mcp_config.json` and keeps
the rest of the file. `--project` writes `.agents/mcp_config.json` in this
project instead. Then sign in to Gregale: in the Antigravity app, open
Customizations, then Installed, and select Authenticate next to `gregale`
under MCP Servers; in `agy`, run `/mcp`. Your browser opens once.

To add it by hand, put this in either file:

```json
{
  "mcpServers": {
    "gregale": {
      "serverUrl": "https://api.gregale.dev/mcp"
    }
  }
}
```

Antigravity signs in to MCP servers only through the browser. Its config
cannot read an agent key from the environment, and Gregale never writes a key
to a file, so `gregale mcp connect --headless` leaves Antigravity out. For an
agent with no person at a browser, use the CLI with `GREGALE_TOKEN` instead. See
[Connect an agent to Gregale](agents.md#connect-an-agent-to-gregale).

## What Gregale learns

When the CLI can tell which coding agent runs it, it tells Gregale the
agent's name, and nothing else about the session. It does not recognize
Antigravity yet, so it sends no agent name for Antigravity sessions. Set
`DO_NOT_TRACK=1` to leave the name out for every agent.
