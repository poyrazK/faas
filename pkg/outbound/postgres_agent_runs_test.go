package outbound

// adr: 20261027090010 — the agent run authorizer follows agentd's live lease, and the
// spend ledger is a hard cap over settled spend and live reservations.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/agents"
)

// agentRunsPool applies the agent migrations over minimal accounts, tenants
// and integrations tables, as pkg/state/agents' tests do.
func agentRunsPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE accounts (id uuid PRIMARY KEY, plan text NOT NULL DEFAULT 'pro');
		CREATE TABLE platform_tenants (id uuid PRIMARY KEY, account_id uuid NOT NULL REFERENCES accounts(id), status text NOT NULL,
			UNIQUE (account_id, id));
		CREATE TABLE outbound_integrations (id uuid PRIMARY KEY, account_id uuid NOT NULL REFERENCES accounts(id),
			enabled boolean NOT NULL DEFAULT true, owner_kind text NOT NULL DEFAULT 'operator', ai_provider text);
		CREATE TABLE github_installations (account_id uuid NOT NULL, installation_id bigint NOT NULL,
			sealed_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (account_id, installation_id));
		CREATE TABLE app_webhooks (id uuid PRIMARY KEY, account_id uuid NOT NULL, scope text NOT NULL DEFAULT 'app',
			enabled boolean NOT NULL DEFAULT true, event_filter text[] NOT NULL DEFAULT '{}');
		CREATE TABLE app_webhook_deliveries (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), webhook_id uuid NOT NULL, app_id uuid,
			account_id uuid NOT NULL, event text NOT NULL, payload jsonb NOT NULL, source_event_id uuid);`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20261027220903390_agent_runs.sql", "20261027220910563_agent_run_execution.sql",
		"20261027220917736_agent_run_model_access.sql", "20261027220924909_agent_run_checkout.sql", "20261027220938256_agent_deliveries.sql", "20261027220945429_agent_delivery_destinations.sql", "20261027221006948_agent_run_workflow_steps.sql"} {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	return pool
}

func TestPostgresAgentRunsAuthorizeAndCap(t *testing.T) {
	pool := agentRunsPool(t)
	ctx := context.Background()
	account, tenant, integration, unnamed := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id) VALUES ($1)`, account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO platform_tenants VALUES ($1, $2, 'active')`, tenant, account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbound_integrations (id, account_id, enabled, owner_kind, ai_provider)
		VALUES ($1, $3, true, 'customer', 'openai'), ($2, $3, true, 'customer', 'anthropic')`, integration, unnamed, account); err != nil {
		t.Fatal(err)
	}
	store := agents.New(pool)
	agent, _, err := store.CreateAgent(ctx, account, "triage", "", agents.VersionSpec{Harness: agents.Harness{Preset: "codex"},
		PromptTemplate: "go", TimeoutSec: 300, Integrations: map[string]string{"openai": integration}}, 10)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.AdmitRun(ctx, agents.Admission{AccountID: account, Agent: "triage", TenantID: tenant, Trigger: "api",
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
	identity := AgentRunIdentity{AccountID: account, RunID: run.ID, LeaseToken: lease, TenantID: tenant, AgentID: agent.ID, AgentVersion: 1}
	if _, ok, err := a.AuthorizeAgentRun(ctx, identity, integration); err != nil || ok {
		t.Fatalf("a starting run was authorized: %v %v", ok, err)
	}
	if err := store.MarkRunning(ctx, run.ID, lease, "agents-1", "agent-"+run.ID, 1000); err != nil {
		t.Fatal(err)
	}
	grant, ok, err := a.AuthorizeAgentRun(ctx, identity, integration)
	if err != nil || !ok || grant.TenantID != tenant || grant.AIBudgetMillicents != 1000 {
		t.Fatalf("grant %+v %v %v", grant, ok, err)
	}
	refused := map[string]struct {
		mutate      func(*AgentRunIdentity)
		integration string
	}{
		"another lease":       {func(i *AgentRunIdentity) { i.LeaseToken = uuid.NewString() }, integration},
		"another tenant":      {func(i *AgentRunIdentity) { i.TenantID = "" }, integration},
		"another version":     {func(i *AgentRunIdentity) { i.AgentVersion = 2 }, integration},
		"an unnamed provider": {func(*AgentRunIdentity) {}, unnamed},
	}
	for name, tc := range refused {
		i := identity
		tc.mutate(&i)
		if _, ok, err := a.AuthorizeAgentRun(ctx, i, tc.integration); err != nil || ok {
			t.Errorf("%s was authorized: %v %v", name, ok, err)
		}
	}

	expires := time.Now().Add(time.Minute)
	reserve := func(lease string, cost int64, until time.Time) bool {
		t.Helper()
		fits, err := a.ReserveAgentRunSpend(ctx, run.ID, account, lease, 1000, cost, until)
		if err != nil {
			t.Fatal(err)
		}
		return fits
	}
	if !reserve("lease-a", 600, expires) || reserve("lease-b", 600, expires) {
		t.Fatal("two reservations passed the cap together")
	}
	if err := a.SettleAgentRunSpend(ctx, run.ID, "lease-a", 300); err != nil {
		t.Fatal(err)
	}
	if !reserve("lease-c", 600, expires) || reserve("lease-d", 200, expires) {
		t.Fatal("settled spend plus reservations was not the cap")
	}
	if err := a.ReleaseAgentRunSpend(ctx, "lease-c"); err != nil {
		t.Fatal(err)
	}
	if !reserve("lease-e", 600, time.Now().Add(-time.Second)) || !reserve("lease-f", 700, expires) {
		t.Fatal("a lapsed reservation still held the cap")
	}
	if err := a.SettleAgentRunSpend(ctx, run.ID, "lease-f", 700); err != nil {
		t.Fatal(err)
	}
	if reserve("lease-g", 0, expires) {
		t.Fatal("a run at its cap reserved another call")
	}
	var spent, calls int64
	if err := pool.QueryRow(ctx, `SELECT spent_millicents, calls FROM agent_run_ai_spend WHERE run_id = $1`, run.ID).Scan(&spent, &calls); err != nil ||
		spent != 1000 || calls != 2 {
		t.Fatalf("spend %d calls %d %v", spent, calls, err)
	}

	if _, err := store.CancelRun(ctx, account, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := a.AuthorizeAgentRun(ctx, identity, integration); err != nil || ok {
		t.Fatalf("a run with a cancel request was authorized: %v %v", ok, err)
	}
}
