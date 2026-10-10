package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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

// WithRouteProbes enables ADR-954 synthetic probes. Without a configured
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
// apid replicas.
func (s *server) drainRouteProbes(ctx context.Context) error {
	store, ok := s.store.(state.RouteProbeStore)
	if !ok || s.routeProbes == nil {
		return nil
	}
	now := time.Now().UTC()
	if err := store.PruneRouteProbeData(ctx, now.Add(-api.RouteHealthProbeRetention)); err != nil {
		s.log.Warn("route probe prune failed", "err", err)
	}
	targets, err := store.ListRouteProbeTargets(ctx)
	if err != nil {
		return err
	}
	minute := now.Truncate(time.Minute)
	for _, target := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		claimed, err := store.ClaimRouteProbeRound(ctx, target.AppID, minute)
		if err != nil || !claimed {
			continue
		}
		roundCtx, cancel := context.WithTimeout(ctx, api.RouteHealthProbePollInterval-5*time.Second)
		if err := s.probeRouteHealthTarget(roundCtx, store, target, minute); err != nil {
			s.log.Warn("route probe round skipped", "app_id", target.AppID, "err", err)
		}
		cancel()
	}
	return nil
}

type routeProbePlan struct {
	selector api.RouteHealthRoute
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
			plans = append(plans, routeProbePlan{selector: gate.Routes[i]})
		}
	}
	return plans
}

func (s *server) probeRouteHealthTarget(ctx context.Context, store state.RouteProbeStore, target state.RouteProbeTarget, minute time.Time) error {
	health, ok := s.store.(state.RouteHealthStore)
	if !ok {
		return nil
	}
	gate, err := health.GetRouteHealthGate(ctx, target.AccountID, target.AppID)
	if err != nil {
		return err
	}
	report, err := health.GetRouteHealthReport(ctx, target.AccountID, target.AppID, target.CandidateID)
	if err != nil {
		return err
	}
	plans := routeProbePlans(gate, report)
	if len(plans) == 0 || report.StableDeploymentID == "" {
		return nil
	}
	deployments := []string{target.CandidateID, report.StableDeploymentID}
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

// sendRouteProbes sends RouteHealthProbeRequestsPerMinute probes per route
// and deployment with bounded concurrency and counts attributed responses.
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
			for range api.RouteHealthProbeRequestsPerMinute {
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
