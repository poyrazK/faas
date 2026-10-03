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

func TestPgStorePreviewAppByProjectWorkload(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "preview-scope-pg@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "preview-scope-pg"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	create := func(slug string, prNumber int) state.App {
		t.Helper()
		app, createErr := store.CreateApp(ctx, state.App{
			AccountID: account.ID, ProjectID: project.ID, Slug: slug,
			WorkloadName: "billing", PreviewOfSlug: "billing", PreviewPrNumber: prNumber,
			RAMMB: 128, Status: state.AppActive,
		})
		if createErr != nil {
			t.Fatalf("CreateApp(%s): %v", slug, createErr)
		}
		return app
	}
	want := create("pr-42-preview-scope-pg-billing", 42)
	otherPR := create("pr-43-preview-scope-pg-billing", 43)
	if _, err := store.SoftDeleteAppCascade(ctx, otherPR.ID); err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}

	got, err := store.PreviewAppByProjectWorkload(ctx, account.ID, project.ID, 42, "billing")
	if err != nil {
		t.Fatalf("PreviewAppByProjectWorkload: %v", err)
	}
	if got.ID != want.ID {
		t.Fatalf("resolved app = %q, want %q", got.ID, want.ID)
	}
	if _, err := store.PreviewAppByProjectWorkload(ctx, account.ID, project.ID, 43, "billing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted preview lookup error = %v, want ErrNotFound", err)
	}
	if _, err := store.PreviewAppByProjectWorkload(ctx, account.ID, project.ID, 42, "orders"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("other workload lookup error = %v, want ErrNotFound", err)
	}
}

func TestPgStorePRPreviewSetRetiresOnlyUnreferencedMembers(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "preview-set-retirement@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "preview-set-retirement"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string) state.App {
		t.Helper()
		app, createErr := store.CreateApp(ctx, state.App{
			AccountID: account.ID, ProjectID: project.ID, Slug: "pr-42-" + name,
			WorkloadName: name, PreviewOfSlug: name, PreviewPrNumber: 42,
			PreviewPrState: state.PreviewPrStateOpen, RAMMB: 128, Status: state.AppActive,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return app
	}
	rootA, rootB, worker := create("api"), create("web"), create("worker")
	setA := state.PRPreviewSet{InstallationID: 7, RepoFullName: "octo/api", PRNumber: 42,
		CommitSHA: strings.Repeat("a", 40), RootAppID: rootA.ID, MemberAppIDs: []string{rootA.ID, worker.ID}}
	setB := state.PRPreviewSet{InstallationID: 7, RepoFullName: "octo/web", PRNumber: 42,
		CommitSHA: strings.Repeat("a", 40), RootAppID: rootB.ID, MemberAppIDs: []string{rootB.ID, worker.ID}}
	for _, set := range []state.PRPreviewSet{setA, setB} {
		if err := store.PutPRPreviewSet(ctx, set); err != nil {
			t.Fatal(err)
		}
	}
	setA.MemberAppIDs = []string{rootA.ID}
	setA.CommitSHA = strings.Repeat("b", 40)
	if err := store.PutPRPreviewSet(ctx, setA); err != nil {
		t.Fatal(err)
	}
	if got, err := store.AppByID(ctx, worker.ID); err != nil || got.PreviewPrState != state.PreviewPrStateOpen {
		t.Fatalf("shared worker = (%+v, %v), want open", got, err)
	}
	setB.MemberAppIDs = []string{rootB.ID}
	setB.CommitSHA = strings.Repeat("b", 40)
	if err := store.PutPRPreviewSet(ctx, setB); err != nil {
		t.Fatal(err)
	}
	got, err := store.AppByID(ctx, worker.ID)
	if err != nil || got.PreviewPrState != state.PreviewPrStateStale || got.PreviewExpiresAt == nil || got.PreviewExpiresAt.After(time.Now()) {
		t.Fatalf("retired worker = (%+v, %v), want stale and expired", got, err)
	}
}
