package agents_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/agents"
)

const (
	account   = "11111111-1111-1111-1111-111111111111"
	other     = "22222222-2222-2222-2222-222222222222"
	tenant    = "33333333-3333-3333-3333-333333333333"
	suspended = "44444444-4444-4444-4444-444444444444"
	foreign   = "55555555-5555-5555-5555-555555555555" // a tenant of the other account
)

// open applies the agent migration over minimal accounts and tenants tables,
// so the store runs against the real constraints without the full schema.
func open(t *testing.T) *pgxpool.Pool {
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
			account_id uuid NOT NULL, event text NOT NULL, payload jsonb NOT NULL, source_event_id uuid);
		CREATE TABLE object_buckets (id uuid PRIMARY KEY, account_id uuid NOT NULL, app_id uuid NOT NULL, state text NOT NULL DEFAULT 'ready');
		CREATE TABLE platform_mail_agent_run_observations (delivery_id uuid PRIMARY KEY, account_id uuid NOT NULL, disposition text NOT NULL);
		CREATE TABLE workflow_runs (id uuid PRIMARY KEY, status text NOT NULL);
		CREATE TABLE workflow_events (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), run_id uuid NOT NULL, event_name text NOT NULL);`); err != nil {
		t.Fatal(err)
	}
	for _, name := range agentMigrations {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO accounts VALUES ($1), ($2)`, account, other); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO platform_tenants VALUES ($1, $4, 'active'), ($2, $4, 'suspended'), ($3, $5, 'active')`,
		tenant, suspended, foreign, account, other); err != nil {
		t.Fatal(err)
	}
	return pool
}

// agentMigrations are the agent migrations, in order.
var agentMigrations = []string{"20261027141003390_agent_runs.sql", "20261027141010563_agent_run_execution.sql",
	"20261027141017736_agent_run_model_access.sql", "20261027141024909_agent_run_checkout.sql", "20261027141038256_agent_deliveries.sql", "20261027141045429_agent_delivery_destinations.sql", "20261027141106948_agent_run_workflow_steps.sql"}

func spec() agents.VersionSpec {
	return agents.VersionSpec{Harness: agents.Harness{Preset: "codex"}, PromptTemplate: "Triage {{event.title}}", TimeoutSec: 300}
}

var roomy = agents.AdmitLimits{RunsPerDay: 100, QueuedMax: 100, TimeoutMaxSec: 3600, RAMMBMax: 4096}

func admit(t *testing.T, s *agents.Store, a agents.Admission) agents.Run {
	t.Helper()
	if a.AccountID == "" {
		a.AccountID = account
	}
	if a.Agent == "" {
		a.Agent = "triage"
	}
	if a.Trigger == "" {
		a.Trigger = "api"
	}
	if a.Limits == (agents.AdmitLimits{}) {
		a.Limits = roomy
	}
	run, _, err := s.AdmitRun(context.Background(), a)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	return run
}

func TestAgentsAndVersions(t *testing.T) {
	s := agents.New(open(t))
	ctx := context.Background()
	agent, v1, err := s.CreateAgent(ctx, account, "triage", "PR triage", spec(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if agent.LatestVersion != 1 || v1.Version != 1 || v1.Harness.Preset != "codex" || v1.Harness.Models == nil {
		t.Fatalf("created %+v %+v", agent, v1)
	}
	if _, _, err := s.CreateAgent(ctx, account, "triage", "", spec(), 2); !errors.Is(err, agents.ErrNameTaken) {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, _, err := s.CreateAgent(ctx, other, "triage", "", spec(), 2); err != nil {
		t.Fatalf("names are per account: %v", err)
	}
	if _, _, err := s.CreateAgent(ctx, account, "review", "", spec(), 2); err != nil {
		t.Fatal(err)
	}
	var limit *agents.LimitError
	if _, _, err := s.CreateAgent(ctx, account, "third", "", spec(), 2); !errors.As(err, &limit) || limit.Limit != "AgentsPerAccount" || limit.Observed != 2 {
		t.Fatalf("agent limit: %v", err)
	}

	image := agents.VersionSpec{Harness: agents.Harness{Image: "ghcr.io/acme/agent:1.4", Run: []string{"agent", "{prompt}"},
		Models: map[string]string{"openai": "OPENAI_BASE_URL"}}, PromptTemplate: "Go", RAMMB: 2048, TimeoutSec: 600}
	agent, v2, err := s.AddVersion(ctx, account, "triage", image, 2)
	if err != nil {
		t.Fatal(err)
	}
	if agent.LatestVersion != 2 || v2.Version != 2 || v2.Harness.Image == "" || v2.Harness.Models["openai"] != "OPENAI_BASE_URL" || v2.RAMMB != 2048 {
		t.Fatalf("version 2: %+v %+v", agent, v2)
	}
	if _, _, err := s.AddVersion(ctx, account, "triage", spec(), 2); !errors.As(err, &limit) || limit.Limit != "AgentVersionsPerAgent" {
		t.Fatalf("version limit: %v", err)
	}
	got, err := s.Version(ctx, account, "triage", 1)
	if err != nil || got.Harness.Preset != "codex" || got.PromptTemplate != "Triage {{event.title}}" {
		t.Fatalf("version 1 is immutable: %+v %v", got, err)
	}
	if _, err := s.Version(ctx, other, "triage", 2); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("another account's version: %v", err)
	}
	list, err := s.Agents(ctx, account, "", 10)
	if err != nil || len(list) != 2 || list[0].Name != "review" || list[1].Name != "triage" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if page, _ := s.Agents(ctx, account, "review", 10); len(page) != 1 || page[0].Name != "triage" {
		t.Fatalf("page after review: %+v", page)
	}
}

func TestVersionConstraints(t *testing.T) {
	s := agents.New(open(t))
	ctx := context.Background()
	if _, _, err := s.CreateAgent(ctx, account, "triage", "", spec(), 10); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]agents.VersionSpec{
		"preset and image":  {Harness: agents.Harness{Preset: "codex", Image: "x:1"}, PromptTemplate: "p", TimeoutSec: 10},
		"neither":           {PromptTemplate: "p", TimeoutSec: 10},
		"unknown preset":    {Harness: agents.Harness{Preset: "aider"}, PromptTemplate: "p", TimeoutSec: 10},
		"image without run": {Harness: agents.Harness{Image: "x:1"}, PromptTemplate: "p", TimeoutSec: 10},
		"odd size":          {Harness: agents.Harness{Preset: "codex"}, PromptTemplate: "p", RAMMB: 700, TimeoutSec: 10},
		"empty prompt":      {Harness: agents.Harness{Preset: "codex"}, TimeoutSec: 10},
		"long timeout":      {Harness: agents.Harness{Preset: "codex"}, PromptTemplate: "p", TimeoutSec: 3601},
	} {
		if _, _, err := s.AddVersion(ctx, account, "triage", v, 10); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, _, err := s.CreateAgent(ctx, account, "Bad_Name", "", spec(), 10); err == nil {
		t.Error("invalid name accepted")
	}
}

func TestAdmitRun(t *testing.T) {
	s := agents.New(open(t))
	ctx := context.Background()
	if _, _, err := s.CreateAgent(ctx, account, "triage", "", spec(), 10); err != nil {
		t.Fatal(err)
	}
	prompt := "look at PR 7"
	run := admit(t, s, agents.Admission{TenantID: tenant, DedupeKey: "pr-7", Prompt: &prompt, Inputs: json.RawMessage(`{"pr":7}`)})
	if run.State != agents.StateQueued || run.AgentName != "triage" || run.AgentVersion != 1 || run.TenantID != tenant ||
		run.TimeoutSec != 300 || run.RAMMB != 0 || *run.Prompt != prompt || run.Attempt != 1 || run.Terminal() {
		t.Fatalf("admitted %+v", run)
	}
	again, existing, err := s.AdmitRun(ctx, agents.Admission{AccountID: account, Agent: "triage", Trigger: "api", TenantID: tenant,
		DedupeKey: "pr-7", Limits: roomy})
	if err != nil || !existing || again.ID != run.ID {
		t.Fatalf("dedupe: %+v %v %v", again, existing, err)
	}
	own := admit(t, s, agents.Admission{DedupeKey: "pr-7"})
	if own.ID == run.ID || own.TenantID != "" {
		t.Fatalf("the seller's own key is separate from the tenant's: %+v", own)
	}
	if _, existing, _ := s.AdmitRun(ctx, agents.Admission{AccountID: account, Agent: "triage", Trigger: "api", DedupeKey: "pr-7", Limits: roomy}); !existing {
		t.Fatal("a tenantless dedupe key matches itself")
	}

	var limit *agents.LimitError
	for name, tc := range map[string]struct {
		a    agents.Admission
		want error
		lim  string
	}{
		"unknown agent":     {a: agents.Admission{Agent: "nope"}, want: agents.ErrNotFound},
		"unknown version":   {a: agents.Admission{Version: 9}, want: agents.ErrNotFound},
		"unknown tenant":    {a: agents.Admission{TenantID: "66666666-6666-6666-6666-666666666666"}, want: agents.ErrTenantNotFound},
		"foreign tenant":    {a: agents.Admission{TenantID: foreign}, want: agents.ErrTenantNotFound},
		"suspended tenant":  {a: agents.Admission{TenantID: suspended}, want: agents.ErrTenantInactive},
		"timeout over plan": {a: agents.Admission{Limits: agents.AdmitLimits{RunsPerDay: 100, QueuedMax: 100, TimeoutMaxSec: 60, RAMMBMax: 4096}}, lim: "AgentRunTimeoutMaxSec"},
		"daily runs":        {a: agents.Admission{Limits: agents.AdmitLimits{RunsPerDay: 2, QueuedMax: 100, TimeoutMaxSec: 3600, RAMMBMax: 4096}}, lim: "AgentRunsPerDay"},
		"queued runs":       {a: agents.Admission{Limits: agents.AdmitLimits{RunsPerDay: 100, QueuedMax: 2, TimeoutMaxSec: 3600, RAMMBMax: 4096}}, lim: "AgentRunQueuedPerAccount"},
		"free plan":         {a: agents.Admission{Limits: agents.AdmitLimits{}}, lim: "AgentRunTimeoutMaxSec"},
	} {
		a := tc.a
		a.AccountID, a.Trigger = account, "api"
		if a.Agent == "" {
			a.Agent = "triage"
		}
		if a.Limits == (agents.AdmitLimits{}) && name != "free plan" {
			a.Limits = roomy
		}
		_, _, err := s.AdmitRun(ctx, a)
		switch {
		case tc.lim != "":
			if !errors.As(err, &limit) || limit.Limit != tc.lim {
				t.Errorf("%s: %v, want limit %s", name, err, tc.lim)
			}
		case !errors.Is(err, tc.want):
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	if _, _, err := s.AdmitRun(ctx, agents.Admission{AccountID: other, Agent: "triage", Trigger: "api", Limits: roomy}); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("another account's agent: %v", err)
	}
	if _, err := s.Run(ctx, other, run.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("another account's run: %v", err)
	}
	if _, err := s.Run(ctx, account, "not-a-uuid"); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("malformed run id: %v", err)
	}
}

func TestAdmitRunRAMOverPlan(t *testing.T) {
	s := agents.New(open(t))
	ctx := context.Background()
	big := spec()
	big.RAMMB = 4096
	if _, _, err := s.CreateAgent(ctx, account, "triage", "", big, 10); err != nil {
		t.Fatal(err)
	}
	var limit *agents.LimitError
	_, _, err := s.AdmitRun(ctx, agents.Admission{AccountID: account, Agent: "triage", Trigger: "api",
		Limits: agents.AdmitLimits{RunsPerDay: 10, QueuedMax: 10, TimeoutMaxSec: 3600, RAMMBMax: 2048}})
	if !errors.As(err, &limit) || limit.Limit != "AgentRunRAMMBMax" || limit.Observed != 4096 {
		t.Fatalf("ram over plan: %v", err)
	}
}

// TestAdmitRunConcurrentQueueBound is the ADR-20261027090010 invariant that admission
// never exceeds the account's queue bound, however many admissions race.
func TestAdmitRunConcurrentQueueBound(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	if _, _, err := s.CreateAgent(ctx, account, "triage", "", spec(), 10); err != nil {
		t.Fatal(err)
	}
	limits := agents.AdmitLimits{RunsPerDay: 100, QueuedMax: 5, TimeoutMaxSec: 3600, RAMMBMax: 4096}
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.AdmitRun(ctx, agents.Admission{AccountID: account, Agent: "triage", Trigger: "api", Limits: limits}); err == nil {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	var queued int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_runs WHERE state='queued'`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if admitted != 5 || queued != 5 {
		t.Fatalf("admitted %d, queued %d, want 5", admitted, queued)
	}
}

func TestCancelAndDelete(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	if _, _, err := s.CreateAgent(ctx, account, "triage", "", spec(), 10); err != nil {
		t.Fatal(err)
	}
	queued := admit(t, s, agents.Admission{})
	started := admit(t, s, agents.Admission{})
	done := admit(t, s, agents.Admission{})
	if _, err := pool.Exec(ctx, `UPDATE agent_runs SET state='running', started_at=now(), running_at=now() WHERE id=$1`, started.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_runs SET state='succeeded', finished_at=now() WHERE id=$1`, done.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAgent(ctx, account, "triage"); !errors.Is(err, agents.ErrBusy) {
		t.Fatalf("delete with live runs: %v", err)
	}

	run, err := s.CancelRun(ctx, account, queued.ID)
	if err != nil || run.State != agents.StateCancelled || run.FinishedAt == nil || run.OutcomeCode != "cancelled" {
		t.Fatalf("cancel queued: %+v %v", run, err)
	}
	if run, err = s.CancelRun(ctx, account, queued.ID); err != nil || run.State != agents.StateCancelled {
		t.Fatalf("cancel twice: %+v %v", run, err)
	}
	if run, err = s.CancelRun(ctx, account, started.ID); err != nil || run.State != agents.StateRunning || run.CancelRequestedAt == nil {
		t.Fatalf("cancel running: %+v %v", run, err)
	}
	if _, err = s.CancelRun(ctx, account, done.ID); !errors.Is(err, agents.ErrFinished) {
		t.Fatalf("cancel finished: %v", err)
	}
	if _, err = s.CancelRun(ctx, other, started.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("cancel another account's run: %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE agent_runs SET state='cancelled', finished_at=now() WHERE id=$1`, started.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAgent(ctx, account, "triage"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := s.Agent(ctx, account, "triage"); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("deleted agent still readable: %v", err)
	}
	if run, err := s.Run(ctx, account, done.ID); err != nil || run.AgentName != "triage" {
		t.Fatalf("a deleted agent's runs stay readable: %+v %v", run, err)
	}
	if _, _, err := s.CreateAgent(ctx, account, "triage", "", spec(), 10); err != nil {
		t.Fatalf("a deleted agent's name is free again: %v", err)
	}
}

func TestRunListing(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	for _, name := range []string{"triage", "review"} {
		if _, _, err := s.CreateAgent(ctx, account, name, "", spec(), 10); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	for i := range 5 {
		a := agents.Admission{}
		if i%2 == 1 {
			a.Agent, a.TenantID = "review", tenant
		}
		ids = append(ids, admit(t, s, a).ID)
	}
	all, err := s.Runs(ctx, account, agents.RunFilter{Limit: 10})
	if err != nil || len(all) != 5 || all[0].ID != ids[4] || all[4].ID != ids[0] {
		t.Fatalf("newest first: %v", err)
	}
	page, err := s.Runs(ctx, account, agents.RunFilter{Limit: 2, Before: &all[1]})
	if err != nil || len(page) != 2 || page[0].ID != ids[2] || page[1].ID != ids[1] {
		t.Fatalf("page: %+v %v", page, err)
	}
	for name, tc := range map[string]struct {
		f    agents.RunFilter
		want int
	}{
		"agent":         {agents.RunFilter{Agent: "review", Limit: 10}, 2},
		"tenant":        {agents.RunFilter{TenantID: tenant, Limit: 10}, 2},
		"state":         {agents.RunFilter{State: agents.StateQueued, Limit: 10}, 5},
		"no such state": {agents.RunFilter{State: agents.StateRunning, Limit: 10}, 0},
		"no such agent": {agents.RunFilter{Agent: "nope", Limit: 10}, 0},
	} {
		got, err := s.Runs(ctx, account, tc.f)
		if err != nil || len(got) != tc.want {
			t.Errorf("%s: %d runs (%v), want %d", name, len(got), err, tc.want)
		}
	}
	if got, _ := s.Runs(ctx, other, agents.RunFilter{Limit: 10}); len(got) != 0 {
		t.Fatalf("another account lists %d runs", len(got))
	}
}

func TestRunStateConstraints(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	if _, _, err := s.CreateAgent(ctx, account, "triage", "", spec(), 10); err != nil {
		t.Fatal(err)
	}
	run := admit(t, s, agents.Admission{})
	for name, sql := range map[string]string{
		"unknown state":             `UPDATE agent_runs SET state='paused' WHERE id=$1`,
		"terminal without finish":   `UPDATE agent_runs SET state='failed' WHERE id=$1`,
		"finish while not terminal": `UPDATE agent_runs SET finished_at=now() WHERE id=$1`,
		"lease without expiry":      `UPDATE agent_runs SET lease_token='t' WHERE id=$1`,
		"inputs not an object":      `UPDATE agent_runs SET inputs='[]' WHERE id=$1`,
	} {
		if _, err := pool.Exec(ctx, sql, run.ID); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
