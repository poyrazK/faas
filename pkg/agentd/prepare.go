package agentd

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/agentpresets"
	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// Machine is the VM agentd asks schedd for.
type Machine struct {
	RAMMB         int
	VCPU          int
	CPUMillicores int
}

// defaultImageRAMMB sizes a seller image's first run before size history
// exists (ADR-20261027090010 §5); presets carry their own default.
const defaultImageRAMMB = 1024

var placeholder = regexp.MustCompile(`\{\{\s*(prompt|(?:inputs|event)\.[A-Za-z0-9_]+)\s*\}\}`)

// prepare turns a claimed run into its machine and its guest request. Gregale
// renders the prompt; the guest never renders templates (ADR-20261027090010 §3).
func prepare(claim agentstore.Claim) (Machine, agentrunproto.Request, error) {
	plan := api.Plan(claim.Plan)
	if !plan.Valid() || plan == api.PlanFree {
		return Machine{}, agentrunproto.Request{}, fmt.Errorf("the %q plan has no agent runs", claim.Plan)
	}
	idx := plan.PlanIndex()
	preset, isPreset := agentpresets.Lookup(claim.Harness.Preset)
	ram := claim.RAMMB
	if ram == 0 {
		ram = defaultImageRAMMB
		if isPreset {
			ram = preset.DefaultRAMMB
		}
	}
	ram = min(ram, api.AgentRunRAMMBMax[idx])
	vcpu := api.AgentRunVCPUMax[idx]
	machine := Machine{RAMMB: ram, VCPU: vcpu, CPUMillicores: vcpu * 1000}

	prompt := renderPrompt(claim.Template, claim.Prompt, claim.Inputs)
	command := claim.Harness.Run
	if len(command) == 0 && isPreset {
		command = preset.Run
	}
	if len(command) == 0 {
		return Machine{}, agentrunproto.Request{}, errors.New("the agent version has no command")
	}
	args := make([]string, len(command))
	for i, arg := range command {
		args[i] = strings.ReplaceAll(arg, agentpresets.PromptPlaceholder, prompt)
	}
	event := []byte(claim.Inputs)
	if len(event) == 0 {
		event = []byte(`{}`)
	}
	env := map[string]string{}
	if isPreset {
		for name, value := range preset.Env {
			env[name] = value
		}
	}
	modelEnv(env, claim.Harness.Models, claim.Integrations)
	if len(env) == 0 {
		env = nil
	}
	req := agentrunproto.Request{
		Version: agentrunproto.Version, RunID: claim.RunID, Command: args, Env: env,
		Files: []agentrunproto.File{
			{Path: "prompt.txt", Content: []byte(prompt)},
			{Path: "event.json", Content: event},
		},
		TimeoutSec: claim.TimeoutSec, MaxLogBytes: api.AgentRunLogMaxBytes, MaxResultBytes: api.AgentResultMaxBytes[idx],
		MaxOutputFiles: api.AgentRunOutputFilesMax, MaxOutputBytes: api.AgentRunOutputFilesMaxBytes,
	}
	if err := req.Validate(); err != nil {
		return Machine{}, agentrunproto.Request{}, err
	}
	return machine, req, nil
}

// modelProviders is how each provider's SDK reaches the gateway: the path
// under /i/{integration} its base URL ends at, and the key variable the SDK
// refuses to start without. The relay replaces the placeholder key with the
// integration's credential (ADR-989), so no model key enters the VM.
var modelProviders = map[string]struct{ basePath, keyVar string }{
	api.AgentModelOpenAI:    {basePath: "/v1", keyVar: "OPENAI_API_KEY"},
	api.AgentModelAnthropic: {basePath: "", keyVar: "ANTHROPIC_API_KEY"},
}

// relayPlaceholderKey satisfies SDKs that require a key to be set.
const relayPlaceholderKey = "gregale-relay"

// modelEnv points each provider's base-URL variable at its integration
// through the relay (ADR-20261027090010 §3). A provider with no integration gets
// nothing, so the harness fails fast instead of reaching a provider directly.
func modelEnv(env, models, integrations map[string]string) {
	for provider, id := range integrations {
		variable, shape := models[provider], modelProviders[provider]
		if variable == "" || id == "" {
			continue
		}
		env[variable] = agentrunproto.OutboundRelayEndpoint + "/i/" + id + shape.basePath
		if shape.keyVar != "" && env[shape.keyVar] == "" {
			env[shape.keyVar] = relayPlaceholderKey
		}
	}
}

// aiBudget is the run's model spend cap: the version's, or the plan's
// default (ADR-20261027090010 Limits).
func aiBudget(claim agentstore.Claim) int64 {
	if claim.AIBudgetMillicents > 0 {
		return claim.AIBudgetMillicents
	}
	plan := api.Plan(claim.Plan)
	if !plan.Valid() {
		return 0
	}
	return api.AgentRunAIBudgetDefaultMillicents[plan.PlanIndex()]
}

// renderPrompt fills {{prompt}} with the run's prompt and {{inputs.KEY}} or
// {{event.KEY}} with an input: a string as is, anything else as JSON, a
// missing key as nothing. A run prompt the template never places is
// appended after a blank line.
func renderPrompt(template string, prompt *string, inputs json.RawMessage) string {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(inputs, &fields)
	placedPrompt := false
	out := placeholder.ReplaceAllStringFunc(template, func(match string) string {
		name := strings.TrimSpace(match[2 : len(match)-2])
		if name == "prompt" {
			placedPrompt = true
			if prompt == nil {
				return ""
			}
			return *prompt
		}
		_, key, _ := strings.Cut(name, ".")
		raw, ok := fields[key]
		if !ok {
			return ""
		}
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return text
		}
		return string(raw)
	})
	if !placedPrompt && prompt != nil && *prompt != "" {
		out += "\n\n" + *prompt
	}
	return out
}
