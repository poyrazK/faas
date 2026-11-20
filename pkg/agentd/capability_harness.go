package agentd

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/onebox-faas/faas/pkg/agentpresets"
	"github.com/onebox-faas/faas/pkg/api"
)

// capabilityServerName is the MCP server name a preset harness knows the
// capability gateway by; its tools appear as mcp__gregale__<tool> and
// similar.
const capabilityServerName = "gregale"

// capabilityToolTimeoutSec is how long a preset harness lets one gateway
// tool call run: the longest approval wait (ADR-20261027090016 amendment 2)
// plus room for the tool itself. Harnesses default to about a minute,
// which would abandon every approval.
const capabilityToolTimeoutSec = api.AgentRunApprovalTimeoutSec + 120

// presetCapabilityWiring registers the gateway with a preset harness, using
// each harness's documented runtime configuration so the read-only
// agent-base drive needs no config file. It applies only when the run uses
// the preset's own command; a seller's command gets the URL variable alone.
type presetCapabilityWiring struct {
	// args returns flags to put after the binary name (prepend) and at the
	// end of the command (appendArgs).
	args func(url string) (prepend, appendArgs []string)
	env  func(url string) map[string]string
}

var presetCapabilityWirings = map[string]presetCapabilityWiring{
	// Codex: -c overrides config.toml keys; tool_timeout_sec defaults to 60.
	// codex exec runs with approval policy "never", under which it refuses
	// every MCP tool call that would ask first, so the server's tools are
	// approved up front: the gateway applies the version's policy itself.
	"codex": {args: func(url string) ([]string, []string) {
		server := "mcp_servers." + capabilityServerName
		return []string{
			"-c", server + ".url=" + strconv.Quote(url),
			"-c", server + ".tool_timeout_sec=" + strconv.Itoa(capabilityToolTimeoutSec),
			"-c", server + `.default_tools_approval_mode="approve"`,
		}, nil
	}},
	// Claude Code: --mcp-config takes inline JSON; print mode runs an MCP
	// tool only when --allowedTools names it (mcp__<server> names them all).
	// --mcp-config and --allowedTools are variadic, so they go last.
	"claude-code": {
		args: func(url string) ([]string, []string) {
			return nil, []string{"--mcp-config", mcpServersJSON("mcpServers", map[string]any{"type": "http", "url": url}),
				"--allowedTools", "mcp__" + capabilityServerName}
		},
		env: func(string) map[string]string {
			return map[string]string{"MCP_TOOL_TIMEOUT": strconv.Itoa(capabilityToolTimeoutSec * 1000)}
		},
	},
	// OpenCode: OPENCODE_CONFIG_CONTENT is inline config layered over the
	// files. The server's timeout (milliseconds) bounds each tool call; the
	// default is a minute.
	"opencode": {env: func(url string) map[string]string {
		return map[string]string{"OPENCODE_CONFIG_CONTENT": mcpServersJSON("mcp",
			map[string]any{"type": "remote", "url": url, "enabled": true,
				"timeout": capabilityToolTimeoutSec * 1000})}
	}},
}

func mcpServersJSON(key string, server map[string]any) string {
	raw, err := json.Marshal(map[string]any{key: map[string]any{capabilityServerName: server}})
	if err != nil {
		panic(err) // maps of strings and bools always encode
	}
	return string(raw)
}

// wireCapabilities registers the gateway with the run's preset harness when
// the run has the gateway's URL and runs the preset's own command.
func wireCapabilities(env map[string]string, command []string, preset agentpresets.Preset, isPreset bool) []string {
	url := env[api.AgentCapabilityMCPURLEnv]
	wiring, ok := presetCapabilityWirings[preset.Name]
	if url == "" || !isPreset || !ok || !slices.Equal(command, preset.Run) {
		return command
	}
	if wiring.env != nil {
		for name, value := range wiring.env(url) {
			env[name] = value
		}
	}
	if wiring.args == nil {
		return command
	}
	prepend, appendArgs := wiring.args(url)
	out := prependArgs(command, prepend)
	return append(out[:len(out):len(out)], appendArgs...)
}
