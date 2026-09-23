package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStorePRPreviewSetBatchQuotaNeutralSwap(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "set-swap@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	project, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "set-swap"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"api", "worker", "db"} {
		if _, err := m.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID,
			Slug: name, WorkloadName: name, Status: AppActive}); err != nil {
			t.Fatal(err)
		}
	}
	expiry := time.Now().Add(time.Hour)
	preview := func(name string) App {
		return App{AccountID: account.ID, ProjectID: project.ID, Slug: "pr-42-" + name,
			WorkloadName: name, PreviewOfSlug: name, PreviewPrNumber: 42,
			PreviewPrState: PreviewPrStateOpen, PreviewExpiresAt: &expiry, Status: AppActive}
	}
	limits := api.Limits{DeployedApps: 5}
	head := PRPreviewHead{InstallationID: 7, RepoFullName: "octo/api", PRNumber: 42, CommitSHA: strings.Repeat("a", 40)}
	first, err := m.ReservePRPreviewSet(ctx, head, []App{preview("api"), preview("worker")}, limits)
	if err != nil || len(first) != 2 {
		t.Fatalf("first reservation = (%+v, %v)", first, err)
	}
	count, err := m.CountDeployedApps(ctx, account.ID)
	if err != nil || count != 5 {
		t.Fatalf("full quota = (%d, %v), want 5", count, err)
	}
	head.CommitSHA = strings.Repeat("b", 40)
	if _, err := m.ReservePRPreviewSet(ctx, head,
		[]App{preview("api"), preview("worker"), preview("db")}, limits); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("net growth error = %v, want quota", err)
	}
	set, err := m.GetPRPreviewSet(ctx, 7, "octo/api", 42)
	if err != nil || set.CommitSHA != strings.Repeat("a", 40) || len(set.MemberAppIDs) != 2 {
		t.Fatalf("failed growth changed set = (%+v, %v)", set, err)
	}
	if _, err := m.AppBySlug(ctx, "pr-42-db"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed growth left db preview: %v", err)
	}
	otherAccount, err := m.CreateAccount(ctx, "set-swap-conflict@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := m.CreateApp(ctx, App{AccountID: otherAccount.ID, Slug: "pr-42-db", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReservePRPreviewSet(ctx, head, []App{preview("api"), preview("db")}, limits); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting swap = %v, want conflict", err)
	}
	if old, err := m.AppByID(ctx, first[1].ID); err != nil || old.Status != AppActive {
		t.Fatalf("conflicting swap retired worker = (%+v, %v)", old, err)
	}
	if set, err := m.GetPRPreviewSet(ctx, 7, "octo/api", 42); err != nil || set.CommitSHA != strings.Repeat("a", 40) {
		t.Fatalf("conflicting swap changed set = (%+v, %v)", set, err)
	}
	if _, err := m.SoftDeleteAppCascade(ctx, conflict.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReservePRPreviewSet(ctx, head, []App{preview("api"), preview("db")}, limits); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted foreign slug error = %v, want conflict", err)
	}
	delete(m.apps, conflict.ID) // remove only this test fixture's slug reservation
	second, err := m.ReservePRPreviewSet(ctx, head, []App{preview("api"), preview("db")}, limits)
	if err != nil || len(second) != 2 || second[0].ID != first[0].ID {
		t.Fatalf("quota-neutral swap = (%+v, %v)", second, err)
	}
	oldWorker, err := m.AppByID(ctx, first[1].ID)
	if err != nil || oldWorker.Status != AppDeleted || oldWorker.PreviewPrState != PreviewPrStateStale ||
		oldWorker.Slug == preview("worker").Slug {
		t.Fatalf("retired worker = (%+v, %v)", oldWorker, err)
	}
	count, err = m.CountDeployedApps(ctx, account.ID)
	if err != nil || count != 5 {
		t.Fatalf("post-swap quota = (%d, %v), want 5", count, err)
	}
	set, err = m.GetPRPreviewSet(ctx, 7, "octo/api", 42)
	if err != nil || set.CommitSHA != head.CommitSHA || len(set.MemberAppIDs) != 2 || set.MemberAppIDs[1] != second[1].ID {
		t.Fatalf("replacement set = (%+v, %v)", set, err)
	}
	if _, err := m.ReservePRPreviewSet(ctx, head, []App{preview("api"), preview("db")}, limits); err != nil {
		t.Fatalf("idempotent retry at quota: %v", err)
	}
	head.CommitSHA = strings.Repeat("c", 40)
	third, err := m.ReservePRPreviewSet(ctx, head, []App{preview("api"), preview("worker")}, limits)
	if err != nil || len(third) != 2 || third[1].ID == first[1].ID {
		t.Fatalf("re-add retired dependency = (%+v, %v)", third, err)
	}
	head.CommitSHA = strings.Repeat("d", 40)
	fourth, err := m.ReservePRPreviewSet(ctx, head, []App{preview("api"), preview("db")}, limits)
	if err != nil || len(fourth) != 2 || fourth[1].ID == second[1].ID {
		t.Fatalf("re-add db dependency = (%+v, %v)", fourth, err)
	}
	if err := m.PutPRPreviewSet(ctx, PRPreviewSet{InstallationID: 7, RepoFullName: "octo/db",
		PRNumber: 42, CommitSHA: head.CommitSHA, RootAppID: fourth[1].ID,
		MemberAppIDs: []string{fourth[1].ID}}); err != nil {
		t.Fatal(err)
	}
	head.CommitSHA = strings.Repeat("e", 40)
	if _, err := m.ReservePRPreviewSet(ctx, head, []App{preview("api"), preview("worker")}, limits); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("swap of shared db = %v, want quota error", err)
	}
	if db, err := m.AppByID(ctx, fourth[1].ID); err != nil || db.Status != AppActive {
		t.Fatalf("shared db was retired = (%+v, %v)", db, err)
	}
}
