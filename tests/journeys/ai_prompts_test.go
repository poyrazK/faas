package journeys

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// aiPromptsJourney is the state TestAIManagedPrompts' steps share.
type aiPromptsJourney struct {
	tg          *target
	key         string
	integration string
	name        string
	dir         string
}

// TestAIManagedPrompts is a customer's managed prompt journey (ADR
// 20261114180740, preview): create an OpenAI AI integration, push a prompt
// file with the CLI, push it again unchanged, push a second version, diff
// and render versions, promote and roll back, and delete the prompt. It
// calls no model, so it needs no provider key. Like TestOutboundIntegrations,
// it does not skip while the AI gateway is unavailable: `prompts_enabled` is
// a known failure until the e2e account is released, and the steps after it
// report blocked.
func TestAIManagedPrompts(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	j := &aiPromptsJourney{tg: tg, name: tg.slug("prompt"), dir: t.TempDir()}
	j.key = tg.scopedKey(t, "prompts")
	t.Cleanup(j.cleanup)
	s := newStory(t)
	s.step("prompts_enabled", func(t *testing.T) { featureEnabled(t, tg, "ai-prompts") })
	s.step("create_ai_integration", j.createIntegration)
	s.step("push_first_version", j.pushFirst)
	s.check("identical_push_unchanged", j.pushUnchanged)
	s.step("push_second_version", j.pushSecond)
	s.check("diff_versions", j.diff)
	s.check("render_locally", j.render)
	s.step("promote_and_roll_back", j.promoteAndRollBack)
	s.step("delete_prompt", j.deletePrompt)
}

func (j *aiPromptsJourney) cleanup() {
	if j.integration != "" {
		j.tg.apiCall(http.MethodDelete, "/v1/outbound/ai-prompts/"+j.name, nil, j.key)
		j.tg.apiCall(http.MethodDelete, "/v1/outbound/integrations/"+j.integration, nil, j.key)
	}
}

func (j *aiPromptsJourney) createIntegration(t *testing.T) {
	r := j.tg.apiCall(http.MethodPost, "/v1/outbound/integrations", map[string]any{
		"name":                  j.tg.slug("prompt-ai"),
		"origin":                "https://api.openai.com",
		"allowed_methods":       []string{"POST"},
		"allowed_path_prefixes": []string{"/v1/chat/completions"},
	}, j.key)
	if r.status != http.StatusCreated {
		t.Fatalf("POST /v1/outbound/integrations: %s", r)
	}
	j.integration = str(jsonBody(t, r), "id")
	if r := j.tg.apiCall(http.MethodPut, "/v1/outbound/integrations/"+j.integration+"/ai",
		map[string]any{"ai_provider": "openai"}, j.key); r.status != http.StatusNoContent && r.status != http.StatusOK {
		t.Fatalf("PUT ai policy: %s", r)
	}
}

// writePrompt writes a prompt file whose system message says system.
func (j *aiPromptsJourney) writePrompt(t *testing.T, system string) string {
	t.Helper()
	path := filepath.Join(j.dir, "prompt.yaml")
	body := "name: " + j.name + "\n" +
		"target: { integration_id: " + j.integration + ", model: gpt-4o-mini }\n" +
		"defaults: { tone: friendly }\n" +
		"messages:\n" +
		"  - role: system\n    content: \"" + system + " Use a {{tone}} tone.\"\n" +
		"  - role: user\n    content: \"{{question}}\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (j *aiPromptsJourney) pushFirst(t *testing.T) {
	doc := object(t, j.tg.ok(t, time.Minute, "ai", "prompts", "push", j.writePrompt(t, "Answer briefly.")).stdout)
	if str(doc, "created") != "true" || str(doc, "version.version") != "1" || str(doc, "prompt.live_version") != "1" {
		t.Fatalf("a prompt's first push should create live v1: %s", compact(doc))
	}
}

func (j *aiPromptsJourney) pushUnchanged(t *testing.T) {
	doc := object(t, j.tg.ok(t, time.Minute, "ai", "prompts", "push", j.writePrompt(t, "Answer briefly.")).stdout)
	if str(doc, "created") != "false" || str(doc, "version.version") != "1" {
		t.Fatalf("an identical push should store nothing: %s", compact(doc))
	}
}

func (j *aiPromptsJourney) pushSecond(t *testing.T) {
	doc := object(t, j.tg.ok(t, time.Minute, "ai", "prompts", "push", j.writePrompt(t, "Answer in one sentence.")).stdout)
	if str(doc, "created") != "true" || str(doc, "version.version") != "2" || str(doc, "prompt.live_version") != "1" {
		t.Fatalf("a later push should not go live without --live: %s", compact(doc))
	}
}

func (j *aiPromptsJourney) diff(t *testing.T) {
	out := string(j.tg.ok(t, time.Minute, "ai", "prompts", "diff", j.name, "1", "2").stdout)
	if !strings.Contains(out, "-") || !strings.Contains(out, "Answer briefly.") || !strings.Contains(out, "Answer in one sentence.") {
		t.Fatalf("diff should show both system messages:\n%s", out)
	}
}

func (j *aiPromptsJourney) render(t *testing.T) {
	doc := object(t, j.tg.ok(t, time.Minute, "ai", "prompts", "render", j.name, "--var", "question=Where is my order?").stdout)
	messages, _ := field(doc, "messages").([]any)
	if str(doc, "version") != "1" || len(messages) != 2 || str(messages[0], "content") != "Answer briefly. Use a friendly tone." ||
		str(messages[1], "content") != "Where is my order?" {
		t.Fatalf("render should fill the live version's variables and defaults: %s", compact(doc))
	}
	if res := j.tg.run(t, time.Minute, "ai", "prompts", "render", j.name); res.exit == 0 {
		t.Fatal("render without a required variable should fail")
	}
}

func (j *aiPromptsJourney) promoteAndRollBack(t *testing.T) {
	doc := object(t, j.tg.ok(t, time.Minute, "ai", "prompts", "promote", j.name, "2").stdout)
	if str(doc, "live_version") != "2" || str(doc, "previous_live_version") != "1" {
		t.Fatalf("promote should make v2 live: %s", compact(doc))
	}
	doc = object(t, j.tg.ok(t, time.Minute, "ai", "prompts", "rollback", j.name).stdout)
	if str(doc, "live_version") != "1" {
		t.Fatalf("rollback should put v1 back: %s", compact(doc))
	}
}

func (j *aiPromptsJourney) deletePrompt(t *testing.T) {
	j.tg.ok(t, time.Minute, "ai", "prompts", "rm", j.name)
	if res := j.tg.run(t, time.Minute, "ai", "prompts", "get", j.name); res.exit == 0 || res.problemCode() != "not_found" {
		t.Fatalf("a deleted prompt should be gone (exit %d, %s)", res.exit, res.problemCode())
	}
}
