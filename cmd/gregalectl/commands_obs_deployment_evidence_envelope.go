package main

import "encoding/json"

func fitObsDeploymentEvidenceHistoryWithin(report obsDeploymentReport, limit int) obsDeploymentReport {
	if report.QualificationEvidence == nil || report.QualificationEvidence.History == nil {
		return report
	}
	evidence := *report.QualificationEvidence
	history := *evidence.History
	for len(history.Events) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			return report
		}
		history.Events = history.Events[1:]
		history.EnvelopeOmittedEvents++
		history.account()
		evidence.History = &history
		report.QualificationEvidence = &evidence
	}
	return report
}
