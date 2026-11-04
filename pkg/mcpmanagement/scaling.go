// adr: 20261104104521
package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func scalingRequestID(p *releasePlan) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale-mcp-scaling:"+p.ID)).String()
}

func scalingRequest(p *releasePlan) (api.ApplyAppScalingRequest, error) {
	if p.Operation.ScalingBefore == nil || p.Operation.ScalingBefore.AppID != p.AppID || p.Operation.ScalingBefore.Scope != p.Scope || p.Operation.ScalingBefore.Revision != p.Operation.ExpectedRevision {
		return api.ApplyAppScalingRequest{}, invalid("invalid_plan", "review a new scaling plan with a native revision checkpoint")
	}
	var req api.ApplyAppScalingRequest
	if err := json.Unmarshal(p.Operation.Request, &req); err != nil {
		return req, err
	}
	revision := p.Operation.ExpectedRevision
	req.ExpectedRevision, req.RequestID = &revision, scalingRequestID(p)
	req.ExpectedEnvironmentID = p.EnvironmentID
	req.ExpectedAppID = p.AppID
	return req, nil
}

func validScalingReceipt(p *releasePlan, receipt api.AppScalingReceipt) bool {
	req, err := scalingRequest(p)
	if err != nil {
		return false
	}
	expected := p.Operation.ScalingBefore.Settings
	if req.RAMMB != nil {
		expected.RAMMB = *req.RAMMB
	}
	if req.CPUMillicores != nil {
		expected.CPUMillicores = *req.CPUMillicores
	}
	if req.MaxConcurrency != nil {
		expected.MaxConcurrency = *req.MaxConcurrency
	}
	if req.MinInstances != nil {
		expected.MinInstances = *req.MinInstances
	}
	return receipt.AppID == p.AppID && receipt.Scope == p.Scope && receipt.EnvironmentID == p.EnvironmentID && receipt.RequestID == req.RequestID && receipt.PreviousRevision == *req.ExpectedRevision && receipt.Revision >= receipt.PreviousRevision && (p.Scope != "default" || receipt.Revision > receipt.PreviousRevision) && receipt.Settings == expected && !receipt.CommittedAt.IsZero()
}

func (m *management) applyNativeScaling(ctx context.Context, p *releasePlan, retry bool) (any, error) {
	req, err := scalingRequest(p)
	if err != nil {
		return nil, err
	}
	if retry {
		receipt, err := m.client.GetAppScalingReceipt(ctx, p.App, p.Scope, req.RequestID)
		if err == nil {
			if !validScalingReceipt(p, receipt) {
				return nil, invalid("invalid_response", "native scaling receipt differs from the reviewed request")
			}
			return receipt, nil
		}
		var native *api.APIError
		if !errors.As(err, &native) || native.Problem.Status != http.StatusNotFound {
			return nil, err
		}
	}
	receipt, err := m.client.ApplyAppScaling(ctx, p.App, p.Scope, req)
	if err != nil {
		return nil, err
	}
	if !validScalingReceipt(p, receipt) {
		return nil, invalid("invalid_response", "native scaling receipt differs from the reviewed request")
	}
	return receipt, nil
}

func (m *management) verifyScaling(ctx context.Context, t verificationTarget, v Verification) (Verification, error) {
	receipt, err := m.client.GetAppScalingReceipt(ctx, t.app, t.scope, t.scalingRequestID)
	if err != nil {
		var native *api.APIError
		if errors.As(err, &native) && native.Problem.Status == http.StatusNotFound {
			v.Status = "pending"
			v.Checks = []verificationCheck{{"scaling_committed", "pending", t.scalingRequestID, "native_receipt_not_available_recover_original_request"}}
			return v, nil
		}
		return v, err
	}
	if t.scalingPlan == nil || !validScalingReceipt(t.scalingPlan, receipt) {
		return v, invalid("invalid_response", "scaling verification receipt differs from the reviewed request")
	}
	snapshot, err := m.client.GetAppScaling(ctx, t.app, t.scope)
	if err != nil {
		return v, err
	}
	if snapshot.AppID != t.appID || snapshot.Scope != t.scope || snapshot.EnvironmentID != t.environmentID || snapshot.Revision < receipt.Revision {
		return v, invalid("invalid_response", "scaling observation does not identify the committed target")
	}
	v.Checks = []verificationCheck{{"scaling_committed", "verified", receipt.RequestID, "exact_native_request_committed"}}
	if snapshot.Revision != receipt.Revision || snapshot.Settings != receipt.Settings {
		v.Status = "blocked"
		v.Checks = append(v.Checks, verificationCheck{"scaling_configuration_current", "blocked", t.appID, "committed_settings_superseded_review_current_configuration"})
		return v, nil
	}
	v.Status = "verified"
	v.Checks = append(v.Checks, verificationCheck{"scaling_configuration_current", "verified", t.appID, "committed_revision_and_settings_current_runtime_convergence_not_assessed"})
	return v, nil
}

func knownScalingRefusal(err error) bool {
	var native *api.APIError
	return errors.As(err, &native) && native.Problem.Status == http.StatusConflict && (native.Problem.Code == "scaling_revision_conflict" || native.Problem.Code == "scaling_request_conflict")
}
