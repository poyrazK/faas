# Gregale plugin for Cursor

Deploy, diagnose and roll back Gregale apps from Cursor. The plugin adds:

- Gregale's hosted MCP server (`https://api.gregale.dev/mcp`). The first time
  Cursor connects, it opens your browser: sign in to Gregale (or sign up),
  choose what the agent may do and which apps it may use, and connect;
- the `deploy` skill, which deploys the current project, checks an
  application response and rolls back a broken release with your approval;
- the `diagnose` skill, which finds why an app is failing or slow;
- a `gregale` rule with the safe defaults both skills follow.

The skills deploy with the `gregale` CLI, so install it and log in (or set
`GREGALE_TOKEN`): https://gregale.dev/docs/install

Without the plugin, add only the MCP server:
[Add Gregale to Cursor](https://cursor.com/en/install-mcp?name=gregale&config=eyJ1cmwiOiJodHRwczovL2FwaS5ncmVnYWxlLmRldi9tY3AifQ==).

See https://gregale.dev/docs/cursor and https://gregale.dev/docs/agents.

## Source

The plugin is maintained in Gregale's main repository
(`packaging/cursor-plugin`) and published to
[gregale-dev/gregale-cursor-plugin](https://github.com/gregale-dev/gregale-cursor-plugin)
with `scripts/publish-cursor-plugin.sh`; don't edit the published copy.

The skills are the Claude Code plugin's skills, copied unchanged.
`cmd/gregale/cursor_plugin_test.go` fails when the copies differ, when the
MCP server is not the hosted endpoint, or when the manifest is incomplete.
Bump `version` in `.cursor-plugin/plugin.json` when anything changes.
