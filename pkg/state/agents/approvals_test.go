package agents_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/agents"
)

// adr: 20261027090016 amendment 2 — a decision is written once, only while
// the approval is pending, and only by the run's account.
func TestRunApprovalDecisions(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	if _, _, err := s.CreateAgent(ctx, account, "triage", "", spec(), 2); err != nil {
		t.Fatal(err)
	}
	run := admit(t, s, agents.Admission{})
	insert := func(expires string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO agent_run_approvals (run_id, account_id, tool, arguments_sha256, arguments_summary, expires_at)
			VALUES ($1, $2, 'web_fetch', $3, '{"url":"https://example.com"}', now() + $4::interval) RETURNING id`,
			run.ID, account, strings.Repeat("a", 64), expires).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	pending, expired := insert("1 hour"), insert("1 hour")
	if _, err := pool.Exec(ctx, `UPDATE agent_run_approvals SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 second'
		WHERE id = $1`, expired); err != nil {
		t.Fatal(err)
	}

	list, err := s.RunApprovals(ctx, account, run.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %+v", err, list)
	}
	if _, err := s.RunApprovals(ctx, other, run.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Errorf("another account listed the run: %v", err)
	}

	if _, err := s.DecideRunApproval(ctx, other, run.ID, pending, "approve", "", "x"); !errors.Is(err, agents.ErrNotFound) {
		t.Errorf("another account decided: %v", err)
	}
	got, err := s.DecideRunApproval(ctx, account, run.ID, pending, "approve", "looks fine", "api_key:k1")
	if err != nil || got.State(time.Now()) != api.AgentRunApprovalApproved || got.Note != "looks fine" || got.DecidedAt == nil {
		t.Fatalf("approve: %v %+v", err, got)
	}
	if got, err := s.DecideRunApproval(ctx, account, run.ID, pending, "deny", "", "x"); !errors.Is(err, agents.ErrApprovalClosed) ||
		got.State(time.Now()) != api.AgentRunApprovalApproved {
		t.Errorf("second decision: %v %+v", err, got)
	}
	if got, err := s.DecideRunApproval(ctx, account, run.ID, expired, "approve", "", "x"); !errors.Is(err, agents.ErrApprovalClosed) ||
		got.State(time.Now()) != api.AgentRunApprovalExpired {
		t.Errorf("expired decision: %v %+v", err, got)
	}
	if _, err := s.DecideRunApproval(ctx, account, run.ID, "66666666-6666-6666-6666-666666666666", "approve", "", "x"); !errors.Is(err, agents.ErrNotFound) {
		t.Errorf("unknown approval: %v", err)
	}
	if _, err := s.DecideRunApproval(ctx, account, run.ID, "nope", "approve", "", "x"); !errors.Is(err, agents.ErrNotFound) {
		t.Errorf("malformed id: %v", err)
	}
}

// ADR-20261027090016 amendment 3: a run's credit is the union of its
// approvals' waits, each ending at its decision, expiry or release.
func TestRunApprovalCredit(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	if _, _, err := s.CreateAgent(ctx, account, "triage", "", spec(), 2); err != nil {
		t.Fatal(err)
	}
	run := admit(t, s, agents.Admission{})
	lease := uuid.NewString()
	if _, found, err := s.ClaimRun(ctx, "agentd-1", lease, time.Minute, func(string) int { return 5 }); err != nil || !found {
		t.Fatalf("claim %v %v", found, err)
	}
	if err := s.MarkRunningCredited(ctx, run.ID, lease, "n", "agent-"+run.ID, 1000, api.AgentRunApprovalCreditMaxSec+1); err == nil {
		t.Fatal("a credit cap above the limit was accepted")
	}
	if err := s.MarkRunningCredited(ctx, run.ID, lease, "n", "agent-"+run.ID, 1000, 900); err != nil {
		t.Fatal(err)
	}
	var capSec int
	if err := pool.QueryRow(ctx, `SELECT deadline_credit_max_sec FROM agent_runs WHERE id = $1`, run.ID).Scan(&capSec); err != nil || capSec != 900 {
		t.Fatalf("credit cap %d %v", capSec, err)
	}
	wait := func(created, expires string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO agent_run_approvals (run_id, account_id, tool, arguments_sha256, created_at, expires_at)
			VALUES ($1, $2, 'web_fetch', $3, now() - $4::interval, now() + $5::interval) RETURNING id`,
			run.ID, account, strings.Repeat("a", 64), created, expires).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	decided := wait("100 seconds", "1 hour") // decided at −70 s: 30 s
	if _, err := pool.Exec(ctx, `INSERT INTO agent_run_approval_decisions (approval_id, account_id, decision, decided_at)
		VALUES ($1, $2, 'approve', now() - interval '70 seconds')`, decided, account); err != nil {
		t.Fatal(err)
	}
	released := wait("80 seconds", "1 hour") // released at −60 s; with the first, −100 s to −60 s
	if _, err := pool.Exec(ctx, `UPDATE agent_run_approvals SET released_at = now() - interval '60 seconds' WHERE id = $1`, released); err != nil {
		t.Fatal(err)
	}
	wait("30 seconds", "-20 seconds") // expired at −20 s: 10 s
	wait("5 seconds", "1 hour")       // still held: 5 s to now

	credit, err := s.RunApprovalCredit(ctx, run.ID, lease)
	if err != nil {
		t.Fatal(err)
	}
	if want := 55 * time.Second; credit < want || credit > want+5*time.Second {
		t.Errorf("credit %v, want about %v", credit, want)
	}
	if credit, err := s.RunApprovalCredit(ctx, run.ID, uuid.NewString()); err != nil || credit != 0 {
		t.Errorf("another lease read credit %v %v", credit, err)
	}
}
