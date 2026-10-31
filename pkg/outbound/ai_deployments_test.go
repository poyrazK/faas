package outbound

// adr: 20261022153155 — a call from an app instance settles with that
// instance, so its deployment is counted; Postgres adds it to the
// deployment's minute row through the instance row, and counts nothing for
// an instance of another app, a missing instance, a cache hit or a caller
// that is not an app.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// recordingAIBackend keeps each settled AI call.
type recordingAIBackend struct {
	*MemoryBackend
	mu      sync.Mutex
	records []AIUsageRecord
}

func (b *recordingAIBackend) RecordAIUsageAlerts(ctx context.Context, record AIUsageRecord) ([]AIBudgetAlert, error) {
	b.mu.Lock()
	b.records = append(b.records, record)
	b.mu.Unlock()
	return b.MemoryBackend.RecordAIUsageAlerts(ctx, record)
}

func TestAICallSettlesWithTheCallingInstance(t *testing.T) {
	f := newPhase8Fixture(t, nil)
	backend := &recordingAIBackend{MemoryBackend: f.backend}
	f.handler.Backend = backend
	rec := f.post(Prefix+"primary/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("call = %d %s", rec.Code, rec.Body)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.records) != 1 {
		t.Fatalf("records = %+v", backend.records)
	}
	record := backend.records[0]
	if record.Caller != f.app || record.InstanceID != "i-1" || record.TTFBMS == nil {
		t.Fatalf("record = caller %q instance %q ttfb %v", record.Caller, record.InstanceID, record.TTFBMS)
	}
}

func TestPostgresAIDeploymentMinutes(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	node, err := f.store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, ImageDigest: "sha256:" + strings.Repeat("d", 64),
		Kind: state.DeploymentKindImage, Status: state.DeployLive, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := f.store.CreateInstance(ctx, f.app.ID, deployment.ID, string(state.StateRunning), 128, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: "ai-other-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	ttfb := int64(300)
	settle := func(caller, instanceID string, outcome AIUsageOutcome, flags []string, cached bool, cost int64) {
		t.Helper()
		record := AIUsageRecord{IntegrationID: f.offer.ID, Caller: caller, Outcome: outcome, InstanceID: instanceID, TTFBMS: &ttfb,
			Usage:          AIUsage{Model: "gpt-4o", Known: outcome == AIUsageOutcomeKnown, InputTokens: 20, OutputTokens: 10},
			CostMillicents: cost, QualityFlags: flags, Cached: cached}
		if outcome == AIUsageOutcomeError {
			record.Usage, record.TTFBMS = AIUsage{Model: "gpt-4o"}, nil
		}
		if cached {
			record.Usage = AIUsage{Model: "gpt-4o", Known: true}
		}
		if _, err := backend.RecordAIUsageAlerts(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	settle(f.app.ID, instance.ID, AIUsageOutcomeKnown, nil, false, 100)
	settle(f.app.ID, instance.ID, AIUsageOutcomeKnown, []string{"truncated"}, false, 300)
	settle(f.app.ID, instance.ID, AIUsageOutcomeError, nil, false, 0)
	// None of these count: another app's caller, a missing instance, no
	// instance, a cache hit and a Run.
	settle(other.ID, instance.ID, AIUsageOutcomeKnown, nil, false, 100)
	settle(f.app.ID, uuid.NewString(), AIUsageOutcomeKnown, nil, false, 100)
	settle(f.app.ID, "", AIUsageOutcomeKnown, nil, false, 100)
	settle(f.app.ID, instance.ID, AIUsageOutcomeKnown, nil, true, 0)
	settle(AIUsageCallerRuns, instance.ID, AIUsageOutcomeKnown, nil, false, 100)

	window, err := f.store.ReleaseGateDeploymentAI(ctx, f.app.ID, deployment.ID, time.Now().Add(-time.Hour), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if window != (state.ReleaseGateAIWindow{Calls: 3, ErrorCalls: 1, FlaggedCalls: 1, CostMillicents: 400, TTFBCalls: 2, TTFBMSTotal: 600}) {
		t.Fatalf("release gate window = %+v", window)
	}
	if other, err := f.store.ReleaseGateDeploymentAI(ctx, other.ID, deployment.ID, time.Now().Add(-time.Hour), time.Now().Add(time.Minute)); err != nil || other.Calls != 0 {
		t.Fatalf("another app's window = %+v, %v", other, err)
	}
	stats, err := f.store.ListOutboundAIDeploymentStats(ctx, f.app.ID, time.Now().Add(-24*time.Hour), api.OutboundAIDeploymentsListMax)
	if err != nil || len(stats) != 1 || stats[0].DeploymentID != deployment.ID || stats[0].Calls != 3 || stats[0].InputTokens != 40 ||
		stats[0].OutputTokens != 20 || stats[0].LastMinute.IsZero() {
		t.Fatalf("stats = %+v, %v", stats, err)
	}

	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_deployment_minutes SET minute = minute - make_interval(days => $1::int)`,
		api.OutboundAIDeploymentStatsRetentionDays+1); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.PruneAIUsage(ctx); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM outbound_ai_deployment_minutes`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rows after prune = %d, %v", rows, err)
	}
}
