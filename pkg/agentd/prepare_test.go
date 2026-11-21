// adr: 20261027090010, 20261027090014
package agentd

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/agentpresets"
	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func TestRenderPrompt(t *testing.T) {
	prompt := "be brief"
	inputs := json.RawMessage(`{"title":"PR 7","n":3,"labels":["a"]}`)
	for _, tc := range []struct {
		template string
		prompt   *string
		want     string
	}{
		{"Triage {{inputs.title}}: {{prompt}}", &prompt, "Triage PR 7: be brief"},
		{"{{ event.title }} has {{inputs.n}} files, {{inputs.labels}}", nil, "PR 7 has 3 files, [\"a\"]"},
		{"Missing {{inputs.nope}}.", nil, "Missing ."},
		{"No placeholder", &prompt, "No placeholder\n\nbe brief"},
		{"{{other}} stays", nil, "{{other}} stays"},
	} {
		if got := renderPrompt(tc.template, tc.prompt, inputs); got != tc.want {
			t.Errorf("%q: %q, want %q", tc.template, got, tc.want)
		}
	}
}

func TestPrepare(t *testing.T) {
	codex, _ := agentpresets.Lookup("codex")
	claim := agentstore.Claim{RunID: "run-1", AccountID: "acct", Plan: "hobby", TimeoutSec: 120,
		Harness: agentstore.Harness{Preset: "codex", Run: codex.Run}, Template: "Fix {{inputs.issue}}", Inputs: json.RawMessage(`{"issue":"#9"}`)}
	machine, req, err := prepare(claim)
	if err != nil {
		t.Fatal(err)
	}
	if machine.RAMMB != codex.DefaultRAMMB || machine.VCPU != api.AgentRunVCPUMax[api.PlanHobby.PlanIndex()] || machine.CPUMillicores != 1000 {
		t.Fatalf("machine %+v", machine)
	}
	if req.Command[len(req.Command)-1] != "Fix #9" || string(req.Files[0].Content) != "Fix #9" || string(req.Files[1].Content) != `{"issue":"#9"}` {
		t.Fatalf("request %+v", req)
	}
	if req.MaxResultBytes != api.AgentResultMaxBytes[api.PlanHobby.PlanIndex()] || req.TimeoutSec != 120 {
		t.Fatalf("limits %+v", req)
	}

	big := claim
	big.Plan, big.RAMMB = "hobby", 4096
	if machine, _, err := prepare(big); err != nil || machine.RAMMB != api.AgentRunRAMMBMax[api.PlanHobby.PlanIndex()] {
		t.Fatalf("a size above the plan is capped: %+v %v", machine, err)
	}
	claude := claim
	claude.Harness = agentstore.Harness{Preset: "claude-code"}
	if _, req, err := prepare(claude); err != nil || req.Env["DISABLE_AUTOUPDATER"] != "1" || req.Command[0] != "claude" {
		t.Fatalf("preset defaults: %+v %v", req, err)
	}
	image := claim
	image.Harness = agentstore.Harness{Image: "ghcr.io/acme/agent:1", Run: []string{"agent", "--task={prompt}"}}
	if machine, req, err := prepare(image); err != nil || req.Env != nil || req.Command[1] != "--task=Fix #9" || machine.RAMMB != defaultImageRAMMB {
		t.Fatalf("image harness: %+v %+v %v", machine, req, err)
	}
	for name, mutate := range map[string]func(*agentstore.Claim){
		"free plan":    func(c *agentstore.Claim) { c.Plan = "free" },
		"no command":   func(c *agentstore.Claim) { c.Harness = agentstore.Harness{Image: "x:1"} },
		"huge prompt":  func(c *agentstore.Claim) { c.Template = strings.Repeat("p", 300<<10) },
		"unknown plan": func(c *agentstore.Claim) { c.Plan = "gold" },
	} {
		c := claim
		mutate(&c)
		if _, _, err := prepare(c); err == nil {
			t.Errorf("%s: prepared", name)
		}
	}
}

// Each provider with an integration gets its base URL under the relay and a
// placeholder key; a provider without one gets nothing (ADR-20261027090010 §3, §7).
func TestPrepareModelEnv(t *testing.T) {
	const id = "0b6a3c2e-8f47-4d3b-9a1e-2c5d7e9f1a3b"
	claim := agentstore.Claim{RunID: "run-1", AccountID: "acct", Plan: "pro", TimeoutSec: 120, Template: "go",
		Harness:      agentstore.Harness{Preset: "opencode", Models: map[string]string{"anthropic": "ANTHROPIC_BASE_URL", "openai": "OPENAI_BASE_URL"}},
		Integrations: map[string]string{"anthropic": id}}
	_, req, err := prepare(claim)
	if err != nil {
		t.Fatal(err)
	}
	// OpenCode's AI SDK takes the base URL to include /v1; Claude Code's
	// Anthropic SDK appends /v1/messages itself.
	if req.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:2774/i/"+id+"/v1" || req.Env["ANTHROPIC_API_KEY"] != relayPlaceholderKey {
		t.Fatalf("anthropic env %v", req.Env)
	}
	claude := claim
	claude.Harness.Preset = "claude-code"
	if _, req, err := prepare(claude); err != nil || req.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:2774/i/"+id {
		t.Fatalf("claude-code anthropic env %v %v", req.Env, err)
	}
	if _, ok := req.Env["OPENAI_BASE_URL"]; ok {
		t.Fatalf("a provider without an integration got a base URL: %v", req.Env)
	}
	image := claim
	image.Harness = agentstore.Harness{Image: "ghcr.io/acme/agent:1", Run: []string{"agent"}, Models: map[string]string{"openai": "LLM_URL"}}
	image.Integrations = map[string]string{"openai": id}
	if _, req, err := prepare(image); err != nil || req.Env["LLM_URL"] != "http://127.0.0.1:2774/i/"+id+"/v1" || req.Env["OPENAI_API_KEY"] != relayPlaceholderKey {
		t.Fatalf("image env %v %v", req.Env, err)
	}
}

// adr: 20261027090016 amendment 1 — a run whose version allows a tool gets
// the capability gateway's MCP URL; one whose policy allows nothing does not.
func TestPrepareCapabilityEnv(t *testing.T) {
	base := agentstore.Claim{RunID: "run-1", AccountID: "acct", Plan: "pro", TimeoutSec: 120, Template: "go",
		Harness: agentstore.Harness{Preset: "opencode"}}
	allowed := base
	allowed.CapabilityPolicy = &api.AgentCapabilityPolicy{Tools: []api.AgentCapabilityTool{{Name: api.AgentCapabilityWebFetch, Mode: api.AgentCapabilityAllow}}}
	denied := base
	denied.CapabilityPolicy = &api.AgentCapabilityPolicy{Tools: []api.AgentCapabilityTool{{Name: api.AgentCapabilityWebFetch, Mode: api.AgentCapabilityDeny}},
		Budgets: &api.AgentCapabilityBudgets{ToolCalls: 10}}
	for name, tc := range map[string]struct {
		claim agentstore.Claim
		want  string
	}{
		"allowed": {allowed, "http://127.0.0.1:2774/mcp"},
		"denied":  {denied, ""},
		"none":    {base, ""},
	} {
		_, req, err := prepare(tc.claim)
		if err != nil {
			t.Fatal(err)
		}
		if got := req.Env[api.AgentCapabilityMCPURLEnv]; got != tc.want {
			t.Errorf("%s: %s=%q, want %q", name, api.AgentCapabilityMCPURLEnv, got, tc.want)
		}
	}
}

func TestAIBudget(t *testing.T) {
	for _, tc := range []struct {
		plan   string
		budget int64
		want   int64
	}{
		{"hobby", 0, 50_000}, {"pro", 0, 200_000}, {"scale", 0, 500_000}, {"pro", 12_345, 12_345}, {"gold", 0, 0},
	} {
		if got := aiBudget(agentstore.Claim{Plan: tc.plan, AIBudgetMillicents: tc.budget}); got != tc.want {
			t.Errorf("%s/%d: %d, want %d", tc.plan, tc.budget, got, tc.want)
		}
	}
}

// adr: 20261027090016 amendment 2 — a preset harness is told about the
// gateway with a tool timeout long enough for an approval; a seller's own
// command and a run without tools are left as they are.
func TestPrepareWiresPresetHarnesses(t *testing.T) {
	policy := &api.AgentCapabilityPolicy{Tools: []api.AgentCapabilityTool{{Name: api.AgentCapabilityWebFetch, Mode: api.AgentCapabilityApprove}}}
	const url = "http://127.0.0.1:2774/mcp"
	for _, name := range []string{"codex", "claude-code", "opencode"} {
		preset, _ := agentpresets.Lookup(name)
		claim := agentstore.Claim{RunID: "run-1", AccountID: "acct", Plan: "pro", TimeoutSec: 120, Template: "go",
			Harness: agentstore.Harness{Preset: name, Run: preset.Run}, CapabilityPolicy: policy}
		_, req, err := prepare(claim)
		if err != nil {
			t.Fatal(err)
		}
		cmd := strings.Join(req.Command, " ")
		if req.Command[0] != preset.Run[0] || !slices.Contains(req.Command, "go") {
			t.Errorf("%s: command %q lost the binary or the prompt", name, cmd)
		}
		switch name {
		case "codex":
			if !strings.Contains(cmd, `-c mcp_servers.gregale.url="`+url+`"`) ||
				!strings.Contains(cmd, "mcp_servers.gregale.tool_timeout_sec=3720") || req.Command[1] != "-c" ||
				!strings.Contains(cmd, `-c mcp_servers.gregale.default_tools_approval_mode="approve" exec`) {
				t.Errorf("codex: %q", cmd)
			}
		case "claude-code":
			if !strings.HasSuffix(cmd, `--mcp-config {"mcpServers":{"gregale":{"type":"http","url":"`+url+`"}}} --allowedTools mcp__gregale`) ||
				req.Env["MCP_TOOL_TIMEOUT"] != "3720000" {
				t.Errorf("claude-code: %q %v", cmd, req.Env)
			}
		case "opencode":
			if req.Env["OPENCODE_CONFIG_CONTENT"] != `{"mcp":{"gregale":{"enabled":true,"timeout":3720000,"type":"remote","url":"`+url+`"}}}` ||
				!slices.Equal(req.Command, []string{"opencode", "run", "go"}) {
				t.Errorf("opencode: %q %v", cmd, req.Env)
			}
		}

		own := claim
		own.Harness.Run = []string{preset.Run[0], "{prompt}"}
		if _, req, err := prepare(own); err != nil || len(req.Command) != 2 || req.Env[api.AgentCapabilityMCPURLEnv] != url {
			t.Errorf("%s with the seller's command: %q %v", name, req.Command, err)
		}
		bare := claim
		bare.CapabilityPolicy = nil
		if _, req, err := prepare(bare); err != nil || len(req.Command) != len(preset.Run) {
			t.Errorf("%s without tools: %q %v", name, req.Command, err)
		}
	}
}

func TestDeadlineCredit(t *testing.T) {
	approve := &api.AgentCapabilityPolicy{Tools: []api.AgentCapabilityTool{{Name: api.AgentCapabilityWebFetch, Mode: api.AgentCapabilityApprove}}}
	allow := &api.AgentCapabilityPolicy{Tools: []api.AgentCapabilityTool{{Name: api.AgentCapabilityWebFetch, Mode: api.AgentCapabilityAllow}}}
	for name, tc := range map[string]struct {
		policy                   *api.AgentCapabilityPolicy
		timeout, credit, ceiling int
	}{
		"no policy":         {nil, 600, 0, 600},
		"no approve tool":   {allow, 600, 0, 600},
		"room for the cap":  {approve, 600, api.AgentRunApprovalCreditMaxSec, 600 + api.AgentRunApprovalCreditMaxSec},
		"scale plan max":    {approve, 3600, 3600, 7200},
		"ceiling bounds it": {approve, agentrunproto.MaxTimeoutSec, 0, agentrunproto.MaxTimeoutSec},
	} {
		credit, ceiling := deadlineCredit(agentstore.Claim{TimeoutSec: tc.timeout, CapabilityPolicy: tc.policy})
		if credit != tc.credit || ceiling != tc.ceiling {
			t.Errorf("%s: credit %d ceiling %d, want %d %d", name, credit, ceiling, tc.credit, tc.ceiling)
		}
	}
}

// Codex ignores OPENAI_BASE_URL, so its preset command selects a provider
// that points at the relay; a run without an OpenAI integration or with the
// seller's own command is left alone.
func TestPrepareRoutesCodexThroughTheRelay(t *testing.T) {
	preset, _ := agentpresets.Lookup("codex")
	const id = "int-1"
	base := agentrunproto.OutboundRelayEndpoint + "/i/" + id + "/v1"
	claim := agentstore.Claim{RunID: "run-1", AccountID: "acct", Plan: "pro", TimeoutSec: 120, Template: "go",
		Harness:      agentstore.Harness{Preset: "codex", Run: preset.Run, Models: map[string]string{"openai": "OPENAI_BASE_URL"}},
		Integrations: map[string]string{"openai": id}}
	_, req, err := prepare(claim)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string{"codex",
		"-c", `model_provider="gregale"`,
		"-c", `model_providers.gregale.name="gregale"`,
		"-c", `model_providers.gregale.base_url="` + base + `"`,
		"-c", `model_providers.gregale.env_key="OPENAI_API_KEY"`,
		"-c", `model_providers.gregale.wire_api="responses"`,
		"-c", "model_providers.gregale.supports_websockets=false"}, "exec", "--skip-git-repo-check", "-s", "workspace-write", "go")
	if !slices.Equal(req.Command, want) || req.Env["OPENAI_API_KEY"] != relayPlaceholderKey {
		t.Errorf("command %q env %v", req.Command, req.Env)
	}

	tools := claim
	tools.CapabilityPolicy = &api.AgentCapabilityPolicy{Tools: []api.AgentCapabilityTool{{Name: api.AgentCapabilityWebFetch, Mode: api.AgentCapabilityAllow}}}
	if _, req, err := prepare(tools); err != nil || !strings.Contains(strings.Join(req.Command, " "), `model_provider="gregale"`) ||
		!strings.Contains(strings.Join(req.Command, " "), "mcp_servers.gregale.url=") || req.Command[len(req.Command)-1] != "go" {
		t.Errorf("codex with tools: %q %v", req.Command, err)
	}
	none := claim
	none.Integrations = nil
	if _, req, err := prepare(none); err != nil || !slices.Equal(req.Command, []string{"codex", "exec", "--skip-git-repo-check", "-s", "workspace-write", "go"}) {
		t.Errorf("codex without an integration: %q %v", req.Command, err)
	}
	own := claim
	own.Harness.Run = []string{"codex", "exec", "{prompt}"}
	if _, req, err := prepare(own); err != nil || !slices.Equal(req.Command, []string{"codex", "exec", "go"}) {
		t.Errorf("codex with the seller's command: %q %v", req.Command, err)
	}
	if !slices.Equal(preset.Run, []string{"codex", "exec", "--skip-git-repo-check", "-s", "workspace-write", "{prompt}"}) {
		t.Errorf("the preset's command was modified: %q", preset.Run)
	}
}
