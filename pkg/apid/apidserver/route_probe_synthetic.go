package apidserver

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/synthetics"
)

// Synthetic release gates (ADR-20261022082305) probe a canary's candidate
// and stable revision with their synthetic check, in the route probe round.

// syntheticGateProbeTargetLimit bounds the synthetic gate rows one minute
// reads.
const syntheticGateProbeTargetLimit = 500

// routeProbeStatusSender sends one probe and returns its status code;
// routeprobe.Client in production.
type routeProbeStatusSender interface {
	ProbeStatus(ctx context.Context, slug, deploymentID, token, method, path string) (int, bool, error)
}

// syntheticGateProbeTargets lists canaries' synthetic gates. A failed read
// only skips synthetic probes this minute.
func (s *server) syntheticGateProbeTargets(ctx context.Context) []state.SyntheticGateProbeTarget {
	store, ok := s.store.(state.ReleaseGateProbeStore)
	if !ok {
		return nil
	}
	if _, ok := s.routeProbes.(routeProbeStatusSender); !ok {
		return nil
	}
	targets, err := store.ListSyntheticGateProbeTargets(ctx, syntheticGateProbeTargetLimit)
	if err != nil {
		s.log.Warn("synthetic gate probe targets unavailable", "err", err)
		return nil
	}
	return targets
}

// probeSyntheticGates sends each synthetic gate's check to every probed
// revision and records how many attributed answers failed the check.
func (s *server) probeSyntheticGates(ctx context.Context, target release.ProbeTarget, deployments []string, tokens map[string]string, minute time.Time) error {
	sender, ok := s.routeProbes.(routeProbeStatusSender)
	store, storeOK := s.store.(state.ReleaseGateProbeStore)
	if !ok || !storeOK || len(target.Synthetic) == 0 {
		return nil
	}
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results []*state.ReleaseGateProbeResult
	)
	sem := make(chan struct{}, 4)
	for _, gate := range target.Synthetic {
		timeout := min(time.Duration(gate.TimeoutMS)*time.Millisecond, api.RouteHealthProbeRequestTimeout)
		if timeout <= 0 {
			timeout = api.RouteHealthProbeRequestTimeout
		}
		for _, deployment := range deployments {
			r := &state.ReleaseGateProbeResult{GateID: gate.GateID, AppID: gate.AppID, DeploymentID: deployment, WindowStart: minute}
			results = append(results, r)
			for range api.ReleaseGateSyntheticProbesPerRound {
				wg.Add(1)
				sem <- struct{}{}
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					requestCtx, cancel := context.WithTimeout(ctx, timeout)
					defer cancel()
					status, attributed, err := sender.ProbeStatus(requestCtx, target.Slug, r.DeploymentID, tokens[r.DeploymentID], gate.Method, gate.Path)
					if err != nil || !attributed {
						return
					}
					mu.Lock()
					defer mu.Unlock()
					r.Requests++
					if !synthetics.StatusOK(status, gate.ExpectedStatus) {
						r.Failures++
					}
				}()
			}
		}
	}
	wg.Wait()
	counted := make([]state.ReleaseGateProbeResult, 0, len(results))
	for _, r := range results {
		if r.Requests > 0 {
			counted = append(counted, *r)
		}
	}
	if len(counted) == 0 {
		return nil
	}
	return store.RecordReleaseGateProbeResults(ctx, counted)
}
