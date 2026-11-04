package mcpmanagement

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Expiring review handles. Durable remote grants can retain them on restart.
// Platform quotas remain in pkg/api/limits.go.
const releasePlanTTL = 15 * time.Minute
const maxReleasePlans = 256

type releasePlan struct {
	Regression              *regressionActionPlan `json:"regression_triage,omitempty"`
	regressionResult        *api.DebugRegressionReview
	Budgets                 *routeBudgetPlan `json:"route_budgets,omitempty"`
	budgetResult            *api.EnvironmentRouteBudgets
	ID                      string                             `json:"plan_id"`
	Action                  string                             `json:"action"`
	AccountID               string                             `json:"account_id"`
	APIBase                 string                             `json:"api_base"`
	Profile                 string                             `json:"profile"`
	AppID                   string                             `json:"app_id"`
	App                     string                             `json:"app"`
	Scope                   string                             `json:"scope"`
	Project                 string                             `json:"project,omitempty"`
	ProjectID               string                             `json:"project_id,omitempty"`
	EnvironmentID           string                             `json:"environment_id,omitempty"`
	ConfigurationHash       string                             `json:"configuration_hash"`
	WorkloadRevision        int64                              `json:"workload_revision,omitempty"`
	WorkloadConfigHash      string                             `json:"workload_config_hash,omitempty"`
	Image                   string                             `json:"image,omitempty"`
	Source                  *api.SourceReviewResponse          `json:"source,omitempty"`
	RolloutMode             string                             `json:"rollout_mode,omitempty"`
	RolloutPolicy           *rolloutPolicy                     `json:"rollout_policy,omitempty"`
	PredecessorDeploymentID string                             `json:"predecessor_deployment_id,omitempty"`
	ServiceRecovery         *api.ServiceRolloutRecoveryReceipt `json:"service_recovery,omitempty"`
	RequestedRef            string                             `json:"requested_ref,omitempty"`
	TargetDeploymentID      string                             `json:"target_deployment_id,omitempty"`
	ExpectedCurrentID       string                             `json:"expected_current_deployment_id"`
	Reason                  string                             `json:"reason"`
	TrafficEffect           string                             `json:"traffic_effect"`
	ExpiresAt               time.Time                          `json:"expires_at"`
	LocalSource             *localSourcePlan                   `json:"local_source,omitempty"`
	EnvChange               *envChangePlan                     `json:"env_change,omitempty"`
	Provision               *provisionPlan                     `json:"provision,omitempty"`
	localReceipt            any
	localInFlight           bool
	appHash                 [32]byte
	latestID                string
	attempted               bool
	result                  *api.DeploymentResponse
	promoted                *api.DeploymentResponse
	promotionAttempted      bool
	review                  *api.AppReleaseReview
}

type planDeployArgs struct {
	App         string `json:"app" jsonschema:"Existing Gregale app slug"`
	Image       string `json:"image" jsonschema:"Immutable OCI image reference ending in @sha256:<64 lowercase hex digits>"`
	Reason      string `json:"reason" jsonschema:"Reviewable release reason; at most 280 characters"`
	Project     string `json:"project,omitempty" jsonschema:"Project slug required with a named environment"`
	Environment string `json:"environment,omitempty" jsonschema:"Registered named environment; omit for default"`
}

type planRollbackArgs struct {
	App                string `json:"app" jsonschema:"Gregale app slug"`
	TargetDeploymentID string `json:"target_deployment_id" jsonschema:"Exact retained deployment ID to restore"`
	Reason             string `json:"reason" jsonschema:"Reviewable rollback reason; at most 280 characters"`
}

type applyArgs struct {
	PlanID string `json:"plan_id" jsonschema:"Unexpired plan ID returned by this connection; reuse the same ID on retries"`
}

type waitArgs struct {
	App          string `json:"app" jsonschema:"Gregale app slug"`
	DeploymentID string `json:"deployment_id" jsonschema:"Exact deployment ID to observe"`
	WaitSeconds  int    `json:"wait_seconds,omitempty" jsonschema:"Wait from 1 to 45 seconds; default 20"`
	Project      string `json:"project,omitempty" jsonschema:"Project slug required with a named environment"`
	Environment  string `json:"environment,omitempty" jsonschema:"Registered named environment; omit for default"`
}

func registerReleases(s *mcp.Server, m *management) {
	registerWorkloadRollouts(s, m)
	register(s, m, "gregale_plan_source_deploy", "Review a connected GitHub source deployment. Resolve a branch once to an exact commit, pin repository/installation identities and target configuration, then apply with gregale_apply_deploy at zero traffic for request mode. For service/worker specify rollout_mode and apply with gregale_apply_workload_rollout; activation follows native handoff/consumer replacement after build. Repository configuration declarations are not imported.", m.planSourceDeploy)
	register(s, m, "gregale_plan_deploy", "Preview an immutable image deployment in the default or an explicitly selected named project environment. Plan pins account, environment identity, configuration revision and current release for 15 minutes. Apply stages at zero traffic; promotion is separate.", m.planDeploy)
	register(s, m, "gregale_plan_rollback", "Preview a checked rollback to an exact retained release. Apply requires writes enabled and unchanged current release; Gregale enforces integrity and binding checks.", m.planRollback)
	register(s, m, "gregale_wait_deployment", "Wait up to 45 seconds for a deployment to settle; a timeout returns pending, so poll the same ID. A live revision at 0% traffic is staged, not serving. Service and worker releases stay pending until their handoff finishes.", m.waitDeployment)
	if !m.opts.AllowWrites {
		return
	}
	registerTool(s, m, "gregale_apply_deploy", "Apply a reviewed deploy plan, creating an idempotent zero-traffic revision. This can incur build/runtime costs. Use wait, then explicit promotion. Retry with the same plan ID after ambiguous errors.", false, m.applyDeploy)
	registerTool(s, m, "gregale_promote_deployment", "Send 100% traffic to the staged revision from a reviewed deploy plan, only if the original serving release still owns traffic in the selected scope. Protected named targets require an approved environment promotion. This changes serving traffic; server-side release gates remain enforced.", false, m.promoteDeployment)
	registerTool(s, m, "gregale_apply_rollback", "Apply a reviewed rollback plan with an atomic current-release guard and stable idempotency key. This changes production traffic and may create an asynchronous rollback operation.", false, m.applyRollback)
}

var immutableImage = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]*@sha256:[a-f0-9]{64}$`)

func validateReason(app, reason string) error {
	if err := validateSegment(app, "app"); err != nil {
		return err
	}
	if reason == "" || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > 280 {
		return invalid("invalid_argument", "reason must contain between 1 and 280 valid Unicode characters")
	}
	return nil
}

func (m *management) planDeploy(ctx context.Context, args planDeployArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	if len(args.Image) > 512 || !immutableImage.MatchString(args.Image) {
		return nil, invalid("invalid_argument", "image must be a digest-pinned OCI reference; tags, credentials, local paths, and mutable branches are not accepted")
	}
	p, err := m.captureScopedPlan(ctx, args.App, args.Project, args.Environment, args.Reason, "deploy")
	if err != nil {
		return nil, err
	}
	p.Image, p.TrafficEffect = args.Image, "Stage a new revision at 0% traffic. Explicit promotion sends 100% to it only if the original serving release is unchanged."
	return m.savePlan(p)
}

func (m *management) planRollback(ctx context.Context, args planRollbackArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	if err := validateSegment(args.TargetDeploymentID, "target_deployment_id"); err != nil {
		return nil, err
	}
	p, err := m.capturePlan(ctx, args.App, args.Reason, "rollback")
	if err != nil {
		return nil, err
	}
	d, err := m.client.GetDeployment(ctx, args.TargetDeploymentID)
	if err != nil {
		return nil, err
	}
	if d.AppID != p.AppID || normalizedScope(d.Scope) != p.Scope || d.ID == p.ExpectedCurrentID || (d.Status != "superseded" && (d.Status != "live" || d.TrafficPercent != 0)) {
		return nil, invalid("invalid_target", "rollback target must be a retained, non-serving release of this app and default scope")
	}
	if p.ExpectedCurrentID == "" {
		return nil, invalid("invalid_target", "checked rollback requires a current serving release")
	}
	p.TargetDeploymentID, p.TrafficEffect = d.ID, "Restore the exact retained release after server-side integrity and binding checks; replace the current serving release."
	p.Image = d.ImageDigest
	return m.savePlan(p)
}

func (m *management) capturePlan(ctx context.Context, slug, reason, action string) (*releasePlan, error) {
	return m.captureScopedPlan(ctx, slug, "", "", reason, action)
}

func (m *management) captureScopedPlan(ctx context.Context, slug, project, environment, reason, action string) (*releasePlan, error) {
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	app, env, err := m.appEnvironment(ctx, slug, project, environment, false)
	if err != nil {
		return nil, err
	}
	// Services have a separate replica/binding rollout contract. Never use a
	// function's manual traffic path to bypass it.
	if action == "deploy" && app.Manifest.EffectiveExecutionMode() != api.ExecutionModeRequest {
		return nil, invalid("unsupported_release", "service and worker image releases require their dedicated rollout workflow; use the CLI release command")
	}
	mode := app.Manifest.EffectiveExecutionMode()
	if action == "rollback" && mode == api.ExecutionModeWorker {
		return nil, invalid("unsupported_release", "worker rollback requires gregale_plan_workload_rollout with an exact retained image deployment; changing HTTP weights does not select worker consumers")
	}
	if action == "workload_rollout" && mode != api.ExecutionModeService && mode != api.ExecutionModeWorker || action == "service_abort" && mode != api.ExecutionModeService {
		return nil, invalid("unsupported_release", "this plan requires the matching service or worker execution mode")
	}
	health, err := m.scopedHealth(ctx, appArgs{App: slug, Project: project, Environment: environment})
	if err != nil {
		return nil, err
	}
	if account.ID == "" || app.ID == "" || app.Slug != slug || health.AppID != app.ID || normalizedScope(health.Scope) != normalizedScope(environment) {
		return nil, invalid("invalid_response", "API returned inconsistent account, app, or health identity")
	}
	if len(health.ServingDeploymentIDs) > 1 {
		return nil, invalid("release_split", "this workflow requires a single serving release; review the active traffic split in the CLI")
	}
	current := ""
	if len(health.ServingDeploymentIDs) == 1 {
		current = health.ServingDeploymentIDs[0]
	}
	hash, err := appConfigurationHash(app)
	if err != nil {
		return nil, err
	}
	var review api.AppReleaseReview
	if environment == "" {
		review, err = m.client.GetAppReleaseReview(ctx, slug)
	} else {
		review, err = m.client.GetAppReleaseReviewInEnvironment(ctx, slug, environment)
	}
	if err != nil {
		return nil, err
	}
	if review.AppID != app.ID || normalizedScope(review.Scope) != normalizedScope(environment) || (env.ID != "" && (review.EnvironmentID != env.ID || review.ProjectID != env.ProjectID)) || len(review.ConfigurationHash) != 64 || review.LatestDeploymentID != health.LatestDeploymentID || len(review.ServingDeploymentIDs) != len(health.ServingDeploymentIDs) || (len(review.ServingDeploymentIDs) == 1 && review.ServingDeploymentIDs[0] != current) {
		return nil, invalid("plan_stale", "App release changed during review; inspect and plan again")
	}
	return &releasePlan{RolloutMode: mode, RolloutPolicy: captureRolloutPolicy(app), review: &review, Action: action, AccountID: account.ID, APIBase: m.opts.APIBase, Profile: m.opts.Profile, AppID: app.ID, App: slug, Scope: normalizedScope(environment), Project: project, ProjectID: env.ProjectID, EnvironmentID: env.ID, ConfigurationHash: review.ConfigurationHash, WorkloadRevision: review.WorkloadRevision, WorkloadConfigHash: review.WorkloadConfigHash, ExpectedCurrentID: current, Reason: reason, ExpiresAt: time.Now().Add(releasePlanTTL), appHash: hash, latestID: health.LatestDeploymentID}, nil
}

func appConfigurationHash(app api.AppResponse) ([32]byte, error) {
	app.DeploymentAvailability, app.BuildCacheHitRatePct, app.WorkloadClass = "", 0, ""
	app.LastScaleOutAt, app.LastScaleInAt, app.ParkedDeployment, app.OverflowNode = nil, nil, nil, nil
	data, err := json.Marshal(app)
	return sha256.Sum256(data), err
}

func normalizedScope(scope string) string {
	if scope == "" {
		return api.DefaultEnvScope
	}
	return scope
}

func (m *management) savePlan(p *releasePlan) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneReleasePlans()
	if len(m.plans)+len(m.environmentPlans) >= maxReleasePlans {
		return nil, invalid("plan_capacity", "connection has too many active release plans; wait for expiration")
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	p.ID = hex.EncodeToString(id)
	m.plans[p.ID] = p
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	return *p, nil
}

func (m *management) checkedPlan(ctx context.Context, id, action string) (*releasePlan, error) {
	if !m.opts.AllowWrites {
		return nil, invalid("writes_disabled", "restart the connection with --allow-writes to enable reviewed release actions")
	}
	p := m.plans[id]
	if p == nil || p.Action != action {
		if err := m.defaultAccess(true); err != nil {
			return nil, err
		}
		return nil, invalid("invalid_plan", "plan ID is unknown or belongs to a different action or connection")
	}
	if time.Now().After(p.ExpiresAt) {
		delete(m.plans, id)
		return nil, invalid("plan_expired", "plan expired; inspect existing deployments before creating a new plan")
	}
	environment := ""
	if p.Scope != api.DefaultEnvScope {
		environment = p.Scope
	}
	app, env, err := m.appEnvironment(ctx, p.App, p.Project, environment, true)
	if err != nil {
		return nil, err
	}
	if env.Protected && (action == "workload_rollout" || action == "service_abort") {
		return nil, invalid("approval_required", "protected targets require their approved environment release workflow")
	}
	if app.ID != p.AppID || env.ID != p.EnvironmentID || env.ProjectID != p.ProjectID {
		return nil, invalid("plan_stale", "app or environment identity changed; create a new plan")
	}
	if !m.appIDAllowed(p.AppID) {
		return nil, invalid("app_access_denied", "release plan is outside this connection's app consent")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID != p.AccountID {
		return nil, invalid("account_changed", "account identity changed; create a new connection and plan")
	}
	return p, nil
}

func (m *management) verifyBaseline(ctx context.Context, p *releasePlan) error {
	environment := ""
	if p.Scope != api.DefaultEnvScope {
		environment = p.Scope
	}
	fresh, err := m.captureScopedPlan(ctx, p.App, p.Project, environment, p.Reason, p.Action)
	if err != nil {
		return err
	}
	if fresh.AccountID != p.AccountID || fresh.AppID != p.AppID || fresh.EnvironmentID != p.EnvironmentID || fresh.appHash != p.appHash || fresh.ExpectedCurrentID != p.ExpectedCurrentID || fresh.latestID != p.latestID || fresh.review.ConfigurationHash != p.review.ConfigurationHash {
		return invalid("plan_stale", "app configuration or release changed since planning; inspect and create a new plan")
	}
	return nil
}

func (m *management) applyDeploy(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.checkedPlan(ctx, args.PlanID, "deploy")
	if err != nil {
		return nil, err
	}
	if p.LocalSource != nil {
		return nil, invalid("invalid_plan", "local source plans apply with gregale_apply_local_deploy")
	}
	if p.result != nil {
		return releaseResult(p, p.result), nil
	}
	if p.review == nil {
		return nil, invalid("invalid_plan", "release plan lacks atomic staging review; create a new plan")
	}
	if !p.attempted {
		if err := m.verifyBaseline(ctx, p); err != nil {
			return nil, err
		}
	}
	p.attempted = true // Ambiguous retries must reuse the same key and payload.
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	zero := 0
	request := api.CreateDeploymentRequest{Image: p.Image, TrafficPercent: &zero, Scope: p.Scope, Reason: &p.Reason, ExpectedReview: p.review}
	if p.EnvironmentID != "" {
		request.Scope, request.Environment = "", p.Scope
	}
	var d api.DeploymentResponse
	if p.Source != nil {
		d, err = m.stageSourcePlan(ctx, p)
	} else {
		d, err = m.client.Deploy(api.ContextWithIdempotencyKey(ctx, "mcp-deploy-"+p.ID), p.App, request)
	}
	if err != nil {
		return nil, err
	}
	if d.ID == "" || d.AppID != p.AppID || normalizedScope(d.Scope) != p.Scope || d.TrafficPercent != 0 {
		return nil, invalid("invalid_response", "API returned an inconsistent staged revision; inspect deployments before retrying this plan")
	}
	if p.Source != nil && (d.CommitSHA != p.Source.CommitSHA || d.Kind != "github" || d.BuildID == "") {
		return nil, invalid("invalid_response", "API returned different source provenance or no build identity; inspect deployments before retrying")
	}
	if p.EnvironmentID != "" && (d.StagedReview == nil || d.StagedReview.EnvironmentID != p.EnvironmentID || d.StagedReview.ProjectID != p.ProjectID || d.StagedReview.AppID != p.AppID || d.StagedReview.LatestDeploymentID != d.ID || d.StagedReview.WorkloadRevision < 1) {
		return nil, invalid("invalid_response", "API did not return the committed workload revision; retry the original plan")
	}
	p.result = &d
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	return releaseResult(p, &d), nil
}

func releaseResult(p *releasePlan, d *api.DeploymentResponse) any {
	out := map[string]any{"plan_id": p.ID, "account_id": p.AccountID, "app": p.App, "deployment": projectDeployment(*d)}
	if d.StagedReview != nil {
		out["staged_review"] = d.StagedReview
	}
	if p.ServiceRecovery != nil {
		out["service_recovery"] = p.ServiceRecovery
	}
	if d.RollbackOperation != nil {
		out["rollback_operation"] = map[string]any{"id": d.RollbackOperation.ID, "status": d.RollbackOperation.Status, "code": d.RollbackOperation.Code}
	}
	return out
}

func (m *management) promoteDeployment(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.checkedPlan(ctx, args.PlanID, "deploy")
	if err != nil {
		return nil, err
	}
	if p.promoted != nil {
		return releaseResult(p, p.promoted), nil
	}
	if p.result == nil {
		return nil, invalid("not_staged", "apply this plan and wait for the staged revision before promotion")
	}
	environment := ""
	if p.EnvironmentID != "" {
		environment = p.Scope
	}
	app, env, err := m.appEnvironment(ctx, p.App, p.Project, environment, true)
	if err != nil {
		return nil, err
	}
	if env.Protected {
		return nil, invalid("approval_required", "protected targets require an approved environment promotion; manual traffic promotion is unavailable")
	}
	hash, err := appConfigurationHash(app)
	if err != nil {
		return nil, err
	}
	if app.ID != p.AppID || hash != p.appHash {
		return nil, invalid("plan_stale", "app configuration changed since planning; inspect the staged revision in the CLI")
	}
	if p.EnvironmentID != "" {
		fresh, err := m.client.GetAppReleaseReviewInEnvironment(ctx, p.App, p.Scope)
		if err != nil {
			return nil, err
		}
		if p.result.StagedReview == nil || fresh.ConfigurationHash != p.result.StagedReview.ConfigurationHash || fresh.EnvironmentID != p.EnvironmentID || fresh.LatestDeploymentID != p.result.ID {
			return nil, invalid("plan_stale", "environment configuration or candidate changed; create a new plan")
		}
	}
	d, err := m.client.GetDeployment(ctx, p.result.ID)
	if err != nil {
		return nil, err
	}
	if d.AppID != p.AppID || normalizedScope(d.Scope) != p.Scope || d.Status != "live" {
		return nil, invalid("not_ready", "the planned revision is not live in the expected app and scope")
	}
	p.promotionAttempted = true
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	writeCtx := api.ContextWithIdempotencyKey(ctx, "mcp-promote-"+p.ID)
	if p.EnvironmentID != "" {
		d, err = m.client.PatchDeploymentTrafficReviewed(writeCtx, d.ID, p.ExpectedCurrentID, *p.result.StagedReview)
	} else {
		d, err = m.client.PatchDeploymentTrafficIfServing(writeCtx, d.ID, 100, p.ExpectedCurrentID)
	}
	if err != nil {
		return nil, err
	}
	p.promoted = &d
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	return releaseResult(p, &d), nil
}

func (m *management) applyRollback(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.checkedPlan(ctx, args.PlanID, "rollback")
	if err != nil {
		return nil, err
	}
	if p.result != nil {
		return releaseResult(p, p.result), nil
	}
	if !p.attempted {
		if err := m.verifyBaseline(ctx, p); err != nil {
			return nil, err
		}
	}
	p.attempted = true
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	d, err := m.client.CheckedRollback(api.ContextWithIdempotencyKey(ctx, "mcp-rollback-"+p.ID), p.App, api.RollbackRequest{ExpectedCurrentDeploymentID: &p.ExpectedCurrentID, TargetDeploymentID: &p.TargetDeploymentID, Reason: p.Reason})
	if err != nil {
		return nil, err
	}
	p.result = &d
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	return releaseResult(p, &d), nil
}

func (m *management) waitDeployment(ctx context.Context, args waitArgs) (any, error) {
	if err := validateSegment(args.App, "app"); err != nil {
		return nil, err
	}
	if err := validateSegment(args.DeploymentID, "deployment_id"); err != nil {
		return nil, err
	}
	seconds := args.WaitSeconds
	if seconds == 0 {
		seconds = 20
	}
	if seconds < 1 || seconds > 45 {
		return nil, invalid("invalid_argument", "wait_seconds must be between 1 and 45")
	}
	app, _, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	if outer, ok := ctx.Deadline(); ok && outer.Before(deadline) {
		deadline = outer.Add(-100 * time.Millisecond)
	}
	return m.pollDeployment(ctx, args.DeploymentID, app.ID, deadline, args.Environment)
}

func (m *management) pollDeployment(ctx context.Context, id, appID string, deadline time.Time, scopes ...string) (any, error) {
	for {
		d, err := m.client.GetDeployment(ctx, id)
		if err != nil {
			return nil, err
		}
		if d.AppID != appID || (len(scopes) > 0 && normalizedScope(d.Scope) != normalizedScope(scopes[0])) {
			return nil, invalid("invalid_target", "deployment does not belong to the requested app")
		}
		terminal := deploymentTerminal(d)
		if terminal || !time.Now().Before(deadline) {
			out := map[string]any{"deployment": projectDeployment(d), "terminal": terminal, "pending": !terminal}
			if d.BuildID != "" {
				build, err := m.client.GetBuildsId(ctx, d.BuildID)
				if err != nil {
					return nil, err
				}
				if build.ID != d.BuildID || build.DeploymentID != d.ID {
					return nil, invalid("invalid_response", "build does not belong to the observed deployment")
				}
				out["build"] = build
			}
			return out, nil
		}
		timer := time.NewTimer(min(time.Second, time.Until(deadline)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
