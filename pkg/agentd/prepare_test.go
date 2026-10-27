// adr: 20261027090010, 20261027090014
package agentd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/agentpresets"
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
	if req.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:2774/i/"+id || req.Env["ANTHROPIC_API_KEY"] != relayPlaceholderKey {
		t.Fatalf("anthropic env %v", req.Env)
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
