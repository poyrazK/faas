package outbound

// adr: 1431 — outboundd finds live access tokens by hash only; an open
// batch's reservation counts in admission until it settles; evaluations are
// claimed with replayable captures, finished once, and start their canary
// only on a pass for an unchanged target.

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresAIAccessTokens(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	unbound := f.secondAIIntegration(api.OutboundAIProviderOpenAI)
	issue := func(name string, integrations []string, expires time.Time) ([32]byte, state.OutboundAIAccessToken, error) {
		secret := api.OutboundAIAccessTokenPrefix + uuid.NewString() + "0123456"
		hash := sha256.Sum256([]byte(secret))
		token, err := f.store.CreateOutboundAIAccessToken(ctx, state.OutboundAIAccessToken{ID: uuid.NewString(), AccountID: f.account.ID,
			AppID: f.app.ID, Name: name, Prefix: secret[:12], TokenHash: hash[:], IntegrationIDs: integrations, ExpiresAt: expires})
		return hash, token, err
	}
	later := time.Now().Add(time.Hour)
	hash, created, err := issue("ci", []string{f.offer.ID}, later)
	if err != nil || created.TokenHash != nil || created.CreatedAt.IsZero() {
		t.Fatalf("create = %+v, %v", created, err)
	}
	found, err := backend.AIAccessToken(ctx, hash)
	if _, listed := found.IntegrationIDs[f.offer.ID]; err != nil || found.ID != created.ID || found.AppID != f.app.ID ||
		found.AccountID != f.account.ID || !listed || len(found.IntegrationIDs) != 1 {
		t.Fatalf("lookup = %+v, %v", found, err)
	}
	if !found.allows(Integration{ID: f.offer.ID, AccountID: f.account.ID, OwnerKind: IntegrationOwnerCustomer,
		ProviderAuthMode: ProviderAuthManaged, AIProvider: api.OutboundAIProviderOpenAI}) {
		t.Fatal("a token did not allow its listed integration")
	}
	if _, _, err := issue("ci", nil, later); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("a duplicate live name = %v", err)
	}
	if _, _, err := issue("unbound", []string{unbound.ID}, later); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("an integration not bound to the app = %v", err)
	}
	if err := backend.TouchAIAccessToken(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	listed, err := f.store.ListOutboundAIAccessTokens(ctx, f.account.ID, f.app.ID)
	if err != nil || len(listed) != 1 || listed[0].LastUsedAt == nil || listed[0].TokenHash != nil || listed[0].Prefix != created.Prefix {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	if err := f.store.RevokeOutboundAIAccessToken(ctx, uuid.NewString(), f.app.ID, created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account revoked the token: %v", err)
	}
	if err := f.store.RevokeOutboundAIAccessToken(ctx, f.account.ID, f.app.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.AIAccessToken(ctx, hash); !errors.Is(err, ErrAIAccessTokenNotFound) {
		t.Fatalf("a revoked token = %v", err)
	}
	if _, _, err := issue("ci", nil, later); err != nil {
		t.Fatalf("a revoked token's name was not reusable: %v", err)
	}
	expiredHash, _, err := issue("expiring", nil, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_access_tokens SET created_at = now() - interval '40 days',
		expires_at = now() - interval '35 days' WHERE name = 'expiring'`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.AIAccessToken(ctx, expiredHash); !errors.Is(err, ErrAIAccessTokenNotFound) {
		t.Fatalf("an expired token = %v", err)
	}
	if _, err := backend.PruneAIUsage(ctx); err != nil {
		t.Fatal(err)
	}
	if listed, err := f.store.ListOutboundAIAccessTokens(ctx, f.account.ID, f.app.ID); err != nil || len(listed) != 2 {
		t.Fatalf("after prune = %+v, %v", listed, err)
	}
	for index := range api.OutboundAIAccessTokensPerApp - 1 {
		if _, _, err := issue("bulk-"+string(rune('a'+index)), nil, later); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := issue("one-too-many", nil, later); !errors.Is(err, state.ErrOutboundAIAccessTokenLimit) {
		t.Fatalf("a token past the limit = %v", err)
	}
}

func TestPostgresAIBatchReservationCountsInAdmission(t *testing.T) {
	limit := int64(100_000)
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{DailyTokenLimit: &limit})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	handler := f.handler(nil, nil)
	batch := AIBatch{ID: uuid.NewString(), IntegrationID: f.offer.ID, AccountID: f.account.ID, Caller: f.app.ID,
		BindingAppID: f.app.ID, Provider: api.OutboundAIProviderOpenAI, Path: "/v1/batches", Model: "gpt-4o",
		Requests: 1000, ReservedTokens: limit - 100, ReservedCostMillicents: 500}
	if err := backend.ReserveAIBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if got := f.call(ctx, handler, `{"model":"gpt-4o","messages":[]}`); got.Code != http.StatusTooManyRequests {
		t.Fatalf("a call under a pending batch's reservation = %d %s", got.Code, got.Body.String())
	}
	if claimed, err := backend.ClaimAIBatches(ctx, 10); err != nil || len(claimed) != 0 {
		t.Fatalf("a pending batch was claimed: %+v, %v", claimed, err)
	}
	if err := backend.ConfirmAIBatch(ctx, batch.ID, "batch_abc"); err != nil {
		t.Fatal(err)
	}
	// The first check waits one poll interval.
	if early, err := backend.ClaimAIBatches(ctx, 10); err != nil || len(early) != 0 {
		t.Fatalf("a just-created batch was checked: %+v, %v", early, err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_batches SET next_check_at = now() WHERE id = $1`, batch.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := backend.ClaimAIBatches(ctx, 10)
	if err != nil || len(claimed) != 1 || claimed[0].ProviderBatchID != "batch_abc" || claimed[0].ReservedTokens != limit-100 ||
		claimed[0].Path != "/v1/batches" || claimed[0].Model != "gpt-4o" || claimed[0].BindingAppID != f.app.ID {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if again, err := backend.ClaimAIBatches(ctx, 10); err != nil || len(again) != 0 {
		t.Fatalf("a batch was claimed again before its poll interval: %+v, %v", again, err)
	}
	if got := f.call(ctx, handler, `{"model":"gpt-4o","messages":[]}`); got.Code != http.StatusTooManyRequests {
		t.Fatalf("a call under an open batch's reservation = %d", got.Code)
	}
	if err := settleAIBatchAtReservation(ctx, backend, AIBatch{ID: batch.ID, IntegrationID: f.offer.ID, Caller: f.app.ID,
		Model: "gpt-4o", ReservedTokens: 10, ReservedCostMillicents: 1}); err != nil {
		t.Fatal(err)
	}
	if err := backend.SettleAIBatch(ctx, batch.ID, AIUsageOutcomeUnknown, 10, 1); err != nil {
		t.Fatal(err)
	}
	if got := f.call(ctx, handler, `{"model":"gpt-4o","messages":[]}`); got.Code != http.StatusOK {
		t.Fatalf("a call after the batch settled = %d %s", got.Code, got.Body.String())
	}
	// A pending batch the provider never confirmed expires.
	stale := AIBatch{ID: uuid.NewString(), IntegrationID: f.offer.ID, AccountID: f.account.ID, Caller: AIUsageCallerRuns,
		Provider: api.OutboundAIProviderOpenAI, Path: "/v1/batches", Model: "gpt-4o", ReservedTokens: 5}
	if err := backend.ReserveAIBatch(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_batches SET created_at = now() - interval '2 hours' WHERE id = $1`, stale.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_batches SET settled_at = now() - interval '8 days' WHERE id = $1`, batch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ClaimAIBatches(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.PruneAIUsage(ctx); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM outbound_ai_batches WHERE integration_id = $1`, f.offer.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("expired and old batches left %d rows: %v", remaining, err)
	}
}

func TestPostgresAIEvaluationLifecycle(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{RequestLogEnabled: true, RequestLogContent: true})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	candidate := f.secondAIIntegration(api.OutboundAIProviderOpenAI)
	route, err := f.store.CreateOutboundAIRoute(ctx, state.OutboundAIRoute{ID: uuid.NewString(), AccountID: f.account.ID, Name: "chat",
		Targets: []state.OutboundAIRouteTarget{{IntegrationID: f.offer.ID}, {IntegrationID: candidate.ID, Model: "gpt-new"}}})
	if err != nil {
		t.Fatal(err)
	}
	settle := func(caller string, status int, content *AIRequestContent) {
		t.Helper()
		ttfb := int64(70)
		if err := backend.RecordAIUsage(ctx, AIUsageRecord{IntegrationID: f.offer.ID, Caller: caller, Outcome: AIUsageOutcomeKnown,
			Usage: AIUsage{Model: "gpt-4o", Known: true, InputTokens: 10, OutputTokens: 2}, CostMillicents: 4,
			Log: &AIRequestLog{Status: status, DurationMS: 40, TTFBMS: &ttfb, Attempts: 1, Content: content}}); err != nil {
			t.Fatal(err)
		}
	}
	chat := func(text string) *AIRequestContent {
		return &AIRequestContent{RequestPath: "/v1/chat/completions", Request: []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"` + text + `"}]}`),
			Response: []byte(`{"choices":[{"message":{"content":"answer"}}]}`), ResponseContentType: "application/json"}
	}
	settle(f.app.ID, http.StatusOK, chat("first"))
	settle(f.app.ID, http.StatusOK, chat("second"))
	settle(f.app.ID, http.StatusBadRequest, chat("refused"))
	settle(AIUsageCallerReplay, http.StatusOK, chat("a replay"))
	settle(f.app.ID, http.StatusOK, &AIRequestContent{RequestPath: "/v1/embeddings", Request: []byte(`{"input":"x"}`),
		Response: []byte(`{}`), ResponseContentType: "application/json"})

	invalid := []state.OutboundAIEvaluation{
		{AccountID: f.account.ID, RouteID: route.ID, Position: 3, JudgeIntegrationID: f.offer.ID, JudgeModel: "j", SampleSize: 5, MaxLossRate: 20},
		{AccountID: f.account.ID, RouteID: route.ID, Position: 2, JudgeIntegrationID: uuid.NewString(), JudgeModel: "j", SampleSize: 5, MaxLossRate: 20},
	}
	for _, evaluation := range invalid {
		if _, err := f.store.CreateOutboundAIEvaluation(ctx, evaluation); !errors.Is(err, state.ErrInvalidArgument) {
			t.Errorf("invalid evaluation %+v = %v", evaluation, err)
		}
	}
	if _, err := f.store.CreateOutboundAIEvaluation(ctx, state.OutboundAIEvaluation{AccountID: uuid.NewString(), RouteID: route.ID,
		Position: 2, JudgeIntegrationID: f.offer.ID, JudgeModel: "j", SampleSize: 5}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account's route = %v", err)
	}
	canary := &api.PutOutboundAIRouteCanaryRequest{Target: 2, Percent: 10}
	normalized := canary.Normalized()
	created, err := f.store.CreateOutboundAIEvaluation(ctx, state.OutboundAIEvaluation{AccountID: f.account.ID, RouteID: route.ID,
		Position: 2, JudgeIntegrationID: f.offer.ID, JudgeModel: "judge", SampleSize: 5, MaxLossRate: 50, Canary: &normalized})
	if err != nil || created.State != api.OutboundAIEvaluationPending || created.Canary == nil || created.Canary.Percent != 10 {
		t.Fatalf("create = %+v, %v", created, err)
	}
	job, ok, err := backend.ClaimAIEvaluation(ctx)
	if err != nil || !ok || job.ID != created.ID || job.Position != 2 || job.Baseline.IntegrationID != f.offer.ID ||
		job.Candidate != (AIRouteTarget{IntegrationID: candidate.ID, Model: "gpt-new"}) || job.JudgeModel != "judge" || len(job.Samples) != 2 {
		t.Fatalf("claim = %+v, %v, %v", job, ok, err)
	}
	if sample := job.Samples[0]; !strings.Contains(string(sample.RequestBody), "second") || sample.CostMillicents != 4 || sample.TTFBMS == nil ||
		*sample.TTFBMS != 70 || sample.ResponseContentType != "application/json" {
		t.Fatalf("latest sample = %+v", sample)
	}
	if _, again, err := backend.ClaimAIEvaluation(ctx); again || err != nil {
		t.Fatalf("a running evaluation was claimed again: %v, %v", again, err)
	}
	ttfb := int64(55)
	items := []AIEvaluationItem{
		{LogID: job.Samples[0].LogID, Verdict: api.OutboundAIEvaluationItemWin, CandidateStatus: 200, CandidateCostMillicents: 2,
			CandidateTTFBMS: &ttfb, BaselineCostMillicents: 4, BaselineTTFBMS: job.Samples[0].TTFBMS, BaselineOutputText: "answer", CandidateOutputText: "better"},
		{LogID: job.Samples[1].LogID, Verdict: api.OutboundAIEvaluationItemLoss, CandidateStatus: 200, BaselineCostMillicents: 4},
	}
	verdict, err := backend.FinishAIEvaluation(ctx, job, items, "")
	if err != nil || !verdict.Pass || !verdict.CanaryStarted {
		t.Fatalf("finish = %+v, %v", verdict, err)
	}
	if again, err := backend.FinishAIEvaluation(ctx, job, items, ""); err != nil || again.CanaryStarted {
		t.Fatalf("a finished evaluation was finished again: %+v, %v", again, err)
	}
	stored, err := f.store.GetOutboundAIEvaluation(ctx, f.account.ID, created.ID)
	if err != nil || stored.State != api.OutboundAIEvaluationDone || stored.Verdict != api.OutboundAIEvaluationPass || !stored.CanaryStarted ||
		stored.Samples != 2 || stored.Wins != 1 || stored.Losses != 1 || stored.BaselineCostMillicents != 8 || stored.CandidateCostMillicents != 2 ||
		stored.BaselineTTFBMSTotal != 70 || stored.BaselineTTFBCalls != 1 || stored.CandidateTTFBMSTotal != 55 || len(stored.Items) != 2 ||
		stored.Items[0].CandidateOutputText != "better" || *stored.Items[0].CandidateStatus != 200 || stored.FinishedAt == nil {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	withCanary, err := f.store.GetOutboundAIRoute(ctx, f.account.ID, route.ID)
	if err != nil || withCanary.Canary == nil || withCanary.Canary.Position != 2 || withCanary.Canary.Percent != 10 ||
		withCanary.Canary.State != api.OutboundAIRouteCanaryActive {
		t.Fatalf("route canary = %+v, %v", withCanary.Canary, err)
	}

	// A second passing evaluation leaves the active canary alone, and one
	// whose target changed starts nothing.
	second, err := f.store.CreateOutboundAIEvaluation(ctx, state.OutboundAIEvaluation{AccountID: f.account.ID, RouteID: route.ID,
		Position: 2, JudgeIntegrationID: f.offer.ID, JudgeModel: "judge", SampleSize: 1, MaxLossRate: 0, Canary: &normalized})
	if err != nil {
		t.Fatal(err)
	}
	job, ok, err = backend.ClaimAIEvaluation(ctx)
	if err != nil || !ok || job.ID != second.ID || len(job.Samples) != 1 {
		t.Fatalf("second claim = %+v, %v, %v", job, ok, err)
	}
	if verdict, err := backend.FinishAIEvaluation(ctx, job, items[:1], ""); err != nil || !verdict.Pass || verdict.CanaryStarted {
		t.Fatalf("a pass under an active canary = %+v, %v", verdict, err)
	}
	if err := f.store.DeleteOutboundAIRouteCanary(ctx, f.account.ID, route.ID); err != nil {
		t.Fatal(err)
	}
	third, err := f.store.CreateOutboundAIEvaluation(ctx, state.OutboundAIEvaluation{AccountID: f.account.ID, RouteID: route.ID,
		Position: 2, JudgeIntegrationID: f.offer.ID, JudgeModel: "judge", SampleSize: 1, MaxLossRate: 0, Canary: &normalized})
	if err != nil {
		t.Fatal(err)
	}
	job, _, err = backend.ClaimAIEvaluation(ctx)
	if err != nil || job.ID != third.ID {
		t.Fatalf("third claim = %+v, %v", job, err)
	}
	job.Candidate.Model = "gpt-changed"
	if verdict, err := backend.FinishAIEvaluation(ctx, job, items[:1], ""); err != nil || verdict.CanaryStarted {
		t.Fatalf("a pass for a changed target = %+v, %v", verdict, err)
	}

	// Failures and interruptions.
	fourth, err := f.store.CreateOutboundAIEvaluation(ctx, state.OutboundAIEvaluation{AccountID: f.account.ID, RouteID: route.ID,
		Position: 2, JudgeIntegrationID: f.offer.ID, JudgeModel: "judge", SampleSize: 1, MaxLossRate: 0})
	if err != nil {
		t.Fatal(err)
	}
	job, _, err = backend.ClaimAIEvaluation(ctx)
	if err != nil || job.ID != fourth.ID {
		t.Fatalf("fourth claim = %+v, %v", job, err)
	}
	if _, err := backend.FinishAIEvaluation(ctx, job, nil, "outbound_ai_evaluation_judge_unavailable"); err != nil {
		t.Fatal(err)
	}
	if failed, err := f.store.GetOutboundAIEvaluation(ctx, f.account.ID, fourth.ID); err != nil || failed.State != api.OutboundAIEvaluationFailed ||
		failed.ErrorCode != "outbound_ai_evaluation_judge_unavailable" || failed.Verdict != "" {
		t.Fatalf("failed = %+v, %v", failed, err)
	}
	fifth, err := f.store.CreateOutboundAIEvaluation(ctx, state.OutboundAIEvaluation{AccountID: f.account.ID, RouteID: route.ID,
		Position: 2, JudgeIntegrationID: f.offer.ID, JudgeModel: "judge", SampleSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := backend.ClaimAIEvaluation(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_evaluations SET started_at = now() - interval '2 hours' WHERE id = $1`, fifth.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := backend.ClaimAIEvaluation(ctx); ok || err != nil {
		t.Fatalf("claim with none pending = %v, %v", ok, err)
	}
	if interrupted, err := f.store.GetOutboundAIEvaluation(ctx, f.account.ID, fifth.ID); err != nil ||
		interrupted.ErrorCode != "outbound_ai_evaluation_interrupted" {
		t.Fatalf("interrupted = %+v, %v", interrupted, err)
	}
	if listed, err := f.store.ListOutboundAIEvaluations(ctx, f.account.ID, route.ID); err != nil || len(listed) != 5 ||
		listed[0].ID != fifth.ID || listed[0].Items != nil {
		t.Fatalf("list = %d, %v", len(listed), err)
	}

	// The daily limit counts the route's evaluations of the UTC day.
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO outbound_ai_evaluations (id, account_id, route_id, position, judge_integration_id, judge_model, sample_size, max_loss_rate)
		SELECT gen_random_uuid(), $1, $2, 2, $3, 'judge', 1, 0 FROM generate_series(6, $4::int)`,
		f.account.ID, route.ID, f.offer.ID, api.OutboundAIEvaluationsPerDay); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateOutboundAIEvaluation(ctx, state.OutboundAIEvaluation{AccountID: f.account.ID, RouteID: route.ID,
		Position: 2, JudgeIntegrationID: f.offer.ID, JudgeModel: "judge", SampleSize: 1}); !errors.Is(err, state.ErrOutboundAIEvaluationLimit) {
		t.Fatalf("an evaluation past the daily limit = %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_evaluations SET created_at = now() - interval '31 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.PruneAIUsage(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetOutboundAIEvaluation(ctx, f.account.ID, created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("an evaluation past retention = %v", err)
	}
}
