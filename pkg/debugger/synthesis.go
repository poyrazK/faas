// Package debugger contains customer-safe debugger projections that are
// shared by the API, dashboard, and CLI. It accepts only the redacted API
// evidence envelope so a future prose provider can be added behind the same
// bounded contract without widening the data it can see.
package debugger

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const synthesisVersion = "rules-v1"

// Synthesize builds a deterministic root-cause summary from retained
// debugger evidence. The rules intentionally report observations and safe
// next actions; they do not infer a causal chain when a stage is missing.
func Synthesize(evidence api.DebugRequestEvidenceResponse) api.DebugEvidenceExplanation {
	out := evidence.Explanation
	if out.Status == "" {
		out.Status = "unobserved"
	}
	if out.GeneratedBy == "" {
		out.GeneratedBy = synthesisVersion
	}
	if out.PrimarySpan == nil && len(evidence.Spans) > 0 {
		primary := evidence.Spans[0]
		out.PrimarySpan = &primary
	}

	findings := make([]api.DebugEvidenceFinding, 0, 6)
	recommendations := make([]api.DebugEvidenceRecommendation, 0, 5)
	refs := make([]api.DebugEvidenceRef, 0, 8)
	seenRefs := make(map[string]bool)
	seenRecommendations := make(map[string]bool)

	addRef := func(kind, label, value string) string {
		key := kind + "\x00" + value
		if !seenRefs[key] && len(refs) < 8 {
			seenRefs[key] = true
			refs = append(refs, api.DebugEvidenceRef{Kind: kind, Label: label, Value: value})
		}
		return value
	}
	addFinding := func(code, title, detail, confidence string, evidenceRefs ...string) {
		if len(findings) >= 8 {
			return
		}
		findings = append(findings, api.DebugEvidenceFinding{
			Code:         code,
			Title:        title,
			Detail:       detail,
			Confidence:   confidence,
			EvidenceRefs: evidenceRefs,
		})
	}
	addRecommendation := func(action, detail string) {
		if seenRecommendations[action] || len(recommendations) >= 5 {
			return
		}
		seenRecommendations[action] = true
		recommendations = append(recommendations, api.DebugEvidenceRecommendation{Action: action, Detail: detail})
	}

	requestRef := addRef("request", "Request outcome", "request")
	if evidence.Request.Status >= 500 {
		addFinding("request_failure", "Request returned a server error",
			fmt.Sprintf("The retained request ended with HTTP %d.", evidence.Request.Status), "high", requestRef)
		addRecommendation("inspect_guest_failure", "Inspect the guest outcome and error class before changing deployment configuration.")
	} else if evidence.Request.Status >= 400 {
		addFinding("request_client_error", "Request returned a client error",
			fmt.Sprintf("The retained request ended with HTTP %d; this is not enough evidence to call it a platform failure.", evidence.Request.Status), "medium", requestRef)
	}

	if guest := evidence.Request.Guest; guest != nil {
		guestRef := addRef("guest", "Guest execution", "guest")
		if guest.Outcome != "" && guest.Outcome != "ok" {
			detail := fmt.Sprintf("Guest execution reported outcome %q.", guest.Outcome)
			if guest.ErrorClass != "" {
				detail += fmt.Sprintf(" Error class: %s.", guest.ErrorClass)
			}
			addFinding("guest_failure", "Guest execution did not complete successfully", detail, "high", guestRef)
			addRecommendation("inspect_guest_failure", "Inspect the guest runtime error class and the corresponding application logs.")
		}
	}

	if evidence.Request.ColdBoot {
		addFinding("cold_boot", "A cold boot was observed",
			"The request was served through a cold-start path; startup time may account for part of the observed latency.", "medium", requestRef)
		addRecommendation("inspect_wake_timeline", "Inspect the wake timeline to separate boot time from guest execution time.")
	}

	regressionRef := ""
	if regression := evidence.Regression; regression != nil {
		regressionRef = addRef("regression", "Regression observation", "regression")
		detail := fmt.Sprintf("The route is at p95 %dms versus %dms baseline (%sx), affecting %d represented requests.", regression.P95MS, regression.P95BaseMS, regression.Factor, regression.AffectedCount)
		addFinding("performance_regression", "A deployment regression is active", detail, "high", regressionRef)
		addRecommendation("compare_deployments", "Compare this deployment with its previous healthy deployment before rolling back.")
	}

	if len(evidence.Spans) > 0 {
		spanRef := addRef("span", "Slowest retained span", "span:0")
		primary := evidence.Spans[0]
		detail := fmt.Sprintf("The slowest retained span is %q at %dms.", primary.Name, primary.DurationNanos/1_000_000)
		if primary.DBStatement != "" {
			detail += " A redacted database fingerprint is attached."
			addFinding("database_span", "Database work is present in the slowest span", detail, "medium", spanRef)
			addRecommendation("inspect_query_plan", "Inspect the redacted database fingerprint and query plan for the slowest span.")
		} else {
			addFinding("slowest_span", "A slow span was retained", detail, "medium", spanRef)
		}
	}

	longestStage := ""
	longestDuration := int64(0)
	incomplete := false
	for _, stage := range evidence.Correlation.Stages {
		stageRef := addRef("correlation", "Correlation stage", "correlation:"+stage.Phase)
		if stage.Status == "missing" || stage.Status == "partial" {
			incomplete = true
			detail := stage.Reason
			if detail == "" {
				detail = "This stage has no complete retained evidence."
			}
			addFinding("evidence_gap", titleStage(stage.Phase)+" evidence is incomplete", detail, "low", stageRef)
			continue
		}
		if stage.Status == "observed" && stage.DurationMS > longestDuration {
			longestStage = stage.Phase
			longestDuration = stage.DurationMS
		}
	}
	if longestStage != "" && longestStage != "edge" {
		stageRef := "correlation:" + longestStage
		addFinding("longest_stage", "The longest observed request stage was "+longestStage,
			fmt.Sprintf("The retained correlation measures %dms for this stage.", longestDuration), "medium", stageRef)
		addRecommendation("inspect_stage_timeline", "Use the correlation timeline to focus investigation on the longest observed stage.")
	}
	if incomplete {
		addRecommendation("improve_telemetry", "Enable or retain the missing debugger signal before treating this synthesis as conclusive.")
	}

	if evidence.Request.Guest != nil && evidence.Request.Guest.Outcome != "" && evidence.Request.Guest.Outcome != "ok" {
		out.Diagnosis = "request_failure"
		out.Confidence = "high"
	} else if evidence.Regression != nil {
		out.Diagnosis = "performance_regression"
		out.Confidence = "high"
	} else if evidence.Request.Status >= 500 {
		out.Diagnosis = "request_failure"
		out.Confidence = "high"
	} else if evidence.Request.ColdBoot {
		out.Diagnosis = "cold_start"
		out.Confidence = "medium"
	} else if len(evidence.Spans) > 0 || longestStage != "" {
		out.Diagnosis = "slow_path"
		out.Confidence = "medium"
	} else if incomplete {
		out.Diagnosis = "insufficient_evidence"
		out.Confidence = "low"
	} else {
		out.Diagnosis = "no_issue_observed"
		out.Confidence = "low"
	}

	switch out.Diagnosis {
	case "request_failure":
		if evidence.Request.Guest != nil && evidence.Request.Guest.Outcome != "" && evidence.Request.Guest.Outcome != "ok" {
			out.Headline = fmt.Sprintf("Guest execution reported %s for HTTP %d.", evidence.Request.Guest.Outcome, evidence.Request.Status)
		} else {
			out.Headline = fmt.Sprintf("The request returned HTTP %d; retained evidence points to a request failure.", evidence.Request.Status)
		}
	case "performance_regression":
		out.Headline = "An active deployment regression is the strongest retained signal for this request."
	case "cold_start":
		out.Headline = "Cold boot is the strongest retained contributor to this request's latency."
	case "slow_path":
		if longestStage != "" && longestStage != "edge" {
			out.Headline = fmt.Sprintf("The %s stage is the longest observed part of the request path.", longestStage)
		} else if len(evidence.Spans) > 0 {
			out.Headline = "A slow retained span is the strongest available performance signal."
		}
	case "insufficient_evidence":
		out.Headline = "No single root cause can be established from the retained debugger evidence."
	case "no_issue_observed":
		out.Headline = "No error or dominant slow path was observed in the retained debugger evidence."
	}

	out.Findings = findings
	out.Recommendations = recommendations
	out.EvidenceRefs = refs
	return out
}

func titleStage(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
