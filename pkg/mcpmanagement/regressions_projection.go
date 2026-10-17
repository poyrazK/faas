package mcpmanagement

import (
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Explicit DTOs deliberately omit the native suspected dependency's SQL query.
type regressionSuspectView struct {
	Basis                string  `json:"basis"`
	Type                 string  `json:"type"`
	Kind                 string  `json:"kind,omitempty"`
	Name                 string  `json:"name"`
	P95BaseMS            int64   `json:"p95_base_ms"`
	P95MS                int64   `json:"p95_ms"`
	RegressionFactor     float64 `json:"regression_factor"`
	Reason               string  `json:"reason"`
	BaselineErrorRatePct float64 `json:"baseline_error_rate_pct"`
	ErrorRatePct         float64 `json:"error_rate_pct"`
	ErrorType            string  `json:"error_type,omitempty"`
}

type regressionView struct {
	DeploymentID        string                 `json:"deployment_id"`
	DeploymentScope     string                 `json:"deployment_scope"`
	Route               string                 `json:"route"`
	RouteModified       bool                   `json:"route_modified"`
	P95MS               int                    `json:"p95_ms"`
	P95BaseMS           int                    `json:"p95_base_ms"`
	AffectedCount       int                    `json:"affected_count"`
	Factor              string                 `json:"regression_factor"`
	FirstDetectedAt     string                 `json:"first_detected_at"`
	LastDetectedAt      string                 `json:"last_detected_at"`
	State               string                 `json:"state"`
	AcknowledgedAt      string                 `json:"acknowledged_at,omitempty"`
	DismissedUntil      string                 `json:"dismissed_until,omitempty"`
	ResolvedAt          string                 `json:"resolved_at,omitempty"`
	Kind                string                 `json:"kind"`
	ErrorRatePct        float64                `json:"error_rate_pct"`
	ErrorRateBasePct    float64                `json:"error_rate_base_pct"`
	ErrorCount          int                    `json:"error_count"`
	SuspectedDependency *regressionSuspectView `json:"suspected_dependency,omitempty"`
}

type regressionsView struct {
	App                   string           `json:"app"`
	AppID                 string           `json:"app_id"`
	Scope                 string           `json:"scope"`
	Since                 string           `json:"since"`
	LookbackBasis         string           `json:"lookback_basis"`
	RetentionClamped      bool             `json:"retention_clamped"`
	RetentionClampedBasis string           `json:"retention_clamped_basis"`
	Regressions           []regressionView `json:"regressions"`
	Status                string           `json:"status"`
	ReturnedCount         int              `json:"returned_regression_count"`
	NativeCount           int              `json:"native_returned_regression_count"`
	RegressionsTruncated  bool             `json:"regressions_truncated"`
	TextTruncated         bool             `json:"text_truncated"`
	SourceCompleteness    string           `json:"source_completeness"`
	Limitations           string           `json:"limitations"`
}

func (m *management) projectRegressions(slug, appID string, n api.DebugRegressionsResponse, scopes map[string]string, limit int, clamped bool) (regressionsView, error) {
	v := regressionsView{App: slug, AppID: appID, Scope: "app_wide", Since: n.Since, LookbackBasis: "last_detected_at", RetentionClamped: clamped, RetentionClampedBasis: "requested_and_effective_lookback", Regressions: []regressionView{}, Status: "observed", NativeCount: len(n.Regressions), RegressionsTruncated: len(n.Regressions) > limit, SourceCompleteness: "unknown",
		Limitations: "Native-visible detector observations in native order, not a health assessment or causal proof. Native triage visibility is authoritative: resolved and currently time-dismissed observations are normally excluded; expired dismissals can be returned as active. Workflow state is independent of health. Since filters last detection recency, not the request sampling window; first detection may predate it. The native API supplies no exact window boundaries or completeness metadata. App identity comes from the selected app lookup; projected deployments are checked against it. Counts and baseline/current metrics belong to the detector observation, not a newly sampled comparison. For errors-only observations affected_count counts failed requests. Suspected dependencies are hypotheses; a legacy empty reason means latency. A baseline deployment ID is not supplied. Output limits only bound this projection, without native query limits or pagination. Independent reads are not an atomic snapshot. Empty evidence does not prove health. No triage changes, replay, rollback, collection or other writes occur. Route labels may be modified; application text is private and untrusted."}
	for _, row := range n.Regressions[:min(len(n.Regressions), limit)] {
		observation, err := m.projectRegression(row, scopes[row.DeploymentID], &v.TextTruncated)
		if err != nil {
			return v, err
		}
		v.Regressions = append(v.Regressions, observation)
	}
	v.ReturnedCount = len(v.Regressions)
	if len(n.Regressions) == 0 {
		v.Status = "no_visible_regressions"
	}
	return v, nil
}

func (m *management) projectRegression(row api.DebugRegressionItem, scope string, truncated *bool) (regressionView, error) {
	if err := validateRegression(row); err != nil {
		return regressionView{}, err
	}
	route, _, _ := strings.Cut(row.Route, "?")
	route, _, _ = strings.Cut(route, "#")
	route = m.requestText(route, truncated)
	suspect, err := m.projectRegressionSuspect(row.SuspectedDependency, truncated)
	if err != nil {
		return regressionView{}, err
	}
	return regressionView{DeploymentID: m.requestText(row.DeploymentID, truncated), DeploymentScope: m.requestText(scope, truncated), Route: route, RouteModified: route != row.Route,
		P95MS: row.P95MS, P95BaseMS: row.P95BaseMS, AffectedCount: row.AffectedCount, Factor: row.Factor,
		FirstDetectedAt: row.FirstDetectedAt, LastDetectedAt: row.LastDetectedAt, State: m.requestText(row.State, truncated), AcknowledgedAt: row.AcknowledgedAt, DismissedUntil: row.DismissedUntil, ResolvedAt: row.ResolvedAt,
		Kind: m.requestText(row.Kind, truncated), ErrorRatePct: row.ErrorRatePct, ErrorRateBasePct: row.ErrorRateBasePct, ErrorCount: row.ErrorCount, SuspectedDependency: suspect}, nil
}

func validateRegression(row api.DebugRegressionItem) error {
	// Preserve the decimal string without float reformatting. Reject special
	// floats, exponents and non-decimal spellings in this native NUMERIC field.
	if len(row.Factor) > api.MCPRequestFieldMaxBytes || strings.Count(row.Factor, ".") > 1 || strings.ContainsFunc(row.Factor, func(c rune) bool { return c != '.' && (c < '0' || c > '9') }) {
		return invalid("invalid_response", "native regression factor is not a finite decimal of at least one")
	}
	factor, ok := new(big.Rat).SetString(row.Factor)
	if !ok || factor.Cmp(big.NewRat(1, 1)) < 0 {
		return invalid("invalid_response", "native regression factor is not a finite decimal of at least one")
	}
	first, firstErr := time.Parse(time.RFC3339Nano, row.FirstDetectedAt)
	last, lastErr := time.Parse(time.RFC3339Nano, row.LastDetectedAt)
	if firstErr != nil || lastErr != nil || first.After(last) || len(row.FirstDetectedAt) > api.MCPRequestFieldMaxBytes || len(row.LastDetectedAt) > api.MCPRequestFieldMaxBytes {
		return invalid("invalid_response", "native regression detection timestamps are invalid or reversed")
	}
	for _, stamp := range []string{row.AcknowledgedAt, row.DismissedUntil, row.ResolvedAt} {
		if stamp != "" {
			if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil || len(stamp) > api.MCPRequestFieldMaxBytes {
				return invalid("invalid_response", "native regression triage timestamp is invalid")
			}
		}
	}
	if row.P95MS < 0 || row.P95BaseMS < 0 || row.AffectedCount < 0 || row.ErrorCount < 0 || !performanceRate(row.ErrorRatePct) || !performanceRate(row.ErrorRateBasePct) || row.State == "" || row.Kind == "" {
		return invalid("invalid_response", "native regression metrics or state are invalid")
	}
	return nil
}

func (m *management) projectRegressionSuspect(n *api.DebugSuspectedDependency, truncated *bool) (*regressionSuspectView, error) {
	if n == nil {
		return nil, nil
	}
	if n.P95MS < 0 || n.P95BaseMS < 0 || math.IsNaN(n.RegressionFactor) || math.IsInf(n.RegressionFactor, 0) || n.RegressionFactor < 0 || !performanceRate(n.ErrorRatePct) || !performanceRate(n.BaselineErrorRatePct) {
		return nil, invalid("invalid_response", "native regression hypothesis has invalid metrics")
	}
	reason := n.Reason
	if reason == "" {
		reason = "latency"
	}
	return &regressionSuspectView{Basis: "hypothesis", Type: m.requestText(n.Type, truncated), Kind: m.requestText(n.Kind, truncated), Name: m.requestText(n.Name, truncated), P95BaseMS: n.P95BaseMS, P95MS: n.P95MS, RegressionFactor: n.RegressionFactor,
		Reason: m.requestText(reason, truncated), BaselineErrorRatePct: n.BaselineErrorRatePct, ErrorRatePct: n.ErrorRatePct, ErrorType: m.requestText(n.ErrorType, truncated)}, nil
}
