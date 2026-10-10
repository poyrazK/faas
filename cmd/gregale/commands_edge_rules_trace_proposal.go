package main

// `gregale edge-rules trace --proposal/--add-rule/--remove-rule`: simulate a
// request against the app's current rules and against a draft change, and
// show what the change would do to that request before it is applied.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

func buildEdgeRuleTraceProposal(proposalFile string, addRules, removeRules []string) (edgeruletrace.Proposal, error) {
	var proposal edgeruletrace.Proposal
	if proposalFile != "" {
		raw, err := readEdgeRuleTraceConfig(proposalFile)
		if err != nil {
			return proposal, err
		}
		if proposal, err = edgeruletrace.ParseProposal(raw); err != nil {
			return proposal, err
		}
	}
	for _, value := range addRules {
		raw := []byte(value)
		if strings.HasPrefix(value, "@") {
			var err error
			if raw, err = readEdgeRuleTraceConfig(value[1:]); err != nil {
				return proposal, fmt.Errorf("--add-rule %s: %w", value, err)
			}
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var req api.CreateEdgeRuleRequest
		if err := dec.Decode(&req); err != nil {
			return proposal, fmt.Errorf("--add-rule: %w", err)
		}
		proposal.Add = append(proposal.Add, req)
	}
	for _, id := range removeRules {
		if id = strings.TrimSpace(id); id == "" {
			return proposal, fmt.Errorf("--remove-rule requires a rule id")
		}
		proposal.Remove = append(proposal.Remove, id)
	}
	return proposal, nil
}

func renderEdgeRuleTraceComparison(cmp edgeruletrace.Comparison) {
	r := cmp.Current
	_, _ = fmt.Fprintf(osStdout, "%s %s%s (app %s) — proposed change\n", r.Method, r.Host, r.Path, r.App)
	_, _ = fmt.Fprintf(osStdout, "  current:  %s\n", edgeRuleTraceOutcomeLine(cmp.Current.Simulation))
	_, _ = fmt.Fprintf(osStdout, "  proposed: %s\n", edgeRuleTraceOutcomeLine(cmp.Proposed.Simulation))
	if !cmp.Changed {
		_, _ = fmt.Fprintln(osStdout, "No change for this request.")
	} else {
		_, _ = fmt.Fprintln(osStdout, "Changes for this request:")
		for _, diff := range cmp.Differences {
			_, _ = fmt.Fprintf(osStdout, "  - %s\n", diff)
		}
	}
	_, _ = fmt.Fprintln(osStdout, "Evaluated locally; the server applies full per-kind and plan validation when the change is made.")
}

func edgeRuleTraceOutcomeLine(sim edgeruletrace.Simulation) string {
	line := sim.Outcome
	if sim.StatusCode != 0 {
		line += fmt.Sprintf(" (HTTP %d)", sim.StatusCode)
	}
	if sim.StoppedAt != "" {
		line += " at " + sim.StoppedAt
	}
	if sim.Location != "" {
		line += " → " + sim.Location
	}
	if sim.TargetApp != "" {
		line += " → app " + sim.TargetApp
	}
	return line
}
