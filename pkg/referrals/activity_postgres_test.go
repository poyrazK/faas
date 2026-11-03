package referrals_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/referrals"
)

func activityFixture(t *testing.T) (*pgxpool.Pool, *referrals.PostgresStore, string, string, string) {
	t.Helper()
	pool, store, owner, other := pipelineFixture(t)
	raw, err := migrations.FS.ReadFile("20261103221800000_sales_activity.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	if _, err = pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	id, err := store.SaveProspect(t.Context(), referrals.Scope{OwnerID: owner}, owner, "", prospect(owner, "history@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	return pool, store, owner, other, id
}
func activityEntry() referrals.ActivityInput {
	return referrals.ActivityInput{ID: uuid.NewString(), LeadVersion: 1, Kind: "call", Summary: "Discussed deployment needs", Outcome: "Demo agreed", NextStep: "Schedule a walkthrough", OccurredAt: time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond), FollowUpTitle: "Arrange demo", FollowUpOn: "2026-11-06"}
}
func TestSalesActivityAtomicRetryOwnershipAndDeletion(t *testing.T) {
	pool, s, owner, other, lead := activityFixture(t)
	ctx := t.Context()
	mine := referrals.Scope{OwnerID: owner}
	theirs := referrals.Scope{OwnerID: other}
	manager := referrals.Scope{OwnerID: owner, All: true}
	in := activityEntry()
	if _, err := s.RecordActivity(ctx, theirs, lead, in); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal("another rep wrote history", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.RecordActivity(ctx, mine, lead, in); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	p, rows, err := s.LeadActivity(ctx, mine, lead, referrals.ActivityFilter{Limit: 25})
	if err != nil || len(rows) != 1 || rows[0].ActorID != owner || rows[0].Outcome != in.Outcome || rows[0].FollowUpOn != in.FollowUpOn {
		t.Fatal(p, rows, err)
	}
	tasks, _ := taskList(t, s, manager)
	if len(tasks) != 1 || tasks[0].LeadID != lead {
		t.Fatal("retry duplicated follow-up", tasks)
	}
	bad := in
	bad.Summary = "Changed retry"
	if _, err = s.RecordActivity(ctx, mine, lead, bad); !errors.Is(err, referrals.ErrConflict) {
		t.Fatal("different payload accepted under the same ID", err)
	}
	// Current ownership governs both the original history and the linked task.
	p.OwnerID = other
	if _, err = s.SaveProspect(ctx, manager, owner, lead, p.ProspectInput); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.LeadActivity(ctx, mine, lead, referrals.ActivityFilter{Limit: 25}); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal("former owner read history", err)
	}
	_, rows, err = s.LeadActivity(ctx, theirs, lead, referrals.ActivityFilter{Limit: 25})
	if err != nil || rows[0].ActorID != owner {
		t.Fatal("original author changed", rows, err)
	}
	fresh := activityEntry()
	if _, err = s.RecordActivity(ctx, theirs, lead, fresh); !errors.Is(err, referrals.ErrConflict) {
		t.Fatal("stale lead version accepted", err)
	}
	fresh.LeadVersion = 2
	if _, err = s.RecordActivity(ctx, theirs, lead, fresh); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "DELETE FROM sales_tasks WHERE id=$1", rows[0].TaskID); err != nil {
		t.Fatal(err)
	}
	_, rows, err = s.LeadActivity(ctx, theirs, lead, referrals.ActivityFilter{Limit: 25})
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	var old referrals.Activity
	for _, r := range rows {
		if r.ID == in.ID {
			old = r
		}
	}
	if old.TaskID != "" || old.FollowUpTitle != in.FollowUpTitle {
		t.Fatal("task deletion erased history snapshot", old)
	}
	if ok, err := s.DeleteProspect(ctx, theirs, lead, 2); err != nil || !ok {
		t.Fatal(ok, err)
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM sales_activities").Scan(&count); err != nil || count != 0 {
		t.Fatal("lead deletion retained activity", count, err)
	}
}
func TestSalesActivityRollbackPaginationAndNoReferralCredit(t *testing.T) {
	pool, s, owner, _, lead := activityFixture(t)
	ctx := t.Context()
	scope := referrals.Scope{OwnerID: owner}
	in := activityEntry()
	in.Kind = "invalid"
	if _, err := s.RecordActivity(ctx, scope, lead, in); err == nil {
		t.Fatal("invalid event should roll back")
	}
	tasks, _ := taskList(t, s, scope)
	if len(tasks) != 0 {
		t.Fatal("task committed without activity", tasks)
	}
	in.Kind = "call"
	if _, err := s.RecordActivity(ctx, scope, lead, in); err != nil {
		t.Fatal("rollback consumed ID", err)
	}
	ids := map[string]bool{in.ID: true}
	for range 4 {
		next := in
		next.ID = uuid.NewString()
		next.FollowUpTitle = ""
		next.FollowUpOn = ""
		ids[next.ID] = true
		if _, err := s.RecordActivity(ctx, scope, lead, next); err != nil {
			t.Fatal(err)
		}
	}
	f := referrals.ActivityFilter{Limit: 2}
	seen := map[string]bool{}
	for {
		_, rows, err := s.LeadActivity(ctx, scope, lead, f)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			if seen[r.ID] {
				t.Fatal("cursor repeated record", r.ID)
			}
			seen[r.ID] = true
		}
		last := rows[len(rows)-1]
		f.AfterID = last.ID
		f.AfterAt = last.OccurredAt
	}
	if len(seen) != len(ids) {
		t.Fatal("tied timestamps lost history", seen, ids)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM referral_attributions").Scan(&count); err != nil || count != 0 {
		t.Fatal("activity created referral credit", count, err)
	}
}
func TestSalesAttentionUsesContactTimeAndTaskState(t *testing.T) {
	pool, s, owner, other, lead := activityFixture(t)
	ctx := t.Context()
	scope := referrals.Scope{OwnerID: owner}
	manager := referrals.Scope{OwnerID: owner, All: true}
	asOf := time.Now().UTC().Truncate(time.Microsecond)
	on := asOf.Format(time.DateOnly)
	if _, err := pool.Exec(ctx, "UPDATE sales_leads SET created_at=$2 WHERE id=$1", lead, asOf.Add(-20*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	f := referrals.AlertFilter{Kind: "all", On: on, AsOf: asOf, StaleDays: 14, Limit: 25}
	rows, c, err := s.LeadAlerts(ctx, scope, f)
	if err != nil || len(rows) != 1 || !rows[0].Stale || rows[0].HasContact || c.Stale != 1 {
		t.Fatal(rows, c, err)
	}
	note := activityEntry()
	note.Kind = "note"
	note.FollowUpOn = ""
	note.FollowUpTitle = ""
	note.OccurredAt = asOf.Add(-time.Minute)
	if _, err = s.RecordActivity(ctx, scope, lead, note); err != nil {
		t.Fatal(err)
	}
	rows, c, err = s.LeadAlerts(ctx, scope, f)
	if err != nil || c.Stale != 1 {
		t.Fatal("internal note reset contact age", rows, c, err)
	}
	contact := note
	contact.ID = uuid.NewString()
	contact.Kind = "email"
	if _, err = s.RecordActivity(ctx, scope, lead, contact); err != nil {
		t.Fatal(err)
	}
	rows, c, err = s.LeadAlerts(ctx, scope, f)
	if err != nil || len(rows) != 0 || c.Leads != 0 {
		t.Fatal("recorded contact did not clear stale alert", rows, c, err)
	}
	task, err := s.SaveTask(ctx, scope, owner, "", referrals.TaskInput{LeadID: lead, Title: "Follow up", DueOn: asOf.AddDate(0, 0, -1).Format(time.DateOnly)})
	if err != nil {
		t.Fatal(err)
	}
	rows, c, err = s.LeadAlerts(ctx, scope, f)
	if err != nil || len(rows) != 1 || rows[0].Stale || rows[0].OverdueTasks != 1 || c.OverdueTasks != 1 {
		t.Fatal(rows, c, err)
	}
	// Today is not overdue, and completed tasks stop alerting.
	if _, err = s.SaveTask(ctx, scope, owner, task, referrals.TaskInput{Title: "Follow up", DueOn: asOf.AddDate(0, 0, -1).Format(time.DateOnly), Done: true, Version: 1}); err != nil {
		t.Fatal(err)
	}
	rows, _, err = s.LeadAlerts(ctx, scope, f)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	if _, err = s.SaveTask(ctx, scope, owner, "", referrals.TaskInput{LeadID: lead, Title: "Today", DueOn: on}); err != nil {
		t.Fatal(err)
	}
	rows, _, err = s.LeadAlerts(ctx, scope, f)
	if err != nil || len(rows) != 0 {
		t.Fatal("today was treated as overdue", rows, err)
	}
	// A backdated interaction stays stale; closed stages do not count as stale.
	if _, err = pool.Exec(ctx, "UPDATE sales_activities SET occurred_at=$2 WHERE lead_id=$1 AND kind='email'", lead, asOf.Add(-20*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, _, err = s.LeadAlerts(ctx, scope, f)
	if err != nil || len(rows) != 1 || !rows[0].Stale {
		t.Fatal(rows, err)
	}
	p, _, err := s.LeadActivity(ctx, scope, lead, referrals.ActivityFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	p.Stage = "won"
	if _, err = s.SaveProspect(ctx, manager, owner, lead, p.ProspectInput); err != nil {
		t.Fatal(err)
	}
	rows, _, err = s.LeadAlerts(ctx, scope, f)
	if err != nil || len(rows) != 0 {
		t.Fatal("closed deal marked stalled", rows, err)
	}
	p.OwnerID = other
	p.Version = 2
	if _, err = s.SaveProspect(ctx, manager, owner, lead, p.ProspectInput); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveTask(ctx, manager, owner, "", referrals.TaskInput{LeadID: lead, Title: "Onboard won lead", DueOn: asOf.AddDate(0, 0, -1).Format(time.DateOnly)}); err != nil {
		t.Fatal(err)
	}
	rows, _, err = s.LeadAlerts(ctx, scope, f)
	if err != nil || len(rows) != 0 {
		t.Fatal("former owner received alerts", rows, err)
	}
	f.Owner = other
	f.Kind = "overdue"
	rows, c, err = s.LeadAlerts(ctx, manager, f)
	if err != nil || len(rows) != 1 || c.Stale != 0 || c.OverdueLeads != 1 {
		t.Fatal(rows, c, err)
	}
	f.Kind = "stale"
	rows, _, err = s.LeadAlerts(ctx, manager, f)
	if err != nil || len(rows) != 0 {
		t.Fatal("overdue lead leaked into stale filter", rows, err)
	}
}
