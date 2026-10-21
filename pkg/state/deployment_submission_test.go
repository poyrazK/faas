package state_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentSubmissionMemory(t *testing.T) { testDeploymentSubmissions(t, state.NewMemStore()) }

func testDeploymentSubmissions(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "submissions@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "submissions"})
	if err != nil {
		t.Fatal(err)
	}
	receipts := store.(state.DeploymentSubmissionStore)
	id := strings.Repeat("a", 32)
	if _, err := receipts.DeploymentSubmission(ctx, app.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing receipt: %v", err)
	}
	const racers = 8
	var wg sync.WaitGroup
	results := make(chan error, racers)
	for range racers {
		wg.Go(func() {
			_, err := store.CreateDeployment(state.ContextWithDeploymentSubmission(ctx, id), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64)})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, state.ErrDeploymentSubmissionConflict) {
			t.Fatalf("concurrent create: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("accepted %d deployments for one attempt", winners)
	}
	r, err := receipts.DeploymentSubmission(ctx, app.ID, id)
	if err != nil || r.AppID != app.ID || r.AttemptID != id || r.DeploymentID == "" || r.AcceptedAt.IsZero() {
		t.Fatalf("receipt=%+v err=%v", r, err)
	}
	deps, err := store.ListDeploymentsForApp(ctx, app.ID, 20, 0)
	if err != nil || len(deps) != 1 || deps[0].ID != r.DeploymentID || deps[0].Status != state.DeployPending {
		t.Fatalf("rejected attempts mutated deployments: %v err=%v", deps, err)
	}
	alias := strings.Repeat("b", 32)
	a, err := receipts.AttachDeploymentSubmission(ctx, app.ID, alias, r.DeploymentID)
	if err != nil || a.DeploymentID != r.DeploymentID {
		t.Fatalf("attach=%+v err=%v", a, err)
	}
	a2, err := receipts.AttachDeploymentSubmission(ctx, app.ID, alias, r.DeploymentID)
	if err != nil || !a2.AcceptedAt.Equal(a.AcceptedAt) {
		t.Fatalf("receipt changed: %+v err=%v", a2, err)
	}
	next, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receipts.AttachDeploymentSubmission(ctx, app.ID, alias, next.ID); !errors.Is(err, state.ErrDeploymentSubmissionConflict) {
		t.Fatalf("receipt takeover: %v", err)
	}
	for _, status := range []state.DeploymentStatus{state.DeployFailed, state.DeploySuperseded} {
		if err := store.UpdateDeploymentStatus(ctx, r.DeploymentID, status, "fixture"); err != nil {
			t.Fatal(err)
		}
		got, err := receipts.DeploymentSubmission(ctx, app.ID, id)
		if err != nil || got != r {
			t.Fatalf("terminal receipt changed: %+v err=%v", got, err)
		}
		if _, err := store.CreateDeployment(state.ContextWithDeploymentSubmission(ctx, id), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("d", 64)}); !errors.Is(err, state.ErrDeploymentSubmissionConflict) {
			t.Fatalf("terminal attempt replay: %v", err)
		}
	}
	other, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "other-submission"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receipts.AttachDeploymentSubmission(ctx, other.ID, id, r.DeploymentID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-app link: %v", err)
	}
	if _, err := receipts.DeploymentSubmission(ctx, other.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-app lookup: %v", err)
	}
	if _, err := store.CreateDeployment(state.ContextWithDeploymentSubmission(ctx, "invalid"), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("d", 64)}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid attempt: %v", err)
	}
}

// Compile-time assertions include the minimal receipt interface.
var _ state.DeploymentSubmissionStore = (*state.MemStore)(nil)
var _ state.DeploymentSubmissionStore = (*state.PgStore)(nil)
