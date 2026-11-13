// adr: 20261027090013 §1, §7 — a session is admitted queued within its
// account's queued limit, shared with runs; only a live session changes; apid
// closes a queued session and asks agentd to close a claimed one; an ended
// session stays ended.
package agents_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func admitSandbox(t *testing.T, s *agents.Store, a agents.SandboxAdmission) agents.Sandbox {
	t.Helper()
	sb, err := s.CreateSandbox(context.Background(), sandboxAdmission(a))
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	return sb
}

func sandboxAdmission(a agents.SandboxAdmission) agents.SandboxAdmission {
	if a.AccountID == "" {
		a.AccountID = account
	}
	if a.Kind == "" {
		a.Kind = "ephemeral"
	}
	if a.Preset == "" {
		a.Preset = "codex"
	}
	if a.RAMMB == 0 {
		a.RAMMB = 1024
	}
	if a.NetworkMode == "" {
		a.NetworkMode = "registries"
	}
	if a.IdleTimeoutSec == 0 {
		a.IdleTimeoutSec = 600
	}
	if a.ExpiresAt.IsZero() {
		a.ExpiresAt = time.Now().Add(time.Hour)
	}
	if a.QueuedMax == 0 {
		a.QueuedMax = 100
	}
	return a
}

func TestSandboxSessions(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	sb := admitSandbox(t, s, agents.SandboxAdmission{TenantID: tenant, Labels: map[string]string{"team": "triage"}, ExpiresAt: expires})
	if sb.State != agents.SandboxQueued || sb.Kind != "ephemeral" || sb.Preset != "codex" || sb.RAMMB != 1024 || sb.NetworkMode != "registries" ||
		sb.TenantID != tenant || sb.IdleTimeoutSec != 600 || !sb.ExpiresAt.Equal(expires) || sb.Labels["team"] != "triage" || sb.ClosedAt != nil {
		t.Fatalf("created %+v", sb)
	}
	for name, want := range map[string]error{suspended: agents.ErrTenantInactive, foreign: agents.ErrTenantNotFound, "acme": agents.ErrTenantNotFound} {
		if _, err := s.CreateSandbox(ctx, sandboxAdmission(agents.SandboxAdmission{TenantID: name})); !errors.Is(err, want) {
			t.Errorf("tenant %s: %v, want %v", name, err, want)
		}
	}
	if _, err := s.Sandbox(ctx, other, sb.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("another account reads the session: %v", err)
	}

	// Update and close change only a live session.
	later := expires.Add(time.Hour)
	if got, err := s.UpdateSandboxTimeouts(ctx, account, sb.ID, 60, later); err != nil || got.IdleTimeoutSec != 60 || !got.ExpiresAt.Equal(later) {
		t.Fatalf("update %+v %v", got, err)
	}
	closed, err := s.CloseSandbox(ctx, account, sb.ID)
	if err != nil || closed.State != agents.SandboxClosed || closed.ClosedAt == nil || closed.OutcomeCode != "closed" {
		t.Fatalf("close a queued session %+v %v", closed, err)
	}
	if _, err := s.UpdateSandboxTimeouts(ctx, account, sb.ID, 30, later); !errors.Is(err, agents.ErrFinished) {
		t.Fatalf("update an ended session: %v", err)
	}
	if again, err := s.CloseSandbox(ctx, account, sb.ID); err != nil || !again.ClosedAt.Equal(*closed.ClosedAt) || again.CloseRequestedAt != nil {
		t.Fatalf("close again %+v %v", again, err)
	}

	// A claimed session is agentd's: closing it is a request.
	claimed := admitSandbox(t, s, agents.SandboxAdmission{})
	if _, err := pool.Exec(ctx, `UPDATE sandbox_sessions SET state='starting', lease_token='t', lease_expires_at=now()+interval '1 minute' WHERE id=$1`, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.CloseSandbox(ctx, account, claimed.ID); err != nil || got.State != agents.SandboxStarting || got.CloseRequestedAt == nil {
		t.Fatalf("close a claimed session %+v %v", got, err)
	}
}

// Queued sessions and queued runs share AgentRunQueuedPerAccount.
func TestSandboxQueuedLimitIsSharedWithRuns(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	if _, _, err := s.CreateAgent(ctx, account, "triage", "", spec(), 10); err != nil {
		t.Fatal(err)
	}
	admit(t, s, agents.Admission{})
	admitSandbox(t, s, agents.SandboxAdmission{QueuedMax: 3})
	admitSandbox(t, s, agents.SandboxAdmission{QueuedMax: 3})
	var limit *agents.LimitError
	if _, err := s.CreateSandbox(ctx, sandboxAdmission(agents.SandboxAdmission{QueuedMax: 3})); !errors.As(err, &limit) ||
		limit.Limit != "AgentRunQueuedPerAccount" || limit.Max != 3 || limit.Observed != 3 {
		t.Fatalf("past the shared limit: %v", err)
	}
	if _, err := s.CreateSandbox(ctx, sandboxAdmission(agents.SandboxAdmission{AccountID: other, QueuedMax: 3})); err != nil {
		t.Fatalf("another account's limit is its own: %v", err)
	}
}

func TestSandboxListPagesNewestFirst(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	var ids []string
	for range 4 {
		ids = append(ids, admitSandbox(t, s, agents.SandboxAdmission{}).ID)
		time.Sleep(2 * time.Millisecond)
	}
	admitSandbox(t, s, agents.SandboxAdmission{AccountID: other})
	if _, err := pool.Exec(ctx, `UPDATE sandbox_sessions SET state='ready', lease_token='t', lease_expires_at=now() WHERE id=$1`, ids[1]); err != nil {
		t.Fatal(err)
	}
	page, err := s.Sandboxes(ctx, account, agents.SandboxFilter{Limit: 2})
	if err != nil || len(page) != 2 || page[0].ID != ids[3] || page[1].ID != ids[2] {
		t.Fatalf("first page %v %v", page, err)
	}
	rest, err := s.Sandboxes(ctx, account, agents.SandboxFilter{Limit: 10, Before: &page[1]})
	if err != nil || len(rest) != 2 || rest[0].ID != ids[1] || rest[1].ID != ids[0] {
		t.Fatalf("second page %v %v", rest, err)
	}
	ready, err := s.Sandboxes(ctx, account, agents.SandboxFilter{State: agents.SandboxReady, Limit: 10})
	if err != nil || len(ready) != 1 || ready[0].ID != ids[1] {
		t.Fatalf("ready %v %v", ready, err)
	}
}
