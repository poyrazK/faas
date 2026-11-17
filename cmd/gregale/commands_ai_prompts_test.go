package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR 20261114180740: `gregale ai prompts push` reads a YAML file, `render`
// substitutes variables locally, `diff` compares two versions line by line
// and `promote` sets the live version.
func TestAIPromptsCLI(t *testing.T) {
	main.ResetJSONOut(t)
	live := 1
	versions := map[string]api.OutboundAIPromptVersion{
		"1": {Version: 1, Messages: []api.OutboundAIPromptMessage{{Role: "system", Content: "Be kind."}, {Role: "user", Content: "{{question}}"}},
			Target: api.OutboundAIPromptTarget{IntegrationID: "i", Model: "gpt-4o"}},
		"2": {Version: 2, Messages: []api.OutboundAIPromptMessage{{Role: "system", Content: "Be brief."}, {Role: "user", Content: "{{question}}"}},
			Target: api.OutboundAIPromptTarget{IntegrationID: "i", Model: "gpt-4o"}},
	}
	var pushed api.PushOutboundAIPromptVersionRequest
	var liveRequest api.PutOutboundAIPromptLiveRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := r.Method + " " + r.URL.Path; {
		case path == "POST /v1/outbound/ai-prompts/support/versions":
			if err := json.NewDecoder(r.Body).Decode(&pushed); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusCreated)
			main.WriteJSONTest(w, api.PushOutboundAIPromptVersionResponse{Created: true,
				Prompt: api.OutboundAIPrompt{Name: "support", LiveVersion: &live, LatestVersion: 2}, Version: versions["2"]})
		case path == "GET /v1/outbound/ai-prompts/support":
			main.WriteJSONTest(w, api.OutboundAIPrompt{Name: "support", LiveVersion: &live, LatestVersion: 2})
		case strings.HasPrefix(path, "GET /v1/outbound/ai-prompts/support/versions/"):
			main.WriteJSONTest(w, versions[strings.TrimPrefix(r.URL.Path, "/v1/outbound/ai-prompts/support/versions/")])
		case path == "PUT /v1/outbound/ai-prompts/support/live":
			if err := json.NewDecoder(r.Body).Decode(&liveRequest); err != nil {
				t.Error(err)
			}
			promoted, previous := 2, 1
			main.WriteJSONTest(w, api.OutboundAIPrompt{Name: "support", LiveVersion: &promoted, PreviousLiveVersion: &previous})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	var stdout bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &stdout
	defer func() { (*main.OsStdout) = oldOut }()

	file := filepath.Join(t.TempDir(), "support.yaml")
	if err := os.WriteFile(file, []byte(`name: support
messages:
  - role: system
    content: "Be brief. Tone: {{tone}}"
  - role: user
    content: "{{question}}"
defaults:
  tone: kind
target:
  integration_id: i
  model: gpt-4o
params:
  temperature: 0.2
  response_format:
    type: json_object
note: shorter answers
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := main.CmdAI([]string{"prompts", "push", file}); code != 0 {
		t.Fatalf("push exit = %d", code)
	}
	if len(pushed.Messages) != 2 || pushed.Defaults["tone"] != "kind" || pushed.Params.Temperature == nil || *pushed.Params.Temperature != 0.2 ||
		string(pushed.Params.ResponseFormat) != `{"type":"json_object"}` || pushed.Note != "shorter answers" || pushed.Live {
		t.Fatalf("pushed = %+v", pushed)
	}
	if !strings.Contains(stdout.String(), "support v2 pushed; live is still v1") {
		t.Fatalf("push output = %q", stdout.String())
	}

	stdout.Reset()
	if code := main.CmdAI([]string{"prompts", "render", "support", "--var", "question=hello"}); code != 0 {
		t.Fatalf("render exit = %d", code)
	}
	var rendered struct {
		Version  int                           `json:"version"`
		Messages []api.OutboundAIPromptMessage `json:"messages"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &rendered); err != nil || rendered.Version != 1 || rendered.Messages[1].Content != "hello" {
		t.Fatalf("render = %s %v", stdout.String(), err)
	}
	if code := main.CmdAI([]string{"prompts", "render", "support"}); code == 0 {
		t.Fatal("render without a required variable succeeded")
	}

	stdout.Reset()
	if code := main.CmdAI([]string{"prompts", "diff", "support", "1", "2"}); code != 0 {
		t.Fatalf("diff exit = %d", code)
	}
	if out := stdout.String(); !strings.Contains(out, "-") || !strings.Contains(out, "Be kind.") || !strings.Contains(out, "+") ||
		!strings.Contains(out, "Be brief.") {
		t.Fatalf("diff = %q", out)
	}

	stdout.Reset()
	if code := main.CmdAI([]string{"prompts", "promote", "support", "2"}); code != 0 || liveRequest.Version != 2 ||
		!strings.Contains(stdout.String(), "live v2 (previous v1)") {
		t.Fatalf("promote exit = %d, request %+v, output %q", code, liveRequest, stdout.String())
	}
	for _, args := range [][]string{{"promote", "support", "zero"}, {"diff", "support", "1"}, {"bogus"}} {
		if code := main.CmdAI(append([]string{"prompts"}, args...)); code == 0 {
			t.Fatalf("%v was accepted", args)
		}
	}
}
