package main

// adr: 20261122194324 — gateway-enforced MCP access (api-key, oauth-proxy),
// tool limits and replay are dark: apid refuses the policies until the
// mcp_gateway_auth platform flag releases them. Until then the CLI keeps
// `mcp auth`, `mcp replay`, `mcp insights` and `mcp keys` out of help, completions, man pages, the
// generated reference and dispatch. FAAS_MCP_GATEWAY_PREVIEW_ENABLED in the
// shell shows them.

import (
	"os"
	"strings"
)

func mcpGatewayPreviewCLI() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FAAS_MCP_GATEWAY_PREVIEW_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// mcpGatewayPreviewSubcommands are the mcp subcommands the preview adds.
var mcpGatewayPreviewSubcommands = map[string]bool{"auth": true, "replay": true, "insights": true, "keys": true, "stats": true}

// withoutMCPGatewayPreview is command as the CLI shows it while the preview
// is hidden. It never modifies the shared manifest.
func withoutMCPGatewayPreview(command cliCommand) cliCommand {
	if command.Name != "mcp" || command.Subcommands == nil {
		return command
	}
	subs := make([]cliSub, 0, len(command.Subcommands))
	for _, sub := range command.Subcommands {
		if mcpGatewayPreviewSubcommands[sub.Name] {
			continue
		}
		if sub.Name == "deploy" {
			sub.Flags = withoutFlag(sub.Flags, "replay")
		}
		subs = append(subs, sub)
	}
	command.Subcommands = subs
	return command
}

// withoutFlag is flags without name, in a new slice.
func withoutFlag(flags []cliFlag, name string) []cliFlag {
	out := make([]cliFlag, 0, len(flags))
	for _, f := range flags {
		if f.Name != name {
			out = append(out, f)
		}
	}
	return out
}
