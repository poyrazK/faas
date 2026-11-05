package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261026105459: a plain member (`gregale dev --all`) is named by any
// service DNS label, such as `ui`; members enrolled in rehearsal capture keep
// the slug rule their action policies are written for.
func assertScenarioTestMemberServiceNames(t *testing.T, ctx context.Context, store state.Store, email string) {
	t.Helper()
	account, err := store.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour)
	create := func(slug string) state.App {
		t.Helper()
		app, createErr := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug, RAMMB: 128, Status: state.AppActive,
			PreviewOfSlug: slug, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return app
	}
	ui, bad, captured := create("dev-names-ui"), create("dev-names-bad"), create("dev-names-captured")
	run := "abcdef0123456789abcdef0123456789"
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, run, []state.ScenarioTestMember{{Workload: "ui", AppID: ui.ID}}); err != nil {
		t.Fatalf("short service name refused: %v", err)
	}
	if got, err := store.ScenarioTestAppByWorkload(ctx, account.ID, run, "ui"); err != nil || got.ID != ui.ID {
		t.Fatalf("ui = (%+v, %v)", got, err)
	}
	for _, name := range []string{"-ui", "ui_api", "UI"} {
		if err := store.RegisterScenarioTestMembers(ctx, account.ID, run, []state.ScenarioTestMember{{Workload: name, AppID: bad.ID}}); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("service name %q = %v, want refused", name, err)
		}
	}
	capture := "0123456789abcdef0123456789abcdef"
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, capture, []state.ScenarioTestMember{
		{Workload: "ui", AppID: captured.ID, WebhookMode: state.ScenarioWebhookCaptureMode},
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("capture member with a short name = %v, want refused", err)
	}
}

func TestScenarioTestMemberServiceNames(t *testing.T) {
	assertScenarioTestMemberServiceNames(t, context.Background(), state.NewMemStore(), "member-names@example.com")
}
