package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildEdgeRuleTraceProposalMergesFileAndFlags(t *testing.T) {
	dir := t.TempDir()
	proposalPath := filepath.Join(dir, "proposal.json")
	if err := os.WriteFile(proposalPath, []byte(`{"remove":["r1"],"update":{"r2":{"priority":5}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rulePath := filepath.Join(dir, "rule.json")
	if err := os.WriteFile(rulePath, []byte(`{"kind":"redirect","match_host":"a.example.com","action":{"redirect":{"to":"/x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := buildEdgeRuleTraceProposal(proposalPath,
		[]string{`{"kind":"maintenance","match_host":"b.example.com","action":{"maintenance":{"message":"m"}}}`, "@" + rulePath},
		[]string{"r3"})
	if err != nil {
		t.Fatalf("buildEdgeRuleTraceProposal: %v", err)
	}
	if len(p.Add) != 2 || p.Add[1].Kind != "redirect" || len(p.Remove) != 2 || p.Remove[1] != "r3" || *p.Update["r2"].Priority != 5 {
		t.Fatalf("proposal = %+v", p)
	}
	if _, err := buildEdgeRuleTraceProposal("", []string{`{"kind":"redirect","bogus":1}`}, nil); err == nil {
		t.Fatal("unknown field in --add-rule was accepted")
	}
	if _, err := buildEdgeRuleTraceProposal("", nil, []string{"  "}); err == nil {
		t.Fatal("blank --remove-rule was accepted")
	}
}
