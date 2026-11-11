package referrals_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/referrals"
)

func intakeFixture(t *testing.T) (*pgxpool.Pool, *referrals.PostgresStore, string, string, referrals.IntakeOptions) {
	t.Helper()
	pool, store, owner, other := pipelineFixture(t)
	raw, err := migrations.FS.ReadFile("20261111125848993_sales_intake.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	if _, err = pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	o := referrals.IntakeOptions{MemberEmails: []string{"rep@example.com", "other@example.com"}, RepEmails: []string{"rep@example.com", "other@example.com"}, DueOn: "2026-11-05"}
	return pool, store, owner, other, o
}
func inquiry(email string) referrals.InquiryInput {
	return referrals.InquiryInput{Name: "Maya Chen", Email: email, Company: "Northstar", Intent: "demo", Message: "Could we discuss our deployment?"}
}
func submitInquiry(t *testing.T, s *referrals.PostgresStore, in referrals.InquiryInput, o referrals.IntakeOptions) {
	t.Helper()
	if err := s.SubmitInquiry(t.Context(), uuid.NewString(), time.Now().Add(time.Hour), in, o); err != nil {
		t.Fatal(err)
	}
}
func inquiries(t *testing.T, s *referrals.PostgresStore, scope referrals.Scope) []referrals.Inquiry {
	t.Helper()
	rows, err := s.Inquiries(t.Context(), scope, referrals.InquiryFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
func TestSalesIntakeCodeDedupReplayAndFollowup(t *testing.T) {
	pool, s, owner, other, o := intakeFixture(t)
	ctx := t.Context()
	scope := referrals.Scope{OwnerID: owner, All: true}
	link, err := s.EnsureLink(ctx, owner, "sales")
	if err != nil {
		t.Fatal(err)
	}
	in := inquiry("maya@example.com")
	in.Code = link.Code
	nonce := uuid.NewString()
	if err = s.SubmitInquiry(ctx, nonce, time.Now().Add(time.Hour), in, o); err != nil {
		t.Fatal(err)
	}
	// A changed payload cannot use an already-consumed token to create a second lead.
	retry := in
	retry.Email = "other-prospect@example.com"
	if err = s.SubmitInquiry(ctx, nonce, time.Now().Add(time.Hour), retry, o); err != nil {
		t.Fatal(err)
	}
	rows := inquiries(t, s, scope)
	if len(rows) != 1 || rows[0].OwnerID != owner || rows[0].Status != "assigned" {
		t.Fatal(rows)
	}
	if len(inquiries(t, s, referrals.Scope{OwnerID: other})) != 0 {
		t.Fatal("another rep read inquiry")
	}
	tasks, _ := taskList(t, s, scope)
	if len(tasks) != 1 || tasks[0].DueOn != o.DueOn {
		t.Fatal(tasks)
	}
	lead := leadList(t, s, scope)[0]
	original := lead.ProspectInput
	original.Note = "Staff verified context"
	original.Stage = "qualified"
	if _, err = s.SaveProspect(ctx, scope, owner, lead.ID, original); err != nil {
		t.Fatal(err)
	}
	repeated := in
	repeated.Code = "ZZZZZZ"
	repeated.Name = "Unverified name"
	repeated.Message = "Second message"
	submitInquiry(t, s, repeated, o)
	rows = inquiries(t, s, scope)
	if rows[0].RepeatCount != 1 || rows[0].Message != in.Message || rows[0].LatestMessage != "Second message" || rows[0].OwnerID != owner {
		t.Fatal(rows)
	}
	tasks, _ = taskList(t, s, scope)
	if len(tasks) != 1 {
		t.Fatal("duplicate follow-up", tasks)
	}
	got := leadList(t, s, scope)[0]
	if got.Note != original.Note || got.Stage != "qualified" || got.Name != in.Name {
		t.Fatal("staff data overwritten", got)
	}
	done := tasks[0].TaskInput
	done.Done = true
	if _, err = s.SaveTask(ctx, scope, owner, tasks[0].ID, done); err != nil {
		t.Fatal(err)
	}
	submitInquiry(t, s, repeated, o)
	tasks, _ = taskList(t, s, scope)
	if len(tasks) != 2 {
		t.Fatal("repeat did not rearm completed follow-up", tasks)
	}
	var credit int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM referral_attributions").Scan(&credit); err != nil || credit != 0 {
		t.Fatal("inquiry granted referral credit", credit, err)
	}
}
func TestSalesIntakeReviewAssignmentAndVersions(t *testing.T) {
	_, s, owner, other, o := intakeFixture(t)
	ctx := t.Context()
	scope := referrals.Scope{OwnerID: owner, All: true}
	in := inquiry("queue@example.com")
	in.Code = "POJU43"
	submitInquiry(t, s, in, o)
	rows := inquiries(t, s, scope)
	r := rows[0]
	if r.Status != "pending" || r.Reason != "code_unavailable" || r.LeadID != "" {
		t.Fatal(r)
	}
	if len(inquiries(t, s, referrals.Scope{OwnerID: other})) != 0 {
		t.Fatal("rep saw manager queue")
	}
	if err := s.AssignInquiry(ctx, r.ID, r.Version, uuid.NewString(), o); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.AssignInquiry(ctx, r.ID, r.Version, other, o); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignInquiry(ctx, r.ID, r.Version, owner, o); !errors.Is(err, referrals.ErrConflict) {
		t.Fatal("stale assignment", err)
	}
	r = inquiries(t, s, scope)[0]
	if r.OwnerID != other || r.Status != "assigned" {
		t.Fatal(r)
	}
	if closed, err := s.CloseInquiry(ctx, referrals.Scope{OwnerID: owner}, r.ID, r.Version); err != nil || closed {
		t.Fatal("rep closed another rep inquiry", closed, err)
	}
	if closed, err := s.CloseInquiry(ctx, referrals.Scope{OwnerID: other}, r.ID, r.Version); err != nil || !closed {
		t.Fatal(closed, err)
	}
	if len(leadList(t, s, scope)) != 1 {
		t.Fatal("closing deleted lead")
	}
	submitInquiry(t, s, in, o)
	if len(inquiries(t, s, scope)) != 2 {
		t.Fatal("closed inquiry blocked fresh inquiry")
	}
}
func TestSalesIntakePreservesExistingOwnerAndQueuesConflicts(t *testing.T) {
	pool, s, owner, other, o := intakeFixture(t)
	ctx := t.Context()
	scope := referrals.Scope{OwnerID: owner, All: true}
	id, err := s.SaveProspect(ctx, scope, owner, "", prospect(owner, "existing@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	submitInquiry(t, s, inquiry("existing@example.com"), o)
	r := inquiries(t, s, scope)[0]
	if r.OwnerID != owner || r.LeadID != id {
		t.Fatal(r)
	}
	link, err := s.EnsureLink(ctx, other, "sales")
	if err != nil {
		t.Fatal(err)
	}
	if closed, err := s.CloseInquiry(ctx, scope, r.ID, r.Version); err != nil || !closed {
		t.Fatal(err)
	}
	in := inquiry("existing@example.com")
	in.Code = link.Code
	submitInquiry(t, s, in, o)
	r = inquiries(t, s, scope)[0]
	if r.Reason != "owner_conflict" || r.Status != "pending" {
		t.Fatal(r)
	}
	// A removed lead is presented as manager review and can be recovered.
	if _, err = pool.Exec(ctx, "DELETE FROM sales_leads WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err = s.AssignInquiry(ctx, r.ID, r.Version, other, o); err != nil {
		t.Fatal(err)
	}
	r = inquiries(t, s, scope)[0]
	if _, err = pool.Exec(ctx, "DELETE FROM sales_leads WHERE id=$1", r.LeadID); err != nil {
		t.Fatal(err)
	}
	r = inquiries(t, s, scope)[0]
	if r.Status != "pending" || r.Reason != "lead_unavailable" {
		t.Fatal(r)
	}
	if err = s.AssignInquiry(ctx, r.ID, r.Version, owner, o); err != nil {
		t.Fatal(err)
	}
}
func TestSalesIntakeConcurrentDedupAndRoundRobin(t *testing.T) {
	_, s, owner, _, o := intakeFixture(t)
	ctx := t.Context()
	scope := referrals.Scope{OwnerID: owner, All: true}
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			email := "shared@example.com"
			if i >= 6 {
				email = fmt.Sprintf("prospect-%d@example.com", i)
			}
			failures <- s.SubmitInquiry(ctx, uuid.NewString(), time.Now().Add(time.Hour), inquiry(email), o)
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows := inquiries(t, s, scope)
	if len(rows) != 7 {
		t.Fatal(len(rows))
	}
	owners := map[string]int{}
	for _, r := range rows {
		owners[r.OwnerID]++
		if r.Email == "shared@example.com" && r.RepeatCount != 5 {
			t.Fatal(r)
		}
	}
	if len(owners) != 2 {
		t.Fatal(owners)
	}
	for _, n := range owners {
		if n < 3 || n > 4 {
			t.Fatal("unbalanced routing", owners)
		}
	}
	tasks, _ := taskList(t, s, scope)
	if len(tasks) != 7 {
		t.Fatal("concurrent requests duplicated tasks", len(tasks))
	}
	o.RepEmails = []string{}
	submitInquiry(t, s, inquiry("no-reps@example.com"), o)
	r := inquiries(t, s, scope)[0]
	if r.Reason != "no_reps" || r.Status != "pending" {
		t.Fatal(r)
	}
}

func TestSalesIntakeRollbackUnavailableOwnersAndReviewPrivacy(t *testing.T) {
	pool, s, owner, other, o := intakeFixture(t)
	ctx := t.Context()
	scope := referrals.Scope{OwnerID: owner, All: true}
	nonce := uuid.NewString()
	in := inquiry("rollback@example.com")
	bad := o
	bad.DueOn = "invalid"
	if err := s.SubmitInquiry(ctx, nonce, time.Now().Add(time.Hour), in, bad); err == nil {
		t.Fatal("invalid task should roll back intake")
	}
	if len(inquiries(t, s, scope)) != 0 || len(leadList(t, s, scope)) != 0 {
		t.Fatal("partial inquiry transaction committed")
	}
	// A schema-only database also initializes the routing lock atomically.
	if _, err := pool.Exec(ctx, "DELETE FROM sales_intake_routing"); err != nil {
		t.Fatal(err)
	}
	if err := s.SubmitInquiry(ctx, nonce, time.Now().Add(time.Hour), in, o); err != nil {
		t.Fatal("rollback consumed receipt or failed lock initialization", err)
	}
	r := inquiries(t, s, scope)[0]
	currentOwner := r.OwnerID
	if _, err := pool.Exec(ctx, "UPDATE sales_leads SET email='staff-corrected@example.com' WHERE id=$1", r.LeadID); err != nil {
		t.Fatal(err)
	}
	submitInquiry(t, s, in, o)
	r = inquiries(t, s, scope)[0]
	if r.Status != "pending" || r.Reason != "lead_unavailable" {
		t.Fatal(r)
	}
	if len(inquiries(t, s, referrals.Scope{OwnerID: currentOwner})) != 0 {
		t.Fatal("rep can read linked manager review")
	}
	if closed, err := s.CloseInquiry(ctx, referrals.Scope{OwnerID: currentOwner}, r.ID, r.Version); err != nil || closed {
		t.Fatal("rep can close manager review", closed, err)
	}
	// Paused sales codes and customer-program codes also go to review.
	salesLink, err := s.EnsureLink(ctx, owner, "sales")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetLinkActive(ctx, scope, salesLink.Code, false); err != nil {
		t.Fatal(err)
	}
	customerLink, err := s.EnsureLink(ctx, other, "customer")
	if err != nil {
		t.Fatal(err)
	}
	for i, code := range []string{salesLink.Code, customerLink.Code} {
		in := inquiry(fmt.Sprintf("review-%d@example.com", i))
		in.Code = code
		submitInquiry(t, s, in, o)
		r := inquiries(t, s, scope)[0]
		if r.Reason != "code_unavailable" {
			t.Fatal(r)
		}
	}
	if _, err = pool.Exec(ctx, "UPDATE accounts SET mfa_enrolled_at=NULL WHERE id=$1", other); err != nil {
		t.Fatal(err)
	}
	if err = s.AssignInquiry(ctx, r.ID, r.Version, other, o); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal("unenrolled rep accepted assignment", err)
	}
}
