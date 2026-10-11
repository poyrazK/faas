package state

import "github.com/onebox-faas/faas/pkg/api"

// Build from the same checked observation, without another workflow read or decision.
func readinessHandoff(check api.OperationWorkflowReadinessNotificationCheck) api.OperationWorkflowReadinessHandoff {
	h := api.OperationWorkflowReadinessHandoff{Summary: "Current workflow evidence is unavailable.", Limitations: []string{
		"Advisory application reports only; the application owns decisions, authorization and execution.",
		"Comparison starts at the saved notification observation, not at follow-up scheduling. Scheduling-time evidence was not retained.",
	}}
	if saved := readinessNotificationBaseline(check.Notification); saved != nil {
		t := saved.EvaluatedAt
		h.BaselineEvaluatedAt = &t
	}
	if check.Change == nil {
		return h
	}
	c := check.Change
	h.Comparable = c.Current != nil && (check.Status == "ready" || check.Status == "blocked")
	h.Summary = c.Explanation
	if !h.Comparable {
		h.Limitations = append(h.Limitations, "Current evidence crosses a comparison boundary: "+check.Status+". Saved requirements must not be treated as current.")
		return h
	}
	if !c.EvidenceComparisonComplete {
		h.Limitations = append(h.Limitations, "Detailed evidence comparison is incomplete; absent facts do not establish resolution.")
	}
	if c.Current.PendingVerificationsTruncated {
		h.Limitations = append(h.Limitations, "Pending verification details are truncated; consult the workflow evidence view.")
	}
	if c.Current.MissingReportCount > 0 || c.Current.OverdueReportCount > 0 || (c.Current.ReportingStatus != "" && c.Current.ReportingStatus != "compared" && c.Current.ReportingStatus != "unconfigured") {
		h.Limitations = append(h.Limitations, "Application reporting status: "+c.Current.ReportingStatus+". Consult reporting advisories before relying on this observation.")
	}
	return h
}
