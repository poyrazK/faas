// adr: 20261027090010, 20261027090014
package agents_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/agents"
)

const ttl = 30 * time.Second

func maxLive(limit int) func(string) int { return func(string) int { return limit } }

func agentdFixture(t *testing.T) (*pgxpool.Pool, *agents.Store) {
	t.Helper()
	pool := open(t)
	s := agents.New(pool)
	if _, _, err := s.CreateAgent(context.Background(), account, "triage", "", spec(), 10); err != nil {
		t.Fatal(err)
	}
	return pool, s
}

func TestClaimLifecycle(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	prompt := "look at PR 7"
	run := admit(t, s, agents.Admission{Prompt: &prompt, Inputs: json.RawMessage(`{"pr":7}`)})

	claim, found, err := s.ClaimRun(ctx, "agentd-1", "tok-1", ttl, maxLive(5))
	if err != nil || !found || claim.RunID != run.ID {
		t.Fatalf("claim %+v %v %v", claim, found, err)
	}
	var inputs map[string]int
	if claim.Plan != "pro" || claim.Harness.Preset != "codex" || claim.Template == "" || *claim.Prompt != prompt ||
		json.Unmarshal(claim.Inputs, &inputs) != nil || inputs["pr"] != 7 || claim.TimeoutSec != 300 {
		t.Fatalf("claim fields %+v", claim)
	}
	if _, found, _ := s.ClaimRun(ctx, "agentd-2", "tok-2", ttl, maxLive(5)); found {
		t.Fatal("a claimed run was claimed twice")
	}
	if got, _ := s.Run(ctx, account, run.ID); got.State != agents.StateStarting {
		t.Fatalf("state %s", got.State)
	}
	if err := s.MarkRunning(ctx, run.ID, "tok-wrong", "node", "inst", 1000); !errors.Is(err, agents.ErrLeaseLost) {
		t.Fatalf("a wrong token moved the run: %v", err)
	}
	if err := s.MarkRunning(ctx, run.ID, "tok-1", "agents-1", "agent-"+run.ID, 0); err == nil {
		t.Fatal("a run became running without a spend cap")
	}
	if err := s.MarkRunning(ctx, run.ID, "tok-1", "agents-1", "agent-"+run.ID, 200_000); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordBoot(ctx, run.ID, "tok-1", 900*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := s.Requeue(ctx, run.ID, "tok-1"); !errors.Is(err, agents.ErrLeaseLost) {
		t.Fatalf("a running run went back to the queue: %v", err)
	}
	if cancel, err := s.Renew(ctx, run.ID, "tok-1", ttl); err != nil || cancel {
		t.Fatalf("renew %v %v", cancel, err)
	}
	if _, err := s.CancelRun(ctx, account, run.ID); err != nil {
		t.Fatal(err)
	}
	if cancel, err := s.Renew(ctx, run.ID, "tok-1", ttl); err != nil || !cancel {
		t.Fatalf("renew after cancel %v %v", cancel, err)
	}
	exit := 0
	if err := s.FinishRun(ctx, account, run.ID, "tok-1", agents.Finish{State: agents.StateSucceeded, ExitCode: &exit,
		Result: json.RawMessage(`{"verdict":"ok"}`), Files: []agents.OutputFile{{Name: "a.md", Size: 1, SHA256: "sha256:x"}},
		LogTail: "done", LogTruncated: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, account, run.ID, "tok-1", agents.Finish{State: agents.StateFailed}); !errors.Is(err, agents.ErrLeaseLost) {
		t.Fatalf("a finished run finished twice: %v", err)
	}
	got, _ := s.Run(ctx, account, run.ID)
	if got.State != agents.StateSucceeded || got.FinishedAt == nil {
		t.Fatalf("finished run %+v", got)
	}
	out, err := s.RunOutputs(ctx, account, run.ID)
	if err != nil || len(out.Files) != 1 || out.LogTail != "done" || !out.LogTruncated {
		t.Fatalf("outputs %+v %v", out, err)
	}
	var verdict map[string]string
	if json.Unmarshal(out.Result, &verdict) != nil || verdict["verdict"] != "ok" {
		t.Fatalf("result %s", out.Result)
	}
	var token *string
	if err := pool.QueryRow(ctx, `SELECT lease_token FROM agent_runs WHERE id=$1`, run.ID).Scan(&token); err != nil || token != nil {
		t.Fatalf("lease kept after finish: %v %v", token, err)
	}
	if _, err := s.RunOutputs(ctx, other, run.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("another account read the outputs: %v", err)
	}
}

func TestClaimRespectsAccountConcurrency(t *testing.T) {
	_, s := agentdFixture(t)
	ctx := context.Background()
	for range 3 {
		admit(t, s, agents.Admission{})
	}
	claimed := 0
	for i := range 3 {
		if _, found, err := s.ClaimRun(ctx, "agentd", "tok-"+string(rune('a'+i)), ttl, maxLive(2)); err != nil {
			t.Fatal(err)
		} else if found {
			claimed++
		}
	}
	if claimed != 2 {
		t.Fatalf("claimed %d runs with a limit of 2", claimed)
	}
	if _, found, _ := s.ClaimRun(ctx, "agentd", "tok-z", ttl, maxLive(0)); found {
		t.Fatal("a plan with no agent runs was claimed")
	}
}

func TestRequeueAndExpiry(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	starting := admit(t, s, agents.Admission{})
	running := admit(t, s, agents.Admission{})
	requeued := admit(t, s, agents.Admission{})
	for i, want := range []string{starting.ID, running.ID, requeued.ID} {
		claim, found, err := s.ClaimRun(ctx, "agentd", "tok-"+want, ttl, maxLive(5))
		if err != nil || !found || claim.RunID != want {
			t.Fatalf("claim %d: %+v %v %v", i, claim, found, err)
		}
	}
	if err := s.MarkRunning(ctx, running.ID, "tok-"+running.ID, "agents-1", "agent-x", 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.Requeue(ctx, requeued.ID, "tok-"+requeued.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Run(ctx, account, requeued.ID); got.State != agents.StateQueued || got.StartedAt != nil {
		t.Fatalf("requeued run %+v", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_runs SET lease_expires_at = now() - interval '1 second' WHERE lease_token IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	queued, failed, err := s.ExpireLeases(ctx)
	if err != nil || queued != 1 || failed != 1 {
		t.Fatalf("expire %d %d %v", queued, failed, err)
	}
	if got, _ := s.Run(ctx, account, starting.ID); got.State != agents.StateQueued {
		t.Fatalf("expired starting run %s", got.State)
	}
	if got, _ := s.Run(ctx, account, running.ID); got.State != agents.StateFailed || got.OutcomeCode != "lease_lost" {
		t.Fatalf("expired running run %+v", got)
	}
	if _, err := s.Renew(ctx, running.ID, "tok-"+running.ID, ttl); !errors.Is(err, agents.ErrLeaseLost) {
		t.Fatalf("renew after expiry: %v", err)
	}
}

// A version names only the account's enabled customer AI integrations, one
// per provider, and a claim carries them with the version's spend cap.
func TestVersionModelIntegrations(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	openai, anthropic, disabled, foreignID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO outbound_integrations (id, account_id, enabled, owner_kind, ai_provider) VALUES
		($1, $5, true, 'customer', 'openai'), ($2, $5, true, 'customer', 'anthropic'),
		($3, $5, false, 'customer', 'openai'), ($4, $6, true, 'customer', 'openai')`,
		openai, anthropic, disabled, foreignID, account, other); err != nil {
		t.Fatal(err)
	}
	withIntegration := func(id string) agents.VersionSpec {
		v := spec()
		v.Integrations, v.AIBudgetMillicents = map[string]string{"openai": id}, 75_000
		return v
	}
	for name, id := range map[string]string{"wrong provider": anthropic, "disabled": disabled, "another account's": foreignID, "unknown": uuid.NewString()} {
		var invalid *agents.IntegrationError
		if _, _, err := s.CreateAgent(ctx, account, "bad", "", withIntegration(id), 10); !errors.As(err, &invalid) || invalid.Provider != "openai" {
			t.Errorf("%s integration: %v", name, err)
		}
	}
	_, version, err := s.CreateAgent(ctx, account, "triage", "", withIntegration(openai), 10)
	if err != nil || version.Integrations["openai"] != openai || version.AIBudgetMillicents != 75_000 {
		t.Fatalf("version %+v %v", version, err)
	}
	run := admit(t, s, agents.Admission{})
	claim, found, err := s.ClaimRun(ctx, "agentd-1", "tok-1", ttl, maxLive(5))
	if err != nil || !found || claim.RunID != run.ID || claim.Integrations["openai"] != openai || claim.AIBudgetMillicents != 75_000 {
		t.Fatalf("claim %+v %v %v", claim, found, err)
	}
}

// A checkout is fixed to the account's named or most recent installation at
// admission, and refused when there is none.
func TestAdmitCheckout(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	const commit = "0123456789abcdef0123456789abcdef01234567"
	if _, err := admitErr(s, agents.Admission{Checkout: &agents.Checkout{Repository: "acme/app", Commit: commit}}); !errors.Is(err, agents.ErrCheckoutUnavailable) {
		t.Fatalf("no installation: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO github_installations (account_id, installation_id, sealed_at) VALUES
		($1, 7, now() - interval '1 day'), ($1, 8, now()), ($2, 9, now())`, account, other); err != nil {
		t.Fatal(err)
	}
	latest := admit(t, s, agents.Admission{Checkout: &agents.Checkout{Repository: "acme/app", Commit: commit}})
	named := admit(t, s, agents.Admission{Checkout: &agents.Checkout{Repository: "acme/app", Commit: commit, InstallationID: 7}})
	if latest.Checkout == nil || latest.Checkout.InstallationID != 8 || named.Checkout.InstallationID != 7 || named.Checkout.Commit != commit {
		t.Fatalf("checkouts %+v %+v", latest.Checkout, named.Checkout)
	}
	if _, err := admitErr(s, agents.Admission{Checkout: &agents.Checkout{Repository: "acme/app", Commit: commit, InstallationID: 9}}); !errors.Is(err, agents.ErrCheckoutUnavailable) {
		t.Fatalf("another account's installation: %v", err)
	}
	claim, found, err := s.ClaimRun(ctx, "agentd-1", "tok-1", ttl, maxLive(5))
	if err != nil || !found || claim.Checkout == nil || claim.Checkout.Repository != "acme/app" {
		t.Fatalf("claim %+v %v %v", claim, found, err)
	}
}

func admitErr(s *agents.Store, a agents.Admission) (agents.Run, error) {
	a.AccountID, a.Agent, a.Trigger, a.Limits = account, "triage", "api", roomy
	run, _, err := s.AdmitRun(context.Background(), a)
	return run, err
}

// Every terminal run is announced once to the account's subscribed account
// receivers, whichever path ended it (ADR-20261027090010 §8).
func TestFinishedRunsAreAnnounced(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	subscribed, other, release := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO app_webhooks (id, account_id, scope, enabled, event_filter) VALUES
		($1, $4, 'account', true, ARRAY['agent_run.finished']), ($2, $5, 'account', true, ARRAY['agent_run.finished']),
		($3, $4, 'account', true, ARRAY['deployment.live'])`, subscribed, other, release, account, other); err != nil {
		t.Fatal(err)
	}
	deliveries := func(runID string) []api.AgentRunFinishedWebhookPayload {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT webhook_id::text, payload FROM app_webhook_deliveries WHERE source_event_id = $1`, runID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []api.AgentRunFinishedWebhookPayload
		for rows.Next() {
			var webhook string
			var body []byte
			if err := rows.Scan(&webhook, &body); err != nil {
				t.Fatal(err)
			}
			if webhook != subscribed {
				t.Fatalf("delivered to %s", webhook)
			}
			var p api.AgentRunFinishedWebhookPayload
			if err := json.Unmarshal(body, &p); err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		}
		return out
	}
	done := admit(t, s, agents.Admission{})
	if _, _, err := s.ClaimRun(ctx, "agentd-1", "tok-1", ttl, maxLive(5)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRunning(ctx, done.ID, "tok-1", "agents-1", "agent-x", 1000); err != nil {
		t.Fatal(err)
	}
	exit := 0
	if err := s.FinishRun(ctx, account, done.ID, "tok-1", agents.Finish{State: agents.StateSucceeded, ExitCode: &exit,
		Result: json.RawMessage(`{"verdict":"ok"}`), Files: []agents.OutputFile{{Name: "a.md", Size: 1, SHA256: "sha256:x"}}}); err != nil {
		t.Fatal(err)
	}
	got := deliveries(done.ID)
	var verdict map[string]string
	if len(got) != 1 || got[0].State != agents.StateSucceeded || json.Unmarshal(got[0].Result, &verdict) != nil || verdict["verdict"] != "ok" ||
		len(got[0].Files) != 1 || got[0].Agent != "triage" || got[0].FinishedAt.IsZero() {
		t.Fatalf("succeeded run deliveries %+v", got)
	}

	// A failed run announces its end but delivers nothing it wrote.
	broken := admit(t, s, agents.Admission{})
	if _, _, err := s.ClaimRun(ctx, "agentd-1", "tok-3", ttl, maxLive(5)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRunning(ctx, broken.ID, "tok-3", "agents-1", "agent-z", 1000); err != nil {
		t.Fatal(err)
	}
	exit = 1
	if err := s.FinishRun(ctx, account, broken.ID, "tok-3", agents.Finish{State: agents.StateFailed, ExitCode: &exit,
		Result: json.RawMessage(`{"verdict":"partial"}`), Files: []agents.OutputFile{{Name: "b.md", Size: 1, SHA256: "sha256:y"}}}); err != nil {
		t.Fatal(err)
	}
	if got := deliveries(broken.ID); len(got) != 1 || got[0].State != agents.StateFailed || got[0].Result != nil ||
		got[0].ResultOmitted || got[0].Files != nil || got[0].ExitCode == nil || *got[0].ExitCode != 1 {
		t.Fatalf("failed run deliveries %+v", got)
	}

	queued := admit(t, s, agents.Admission{})
	if _, err := s.CancelRun(ctx, account, queued.ID); err != nil {
		t.Fatal(err)
	}
	if got := deliveries(queued.ID); len(got) != 1 || got[0].State != agents.StateCancelled {
		t.Fatalf("cancelled-while-queued deliveries %+v", got)
	}

	lost := admit(t, s, agents.Admission{})
	if _, _, err := s.ClaimRun(ctx, "agentd-1", "tok-2", ttl, maxLive(5)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRunning(ctx, lost.ID, "tok-2", "agents-1", "agent-y", 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_runs SET lease_expires_at = now() - interval '1 minute' WHERE id = $1`, lost.ID); err != nil {
		t.Fatal(err)
	}
	if _, failed, err := s.ExpireLeases(ctx); err != nil || failed != 1 {
		t.Fatalf("expire %d %v", failed, err)
	}
	if got := deliveries(lost.ID); len(got) != 1 || got[0].Outcome == nil || got[0].Outcome.Code != "lease_lost" {
		t.Fatalf("lease-lost deliveries %+v", got)
	}
}
