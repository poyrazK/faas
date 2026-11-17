// adr: 20261117065818 §2 — a gregale_browser action is recorded once per
// tool call as its session's next; agentd sees one pending action per
// session that runs none, and starts and ends it only under the session's
// lease.
package agents_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func TestSandboxActions(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	const endpoint = "55555555-5555-5555-5555-555555555555"
	if _, err := pool.Exec(ctx, `INSERT INTO managed_realtime_endpoints VALUES ($1)`, endpoint); err != nil {
		t.Fatal(err)
	}
	sb := admitSandbox(t, s, agents.SandboxAdmission{Browser: true, NetworkMode: "web"})
	call := func(id string) agents.SandboxTaskCall {
		return agents.SandboxTaskCall{EndpointID: endpoint, ThreadID: "thread-1", ToolCallID: id, IssuedAt: time.Now().UTC()}
	}
	first, existing, err := s.CreateSandboxAction(ctx, account, sb.ID, "navigate",
		agents.SandboxActionArgs{URL: "https://example.com/", MaxChars: 4000}, call("call_1"))
	if err != nil || existing || first.Seq != 1 {
		t.Fatalf("first action = %+v, %v, %v", first, existing, err)
	}
	again, existing, err := s.CreateSandboxAction(ctx, account, sb.ID, "navigate",
		agents.SandboxActionArgs{URL: "https://example.com/"}, call("call_1"))
	if err != nil || !existing || again.ID != first.ID {
		t.Fatalf("a retried call = %+v, %v, %v; want %s", again, existing, err, first.ID)
	}
	second, _, err := s.CreateSandboxAction(ctx, account, sb.ID, "click", agents.SandboxActionArgs{Ref: 7}, call("call_2"))
	if err != nil || second.Seq != 2 {
		t.Fatalf("second action = %+v, %v", second, err)
	}

	claim, found, err := s.ClaimSandbox(ctx, "agentd", "tok", ttl, maxLive(5))
	if err != nil || !found || claim.ID != sb.ID {
		t.Fatalf("claim = %+v, %v, %v", claim, found, err)
	}
	next, err := s.NextSandboxActions(ctx, []string{sb.ID})
	if err != nil || len(next) != 1 || next[0].ID != first.ID || next[0].Kind != "navigate" ||
		next[0].Args.URL != "https://example.com/" || next[0].Args.MaxChars != 4000 {
		t.Fatalf("next = %+v, %v", next, err)
	}
	if ok, err := s.StartSandboxAction(ctx, first.ID, "other"); err != nil || ok {
		t.Fatalf("a start under another lease = %v, %v", ok, err)
	}
	if ok, err := s.StartSandboxAction(ctx, first.ID, "tok"); err != nil || !ok {
		t.Fatalf("start = %v, %v", ok, err)
	}
	if next, err := s.NextSandboxActions(ctx, []string{sb.ID}); err != nil || len(next) != 0 {
		t.Fatalf("a session running an action was given another: %+v, %v", next, err)
	}
	code := 0
	if err := s.FinishSandboxAction(ctx, first.ID, "tok", &code, []byte(`{"title":"Example"}`)); err != nil {
		t.Fatal(err)
	}
	var state string
	var exit *int
	var output []byte
	if err := pool.QueryRow(ctx, `SELECT state, exit_code, output FROM sandbox_actions WHERE id=$1`, first.ID).Scan(&state, &exit, &output); err != nil {
		t.Fatal(err)
	}
	if state != agents.SandboxActionDone || exit == nil || *exit != 0 || string(output) != `{"title":"Example"}` {
		t.Fatalf("finished action = %s, %v, %q", state, exit, output)
	}
	next, err = s.NextSandboxActions(ctx, []string{sb.ID})
	if err != nil || len(next) != 1 || next[0].ID != second.ID || next[0].Args.Ref != 7 {
		t.Fatalf("next after the first = %+v, %v", next, err)
	}
	if ok, err := s.StartSandboxAction(ctx, second.ID, "tok"); err != nil || !ok {
		t.Fatalf("start second = %v, %v", ok, err)
	}
	if err := s.EndOpenSandboxActions(ctx, sb.ID, "tok", agents.SandboxActionPending, agents.SandboxActionRunning); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT state, exit_code FROM sandbox_actions WHERE id=$1`, second.ID).Scan(&state, &exit); err != nil {
		t.Fatal(err)
	}
	if state != agents.SandboxActionDone || exit != nil {
		t.Fatalf("an ended open action = %s, %v", state, exit)
	}
}
