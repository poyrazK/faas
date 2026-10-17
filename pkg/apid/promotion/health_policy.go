package promotion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func HealthHash(hash string, policy *api.ProjectPromotionHealthPolicy) (string, error) {
	if policy == nil {
		return hash, nil
	}
	if err := state.ValidatePromotionHealthPolicy(policy); err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Release string
		Health  *api.ProjectPromotionHealthPolicy
	}{hash, policy})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// PrepareHealth requires a probe for every HTTP member so unprobed services
// cannot silently inherit the project's healthy verdict.
func PrepareHealth(ctx context.Context, store state.Store, accountID string, sources, targets map[string]state.Deployment, ttl int, migrate ...bool) (*api.ProjectPromotionHealthPolicy, error) {
	if len(sources) == 0 || len(sources) != len(targets) || time.Duration(ttl)*time.Second < api.ProjectPromotionHealthMinRetention {
		return nil, fmt.Errorf("verified promotion requires complete source and destination graphs with at least %s retention", api.ProjectPromotionHealthMinRetention)
	}
	health, ok := store.(state.RouteHealthStore)
	if !ok {
		return nil, fmt.Errorf("route probe policy unavailable")
	}
	policy := &api.ProjectPromotionHealthPolicy{}
	for slug, source := range sources {
		target, found := targets[slug]
		if !found || source.AppID != target.AppID {
			return nil, fmt.Errorf("verified promotion requires matching HTTP graphs")
		}
		sourceApp, err := state.AppForDeployment(ctx, store, source)
		if err != nil {
			return nil, err
		}
		if sourceApp.WorkloadClass == state.WorkloadClassWorker && len(migrate) > 1 && migrate[1] {
			targetApp, err := state.AppForDeployment(ctx, store, target)
			if err != nil || targetApp.AccountID != accountID || targetApp.WorkloadClass != state.WorkloadClassWorker || targetApp.Manifest.ExecutionMode != api.ExecutionModeWorker || target.Scope != "production" {
				return nil, fmt.Errorf("worker promotion requires a matching production worker")
			}
			continue
		}
		for _, d := range []state.Deployment{source, target} {
			app, err := state.AppForDeployment(ctx, store, d)
			if err != nil || app.AccountID != accountID {
				return nil, fmt.Errorf("verification workload settings unavailable")
			}
			if app.WorkloadClass != "" && app.WorkloadClass != state.WorkloadClassHTTP || app.Manifest.ExecutionMode != "" && app.Manifest.ExecutionMode != api.ExecutionModeRequest && app.Manifest.ExecutionMode != api.ExecutionModeService || (len(migrate) == 0 || !migrate[0]) && (len(d.ReleaseCommand) != 0 || d.ReleaseCommandShell) || len(d.Sidecars) > 0 && string(d.Sidecars) != "null" && string(d.Sidecars) != "[]" {
				return nil, fmt.Errorf("verified promotion supports HTTP workloads without workers, release commands or sidecars")
			}
		}
		gate, err := health.GetRouteHealthGate(ctx, accountID, source.AppID)
		if err != nil {
			return nil, err
		}
		before := len(policy.Probes)
		for _, route := range gate.Routes {
			if route.Probe != nil {
				policy.Probes = append(policy.Probes, api.ProjectReleaseProbe{AppID: source.AppID, Slug: slug, Method: route.Method, Path: route.Probe.Path})
			}
		}
		if len(policy.Probes) == before {
			return nil, fmt.Errorf("workload %q needs a configured GET/HEAD route-health probe", slug)
		}
	}
	sort.Slice(policy.Probes, func(i, j int) bool {
		a, b := policy.Probes[i], policy.Probes[j]
		return a.AppID+a.Method+a.Path < b.AppID+b.Method+b.Path
	})
	if err := state.ValidatePromotionHealthPolicy(policy); err != nil {
		return nil, fmt.Errorf("verified promotion supports at most %d distinct GET/HEAD probes", api.ProjectReleaseVerificationMaxProbes)
	}
	return policy, nil
}

func AssignWorkerPolicy(policy *api.ProjectPromotionHealthPolicy, capture *state.ProjectEnvironmentPromotionReleaseConfiguration, requested bool) *api.Problem {
	if !requested {
		return nil
	}
	policy.Worker, policy.Workers = capture.Worker, capture.Workers
	if len(api.ProjectPromotionWorkers(policy)) == 0 {
		return api.ErrValidation("worker promotion requires production push consumers")
	}
	if state.ValidatePromotionHealthPolicy(policy) != nil {
		return api.ErrValidation("invalid promotion worker set")
	}
	return nil
}
func promotionWorkerSlug(policy api.ProjectPromotionHealthPolicy, slug string) bool {
	for _, w := range api.ProjectPromotionWorkers(&policy) {
		if w.Slug == slug {
			return true
		}
	}
	return false
}
