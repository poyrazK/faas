# Deploy with Gemini CLI

Gemini CLI can deploy, check and roll back your apps on Gregale by running the
`gregale` CLI, the same commands you would run yourself. This page sets that
up and shows what to ask for. The CLI's automation contract, scoped keys and
approvals are on [Use Gregale from coding agents](agents.md).

Gemini CLI serves Gemini Code Assist Standard and Enterprise licenses and
paid Gemini API or Vertex AI keys. Free, Google AI Pro and Google AI Ultra
accounts moved to Antigravity in June 2026; see
[Deploy with Antigravity](antigravity.md).

## Install the CLI and sign in

```sh
curl -fsSL https://get.gregale.dev | sh
gregale login
```

`gregale login` opens your browser once, and Gemini CLI uses that login for
every `gregale` command it runs. Where nobody can open a browser, put a
scoped key in Gemini CLI's environment as `GREGALE_TOKEN` instead; see
[Give the agent its own key](agents.md#give-the-agent-its-own-key).

## Tell Gemini CLI how the project deploys

Projects made with `gregale init` include an `AGENTS.md` with Gregale's
deploy, check and rollback steps.

For an existing project, run this in its root:

```sh
gregale init --agents --name my-api
```

It adds a Gregale section to `AGENTS.md`; see
[Project instructions for agents](agents.md#project-instructions-for-agents).
In a project with a `.gemini` folder, it also creates
`.gemini/settings.json` so Gemini CLI reads `AGENTS.md`.

Gemini CLI reads `GEMINI.md` unless told otherwise. If the project has no
`.gemini` folder, or already has a `.gemini/settings.json`, add `AGENTS.md`
yourself (older releases call the setting `contextFileName`):

```json
{
  "context": {
    "fileName": ["AGENTS.md", "GEMINI.md"]
  }
}
```

## Deploy

Ask in your own words, for example:

> Deploy this project to Gregale as my-api. Check what Gregale detects first,
> and verify the app answers after.

Gemini CLI then runs commands like these, asking you before each one unless
you have allowed it:

```sh
gregale deploy --dry-run
gregale --non-interactive --json deploy --name my-api
gregale --non-interactive --json release verify <deployment-id> --app my-api --path /
```

`--dry-run` shows the framework, start command and port Gregale detects, and
warns about problems such as a server bound to `localhost`. When a deploy
fails, the error names the cause, and Gemini CLI can read more with:

```sh
gregale --non-interactive --json app health my-api --errors
gregale logs my-api --since 15m
```

A new app can be private: its URL answers `401` until you open it. Ask Gemini
CLI to run `gregale app update my-api --no-require-authn` only if the app
should be public.

## Roll back

> The last deploy of my-api broke the checkout page. Roll it back.

```sh
gregale --non-interactive --json rollback my-api
```

The command returns when the previous release serves again. See
[Safe releases](releases.md).

## The MCP server (preview)

Gregale's hosted MCP server, in preview, gives Gemini CLI Gregale's tools
without the CLI. `gregale mcp connect` does not configure Gemini CLI yet, so
add the server with Gemini CLI's own command, then sign in from inside Gemini
CLI with `/mcp auth gregale`:

```sh
gemini mcp add --scope user --transport http gregale https://api.gregale.dev/mcp
```

See [Connect an agent to Gregale](agents.md#connect-an-agent-to-gregale).

## What Gregale learns

The CLI tells Gregale that it runs under Gemini CLI, so Gregale can see which
agents its customers use. It sends nothing else about the session. Set
`DO_NOT_TRACK=1` to leave it out.
