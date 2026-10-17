package api

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// ProjectPromotionControlRequest binds a durable operator decision to an inspected checkpoint.
type ProjectPromotionControlRequest struct {
	RequestID          string `json:"request_id"`
	ExpectedGeneration int64  `json:"expected_generation"`
}

func (r ProjectPromotionControlRequest) Valid() bool {
	return ValidDeploymentAttemptID(r.RequestID) && r.ExpectedGeneration > 0
}

type ProjectPromotionControl struct {
	Request           ProjectPromotionControlRequest `json:"request"`
	Action            string                         `json:"action"`
	Status            string                         `json:"status"`
	OperatorAccountID string                         `json:"operator_account_id"`
	RequestedAt       time.Time                      `json:"requested_at"`
	Deadline          time.Time                      `json:"deadline"`
	ResolvedAt        *time.Time                     `json:"resolved_at,omitempty"`
	Reason            string                         `json:"reason,omitempty"`
}

func ProjectPromotionControlActions(h *ProjectPromotionHealth) []string {
	if h == nil || h.Generation <= 0 || h.Policy.Migration != nil && h.Phase != "queued" || h.Control != nil && h.Control.Status == "pending" {
		return nil
	}
	switch h.Phase {
	case "queued", "preflight", "cutover", "verifying", "finalizing", "rolling_back":
		return []string{"abort"}
	case "rollback_failed":
		return []string{"recover"}
	}
	return nil
}

func (c *Client) ControlProjectEnvironmentPromotion(ctx context.Context, project, environment, promotion, action string, request ProjectPromotionControlRequest) (ProjectPromotionControl, error) {
	var out ProjectPromotionControl
	if !ValidProjectSlug(project) || !ValidProjectEnvironmentSlug(environment) || promotion == "" || !request.Valid() || action != "abort" && action != "recover" {
		return out, fmt.Errorf("invalid promotion control request")
	}
	path := "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/promotions/" + url.PathEscape(promotion) + "/" + action
	return out, c.do(ctx, "POST", path, request, &out)
}
