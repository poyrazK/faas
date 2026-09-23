package state

import (
	"context"
	"errors"
	"strings"
	"testing"

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
