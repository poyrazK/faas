package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) runAutomaticRouteCheckWorker(ctx context.Context) {
	ticker := time.NewTicker(api.RouteCheckPollInterval)
	defer ticker.Stop()
	for {
		if _, err := s.drainAutomaticRouteChecks(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("automatic route check queue unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) drainAutomaticRouteChecks(ctx context.Context) (int, error) {
	queue, ok := s.store.(state.AutomaticRouteCheckStore)
	checks, hasChecks := s.store.(state.RouteRequirementsStore)
	if !ok || !hasChecks {
		return 0, nil
	}
	completed := 0
	for range api.RouteCheckBatchSize {
		claim, err := queue.ClaimAutomaticRouteCheck(ctx, api.RouteCheckClaimLease)
		if errors.Is(err, state.ErrNotFound) {
			return completed, nil
		}
		if err != nil {
			return completed, err
		}
		done, err := s.processAutomaticRouteCheck(ctx, queue, checks, claim)
		if err != nil && ctx.Err() == nil {
			// Failure details can contain database or private intent context.
			// Persist/expose only the stable failure code and retry schedule.
			s.log.Warn("automatic route check retry scheduled", "app_id", claim.AppID, "deployment_id", claim.DeploymentID)
		}
		if done {
			completed++
		}
	}
	return completed, nil
}

func (s *server) processAutomaticRouteCheck(ctx context.Context, queue state.AutomaticRouteCheckStore, checks state.RouteRequirementsStore, claim state.AutomaticRouteCheckClaim) (bool, error) {
	checkCtx, cancel := context.WithTimeout(ctx, api.RouteCheckTimeout)
	defer cancel()
	var captureSHA string
	var truncated bool
	checker := func(snapshot state.RoutePolicySnapshot, saved api.SavedRouteRequirements) (api.RouteRequirementsCheck, error) {
		captureSHA, truncated = state.RouteCheckCaptureIdentity(snapshot.Contract)
		result, err := routerequirements.BuildSavedCheck(saved, s.routeRequirementsContext(snapshot), string(snapshot.Account.Plan), claim.DeploymentID, routePolicyInventory(snapshot.Contract, snapshot.Account.Plan))
		if err != nil {
			return result, err
		}
		return routerequirements.BoundAutomaticCheck(result)
	}
	result, err := checks.CheckRouteRequirements(checkCtx, claim.AccountID, claim.AppID, api.CheckRouteRequirementsRequest{DeploymentID: claim.DeploymentID}, checker)
	if err == nil {
		var done bool
		done, err = queue.CompleteAutomaticRouteCheck(checkCtx, claim, result, captureSHA, truncated)
		if err == nil {
			return done, nil
		}
	}
	if ctx.Err() == nil {
		if _, failErr := queue.FailAutomaticRouteCheck(ctx, claim); failErr != nil {
			return false, failErr
		}
	}
	return false, err
}
