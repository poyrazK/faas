// adr: 20261116182615 §3 — a sandbox task carries its kind and arguments to
// agentd and records its result once, under the session's lease. §4 — a
// gregale_browse call admits one task, linked to its thread, and the ended
// task is listed for delivery until marked.
package agents_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func TestSandboxTasks(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	const endpoint = "55555555-5555-5555-5555-555555555555"
	if _, err := pool.Exec(ctx, `INSERT INTO managed_realtime_endpoints VALUES ($1)`, endpoint); err != nil {
		t.Fatal(err)
	}
	task := &agents.SandboxTask{Kind: agents.SandboxTaskBrowse, URL: "https://example.com/", MaxChars: 500}
	call := agents.SandboxTaskCall{EndpointID: endpoint, ThreadID: "thread-1", ToolCallID: "call_1",
		IssuedAt: time.Now().UTC().Truncate(time.Microsecond)}
	sb, existing, err := s.CreateSandboxTask(ctx, sandboxAdmission(agents.SandboxAdmission{Browser: true, NetworkMode: "web", Task: task}), call)
	if err != nil || existing {
		t.Fatalf("create = %+v, %v, %v", sb, existing, err)
	}
	again, existing, err := s.CreateSandboxTask(ctx, sandboxAdmission(agents.SandboxAdmission{Browser: true, NetworkMode: "web", Task: task}), call)
	if err != nil || !existing || again.ID != sb.ID {
		t.Fatalf("a retried call = %+v, %v, %v; want %s", again, existing, err, sb.ID)
	}
	if _, _, err := s.CreateSandboxTask(ctx, sandboxAdmission(agents.SandboxAdmission{}), call); err == nil {
		t.Fatal("a task without arguments was admitted")
	}
	if _, err := s.SandboxTaskResult(ctx, account, sb.ID); err != nil {
		t.Fatalf("a queued task's result: %v", err)
	}
	plain := admitSandbox(t, s, agents.SandboxAdmission{})
	if _, err := s.SandboxTaskResult(ctx, account, plain.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("an ordinary session's task result: %v", err)
	}

	claim, found, err := s.ClaimSandbox(ctx, "agentd", "tok", ttl, maxLive(5))
	if err != nil || !found || claim.ID != sb.ID || claim.Task == nil || *claim.Task != *task {
		t.Fatalf("claim = %+v, %v, %v", claim, found, err)
	}
	if err := s.RecordSandboxTaskResult(ctx, sb.ID, "other", 0, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSandboxTaskResult(ctx, sb.ID, "tok", 0, []byte(`{"title":"Example"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSandboxTaskResult(ctx, sb.ID, "tok", 3, []byte("late")); err != nil {
		t.Fatal(err)
	}
	if ends, err := s.UndeliveredSandboxTasks(ctx, 10); err != nil || len(ends) != 0 {
		t.Fatalf("a live task is listed for delivery: %+v, %v", ends, err)
	}
	if err := s.FinishSandbox(ctx, sb.ID, "tok", "closed", "task_done", ""); err != nil {
		t.Fatal(err)
	}
	ends, err := s.UndeliveredSandboxTasks(ctx, 10)
	if err != nil || len(ends) != 1 {
		t.Fatalf("undelivered = %+v, %v", ends, err)
	}
	end := ends[0]
	if end.SandboxID != sb.ID || end.Call != call || end.State != "closed" || end.URL != task.URL || end.ExitCode == nil ||
		*end.ExitCode != 0 || string(end.Output) != `{"title":"Example"}` {
		t.Fatalf("end = %+v", end)
	}
	res, err := s.SandboxTaskResult(ctx, account, sb.ID)
	if err != nil || res.State != "closed" || res.ExitCode == nil || string(res.Output) != `{"title":"Example"}` || res.OutcomeCode != "task_done" {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if err := s.MarkSandboxTaskDelivered(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	if ends, err := s.UndeliveredSandboxTasks(ctx, 10); err != nil || len(ends) != 0 {
		t.Fatalf("a delivered task is listed: %+v, %v", ends, err)
	}
}
