package agentd

import (
	"strconv"

	"github.com/onebox-faas/faas/pkg/agentpresets"
)

// relayProviderName is the model provider id a preset harness knows the
// relay by when it does not read its provider's base-URL variable.
const relayProviderName = "gregale"

// presetModelWirings route a preset harness's model calls through the relay
// when the harness ignores the base-URL variable modelEnv sets. Each returns
// flags to put after the binary name, given the run's environment, or nil
// when the run has no integration for the harness's provider.
var presetModelWirings = map[string]func(env map[string]string) []string{
	// Codex 0.163 ignores OPENAI_BASE_URL and calls api.openai.com, over a
	// WebSocket by default (metal check 2026-11-20). A custom provider is
	// the documented way to move it: -c overrides config.toml keys, the
	// Responses API over plain HTTP, and the relay's placeholder key.
	"codex": func(env map[string]string) []string {
		base := env["OPENAI_BASE_URL"]
		if base == "" {
			return nil
		}
		p := "model_providers." + relayProviderName
		return []string{
			"-c", "model_provider=" + strconv.Quote(relayProviderName),
			"-c", p + ".name=" + strconv.Quote(relayProviderName),
			"-c", p + ".base_url=" + strconv.Quote(base),
			"-c", p + `.env_key="OPENAI_API_KEY"`,
			"-c", p + `.wire_api="responses"`,
			"-c", p + ".supports_websockets=false",
		}
	},
}

// wireModels applies the preset's model wiring. presetCommand says the run
// uses the preset's own command; a seller's command is left as written.
func wireModels(env map[string]string, command []string, preset agentpresets.Preset, presetCommand bool) []string {
	wiring, ok := presetModelWirings[preset.Name]
	if !presetCommand || !ok {
		return command
	}
	return prependArgs(command, wiring(env))
}

// prependArgs puts args after the command's binary name.
func prependArgs(command, args []string) []string {
	if len(args) == 0 {
		return command
	}
	out := make([]string, 0, len(command)+len(args))
	out = append(out, command[0])
	out = append(out, args...)
	return append(out, command[1:]...)
}
