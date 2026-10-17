package apidserver

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/routeprobe"
	"github.com/onebox-faas/faas/pkg/state"
)

// routeProbeSender sends one probe; routeprobe.Client in production.
type routeProbeSender interface {
	Configured() bool
	Probe(ctx context.Context, slug, deploymentID, token, method, path string) (routeprobe.Outcome, error)
}

// routeProbeChallengeDelay lets every gateway receive a new challenge before
// probes use it. A probe that still races delivery is refused by the gateway
// and simply not counted.
var routeProbeChallengeDelay = time.Second

// WithRouteProbes enables ADR-847 synthetic probes. Without a configured
// sender the worker never runs and probe evidence stays unavailable.
func (s *server) WithRouteProbes(sender routeProbeSender) *server {
	s.routeProbes = sender
	return s
}

func (s *server) runRouteProbeWorker(ctx context.Context) {
	if s.routeProbes == nil || !s.routeProbes.Configured() {
		return
	}
	ticker := time.NewTicker(api.RouteHealthProbePollInterval)
	defer ticker.Stop()
	for {
		if err := s.drainRouteProbes(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("route probe round failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// drainRouteProbes runs at most one probe round per app and minute across
// apid replicas. It runs every RouteHealthProbePollInterval, so a minute's
// round starts soon after the minute does (ADR-1321).
func (s *server) drainRouteProbes(ctx context.Context) error {
	store, ok := s.store.(state.RouteProbeStore)
	if !ok || s.routeProbes == nil {
		return nil
	}
	now := time.Now().UTC()
	if now.Sub(s.routeProbePrunedAt) >= api.RouteHealthProbePruneInterval {
		s.routeProbePrunedAt = now
		if err := store.PruneRouteProbeData(ctx, now.Add(-api.RouteHealthProbeRetention)); err != nil {
			s.log.Warn("route probe prune failed", "err", err)
		}
	}
	gated, err := store.ListRouteProbeTargets(ctx)
	if err != nil {
		return err
	}
	targets := release.MergeProbeTargets(gated, s.defaultRouteProbeTargets(ctx))
	minute := now.Truncate(time.Minute)
	for _, target := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		claimed, err := store.ClaimRouteProbeRound(ctx, target.AppID, minute)
		if err != nil || !claimed {
			continue
		}
		roundCtx, cancel := context.WithTimeout(ctx, api.RouteHealthProbeRoundTimeout)
		if err := s.probeRouteHealthTarget(roundCtx, store, target, minute); err != nil {
			s.log.Warn("route probe round skipped", "app_id", target.AppID, "err", err)
		}
		cancel()
	}
	return nil
}

// defaultRouteProbeTargets lists safe-policy canaries for the ADR-1446
// default probe. A failed read only skips default probes this minute.
func (s *server) defaultRouteProbeTargets(ctx context.Context) []state.RouteProbeTarget {
	store, ok := s.store.(state.DefaultRouteProbeStore)
	if !ok || !release.DefaultProbesEnabled() {
		return nil
	}
	targets, err := store.ListDefaultRouteProbeTargets(ctx)
	if err != nil {
		s.log.Warn("default route probe targets unavailable", "err", err)
		return nil
	}
	return targets
}

// routeProbePlan is one probed selector and how many probes each
// deployment gets this round.
type routeProbePlan struct {
	selector api.RouteHealthRoute
	requests int
}

// routeProbePlans picks probed selectors whose organic evidence is sparse, or
// already rests on probes, for the candidate's current report.
func routeProbePlans(gate api.RouteHealthGate, report api.RouteHealthReport) []routeProbePlan {
	plans := []routeProbePlan{}
	for i, f := range report.Routes {
		if i >= len(gate.Routes) || gate.Routes[i].Probe == nil || gate.Routes[i].Method != f.Method || gate.Routes[i].Path != f.Path {
			continue
		}
		if f.EvidenceWindow == "synthetic" || f.EvidenceWindow == "" && routehealth.NeedsPooledEvidence(f) {
			plans = append(plans, routeProbePlan{selector: gate.Routes[i], requests: api.RouteHealthProbeRequestsPerMinute})
		}
	}
	return plans
}

func (s *server) probeRouteHealthTarget(ctx context.Context, store state.RouteProbeStore, target release.ProbeTarget, minute time.Time) error {
	plans, stableID, err := s.routeProbeRound(ctx, target)
	if err != nil || len(plans) == 0 || stableID == "" {
		return err
	}
	deployments := []string{target.CandidateID, stableID}
	tokens := map[string]string{}
	for _, deployment := range deployments {
		token, err := routeprobe.NewToken()
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(struct {
			AppID        string    `json:"app_id"`
			DeploymentID string    `json:"deployment_id"`
			Token        string    `json:"token"`
			ExpiresAt    time.Time `json:"expires_at"`
		}{target.AppID, deployment, token, time.Now().UTC().Add(api.RouteHealthProbeChallengeTTL)})
		if s.notif == nil {
			return nil
		}
		if err := s.notif.Notify(ctx, db.NotifyRouteProbeChallenge, string(payload)); err != nil {
			return err
		}
		tokens[deployment] = token
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(routeProbeChallengeDelay):
	}
	observations := s.sendRouteProbes(ctx, target.Slug, plans, deployments, tokens, minute)
	return store.RecordRouteProbeObservations(ctx, target.AccountID, target.AppID, observations)
}

// routeProbeRound picks one round's probes: ADR-847 selectors whose organic
// evidence is sparse, plus the ADR-1446 default probe for a safe-policy
// canary, and the predecessor to probe beside the candidate.
func (s *server) routeProbeRound(ctx context.Context, target release.ProbeTarget) ([]routeProbePlan, string, error) {
	plans, stableID := []routeProbePlan{}, target.StableID
	if health, ok := s.store.(state.RouteHealthStore); ok && target.Gated {
		gate, err := health.GetRouteHealthGate(ctx, target.AccountID, target.AppID)
		if err != nil {
			return nil, "", err
		}
		report, err := health.GetRouteHealthReport(ctx, target.AccountID, target.AppID, target.CandidateID)
		if err != nil {
			return nil, "", err
		}
		plans = routeProbePlans(gate, report)
		if stableID == "" {
			stableID = report.StableDeploymentID
		}
	}
	if target.Default {
		plans = withDefaultProbePlans(plans, release.DefaultProbeRoutes(target.ProbePaths))
	}
	return plans, stableID, nil
}

// withDefaultProbePlans adds the default probe's routes: the app's
// release_probe_paths or "/" (ADR-1449). Together they meet the circuit
// breaker's request floor in one round (ADR-1321), also when a gated selector
// already probes one of them.
func withDefaultProbePlans(plans []routeProbePlan, routes []api.RouteHealthRoute) []routeProbePlan {
	requests := release.DefaultProbeRequests(len(routes))
next:
	for _, route := range routes {
		for i, p := range plans {
			if p.selector.Method == route.Method && p.selector.Probe.Path == route.Probe.Path {
				if p.requests < requests {
					plans[i].requests = requests
				}
				continue next
			}
		}
		plans = append(plans, routeProbePlan{selector: route, requests: requests})
	}
	return plans
}

// sendRouteProbes sends each plan's probes per deployment with bounded
// concurrency and counts attributed responses.
func (s *server) sendRouteProbes(ctx context.Context, slug string, plans []routeProbePlan, deployments []string, tokens map[string]string, minute time.Time) []state.RouteProbeObservation {
	var mu sync.Mutex
	// Each goroutine gets its observation directly; nothing it touches is
	// written while probes are in flight.
	counts := []*state.RouteProbeObservation{}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, plan := range plans {
		for _, deployment := range deployments {
			c := &state.RouteProbeObservation{DeploymentID: deployment, Method: plan.selector.Method, Path: plan.selector.Path, WindowStart: minute}
			counts = append(counts, c)
			token, probePath := tokens[deployment], plan.selector.Probe.Path
			for range plan.requests {
				wg.Add(1)
				sem <- struct{}{}
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					requestCtx, cancel := context.WithTimeout(ctx, api.RouteHealthProbeRequestTimeout)
					defer cancel()
					outcome, err := s.routeProbes.Probe(requestCtx, slug, c.DeploymentID, token, c.Method, probePath)
					if err != nil || outcome == routeprobe.Unattributed {
						return
					}
					mu.Lock()
					defer mu.Unlock()
					c.Requests++
					switch outcome {
					case routeprobe.ServerError:
						c.ServerErrors++
					case routeprobe.Unauthenticated:
						c.Unauthenticated++
					}
				}()
			}
		}
	}
	wg.Wait()
	out := []state.RouteProbeObservation{}
	for _, c := range counts {
		if c.Requests > 0 {
			out = append(out, *c)
		}
	}
	return out
}
