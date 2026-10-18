package mcpmanagement

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func projectRequestRegressionFinding(e requestEvidenceView, f api.DebugEvidenceFinding, v requestFindingView) (requestFindingView, bool) {
	r := e.Regression.Observation
	if f.EvidenceRefs[0] != "regression" || e.Regression.Status != "observed" || r == nil {
		return v, false
	}
	switch f.Code {
	case "error_regression":
		if (r.Kind != "errors" && r.Kind != "latency_and_errors") || r.ErrorCount <= 0 || r.ErrorRatePct <= r.ErrorRateBasePct {
			return v, false
		}
		v.Title = "Detector observed a higher route error rate"
		v.Detail = fmt.Sprintf("The matched observation reports error rate %.1f%% versus %.1f%% baseline, with %d failed requests. These are detector metrics, not an individual request measurement or causal proof.", r.ErrorRatePct, r.ErrorRateBasePct, r.ErrorCount)
	case "performance_regression":
		if (r.Kind != "latency" && r.Kind != "latency_and_errors") || r.AffectedCount <= 0 || r.P95MS <= r.P95BaseMS {
			return v, false
		}
		v.Title = "Detector observed higher route latency"
		v.Detail = fmt.Sprintf("The matched observation reports p95 %dms versus %dms baseline, factor %s, affecting %d represented requests. These are detector metrics, not an individual request measurement or causal proof.", r.P95MS, r.P95BaseMS, r.Factor, r.AffectedCount)
	default:
		return v, false
	}
	v.EvidenceRefs, v.Approximate = []string{"/regression/observation"}, true
	return v, true
}
