package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgIssueResponseMembershipRemovalIsSerialized(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	member, err := s.CreateAccount(ctx, "issue-fence-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := s.CreateOrg(ctx, state.Org{Slug: "issue-fence-" + uuid.NewString()[:8], Name: "Issue fence", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.AddOrgMember(ctx, org.ID, member.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE apps SET org_id=$2 WHERE id=$1`, app.ID, org.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	issue, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: owner.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "FenceError", SourceKind: "exception"}, Fingerprint: strings.Repeat("a", 64), Title: "Fence", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE app_issues SET assignee_account_id=$2 WHERE id=$1`, issue.IssueID, member.ID); err != nil {
		t.Fatal(err)
	}
	waitFor := func(fragment string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			var waiting bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND position($1 in query)>0)`, fragment).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("no lock waiter for %q", fragment)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	act := func() error {
		return s.UpdateIssueApplicationTriage(ctx, app.ID, owner.ID, member.ID, issue.IssueID, 0, state.IssueApplicationTriageAction{Action: "acknowledge"})
	}

	// If removal acquires the membership row first, the action must wait and
	// then reject the now-removed actor without recording response state.
	removal, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = removal.Rollback(context.Background()) }()
	if _, err = removal.Exec(ctx, `UPDATE org_memberships SET removed_at=clock_timestamp() WHERE org_id=$1 AND account_id=$2`, org.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- act() }()
	waitFor("LockMembership")
	if err = removal.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("removed actor wrote response: %v", err)
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM issue_application_triage WHERE issue_id=$1`, issue.IssueID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("removed action persisted: %d %v", n, err)
	}

	if _, err = pool.Exec(ctx, `UPDATE org_memberships SET removed_at=NULL WHERE org_id=$1 AND account_id=$2`, org.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	// Conversely, once the action has the membership lock, removal must wait
	// until the action has either committed or rolled back.
	blockApp, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blockApp.Rollback(context.Background()) }()
	if _, err = blockApp.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app.ID); err != nil {
		t.Fatal(err)
	}
	go func() { result <- act() }()
	waitFor("IssueLockApp")
	removed := make(chan error, 1)
	go func() { removed <- s.RemoveOrgMember(ctx, org.ID, member.ID) }()
	select {
	case err := <-removed:
		t.Fatalf("removal passed a live response transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = blockApp.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatalf("authorized action failed: %v", err)
	}
	if err = <-removed; err != nil {
		t.Fatalf("removal did not complete: %v", err)
	}
}
