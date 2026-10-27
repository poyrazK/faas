// adr: 20261027090010, 20261027090014
package agentd_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/agentd"
	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// account differs from pkg/state/agents' fixtures: the per-account advisory
// lock is cluster-wide, so packages sharing a test cluster must not share it.
const account = "a9e17d00-0000-4000-8000-000000000001"

func openStore(t *testing.T) (*pgxpool.Pool, *agentstore.Store) {
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
	for _, name := range []string{"20261027141003390_agent_runs.sql", "20261027141010563_agent_run_execution.sql",
		"20261027141017736_agent_run_model_access.sql", "20261027141024909_agent_run_checkout.sql", "20261027141038256_agent_deliveries.sql", "20261027141045429_agent_delivery_destinations.sql", "20261027141106948_agent_run_workflow_steps.sql"} {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id) VALUES ($1)`, account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO github_installations (account_id, installation_id) VALUES ($1, 42)`, account); err != nil {
		t.Fatal(err)
	}
	return pool, agentstore.New(pool)
}

// queueRun defines an agent whose command names the fake schedd's script,
// and admits one run of it.
func queueRun(t *testing.T, s *agentstore.Store, script string) string {
	t.Helper()
	return queueRunWith(t, s, script, nil)
}

func queueRunWith(t *testing.T, s *agentstore.Store, script string, checkout *agentstore.Checkout) string {
	t.Helper()
	ctx := context.Background()
	spec := agentstore.VersionSpec{Harness: agentstore.Harness{Image: "ghcr.io/acme/agent:1", Run: []string{script, "{prompt}"}},
		PromptTemplate: "Triage {{inputs.title}}: {{prompt}}", RAMMB: 1024, TimeoutSec: 60}
	if _, _, err := s.CreateAgent(ctx, account, script, "", spec, 10); err != nil {
		t.Fatal(err)
	}
	prompt := "be brief"
	run, _, err := s.AdmitRun(ctx, agentstore.Admission{AccountID: account, Agent: script, Trigger: "api", Prompt: &prompt,
		Inputs: json.RawMessage(`{"title":"PR 7"}`), Checkout: checkout,
		Limits: agentstore.AdmitLimits{RunsPerDay: 100, QueuedMax: 100, TimeoutMaxSec: 3600, RAMMBMax: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	return run.ID
}

// fakeSchedd scripts RunAgent by the run's first argument.
type fakeSchedd struct {
	scheddpb.UnimplementedScheddServer
	mu             sync.Mutex
	start          *scheddpb.AgentRunStart
	run            agentrunproto.Request
	proceededFirst bool
}

func (f *fakeSchedd) RunAgent(stream scheddpb.Schedd_RunAgentServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	var run agentrunproto.Request
	if err := json.Unmarshal(first.GetStart().GetRun(), &run); err != nil {
		return err
	}
	f.mu.Lock()
	f.start, f.run = first.GetStart(), run
	f.mu.Unlock()
	switch run.Command[0] {
	case "full":
		return status.Error(codes.ResourceExhausted, "no capacity")
	case "reject":
		return status.Error(codes.FailedPrecondition, "outside the plan")
	}
	if err := stream.Send(&scheddpb.AgentRunEvent{Frame: &scheddpb.AgentRunEvent_Placed{Placed: &scheddpb.AgentRunPlaced{Instance: "agent-" + run.RunID, NodeId: "agents-1"}}}); err != nil {
		return err
	}
	frame, err := stream.Recv()
	if err != nil || frame.GetProceed() == nil {
		return status.Error(codes.Aborted, "not told to proceed")
	}
	f.mu.Lock()
	f.proceededFirst = true
	f.mu.Unlock()
	if run.Command[0] == "drop" {
		return status.Error(codes.Unavailable, "vmmd went away")
	}
	_ = stream.Send(&scheddpb.AgentRunEvent{Frame: &scheddpb.AgentRunEvent_Booted{Booted: &scheddpb.AgentRunBooted{Method: "WAKE_COLD_BOOT", BootMs: 900}}})
	_ = stream.Send(&scheddpb.AgentRunEvent{Frame: &scheddpb.AgentRunEvent_Output{Output: &scheddpb.AgentRunOutput{Stream: "stdout", Chunk: []byte("working on " + run.Command[1])}}})
	result := agentrunproto.Result{Status: agentrunproto.StatusSucceeded, Result: json.RawMessage(`{"verdict":"ok"}`),
		Files: []agentrunproto.OutputFile{agentrunproto.NewOutputFile("report.md", []byte("# ok"))}}
	if run.Command[0] == "comment" {
		result.Result = json.RawMessage(`{"verdict":"ok","comment":"Looks good."}`)
	}
	if run.Command[0] == "wait-cancel" {
		frame, err := stream.Recv()
		if err != nil || frame.GetCancel() == nil {
			return status.Error(codes.Internal, "expected a cancel")
		}
		exit := int32(143)
		result = agentrunproto.Result{Status: agentrunproto.StatusCancelled, ExitCode: exit}
	}
	body, _ := json.Marshal(result)
	return stream.Send(&scheddpb.AgentRunEvent{Frame: &scheddpb.AgentRunEvent_Result{Result: &scheddpb.AgentRunResult{Result: body}}})
}

func newAgentd(t *testing.T, store *agentstore.Store, fake *fakeSchedd) *agentd.Agentd {
	t.Helper()
	srv := grpc.NewServer()
	scheddpb.RegisterScheddServer(srv, fake)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return agentd.New(store, scheddpb.NewScheddClient(conn), agentd.Config{Owner: "agentd-test", LeaseTTL: 300 * time.Millisecond,
		PollInterval: 10 * time.Millisecond}, nil)
}

func runState(t *testing.T, s *agentstore.Store, id string) agentstore.Run {
	t.Helper()
	run, err := s.Run(context.Background(), account, id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestRunSucceeds(t *testing.T) {
	pool, store := openStore(t)
	fake := &fakeSchedd{}
	id := queueRun(t, store, "ok")
	if claimed, err := newAgentd(t, store, fake).ClaimOnce(context.Background()); err != nil || !claimed {
		t.Fatalf("claim %v %v", claimed, err)
	}
	run := runState(t, store, id)
	if run.State != agentstore.StateSucceeded || run.FinishedAt == nil {
		t.Fatalf("run %+v", run)
	}
	if !fake.proceededFirst || fake.start.GetRamMb() != 1024 || fake.start.GetVcpu() != 2 || fake.start.GetPlan() != "pro" {
		t.Fatalf("start %+v proceeded %v", fake.start, fake.proceededFirst)
	}
	if prompt := fake.run.Command[1]; prompt != "Triage PR 7: be brief" {
		t.Fatalf("rendered prompt %q", prompt)
	}
	out, err := store.RunOutputs(context.Background(), account, id)
	if err != nil || string(out.LogTail) != "working on Triage PR 7: be brief" || len(out.Files) != 1 || out.Files[0].Name != "report.md" {
		t.Fatalf("outputs %+v %v", out, err)
	}
	var node, instance string
	var boot int
	var budget int64
	if err := pool.QueryRow(context.Background(), `SELECT node_id, instance_id, boot_ms, ai_budget_millicents FROM agent_runs WHERE id=$1`, id).
		Scan(&node, &instance, &boot, &budget); err != nil || node != "agents-1" || instance != "agent-"+id || boot != 900 ||
		budget != api.AgentRunAIBudgetDefaultMillicents[api.PlanPro.PlanIndex()] {
		t.Fatalf("placement %q %q %d %d %v", node, instance, boot, budget, err)
	}
	relay := fake.start.GetRelay()
	if relay.GetLeaseToken() == "" || relay.GetAgentId() == "" || relay.GetAgentVersion() != 1 {
		t.Fatalf("relay identity %+v", relay)
	}
}

func TestRunOutcomesBeforeAndAfterPlacement(t *testing.T) {
	_, store := openStore(t)
	fake := &fakeSchedd{}
	a := newAgentd(t, store, fake)
	for script, want := range map[string]struct {
		state, outcome string
	}{
		"full":   {agentstore.StateQueued, ""},                 // nothing booted: back to the queue
		"reject": {agentstore.StateFailed, "rejected"},         // outside the plan
		"drop":   {agentstore.StateFailed, "transport_failed"}, // may have reached a VM: never replayed
	} {
		id := queueRun(t, store, script)
		if claimed, err := a.ClaimOnce(context.Background()); err != nil || !claimed {
			t.Fatalf("%s: claim %v %v", script, claimed, err)
		}
		run := runState(t, store, id)
		if run.State != want.state || run.OutcomeCode != want.outcome {
			t.Errorf("%s: %s %q, want %s %q", script, run.State, run.OutcomeCode, want.state, want.outcome)
		}
		if script == "full" {
			// Leave it queued but out of the way of the next claims.
			if _, err := store.CancelRun(context.Background(), account, id); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestCancelReachesARunningRun(t *testing.T) {
	_, store := openStore(t)
	fake := &fakeSchedd{}
	a := newAgentd(t, store, fake)
	id := queueRun(t, store, "wait-cancel")
	done := make(chan error, 1)
	go func() {
		_, err := a.ClaimOnce(context.Background())
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for runState(t, store, id).State != agentstore.StateRunning {
		if time.Now().After(deadline) {
			t.Fatal("run never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := store.CancelRun(context.Background(), account, id); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancel never reached the run")
	}
	if run := runState(t, store, id); run.State != agentstore.StateCancelled {
		t.Fatalf("run %+v", run)
	}
}

func TestSweepAndRunLoop(t *testing.T) {
	pool, store := openStore(t)
	fake := &fakeSchedd{}
	a := newAgentd(t, store, fake)
	stuck := queueRun(t, store, "ok")
	if _, err := pool.Exec(context.Background(), `UPDATE agent_runs SET state='running', started_at=now(), running_at=now(),
		lease_token='gone', lease_expires_at=now()-interval '1 second' WHERE id=$1`, stuck); err != nil {
		t.Fatal(err)
	}
	if err := a.SweepOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if run := runState(t, store, stuck); run.State != agentstore.StateFailed || run.OutcomeCode != "lease_lost" {
		t.Fatalf("expired running run %+v", run)
	}
	ids := []string{queueRun(t, store, "ok2"), queueRun(t, store, "ok3")}
	ctx, stop := context.WithCancel(context.Background())
	loop := make(chan error, 1)
	go func() { loop <- a.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for _, id := range ids {
		for runState(t, store, id).State != agentstore.StateSucceeded {
			if time.Now().After(deadline) {
				t.Fatalf("run %s is %s", id, runState(t, store, id).State)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	stop()
	if err := <-loop; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// A run with a checkout gets its input drive key and the archive's digest,
// or fails at once when this replica cannot prepare checkouts.
func TestRunWithCheckout(t *testing.T) {
	_, store := openStore(t)
	ctx := context.Background()
	without := queueRunWith(t, store, "ok", &agentstore.Checkout{Repository: "acme/app", Commit: commit})
	if claimed, err := newAgentd(t, store, &fakeSchedd{}).ClaimOnce(ctx); err != nil || !claimed {
		t.Fatalf("claim %v %v", claimed, err)
	}
	if run := runState(t, store, without); run.State != agentstore.StateFailed || run.OutcomeCode != "checkout_failed" {
		t.Fatalf("run without a checkout source %+v", run)
	}

	fake := &fakeSchedd{}
	c, _ := checkouts(t, &fakeSource{archive: repoArchive(t)})
	id := queueRunWith(t, store, "ok-checkout", &agentstore.Checkout{Repository: "acme/app", Commit: commit})
	if claimed, err := newAgentd(t, store, fake).WithCheckouts(c).ClaimOnce(ctx); err != nil || !claimed {
		t.Fatalf("claim %v %v", claimed, err)
	}
	if run := runState(t, store, id); run.State != agentstore.StateSucceeded || run.Checkout == nil || run.Checkout.InstallationID != 42 {
		t.Fatalf("run %+v", run)
	}
	if !agentrunproto.ValidInputKey(fake.start.GetInputKey(), account) || fake.run.Checkout == nil || fake.run.Checkout.Bytes < 1 {
		t.Fatalf("start %+v checkout %+v", fake.start, fake.run.Checkout)
	}
}
