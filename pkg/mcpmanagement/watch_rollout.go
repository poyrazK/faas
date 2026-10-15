package mcpmanagement

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type watchRolloutResult struct {
	AppID           string                        `json:"app_id"`
	Scope           string                        `json:"scope"`
	ProjectID       string                        `json:"project_id,omitempty"`
	EnvironmentID   string                        `json:"environment_id,omitempty"`
	Mode            string                        `json:"mode"`
	CheckedAt       time.Time                     `json:"checked_at"`
	Basis           string                        `json:"basis"`
	Deployment      deploymentView                `json:"deployment"`
	Build           *api.BuildResponse            `json:"build,omitempty"`
	Health          api.AppHealthResponse         `json:"health"`
	Worker          *api.WorkerRolloutObservation `json:"worker_observation,omitempty"`
	Accepted        bool                          `json:"accepted"`
	Live            bool                          `json:"live"`
	Healthy         bool                          `json:"healthy"`
	Completed       bool                          `json:"completed"`
	Failed          bool                          `json:"failed"`
	TimedOut        bool                          `json:"watch_timed_out"`
	CompletionBasis string                        `json:"completion_basis"`
	Blockers        []string                      `json:"blockers"`
	NextRead        string                        `json:"next_read"`
}

func (m *management) watchRollout(ctx context.Context, args waitArgs) (any, error) {
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
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	if outer, ok := ctx.Deadline(); ok && outer.Before(deadline.Add(200*time.Millisecond)) {
		deadline = outer.Add(-200 * time.Millisecond)
	}
	readCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var last *watchRolloutResult
	for {
		out, err := m.rolloutSnapshot(readCtx, args)
		if err != nil {
			if last != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				last.TimedOut = true
				return last, nil
			}
			return nil, err
		}
		if last != nil && (last.AppID != out.AppID || last.EnvironmentID != out.EnvironmentID || last.ProjectID != out.ProjectID || last.Mode != out.Mode) {
			return nil, invalid("invalid_response", "rollout identity changed while watching")
		}
		if err := reportWatchProgress(readCtx, out); err != nil {
			return nil, err
		}
		last = &out
		if out.Completed || out.Failed {
			return out, nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-readCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last.TimedOut = true
			return last, nil
		case <-timer.C:
		}
	}
}

func (m *management) rolloutSnapshot(ctx context.Context, args waitArgs) (watchRolloutResult, error) {
	var out watchRolloutResult
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return out, err
	}
	if app.ID == "" || app.Slug != args.App || !m.appIDAllowed(app.ID) || m.opts.AllowedApps != nil && m.opts.AllowedApps[args.App] != app.ID {
		return out, invalid("app_access_denied", "app identity is outside watch consent")
	}
	d, err := m.client.GetDeployment(ctx, args.DeploymentID)
	if err != nil {
		return out, err
	}
	if d.ID != args.DeploymentID || d.AppID != app.ID || normalizedScope(d.Scope) != normalizedScope(args.Environment) {
		return out, invalid("invalid_target", "deployment does not belong to the selected app and environment")
	}
	out = watchRolloutResult{AppID: app.ID, Scope: normalizedScope(args.Environment), ProjectID: env.ProjectID, EnvironmentID: env.ID, Mode: app.Manifest.EffectiveExecutionMode(), CheckedAt: time.Now().UTC(), Basis: "sequential_passive_reads", Deployment: projectDeployment(d), Accepted: true, Live: d.Status == "live", Blockers: []string{}, CompletionBasis: "unconfirmed", NextRead: "gregale_watch_rollout"}
	if d.BuildID != "" {
		build, err := m.client.GetBuildsId(ctx, d.BuildID)
		if err != nil {
			return out, err
		}
		if build.ID != d.BuildID || build.DeploymentID != d.ID {
			return out, invalid("invalid_response", "build does not belong to the watched deployment")
		}
		out.Build = &build
	}
	h, err := m.scopedHealth(ctx, appArgs{App: args.App, Project: args.Project, Environment: args.Environment})
	if err != nil {
		return out, err
	}
	if h.AppID != app.ID || h.Scope != out.Scope || h.EnvironmentID != env.ID || h.ProjectID != env.ProjectID {
		return out, invalid("invalid_response", "health does not match watched environment")
	}
	out.Health = h
	if out.Mode == api.ExecutionModeWorker && out.Live {
		worker, err := m.client.GetWorkerRolloutObservation(ctx, args.App, d.ID, args.Environment)
		if err != nil {
			return out, err
		}
		if worker.AppID != app.ID || worker.DeploymentID != d.ID || worker.Scope != out.Scope || worker.EnvironmentID != env.ID || worker.ProjectID != env.ProjectID || worker.Basis != "resident_generation_snapshot" || worker.QueueProcessingConfirmed {
			return out, invalid("invalid_response", "worker observation does not match watched generation")
		}
		out.Worker = &worker
	}
	fresh, finalEnv, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return out, err
	}
	if fresh.ID != app.ID || finalEnv.ID != env.ID || finalEnv.ProjectID != env.ProjectID || fresh.Manifest.EffectiveExecutionMode() != out.Mode {
		return out, invalid("invalid_response", "rollout identity changed during observation")
	}
	out.CheckedAt = time.Now().UTC()
	deriveRolloutWatch(&out, d)
	return out, nil
}

func deriveRolloutWatch(out *watchRolloutResult, d api.DeploymentResponse) {
	out.Failed = d.Status == "failed" || d.Status == "cancelled" || d.Status == "superseded" || out.Build != nil && (out.Build.Status == "failed" || out.Build.Status == "cancelled")
	if out.Failed {
		out.Blockers = append(out.Blockers, "deployment_or_build_terminal_failure")
		out.NextRead = "gregale_list_app_deployments"
		return
	}
	out.Healthy = watchedHealthFresh(out.Health, d.ID, out.CheckedAt)
	if !out.Live {
		out.Blockers = append(out.Blockers, "artifact_not_live")
		out.NextRead = "gregale_wait_deployment"
	}
	if out.Build != nil && out.Build.Status != "succeeded" {
		out.Blockers = append(out.Blockers, "build_not_succeeded")
		out.NextRead = "gregale_wait_deployment"
		return
	}
	if !out.Healthy {
		out.Blockers = append(out.Blockers, "exact_deployment_health_unconfirmed")
		if out.Live {
			out.NextRead = "gregale_get_health"
		}
	}
	if out.Health.LatestDeploymentID != d.ID {
		out.Blockers = append(out.Blockers, "deployment_is_not_current")
		return
	}
	switch out.Mode {
	case api.ExecutionModeRequest:
		out.CompletionBasis = "current_serving_release_and_fresh_health"
		out.Completed = out.Live && out.Healthy && d.TrafficPercent == 100 && d.RolloutState == "complete"
		if !out.Completed {
			out.Blockers = append(out.Blockers, "serving_promotion_or_rollout_unconfirmed")
		}
	case api.ExecutionModeService:
		out.CompletionBasis = "native_handoff_and_fresh_health"
		handoff := d.ServiceRolloutHandoff
		settled := d.RolloutState == "complete" && (handoff != nil && handoff.CompletedAt != nil && len(handoff.MissingGateways) == 0 && handoff.Action == "promote")
		out.Completed = out.Live && out.Healthy && d.TrafficPercent == 100 && settled
		if !settled {
			out.Blockers = append(out.Blockers, "native_service_handoff_or_drain_pending")
			out.NextRead = "gregale_get_rollout"
		}
	case api.ExecutionModeWorker:
		out.CompletionBasis = "resident_generation_snapshot_not_queue_completion"
		out.Completed = out.Live && out.Worker != nil && out.Worker.GenerationConverged && out.Worker.SelectedDeploymentID == d.ID && out.Worker.Running > 0 && out.Worker.Starting == 0 && out.Worker.Draining == 0 && out.Worker.OtherResidents == 0 && out.Worker.RetiredResidents == 0
		if !out.Completed {
			out.Blockers = append(out.Blockers, "worker_generation_residency_unconfirmed")
			out.NextRead = "gregale_get_rollout"
		}
	default:
		out.Blockers = append(out.Blockers, "unsupported_workload_mode")
	}
	if out.Completed {
		out.NextRead = "gregale_get_operational_summary"
	}
}

func watchedHealthFresh(h api.AppHealthResponse, id string, now time.Time) bool {
	if h.Status != "healthy" || len(h.ServingDeploymentIDs) != 1 || h.ServingDeploymentIDs[0] != id || h.ValidForSeconds <= 0 {
		return false
	}
	evaluated, err := time.Parse(time.RFC3339Nano, h.EvaluatedAt)
	if err != nil || evaluated.After(now) || now.Sub(evaluated) > min(time.Duration(h.ValidForSeconds)*time.Second, api.AppHealthEvidenceMaxAge) {
		return false
	}
	return true
}
