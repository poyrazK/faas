package state

// adr: 693
// adr: 694

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRuntimeUpgradeVerificationRequiresFreshScopedPostCutoverEvidence(t *testing.T) {
	now := time.Now().UTC()
	app, id, session := uuid.NewString(), uuid.NewString(), uuid.NewString()
	cutover := RuntimeUpgradeCutover{DeploymentID: uuid.NewString(), CutoverAt: now.Add(-6 * time.Minute)}
	tests := []struct {
		name, reason string
		edit         func(*RuntimeUpgradeVerification, *RuntimeUpgradeCutover, *string, *[]runtimeUpgradeGatewayReceipt, *api.AppHealthResponse)
	}{
		{"healthy", "", nil},
		{"fractional_prometheus_counts", "", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.Requests.ServerErrors = 2
			h.Requests.ErrorRatePct = 2.9 / 100.8 * 100
		}},
		{"missing_gateway", "gateway_confirmation_pending", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, r *[]runtimeUpgradeGatewayReceipt, _ *api.AppHealthResponse) {
			*r = nil
		}},
		{"wrong_session", "gateway_confirmation_pending", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, r *[]runtimeUpgradeGatewayReceipt, _ *api.AppHealthResponse) {
			(*r)[0].SessionID = uuid.NewString()
		}},
		{"wrong_cutover", "gateway_confirmation_pending", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, r *[]runtimeUpgradeGatewayReceipt, _ *api.AppHealthResponse) {
			(*r)[0].CutoverAt = now
		}},
		{"stale_gateway", "gateway_confirmation_pending", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, r *[]runtimeUpgradeGatewayReceipt, _ *api.AppHealthResponse) {
			(*r)[0].InstalledAt = now.Add(-2 * time.Minute)
		}},
		{"future_gateway", "gateway_confirmation_pending", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, r *[]runtimeUpgradeGatewayReceipt, _ *api.AppHealthResponse) {
			(*r)[0].InstalledAt = now.Add(time.Second)
		}},
		{"named_environment", "health_scope_unsupported", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, s *string, _ *[]runtimeUpgradeGatewayReceipt, _ *api.AppHealthResponse) {
			*s = "production"
		}},
		{"stale_health", "health_evidence_stale", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.EvaluatedAt = now.Add(-3 * time.Minute).Format(time.RFC3339Nano)
		}},
		{"future_health", "health_evidence_stale", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.EvaluatedAt = now.Add(time.Second).Format(time.RFC3339Nano)
		}},
		{"wrong_app", "health_scope_changed", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.AppID = uuid.NewString()
		}},
		{"wrong_release", "health_scope_changed", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.ServingDeploymentIDs = []string{uuid.NewString()}
		}},
		{"later_deploy", "health_scope_changed", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.LatestDeploymentID = uuid.NewString()
		}},
		{"collection_window_overlaps_cutover", "post_cutover_window_pending", func(_ *RuntimeUpgradeVerification, c *RuntimeUpgradeCutover, _ *string, r *[]runtimeUpgradeGatewayReceipt, _ *api.AppHealthResponse) {
			c.CutoverAt = now.Add(-api.AppHealthMetricsWindow - api.AppHealthCollectorLease + time.Second)
			(*r)[0].CutoverAt = c.CutoverAt
		}},
		{"short_window", "post_cutover_window_pending", func(_ *RuntimeUpgradeVerification, c *RuntimeUpgradeCutover, _ *string, r *[]runtimeUpgradeGatewayReceipt, _ *api.AppHealthResponse) {
			c.CutoverAt = now.Add(-time.Minute)
			(*r)[0].CutoverAt = c.CutoverAt
		}},
		{"unhealthy", "candidate_health_unconfirmed", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.Status = "unhealthy"
		}},
		{"parked", "candidate_health_unconfirmed", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.Capacity.Ready = 0
		}},
		{"unknown_capacity", "candidate_health_unconfirmed", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.Capacity.Known = false
		}},
		{"no_telemetry", "request_evidence_unavailable", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.Requests.Known = false
		}},
		{"wrong_request_scope", "request_evidence_unavailable", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.Requests.DeploymentIDs = []string{uuid.NewString()}
		}},
		{"old_metrics", "post_cutover_metrics_pending", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.MetricsAsOf = now.Add(-3 * time.Minute).Format(time.RFC3339Nano)
		}},
		{"metrics_predate_window", "post_cutover_metrics_pending", func(_ *RuntimeUpgradeVerification, c *RuntimeUpgradeCutover, _ *string, r *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			c.CutoverAt = now.Add(-api.AppHealthMetricsWindow - api.AppHealthCollectorLease)
			(*r)[0].CutoverAt = c.CutoverAt
			h.MetricsAsOf = now.Add(-api.AppHealthCollectorLease - time.Second).Format(time.RFC3339Nano)
		}},
		{"no_requests", "request_volume_insufficient", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.Requests.RequestCount = 0
		}},
		{"few_requests", "request_volume_insufficient", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.Requests.RequestCount = 49
		}},
		{"inconsistent_rate", "request_evidence_invalid", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.Requests.ServerErrors = 5
		}},
		{"truncation_lower_bound", "request_evidence_invalid", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.Requests.ServerErrors = 2
			h.Requests.ErrorRatePct = 1.7
		}},
		{"elevated_errors", "request_error_rate_elevated", func(_ *RuntimeUpgradeVerification, _ *RuntimeUpgradeCutover, _ *string, _ *[]runtimeUpgradeGatewayReceipt, h *api.AppHealthResponse) {
			h.Requests.ServerErrors = 5
			h.Requests.ErrorRatePct = 5
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := newRuntimeUpgradeVerification(id, []string{session}, now)
			if err != nil {
				t.Fatal(err)
			}
			c, scope := cutover, DefaultEnvScope
			receipts := []runtimeUpgradeGatewayReceipt{{SessionID: session, DeploymentID: c.DeploymentID, CutoverAt: c.CutoverAt, InstalledAt: now.Add(-10 * time.Second)}}
			h := api.AppHealthResponse{AppID: app, Status: "healthy", Scope: DefaultEnvScope, EvaluatedAt: now.Format(time.RFC3339Nano), ValidForSeconds: 120, MetricsAsOf: now.Format(time.RFC3339Nano), ServingDeploymentIDs: []string{c.DeploymentID}, LatestDeploymentID: c.DeploymentID, Capacity: api.AppHealthCapacity{Known: true, Ready: 1, Required: 1}, Requests: &api.AppHealthRequests{Known: true, Coverage: "serving_deployments", WindowSeconds: 300, DeploymentIDs: []string{c.DeploymentID}, RequestCount: 100}}
			if tt.edit != nil {
				tt.edit(&out, &c, &scope, &receipts, &h)
			}
			got := evaluateRuntimeUpgradeVerification(out, c, scope, receipts, &h, app)
			if got.Reason != tt.reason || (got.Status == "verified") != (tt.reason == "") {
				t.Fatalf("got %#v; want reason %q", got, tt.reason)
			}
			if tt.reason == "" && got.ValidForSeconds != 50 {
				t.Fatal("receipt validity overstated", got)
			}
		})
	}
	out, _ := newRuntimeUpgradeVerification(id, []string{session}, now)
	receipt := runtimeUpgradeGatewayReceipt{SessionID: session, DeploymentID: cutover.DeploymentID, CutoverAt: cutover.CutoverAt, InstalledAt: now}
	if got := evaluateRuntimeUpgradeVerification(out, cutover, DefaultEnvScope, []runtimeUpgradeGatewayReceipt{receipt}, nil, app); got.Reason != "health_evidence_unavailable" {
		t.Fatal(got)
	}
	sessions := []string{uuid.NewString(), uuid.NewString()}
	_, err := newRuntimeUpgradeVerification(id, sessions, now)
	if err != nil {
		t.Fatal(err)
	}
	before := slices.Clone(sessions)
	_, _ = newRuntimeUpgradeVerification(id, sessions, now)
	if !slices.Equal(before, sessions) {
		t.Fatal("caller participants mutated")
	}
	for _, bad := range [][]string{nil, {session, session}, {session, strings.ToUpper(session)}, {"invalid"}} {
		if _, err := newRuntimeUpgradeVerification(id, bad, now); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal("invalid participants accepted", bad, err)
		}
	}
}
