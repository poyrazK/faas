package main_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWarnTemplatedEdgeRulePaths(t *testing.T) {
	var buf bytes.Buffer
	main.WarnTemplatedEdgeRulePaths(&buf, []api.EdgeRuleResponse{
		{ID: "rule-dead", MatchPath: "/users/{id}"},
		{ID: "rule-ok", MatchPath: "/users/*"},
	})
	out := buf.String()
	if !strings.Contains(out, "rule rule-dead never matches") ||
		!strings.Contains(out, "gregale edge-rules update rule-dead --match-path '/users/?*'") {
		t.Fatalf("warning = %q", out)
	}
	if strings.Contains(out, "rule-ok") || strings.Count(out, "\n") != 1 {
		t.Fatalf("warned about a glob rule: %q", out)
	}
}
