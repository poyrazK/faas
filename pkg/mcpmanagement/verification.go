// adr: 20261103202626
package mcpmanagement

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Verification describes an obligation, or a dated observation of that
// obligation. Mutation recovery and verification are deliberately independent.
type Verification struct {
	Status    string              `json:"status"`
	Goal      string              `json:"goal"`
	Checks    []verificationCheck `json:"checks"`
	Next      *Recovery           `json:"next,omitempty"`
	CheckedAt *time.Time          `json:"checked_at,omitempty"`
}

type verificationCheck struct {
	Criterion  string `json:"criterion"`
	Status     string `json:"status"`
	ResourceID string `json:"resource_id,omitempty"`
	Reason     string `json:"reason"`
}

// Targets are reconstructed from journaled native receipts. Cached health is
// never journaled as a lasting claim of success.
type verificationTarget struct {
	planID, app, appID, scope, project, projectID, environmentID string
	wakeID, deploymentID                                         string
	revision                                                     int64
}

func (m *management) planVerification(p *releasePlan) (Verification, verificationTarget) {
	v := Verification{Status: "unknown", Goal: "manual_review", Checks: []verificationCheck{}}
	t := verificationTarget{planID: p.ID, app: p.App, appID: p.AppID, scope: p.Scope, project: p.Project, projectID: p.ProjectID, environmentID: p.EnvironmentID}
	var receipt struct {
		Deployment deploymentView `json:"deployment"`
		Result     struct {
			WakeID string `json:"wake_id"`
		} `json:"result"`
	}
	if p.localReceipt != nil {
		raw, _ := json.Marshal(p.localReceipt)
		_ = json.Unmarshal(raw, &receipt)
	}
	t.wakeID = receipt.Result.WakeID
	if p.configReceipt != nil {
		t.revision = p.configReceipt.ConfigChange.Revision
	}
	deployment := receipt.Deployment
	if p.result != nil {
		deployment = projectDeployment(*p.result)
	}
	if p.promoted != nil {
		deployment = projectDeployment(*p.promoted)
	}
	if t.appID == "" {
		t.appID = deployment.AppID // a local plan may have created its app
	}
	t.deploymentID = deployment.ID
	switch {
	case p.Action == "service_abort":
		// An abort's target is the candidate being stopped. Bringing that
		// candidate to serving readiness would verify the opposite intent.
		v.Checks = []verificationCheck{{"service_abort_recovery", "unknown", deployment.ID, "inspect_reverse_handoff_and_predecessor_evidence"}}
	case t.wakeID != "":
		v.Status, v.Goal = "required", "restart_processing_and_health"
		v.Checks = []verificationCheck{{"restart_processed", "required", t.wakeID, "accepted_restart_requires_observation"}, {"fresh_app_health", "required", t.appID, "health_after_restart_required"}}
	case t.deploymentID != "":
		v.Status, v.Goal = "required", "release_readiness"
		v.Checks = []verificationCheck{{"release_ready", "required", t.deploymentID, "exact_deployment_requires_observation"}}
	case p.localReceipt != nil && p.Operation != nil && p.Operation.Kind == "env":
		v.Status, v.Goal = "not_required", "configuration_commit"
		v.Checks = []verificationCheck{{"configuration_committed", "verified", "", "native_commit_receipt_without_requested_restart"}}
	}
	if v.Status == "required" {
		r := m.recoveryTool(Recovery{Action: "inspect"}, "gregale_verify_plan", map[string]any{"plan_id": p.ID})
		v.Next = &r
	}
	return v, t
}

func (m *management) verifyPlan(ctx context.Context, args planLookupArgs) (any, error) {
	// Snapshot the exact targets while holding the plan lock, then release it
	// before native reads so polling never holds up mutations or revocation.
	m.mu.Lock()
	p := m.plans[args.PlanID]
	if p == nil {
		m.mu.Unlock()
		return m.getPlan(ctx, args) // whole-environment plans retain manual review
	}
	if time.Now().After(p.ExpiresAt) {
		m.mu.Unlock()
		return nil, invalid("invalid_plan", "verification requires an unexpired plan from this connection")
	}
	r := m.recoverableReceipt(p)
	v, target := m.planVerification(p)
	m.mu.Unlock()
	if err := m.authorizeVerification(ctx, target); err != nil {
		return nil, err
	}
	var err error
	switch v.Goal {
	case "restart_processing_and_health":
		v, err = m.verifyRestart(ctx, target, v)
	case "release_readiness":
		v, err = m.verifyRelease(ctx, target, v)
	}
	if err != nil {
		return nil, err
	}
	// Authorization, immutable app/environment identity and cancellation must
	// still hold after the last evidence read, including on local connections.
	if err := m.authorizeVerification(ctx, target); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	v.CheckedAt = &now
	if v.Status == "verified" || v.Status == "failed" || v.Status == "blocked" || v.Status == "not_required" {
		v.Next = nil
	} else if v.Next != nil {
		seconds := int64(2)
		v.Next.AfterSeconds = &seconds
	}
	r.Verification = &v
	return r, nil
}

func (m *management) authorizeVerification(ctx context.Context, t verificationTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	args := appArgs{App: t.app, Project: t.project, Environment: namedScope(t.scope)}
	if err := m.authorizeArgs(ctx, args); err != nil {
		return err
	}
	app, env, err := m.appEnvironment(ctx, t.app, t.project, namedScope(t.scope), false)
	if err != nil {
		return err
	}
	if t.appID == "" || app.ID != t.appID || app.Slug != t.app || env.ID != t.environmentID || env.ProjectID != t.projectID {
		return invalid("app_access_denied", "verification target identity changed; inspect current state")
	}
	return nil
}

func (m *management) verifyRestart(ctx context.Context, t verificationTarget, v Verification) (Verification, error) {
	value, err := m.getRestart(ctx, restartStatusArgs{App: t.app, WakeID: t.wakeID})
	if err != nil {
		return v, err
	}
	restart := value.(api.RuntimeConfigRestartStatusResponse)
	v.Status, v.Checks[0].Status = "pending", "pending"
	v.Checks[0].Reason = "restart_processing_pending"
	switch restart.Status {
	case "failed":
		v.Status, v.Checks[0].Status, v.Checks[0].Reason = "failed", "failed", "restart_processing_failed"
		return v, nil
	case "queued", "retrying", "running":
		return v, nil
	case "completed":
		if restart.CompletedAt == nil || restart.CompletedAt.IsZero() || restart.CompletedAt.After(time.Now()) {
			return v, invalid("invalid_response", "completed restart lacks a valid completion time")
		}
	default:
		return v, invalid("invalid_response", "restart status is unknown")
	}
	v.Checks[0].Status, v.Checks[0].Reason = "verified", "restart_processing_completed"
	return m.verifyRestartHealth(ctx, t, v, *restart.CompletedAt)
}

func (m *management) verificationConfigCurrent(ctx context.Context, t verificationTarget) (bool, error) {
	if t.revision == 0 {
		return true, nil
	}
	revision, err := m.configRevision(ctx, t.app, t.scope)
	return revision == t.revision, err
}

func (m *management) verifyRestartHealth(ctx context.Context, t verificationTarget, v Verification, completed time.Time) (Verification, error) {
	if err := m.authorizeVerification(ctx, t); err != nil {
		return v, err
	}
	current, err := m.verificationConfigCurrent(ctx, t)
	if err != nil {
		return v, err
	}
	if !current {
		v.Status, v.Checks[1].Status, v.Checks[1].Reason = "blocked", "blocked", "configuration_changed_since_apply"
		return v, nil
	}
	health, err := m.scopedHealth(ctx, appArgs{App: t.app})
	if err != nil {
		return v, err
	}
	if health.AppID != t.appID || health.ProjectID != "" || health.EnvironmentID != "" {
		return v, invalid("invalid_response", "restart health identity differs from the reviewed app")
	}
	current, err = m.verificationConfigCurrent(ctx, t)
	if err != nil {
		return v, err
	}
	if !current {
		v.Status, v.Checks[1].Status, v.Checks[1].Reason = "blocked", "blocked", "configuration_changed_during_verification"
		return v, nil
	}
	return assessRestartHealth(v, health, completed), nil
}

func assessRestartHealth(v Verification, health api.AppHealthResponse, completed time.Time) Verification {
	evaluated, parseErr := time.Parse(time.RFC3339Nano, health.EvaluatedAt)
	v.Status, v.Checks[1].Status, v.Checks[1].Reason = "unknown", "unknown", "fresh_post_restart_health_unconfirmed"
	if health.LatestDeploymentID != "" && parseErr == nil && !evaluated.Before(completed) && watchedHealthFresh(health, health.LatestDeploymentID, time.Now()) {
		v.Status, v.Checks[1].Status, v.Checks[1].Reason = "verified", "verified", "fresh_post_restart_app_health"
	} else if health.Status == "unhealthy" && parseErr == nil && !evaluated.Before(completed) && health.ValidForSeconds > 0 && !evaluated.After(time.Now()) && time.Since(evaluated) <= min(time.Duration(health.ValidForSeconds)*time.Second, api.AppHealthEvidenceMaxAge) {
		v.Status, v.Checks[1].Status, v.Checks[1].Reason = "failed", "failed", "post_restart_app_unhealthy"
	}
	return v
}

func (m *management) verifyRelease(ctx context.Context, t verificationTarget, v Verification) (Verification, error) {
	if err := validateSegment(t.deploymentID, "deployment_id"); err != nil {
		return v, err
	}
	out, err := m.readRolloutSnapshot(ctx, waitArgs{App: t.app, Project: t.project, Environment: namedScope(t.scope), DeploymentID: t.deploymentID}, false)
	if err != nil {
		return v, err
	}
	v.Status, v.Checks[0].Status, v.Checks[0].Reason = "pending", "pending", "exact_release_readiness_unconfirmed"
	switch {
	case out.Failed:
		v.Status, v.Checks[0].Status, v.Checks[0].Reason = "failed", "failed", "release_or_build_failed"
	case out.Completed:
		v.Status, v.Checks[0].Status, v.Checks[0].Reason = "verified", "verified", out.CompletionBasis
	case out.Live && out.Deployment.TrafficPercent == 0 && out.Mode == api.ExecutionModeRequest:
		v.Status, v.Checks[0].Status, v.Checks[0].Reason = "blocked", "blocked", "explicit_promotion_required"
	}
	if out.Mode == api.ExecutionModeWorker {
		v.Goal = "worker_generation_residency"
	}
	return v, nil
}

func (m *management) addToolVerification(name string, args any, data json.RawMessage) (json.RawMessage, error) {
	if !isApplyTool(name) {
		return data, nil
	}
	var lookup planLookupArgs
	raw, _ := json.Marshal(args)
	_ = json.Unmarshal(raw, &lookup)
	v := Verification{Status: "unknown", Goal: "manual_review", Checks: []verificationCheck{}}
	m.mu.Lock()
	if p := m.plans[lookup.PlanID]; p != nil {
		v, _ = m.planVerification(p)
	}
	m.mu.Unlock()
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, invalid("invalid_response", "apply result must be an object")
	}
	fields["verification"], _ = json.Marshal(v)
	out, err := json.Marshal(fields)
	if err != nil || len(out) > maxResultBytes {
		return nil, invalid("response_too_large", "verification exceeds the MCP output bound")
	}
	return out, nil
}
