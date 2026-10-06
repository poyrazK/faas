package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type rolloutEvidenceStore struct {
	*state.MemStore
	rows  []sqlc.FeatureFlagRequestOutcomesRow
	query sqlc.FeatureFlagRequestOutcomesParams
}

type rolloutStageClockStore struct {
	*rolloutEvidenceStore
	stageStartedAt time.Time
}

func (s *rolloutStageClockStore) GetFeatureFlags(ctx context.Context, scope state.FeatureFlagScope, version int64) (state.FeatureFlagVersion, error) {
	v, err := s.rolloutEvidenceStore.GetFeatureFlags(ctx, scope, version)
	if err == nil && version == 0 {
		v.CreatedAt = s.stageStartedAt
	}
	return v, err
}

func (s *rolloutEvidenceStore) FeatureFlagRequestOutcomes(_ context.Context, query sqlc.FeatureFlagRequestOutcomesParams) ([]sqlc.FeatureFlagRequestOutcomesRow, error) {
	s.query = query
	return s.rows, nil
}

func (s *rolloutEvidenceStore) ListFeatureFlagRequestEvidence(context.Context, sqlc.ListFeatureFlagRequestEvidenceParams) ([]sqlc.ListFeatureFlagRequestEvidenceRow, error) {
	return nil, nil
}

func setupProgressiveRollout(t *testing.T, evidence []sqlc.FeatureFlagRequestOutcomesRow) (testEnv, state.FeatureFlagScope, *rolloutEvidenceStore) {
	t.Helper()
	e := setup(t, api.PlanPro)
	e.s.featureFlagsEnabled = true
	ctx := context.Background()
	project, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "rollouts", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	env, err := e.store.ProjectEnvironmentBySlug(ctx, e.acct.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "rollout-api", Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	scope := state.FeatureFlagScope{AccountID: e.acct.ID, ProjectID: project.ID, EnvironmentID: env.ID}
	rollout := 100
	config := flags.Config{Flags: []flags.Flag{{
		Key: "new-export", Enabled: true,
		Rules: []flags.Rule{{ID: "selected", Rollout: &rollout, Value: true, Progression: &flags.ProgressiveRollout{
			Stages: []int{100, 1000, 10000}, CurrentStage: 0,
			MinimumUsedRequests: 10, MaximumHTTP5xxRateBasisPoints: 200,
			MaximumP95LatencyMS: 500, WindowSeconds: 300,
		}}},
	}}}
	if _, err := e.store.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, Config: config, Actor: e.acct.ID}); err != nil {
		t.Fatal(err)
	}
	evidenceStore := &rolloutEvidenceStore{MemStore: e.store, rows: evidence}
	e.s.store = evidenceStore
	return e, scope, evidenceStore
}

func TestFeatureFlagRolloutPromotionAdvancesOnlyTargetRule(t *testing.T) {
	e, scope, evidenceStore := setupProgressiveRollout(t, []sqlc.FeatureFlagRequestOutcomesRow{
		{DecisionType: "boolean", DecisionValue: "true", RequestCount: 50, UsedCount: 50, ErrorCount: 0, P95LatencyMs: 300},
	})
	path := "/v1/projects/rollouts/environments/production/flags/new-export/rollout/promote"
	one := int64(1)
	res := e.do(t, http.MethodPost, path, promoteFeatureFlagRolloutRequest{ExpectedVersion: &one, RuleID: "selected"}, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("promote: %d %s", res.Code, res.Body.String())
	}
	var response featureFlagRolloutPromotionResponse
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "promoted" || response.ConfigVersion != 2 || response.CurrentStage != 2 || response.RolloutBasisPoints != 1000 || response.NextRolloutBasisPoints == nil || *response.NextRolloutBasisPoints != 10000 {
		t.Fatalf("promotion response=%+v", response)
	}
	if evidenceStore.query.FlagKey != "new-export" || evidenceStore.query.RuleID != "selected" || evidenceStore.query.ConfigVersion != 1 || evidenceStore.query.ReceivedUntil.Time.Sub(evidenceStore.query.ReceivedFrom.Time) != 5*time.Minute || len(evidenceStore.query.AppIds) != 1 {
		t.Fatalf("evidence query=%+v", evidenceStore.query)
	}
	current, err := e.store.GetFeatureFlags(context.Background(), scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	rule := current.Flags[0].Rules[0]
	if current.Version != 2 || rule.Progression.CurrentStage != 1 || *rule.Rollout != 1000 {
		t.Fatalf("published progression=%+v version=%d", rule, current.Version)
	}
	if res = e.do(t, http.MethodPost, path, promoteFeatureFlagRolloutRequest{ExpectedVersion: &one, RuleID: "selected"}, nil); res.Code != http.StatusConflict {
		t.Fatalf("stale promotion=%d %s", res.Code, res.Body.String())
	}
}

func TestFeatureFlagRolloutPromotionHoldsOnMissingOrUnhealthyEvidence(t *testing.T) {
	tests := []struct {
		name   string
		row    sqlc.FeatureFlagRequestOutcomesRow
		reason string
	}{
		{name: "minimum used requests", row: sqlc.FeatureFlagRequestOutcomesRow{DecisionType: "boolean", DecisionValue: "true", RequestCount: 20, UsedCount: 9, P95LatencyMs: 100}, reason: "insufficient_used_requests"},
		{name: "5xx threshold", row: sqlc.FeatureFlagRequestOutcomesRow{DecisionType: "boolean", DecisionValue: "true", RequestCount: 20, UsedCount: 20, ErrorCount: 1, P95LatencyMs: 100}, reason: "http_5xx_rate_exceeded"},
		{name: "p95 threshold", row: sqlc.FeatureFlagRequestOutcomesRow{DecisionType: "boolean", DecisionValue: "true", RequestCount: 20, UsedCount: 20, P95LatencyMs: 501}, reason: "p95_latency_exceeded"},
		{name: "no evidence", reason: "insufficient_used_requests"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rows []sqlc.FeatureFlagRequestOutcomesRow
			if tt.row.DecisionType != "" {
				rows = []sqlc.FeatureFlagRequestOutcomesRow{tt.row}
			}
			e, scope, _ := setupProgressiveRollout(t, rows)
			one := int64(1)
			res := e.do(t, http.MethodPost, "/v1/projects/rollouts/environments/production/flags/new-export/rollout/promote", promoteFeatureFlagRolloutRequest{ExpectedVersion: &one, RuleID: "selected"}, nil)
			if res.Code != http.StatusOK {
				t.Fatalf("promote: %d %s", res.Code, res.Body.String())
			}
			var response featureFlagRolloutPromotionResponse
			if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Status != "held" || response.Reason != tt.reason {
				t.Fatalf("hold response=%+v", response)
			}
			current, err := e.store.GetFeatureFlags(context.Background(), scope, 0)
			if err != nil {
				t.Fatal(err)
			}
			if current.Version != 1 || *current.Flags[0].Rules[0].Rollout != 100 {
				t.Fatalf("held promotion changed configuration: %+v", current)
			}
		})
	}
}

func TestFeatureFlagRolloutPromotionReportsCompletedPlan(t *testing.T) {
	e, scope, evidenceStore := setupProgressiveRollout(t, nil)
	current, err := e.store.GetFeatureFlags(context.Background(), scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	rollout := 10000
	current.Flags[0].Rules[0].Rollout = &rollout
	current.Flags[0].Rules[0].Progression.CurrentStage = 2
	if _, err := e.store.UpdateFeatureFlags(context.Background(), state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: current.Version, Config: current.Config, Actor: e.acct.ID}); err != nil {
		t.Fatal(err)
	}
	two := int64(2)
	res := e.do(t, http.MethodPost, "/v1/projects/rollouts/environments/production/flags/new-export/rollout/promote", promoteFeatureFlagRolloutRequest{ExpectedVersion: &two, RuleID: "selected"}, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("promote complete rollout: %d %s", res.Code, res.Body.String())
	}
	var response featureFlagRolloutPromotionResponse
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "complete" || response.Reason != "all_stages_complete" || response.ConfigVersion != 2 || response.WindowStart != nil || evidenceStore.query.FlagKey != "" {
		t.Fatalf("completed response=%+v query=%+v", response, evidenceStore.query)
	}
}

func setupAutomaticProgressiveRollout(t *testing.T, evidence []sqlc.FeatureFlagRequestOutcomesRow, stageStartedAt time.Time) (testEnv, state.FeatureFlagScope, *rolloutStageClockStore) {
	t.Helper()
	e, scope, evidenceStore := setupProgressiveRollout(t, evidence)
	current, err := e.store.GetFeatureFlags(context.Background(), scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	current.Flags[0].Rules[0].Progression.AutoAdvance = true
	if _, err := e.store.UpdateFeatureFlags(context.Background(), state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: current.Version, Config: current.Config, Actor: e.acct.ID}); err != nil {
		t.Fatal(err)
	}
	clockStore := &rolloutStageClockStore{rolloutEvidenceStore: evidenceStore, stageStartedAt: stageStartedAt}
	e.s.store = clockStore
	return e, scope, clockStore
}

func TestFeatureFlagAutoAdvanceWaitsForFullWindowAndAdvancesOneStage(t *testing.T) {
	now := time.Now().UTC()
	e, scope, evidenceStore := setupAutomaticProgressiveRollout(t, []sqlc.FeatureFlagRequestOutcomesRow{
		{DecisionType: "boolean", DecisionValue: "true", RequestCount: 50, UsedCount: 50, ErrorCount: 0, P95LatencyMs: 300},
	}, now.Add(-4*time.Minute))
	candidate := state.FeatureFlagAutoRolloutCandidate{Scope: scope, ProjectSlug: "rollouts", EnvironmentSlug: "production"}
	if err := e.s.autoAdvanceFeatureFlagRollout(context.Background(), candidate, now); err != nil {
		t.Fatal(err)
	}
	current, err := e.store.GetFeatureFlags(context.Background(), scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 2 || current.Flags[0].Rules[0].Progression.CurrentStage != 0 || *current.Flags[0].Rules[0].Rollout != 100 {
		t.Fatalf("advanced before full evidence window: %+v", current)
	}
	if evidenceStore.query.FlagKey != "" {
		t.Fatalf("queried evidence before the full window: %+v", evidenceStore.query)
	}

	evidenceStore.stageStartedAt = now.Add(-5 * time.Minute)
	if err := e.s.autoAdvanceFeatureFlagRollout(context.Background(), candidate, now); err != nil {
		t.Fatal(err)
	}
	current, err = e.store.GetFeatureFlags(context.Background(), scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	rule := current.Flags[0].Rules[0]
	if current.Version != 3 || rule.Progression.CurrentStage != 1 || *rule.Rollout != 1000 || current.Actor != featureFlagAutoAdvanceActor {
		t.Fatalf("automatic promotion=%+v actor=%q", rule, current.Actor)
	}
	if evidenceStore.query.FlagKey != "new-export" || evidenceStore.query.RuleID != "selected" || evidenceStore.query.ConfigVersion != 2 || evidenceStore.query.ReceivedUntil.Time.Sub(evidenceStore.query.ReceivedFrom.Time) != 5*time.Minute {
		t.Fatalf("automatic evidence query=%+v", evidenceStore.query)
	}
}

func TestFeatureFlagAutoAdvanceHoldsOnUnhealthyEvidenceAndDefaultsToManual(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name      string
		rows      []sqlc.FeatureFlagRequestOutcomesRow
		automatic bool
	}{
		{name: "unhealthy evidence", rows: []sqlc.FeatureFlagRequestOutcomesRow{{DecisionType: "boolean", DecisionValue: "true", RequestCount: 20, UsedCount: 20, ErrorCount: 1, P95LatencyMs: 100}}, automatic: true},
		{name: "manual remains default", rows: []sqlc.FeatureFlagRequestOutcomesRow{{DecisionType: "boolean", DecisionValue: "true", RequestCount: 50, UsedCount: 50, P95LatencyMs: 100}}, automatic: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, scope, evidenceStore := setupAutomaticProgressiveRollout(t, tt.rows, now.Add(-5*time.Minute))
			if !tt.automatic {
				current, err := e.store.GetFeatureFlags(context.Background(), scope, 0)
				if err != nil {
					t.Fatal(err)
				}
				current.Flags[0].Rules[0].Progression.AutoAdvance = false
				if _, err := e.store.UpdateFeatureFlags(context.Background(), state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: current.Version, Config: current.Config, Actor: e.acct.ID}); err != nil {
					t.Fatal(err)
				}
			}
			candidate := state.FeatureFlagAutoRolloutCandidate{Scope: scope, ProjectSlug: "rollouts", EnvironmentSlug: "production"}
			if err := e.s.autoAdvanceFeatureFlagRollout(context.Background(), candidate, now); err != nil {
				t.Fatal(err)
			}
			current, err := e.store.GetFeatureFlags(context.Background(), scope, 0)
			if err != nil {
				t.Fatal(err)
			}
			wantVersion := int64(2)
			if !tt.automatic {
				wantVersion = 3
			}
			if current.Version != wantVersion || current.Flags[0].Rules[0].Progression.CurrentStage != 0 || *current.Flags[0].Rules[0].Rollout != 100 {
				t.Fatalf("held rollout changed configuration: %+v", current)
			}
			if tt.automatic && evidenceStore.query.FlagKey != "new-export" {
				t.Fatalf("expected unhealthy rule to be evaluated, query=%+v", evidenceStore.query)
			}
			if !tt.automatic && evidenceStore.query.FlagKey != "" {
				t.Fatalf("manual rule should not be evaluated automatically, query=%+v", evidenceStore.query)
			}
		})
	}
}
