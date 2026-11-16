package apidserver

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/routeprobe"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/synthetics"
)

// Baseline health checks (ADR-20261116120000 §3) are probed by apid, not
// meterd's synthetic runner: through the route-probe challenge (ADR-847),
// pinned to the app's live production deployment, so the gateway writes no
// request telemetry and no usage for them. Results land in the check's
// ordinary run history, which its alert and the dashboard read.

// protectionHealthProbesAvailable reports whether apid can send unbilled
// probes: the route prober is configured and reports status codes.
func (s *server) protectionHealthProbesAvailable() bool {
	if s.routeProbes == nil || !s.routeProbes.Configured() {
		return false
	}
	_, ok := s.routeProbes.(routeProbeStatusSender)
	return ok
}

// runProtectionHealthProber probes due baseline health checks every
// ProtectionHealthProbePoll.
func (s *server) runProtectionHealthProber(ctx context.Context) {
	if !s.protectionHealthProbesAvailable() {
		return
	}
	ticker := time.NewTicker(api.ProtectionHealthProbePoll)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, api.ProtectionHealthProbePoll)
		if err := s.protectionHealthProbeSweep(bounded); err != nil && ctx.Err() == nil {
			s.log.Warn("protection health probe sweep", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// protectionHealthTarget is one claimed check with what probing it needs.
type protectionHealthTarget struct {
	check        state.SyntheticCheck
	slug         string
	deploymentID string
	token        string
}

// protectionHealthProbeSweep claims due checks, publishes a challenge per
// target deployment, waits for gateways to receive them, then probes and
// records each run.
func (s *server) protectionHealthProbeSweep(ctx context.Context) error {
	store, ok := s.store.(state.AppProtectionStore)
	checks, ok2 := s.store.(state.SyntheticCheckStore)
	runs, ok3 := s.store.(state.SyntheticRunStore)
	sender, ok4 := s.routeProbes.(routeProbeStatusSender)
	if !ok || !ok2 || !ok3 || !ok4 || s.notif == nil {
		return nil
	}
	claimed, err := store.ClaimProtectionHealthProbes(ctx, api.ProtectionHealthProbeBatch)
	if err != nil || len(claimed) == 0 {
		return err
	}
	var targets []protectionHealthTarget
	for _, p := range claimed {
		target, ok := s.protectionHealthTarget(ctx, checks, p)
		if !ok {
			continue
		}
		token, err := routeprobe.NewToken()
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(struct {
			AppID        string    `json:"app_id"`
			DeploymentID string    `json:"deployment_id"`
			Token        string    `json:"token"`
			ExpiresAt    time.Time `json:"expires_at"`
		}{p.AppID, target.deploymentID, token, time.Now().UTC().Add(api.RouteHealthProbeChallengeTTL)})
		if err := s.notif.Notify(ctx, db.NotifyRouteProbeChallenge, string(payload)); err != nil {
			return err
		}
		target.token = token
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(routeProbeChallengeDelay):
	}
	sem := make(chan struct{}, api.ProtectionHealthProbeWorkers)
	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			run := probeProtectionHealth(ctx, sender, target)
			if err := runs.RecordSyntheticCheckRun(ctx, run); err != nil {
				s.log.Warn("protection health probe: record run", "check", target.check.ID, "err", err)
			}
		}()
	}
	wg.Wait()
	return nil
}

// protectionHealthTarget loads a claimed check and the deployment to probe:
// the newest live deployment in the production scope. An app with none has
// nothing to check this round.
func (s *server) protectionHealthTarget(ctx context.Context, checks state.SyntheticCheckStore, p state.ProtectionHealthProbe) (protectionHealthTarget, bool) {
	app, err := s.store.AppByID(ctx, p.AppID)
	if err != nil {
		return protectionHealthTarget{}, false
	}
	check, err := checks.GetSyntheticCheck(ctx, p.AppID, p.CheckID)
	if err != nil {
		return protectionHealthTarget{}, false
	}
	deps, err := s.store.LiveDeployments(ctx, p.AppID)
	if err != nil {
		return protectionHealthTarget{}, false
	}
	for _, d := range deps { // newest first
		if state.IsProductionScope(d.Scope) {
			return protectionHealthTarget{check: check, slug: app.Slug, deploymentID: d.ID}, true
		}
	}
	return protectionHealthTarget{}, false
}

// probeProtectionHealth sends one probe and classifies it as meterd's runner
// does. An answer the gateway could not attribute to the deployment (it
// could not wake it, or refused the probe) is a failed run: the check
// exists to catch exactly that.
func probeProtectionHealth(ctx context.Context, sender routeProbeStatusSender, target protectionHealthTarget) state.SyntheticCheckRun {
	started := time.Now()
	run := state.SyntheticCheckRun{CheckID: target.check.ID, StartedAt: started.UTC().Truncate(time.Millisecond)}
	timeout := time.Duration(target.check.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(api.SyntheticCheckDefaultTimeoutMS) * time.Millisecond
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	status, attributed, err := sender.ProbeStatus(requestCtx, target.slug, target.deploymentID, target.token, target.check.Method, target.check.Path)
	run.StatusCode = status
	run.LatencyMS = int(time.Since(started).Milliseconds())
	switch {
	case err != nil:
		run.ErrorClass = synthetics.Classify(err)
	case !attributed:
		if status == 0 {
			run.ErrorClass = synthetics.ErrorOther
		} else {
			run.ErrorClass = synthetics.ErrorStatus
		}
	case !synthetics.StatusOK(status, target.check.ExpectedStatus):
		run.ErrorClass = synthetics.ErrorStatus
	}
	run.OK = run.ErrorClass == ""
	return run
}
