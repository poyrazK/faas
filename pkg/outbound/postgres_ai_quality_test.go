package outbound

// adr: 20261021171712 — Postgres logs a call under the gateway's call ID
// with its quality flags and counts them per day; moves the day's ratings
// when an app rates a call again; samples captured calls for online
// evaluation, claims them once and counts each score once; and records a
// drift alert once, notifying the bound app's webhook.

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresAIQualitySignals(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{RequestLogEnabled: true, RequestLogContent: true})
	ctx := context.Background()
	percent := int32(100)
	if err := f.store.SetOutboundAIPolicy(ctx, f.account.ID, f.offer.ID, state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI,
		RequestLogEnabled: true, RequestLogContent: true, QualityAlerts: true, OnlineEvalPercent: &percent,
		OnlineEvalJudgeIntegrationID: f.offer.ID, OnlineEvalModel: "gpt-4o-mini", OnlineEvalRubric: "Name one city."}); err != nil {
		t.Fatal(err)
	}
	integration, err := f.integration(f.offer.ID)
	if err != nil || integration.AIOnlineEvalPercent != 100 {
		t.Fatalf("resolved online evaluation = %d, %v", integration.AIOnlineEvalPercent, err)
	}
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	callID := uuid.NewString()
	record := AIUsageRecord{IntegrationID: f.offer.ID, Caller: f.app.ID, Outcome: AIUsageOutcomeKnown, CallID: callID,
		QualityFlags: []string{AIQualityTruncated}, EvaluationSampled: true,
		Usage: AIUsage{Model: "gpt-4o", Known: true, InputTokens: 20, OutputTokens: 10}, CostMillicents: 40,
		Log: &AIRequestLog{Status: http.StatusOK, Attempts: 1, Content: &AIRequestContent{
			Request:  []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Capital of France?"}]}`),
			Response: []byte(`{"choices":[{"message":{"content":"Paris"}}]}`), ResponseContentType: "application/json"}}}
	if _, err := backend.RecordAIUsageAlerts(ctx, record); err != nil {
		t.Fatal(err)
	}
	logged, err := f.store.ListOutboundAIRequestLog(ctx, f.account.ID, f.offer.ID, nil, 10)
	if err != nil || len(logged) != 1 || logged[0].ID != callID || len(logged[0].QualityFlags) != 1 || logged[0].QualityFlags[0] != "truncated" {
		t.Fatalf("request log = %+v, %v", logged, err)
	}
	usageItem := func() state.OutboundAIUsageItem {
		t.Helper()
		usage, err := f.store.GetOutboundAIUsage(ctx, f.account.ID, f.offer.ID, "")
		if err != nil || len(usage.Items) != 1 {
			t.Fatalf("usage = %+v, %v", usage, err)
		}
		return usage.Items[0]
	}
	if item := usageItem(); item.TruncatedRequests != 1 || item.EmptyRequests != 0 {
		t.Fatalf("usage item = %+v", item)
	}
	// The call ID keys the settlement: a retry of one that committed counts
	// nothing again.
	if _, err := backend.RecordAIUsageAlerts(ctx, record); err != nil {
		t.Fatalf("a repeated settlement = %v", err)
	}
	if item := usageItem(); item.Requests != 1 || item.TruncatedRequests != 1 {
		t.Fatalf("a repeated settlement counted again: %+v", item)
	}

	// Ratings: a second rating moves the day's count; another app cannot
	// rate the call.
	rate := func(caller, rating string) error {
		return backend.RecordAIFeedback(ctx, AIFeedback{IntegrationID: f.offer.ID, Caller: caller, CallID: callID, Rating: rating})
	}
	if err := rate(f.app.ID, "bad"); err != nil {
		t.Fatal(err)
	}
	if item := usageItem(); item.BadFeedback != 1 || item.GoodFeedback != 0 {
		t.Fatalf("after bad = %+v", item)
	}
	if err := rate(f.app.ID, "good"); err != nil {
		t.Fatal(err)
	}
	if err := rate(f.app.ID, "good"); err != nil {
		t.Fatal(err)
	}
	if item := usageItem(); item.BadFeedback != 0 || item.GoodFeedback != 1 {
		t.Fatalf("after good = %+v", item)
	}
	if err := rate(uuid.NewString(), "bad"); !errors.Is(err, ErrAICallNotFound) {
		t.Fatalf("another app's rating = %v", err)
	}
	if err := f.store.SetOutboundAIRequestFeedback(ctx, f.account.ID, f.offer.ID, callID, "bad", "wrong_answer"); err != nil {
		t.Fatal(err)
	}
	if item := usageItem(); item.BadFeedback != 1 || item.GoodFeedback != 0 {
		t.Fatalf("after a person's rating = %+v", item)
	}

	// Online evaluation: claimed once, scored once.
	jobs, err := backend.ClaimAIOnlineEvaluations(ctx, 10)
	if err != nil || len(jobs) != 1 || jobs[0].LogID != callID || !jobs[0].Captured || jobs[0].JudgeIntegrationID != f.offer.ID ||
		jobs[0].JudgeModel != "gpt-4o-mini" || jobs[0].Rubric != "Name one city." || len(jobs[0].ResponseBody) == 0 {
		t.Fatalf("claimed = %+v, %v", jobs, err)
	}
	if again, err := backend.ClaimAIOnlineEvaluations(ctx, 10); err != nil || len(again) != 0 {
		t.Fatalf("claimed twice = %+v, %v", again, err)
	}
	for range 2 {
		if err := backend.FinishAIOnlineEvaluation(ctx, jobs[0], 4); err != nil {
			t.Fatal(err)
		}
	}
	if item := usageItem(); item.EvaluatedRequests != 1 || item.EvaluationScoreSum != 4 || item.EvaluationErrors != 0 {
		t.Fatalf("after scoring = %+v", item)
	}
	if logged, _ := f.store.ListOutboundAIRequestLog(ctx, f.account.ID, f.offer.ID, nil, 10); logged[0].EvaluationScore == nil ||
		*logged[0].EvaluationScore != 4 || logged[0].Feedback != "bad" || logged[0].FeedbackLabel != "wrong_answer" {
		t.Fatalf("scored entry = %+v", logged[0])
	}

	// Drift: a model whose truncations rose from 1% to 20% today.
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO outbound_ai_usage_daily (integration_id, usage_date, caller, model, requests, truncated_requests)
		VALUES ($1, (now() AT TIME ZONE 'UTC')::date - 1, $2, 'gpt-5', 1000, 10),
		       ($1, (now() AT TIME ZONE 'UTC')::date, $2, 'gpt-5', 200, 40),
		       ($1, (now() AT TIME ZONE 'UTC')::date, 'replay', 'gpt-5', 1000, 1000)`, f.offer.ID, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateAppWebhook(ctx, state.AppWebhook{AccountID: f.account.ID, AppID: f.app.ID,
		TargetURL: "https://hooks.example.test/ai", SecretSealed: []byte("sealed"), EventFilter: []string{"outbound.ai_quality.drift"},
		Enabled: true}); err != nil {
		t.Fatal(err)
	}
	worker := &AIQualityDriftWorker{Store: backend, Now: time.Now}
	alerts, err := worker.CheckOnce(ctx)
	if err != nil || len(alerts) != 1 || alerts[0].Model != "gpt-5" || alerts[0].Metric != AIQualityTruncated ||
		alerts[0].CurrentValue != 2_000 || alerts[0].BaselineValue != 100 {
		t.Fatalf("drift = %+v, %v", alerts, err)
	}
	if again, err := worker.CheckOnce(ctx); err != nil || len(again) != 0 {
		t.Fatalf("alerted twice = %+v, %v", again, err)
	}
	listed, err := f.store.ListOutboundAIQualityAlerts(ctx, f.account.ID, f.offer.ID, 10)
	if err != nil || len(listed) != 1 || listed[0].Metric != "truncated" || listed[0].CurrentCount != 200 {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	var queued int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM app_webhook_event_outbox WHERE event = 'outbound.ai_quality.drift' AND app_id = $1`,
		f.app.ID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("queued webhooks = %d, %v", queued, err)
	}

	// The database refuses unknown flags and scores out of range.
	for _, statement := range []string{
		`UPDATE outbound_ai_request_log SET quality_flags = ARRAY['slow'] WHERE id = $1`,
		`UPDATE outbound_ai_request_log SET evaluation_score = 6 WHERE id = $1`,
		`UPDATE outbound_ai_request_log SET feedback = 'meh', feedback_at = now() WHERE id = $1`,
	} {
		if _, err := f.pool.Exec(ctx, statement, callID); err == nil {
			t.Errorf("%s was accepted", statement)
		}
	}
}
