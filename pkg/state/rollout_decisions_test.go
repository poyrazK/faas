package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRolloutDecisionsMem(t *testing.T) { rolloutDecisionSuite(t, state.NewMemStore()) }

func TestRolloutDecisionsPG(t *testing.T) {
	s, _, _ := pgStoreWithPool(t)
	rolloutDecisionSuite(t, s)
}

func encodedDecision(t *testing.T, rule string, step int) json.RawMessage {
	t.Helper()
	d := api.ManualRolloutDecision(rule, "insufficient request samples (candidate=0 stable=3; need 20 each)", step, 25, time.Now())
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func decisionRule(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if len(raw) == 0 {
		return ""
	}
	var d api.RolloutDecision
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("decode decision %s: %v", raw, err)
	}
	return d.Rule
}

// rolloutDecisionSuite pins ADR-1447's storage contract on both stores: an
// advance and an abort record their decision on the deployment and in the
// audit row, holds are deduplicated against the step's newest hold, and the
// rollout.aborted webhook carries an abort decision.
func rolloutDecisionSuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	a, app, predecessor, _ := bindingPromotionFixtureCtx(ctx, t, s)
	started := time.Now().Add(-time.Minute)
	candidate, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "default", TrafficPercent: 1, TrafficPercentExplicit: true, ImageDigest: predecessor.ImageDigest, CanaryPreset: "balanced", CanaryStep: 0, CanaryTotalSteps: 4, CanaryStepStartedAt: &started, RolloutState: "rolling_out"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, candidate.ID, "/test/"+candidate.ID, "test/"+candidate.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, candidate.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	decisions := s.(state.RolloutDecisionStore)
	hook, err := s.CreateAccountReleaseWebhookIfUnderQuota(ctx, state.AppWebhook{
		AccountID: a.ID, Scope: state.AppWebhookScopeAccount,
		TargetURL:    "https://example.com/releases-" + uuid.NewString(),
		SecretSealed: []byte("sealed"), EventFilter: []string{"rollout.aborted"}, Enabled: true,
	}, api.Limits{WebhookPerAccount: 5})
	if err != nil {
		t.Fatal(err)
	}

	current, err := s.DeploymentByID(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	step := current.CanaryStep
	advanced, _, err := s.(state.CanaryAdvancer).AdvanceCanary(ctx, candidate.ID, state.CanaryAdvanceParams{
		ExpectedStep: step, TrafficPercent: 10, Decision: encodedDecision(t, api.RolloutRuleAdvanceProbeEvidence, step),
		Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "test:canary"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := decisionRule(t, advanced.RolloutDecision); got != api.RolloutRuleAdvanceProbeEvidence {
		t.Fatalf("advance decision = %q", got)
	}
	reread, err := s.DeploymentByID(ctx, candidate.ID)
	if err != nil || decisionRule(t, reread.RolloutDecision) != api.RolloutRuleAdvanceProbeEvidence {
		t.Fatalf("stored advance decision = %s, %v", reread.RolloutDecision, err)
	}

	step = advanced.CanaryStep
	base := time.Now().Add(time.Second)
	hold := func(rule string, expectedStep int, offset time.Duration) bool {
		t.Helper()
		recorded, _, err := decisions.RecordRolloutHold(ctx, state.RolloutHoldParams{
			DeploymentID: candidate.ID, ExpectedStep: expectedStep, Rule: rule,
			Decision: encodedDecision(t, rule, expectedStep), Actor: "test:canary", At: base.Add(offset),
		})
		if err != nil {
			t.Fatalf("RecordRolloutHold(%s): %v", rule, err)
		}
		return recorded
	}
	latest := func() string {
		t.Helper()
		raw, err := decisions.LatestRolloutHold(ctx, candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		return decisionRule(t, raw)
	}
	if latest() != "" {
		t.Fatal("hold reported before any was recorded")
	}
	steps := []struct {
		rule     string
		step     int
		offset   time.Duration
		recorded bool
		latest   string
	}{
		{api.RolloutRuleHoldInsufficient, step, 0, true, api.RolloutRuleHoldInsufficient},
		{api.RolloutRuleHoldInsufficient, step, time.Minute, false, api.RolloutRuleHoldInsufficient},
		{api.RolloutRuleHoldSignalUnavailable, step, 2 * time.Minute, true, api.RolloutRuleHoldSignalUnavailable},
		{api.RolloutRuleHoldInsufficient, step, 3 * time.Minute, true, api.RolloutRuleHoldInsufficient},
		{api.RolloutRuleHoldInsufficient, step - 1, 4 * time.Minute, false, api.RolloutRuleHoldInsufficient},
		{api.RolloutRuleHoldInsufficient, step, 3*time.Minute + api.RolloutHoldRefreshInterval + time.Second, true, api.RolloutRuleHoldInsufficient},
	}
	for i, tc := range steps {
		if got := hold(tc.rule, tc.step, tc.offset); got != tc.recorded {
			t.Fatalf("hold %d (%s at step %d) recorded = %v, want %v", i, tc.rule, tc.step, got, tc.recorded)
		}
		if got := latest(); got != tc.latest {
			t.Fatalf("after hold %d latest = %q, want %q", i, got, tc.latest)
		}
	}
	if _, _, err := decisions.RecordRolloutHold(ctx, state.RolloutHoldParams{
		DeploymentID: candidate.ID, ExpectedStep: step, Rule: api.RolloutRuleAbort5xx,
		Decision: encodedDecision(t, api.RolloutRuleAbort5xx, step), Actor: "test:canary", At: base,
	}); err == nil {
		t.Fatal("an abort rule was recorded as a hold")
	}

	aborted, auditID, err := decisions.RecoverRolloutForDeploymentWithDecision(ctx, app.ID, candidate.ID, predecessor.ID, "abort",
		"circuit breaker: 5xx error rate regression; restoring predecessor "+predecessor.ID, encodedDecision(t, api.RolloutRuleAbort5xx, step))
	if err != nil {
		t.Fatal(err)
	}
	if aborted.RolloutState != "aborted" || decisionRule(t, aborted.RolloutDecision) != api.RolloutRuleAbort5xx {
		t.Fatalf("aborted = state %q decision %s", aborted.RolloutState, aborted.RolloutDecision)
	}
	audits, err := s.ListDeploymentAudit(ctx, candidate.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var abortAudit *state.DeploymentAudit
	held := 0
	for i := range audits {
		if audits[i].ID == auditID {
			abortAudit = &audits[i]
		}
		if audits[i].Kind == state.DeployRolloutHeld {
			held++
		}
	}
	if abortAudit == nil {
		t.Fatalf("abort audit %d not listed", auditID)
	}
	var abortData struct {
		Decision json.RawMessage `json:"decision"`
	}
	if err := json.Unmarshal(abortAudit.Data, &abortData); err != nil || decisionRule(t, abortData.Decision) != api.RolloutRuleAbort5xx {
		t.Fatalf("abort audit data = %s, %v", abortAudit.Data, err)
	}
	if held != 4 {
		t.Fatalf("hold audit rows = %d, want 4", held)
	}
	if latest() != "" {
		t.Fatal("an aborted rollout still reports a hold")
	}
	if hold(api.RolloutRuleHoldInsufficient, step, 20*time.Minute) {
		t.Fatal("a hold was recorded for an aborted rollout")
	}

	deliveries, _, err := s.ListAccountReleaseWebhookDeliveries(ctx, a.ID, hook.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("rollout.aborted deliveries = %d, want 1", len(deliveries))
	}
	var payload api.RolloutAbortedWebhookPayload
	if err := json.Unmarshal(deliveries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Decision == nil || payload.Decision.Rule != api.RolloutRuleAbort5xx {
		t.Fatalf("rollout.aborted payload = %s", deliveries[0].Payload)
	}
}

func TestRolloutCompletedDecisionMem(t *testing.T) {
	rolloutCompletedDecisionCase(t, state.NewMemStore())
}

func TestRolloutCompletedDecisionPG(t *testing.T) {
	s, _, _ := pgStoreWithPool(t)
	rolloutCompletedDecisionCase(t, s)
}

// rolloutCompletedDecisionCase pins that the advance which completes a
// rollout puts its decision on the rollout.completed webhook (ADR-1447).
func rolloutCompletedDecisionCase(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	a, app, predecessor, _ := bindingPromotionFixtureCtx(ctx, t, s)
	started := time.Now().Add(-time.Minute)
	candidate, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "default", TrafficPercent: 50, TrafficPercentExplicit: true, ImageDigest: predecessor.ImageDigest, CanaryPreset: "balanced", CanaryStep: 2, CanaryTotalSteps: 3, CanaryStepStartedAt: &started, RolloutState: "rolling_out"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, candidate.ID, "/test/"+candidate.ID, "test/"+candidate.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, candidate.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	hook, err := s.CreateAccountReleaseWebhookIfUnderQuota(ctx, state.AppWebhook{
		AccountID: a.ID, Scope: state.AppWebhookScopeAccount,
		TargetURL:    "https://example.com/releases-" + uuid.NewString(),
		SecretSealed: []byte("sealed"), EventFilter: []string{"rollout.completed"}, Enabled: true,
	}, api.Limits{WebhookPerAccount: 5})
	if err != nil {
		t.Fatal(err)
	}
	completed, _, err := s.(state.CanaryAdvancer).AdvanceCanary(ctx, candidate.ID, state.CanaryAdvanceParams{
		ExpectedStep: 2, TrafficPercent: 100, Decision: encodedDecision(t, api.RolloutRuleAdvanceHealthy, 2),
		Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "test:canary"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.RolloutState != "complete" {
		t.Fatalf("rollout state = %q, want complete", completed.RolloutState)
	}
	deliveries, _, err := s.ListAccountReleaseWebhookDeliveries(ctx, a.ID, hook.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("rollout.completed deliveries = %d, want 1", len(deliveries))
	}
	var payload api.RolloutCompletedWebhookPayload
	if err := json.Unmarshal(deliveries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Decision == nil || payload.Decision.Rule != api.RolloutRuleAdvanceHealthy {
		t.Fatalf("rollout.completed payload = %s", deliveries[0].Payload)
	}
}
