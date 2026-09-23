package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemStorePRPreviewSetHeadAndClosure(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	rootID, siblingID, otherPRID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	root := App{ID: rootID, AccountID: uuid.NewString(), ProjectID: uuid.NewString(), Slug: "pr-42-api", PreviewOfSlug: "api", PreviewPrNumber: 42, Status: AppActive}
	m.apps[rootID] = root
	m.apps[siblingID] = App{ID: siblingID, AccountID: root.AccountID, ProjectID: root.ProjectID, Slug: "pr-42-worker", PreviewOfSlug: "worker", PreviewPrNumber: 42, Status: AppActive}
	m.apps[otherPRID] = App{ID: otherPRID, AccountID: root.AccountID, ProjectID: root.ProjectID, Slug: "pr-43-worker", PreviewOfSlug: "worker", PreviewPrNumber: 43, Status: AppActive}
	set := PRPreviewSet{InstallationID: 7, RepoFullName: "octo/api", PRNumber: 42, CommitSHA: strings.Repeat("a", 40), RootAppID: rootID, MemberAppIDs: []string{siblingID, rootID}}
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
	if err := m.PutPRPreviewSet(ctx, set); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-PR member error = %v, want conflict", err)
	}
}

func TestMemStorePRPreviewSetRetiresOnlyUnreferencedMembers(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	accountID, projectID := uuid.NewString(), uuid.NewString()
	rootA, rootB, sibling := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for id, name := range map[string]string{rootA: "api", rootB: "web", sibling: "worker"} {
		m.apps[id] = App{ID: id, AccountID: accountID, ProjectID: projectID,
			Slug: "pr-42-" + name, WorkloadName: name, PreviewOfSlug: name,
			PreviewPrNumber: 42, PreviewPrState: PreviewPrStateOpen, Status: AppActive}
	}
	setA := PRPreviewSet{InstallationID: 7, RepoFullName: "octo/api", PRNumber: 42,
		CommitSHA: strings.Repeat("a", 40), RootAppID: rootA, MemberAppIDs: []string{rootA, sibling}}
	setB := PRPreviewSet{InstallationID: 7, RepoFullName: "octo/web", PRNumber: 42,
		CommitSHA: strings.Repeat("a", 40), RootAppID: rootB, MemberAppIDs: []string{rootB, sibling}}
	for _, set := range []PRPreviewSet{setA, setB} {
		if err := m.PutPRPreviewSet(ctx, set); err != nil {
			t.Fatal(err)
		}
	}
	setA.MemberAppIDs = []string{rootA}
	setA.CommitSHA = strings.Repeat("b", 40)
	if err := m.PutPRPreviewSet(ctx, setA); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.AppByID(ctx, sibling); got.PreviewPrState != PreviewPrStateOpen {
		t.Fatalf("shared member state = %q, want open", got.PreviewPrState)
	}
	setB.MemberAppIDs = []string{rootB}
	setB.CommitSHA = strings.Repeat("b", 40)
	if err := m.PutPRPreviewSet(ctx, setB); err != nil {
		t.Fatal(err)
	}
	got, err := m.AppByID(ctx, sibling)
	if err != nil || got.PreviewPrState != PreviewPrStateStale || got.PreviewExpiresAt == nil || got.PreviewExpiresAt.After(time.Now()) {
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
	if got, _ := m.AppByID(ctx, sibling); got.PreviewPrState != PreviewPrStateOpen {
		t.Fatalf("reintroduced member state = %q, want open", got.PreviewPrState)
	}
}
