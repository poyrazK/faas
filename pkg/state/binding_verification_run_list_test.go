package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestVerificationRunListPaginationIsolationAndExpiry(t *testing.T) {
	m, app, _, in := verificationReceiptFixture(t)
	m.bindingVerificationRuns = map[string]verificationRunRecord{}
	created := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	// Seed retained receipts directly to exercise tied timestamps and old states,
	// independently of admission caps and create-time pruning.
	for i := 1; i <= 5; i++ {
		r := cloneVerificationRecord(verificationRunRecord{Report: in.Report})
		r.Report.ID = fmt.Sprintf("11111111-1111-4111-8111-%012d", i)
		r.Report.CreatedAt = created
		r.Report.ExpiresAt = created.Add(time.Minute)
		r.Report.Status = "passed"
		m.bindingVerificationRuns[r.Report.ID] = r
	}
	child := cloneVerificationRecord(verificationRunRecord{Report: in.Report})
	child.Report.ID = uuid.NewString()
	child.Report.GraphRunID = uuid.NewString()
	child.Report.Status = "pending"
	m.bindingVerificationRuns[child.Report.ID] = child
	first, e := m.ListBindingVerificationRuns(t.Context(), app.AccountID, app.ID, api.BindingVerificationRunListOptions{Limit: 2, Status: "expired"})
	if e != nil || len(first.Runs) != 2 || first.NextCursor == "" || first.Runs[0].ID != "11111111-1111-4111-8111-000000000005" {
		t.Fatalf("first: %+v %v", first, e)
	}
	newer := cloneVerificationRecord(verificationRunRecord{Report: in.Report})
	newer.Report.ID = uuid.NewString()
	newer.Report.CreatedAt = time.Now()
	m.bindingVerificationRuns[newer.Report.ID] = newer
	all := append([]api.BindingVerificationRunSummary{}, first.Runs...)
	cursor := first.NextCursor
	for cursor != "" {
		page, err := m.ListBindingVerificationRuns(t.Context(), app.AccountID, app.ID, api.BindingVerificationRunListOptions{Limit: 2, Status: "expired", Cursor: cursor})
		if err != nil || !page.ObservedAt.Equal(first.ObservedAt) {
			t.Fatal(err)
		}
		all = append(all, page.Runs...)
		cursor = page.NextCursor
	}
	if len(all) != 5 {
		t.Fatalf("pagination: %+v", all)
	}
	seen := map[string]bool{}
	for _, r := range all {
		if seen[r.ID] || r.Status != "expired" {
			t.Fatalf("duplicate/state: %+v", r)
		}
		seen[r.ID] = true
	}
	foreign, e := m.ListBindingVerificationRuns(t.Context(), uuid.NewString(), app.ID, api.BindingVerificationRunListOptions{})
	if e != nil || len(foreign.Runs) != 0 {
		t.Fatal("tenant leak", e)
	}
	for _, o := range []api.BindingVerificationRunListOptions{{Cursor: first.NextCursor}, {Status: "expired", Cursor: first.NextCursor, Scope: "other"}, {Limit: 101}, {Cursor: "bad"}, {DeploymentID: "bad"}} {
		if _, err := m.ListBindingVerificationRuns(t.Context(), app.AccountID, app.ID, o); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid options accepted: %+v %v", o, err)
		}
	}
	page, e := m.ListBindingVerificationRuns(t.Context(), app.AccountID, app.ID, api.BindingVerificationRunListOptions{IncludeMembers: true, Scope: child.Report.Scope, DeploymentID: child.Report.DeploymentID})
	if e != nil || len(page.Runs) != 7 {
		t.Fatalf("member/filter: %+v %v", page, e)
	}
	body, _ := json.Marshal(page)
	var values map[string]any
	_ = json.Unmarshal(body, &values)
	for _, entry := range values["runs"].([]any) {
		r := entry.(map[string]any)
		if r["inventory"] != nil || r["policy"] != nil || r["steps"] != nil || r["review_digest"] != nil {
			t.Fatalf("private snapshot exposed: %s", body)
		}
	}
}
func TestGraphVerificationRunListRootsAndFilters(t *testing.T) {
	m, p, in := graphReceiptFixture(t)
	beforeTasks, beforeInv := len(m.appTasks), len(m.invocations)
	root, e := m.CreateProjectBindingVerificationRun(t.Context(), p.AccountID, p.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	deployment := ""
	for _, id := range root.Manifest.Deployments {
		deployment = id
		break
	}
	page, e := m.ListProjectBindingVerificationRuns(t.Context(), p.AccountID, p.ID, root.Environment, api.BindingVerificationRunListOptions{DeploymentID: deployment})
	if e != nil || len(page.Runs) != 1 || page.Runs[0].ID != root.ID || page.Runs[0].App != "" {
		t.Fatalf("graph: %+v %v", page, e)
	}
	missing, e := m.ListProjectBindingVerificationRuns(t.Context(), p.AccountID, p.ID, "missing", api.BindingVerificationRunListOptions{})
	if e != nil || len(missing.Runs) != 0 {
		t.Fatal("environment leaked")
	}
	for name, member := range root.Members {
		app := member.Policy.AppID
		page, e = m.ListBindingVerificationRuns(t.Context(), p.AccountID, app, api.BindingVerificationRunListOptions{})
		if e != nil || len(page.Runs) != 0 {
			t.Fatal("member listed as root", name, e)
		}
		page, e = m.ListBindingVerificationRuns(t.Context(), p.AccountID, app, api.BindingVerificationRunListOptions{IncludeMembers: true})
		if e != nil || len(page.Runs) != 1 || page.Runs[0].GraphRunID != root.ID {
			t.Fatal("missing member linkage", name, e)
		}
	}
	if beforeTasks != len(m.appTasks) || beforeInv != len(m.invocations) {
		t.Fatal("listing created work")
	}
}
