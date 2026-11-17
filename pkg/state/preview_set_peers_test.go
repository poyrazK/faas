//go:build !no_pg

package state_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type previewSetPeerStore interface {
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	PutPRPreviewSet(context.Context, state.PRPreviewSet) error
	state.PRPreviewSetPeerReader
}

// ADR-20261105090226: a standalone PR preview's service calls are scoped by
// the preview set that holds it.
func TestPRPreviewSetPeerIDs(t *testing.T) {
	t.Run("mem", func(t *testing.T) { checkPRPreviewSetPeerIDs(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) {
		store, _ := pgStore(t)
		checkPRPreviewSetPeerIDs(t, store)
	})
}

func checkPRPreviewSetPeerIDs(t *testing.T, store previewSetPeerStore) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "set-peers@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(slug string, pr int) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 128,
			Status: state.AppActive, PreviewOfSlug: strings.TrimPrefix(slug, "pr-1-"), PreviewPrNumber: pr, PreviewPrState: state.PreviewPrStateOpen})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	web, api1, other := mk("pr-1-peers-web", 1), mk("pr-1-peers-api", 1), mk("pr-1-peers-other", 1)
	if err := store.PutPRPreviewSet(ctx, state.PRPreviewSet{InstallationID: 3, RepoFullName: "octo/peers", PRNumber: 1,
		CommitSHA: strings.Repeat("c", 40), RootAppID: web.ID, MemberAppIDs: []string{web.ID, api1.ID}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.PRPreviewSetPeerIDs(ctx, web.ID)
	want := []string{web.ID, api1.ID}
	slices.Sort(want)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("peers = (%v, %v), want %v", got, err, want)
	}
	if got, err := store.PRPreviewSetPeerIDs(ctx, other.ID); err != nil || len(got) != 0 {
		t.Fatalf("app outside any set = (%v, %v), want none", got, err)
	}
	if got, err := store.PRPreviewSetPeerIDs(ctx, "not-a-uuid"); err != nil || len(got) != 0 {
		t.Fatalf("malformed ID = (%v, %v), want none", got, err)
	}
}
