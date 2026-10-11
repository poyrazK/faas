package apphealth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func collectorFixture(t *testing.T) (*state.MemStore, state.Account, state.App) {
	t.Helper()
	s := state.NewMemStore()
	a, err := s.CreateAccount(t.Context(), "collector@example.test", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "collector", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return s, a, app
}

func TestAppHealthCollectorIndependentReadOnlyAndSpacing(t *testing.T) {
	s, account, app := collectorFixture(t)
	observed := []string{}
	c := Collector{Store: s, Observe: func(outcome string, _ time.Duration) { observed = append(observed, outcome) }}
	n, err := c.Sweep(t.Context())
	if err != nil || n != 1 || len(observed) != 1 || observed[0] != "recorded" {
		t.Fatalf("sweep %d %v %v", n, err, observed)
	}
	page, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 20, "", time.Now().UTC())
	if err != nil || len(page.Entries) != 1 || !page.CollectorFresh || page.Latest.Status != Unknown {
		t.Fatalf("recorded without dashboard %+v %v", page, err)
	}
	if page.Latest.Checks[len(page.Latest.Checks)-1].Reason != "request_plan_restricted" {
		t.Fatal("collection bypassed plan entitlement")
	}
	instances, err := s.ListInstancesForApp(t.Context(), app.ID)
	if err != nil || len(instances) != 0 {
		t.Fatal("collector woke app", err)
	}
	if n, err := c.Sweep(t.Context()); err != nil || n != 0 {
		t.Fatalf("minimum spacing %d %v", n, err)
	}
	// On-demand collection also cannot append or refresh history.
	_ = Collect(t.Context(), s, nil, app, false)
	after, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 20, "", time.Now().UTC())
	if err != nil || len(after.Entries) != 1 || after.Latest.EvaluatedAt != page.Latest.EvaluatedAt {
		t.Fatal("read refreshed history", err)
	}
}

type failingCollectionStore struct{ *state.MemStore }

func (s failingCollectionStore) AccountByID(context.Context, string) (state.Account, error) {
	return state.Account{}, errors.New("unavailable")
}

func TestAppHealthCollectorFailureAndCancellation(t *testing.T) {
	s, account, app := collectorFixture(t)
	c := Collector{Store: failingCollectionStore{s}}
	if n, err := c.Sweep(t.Context()); err != nil || n != 1 {
		t.Fatalf("failed check %d %v", n, err)
	}
	page, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 20, "", time.Now().UTC())
	if err != nil || page.Latest != nil || page.CollectorFresh {
		t.Fatal("failure published success", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestAppHealthCollectorExcludesWorkerAndDeletedApps(t *testing.T) {
	s, account, app := collectorFixture(t)
	app.Manifest.ExecutionMode = "worker"
	if _, err := s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest}); err != nil {
		t.Fatal(err)
	}
	c := Collector{Store: s}
	if n, err := c.Sweep(t.Context()); err != nil || n != 0 {
		t.Fatalf("worker collected %d %v", n, err)
	}
	app.Manifest.ExecutionMode = "service"
	if _, err := s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SoftDeleteAppCascade(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := c.Sweep(t.Context()); err != nil || n != 0 {
		t.Fatalf("deleted app collected %d %v", n, err)
	}
	_ = account
}
