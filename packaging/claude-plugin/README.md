# Gregale plugin for Claude Code

Deploy, diagnose and roll back Gregale apps from Claude Code. The plugin adds:

- the Gregale management MCP server (`gregale mcp serve --transport stdio
  --tools core`), read-only, with the everyday `core` toolset;
- `/gregale:deploy`, which deploys the current project, checks its URL and
  rolls back a broken release with your approval;
- `/gregale:diagnose`, which finds why an app is failing or slow.

## Install

Install the `gregale` CLI and log in (or set `FAAS_TOKEN`), then:

```sh
claude plugin marketplace add poyrazK/faas-web
claude plugin install gregale@gregale
```

The marketplace entry in `poyrazK/faas-web` points at this directory.

## Develop

```sh
claude plugin validate packaging/claude-plugin
claude --plugin-dir packaging/claude-plugin
```

`cmd/gregale/claude_plugin_test.go` keeps the plugin in step with the CLI: the
MCP server arguments must parse, and every `gregale` command and `gregale_*`
tool the skills name must exist. Bump `version` in
`.claude-plugin/plugin.json` when the skills change.

See https://gregale.dev/docs/agents for the full agent guide.
