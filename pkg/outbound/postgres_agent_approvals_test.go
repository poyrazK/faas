package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/agents"
)

// adr: 20261027090016 amendment 2 — on Postgres, a running run's request
// expires before its deadline, queues the approval webhook, stops at the
// pending cap, and reads the decision apid writes.
func TestPostgresAgentRunApprovals(t *testing.T) {
	pool := agentRunsPool(t)
	ctx := context.Background()
	account, hook := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, plan) VALUES ($1, 'pro')`, account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO app_webhooks (id, account_id, scope, event_filter) VALUES ($1, $2, 'account', $3)`,
		hook, account, []string{api.AgentRunApprovalRequestedWebhookEvent}); err != nil {
		t.Fatal(err)
	}
	policy := &api.AgentCapabilityPolicy{Tools: []api.AgentCapabilityTool{{Name: api.AgentCapabilityWebFetch, Mode: api.AgentCapabilityApprove}}}
	store := agents.New(pool)
	agent, _, err := store.CreateAgent(ctx, account, "reader", "", agents.VersionSpec{Harness: agents.Harness{Preset: "codex"},
		PromptTemplate: "go", TimeoutSec: 300, CapabilityPolicy: policy}, 10)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.AdmitRun(ctx, agents.Admission{AccountID: account, Agent: "reader", Trigger: "api",
		Limits: agents.AdmitLimits{RunsPerDay: 10, QueuedMax: 10, TimeoutMaxSec: 3600, RAMMBMax: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	lease := uuid.NewString()
	if _, found, err := store.ClaimRun(ctx, "agentd-1", lease, time.Minute, func(string) int { return 5 }); err != nil || !found {
		t.Fatalf("claim %v %v", found, err)
	}
	a, err := NewPostgresAgentRuns(pool)
	if err != nil {
		t.Fatal(err)
	}
	identity := AgentRunIdentity{AccountID: account, RunID: run.ID, LeaseToken: lease, AgentID: agent.ID, AgentVersion: 1}
	req := ToolApprovalRequest{Tool: api.AgentCapabilityWebFetch, ArgumentsSHA256: strings.Repeat("b", 64), ArgumentsSummary: `{"url":"x"}`}
	if _, err := a.RequestApproval(ctx, identity, req); !errors.Is(err, ErrApprovalRefused) {
		t.Fatalf("a starting run asked: %v", err)
	}
	if err := store.MarkRunning(ctx, run.ID, lease, "agents-1", "agent-"+run.ID, 1000); err != nil {
		t.Fatal(err)
	}

	first, err := a.RequestApproval(ctx, identity, req)
	if err != nil {
		t.Fatal(err)
	}
	if left := time.Until(first.ExpiresAt); left > 300*time.Second || left < 200*time.Second {
		t.Errorf("expires in %v, want the run's 300 s deadline less the margin", left)
	}
	var payload api.AgentRunApprovalRequestedWebhookPayload
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM app_webhook_deliveries WHERE webhook_id = $1 AND event = $2 AND source_event_id = $3`,
		hook, api.AgentRunApprovalRequestedWebhookEvent, first.ID).Scan(&raw); err != nil {
		t.Fatalf("webhook delivery: %v", err)
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.ApprovalID != first.ID || payload.RunID != run.ID ||
		payload.AgentID != agent.ID || payload.Tool != req.Tool || payload.ArgumentsSummary != req.ArgumentsSummary {
		t.Errorf("payload %s: %v", raw, err)
	}

	for range api.AgentRunApprovalsPendingMax - 1 {
		if _, err := a.RequestApproval(ctx, identity, req); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.RequestApproval(ctx, identity, req); !errors.Is(err, ErrApprovalRefused) {
		t.Fatalf("request past the pending cap: %v", err)
	}
	if d, err := a.ApprovalDecision(ctx, identity, first.ID); err != nil || d != "" {
		t.Fatalf("undecided: %q %v", d, err)
	}
	if _, err := store.DecideRunApproval(ctx, account, run.ID, first.ID, api.AgentRunApprovalDecisionApprove, "", "account"); err != nil {
		t.Fatal(err)
	}
	if d, err := a.ApprovalDecision(ctx, identity, first.ID); err != nil || d != api.AgentRunApprovalDecisionApprove {
		t.Fatalf("decided: %q %v", d, err)
	}
	if _, err := a.RequestApproval(ctx, identity, req); err != nil {
		t.Fatalf("a decision frees a pending slot: %v", err)
	}
	other := identity
	other.RunID = uuid.NewString()
	if _, err := a.ApprovalDecision(ctx, other, first.ID); err == nil {
		t.Error("another run read the decision")
	}

	if _, err := pool.Exec(ctx, `UPDATE agent_runs SET running_at = now() - interval '280 seconds' WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM agent_run_approvals WHERE run_id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RequestApproval(ctx, identity, req); !errors.Is(err, ErrApprovalRefused) {
		t.Fatalf("a run 20 s from its deadline asked: %v", err)
	}

	// ADR-20261027090016 amendment 3: a run that may earn credit can wait
	// until its cap's deadline.
	if _, err := pool.Exec(ctx, `UPDATE agent_runs SET deadline_credit_max_sec = 600 WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	credited, err := a.RequestApproval(ctx, identity, req)
	if err != nil {
		t.Fatalf("a run with credit left was refused: %v", err)
	}
	if left := time.Until(credited.ExpiresAt); left > 600*time.Second || left < 500*time.Second {
		t.Errorf("expires in %v, want 300 s + 600 s credit − 280 s run − 30 s margin", left)
	}
	var released *time.Time
	for range 2 {
		if err := a.ReleaseApproval(ctx, identity, credited.ID); err != nil {
			t.Fatal(err)
		}
		var at time.Time
		if err := pool.QueryRow(ctx, `SELECT released_at FROM agent_run_approvals WHERE id = $1`, credited.ID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		if released != nil && !at.Equal(*released) {
			t.Errorf("a second release moved released_at from %v to %v", *released, at)
		}
		released = &at
	}
	if err := a.ReleaseApproval(ctx, other, credited.ID); err != nil {
		t.Fatal(err)
	}
}
