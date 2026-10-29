package state_test

// adr: 20261028100000 — a PR preview goes when its environment's production
// app goes; a service that exists only in the PR stays with its root.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type orphanPreviewStore interface {
	CreateAccount(ctx context.Context, email string, plan api.Plan) (state.Account, error)
	CreateApp(ctx context.Context, app state.App) (state.App, error)
	PutPRPreviewSet(ctx context.Context, set state.PRPreviewSet) error
	ScheduleAppDeletion(ctx context.Context, id string, graceUntil time.Time) (state.App, error)
	AppByID(ctx context.Context, id string) (state.App, error)
	state.OrphanedPRPreviewMarker
}

func orphanedPRPreviewContract(t *testing.T, ctx context.Context, s orphanPreviewStore) {
	t.Helper()
	account, err := s.CreateAccount(ctx, "preview-orphans@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	create := func(slug, parent string, pr int) state.App {
		t.Helper()
		app := state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, Status: state.AppActive}
		if parent != "" {
			app.PreviewOfSlug, app.PreviewPrNumber, app.PreviewPrState = parent, pr, state.PreviewPrStateOpen
		}
		created, err := s.CreateApp(ctx, app)
		if err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
		return created
	}
	wantState := func(app state.App, want string) {
		t.Helper()
		got, err := s.AppByID(ctx, app.ID)
		if err != nil || got.PreviewPrState != want {
			t.Fatalf("%s preview state = (%q, %v), want %q", app.Slug, got.PreviewPrState, err, want)
		}
	}
	web := create("orphans-web", "", 0)
	root := create("pr-7-orphans-web", web.Slug, 7)
	prOnly := create("pr-7-orphans-jobs", "orphans-jobs", 7) // the PR adds this service
	loose := create("pr-8-orphans-gone", "orphans-gone", 8)  // its production app is gone
	if err := s.PutPRPreviewSet(ctx, state.PRPreviewSet{InstallationID: 9, RepoFullName: "octo/orphans", PRNumber: 7,
		CommitSHA: strings.Repeat("a", 40), RootAppID: root.ID, MemberAppIDs: []string{root.ID, prOnly.ID}}); err != nil {
		t.Fatal(err)
	}

	if marked, err := s.MarkOrphanedPRPreviewsForTeardown(ctx, 100); err != nil || marked != 1 {
		t.Fatalf("first pass marked = (%d, %v), want only the preview without a production app", marked, err)
	}
	wantState(loose, state.PreviewPrStateTearingDown)
	wantState(root, state.PreviewPrStateOpen)
	wantState(prOnly, state.PreviewPrStateOpen)

	if _, err := s.ScheduleAppDeletion(ctx, web.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if marked, err := s.MarkOrphanedPRPreviewsForTeardown(ctx, 100); err != nil || marked != 2 {
		t.Fatalf("after the production delete marked = (%d, %v), want the whole PR environment", marked, err)
	}
	wantState(root, state.PreviewPrStateTearingDown)
	wantState(prOnly, state.PreviewPrStateTearingDown)
}

func TestMemStoreMarksOrphanedPRPreviews(t *testing.T) {
	orphanedPRPreviewContract(t, context.Background(), state.NewMemStore())
}
