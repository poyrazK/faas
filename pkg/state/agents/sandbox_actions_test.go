// adr: 20261117065818 §2 — a gregale_browser action is recorded once per
// tool call as its session's next; agentd sees one pending action per
// session that runs none, and starts and ends it only under the session's
// lease.
package agents_test

import (
	"context"
	"errors"
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
	if err := s.FinishSandboxAction(ctx, first.ID, "tok", &code, []byte(`{"title":"Example"}`), agents.SandboxScreenshot{}); err != nil {
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

// adr: 20261117065818 §3 — gregale_browser opens a session and its first
// action in one transaction, once per call; the session answers its first
// action's thread; ended actions are listed for delivery until marked.
func TestBrowserSessions(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	const endpoint = "55555555-5555-5555-5555-555555555555"
	if _, err := pool.Exec(ctx, `INSERT INTO managed_realtime_endpoints VALUES ($1)`, endpoint); err != nil {
		t.Fatal(err)
	}
	call := agents.SandboxTaskCall{EndpointID: endpoint, ThreadID: "thread-1", ToolCallID: "call_open", IssuedAt: time.Now().UTC()}
	admission := sandboxAdmission(agents.SandboxAdmission{Browser: true, NetworkMode: "web"})
	sb, existing, err := s.CreateBrowserSession(ctx, admission, agents.SandboxActionArgs{URL: "https://example.com/", MaxChars: 4000}, call)
	if err != nil || existing {
		t.Fatalf("open = %+v, %v, %v", sb, existing, err)
	}
	again, existing, err := s.CreateBrowserSession(ctx, admission, agents.SandboxActionArgs{URL: "https://example.com/"}, call)
	if err != nil || !existing || again.ID != sb.ID {
		t.Fatalf("a retried open = %+v, %v, %v; want %s", again, existing, err, sb.ID)
	}
	th, err := s.BrowserSessionThread(ctx, account, sb.ID)
	if err != nil || th.EndpointID != endpoint || th.ThreadID != "thread-1" || th.Actions != 1 || th.State != agents.SandboxQueued {
		t.Fatalf("thread = %+v, %v", th, err)
	}
	plain := admitSandbox(t, s, agents.SandboxAdmission{})
	if _, err := s.BrowserSessionThread(ctx, account, plain.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("a session gregale_browser did not open: %v", err)
	}

	claim, found, err := s.ClaimSandbox(ctx, "agentd", "tok", ttl, maxLive(5))
	if err != nil || !found || claim.ID != sb.ID {
		t.Fatalf("claim = %+v, %v, %v", claim, found, err)
	}
	next, err := s.NextSandboxActions(ctx, []string{sb.ID})
	if err != nil || len(next) != 1 || next[0].Seq != 1 || next[0].Kind != "navigate" {
		t.Fatalf("next = %+v, %v", next, err)
	}
	if ok, err := s.StartSandboxAction(ctx, next[0].ID, "tok"); err != nil || !ok {
		t.Fatalf("start = %v, %v", ok, err)
	}
	if ends, err := s.UndeliveredSandboxActions(ctx, 10); err != nil || len(ends) != 0 {
		t.Fatalf("a running action is listed for delivery: %+v, %v", ends, err)
	}
	code := 0
	shot := agents.SandboxScreenshot{Key: "sandbox-screenshots/" + account + "/" + sb.ID + "/" + next[0].ID + ".jpg", Bytes: 1234}
	if err := s.FinishSandboxAction(ctx, next[0].ID, "tok", &code, []byte(`{"title":"E"}`), shot); err != nil {
		t.Fatal(err)
	}
	ends, err := s.UndeliveredSandboxActions(ctx, 10)
	if err != nil || len(ends) != 1 {
		t.Fatalf("undelivered = %+v, %v", ends, err)
	}
	if end := ends[0]; end.SessionID != sb.ID || end.Seq != 1 || end.Call.ToolCallID != "call_open" || end.Args.URL != "https://example.com/" ||
		end.ExitCode == nil || *end.ExitCode != 0 || string(end.Output) != `{"title":"E"}` || end.SessionState == "" || end.ScreenshotBytes != 1234 {
		t.Fatalf("end = %+v", end)
	}
	res, err := s.SandboxActionResult(ctx, account, sb.ID, 1)
	if err != nil || res.Kind != "navigate" || res.State != agents.SandboxActionDone || string(res.Output) != `{"title":"E"}` || res.Screenshot != shot {
		t.Fatalf("result = %+v, %v", res, err)
	}
	// adr: 20261117065818 §5 — a screenshot is swept only once its session
	// ended its retention ago, and its key is then forgotten.
	if due, err := s.ExpiredSandboxScreenshots(ctx, time.Hour, 10); err != nil || len(due) != 0 {
		t.Fatalf("a live session's screenshot is due: %+v, %v", due, err)
	}
	if err := s.FinishSandbox(ctx, sb.ID, "tok", agents.SandboxClosed, "closed", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sandbox_sessions SET closed_at = now() - interval '2 hours' WHERE id = $1`, sb.ID); err != nil {
		t.Fatal(err)
	}
	due, err := s.ExpiredSandboxScreenshots(ctx, time.Hour, 10)
	if err != nil || len(due) != 1 || due[0].ActionID != next[0].ID || due[0].Key != shot.Key {
		t.Fatalf("due = %+v, %v", due, err)
	}
	if err := s.ClearSandboxScreenshot(ctx, next[0].ID); err != nil {
		t.Fatal(err)
	}
	if due, err := s.ExpiredSandboxScreenshots(ctx, time.Hour, 10); err != nil || len(due) != 0 {
		t.Fatalf("a cleared screenshot is due again: %+v, %v", due, err)
	}
	if _, err := s.SandboxActionResult(ctx, account, sb.ID, 2); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("a missing action: %v", err)
	}
	if err := s.MarkSandboxActionDelivered(ctx, ends[0].ID); err != nil {
		t.Fatal(err)
	}
	if ends, err := s.UndeliveredSandboxActions(ctx, 10); err != nil || len(ends) != 0 {
		t.Fatalf("a delivered action is listed again: %+v, %v", ends, err)
	}
}
