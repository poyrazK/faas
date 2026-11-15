package api

// adr: 20261114180740 — prompt templates substitute variables as plain
// text, report the first missing variable, and pushes are validated.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderOutboundAIPrompt(t *testing.T) {
	messages := []OutboundAIPromptMessage{
		{Role: OutboundAIPromptRoleSystem, Content: "Tone: {{ tone }}. Literal {{not a var}} and {{ }}."},
		{Role: OutboundAIPromptRoleUser, Content: "{{question}} / {{question}}"},
	}
	if got := OutboundAIPromptVariables(messages); strings.Join(got, ",") != "question,tone" {
		t.Fatalf("variables = %v", got)
	}
	rendered, missing := RenderOutboundAIPrompt(messages, map[string]string{"tone": "kind"}, map[string]string{"question": "{{tone}}?"})
	if missing != "" || rendered[0].Content != "Tone: kind. Literal {{not a var}} and {{ }}." || rendered[1].Content != "{{tone}}? / {{tone}}?" {
		t.Fatalf("rendered = %+v missing %q", rendered, missing)
	}
	if _, missing := RenderOutboundAIPrompt(messages, nil, map[string]string{"question": "q"}); missing != "tone" {
		t.Fatalf("missing = %q", missing)
	}
}

func TestPushOutboundAIPromptVersionProblem(t *testing.T) {
	valid := func() PushOutboundAIPromptVersionRequest {
		return PushOutboundAIPromptVersionRequest{
			Messages: []OutboundAIPromptMessage{{Role: "user", Content: "{{q}}"}},
			Target:   OutboundAIPromptTarget{IntegrationID: "i", Model: "gpt-4o"},
		}
	}
	temperature, maxTokens := 3.0, int64(0)
	for name, tc := range map[string]struct {
		edit func(*PushOutboundAIPromptVersionRequest)
		want string
	}{
		"valid":         {func(*PushOutboundAIPromptVersionRequest) {}, ""},
		"bad role":      {func(r *PushOutboundAIPromptVersionRequest) { r.Messages[0].Role = "tool" }, "role must be"},
		"empty content": {func(r *PushOutboundAIPromptVersionRequest) { r.Messages[0].Content = " " }, "must not be empty"},
		"both targets":  {func(r *PushOutboundAIPromptVersionRequest) { r.Target.RouteID = "r" }, "exactly one"},
		"no model":      {func(r *PushOutboundAIPromptVersionRequest) { r.Target.Model = "" }, "needs a model"},
		"route with model": {func(r *PushOutboundAIPromptVersionRequest) {
			r.Target = OutboundAIPromptTarget{RouteID: "r", Model: "m"}
		}, ""},
		"unused default":    {func(r *PushOutboundAIPromptVersionRequest) { r.Defaults = map[string]string{"x": "1"} }, "names no"},
		"temperature":       {func(r *PushOutboundAIPromptVersionRequest) { r.Params.Temperature = &temperature }, "temperature"},
		"max tokens":        {func(r *PushOutboundAIPromptVersionRequest) { r.Params.MaxTokens = &maxTokens }, "max_tokens"},
		"format not object": {func(r *PushOutboundAIPromptVersionRequest) { r.Params.ResponseFormat = json.RawMessage(`[1]`) }, "JSON object"},
		"too large":         {func(r *PushOutboundAIPromptVersionRequest) { r.Messages[0].Content = strings.Repeat("x", 70<<10) }, "at most"},
	} {
		request := valid()
		tc.edit(&request)
		got := request.Problem()
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: problem = %q, want %q", name, got, tc.want)
		}
	}
}

func TestSameOutboundAIPromptVersionIgnoresEmptyCollections(t *testing.T) {
	a := PushOutboundAIPromptVersionRequest{Messages: []OutboundAIPromptMessage{{Role: "user", Content: "x"}},
		Params: OutboundAIPromptParams{ResponseFormat: json.RawMessage(`{ "type": "json_object" }`)}}
	b := a
	b.Defaults, b.Params.Stop = map[string]string{}, []string{}
	b.Params.ResponseFormat = json.RawMessage(`{"type":"json_object"}`)
	if !SameOutboundAIPromptVersion(a, b) {
		t.Fatal("equal versions compare different")
	}
	b.Note = "changed"
	if SameOutboundAIPromptVersion(a, b) {
		t.Fatal("different notes compare equal")
	}
}
