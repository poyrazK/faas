package mcpmanagement

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type environmentCacheWatchArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project" jsonschema:"Project owning the named environment"`
	Environment string `json:"environment" jsonschema:"Registered named environment"`
	Revision    int64  `json:"revision" jsonschema:"Exact positive revision returned by cache-policy apply"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"Bounded wait from 1 to 45 seconds; default 20. Poll again after timed_out"`
}
type environmentCacheWatchResult struct {
	api.EnvironmentCacheConvergence
	TimedOut bool   `json:"watch_timed_out"`
	Basis    string `json:"basis"`
}

func validateCacheConvergence(out api.EnvironmentCacheConvergence, app, scope, project, environment string) error {
	if out.AppID != app || out.ProjectID != project || out.EnvironmentID != environment || out.Scope != scope || environment == "" || out.Revision < 0 || out.Revision > api.RouteRequirementsMaxRevision || out.CheckedAt.IsZero() || out.CheckedAt.After(time.Now().Add(5*time.Second)) || time.Since(out.CheckedAt) > 90*time.Second || out.ServingGateways < 0 || out.AcknowledgedGateways < 0 || out.PendingGateways < 0 || out.StaleGateways < 0 || out.AcknowledgedGateways+out.PendingGateways+out.StaleGateways != out.ServingGateways {
		return invalid("invalid_response", "cache convergence lacks fresh scoped gateway evidence")
	}
	expected := "pending"
	if out.Revision < 1 || out.ServingGateways == 0 || out.StaleGateways > 0 {
		expected = "degraded"
	} else if out.AcknowledgedGateways == out.ServingGateways {
		expected = "converged"
	}
	if out.Status != expected {
		return invalid("invalid_response", "cache convergence state contradicts gateway evidence")
	}
	return nil
}
func (m *management) cacheConvergenceSnapshot(ctx context.Context, args environmentCacheWatchArgs) (api.EnvironmentCacheConvergence, error) {
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return api.EnvironmentCacheConvergence{}, err
	}
	out, err := m.client.GetEnvironmentCacheConvergence(ctx, args.App, args.Environment)
	if err != nil {
		return out, err
	}
	if err = validateCacheConvergence(out, app.ID, args.Environment, env.ProjectID, env.ID); err != nil {
		return out, err
	}
	if out.Revision != args.Revision {
		return out, invalid("policy_superseded", "the selected environment's desired cache revision differs from the requested revision; inspect current policy")
	}
	fresh, freshEnv, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return out, err
	}
	if fresh.ID != app.ID || freshEnv.ID != env.ID || freshEnv.ProjectID != env.ProjectID {
		return out, invalid("plan_stale", "environment identity changed during observation")
	}
	return out, nil
}
func (m *management) watchEnvironmentCachePolicy(ctx context.Context, args environmentCacheWatchArgs) (any, error) {
	if args.Environment == "" || args.Revision < 1 || args.Revision > api.RouteRequirementsMaxRevision {
		return nil, invalid("invalid_argument", "a named environment and exact positive cache revision are required")
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
	var last *environmentCacheWatchResult
	for {
		out, err := m.cacheConvergenceSnapshot(readCtx, args)
		if err != nil {
			if last != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				last.Status = "timed_out"
				last.TimedOut = true
				return last, nil
			}
			return nil, err
		}
		if last != nil && (last.AppID != out.AppID || last.EnvironmentID != out.EnvironmentID || last.ProjectID != out.ProjectID) {
			return nil, invalid("plan_stale", "identity changed while watching cache policy")
		}
		last = &environmentCacheWatchResult{EnvironmentCacheConvergence: out, Basis: "current-session exact-revision acknowledgements with fresh gateway and node heartbeats; snapshot is not a continuous serving guarantee"}
		if out.Status == "converged" || out.Status == "degraded" {
			return last, nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-readCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last.Status = "timed_out"
			last.TimedOut = true
			return last, nil
		case <-timer.C:
		}
	}
}
