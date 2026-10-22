package mcpmanagement

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

type planWorkloadRolloutArgs struct {
	App                  string `json:"app" jsonschema:"Existing service or worker app slug"`
	Image                string `json:"image,omitempty" jsonschema:"Digest-pinned OCI image; select this or retained_deployment_id"`
	RetainedDeploymentID string `json:"retained_deployment_id,omitempty" jsonschema:"Exact retained image or GitHub source deployment to redeploy/rebuild as a new generation for rollback"`
	Reason               string `json:"reason" jsonschema:"Reviewable rollout reason, at most 280 characters"`
	Project              string `json:"project,omitempty" jsonschema:"Project slug required with a named environment"`
	Environment          string `json:"environment,omitempty" jsonschema:"Registered unprotected environment; omit for default"`
}

type planServiceAbortArgs struct {
	App                     string `json:"app"`
	DeploymentID            string `json:"deployment_id" jsonschema:"Exact active service rollout candidate"`
	PredecessorDeploymentID string `json:"predecessor_deployment_id" jsonschema:"Exact retained live predecessor"`
	Reason                  string `json:"reason"`
	Project                 string `json:"project,omitempty"`
	Environment             string `json:"environment,omitempty"`
}

func registerWorkloadRollouts(s *mcp.Server, m *management) {
	register(s, m, "gregale_watch_rollout", "Watch one exact app/environment deployment for up to 45 seconds. Combine build progress, passive scoped health and native service handoff or worker residency. Accepted, live, healthy and completed are separate. Worker completion is residency only, never queue acknowledgement. Optional MCP progressToken receives bounded state-change notifications. Cancellation stops observation only. No probes or recovery writes.", m.watchRollout)
	register(s, m, "gregale_plan_workload_rollout", "Review a digest-pinned service or worker rollout, including replicas and stop policy. Apply activates through Gregale's native controller; services switch automatically after readiness and workers replace consumers when the new generation becomes live. A retained image can be redeployed, or a retained GitHub commit rebuilt, as a new generation under current settings for rollback. Protected environments require environment promotion.", m.planWorkloadRollout)
	register(s, m, "gregale_plan_service_abort", "Review reverse handoff of an exact active service rollout to its exact live predecessor. Recovery is asynchronous and retains native binding, routing acknowledgement and drain checks.", m.planServiceAbort)
	register(s, m, "gregale_get_rollout", "Inspect one exact service or worker deployment and its native rollout barriers. A live worker image alone does not prove that consumers have switched; inspect scoped instances too.", m.getRollout)
	if !m.opts.AllowWrites {
		return
	}
	registerTool(s, m, "gregale_apply_workload_rollout", "Apply a reviewed native service or worker rollout with an atomic release guard and stable retry key. This can change serving traffic or active consumers automatically and incur runtime costs. Reuse the plan ID after ambiguous errors.", false, m.applyWorkloadRollout)
	registerTool(s, m, "gregale_apply_service_abort", "Request the reviewed service reverse handoff. Acceptance does not mean recovery is complete; observe the exact rollout until native acknowledgement and drain barriers finish.", false, m.applyServiceAbort)
}

func (m *management) planWorkloadRollout(ctx context.Context, args planWorkloadRolloutArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	if (args.Image == "") == (args.RetainedDeploymentID == "") {
		return nil, invalid("invalid_argument", "select exactly one image or retained_deployment_id")
	}
	p, err := m.captureScopedPlan(ctx, args.App, args.Project, args.Environment, args.Reason, "workload_rollout")
	if err != nil {
		return nil, err
	}
	if p.review.Protected {
		return nil, invalid("approval_required", "protected targets require an approved environment promotion")
	}
	if p.latestID != "" {
		latest, err := m.client.GetDeployment(ctx, p.latestID)
		if err != nil {
			return nil, err
		}
		if latest.AppID != p.AppID || normalizedScope(latest.Scope) != p.Scope {
			return nil, invalid("invalid_response", "latest deployment identity differs from review")
		}
		if !deploymentTerminal(latest) {
			return nil, invalid("rollout_active", "wait for the current deployment or recover its exact rollout before planning another generation")
		}
	}
	image := args.Image
	if args.RetainedDeploymentID != "" {
		if err := validateSegment(args.RetainedDeploymentID, "retained_deployment_id"); err != nil {
			return nil, err
		}
		d, err := m.client.GetDeployment(ctx, args.RetainedDeploymentID)
		if err != nil {
			return nil, err
		}
		if d.ID != args.RetainedDeploymentID || d.AppID != p.AppID || normalizedScope(d.Scope) != p.Scope || d.ID == p.ExpectedCurrentID || (d.Status != "superseded" && d.Status != "live") || (d.Kind != "image" && d.Kind != "github") {
			return nil, invalid("invalid_target", "rollback requires an exact retained image or GitHub deployment in this app and environment")
		}
		if d.Kind == "github" {
			if err := m.attachRetainedSource(ctx, p, d); err != nil {
				return nil, err
			}
		} else {
			image = d.ImageDigest
		}
		p.TargetDeploymentID = d.ID
	}
	if p.Source == nil && (len(image) > 512 || !immutableImage.MatchString(image)) {
		return nil, invalid("invalid_argument", "native rollouts require a digest-pinned OCI image reference")
	}
	p.Image = image
	if p.RolloutMode == api.ExecutionModeService {
		p.TrafficEffect = "Start native service rollout: keep the predecessor serving while desired replicas become ready, then switch routing, wait for gateway acknowledgements and drain old requests automatically."
	} else {
		p.TrafficEffect = "Activate a new worker generation when its artifact becomes live. Gregale selects the newest live generation in this environment, stops retired consumers with the reviewed stop policy and reconciles queue-driven replicas. This is not a zero-traffic staging operation; consumers may switch automatically."
	}
	if p.Source != nil {
		p.TrafficEffect += " Rebuild the exact retained GitHub commit through native builderd under CURRENT reviewed settings and the unchanged build root. This creates a new artifact, not restoration of the original binary; mutable dependencies can differ. Repository declarations are not imported and external queue effects are not reversed."
	} else if p.TargetDeploymentID != "" {
		p.TrafficEffect += " Redeploy the retained image under CURRENT reviewed configuration as a new generation; external queue effects are not reversed."
	}
	return m.savePlan(p)
}

func (m *management) applyWorkloadRollout(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.checkedPlan(ctx, args.PlanID, "workload_rollout")
	if err != nil {
		return nil, err
	}
	if p.result != nil {
		return releaseResult(p, p.result), nil
	}
	if p.review == nil || (p.RolloutMode != api.ExecutionModeService && p.RolloutMode != api.ExecutionModeWorker) {
		return nil, invalid("invalid_plan", "missing reviewed native rollout")
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
	req := api.CreateDeploymentRequest{Image: p.Image, Scope: p.Scope, Reason: &p.Reason, ExpectedReview: p.review, NativeRollout: p.RolloutMode}
	if p.EnvironmentID != "" {
		req.Scope = ""
		req.Environment = p.Scope
	}
	var d api.DeploymentResponse
	if p.Source != nil {
		d, err = m.stageSourcePlan(ctx, p)
	} else {
		d, err = m.client.Deploy(api.ContextWithIdempotencyKey(ctx, "mcp-workload-"+p.ID), p.App, req)
	}
	if err != nil {
		return nil, err
	}
	if d.ID == "" || d.AppID != p.AppID || normalizedScope(d.Scope) != p.Scope || !nativeRolloutArtifactMatches(p, d) || (p.RolloutMode == api.ExecutionModeWorker && d.TrafficPercent != 100) || (p.RolloutMode == api.ExecutionModeService && (d.RolloutState != "rolling_out" || d.TrafficPercent != 0)) {
		return nil, invalid("invalid_response", "inconsistent native rollout receipt; inspect deployment history before replanning")
	}
	if p.EnvironmentID != "" && (d.StagedReview == nil || d.StagedReview.EnvironmentID != p.EnvironmentID || d.StagedReview.ProjectID != p.ProjectID || d.StagedReview.LatestDeploymentID != d.ID) {
		return nil, invalid("invalid_response", "missing committed environment review; retry the original plan")
	}
	p.result = &d
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	return releaseResult(p, &d), nil
}

func (m *management) planServiceAbort(ctx context.Context, args planServiceAbortArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	for _, id := range []string{args.DeploymentID, args.PredecessorDeploymentID} {
		if err := validateSegment(id, "deployment_id"); err != nil {
			return nil, err
		}
	}
	p, err := m.captureScopedPlan(ctx, args.App, args.Project, args.Environment, args.Reason, "service_abort")
	if err != nil {
		return nil, err
	}
	if p.review.Protected {
		return nil, invalid("approval_required", "protected targets require their approved environment recovery workflow")
	}
	d, err := m.client.GetDeployment(ctx, args.DeploymentID)
	if err != nil {
		return nil, err
	}
	predecessor, err := m.client.GetDeployment(ctx, args.PredecessorDeploymentID)
	if err != nil {
		return nil, err
	}
	if d.AppID != p.AppID || normalizedScope(d.Scope) != p.Scope || d.Status != "live" || d.RolloutState != "rolling_out" || d.CanaryTotalSteps != 0 || predecessor.AppID != p.AppID || normalizedScope(predecessor.Scope) != p.Scope || predecessor.Status != "live" || d.ID == predecessor.ID || d.ID != p.latestID {
		return nil, invalid("invalid_target", "select the active service rollout and its exact live predecessor in this environment")
	}
	if d.ServiceRolloutHandoff != nil && d.ServiceRolloutHandoff.PredecessorDeploymentID != "" && d.ServiceRolloutHandoff.PredecessorDeploymentID != predecessor.ID {
		return nil, invalid("invalid_target", "predecessor differs from the native handoff")
	}
	p.TargetDeploymentID = d.ID
	p.PredecessorDeploymentID = predecessor.ID
	p.TrafficEffect = "Request asynchronous reverse handoff to the exact predecessor; native binding checks, gateway acknowledgements and request draining remain authoritative."
	return m.savePlan(p)
}

func (m *management) applyServiceAbort(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.checkedPlan(ctx, args.PlanID, "service_abort")
	if err != nil {
		return nil, err
	}
	if p.result != nil {
		return releaseResult(p, p.result), nil
	}
	if p.review == nil {
		return nil, invalid("invalid_plan", "missing service recovery review")
	}
	// The native store checks configuration and both exact deployments inside its transaction.
	p.attempted = true
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	result, err := m.client.RecoverReviewedServiceRollout(api.ContextWithIdempotencyKey(ctx, "mcp-service-abort-"+p.ID), p.App, p.TargetDeploymentID, p.PredecessorDeploymentID, p.Reason, *p.review)
	if err != nil {
		return nil, err
	}
	d := result.Deployment
	if d.ID != p.TargetDeploymentID || d.AppID != p.AppID || normalizedScope(d.Scope) != p.Scope || result.ServiceRecovery == nil || result.ServiceRecovery.PredecessorDeploymentID != p.PredecessorDeploymentID || result.ServiceRecovery.DeploymentID != d.ID || result.ServiceRecovery.RequestID == "" {
		return nil, invalid("invalid_response", "missing exact native reverse-handoff receipt")
	}
	p.result = &d
	p.ServiceRecovery = result.ServiceRecovery
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	return releaseResult(p, &d), nil
}

func (m *management) getRollout(ctx context.Context, args waitArgs) (any, error) {
	if err := validateSegment(args.App, "app"); err != nil {
		return nil, err
	}
	if err := validateSegment(args.DeploymentID, "deployment_id"); err != nil {
		return nil, err
	}
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	observed, err := m.pollDeployment(ctx, args.DeploymentID, app.ID, time.Now(), args.Environment)
	if err != nil {
		return nil, err
	}
	out := observed.(map[string]any)
	if app.Manifest.EffectiveExecutionMode() == api.ExecutionModeWorker {
		observation, err := m.client.GetWorkerRolloutObservation(ctx, args.App, args.DeploymentID, args.Environment)
		if err != nil {
			return nil, err
		}
		if observation.AppID != app.ID || observation.DeploymentID != args.DeploymentID || observation.Scope != normalizedScope(args.Environment) || observation.EnvironmentID != env.ID || observation.ProjectID != env.ProjectID || observation.Basis != "resident_generation_snapshot" || observation.QueueProcessingConfirmed {
			return nil, invalid("invalid_response", "worker observation lacks exact scoped residency attribution")
		}
		out["worker_observation"] = observation
		out["completion_basis"] = "resident_generation_snapshot"
		out["consumer_switch_confirmed"] = observation.GenerationConverged
		out["instruction"] = "Generation convergence describes resident worker records at checked_at. Queue processing, external consumers and delivery completion require separate queue evidence."
	}
	return out, nil
}

type rolloutPolicy struct {
	MemoryMBPerReplica     int                  `json:"memory_mb_per_replica"`
	CPUMillicores          int                  `json:"cpu_millicores,omitempty"`
	MaxConcurrency         int                  `json:"max_concurrency"`
	ServiceReplicas        *api.ServiceReplicas `json:"service_replicas,omitempty"`
	WorkerReplicas         *api.WorkerScaling   `json:"worker_replicas,omitempty"`
	HealthPath             string               `json:"health_path,omitempty"`
	StopSignal             string               `json:"stop_signal,omitempty"`
	StopGracePeriodSeconds int                  `json:"stop_grace_period_seconds,omitempty"`
}

func captureRolloutPolicy(app api.AppResponse) *rolloutPolicy {
	manifest := app.Manifest
	if manifest.EffectiveExecutionMode() != api.ExecutionModeService && manifest.EffectiveExecutionMode() != api.ExecutionModeWorker {
		return nil
	}
	return &rolloutPolicy{MemoryMBPerReplica: app.RAMMB, CPUMillicores: app.CPUMillicores, MaxConcurrency: app.MaxConcurrency, ServiceReplicas: manifest.ServiceReplicas, WorkerReplicas: manifest.WorkerReplicas, HealthPath: manifest.Healthz, StopSignal: manifest.StopSignal, StopGracePeriodSeconds: int(manifest.StopGracePeriod / time.Second)}
}
func deploymentTerminal(d api.DeploymentResponse) bool {
	if d.Status == "failed" || d.Status == "cancelled" || d.Status == "superseded" {
		return true
	}
	if d.Status != "live" {
		return false
	}
	// A live service image can still be waiting for replicas, bindings or ACKs.
	if d.CanaryTotalSteps == 0 && d.RolloutState == "rolling_out" {
		return false
	}
	return true
}

func nativeRolloutArtifactMatches(p *releasePlan, d api.DeploymentResponse) bool {
	if p.Source != nil {
		return d.Kind == "github" && d.CommitSHA == p.Source.CommitSHA && d.BuildID != ""
	}
	return d.Kind == "image" && d.ImageDigest == p.Image
}
