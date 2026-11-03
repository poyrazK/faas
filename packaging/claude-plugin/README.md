# Gregale plugin for Claude Code

Deploy, diagnose and roll back Gregale apps from Claude Code. The plugin adds:

- Gregale's hosted MCP server (`https://api.gregale.dev/mcp`). The first time
  Claude Code connects, it opens your browser: sign in to Gregale (or sign
  up), choose what the agent may do and which apps it may use, and connect;
- `/gregale:deploy`, which deploys the current project, checks its URL and
  rolls back a broken release with your approval;
- `/gregale:diagnose`, which finds why an app is failing or slow.

## Install

The skills deploy with the `gregale` CLI, so install it and log in (or set
`FAAS_TOKEN`), then:

```sh
claude plugin marketplace add poyrazK/faas-web
claude plugin install gregale@gregale
```

The marketplace entry in `poyrazK/faas-web` points at this directory.

To run the MCP server on your own machine instead, for example without
network access to `api.gregale.dev`, see
https://gregale.dev/docs/agents#run-the-mcp-server-locally.

## Develop

```sh
claude plugin validate packaging/claude-plugin
claude --plugin-dir packaging/claude-plugin
```

`cmd/gregale/claude_plugin_test.go` keeps the plugin in step with the CLI: the
MCP server must be the hosted endpoint, and every `gregale` command and
`gregale_*` tool the skills name must exist. Bump `version` in
`.claude-plugin/plugin.json` when the skills or the server change.

See https://gregale.dev/docs/agents for the full agent guide.
