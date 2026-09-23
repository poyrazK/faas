//go:build !no_pg

package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgPRPreviewSetBatchQuotaNeutralSwap(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "pg-set-swap@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "pg-set-swap"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pg-swap-api", "pg-swap-worker", "pg-swap-db"} {
		if _, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID,
			Slug: name, WorkloadName: name, RAMMB: 128, Status: state.AppActive}); err != nil {
			t.Fatal(err)
		}
	}
	expiry := time.Now().Add(time.Hour)
	preview := func(name string) state.App {
		return state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "pr-42-pg-swap-" + name,
			WorkloadName: "pg-swap-" + name, PreviewOfSlug: "pg-swap-" + name, PreviewPrNumber: 42,
			PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry, RAMMB: 128, Status: state.AppActive}
	}
	limits := api.Limits{DeployedApps: 5}
	head := state.PRPreviewHead{InstallationID: 7, RepoFullName: "octo/api", PRNumber: 42, CommitSHA: strings.Repeat("a", 40)}
	first, err := store.ReservePRPreviewSet(ctx, head, []state.App{preview("api"), preview("worker")}, limits)
	if err != nil || len(first) != 2 {
		t.Fatalf("first reservation = (%+v, %v)", first, err)
	}
	head.CommitSHA = strings.Repeat("b", 40)
	if _, err := store.ReservePRPreviewSet(ctx, head,
		[]state.App{preview("api"), preview("worker"), preview("db")}, limits); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("net growth error = %v, want quota", err)
	}
	set, err := store.GetPRPreviewSet(ctx, 7, "octo/api", 42)
	if err != nil || set.CommitSHA != strings.Repeat("a", 40) || len(set.MemberAppIDs) != 2 {
		t.Fatalf("failed growth changed set = (%+v, %v)", set, err)
	}
	if _, err := store.AppBySlug(ctx, "pr-42-pg-swap-db"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed growth left db preview: %v", err)
	}
	otherAccount, err := store.CreateAccount(ctx, "pg-set-swap-conflict@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := store.CreateApp(ctx, state.App{AccountID: otherAccount.ID,
		Slug: "pr-42-pg-swap-db", RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReservePRPreviewSet(ctx, head, []state.App{preview("api"), preview("db")}, limits); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("conflicting swap = %v, want conflict", err)
	}
	if old, err := store.AppByID(ctx, first[1].ID); err != nil || old.Status != state.AppActive {
		t.Fatalf("conflicting swap retired worker = (%+v, %v)", old, err)
	}
	if set, err := store.GetPRPreviewSet(ctx, 7, "octo/api", 42); err != nil || set.CommitSHA != strings.Repeat("a", 40) {
		t.Fatalf("conflicting swap changed set = (%+v, %v)", set, err)
	}
	if _, err := store.SoftDeleteAppCascade(ctx, conflict.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReservePRPreviewSet(ctx, head, []state.App{preview("api"), preview("db")}, limits); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("deleted foreign slug error = %v, want conflict", err)
	}
	// A normal soft delete retains the global slug; remove only this test
	// fixture so the following swap starts with a truly unclaimed slug.
	if _, err := pool.Exec(ctx, `delete from apps where id = $1`, conflict.ID); err != nil {
		t.Fatal(err)
	}
	second, err := store.ReservePRPreviewSet(ctx, head, []state.App{preview("api"), preview("db")}, limits)
	if err != nil || len(second) != 2 || second[0].ID != first[0].ID {
		t.Fatalf("quota-neutral swap = (%+v, %v)", second, err)
	}
	oldWorker, err := store.AppByID(ctx, first[1].ID)
	if err != nil || oldWorker.Status != state.AppDeleted || oldWorker.PreviewPrState != state.PreviewPrStateStale ||
		oldWorker.Slug == preview("worker").Slug {
		t.Fatalf("retired worker = (%+v, %v)", oldWorker, err)
	}
	count, err := store.CountDeployedApps(ctx, account.ID)
	if err != nil || count != 5 {
		t.Fatalf("post-swap quota = (%d, %v), want 5", count, err)
	}
	set, err = store.GetPRPreviewSet(ctx, 7, "octo/api", 42)
	if err != nil || set.CommitSHA != head.CommitSHA || len(set.MemberAppIDs) != 2 || set.MemberAppIDs[1] != second[1].ID {
		t.Fatalf("replacement set = (%+v, %v)", set, err)
	}
	head.CommitSHA = strings.Repeat("c", 40)
	third, err := store.ReservePRPreviewSet(ctx, head, []state.App{preview("api"), preview("worker")}, limits)
	if err != nil || len(third) != 2 || third[1].ID == first[1].ID {
		t.Fatalf("re-add retired dependency = (%+v, %v)", third, err)
	}
	head.CommitSHA = strings.Repeat("d", 40)
	fourth, err := store.ReservePRPreviewSet(ctx, head, []state.App{preview("api"), preview("db")}, limits)
	if err != nil || len(fourth) != 2 || fourth[1].ID == second[1].ID {
		t.Fatalf("re-add db dependency = (%+v, %v)", fourth, err)
	}
	if err := store.PutPRPreviewSet(ctx, state.PRPreviewSet{InstallationID: 7, RepoFullName: "octo/db",
		PRNumber: 42, CommitSHA: head.CommitSHA, RootAppID: fourth[1].ID,
		MemberAppIDs: []string{fourth[1].ID}}); err != nil {
		t.Fatal(err)
	}
	head.CommitSHA = strings.Repeat("e", 40)
	if _, err := store.ReservePRPreviewSet(ctx, head, []state.App{preview("api"), preview("worker")}, limits); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("swap of shared db = %v, want quota error", err)
	}
	if db, err := store.AppByID(ctx, fourth[1].ID); err != nil || db.Status != state.AppActive {
		t.Fatalf("shared db was retired = (%+v, %v)", db, err)
	}
}
