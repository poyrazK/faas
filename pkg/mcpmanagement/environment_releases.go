package mcpmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"time"
)

type environmentPlan struct {
	ID               string                                  `json:"plan_id"`
	Action           string                                  `json:"action"`
	AccountID        string                                  `json:"account_id"`
	APIBase          string                                  `json:"api_base"`
	Project          string                                  `json:"project"`
	ProjectID        string                                  `json:"project_id"`
	FromEnvironment  string                                  `json:"from_environment"`
	FromID           string                                  `json:"from_environment_id"`
	Environment      string                                  `json:"environment"`
	EnvironmentID    string                                  `json:"environment_id"`
	PromotionID      string                                  `json:"promotion_id,omitempty"`
	PromotionHash    string                                  `json:"promotion_hash"`
	Reason           string                                  `json:"reason"`
	ExpiresAt        time.Time                               `json:"expires_at"`
	ApprovalRequired bool                                    `json:"approval_required"`
	TrafficEffect    string                                  `json:"traffic_effect"`
	Changes          []api.ProjectEnvironmentPromotionChange `json:"changes,omitempty"`
	// This read-only preview token identifies the exact plan a human approves;
	// it does not grant authority. Approval credentials remain private.
	PromotionToken string `json:"promotion_token,omitempty"`
	// Private execution payloads are persisted encrypted, never returned to agents.
	token           string
	approval        string
	approvalRequest *api.BrowserPromotionApprovalResponse
	attempted       bool
	result          any
}

type planEnvironmentPromotionArgs struct {
	Project         string `json:"project" jsonschema:"Gregale project slug"`
	FromEnvironment string `json:"from_environment" jsonschema:"Registered source environment"`
	ToEnvironment   string `json:"to_environment" jsonschema:"Registered target environment"`
	Reason          string `json:"reason" jsonschema:"Reviewable release reason; at most 280 characters"`
}

type planEnvironmentRollbackArgs struct {
	Project     string `json:"project" jsonschema:"Gregale project slug"`
	Environment string `json:"environment" jsonschema:"Target environment to restore"`
	PromotionID string `json:"promotion_id" jsonschema:"Exact durable promotion ID to roll back"`
	Reason      string `json:"reason" jsonschema:"Reviewable rollback reason; at most 280 characters"`
}

type applyEnvironmentPromotionArgs struct {
	PlanID        string `json:"plan_id" jsonschema:"Unexpired environment promotion plan ID; reuse on retries"`
	ApprovalToken string `json:"approval_token,omitempty" jsonschema:"Optional existing human-issued approval for this exact plan. Omit to use private browser review; agents cannot approve reviews"`
}

func (m *management) environmentBaseline(ctx context.Context, project, from, to, reason, action string) (*environmentPlan, error) {
	if err := validateReason(project, reason); err != nil {
		return nil, err
	}
	if from == to {
		return nil, invalid("invalid_argument", "source and target environments must differ")
	}
	p, source, err := m.environmentAccess(ctx, project, from, false, true)
	if err != nil {
		return nil, err
	}
	_, target, err := m.environmentAccess(ctx, project, to, false, true)
	if err != nil {
		return nil, err
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, invalid("invalid_response", "missing account identity")
	}
	return &environmentPlan{Action: action, AccountID: account.ID, APIBase: m.opts.APIBase, Project: project, ProjectID: p.ID, FromEnvironment: from, FromID: source.ID, Environment: to, EnvironmentID: target.ID, Reason: reason, ExpiresAt: time.Now().Add(releasePlanTTL)}, nil
}

func (m *management) planEnvironmentPromotion(ctx context.Context, args planEnvironmentPromotionArgs) (any, error) {
	p, err := m.environmentBaseline(ctx, args.Project, args.FromEnvironment, args.ToEnvironment, args.Reason, "environment_promotion")
	if err != nil {
		return nil, err
	}
	preview, err := m.client.GetProjectEnvironmentPromotionPreview(ctx, args.Project, args.ToEnvironment, args.FromEnvironment)
	if err != nil {
		return nil, err
	}
	if preview.ProjectSlug != p.Project || preview.FromEnvironment != p.FromEnvironment || preview.ToEnvironment != p.Environment || preview.SyncConfig || len(preview.PromotionHash) != 64 || preview.PromotionToken == "" {
		return nil, invalid("invalid_response", "promotion preview identity mismatch")
	}
	if !preview.CanPromote {
		return nil, invalid("promotion_blocked", "environment promotion is blocked by Gregale qualification or release checks; inspect it with the CLI")
	}
	p.PromotionHash, p.token, p.ApprovalRequired, p.Changes = preview.PromotionHash, preview.PromotionToken, preview.ApprovalRequired, preview.Changes
	p.PromotionToken = preview.PromotionToken
	p.TrafficEffect = "Deploy the reviewed source artifacts into the target environment and change its serving releases; preserve target configuration and secrets."
	return m.saveEnvironmentPlan(p)
}

func (m *management) planEnvironmentRollback(ctx context.Context, args planEnvironmentRollbackArgs) (any, error) {
	if err := validateReason(args.Project, args.Reason); err != nil {
		return nil, err
	}
	value, err := m.getEnvironmentPromotion(ctx, environmentPromotionArgs{Project: args.Project, Environment: args.Environment, PromotionID: args.PromotionID})
	if err != nil {
		return nil, err
	}
	status := value.(api.ProjectEnvironmentPromotionStatusResponse)
	if status.Status == "running" || status.RollbackStatus != "" {
		return nil, invalid("invalid_target", "promotion must be finished and not already rolling back")
	}
	p, err := m.environmentBaseline(ctx, args.Project, status.FromEnvironment, args.Environment, args.Reason, "environment_rollback")
	if err != nil {
		return nil, err
	}
	p.PromotionID, p.PromotionHash = status.PromotionID, status.PromotionHash
	p.TrafficEffect = "Restore the target releases captured before this exact promotion; stop if target state changed."
	return m.saveEnvironmentPlan(p)
}

func (m *management) saveEnvironmentPlan(p *environmentPlan) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneReleasePlans()
	if len(m.plans)+len(m.environmentPlans) >= maxReleasePlans {
		return nil, invalid("plan_capacity", "too many active environment plans")
	}
	id, err := randomToken("")
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(id))
	p.ID = hex.EncodeToString(hash[:])
	m.environmentPlans[p.ID] = p
	if err := m.persistEnvironmentPlans(); err != nil {
		return nil, err
	}
	return *p, nil
}

func (m *management) checkedEnvironmentPlan(ctx context.Context, id, action string) (*environmentPlan, error) {
	if !m.opts.AllowWrites {
		return nil, invalid("writes_disabled", "connection writes are disabled")
	}
	p := m.environmentPlans[id]
	if p == nil || p.Action != action {
		return nil, invalid("invalid_plan", "unknown environment plan for this connection or action")
	}
	if time.Now().After(p.ExpiresAt) {
		return nil, invalid("plan_expired", "environment plan expired; inspect and plan again")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	project, source, err := m.environmentAccess(ctx, p.Project, p.FromEnvironment, false, true)
	if err != nil {
		return nil, err
	}
	_, target, err := m.environmentAccess(ctx, p.Project, p.Environment, true, true)
	if err != nil {
		return nil, err
	}
	if account.ID != p.AccountID || p.APIBase != m.opts.APIBase || project.ID != p.ProjectID || source.ID != p.FromID || target.ID != p.EnvironmentID {
		return nil, invalid("plan_stale", "account or environment identity changed; create a new plan")
	}
	return p, nil
}

func (m *management) applyEnvironmentPromotion(ctx context.Context, args applyEnvironmentPromotionArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.checkedEnvironmentPlan(ctx, args.PlanID, "environment_promotion")
	if err != nil {
		return nil, err
	}
	if p.result != nil {
		return p.result, nil
	}
	if len(args.ApprovalToken) > 4096 {
		return nil, invalid("invalid_argument", "approval token is too long")
	}
	if p.attempted && args.ApprovalToken != "" && args.ApprovalToken != p.approval {
		return nil, invalid("invalid_argument", "retry must use the original approval")
	}
	if !p.attempted {
		preview, err := m.client.GetProjectEnvironmentPromotionPreview(ctx, p.Project, p.Environment, p.FromEnvironment)
		if err != nil {
			return nil, err
		}
		if !preview.CanPromote || preview.SyncConfig || preview.PromotionHash != p.PromotionHash || preview.ProjectSlug != p.Project || preview.FromEnvironment != p.FromEnvironment || preview.ToEnvironment != p.Environment {
			return nil, invalid("plan_stale", "environment releases or configuration changed; create a new plan")
		}
		if (p.ApprovalRequired || preview.ApprovalRequired) && args.ApprovalToken == "" && p.approval == "" {
			pending, err := m.browserEnvironmentApproval(ctx, p)
			if err != nil || pending != nil {
				return pending, err
			}
		}
		if args.ApprovalToken != "" {
			p.approval = args.ApprovalToken
		}
		p.attempted = true
	}
	if err := m.persistEnvironmentPlans(); err != nil {
		return nil, err
	}
	result, err := m.client.PromoteProjectEnvironment(api.ContextWithIdempotencyKey(ctx, "gmcp-env-promote-"+p.ID), p.Project, p.Environment, api.PromoteProjectEnvironmentRequest{FromEnvironment: p.FromEnvironment, PromotionToken: p.token, ApprovalToken: p.approval})
	if err != nil {
		return nil, redactApprovalError(err, p.approval)
	}
	if result.ProjectSlug != p.Project || result.FromEnvironment != p.FromEnvironment || result.ToEnvironment != p.Environment || result.PromotionID == "" {
		return nil, invalid("invalid_response", "promotion result identity mismatch; retry the original plan")
	}
	p.result = result
	if err := m.persistEnvironmentPlans(); err != nil {
		return nil, err
	}
	return result, nil
}

func redactApprovalError(err error, credential string) error {
	var apiErr *api.APIError
	if credential == "" || !errors.As(err, &apiErr) {
		return err
	}
	copyErr := *apiErr
	problem := apiErr.Problem
	problem.Code = strings.ReplaceAll(problem.Code, credential, "[REDACTED]")
	problem.Title = strings.ReplaceAll(problem.Title, credential, "[REDACTED]")
	problem.Detail = strings.ReplaceAll(problem.Detail, credential, "[REDACTED]")
	problem.Hint = strings.ReplaceAll(problem.Hint, credential, "[REDACTED]")
	copyErr.Problem = problem
	return &copyErr
}

func (m *management) applyEnvironmentRollback(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.checkedEnvironmentPlan(ctx, args.PlanID, "environment_rollback")
	if err != nil {
		return nil, err
	}
	if p.result != nil {
		return p.result, nil
	}
	p.attempted = true
	if err := m.persistEnvironmentPlans(); err != nil {
		return nil, err
	}
	result, err := m.client.RollbackProjectEnvironmentPromotion(api.ContextWithIdempotencyKey(ctx, "gmcp-env-rollback-"+p.ID), p.Project, p.Environment, p.PromotionID)
	if err != nil {
		return nil, err
	}
	if result.ProjectSlug != p.Project || result.FromEnvironment != p.FromEnvironment || result.ToEnvironment != p.Environment || result.PromotionID != p.PromotionID {
		return nil, invalid("invalid_response", "rollback result identity mismatch; retry the original plan")
	}
	p.result = result
	if err := m.persistEnvironmentPlans(); err != nil {
		return nil, err
	}
	return result, nil
}

type storedEnvironmentPlan struct {
	Plan            environmentPlan
	Token           string
	Approval        string
	Attempted       bool
	Result          json.RawMessage
	ApprovalRequest *api.BrowserPromotionApprovalResponse
}

func (m *management) persistEnvironmentPlans() error {
	if m.opts.planStore == nil {
		return nil
	}
	records := map[string]storedEnvironmentPlan{}
	for id, p := range m.environmentPlans {
		if time.Now().After(p.ExpiresAt) {
			continue
		}
		result, err := json.Marshal(p.result)
		if err != nil {
			return err
		}
		records[id] = storedEnvironmentPlan{Plan: *p, Token: p.token, Approval: p.approval, Attempted: p.attempted, Result: result, ApprovalRequest: p.approvalRequest}
	}
	return m.opts.planStore.update("environment-plans/"+m.opts.planNamespace, records)
}

func (m *management) restoreEnvironmentPlans() error {
	if m.opts.planStore == nil {
		return nil
	}
	var records map[string]storedEnvironmentPlan
	if err := m.opts.planStore.read("environment-plans/"+m.opts.planNamespace, &records); err != nil {
		return err
	}
	if len(records) > maxReleasePlans {
		return invalid("invalid_plan", "invalid stored environment plan capacity")
	}
	for id, r := range records {
		p := r.Plan
		if time.Now().After(p.ExpiresAt) {
			continue
		}
		if id != p.ID || len(id) != 64 || p.APIBase != m.opts.APIBase || (p.Action != "environment_promotion" && p.Action != "environment_rollback") {
			return invalid("invalid_plan", "stored environment plan binding mismatch")
		}
		p.token, p.approval, p.attempted = r.Token, r.Approval, r.Attempted
		p.approvalRequest = r.ApprovalRequest
		if len(r.Result) > 0 && string(r.Result) != "null" {
			p.result = json.RawMessage(r.Result)
		}
		m.environmentPlans[id] = &p
	}
	if len(m.plans)+len(m.environmentPlans) > maxReleasePlans {
		return invalid("invalid_plan", "invalid stored plan capacity")
	}
	return nil
}

func (m *management) pruneReleasePlans() {
	for id, p := range m.plans {
		if time.Now().After(p.ExpiresAt) {
			delete(m.plans, id)
		}
	}
	for id, p := range m.environmentPlans {
		if time.Now().After(p.ExpiresAt) {
			delete(m.environmentPlans, id)
		}
	}
}
