package agents_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
