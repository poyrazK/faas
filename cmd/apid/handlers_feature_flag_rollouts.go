package main

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

type promoteFeatureFlagRolloutRequest struct {
	ExpectedVersion *int64 `json:"expected_version"`
	RuleID          string `json:"rule_id"`
}

type featureFlagRolloutPromotionResponse struct {
	Status                        string     `json:"status"`
	Reason                        string     `json:"reason,omitempty"`
	Flag                          string     `json:"flag"`
	RuleID                        string     `json:"rule_id"`
	ConfigVersion                 int64      `json:"config_version"`
	CurrentStage                  int        `json:"current_stage"`
	StageCount                    int        `json:"stage_count"`
	RolloutBasisPoints            int        `json:"rollout_basis_points"`
	NextRolloutBasisPoints        *int       `json:"next_rollout_basis_points,omitempty"`
	RequestCount                  int64      `json:"request_count"`
	UsedCount                     int64      `json:"used_count"`
	HTTP5xxCount                  int64      `json:"http_5xx_count"`
	HTTP5xxRate                   float64    `json:"http_5xx_rate"`
	P95LatencyMS                  int32      `json:"p95_latency_ms"`
	LatencyQuantized              bool       `json:"latency_quantized"`
	MinimumUsedRequests           int64      `json:"minimum_used_requests"`
	MaximumHTTP5xxRateBasisPoints int        `json:"maximum_http_5xx_rate_basis_points"`
	MaximumP95LatencyMS           int        `json:"maximum_p95_latency_ms"`
	WindowStart                   *time.Time `json:"window_start,omitempty"`
	WindowEnd                     *time.Time `json:"window_end,omitempty"`
}

func (s *server) promoteFeatureFlagRollout(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, configStore, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return
	}
	var req promoteFeatureFlagRolloutRequest
	if err := decodeJSON(r, &req); err != nil || req.ExpectedVersion == nil || *req.ExpectedVersion < 0 || !flags.ValidKey(req.RuleID) {
		api.WriteProblem(w, api.ErrValidation("expected_version and a valid rule_id are required"))
		return
	}
	version, err := configStore.GetFeatureFlags(r.Context(), scope, 0)
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	if version.Version != *req.ExpectedVersion {
		writeFeatureFlagError(w, state.ErrConflict)
		return
	}
	flagIndex, ruleIndex := -1, -1
	for i := range version.Flags {
		if version.Flags[i].Key != r.PathValue("key") {
			continue
		}
		flagIndex = i
		for j := range version.Flags[i].Rules {
			if version.Flags[i].Rules[j].ID == req.RuleID {
				ruleIndex = j
				break
			}
		}
		break
	}
	if flagIndex < 0 || ruleIndex < 0 || version.Flags[flagIndex].Rules[ruleIndex].Progression == nil {
		writeFeatureFlagError(w, state.ErrNotFound)
		return
	}
	flag := version.Flags[flagIndex]
	if !flag.Enabled {
		api.WriteProblem(w, api.ErrValidation("disabled flags cannot advance a rollout"))
		return
	}
	rule := flag.Rules[ruleIndex]
	progression := *rule.Progression
	response := featureFlagRolloutPromotionResponse{
		Status: "held", Flag: flag.Key, RuleID: rule.ID,
		ConfigVersion: version.Version, CurrentStage: progression.CurrentStage + 1,
		StageCount: len(progression.Stages), RolloutBasisPoints: *rule.Rollout,
		MinimumUsedRequests:           progression.MinimumUsedRequests,
		MaximumHTTP5xxRateBasisPoints: progression.MaximumHTTP5xxRateBasisPoints,
		MaximumP95LatencyMS:           progression.MaximumP95LatencyMS,
	}
	if progression.CurrentStage+1 >= len(progression.Stages) {
		response.Status, response.Reason = "complete", "all_stages_complete"
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, response)
		return
	}
	nextRollout := progression.Stages[progression.CurrentStage+1]
	response.NextRolloutBasisPoints = &nextRollout

	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	window := time.Duration(progression.WindowSeconds) * time.Second
	if retention <= 0 || window > retention {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Rollout evidence unavailable", "the configured evidence window exceeds this account's debugger retention"))
		return
	}
	outcomeRequest := featureFlagRolloutOutcomeRequest(r, window, rule.ID, version.Version)
	params, start, end, err := featureFlagOutcomeQuery(outcomeRequest, scope, retention)
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, scope.ProjectID)
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	for _, app := range apps {
		if app.PreviewOfSlug == "" {
			params.AppIds = append(params.AppIds, stringToPgUUID(app.ID))
		}
	}
	evidenceStore, ok := s.store.(state.FeatureFlagEvidenceStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("rollout promotion requires retained request evidence"))
		return
	}
	rows, err := evidenceStore.FeatureFlagRequestOutcomes(r.Context(), params)
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	response.WindowStart, response.WindowEnd = &start, &end
	for _, row := range rows {
		if row.DecisionType == "boolean" && row.DecisionValue == "true" {
			response.RequestCount = row.RequestCount
			response.UsedCount = row.UsedCount
			response.HTTP5xxCount = row.ErrorCount
			response.P95LatencyMS = row.P95LatencyMs
			response.LatencyQuantized = true
			if row.RequestCount > 0 {
				response.HTTP5xxRate = float64(row.ErrorCount) / float64(row.RequestCount)
			}
			break
		}
	}
	switch {
	case response.UsedCount < progression.MinimumUsedRequests:
		response.Reason = "insufficient_used_requests"
	case response.HTTP5xxRate > float64(progression.MaximumHTTP5xxRateBasisPoints)/10000:
		response.Reason = "http_5xx_rate_exceeded"
	case int(response.P95LatencyMS) > progression.MaximumP95LatencyMS:
		response.Reason = "p95_latency_exceeded"
	default:
		response.Status = "promoted"
	}
	if response.Status == "held" {
		s.audit.Emit(r.Context(), "flags.rollout_held", &scope.AccountID, map[string]any{
			"project_id": scope.ProjectID, "environment_id": scope.EnvironmentID,
			"flag": flag.Key, "rule_id": rule.ID, "config_version": version.Version,
			"reason": response.Reason, "used_count": response.UsedCount,
			"request_count": response.RequestCount, "http_5xx_rate": response.HTTP5xxRate,
			"p95_latency_ms": response.P95LatencyMS,
		})
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, response)
		return
	}
	progression.CurrentStage++
	rule.Progression.CurrentStage = progression.CurrentStage
	*rule.Rollout = nextRollout
	flag.Rules[ruleIndex] = rule
	version.Flags[flagIndex] = flag
	updated, err := configStore.UpdateFeatureFlags(r.Context(), state.FeatureFlagUpdate{
		Scope: scope, ExpectedVersion: version.Version, Config: version.Config, Actor: acct.ID,
	})
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	response.ConfigVersion = updated.Version
	response.CurrentStage = progression.CurrentStage + 1
	response.RolloutBasisPoints = nextRollout
	response.NextRolloutBasisPoints = nil
	if nextStage := progression.CurrentStage + 1; nextStage < len(progression.Stages) {
		response.NextRolloutBasisPoints = &progression.Stages[nextStage]
	}
	s.audit.Emit(r.Context(), "flags.rollout_promoted", &scope.AccountID, map[string]any{
		"project_id": scope.ProjectID, "environment_id": scope.EnvironmentID,
		"flag": flag.Key, "rule_id": rule.ID, "from_version": version.Version,
		"version": updated.Version, "stage": response.CurrentStage,
		"rollout_basis_points": nextRollout, "used_count": response.UsedCount,
		"request_count": response.RequestCount, "http_5xx_rate": response.HTTP5xxRate,
		"p95_latency_ms": response.P95LatencyMS,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}
