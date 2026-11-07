package state_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubscope"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStorePRPreviewSetHeadAndClosure(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	rootID, siblingID, otherPRID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	root := state.App{ID: rootID, AccountID: uuid.NewString(), ProjectID: uuid.NewString(), Slug: "pr-42-api", PreviewOfSlug: "api", PreviewPrNumber: 42, Status: state.AppActive}
	(*m.Apps())[rootID] = root
	(*m.Apps())[siblingID] = state.App{ID: siblingID, AccountID: root.AccountID, ProjectID: root.ProjectID, Slug: "pr-42-worker", PreviewOfSlug: "worker", PreviewPrNumber: 42, Status: state.AppActive}
	(*m.Apps())[otherPRID] = state.App{ID: otherPRID, AccountID: root.AccountID, ProjectID: root.ProjectID, Slug: "pr-43-worker", PreviewOfSlug: "worker", PreviewPrNumber: 43, Status: state.AppActive}
	set := state.PRPreviewSet{InstallationID: 7, RepoFullName: "octo/api", PRNumber: 42, CommitSHA: strings.Repeat("a", 40), RootAppID: rootID, MemberAppIDs: []string{siblingID, rootID}}
	if err := m.PutPRPreviewSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	set.MemberAppIDs[0] = otherPRID
	got, err := m.GetPRPreviewSet(ctx, 7, "octo/api", 42)
	if err != nil || got.MemberAppIDs[0] != siblingID {
		t.Fatalf("stored set = (%+v, %v), want defensive copy", got, err)
	}
	got.MemberAppIDs[0] = otherPRID
	got, _ = m.GetPRPreviewSet(ctx, 7, "octo/api", 42)
	if got.MemberAppIDs[0] != siblingID {
		t.Fatal("get returned mutable member slice")
	}
	if err := m.ClosePRPreviewSet(ctx, 7, "octo/api", 42); err != nil {
		t.Fatal(err)
	}
	got, _ = m.GetPRPreviewSet(ctx, 7, "octo/api", 42)
	if !got.Closed {
		t.Fatal("closed PR set remains active")
	}
	set.MemberAppIDs = []string{rootID}
	set.CommitSHA = strings.Repeat("b", 40)
	if err := m.PutPRPreviewSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	got, _ = m.GetPRPreviewSet(ctx, 7, "octo/api", 42)
	if got.Closed || got.CommitSHA != set.CommitSHA || len(got.MemberAppIDs) != 1 {
		t.Fatalf("new head did not replace closed closure: %+v", got)
	}
	set.MemberAppIDs = []string{rootID, otherPRID}
	if err := m.PutPRPreviewSet(ctx, set); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cross-PR member error = %v, want conflict", err)
	}
}

func TestMemStorePRPreviewSetRetiresOnlyUnreferencedMembers(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	accountID, projectID := uuid.NewString(), uuid.NewString()
	rootA, rootB, sibling := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for id, name := range map[string]string{rootA: "api", rootB: "web", sibling: "worker"} {
		(*m.Apps())[id] = state.App{ID: id, AccountID: accountID, ProjectID: projectID,
			Slug: "pr-42-" + name, WorkloadName: name, PreviewOfSlug: name,
			PreviewPrNumber: 42, PreviewPrState: state.PreviewPrStateOpen, Status: state.AppActive}
	}
	setA := state.PRPreviewSet{InstallationID: 7, RepoFullName: "octo/api", PRNumber: 42,
		CommitSHA: strings.Repeat("a", 40), RootAppID: rootA, MemberAppIDs: []string{rootA, sibling}}
	setB := state.PRPreviewSet{InstallationID: 7, RepoFullName: "octo/web", PRNumber: 42,
		CommitSHA: strings.Repeat("a", 40), RootAppID: rootB, MemberAppIDs: []string{rootB, sibling}}
	for _, set := range []state.PRPreviewSet{setA, setB} {
		if err := m.PutPRPreviewSet(ctx, set); err != nil {
			t.Fatal(err)
		}
	}
	setA.MemberAppIDs = []string{rootA}
	setA.CommitSHA = strings.Repeat("b", 40)
	if err := m.PutPRPreviewSet(ctx, setA); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.AppByID(ctx, sibling); got.PreviewPrState != state.PreviewPrStateOpen {
		t.Fatalf("shared member state = %q, want open", got.PreviewPrState)
	}
	setB.MemberAppIDs = []string{rootB}
	setB.CommitSHA = strings.Repeat("b", 40)
	if err := m.PutPRPreviewSet(ctx, setB); err != nil {
		t.Fatal(err)
	}
	got, err := m.AppByID(ctx, sibling)
	if err != nil || got.PreviewPrState != state.PreviewPrStateStale || got.PreviewExpiresAt == nil || got.PreviewExpiresAt.After(time.Now()) {
		t.Fatalf("retired member = (%+v, %v), want stale and expired", got, err)
	}
	eligible, err := m.ListPreviewsForTeardown(ctx, time.Now(), 10)
	if err != nil || len(eligible) != 1 || eligible[0].ID != sibling {
		t.Fatalf("janitor candidates = (%+v, %v), want retired sibling", eligible, err)
	}
	// A reintroduced dependency can reuse its row while it is still awaiting
	// janitor cleanup; the subsequent set replacement must not re-retire it.
	if _, err := m.RefreshPRPreview(ctx, sibling, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	setA.MemberAppIDs = []string{rootA, sibling}
	setA.CommitSHA = strings.Repeat("c", 40)
	if err := m.PutPRPreviewSet(ctx, setA); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.AppByID(ctx, sibling); got.PreviewPrState != state.PreviewPrStateOpen {
		t.Fatalf("reintroduced member state = %q, want open", got.PreviewPrState)
	}
}

func TestMemSharedPRPreviewSetsAreAccountScoped(t *testing.T) {
	testSharedPRPreviewSets(t, state.NewMemStore())
}
func TestPgSharedPRPreviewSetsAreAccountScoped(t *testing.T) {
	store, _ := pgStore(t)
	testSharedPRPreviewSets(t, store)
}

type sharedPreviewStore interface {
	FailPRPreviewSetup(context.Context, state.PRPreviewHead, string) error
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	PutPRPreviewSet(context.Context, state.PRPreviewSet) error
	GetPRPreviewSet(context.Context, int64, string, int) (state.PRPreviewSet, error)
	ClosePRPreviewSet(context.Context, int64, string, int) error
}

func testSharedPRPreviewSets(t *testing.T, store sharedPreviewStore) {
	t.Helper()
	ctx := t.Context()
	var ids []string
	var roots []string
	for _, name := range []string{"first", "second"} {
		account, err := store.CreateAccount(ctx, name+uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pr-shared-" + name + strings.ReplaceAll(uuid.NewString(), "-", "")[:6], PreviewOfSlug: "shared-" + name, PreviewPrNumber: 42, PreviewPrState: state.PreviewPrStateOpen, RAMMB: 128, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutPRPreviewSet(ctx, state.PRPreviewSet{InstallationID: 7, RepoFullName: "octo/shared", PRNumber: 42, CommitSHA: strings.Repeat("a", 40), RootAppID: app.ID, MemberAppIDs: []string{app.ID}}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, account.ID)
		roots = append(roots, app.ID)
	}
	if _, err := store.GetPRPreviewSet(ctx, 7, "octo/shared", 42); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("ambiguous unscoped read: %v", err)
	}
	head := state.PRPreviewHead{InstallationID: 7, RepoFullName: "octo/shared", PRNumber: 42, CommitSHA: strings.Repeat("a", 40)}
	if err := store.FailPRPreviewSetup(ctx, head, "ambiguous"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("ambiguous failure update: %v", err)
	}
	if err := store.FailPRPreviewSetup(githubscope.WithAccount(ctx, ids[1]), head, "second only"); err != nil {
		t.Fatal(err)
	}
	if err := store.ClosePRPreviewSet(githubscope.WithAccount(ctx, ids[0]), 7, "octo/shared", 42); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		set, err := store.GetPRPreviewSet(githubscope.WithAccount(ctx, id), 7, "octo/shared", 42)
		if err != nil || set.RootAppID != roots[i] || set.Closed != (i == 0) || (set.SetupError == "second only") != (i == 1) {
			t.Fatalf("account preview changed: %+v %v", set, err)
		}
	}
}
