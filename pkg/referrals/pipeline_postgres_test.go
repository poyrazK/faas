package referrals_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/referrals"
)

func pipelineFixture(t *testing.T) (*pgxpool.Pool, *referrals.PostgresStore, string, string) {
	t.Helper()
	pool, store, owner, other := fixture(t)
	if _, err := pool.Exec(t.Context(), "ALTER TABLE accounts ADD COLUMN mfa_enrolled_at timestamptz; UPDATE accounts SET mfa_enrolled_at=now()"); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261111125847978_sales_pipeline.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	if _, err = pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	raw, err = migrations.FS.ReadFile("20261111125851200_sales_qualification.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ = strings.Cut(string(raw), "-- +goose Down")
	if _, err = pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	return pool, store, owner, other
}
func prospect(owner, email string) referrals.ProspectInput {
	return referrals.ProspectInput{Name: "Maya Chen", Email: email, Company: "Orbit", Stage: "qualified", Note: "Review deployment needs", OwnerID: owner}
}
func leadList(t *testing.T, store *referrals.PostgresStore, scope referrals.Scope) []referrals.Prospect {
	t.Helper()
	rows, _, err := store.Prospects(t.Context(), scope, referrals.PipelineFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
func taskList(t *testing.T, store *referrals.PostgresStore, scope referrals.Scope) ([]referrals.Task, referrals.TaskCounts) {
	t.Helper()
	rows, counts, err := store.Tasks(t.Context(), scope, referrals.TaskFilter{Status: "all", Due: "all", On: "2026-11-03", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return rows, counts
}

func TestSalesPipelineOwnershipTasksAndVersions(t *testing.T) {
	_, store, owner, other := pipelineFixture(t)
	ctx := t.Context()
	mine := referrals.Scope{OwnerID: owner}
	theirs := referrals.Scope{OwnerID: other}
	manager := referrals.Scope{OwnerID: owner, All: true}
	input := prospect(owner, "prospect@example.com")
	id, err := store.SaveProspect(ctx, mine, owner, "", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveProspect(ctx, mine, owner, "", input); !errors.Is(err, referrals.ErrDuplicateLead) {
		t.Fatal("duplicate contact accepted", err)
	}
	if len(leadList(t, store, theirs)) != 0 {
		t.Fatal("another rep can read the lead")
	}
	input.Version = 1
	input.OwnerID = other
	if _, err = store.SaveProspect(ctx, theirs, other, id, input); !errors.Is(err, referrals.ErrConflict) {
		t.Fatal("foreign lead was writable", err)
	}
	if _, err = store.SaveProspect(ctx, mine, owner, id, input); !errors.Is(err, referrals.ErrConflict) {
		t.Fatal("rep reassigned their own lead", err)
	}
	task := referrals.TaskInput{LeadID: id, Title: "Schedule demo", DueOn: "2026-11-02"}
	if _, err = store.SaveTask(ctx, theirs, other, "", task); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal("task added to foreign lead", err)
	}
	taskID, err := store.SaveTask(ctx, mine, owner, "", task)
	if err != nil {
		t.Fatal(err)
	}
	rows, counts := taskList(t, store, mine)
	if len(rows) != 1 || counts.Open != 1 || counts.Overdue != 1 {
		t.Fatal(rows, counts)
	}
	edit := referrals.TaskInput{Title: task.Title, DueOn: "2026-11-03", Version: 1, Done: true}
	if _, err = store.SaveTask(ctx, mine, owner, taskID, edit); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveTask(ctx, mine, owner, taskID, edit); !errors.Is(err, referrals.ErrConflict) {
		t.Fatal("stale task overwrote newer state", err)
	}
	_, counts = taskList(t, store, mine)
	if counts.Open != 0 {
		t.Fatal(counts)
	}
	edit.Version = 2
	edit.Done = false
	if _, err = store.SaveTask(ctx, mine, owner, taskID, edit); err != nil {
		t.Fatal(err)
	}
	_, counts = taskList(t, store, mine)
	if counts.Today != 1 || counts.Overdue != 0 {
		t.Fatal(counts)
	}
	if _, err = store.SaveProspect(ctx, manager, owner, id, input); err != nil {
		t.Fatal("manager reassignment failed", err)
	}
	if len(leadList(t, store, mine)) != 0 || len(leadList(t, store, theirs)) != 1 {
		t.Fatal("lead ownership did not move")
	}
	if rows, _ := taskList(t, store, mine); len(rows) != 0 {
		t.Fatal("old rep retained task access")
	}
	if rows, _ := taskList(t, store, theirs); len(rows) != 1 || rows[0].OwnerID != other {
		t.Fatal("task did not follow lead owner", rows)
	}
	edit.Version = 3
	if _, err = store.SaveTask(ctx, mine, owner, taskID, edit); !errors.Is(err, referrals.ErrConflict) {
		t.Fatal("old rep edited reassigned task", err)
	}
	if changed, err := store.DeleteProspect(ctx, theirs, id, 1); err != nil || changed {
		t.Fatal("stale lead was deleted", changed, err)
	}
	if changed, err := store.DeleteProspect(ctx, theirs, id, 2); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if rows, _ := taskList(t, store, manager); len(rows) != 0 {
		t.Fatal("lead deletion left its tasks")
	}
}

func TestSalesPipelineMatchingDoesNotRewriteReferralCredit(t *testing.T) {
	pool, store, owner, other := pipelineFixture(t)
	ctx := t.Context()
	scope := referrals.Scope{OwnerID: owner}
	for _, email := range []string{"future@example.com", "customer-program@example.com", "other-rep@example.com"} {
		if _, err := store.SaveProspect(ctx, scope, owner, "", prospect(owner, email)); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range leadList(t, store, scope) {
		if row.Registered {
			t.Fatal("unregistered lead matched", row)
		}
	}
	signup := func(email, sourceOwner, kind string) string {
		t.Helper()
		link, err := store.EnsureLink(ctx, sourceOwner, kind)
		if err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, "INSERT INTO accounts(id,email) VALUES ($1,$2)", id, email); err != nil {
			t.Fatal(err)
		}
		if err = referrals.Attribute(ctx, tx, id, referrals.Attribution{Code: link.Code, Policy: referrals.Policy{Currency: "usd", CommissionBPS: 1000, HoldDays: 0, ConversionDays: 90}}); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return id
	}
	account := signup("future@example.com", owner, "sales")
	signup("customer-program@example.com", owner, "customer")
	signup("other-rep@example.com", other, "sales")
	var matched referrals.Prospect
	for _, row := range leadList(t, store, scope) {
		if row.Email == "future@example.com" {
			matched = row
			if !row.Registered || row.Paid {
				t.Fatal(row)
			}
		} else if row.Registered {
			t.Fatal("a different referral owner or program leaked a signup", row)
		}
	}
	invoice := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "INSERT INTO invoices(id,account_id,status,currency,amount_paid_cents,subtotal_cents,tax_cents) VALUES ($1,$2,'paid','usd',5000,5000,0)", invoice, account); err != nil {
		t.Fatal(err)
	}
	if err = referrals.RecordPayment(ctx, tx, invoice); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, row := range leadList(t, store, scope) {
		if row.ID == matched.ID && !row.Paid {
			t.Fatal("paid conversion not matched", row)
		}
	}
	matched.OwnerID = other
	matched.Stage = "won"
	matched.CloseReason = "product_fit"
	if _, err = store.SaveProspect(ctx, referrals.Scope{OwnerID: owner, All: true}, owner, matched.ID, matched.ProspectInput); err != nil {
		t.Fatal(err)
	}
	referralsRows, err := store.List(ctx, referrals.Scope{OwnerID: owner, Kind: "sales"}, "", 100)
	if err != nil || len(referralsRows) != 1 || referralsRows[0].AccountID != account || referralsRows[0].RewardCents != 500 {
		t.Fatal("CRM assignment rewrote financial attribution", referralsRows, err)
	}
	if deleted, err := store.DeleteProspect(ctx, referrals.Scope{OwnerID: other}, matched.ID, 2); err != nil || !deleted {
		t.Fatal(deleted, err)
	}
	referralsRows, err = store.List(ctx, referrals.Scope{OwnerID: owner, Kind: "sales"}, "", 100)
	if err != nil || len(referralsRows) != 1 {
		t.Fatal("CRM deletion removed acquisition records", referralsRows, err)
	}
}

func TestSalesPipelineSearchTeamAndPagination(t *testing.T) {
	_, store, owner, other := pipelineFixture(t)
	ctx := t.Context()
	scope := referrals.Scope{OwnerID: owner}
	team, err := store.SalesTeam(ctx, []string{"rep@example.com"})
	if err != nil || len(team) != 1 || team[0].ID != owner {
		t.Fatal(team, err)
	}
	for i := range 30 {
		in := prospect(owner, fmt.Sprintf("lead%02d@example.com", i))
		id, err := store.SaveProspect(ctx, scope, owner, "", in)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.SaveTask(ctx, scope, owner, "", referrals.TaskInput{LeadID: id, Title: "Follow up", DueOn: fmt.Sprintf("2026-11-%02d", i%3+2)})
		if err != nil {
			t.Fatal(err)
		}
	}
	f := referrals.PipelineFilter{Limit: 25}
	first, counts, err := store.Prospects(ctx, scope, f)
	if err != nil || len(first) != 25 || len(counts) != 1 || counts[0].Count != 30 {
		t.Fatal(len(first), counts, err)
	}
	f.After = first[24].ID
	next, _, err := store.Prospects(ctx, scope, f)
	if err != nil || len(next) != 5 || next[0].ID >= first[24].ID {
		t.Fatal(next, err)
	}
	f = referrals.PipelineFilter{Query: "ORBIT", Limit: 25, Owner: other}
	rows, _, err := store.Prospects(ctx, scope, f)
	if err != nil || len(rows) != 0 {
		t.Fatal("owner filter escaped scope", rows, err)
	}
	tf := referrals.TaskFilter{Status: "open", Due: "all", On: "2026-11-03", Limit: 25}
	tasks, summary, err := store.Tasks(ctx, scope, tf)
	if err != nil || len(tasks) != 25 || summary.Open != 30 || summary.Today != 10 || summary.Overdue != 10 {
		t.Fatal(len(tasks), summary, err)
	}
	tf.AfterID = tasks[24].ID
	tf.AfterDate = tasks[24].DueOn
	last, _, err := store.Tasks(ctx, scope, tf)
	if err != nil || len(last) != 5 {
		t.Fatal(last, err)
	}
	tf.AfterID = ""
	tf.AfterDate = ""
	tf.Due = "today"
	tasks, _, err = store.Tasks(ctx, scope, tf)
	if err != nil || len(tasks) != 10 {
		t.Fatal(tasks, err)
	}
}
