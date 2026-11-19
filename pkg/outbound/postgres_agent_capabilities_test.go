package outbound

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/agents"
)

// adr: 20261027090016 amendment 1 — on Postgres, the gateway reads a run's
// policy only while the run holds its lease, never admits more tool calls
// than the budget however many arrive at once, and records each refusal.
func TestPostgresAgentRunCapabilities(t *testing.T) {
	pool := agentRunsPool(t)
	ctx := context.Background()
	account := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, plan) VALUES ($1, 'scale')`, account); err != nil {
		t.Fatal(err)
	}
	policy := &api.AgentCapabilityPolicy{Tools: []api.AgentCapabilityTool{{Name: api.AgentCapabilityWebFetch, Mode: api.AgentCapabilityAllow}}}
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
	if _, ok, err := a.AgentRunCapabilities(ctx, identity); err != nil || ok {
		t.Fatalf("a starting run reached the gateway: %v %v", ok, err)
	}
	if err := store.MarkRunning(ctx, run.ID, lease, "agents-1", "agent-"+run.ID, 1000); err != nil {
		t.Fatal(err)
	}
	caps, ok, err := a.AgentRunCapabilities(ctx, identity)
	if err != nil || !ok || caps.Plan != api.PlanScale || !reflect.DeepEqual(caps.Policy, policy) {
		t.Fatalf("capabilities %+v %v %v", caps, ok, err)
	}
	other := identity
	other.LeaseToken = uuid.NewString()
	if _, ok, err := a.AgentRunCapabilities(ctx, other); err != nil || ok {
		t.Fatalf("another lease reached the gateway: %v %v", ok, err)
	}

	const budget = 5
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := a.ReserveToolCall(ctx, identity, budget)
			if err != nil {
				t.Error(err)
			}
			if ok {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != budget {
		t.Fatalf("20 concurrent calls admitted %d under a budget of %d", admitted.Load(), budget)
	}
	if ok, err := a.ReserveToolCall(ctx, identity, 0); err != nil || ok {
		t.Fatalf("a zero budget admitted a call: %v %v", ok, err)
	}
	for _, d := range []string{CapabilityBudget, CapabilityDenied} {
		if err := a.RecordDecision(ctx, identity, api.AgentCapabilityWebFetch, d); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.RecordDecision(ctx, identity, api.AgentCapabilityWebFetch, CapabilityAllowed); err == nil {
		t.Error("an allowed call was recorded as a decision")
	}
	var calls, refused, rows int
	if err := pool.QueryRow(ctx, `SELECT tool_calls, refused_calls, (SELECT count(*) FROM agent_run_capability_decisions WHERE run_id = $1)
		FROM agent_run_capability_summaries WHERE run_id = $1`, run.ID).Scan(&calls, &refused, &rows); err != nil {
		t.Fatal(err)
	}
	if calls != budget || refused != 2 || rows != 2 {
		t.Fatalf("summary calls=%d refused=%d decision rows=%d, want %d, 2, 2", calls, refused, rows, budget)
	}
}
