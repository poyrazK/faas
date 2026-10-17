package state_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_IssueTrackerLinksScopeRegroupAndAudit(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	record := func(key string) string {
		t.Helper()
		out, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: key, SourceKind: "exception"}, Fingerprint: strings.Repeat(key, 64), Title: key, PayloadHash: strings.Repeat("e", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return out.IssueID
	}
	a, b := record("a"), record("b")
	in := api.PutIssueTrackerLinkRequest{URL: "https://github.com/ACME/Repo/issues/42#comment", Title: "Fix"}
	link, err := s.PutIssueTrackerLink(ctx, app.ID, account.ID, a, account.ID, in, now)
	if err != nil || link.URL != "https://github.com/acme/repo/issues/42" || link.OriginalIssueID != a {
		t.Fatalf("link: %+v %v", link, err)
	}
	same, err := s.PutIssueTrackerLink(ctx, app.ID, account.ID, a, account.ID, in, now.Add(time.Second))
	if err != nil || same.ID != link.ID || !same.UpdatedAt.Equal(link.UpdatedAt) {
		t.Fatal("identical retry changed reference", err)
	}
	in.Title = "Renamed"
	updated, err := s.PutIssueTrackerLink(ctx, app.ID, account.ID, a, account.ID, in, now.Add(time.Second))
	if err != nil || updated.ID != link.ID || !updated.CreatedAt.Equal(link.CreatedAt) || !updated.UpdatedAt.After(link.UpdatedAt) {
		t.Fatal("title update reset identity", err)
	}
	second, err := s.PutIssueTrackerLink(ctx, app.ID, account.ID, b, account.ID, api.PutIssueTrackerLinkRequest{URL: "https://linear.app/acme/issue/PAY-1/title"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListIssueTrackerLinks(ctx, app.ID, uuid.NewString(), a, state.IssueCursor{}); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-owner read", err)
	}
	if err := s.DeleteIssueTrackerLink(ctx, app.ID, account.ID, a, second.ID, account.ID, now); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-issue deletion", err)
	}
	if _, err := s.ActOnIssue(ctx, app.ID, b, account.ID, api.IssueActionRequest{Action: "merge", TargetIssueID: a}, now); err != nil {
		t.Fatal(err)
	}
	group, err := s.ListIssueTrackerLinks(ctx, app.ID, account.ID, b, state.IssueCursor{})
	if err != nil || len(group.Items) != 2 {
		t.Fatal("merged alias lost references", err)
	}
	reused, err := s.PutIssueTrackerLink(ctx, app.ID, account.ID, a, account.ID, api.PutIssueTrackerLinkRequest{URL: second.URL}, now)
	if err != nil || reused.ID != second.ID || reused.OriginalIssueID != b {
		t.Fatal("merged upsert duplicated or reparented reference", err)
	}
	if _, err := s.ActOnIssue(ctx, app.ID, a, account.ID, api.IssueActionRequest{Action: "split", MemberIssueID: b}, now); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, link string }{{a, link.ID}, {b, second.ID}} {
		list, err := s.ListIssueTrackerLinks(ctx, app.ID, account.ID, tc.id, state.IssueCursor{})
		if err != nil || len(list.Items) != 1 || list.Items[0].ID != tc.link {
			t.Fatal("split moved original reference", err)
		}
	}
	if err := s.DeleteIssueTrackerLink(ctx, app.ID, account.ID, a, link.ID, account.ID, now); err != nil {
		t.Fatal(err)
	}
	detail, err := s.GetIssueDetail(ctx, app.ID, a, now.Add(-time.Hour), now.Add(time.Hour), state.IssueDetailCursors{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, activity := range detail.Activity {
		seen[activity.Action]++
	}
	if seen["tracker_linked"] != 1 || seen["tracker_link_updated"] != 1 || seen["tracker_unlinked"] != 1 {
		t.Fatalf("retry duplicated audit or mutations missing: %+v", seen)
	}
	migration, err := migrations.FS.ReadFile("20261014110001001_issue_tracker_links.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, strings.SplitN(string(migration), "-- +goose Down", 2)[1])
	_ = tx.Rollback(ctx)
	if err == nil || !strings.Contains(err.Error(), "remove them explicitly") {
		t.Fatal("rollback discarded tracker metadata", err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "tracker-schema"})
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := s.ProjectEnvironmentCloneSchemaCoverage(ctx, account.ID, project.ID)
	if err != nil || !coverage.Known {
		t.Fatal("tracker schema missing from registry", err)
	}
}
func TestPg_IssueTrackerLinksQuotaAndMergedPagination(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ids := []string{}
	for _, key := range []string{"a", "b", "c"} {
		out, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: key}, Fingerprint: strings.Repeat(key, 64), Title: key, PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, out.IssueID)
	}
	for _, id := range ids {
		for i := 1; i < api.IssueTrackerLinksMax; i++ {
			_, err := s.PutIssueTrackerLink(ctx, app.ID, account.ID, id, account.ID, api.PutIssueTrackerLinkRequest{URL: fmt.Sprintf("https://github.com/acme/repo/issues/%d", i)}, now)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, n := range []int{20, 21} {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := s.PutIssueTrackerLink(ctx, app.ID, account.ID, ids[0], account.ID, api.PutIssueTrackerLinkRequest{URL: fmt.Sprintf("https://github.com/acme/repo/issues/%d", n)}, now)
			results <- err
		}(n)
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
		t.Fatal("quota raced")
	}
	for _, id := range ids[1:] {
		if _, err := s.ActOnIssue(ctx, app.ID, id, account.ID, api.IssueActionRequest{Action: "merge", TargetIssueID: ids[0]}, now); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListIssueTrackerLinks(ctx, app.ID, account.ID, ids[0], state.IssueCursor{})
	if err != nil || len(list.Items) != api.IssuePageSize || list.NextCursor == "" {
		t.Fatalf("merged link pagination: %+v %v", list, err)
	}
	cur, err := state.DecodeIssueCursor(list.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := s.ListIssueTrackerLinks(ctx, app.ID, account.ID, ids[0], cur)
	if err != nil || len(tail.Items) != 8 || tail.NextCursor != "" {
		t.Fatalf("tail lost links: %+v %v", tail, err)
	}
	if _, err := s.PutIssueTrackerLink(ctx, app.ID, account.ID, ids[0], account.ID, api.PutIssueTrackerLinkRequest{URL: "https://github.com/acme/repo/issues/999"}, now); !errors.Is(err, state.ErrIssueQuota) {
		t.Fatal("merged overflow allowed a new reference", err)
	}
	if _, err := s.PutIssueTrackerLink(ctx, app.ID, account.ID, ids[0], account.ID, api.PutIssueTrackerLinkRequest{URL: "https://github.com/acme/repo/issues/1", Title: "Update at quota"}, now); err != nil {
		t.Fatal("existing reference blocked at quota", err)
	}
	if _, err := s.ActOnIssue(ctx, app.ID, ids[0], account.ID, api.IssueActionRequest{Action: "split", MemberIssueID: ids[1]}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListIssueTrackerLinks(ctx, app.ID, account.ID, ids[1], cur); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("cursor escaped canonical group", err)
	}
}
