# Gregale plugin for ChatGPT and Codex

Deploy, check, diagnose and roll back Gregale apps from ChatGPT and Codex.
The plugin is an [Agent Plugins](https://agent-plugins.org) package:

- `plugin.json`: the manifest, with the OpenAI listing, onboarding skill,
  review test cases and release notes under `extensions["com.openai"]`;
- `mcp.json`: Gregale's hosted MCP server, `https://api.gregale.dev/mcp`,
  with OAuth. ChatGPT registers itself by Client ID Metadata Document and
  falls back to dynamic client registration;
- `skills/`: `get-started` (the onboarding skill), `deploy`, `diagnose`,
  `roll-back` and `migrate`. They use only MCP tools, because ChatGPT has no
  shell to run the `gregale` CLI;
- `assets/logo.png`: the 512×512 logo.

Unlike the [Claude Code plugin](../claude-plugin), whose skills deploy with
the CLI, this plugin deploys with the hosted upload tools: small projects
are sent inline (200 files, 96 KiB), and Codex can upload larger ones.

## Test it privately

Package the directory, with the manifest at the root of the ZIP:

```sh
cd packaging/chatgpt-plugin && zip -r ../../gregale-chatgpt-plugin.zip . -x README.md
```

- **ChatGPT:** in Plugins, choose Add, then "Add custom MCP server" and
  "Create as a plugin", or upload the ZIP at
  https://platform.openai.com/plugins and test it before submitting.
- **Codex:** copy the directory to `~/.codex/plugins/gregale` and list it in
  `~/.agents/plugins/marketplace.json`.

## Submit it

Submission happens at https://platform.openai.com/plugins and needs, besides
this package:

- the developer's verified identity, and domain verification for
  `gregale.dev` (`/.well-known/openai-apps-challenge`);
- a demo Gregale account the reviewers can sign in to without MFA or a
  magic link, entered in the portal's review details, never in the package;
- a video of the five positive test cases, as `demo_recording_url` in
  `plugin.json` or in the portal;
- the countries to publish in.

The OAuth callback ChatGPT uses, `https://chatgpt.com/connector_platform_oauth_redirect`,
is already allowed by `faas-mcp-bridge` (`pkg/daemonunitspec/mcp_bridge.go`).
ChatGPT uses this stable callback because the authorization server returns
the `iss` parameter (RFC 9207).

## Develop

`cmd/gregale/chatgpt_plugin_test.go` keeps the package in step with the
server: the listing's field limits, exactly five positive and three
negative review cases, the hosted MCP endpoint, the skill headers, and every
`gregale_*` tool and `gregale` command the skills name. A skill may name a
tool outside the default core toolset only where it also names the
"Diagnose and recover" connection choice that adds it. Bump `version` in
`plugin.json` when the skills or the server change.
