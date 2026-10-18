package mcpmanagement

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func projectRequestFinding(e requestEvidenceView, f api.DebugEvidenceFinding) (requestFindingView, bool) {
	v := requestFindingView{Code: f.Code, Confidence: "unknown", EvidenceRefs: []string{}}
	// Each supported native rule currently refers to exactly one signal. Do
	// not silently drop extra support and retain the rule's confidence label.
	if len(f.EvidenceRefs) != api.MCPRequestExplanationRefsMax {
		return v, false
	}
	switch f.Confidence {
	case "high", "medium", "low":
		v.Confidence = f.Confidence
	}
	switch f.Code {
	case "performance_regression", "error_regression":
		return projectRequestRegressionFinding(e, f, v)
	case "guest_failure":
		g := e.Request.Guest
		if f.EvidenceRefs[0] != "guest" || e.Request.Count == nil || *e.Request.Count <= 0 || g.Status != "observed" || g.DurationMS == nil || g.Outcome == "ok" {
			return v, false
		}
		switch g.Outcome {
		case "http_error", "handler_error", "timeout", "canceled":
		default:
			return v, false
		}
		v.Title = "Runner reported an unsuccessful execution"
		v.Detail = fmt.Sprintf("The retained runner outcome is %s with error class %s. This is separate from the HTTP outcome and does not establish root cause.", g.Outcome, g.ErrorClass)
		v.EvidenceRefs, v.Approximate = []string{"/request/guest"}, *e.Request.Count > 1
	case "request_failure", "request_client_error", "cold_boot":
		if f.EvidenceRefs[0] != "request" || e.Request.Count == nil || *e.Request.Count <= 0 {
			return v, false
		}
		v.EvidenceRefs = []string{"/request"}
		v.Approximate = *e.Request.Count > 1
		switch f.Code {
		case "request_failure":
			if e.Request.Status < 500 || e.Request.Status > 599 {
				return v, false
			}
			v.Title = "Retained request returned a server error"
			v.Detail = fmt.Sprintf("The retained request ended with HTTP %d. This outcome alone does not identify the cause.", e.Request.Status)
		case "request_client_error":
			if e.Request.Status < 400 || e.Request.Status > 499 {
				return v, false
			}
			v.Title = "Retained request returned a client error"
			v.Detail = fmt.Sprintf("The retained request ended with HTTP %d. This alone does not establish a platform failure.", e.Request.Status)
		case "cold_boot":
			if e.Request.ColdBoot == nil || !*e.Request.ColdBoot {
				return v, false
			}
			v.Title = "A cold-start path was observed"
			v.Detail = "A cold boot was recorded; this flag does not establish its share of total request latency."
		}
	case "evidence_gap", "longest_stage":
		stage, index, ok := explanationStage(e, f.EvidenceRefs[0])
		if !ok {
			return v, false
		}
		v.EvidenceRefs = []string{fmt.Sprintf("/correlation/stages/%d", index)}
		v.Approximate = stage.Approximate
		if f.Code == "evidence_gap" {
			if stage.Status != "missing" && stage.Status != "partial" {
				return v, false
			}
			v.Title = "Retained stage evidence is incomplete"
			v.Detail = fmt.Sprintf("The retained %s stage is %s. Missing evidence does not establish whether execution succeeded.", stage.Phase, stage.Status)
		} else {
			if !explanationLongestStage(e, stage) {
				return v, false
			}
			v.Title = "Stage has the longest observed duration"
			v.Detail = fmt.Sprintf("The retained %s stage measures %dms. Stage durations may overlap and do not establish root cause.", stage.Phase, stage.DurationMS)
		}
	default:
		return v, false
	}
	return v, true
}

func explanationStage(e requestEvidenceView, reference string) (api.DebugRequestCorrelationStage, int, bool) {
	if e.CorrelationTruncated {
		return api.DebugRequestCorrelationStage{}, 0, false
	}
	phase, ok := strings.CutPrefix(reference, "correlation:")
	if !ok {
		return api.DebugRequestCorrelationStage{}, 0, false
	}
	switch phase {
	case "edge", "queue", "wake", "guest", "downstream", "billing":
	default:
		return api.DebugRequestCorrelationStage{}, 0, false
	}
	index := -1
	for i, stage := range e.Correlation.Stages {
		if stage.Phase == phase {
			if index != -1 {
				return api.DebugRequestCorrelationStage{}, 0, false // Ambiguous native phase.
			}
			index = i
		}
	}
	if index == -1 {
		return api.DebugRequestCorrelationStage{}, 0, false
	}
	return e.Correlation.Stages[index], index, true
}

func explanationLongestStage(e requestEvidenceView, selected api.DebugRequestCorrelationStage) bool {
	if e.CorrelationTruncated || selected.Phase == "edge" || selected.Status != "observed" || selected.DurationMS <= 0 {
		return false
	}
	for _, s := range e.Correlation.Stages {
		if s.DurationMS < 0 || s.Status == "observed" && s.DurationMS > selected.DurationMS {
			return false
		}
	}
	return true
}
