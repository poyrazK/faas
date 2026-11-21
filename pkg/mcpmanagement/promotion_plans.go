package mcpmanagement

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A deploy staged at 0% could be promoted only with the plan that staged it.
// That plan is gone after the agent's client restarts its stdio server, and
// never existed for a deploy staged by the CLI or another session, so the
// staged revision could not be promoted through MCP at all
// (ADR-20261121140000). gregale_plan_promotion reviews promoting an existing
// staged deployment; gregale_promote_deployment applies that plan with the
// same guards as a deploy plan: unchanged configuration, the original serving
// release still owning traffic, protected environments refused, and the
// production approval gate.

type planPromotionArgs struct {
	App          string `json:"app" jsonschema:"Gregale app slug"`
	DeploymentID string `json:"deployment_id" jsonschema:"The staged deployment: live at 0% traffic, the app's latest revision"`
	Project      string `json:"project,omitempty" jsonschema:"Project slug required with a named environment"`
	Environment  string `json:"environment,omitempty" jsonschema:"Registered named environment; omit for default"`
	Reason       string `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

func registerPromotionPlans(s *mcp.Server, m *management) {
	register(s, m, "gregale_plan_promotion", "Review sending 100% traffic to a deployment staged at 0% (the app's latest revision), from any session or the CLI. Pins the serving release and configuration; apply with gregale_promote_deployment.", m.planPromotion)
}

func (m *management) planPromotion(ctx context.Context, a planPromotionArgs) (any, error) {
	if err := validateReason(a.App, a.Reason); err != nil {
		return nil, err
	}
	if err := validateSegment(a.DeploymentID, "deployment_id"); err != nil {
		return nil, err
	}
	p, err := m.captureScopedPlan(ctx, a.App, a.Project, a.Environment, a.Reason, "deploy")
	if err != nil {
		return nil, err
	}
	d, err := m.client.GetDeployment(ctx, a.DeploymentID)
	if err != nil {
		return nil, err
	}
	if d.AppID != p.AppID || normalizedScope(d.Scope) != p.Scope || d.Status != "live" || d.TrafficPercent != 0 || d.ID == p.ExpectedCurrentID {
		return nil, invalid("not_staged", "the deployment must be live at 0% traffic in this app and scope; wait for it with gregale_wait_deployment")
	}
	if p.review == nil || p.review.LatestDeploymentID != d.ID {
		return nil, invalid("not_latest", "only the app's latest revision can be promoted; a newer deployment exists, so plan its promotion instead")
	}
	if p.ExpectedCurrentID == "" {
		return nil, invalid("not_staged", "nothing serves this app yet; a first deployment serves on its own")
	}
	review := *p.review
	d.StagedReview = &review
	p.result = &d
	p.TargetDeploymentID = d.ID
	p.TrafficEffect = fmt.Sprintf("Send 100%% traffic to staged deployment %s, replacing %s, only if %s still owns traffic and the configuration is unchanged.", d.ID, p.ExpectedCurrentID, p.ExpectedCurrentID)
	return m.savePlan(p)
}
