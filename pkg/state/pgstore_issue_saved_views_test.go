package state_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func assertIssueViewRollbackRefusesUsedSettings(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	sql, err := migrations.FS.ReadFile("20261013194001001_issue_saved_views_and_routing.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := strings.SplitN(string(sql), "-- +goose Down", 2)[1]
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, down)
	_ = tx.Rollback(ctx)
	if err == nil || !strings.Contains(err.Error(), "remove them explicitly before rollback") {
		t.Fatalf("in-use configuration could be discarded or routing widened: %v", err)
	}
}

func TestPg_IssueSavedViewsScopeQuotaAndRollbackGuard(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	now := time.Now().UTC().Truncate(time.Microsecond)
	in := api.PutIssueSavedViewRequest{Name: " My payments ", Filters: api.IssueSavedViewFilters{State: "open", Assignee: "me", Sort: "impact", MinCustomers: 2, Tag: "component=payments"}}
	view, err := s.PutIssueSavedView(ctx, app.ID, account.ID, account.ID, "mine", in, now)
	if err != nil || view.Name != "My payments" || view.Filters.Assignee != "me" || view.UpdatedByAccountID != account.ID {
		t.Fatalf("save: %+v %v", view, err)
	}
	in.Name = "Mine renamed"
	replaced, err := s.PutIssueSavedView(ctx, app.ID, account.ID, account.ID, "mine", in, now.Add(time.Minute))
	if err != nil || !replaced.CreatedAt.Equal(view.CreatedAt) || !replaced.UpdatedAt.After(view.UpdatedAt) {
		t.Fatalf("replacement reset creation metadata: %+v %v", replaced, err)
	}
	for _, fn := range []func() error{
		func() error { _, err := s.GetIssueSavedView(ctx, app.ID, uuid.NewString(), "mine"); return err },
		func() error { _, err := s.ListIssueSavedViews(ctx, app.ID, uuid.NewString()); return err },
		func() error {
			_, err := s.PutIssueSavedView(ctx, app.ID, uuid.NewString(), account.ID, "mine", in, now)
			return err
		},
		func() error { return s.DeleteIssueSavedView(ctx, app.ID, uuid.NewString(), "mine") },
	} {
		if err := fn(); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cross-owner view access: %v", err)
		}
	}
	invalid := in
	invalid.Filters.Assignee = uuid.NewString()
	if _, err := s.PutIssueSavedView(ctx, app.ID, account.ID, account.ID, "invalid", invalid, now); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("ineligible owner filter persisted: %v", err)
	}
	for i := 1; i < api.IssueSavedViewsMax-1; i++ {
		if _, err := s.PutIssueSavedView(ctx, app.ID, account.ID, account.ID, fmt.Sprintf("view-%d", i), in, now); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []string{"last-one", "last-two"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, err := s.PutIssueSavedView(ctx, app.ID, account.ID, account.ID, key, in, now)
			results <- err
		}(key)
	}
	wg.Wait()
	close(results)
	success, quota := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, state.ErrIssueQuota) {
			quota++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || quota != 1 {
		t.Fatalf("quota raced: success=%d quota=%d", success, quota)
	}
	if _, err := s.PutIssueSavedView(ctx, app.ID, account.ID, account.ID, "mine", in, now.Add(time.Hour)); err != nil {
		t.Fatalf("existing view blocked at quota: %v", err)
	}
	views, err := s.ListIssueSavedViews(ctx, app.ID, account.ID)
	if err != nil || len(views.Items) != api.IssueSavedViewsMax {
		t.Fatalf("list: %+v %v", views, err)
	}
	assertIssueViewRollbackRefusesUsedSettings(t, ctx, pool)
	if _, err := s.GetIssueSavedView(ctx, app.ID, account.ID, "mine"); err != nil {
		t.Fatal("rollback guard lost saved data")
	}
	if err := s.DeleteIssueSavedView(ctx, app.ID, account.ID, "mine"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutIssueSavedView(ctx, app.ID, account.ID, account.ID, "replacement", in, now); err != nil {
		t.Fatalf("delete did not free quota: %v", err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "view-schema"})
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := s.ProjectEnvironmentCloneSchemaCoverage(ctx, account.ID, project.ID)
	if err != nil || !coverage.Known {
		t.Fatalf("migrated columns missing from clone registry: %+v %v", coverage, err)
	}
}

func TestPg_IssueSourceTagRoutingAndInboxCursorBinding(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	rules := []api.IssueOwnershipRule{{SourcePathPrefix: "/app/src/payments", Tag: "component=payments", AssigneeAccountID: account.ID}, {SourceKind: "exception", AssigneeAccountID: account.ID}}
	if _, err := s.SetIssueOwnershipRules(ctx, app.ID, account.ID, rules); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetIssueOwnershipRules(ctx, app.ID)
	if err != nil || len(stored.Rules) != 2 || stored.Rules[0].SourcePathPrefix != rules[0].SourcePathPrefix || stored.Rules[0].Tag != rules[0].Tag {
		t.Fatalf("routing not persisted: %+v %v", stored, err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	record := func(index int, file string) api.IssueEventResponse {
		t.Helper()
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now.Add(-time.Minute), ExceptionType: "PaymentError", Message: "failed", SourceKind: "exception", Frames: []api.IssueFrame{{File: file, Function: "charge", InApp: true}}, Tags: map[string]string{"component": "payments"}}
		out, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: event, Fingerprint: fmt.Sprintf("%064x", index), Title: "PaymentError", PayloadHash: strings.Repeat("a", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := record(1, "/app/src/payments/charge.ts")
	detail, err := s.GetIssueDetail(ctx, app.ID, first.IssueID, now.Add(-time.Hour), now, state.IssueDetailCursors{})
	if err != nil || detail.Issue.AssigneeAccountID != account.ID {
		t.Fatalf("new group not routed: %+v %v", detail.Issue, err)
	}
	matched := false
	for _, activity := range detail.Activity {
		if activity.Action == "assigned" && activity.Details["source_path_prefix"] == rules[0].SourcePathPrefix && activity.Details["tag"] == rules[0].Tag {
			matched = true
		}
	}
	if !matched {
		t.Fatal("first matching source/tag rule was not audited")
	}
	if _, err := s.ActOnIssue(ctx, app.ID, first.IssueID, account.ID, api.IssueActionRequest{Action: "assign"}, now); err != nil {
		t.Fatal(err)
	}
	_ = record(1, "/app/src/payments/charge.ts")
	detail, err = s.GetIssueDetail(ctx, app.ID, first.IssueID, now.Add(-time.Hour), now, state.IssueDetailCursors{})
	if err != nil || detail.Issue.AssigneeAccountID != "" {
		t.Fatal("recurrence overwrote manual ownership")
	}
	fallback := record(2, "/app/src/payments-admin/charge.ts")
	detail, err = s.GetIssueDetail(ctx, app.ID, fallback.IssueID, now.Add(-time.Hour), now, state.IssueDetailCursors{})
	if err != nil {
		t.Fatal(err)
	}
	for _, activity := range detail.Activity {
		if activity.Action == "assigned" && activity.Details["source_path_prefix"] != "" {
			t.Fatal("source prefix crossed a path boundary")
		}
	}
	for i := 3; i <= api.IssuePageSize+3; i++ {
		_ = record(i, "/app/src/payments/charge.ts")
	}
	filter := state.IssueListFilter{State: "open", AssigneeAccountID: account.ID, Sort: "recent"}
	list, err := s.ListIssues(ctx, app.ID, filter, state.IssueCursor{})
	if err != nil || list.NextCursor == "" {
		t.Fatalf("pagination fixture: %+v %v", list, err)
	}
	cursor, err := state.DecodeIssueCursor(list.NextCursor)
	if err != nil || len(cursor.FilterHash) != 64 || state.ValidateIssueListCursor(filter, cursor) != nil {
		t.Fatalf("new cursor missing filter binding: %+v %v", cursor, err)
	}
	for _, changed := range []state.IssueListFilter{{State: "open", TeamID: uuid.NewString(), Sort: "recent"}, {State: "resolved", AssigneeAccountID: account.ID, Sort: "recent"}, {State: "open", AssigneeAccountID: uuid.NewString(), Sort: "recent"}, {State: "open", AssigneeAccountID: account.ID, Environment: "staging", Sort: "recent"}} {
		if _, err := s.ListIssues(ctx, app.ID, changed, cursor); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("cursor reused with different saved filters: %+v %v", changed, err)
		}
	}
	assertIssueViewRollbackRefusesUsedSettings(t, ctx, pool)
}
