package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrRouteGateRevision = errors.New("route gate revision changed")
var ErrRouteGatePlan = errors.New("route enforcement requires canary and captured contract entitlement")
var ErrRouteGateRequirements = errors.New("save route requirements before enabling enforcement")

type RouteGateBlockedError struct{ Decision api.RouteGateDecision }

func (e *RouteGateBlockedError) Error() string {
	return "canary route gate blocked: " + strings.Join(e.Decision.Reasons, ", ")
}

type CanaryRouteGateStore interface {
	GetCanaryRouteGate(context.Context, string, string) (api.CanaryRouteGate, error)
	SetCanaryRouteGate(context.Context, string, string, api.SetCanaryRouteGateRequest) (api.CanaryRouteGate, error)
}

func ValidateCanaryRouteGate(request api.SetCanaryRouteGateRequest) error {
	if request.Mode != "report" && request.Mode != "enforce" || request.ExpectedRevision == nil || *request.ExpectedRevision < 0 || *request.ExpectedRevision > api.RouteRequirementsMaxRevision {
		return ErrInvalidArgument
	}
	return nil
}

func defaultCanaryRouteGate(appID string) api.CanaryRouteGate {
	return api.CanaryRouteGate{AppID: appID, Mode: "report"}
}

func decideCanaryRouteGate(gate api.CanaryRouteGate, deploymentID string, result api.AutomaticRouteCheck, missing, unavailable bool) (api.RouteGateDecision, bool) {
	decision := api.RouteGateDecision{Mode: gate.Mode, Revision: gate.Revision, DeploymentID: deploymentID, Status: "report_only", Reasons: []string{}}
	refresh := false
	switch {
	case unavailable:
		decision.Reasons = append(decision.Reasons, "evidence_unavailable")
	case missing:
		decision.Reasons = append(decision.Reasons, "check_missing")
		refresh = true
	default:
		if result.State != "complete" {
			decision.Reasons = append(decision.Reasons, "check_incomplete")
			refresh = true
		}
		if result.Freshness != "current" {
			decision.Reasons = append(decision.Reasons, "check_stale")
			refresh = true
		}
		if result.Check == nil || result.Check.Report.Status == "unknown" {
			decision.Reasons = append(decision.Reasons, "verdict_unknown")
		} else if result.Check.Report.Status != "satisfied" {
			decision.Reasons = append(decision.Reasons, "requirements_violated")
		}
	}
	if gate.Mode == "enforce" {
		decision.Status = "allowed"
		if len(decision.Reasons) > 0 {
			decision.Status = "blocked"
		}
	}
	return decision, refresh
}

func requireCanaryRouteGate(gate api.CanaryRouteGate, decision api.RouteGateDecision, output *api.RouteGateDecision) error {
	if output != nil {
		*output = decision
	}
	if gate.Mode == "enforce" && decision.Status != "allowed" {
		return &RouteGateBlockedError{Decision: decision}
	}
	return nil
}

func legacyCanaryRouteGate(gate api.CanaryRouteGate, deployment Deployment, action string) error {
	if gate.Mode != "enforce" || deployment.CanaryTotalSteps <= 0 || action == "abort" {
		return nil
	}
	return &RouteGateBlockedError{Decision: api.RouteGateDecision{Mode: gate.Mode, Revision: gate.Revision, DeploymentID: deployment.ID, Status: "blocked", Reasons: []string{"use_canary_advance"}}}
}

func stampRouteGateAudit(params *CanaryAdvanceParams) error {
	if params.RouteGateDecision == nil {
		return nil
	}
	var fields map[string]any
	if len(params.Audit.Data) > 0 {
		if err := json.Unmarshal(params.Audit.Data, &fields); err != nil {
			return fmt.Errorf("decode route gate audit: %w", err)
		}
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["route_gate"] = *params.RouteGateDecision
	body, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("encode route gate audit: %w", err)
	}
	params.Audit.Data = body
	return nil
}
