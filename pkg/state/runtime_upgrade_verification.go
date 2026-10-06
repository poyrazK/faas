package state

import (
	"context"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Verification is a fresh observation of the explicitly reviewed gateway
// sessions, not a fleet membership, uptime, drain or automatic rollback proof.
// The historical operation phase remains complete after later traffic changes.
type RuntimeUpgradeVerification struct {
	OperationID       string    `json:"operation_id"`
	Status            string    `json:"status"`
	Reason            string    `json:"reason,omitempty"`
	CheckedAt         time.Time `json:"checked_at"`
	ValidForSeconds   int       `json:"valid_for_seconds"`
	GatewaySessions   []string  `json:"gateway_sessions"`
	ConfirmedGateways int       `json:"confirmed_gateways"`
	HealthEvaluatedAt string    `json:"health_evaluated_at,omitempty"`
}

type RuntimeUpgradeVerificationStore interface {
	VerifyRuntimeUpgrade(context.Context, string, string, []string) (RuntimeUpgradeVerification, error)
}

// Gateway receipt writes belong to gatewayd; verification belongs to private
// apid controls. Only a process that installed the supplied weights calls this.
type RuntimeUpgradeGatewayStore interface {
	RecordRuntimeUpgradeGateway(context.Context, string, string, string) error
	ListRuntimeUpgradeGatewayRepairApps(context.Context, string) ([]string, error)
	PruneExpiredRuntimeUpgradeGatewayReceipts(context.Context) error
}

type runtimeUpgradeGatewayReceipt struct {
	SessionID, DeploymentID string
	CutoverAt, InstalledAt  time.Time
}

func newRuntimeUpgradeVerification(id string, sessions []string, now time.Time) (RuntimeUpgradeVerification, error) {
	if validateRuntimeAppEnvIDs(id, id, id) != nil || len(sessions) == 0 || len(sessions) > api.RuntimeUpgradeGatewaySessionLimit {
		return RuntimeUpgradeVerification{}, ErrInvalidArgument
	}
	sorted := slices.Clone(sessions)
	for i, session := range sorted {
		parsed, err := uuid.Parse(session)
		if err != nil || parsed == uuid.Nil {
			return RuntimeUpgradeVerification{}, ErrInvalidArgument
		}
		sorted[i] = parsed.String()
	}
	slices.Sort(sorted)
	for i, session := range sorted {
		if validateRuntimeAppEnvIDs(session, session, session) != nil || (i > 0 && sorted[i-1] == session) {
			return RuntimeUpgradeVerification{}, ErrInvalidArgument
		}
	}
	return RuntimeUpgradeVerification{OperationID: id, Status: "pending", CheckedAt: now, ValidForSeconds: int(api.RuntimeUpgradeGatewayReceiptMaxAge / time.Second), GatewaySessions: sorted}, nil
}

func runtimeUpgradeActivatedDeployments(deps []Deployment, candidate, serving Deployment) bool {
	if candidate.ID == "" || serving.ID == "" || candidate.ID == serving.ID || candidate.AppID != serving.AppID ||
		candidate.Status != DeployLive || serving.Status != DeployLive || candidate.DeletedAt != nil || serving.DeletedAt != nil ||
		candidate.TrafficPercent != 100 || serving.TrafficPercent != 0 || !candidate.TrafficPercentExplicit ||
		candidate.EnvironmentWorkloadHeld() || serving.EnvironmentWorkloadHeld() || normalizedDeploymentScope(candidate.Scope) != normalizedDeploymentScope(serving.Scope) {
		return false
	}
	var live []Deployment
	for _, d := range deps {
		if d.AppID == candidate.AppID && d.Status == DeployLive && d.DeletedAt == nil {
			live = append(live, d)
		}
	}
	positive := 0
	for _, d := range ProductionRoutingDeployments(live) {
		if d.TrafficPercent > 0 {
			if d.ID != candidate.ID || d.TrafficPercent != 100 {
				return false
			}
			positive++
		}
	}
	return positive == 1 && runtimeUpgradeServingStable(deps, serving, candidate)
}

func evaluateRuntimeUpgradeVerification(out RuntimeUpgradeVerification, cutover RuntimeUpgradeCutover, scope string, receipts []runtimeUpgradeGatewayReceipt, health *api.AppHealthResponse, appID string) RuntimeUpgradeVerification {
	for _, session := range out.GatewaySessions {
		for _, receipt := range receipts {
			if receipt.SessionID == session && receipt.DeploymentID == cutover.DeploymentID && receipt.CutoverAt.Equal(cutover.CutoverAt) &&
				!receipt.InstalledAt.Before(cutover.CutoverAt) && !receipt.InstalledAt.After(out.CheckedAt) && out.CheckedAt.Sub(receipt.InstalledAt) <= api.RuntimeUpgradeGatewayReceiptMaxAge {
				out.ConfirmedGateways++
				remaining := int((api.RuntimeUpgradeGatewayReceiptMaxAge - out.CheckedAt.Sub(receipt.InstalledAt)) / time.Second)
				out.ValidForSeconds = min(out.ValidForSeconds, remaining)
				break
			}
		}
	}
	if out.ConfirmedGateways != len(out.GatewaySessions) {
		out.Reason = "gateway_confirmation_pending"
		return out
	}
	if normalizedDeploymentScope(scope) != DefaultEnvScope {
		out.Reason = "health_scope_unsupported"
		return out
	}
	if health == nil {
		out.Reason = "health_evidence_unavailable"
		return out
	}
	out.HealthEvaluatedAt = health.EvaluatedAt
	evaluated, err := time.Parse(time.RFC3339Nano, health.EvaluatedAt)
	if err != nil || evaluated.After(out.CheckedAt) || health.ValidForSeconds < 1 || health.ValidForSeconds > int(api.AppHealthEvidenceMaxAge/time.Second) || out.CheckedAt.Sub(evaluated) > time.Duration(health.ValidForSeconds)*time.Second {
		out.Reason = "health_evidence_stale"
		return out
	}
	out.ValidForSeconds = min(out.ValidForSeconds, int((time.Duration(health.ValidForSeconds)*time.Second-out.CheckedAt.Sub(evaluated))/time.Second))
	if health.AppID != appID || health.Scope != DefaultEnvScope || !slices.Equal(health.ServingDeploymentIDs, []string{cutover.DeploymentID}) || health.LatestDeploymentID != cutover.DeploymentID {
		out.Reason = "health_scope_changed"
		return out
	}
	// Persisted collection must finish inside its lease. Reserve that whole
	// span so the earliest counter query also has a post-cutover window.
	if evaluated.Before(cutover.CutoverAt.Add(api.AppHealthMetricsWindow + api.AppHealthCollectorLease)) {
		out.Reason = "post_cutover_window_pending"
		return out
	}
	if health.Status != "healthy" || !health.Capacity.Known || health.Capacity.Ready < max(1, health.Capacity.Required) || health.Capacity.Unready != 0 || health.Capacity.Unknown != 0 {
		out.Reason = "candidate_health_unconfirmed"
		return out
	}
	requests := health.Requests
	if requests == nil || !requests.Known || requests.Coverage != "serving_deployments" || requests.WindowSeconds != int(api.AppHealthMetricsWindow/time.Second) || !slices.Equal(requests.DeploymentIDs, []string{cutover.DeploymentID}) {
		out.Reason = "request_evidence_unavailable"
		return out
	}
	asOf, err := time.Parse(time.RFC3339Nano, health.MetricsAsOf)
	if err != nil || asOf.After(evaluated) || out.CheckedAt.Sub(asOf) > api.AppHealthEvidenceMaxAge || asOf.Before(cutover.CutoverAt.Add(api.AppHealthMetricsWindow)) {
		out.Reason = "post_cutover_metrics_pending"
		return out
	}
	out.ValidForSeconds = min(out.ValidForSeconds, int((api.AppHealthEvidenceMaxAge-out.CheckedAt.Sub(asOf))/time.Second))
	if requests.RequestCount < api.AppHealthMinRequests {
		out.Reason = "request_volume_insufficient"
		return out
	}
	if requests.ServerErrors < 0 || requests.ServerErrors > requests.RequestCount || math.IsNaN(requests.ErrorRatePct) || math.IsInf(requests.ErrorRatePct, 0) || requests.ErrorRatePct < 0 || requests.ErrorRatePct > 100 {
		out.Reason = "request_evidence_invalid"
		return out
	}
	// Prometheus increase is fractional. The producer truncates counts while
	// retaining its unrounded rate; validate its integer-truncation interval.
	count, failures := float64(requests.RequestCount), float64(requests.ServerErrors)
	minimumRate := failures / (count + 1) * 100
	maximumRate := min(100, (failures+1)/count*100)
	if requests.ErrorRatePct < minimumRate-1e-9 || requests.ErrorRatePct > maximumRate+1e-9 {
		out.Reason = "request_evidence_invalid"
		return out
	}
	if requests.ErrorRatePct >= api.AppHealthWarningErrorRatePct {
		out.Reason = "request_error_rate_elevated"
		return out
	}
	if out.ValidForSeconds < 1 {
		out.Reason = "evidence_expiring"
		return out
	}
	out.Status, out.Reason = "verified", ""
	return out
}
