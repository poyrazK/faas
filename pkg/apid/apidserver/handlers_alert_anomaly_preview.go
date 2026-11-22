package apidserver

import (
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewAlertAnomaly(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if limits, ok := api.LimitsFor(acct.Plan); !ok || limits.AlertRuleLimitPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanAlertRulesNotAllowed(acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	query := r.URL.Query()
	metric := "request_count"
	if query.Has("metric") {
		metric = query.Get("metric")
		if !api.SupportsEnvironmentAnomalyMetric(metric) {
			api.WriteProblem(w, api.ErrAlertRuleInvalid("metric must be request_count, error_rate_pct, or latency_p95_ms"))
			return
		}
	}
	comparison, window := query.Get("comparison"), query.Get("window_spec")
	threshold, err := strconv.ParseFloat(query.Get("threshold"), 64)
	if err != nil || math.IsNaN(threshold) || math.IsInf(threshold, 0) {
		api.WriteProblem(w, api.ErrAlertRuleInvalid("threshold must be a finite number"))
		return
	}
	if !api.IsAnomalyAlertComparison(comparison) || !slices.Contains(api.AllowedAlertRuleWindowSpecs, window) {
		api.WriteProblem(w, api.ErrAlertRuleInvalid("choose a baseline comparison and a valid alert window_spec"))
		return
	}
	if invalid := api.ValidateAnomalyAlertRule(metric, comparison, threshold, true); invalid != "" {
		api.WriteProblem(w, api.ErrAlertRuleInvalid(invalid))
		return
	}
	config := alerts.AnomalyPreviewComparison{Metric: metric, Comparison: comparison, Threshold: threshold}
	if query.Has("candidate_baseline") {
		if query.Has("candidate_threshold") {
			api.WriteProblem(w, api.ErrAlertRuleInvalid("choose either candidate_baseline or candidate_threshold"))
			return
		}
		switch query.Get("candidate_baseline") {
		case "same_weekday":
			if metric != "request_count" || (window != "5m" && window != "15m") {
				api.WriteProblem(w, api.ErrAlertRuleInvalid("same_weekday requires a 5m or 15m request_count window"))
				return
			}
			config.SameWeekday = true
		case "zero_baseline_fallback":
			if metric != "error_rate_pct" {
				api.WriteProblem(w, api.ErrAlertRuleInvalid("zero_baseline_fallback requires an above_baseline error_rate_pct rule"))
				return
			}
			config.ZeroBaselineFallback = true
		default:
			api.WriteProblem(w, api.ErrAlertRuleInvalid("candidate_baseline must be same_weekday or zero_baseline_fallback"))
			return
		}
	}
	if query.Has("candidate_threshold") {
		value, err := strconv.ParseFloat(query.Get("candidate_threshold"), 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			api.WriteProblem(w, api.ErrAlertRuleInvalid("candidate_threshold must be a finite number"))
			return
		}
		if invalid := api.ValidateAnomalyAlertRule(metric, comparison, value, true); invalid != "" {
			api.WriteProblem(w, api.ErrAlertRuleInvalid("candidate_threshold is outside the allowed range. "+invalid))
			return
		}
		config.CandidateThreshold = &value
	}
	if p := s.anomalyAlertsGate(appFlagSubject(acct, app), comparison); p != nil {
		api.WriteProblem(w, p)
		return
	}
	now := time.Now()
	var start time.Time
	cooldown := api.AlertRuleDefaultCooldownMinutes
	if query.Has("incident_replay_start") {
		const replayDuration = api.AnomalyIncidentReplayChecks * api.AnomalyIncidentReplayStep
		start, err = time.Parse(time.RFC3339, query.Get("incident_replay_start"))
		if err != nil || !start.Equal(start.Truncate(api.AnomalyIncidentReplayStep)) {
			api.WriteProblem(w, api.ErrAlertRuleInvalid("incident_replay_start must be an RFC3339 timestamp aligned to a minute"))
			return
		}
		if start.Before(alerts.AnomalyPreviewFirstCheck(now)) || start.Add(replayDuration).After(now.Truncate(api.AnomalyIncidentReplayStep)) {
			api.WriteProblem(w, api.ErrAlertRuleInvalid("incident replay must cover two completed hours within the preview period"))
			return
		}
	}
	if query.Has("cooldown_minutes") {
		cooldown, err = strconv.Atoi(query.Get("cooldown_minutes"))
		if start.IsZero() || err != nil || cooldown < api.AlertRuleCooldownMinMinutes || cooldown > api.AlertRuleCooldownMaxMinutes {
			api.WriteProblem(w, api.ErrAlertRuleInvalid("cooldown_minutes requires incident_replay_start and must be an integer between 5 and 1440"))
			return
		}
	}
	if query.Has("recovery_confirmation_checks") {
		checks, err := strconv.Atoi(query.Get("recovery_confirmation_checks"))
		if start.IsZero() || err != nil || checks != api.AnomalyReplayRecoveryChecks {
			api.WriteProblem(w, api.ErrAlertRuleInvalid("recovery_confirmation_checks must be 2 and requires incident_replay_start"))
			return
		}
		config.ConfirmRecovery = true
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.AlertObserveBudget)
	defer cancel()
	if query.Has("environment_scope") {
		scope := query.Get("environment_scope")
		if start.IsZero() || scope == "" {
			api.WriteProblem(w, api.ErrAlertRuleInvalid("environment_scope requires incident_replay_start and an environment of this app"))
			return
		}
		if prob := s.alertEnvironmentScopeProblem(ctx, app, scope); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		config.EnvironmentScope = scope
	}
	var preview api.AlertAnomalyPreviewResponse
	if start.IsZero() {
		preview, err = alerts.PreviewAnomaly(ctx, s.promqlClient, app.ID, window, config, now)
	} else {
		preview, err = alerts.ReplayAnomalyIncidents(ctx, s.promqlClient, app.ID, window, config, start, cooldown)
	}
	if err != nil {
		s.log.Warn("alert anomaly preview: read request history", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not read recent request telemetry"))
		return
	}
	if preview.IncidentReplay != nil {
		scope := config.EnvironmentScope
		if scope == "" {
			scope = state.DefaultEnvScope
		}
		preview.IncidentReplay.DeploymentContext = s.anomalyReplayDeployments(ctx, acct.ID, app.ID, scope, start)
	} else {
		preview.EnvironmentScopes = []string{state.DefaultEnvScope}
		if app.ProjectID != "" {
			environments, err := s.store.ListProjectEnvironments(ctx, acct.ID, app.ProjectID)
			if err != nil {
				s.log.Warn("alert anomaly preview: list project environments", "app_id", app.ID, "err", err)
			}
			for _, env := range environments {
				preview.EnvironmentScopes = append(preview.EnvironmentScopes, env.Slug)
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, preview)
}

func validateAlertEnvironmentMode(scope, metric, comparison string, observeOnly bool, action state.AlertAction) *api.Problem {
	if scope == "" {
		return nil
	}
	if !api.IsAnomalyAlertComparison(comparison) || !api.SupportsEnvironmentAnomalyMetric(metric) {
		return api.ErrAlertRuleInvalid("environment_scope requires a request count, error rate, or p95 latency baseline comparison")
	}
	if !observeOnly && action != state.AlertActionWebhook {
		return api.ErrAlertRuleInvalid("scoped Notify rules support notification channels and webhooks")
	}
	return nil
}

func (s *server) alertEnvironmentScopeProblem(ctx context.Context, app state.App, scope string) *api.Problem {
	if scope == "" || scope == api.DefaultEnvScope {
		return nil
	}
	if !api.ValidProjectEnvironmentSlug(scope) || app.ProjectID == "" {
		return api.ErrAlertRuleInvalid("environment_scope must name an environment of this app")
	}
	if _, err := s.store.ProjectEnvironmentBySlug(ctx, app.AccountID, app.ProjectID, scope); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return api.ErrAlertRuleInvalid("environment_scope must name an environment of this app")
		}
		s.log.Warn("alert: load project environment", "app_id", app.ID, "err", err)
		return api.ErrCapacity("could not resolve the alert environment")
	}
	return nil
}

func (s *server) anomalyReplayDeployments(ctx context.Context, accountID, appID, scope string, start time.Time) *api.AlertAnomalyDeploymentContext {
	store, ok := s.store.(state.DeploymentPromotionHistoryStore)
	if !ok {
		return nil
	}
	const limit = 100
	end := start.Add(api.AnomalyIncidentReplayChecks * api.AnomalyIncidentReplayStep)
	promotions, err := store.ListDeploymentPromotions(ctx, accountID, appID, scope, end, limit+1)
	if err != nil {
		s.log.Warn("alert anomaly replay: read deployment promotions", "app_id", appID, "err", err)
		return nil
	}
	history := &api.AlertAnomalyDeploymentContext{
		Scope:      scope,
		Promotions: make([]api.AlertAnomalyDeploymentPromotion, 0, len(promotions)),
	}
	if len(promotions) > limit && !promotions[limit].PromotedAt.Before(start) {
		history.Truncated = true
		promotions = promotions[:limit]
	}
	for _, promotion := range promotions {
		history.Promotions = append(history.Promotions, api.AlertAnomalyDeploymentPromotion{
			DeploymentID: promotion.DeploymentID,
			CommitSHA:    promotion.CommitSHA,
			PromotedAt:   promotion.PromotedAt.UTC(),
		})
		if promotion.PromotedAt.Before(start) {
			break
		}
	}
	return history
}
