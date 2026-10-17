package mcpmanagement

import "github.com/onebox-faas/faas/pkg/api"

type requestRegressionView struct {
	Status             string          `json:"status"`
	Basis              string          `json:"basis"`
	Observation        *regressionView `json:"observation,omitempty"`
	TextTruncated      bool            `json:"text_truncated"`
	SourceCompleteness string          `json:"source_completeness"`
	Limitations        string          `json:"limitations"`
}

func (m *management) projectRequestRegression(e api.DebugRequestEvidenceResponse, scope string) requestRegressionView {
	v := requestRegressionView{Status: "unknown", Basis: "native_route_deployment_observation", SourceCompleteness: "unknown",
		Limitations: "A native-visible detector observation matched to the retained request's exact route and deployment, not an individual request measurement or causal attribution. Detection timestamps are not sampling-window boundaries; no baseline deployment ID or exact observation window is supplied. Workflow state is not health; native visibility is authoritative, without local clock reinterpretation. A missing observation does not prove healthy traffic. Suspected dependencies are hypotheses; query text is excluded. Reads are sequential, not an atomic snapshot. No triage changes, comparison execution, rollback, replay, collection or additional permissions occur."}
	r := e.Request
	if r.ID == "" || r.EvidenceStatus == "request_id_only" || r.Count <= 0 || r.DeploymentID == "" {
		v.Status = "unavailable"
		return v
	}
	if e.Regression == nil {
		switch e.Explanation.Status {
		case "unobserved":
			v.Status = "no_matching_observation"
		case "regression_unavailable":
			v.Status = "unavailable"
		case "regression_detected":
			v.Status = "invalid_native_regression"
		}
		return v
	}
	n := *e.Regression
	v.Status = "invalid_native_regression"
	// Match original native labels before redaction/truncation can make
	// distinct routes look identical. The request deployment is already
	// ownership-checked and rechecked before releasing the response.
	if e.Explanation.Status != "regression_detected" || n.DeploymentID != r.DeploymentID || n.Route == "" || n.Route != r.Route {
		return v
	}
	if n.Kind != "latency" && n.Kind != "errors" && n.Kind != "latency_and_errors" {
		return v
	}
	if n.State != "active" && n.State != "acknowledged" && n.State != "dismissed" {
		return v
	}
	observation, err := m.projectRegression(n, scope, &v.TextTruncated)
	if err != nil {
		v.TextTruncated = false
		return v
	}
	v.Observation, v.Status = &observation, "observed"
	return v
}

const requestRegressionGuidance = "Inspect regression.status separately from request/guest outcomes. An observed regression is the native detector observation matched to this request's exact route and deployment, not proof this individual request regressed or that a deployment caused it. Preserve baseline/current metrics, decimal factor, affected/error counts, detection timestamps, workflow state and hypothesis basis. Detection timestamps are not sampling-window boundaries; the native response supplies neither a baseline deployment ID nor exact observation window/completeness. Native visibility is authoritative; do not reinterpret dismissals using your clock. no_matching_observation, unavailable, unknown and invalid_native_regression do not prove health. Regression findings cite /regression/observation in this response. A compare_deployments suggestion requires two explicit deployment IDs supplied by the user; never infer latest/previous or healthy baseline, and never trigger comparison or rollback automatically. A modified route label is not an exact filter. Diagnostic consent grants no triage, replay, collection, additional logs or writes."
