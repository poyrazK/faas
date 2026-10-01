package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	featureFlagAutoAdvanceInterval = time.Minute
	featureFlagAutoAdvanceBatch    = 100
	featureFlagAutoAdvanceTimeout  = 30 * time.Second
	featureFlagAutoAdvanceActor    = "system:flags-auto-advance"
)

type featureFlagAutoAdvancer struct {
	server             *server
	log                *slog.Logger
	afterEnvironmentID string
}

func startFeatureFlagAutoAdvancer(ctx context.Context, s *server, log *slog.Logger) {
	if s == nil || !s.featureFlagsEnabled || s.store == nil {
		return
	}
	if _, ok := s.store.(state.FeatureFlagAutoRolloutLister); !ok {
		return
	}
	advancer := &featureFlagAutoAdvancer{server: s, log: log}
	go func() {
		advancer.runPass(ctx)
		ticker := time.NewTicker(featureFlagAutoAdvanceInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				advancer.runPass(ctx)
			}
		}
	}()
}

func (a *featureFlagAutoAdvancer) runPass(ctx context.Context) {
	lister, ok := a.server.store.(state.FeatureFlagAutoRolloutLister)
	if !ok {
		return
	}
	candidates, err := lister.ListFeatureFlagAutoRolloutCandidates(ctx, a.afterEnvironmentID, featureFlagAutoAdvanceBatch)
	if err != nil {
		if a.log != nil && !errors.Is(err, context.Canceled) {
			a.log.WarnContext(ctx, "flags auto-advance: list candidates failed", "err", err)
		}
		return
	}
	if len(candidates) == 0 {
		a.afterEnvironmentID = ""
		return
	}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return
		}
		candidateCtx, cancel := context.WithTimeout(ctx, featureFlagAutoAdvanceTimeout)
		if err := a.server.autoAdvanceFeatureFlagRollout(candidateCtx, candidate, time.Now().UTC()); err != nil && !errors.Is(err, context.Canceled) {
			if a.log != nil {
				a.log.WarnContext(candidateCtx, "flags auto-advance: evaluate candidate failed", "environment_id", candidate.Scope.EnvironmentID, "err", err)
			}
		}
		cancel()
	}
	a.afterEnvironmentID = candidates[len(candidates)-1].Scope.EnvironmentID
	if len(candidates) < featureFlagAutoAdvanceBatch {
		a.afterEnvironmentID = ""
	}
}

// autoAdvanceFeatureFlagRollout advances at most one healthy rule in an
// environment. The active configuration version is the durable stage-entry
// marker: waiting a full window after its creation ensures the evidence window
// contains only requests served under this stage before any automatic change.
func (s *server) autoAdvanceFeatureFlagRollout(ctx context.Context, candidate state.FeatureFlagAutoRolloutCandidate, now time.Time) error {
	if !s.featureFlagsEnabled || s.store == nil {
		return nil
	}
	configStore, ok := s.store.(state.FeatureFlagStore)
	if !ok {
		return nil
	}
	evidenceStore, ok := s.store.(state.FeatureFlagEvidenceStore)
	if !ok {
		return nil
	}
	current, err := configStore.GetFeatureFlags(ctx, candidate.Scope, 0)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return err
	}
	account, err := s.store.AccountByID(ctx, candidate.Scope.AccountID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return err
	}
	limits := api.MustLimitsFor(account.Plan)
	if !limits.DebugTelemetryEnabled {
		return nil
	}
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour

	for flagIndex := range current.Flags {
		flag := current.Flags[flagIndex]
		if !flag.Enabled {
			continue
		}
		for ruleIndex := range flag.Rules {
			rule := flag.Rules[ruleIndex]
			if rule.Progression == nil || !rule.Progression.AutoAdvance || rule.Rollout == nil {
				continue
			}
			value, isBoolean := rule.Value.(bool)
			progression := *rule.Progression
			if !isBoolean || !value || progression.CurrentStage < 0 || progression.CurrentStage+1 >= len(progression.Stages) {
				continue
			}
			window := time.Duration(progression.WindowSeconds) * time.Second
			if retention <= 0 || window > retention || current.CreatedAt.IsZero() || now.Before(current.CreatedAt.Add(window)) {
				continue
			}

			outcomeRequest := &http.Request{URL: &url.URL{RawQuery: url.Values{
				"since":          {window.String()},
				"rule_id":        {rule.ID},
				"config_version": {strconv.FormatInt(current.Version, 10)},
			}.Encode()}}
			outcomeRequest.SetPathValue("environment", candidate.EnvironmentSlug)
			outcomeRequest.SetPathValue("key", flag.Key)
			params, start, end, err := featureFlagOutcomeQuery(outcomeRequest, candidate.Scope, retention)
			if err != nil {
				return err
			}
			apps, err := s.store.AppsForProject(ctx, account.ID, candidate.Scope.ProjectID)
			if err != nil {
				return err
			}
			for _, app := range apps {
				if app.PreviewOfSlug == "" {
					params.AppIds = append(params.AppIds, stringToPgUUID(app.ID))
				}
			}
			rows, err := evidenceStore.FeatureFlagRequestOutcomes(ctx, params)
			if err != nil {
				return err
			}
			health := evaluateFeatureFlagRolloutHealth(progression, rows)
			if health.HoldReason != "" {
				if s.log != nil {
					s.log.InfoContext(ctx, "flags auto-advance: rollout held by evidence gate",
						"project", candidate.ProjectSlug, "environment", candidate.EnvironmentSlug,
						"flag", flag.Key, "rule_id", rule.ID, "config_version", current.Version,
						"reason", health.HoldReason, "used_count", health.UsedCount,
						"request_count", health.RequestCount, "http_5xx_rate", health.HTTP5xxRate,
						"p95_latency_ms", health.P95LatencyMS)
				}
				continue
			}

			nextRollout := progression.Stages[progression.CurrentStage+1]
			progression.CurrentStage++
			rule.Progression = &progression
			*rule.Rollout = nextRollout
			flag.Rules[ruleIndex] = rule
			current.Flags[flagIndex] = flag
			updated, err := configStore.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{
				Scope: candidate.Scope, ExpectedVersion: current.Version,
				Config: current.Config, Actor: featureFlagAutoAdvanceActor,
			})
			if err != nil {
				if errors.Is(err, state.ErrConflict) {
					return nil
				}
				return err
			}
			s.audit.EmitAs(ctx, featureFlagAutoAdvanceActor, "flags.rollout_auto_promoted", &candidate.Scope.AccountID, map[string]any{
				"project_id": candidate.Scope.ProjectID, "environment_id": candidate.Scope.EnvironmentID,
				"project": candidate.ProjectSlug, "environment": candidate.EnvironmentSlug,
				"flag": flag.Key, "rule_id": rule.ID, "from_version": current.Version,
				"version": updated.Version, "stage": progression.CurrentStage + 1,
				"rollout_basis_points": nextRollout, "used_count": health.UsedCount,
				"request_count": health.RequestCount, "http_5xx_rate": health.HTTP5xxRate,
				"p95_latency_ms": health.P95LatencyMS, "window_start": start, "window_end": end,
			})
			return nil
		}
	}
	return nil
}
