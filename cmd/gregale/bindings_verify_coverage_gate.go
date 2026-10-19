package main

import "fmt"

// Only strict batch verification emits this gate. Coverage describes which
// probes ran; Passed additionally requires successful outcomes and usable metadata.
type allBindingCoverageGate struct {
	Required bool                        `json:"required"`
	Passed   bool                        `json:"passed"`
	Blockers []allBindingCoverageBlocker `json:"blockers"`
}

type allBindingCoverageBlocker struct {
	Code    string `json:"code"`
	Type    string `json:"type,omitempty"`
	Name    string `json:"name,omitempty"`
	Binding string `json:"binding,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func evaluateAllBindingCoverageGate(batch serviceBindingProbeBatchReport, inventoryComplete bool) *allBindingCoverageGate {
	return evaluateAllBindingCoverage(batch, inventoryComplete, "passed")
}

// Planning accepts only planned steps; execution accepts only passed probes.
func evaluateAllBindingCoverage(batch serviceBindingProbeBatchReport, inventoryComplete bool, successStatus string) *allBindingCoverageGate {
	gate := &allBindingCoverageGate{Required: true, Blockers: []allBindingCoverageBlocker{}}
	if !inventoryComplete {
		gate.Blockers = append(gate.Blockers, allBindingCoverageBlocker{Code: "inventory_incomplete"})
	}
	for _, issue := range batch.Issues {
		if issue.Severity != "warning" {
			gate.Blockers = append(gate.Blockers, allBindingCoverageBlocker{Code: "inventory_issue", Type: issue.Type, Reason: issue.Code})
		}
	}
	eligible := 0
	for _, item := range batch.Bindings {
		if item.Status == "skipped" && (item.Reason == "binding_disabled" || item.Reason == "outside_deployment_scope" || item.Reason == "release_only") {
			continue
		}
		eligible++
		code := ""
		if item.Status == successStatus {
			continue
		}
		switch item.Status {
		case "failed":
			code = "probe_failed"
		case "not_checked":
			code = "probe_not_checked"
		case "unsupported":
			code = item.Reason
			if code == "" {
				code = "probe_unsupported"
			}
		case "skipped":
			code = "unexpected_probe_exclusion"
		default:
			code = "unrecognized_probe_status"
		}
		gate.Blockers = append(gate.Blockers, allBindingCoverageBlocker{Code: code, Type: item.Type, Name: item.Name, Binding: item.Binding, Scope: item.Scope, Reason: item.Reason})
	}
	if eligible == 0 {
		gate.Blockers = append(gate.Blockers, allBindingCoverageBlocker{Code: "no_eligible_bindings"})
	}
	gate.Passed = len(gate.Blockers) == 0
	return gate
}

func renderAllBindingCoverageGate(gate *allBindingCoverageGate) {
	status := "failed"
	if gate.Passed {
		status = "passed"
	}
	_, _ = fmt.Fprintf(osStdout, "Complete coverage gate: %s\n", status)
	for _, blocker := range gate.Blockers {
		_, _ = fmt.Fprintf(osStdout, "  %s", blocker.Code)
		if blocker.Type != "" {
			_, _ = fmt.Fprintf(osStdout, " %s", blocker.Type)
		}
		if blocker.Name != "" {
			_, _ = fmt.Fprintf(osStdout, " %s", blocker.Name)
		}
		if blocker.Binding != "" {
			_, _ = fmt.Fprintf(osStdout, " (%s)", blocker.Binding)
		}
		if blocker.Scope != "" {
			_, _ = fmt.Fprintf(osStdout, " scope=%s", blocker.Scope)
		}
		if blocker.Reason != "" && blocker.Reason != blocker.Code {
			_, _ = fmt.Fprintf(osStdout, " reason=%s", blocker.Reason)
		}
		_, _ = fmt.Fprintln(osStdout)
	}
}
