package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func preparePolicyFixture(current state.App, values map[string]json.RawMessage) (state.AppManifest, error) {
	m := current.Manifest
	for field, value := range values {
		if field == "profiling" {
			if err := json.Unmarshal(value, &m.Profiling); err != nil {
				return m, err
			}
		}
		if field == "tracing" {
			if err := json.Unmarshal(value, &m.Tracing); err != nil {
				return m, err
			}
		}
	}
	return m, nil
}

func stagingFixture(t *testing.T, store state.Store) (state.App, state.DeploymentStagingParams) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "policies@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "policies"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.(state.DeploymentStagingStore).DeploymentStagingSnapshot(ctx, acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return app, state.DeploymentStagingParams{AccountID: acct.ID, Prepare: preparePolicyFixture, Request: api.DeploymentStagingRequest{
		SessionID: strings.Repeat("a", 32), Operation: "stage", ExpectedAppID: app.ID, ExpectedRevision: snapshot.Revision, ExpectedDeploymentID: snapshot.LatestDeploymentID,
		Before: snapshot.Settings, Desired: map[string]json.RawMessage{"profiling": json.RawMessage(`{"enabled":true,"window_seconds":10}`), "tracing": json.RawMessage(`{"enabled":true,"sample_ratio":0.25}`)},
	}}
}

func TestDeploymentPolicyJournalMemory(t *testing.T) {
	testDeploymentPolicyJournal(t, state.NewMemStore())
}

func testDeploymentPolicyJournal(t *testing.T, store state.Store) {
	app, p := stagingFixture(t, store)
	s := store.(state.DeploymentStagingStore)
	ctx := t.Context()
	staged, err := s.ApplyDeploymentStaging(ctx, app.ID, p)
	if err != nil || staged.Receipt.Status != "staged" || staged.Receipt.Revision != p.Request.ExpectedRevision+1 {
		t.Fatalf("stage=%+v err=%v", staged.Receipt, err)
	}
	for range 2 {
		replay, err := s.ApplyDeploymentStaging(ctx, app.ID, p)
		if err != nil || replay.Changed || replay.Receipt != staged.Receipt {
			t.Fatalf("stage replay=%+v err=%v", replay, err)
		}
	}
	// A separate client edits an unrelated manifest field during staging.
	current, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := current.Manifest
	m.RequestTimeoutS = 44
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &m}); err != nil {
		t.Fatal(err)
	}
	p.Request.Operation = "restore"
	cleaned, err := s.ApplyDeploymentStaging(ctx, app.ID, p)
	if err != nil || cleaned.Receipt.Status != "cleaned" || !cleaned.Changed {
		t.Fatalf("cleanup=%+v err=%v", cleaned, err)
	}
	if cleaned.App.Manifest.Profiling != nil || cleaned.App.Manifest.Tracing != nil || cleaned.App.Manifest.RequestTimeoutS != 44 {
		t.Fatalf("cleanup lost before/unrelated values: %+v", cleaned.App.Manifest)
	}
	for range 2 {
		replay, err := s.ApplyDeploymentStaging(ctx, app.ID, p)
		if err != nil || replay.Changed || replay.Receipt.Revision != cleaned.Receipt.Revision {
			t.Fatalf("cleanup reply recovery=%+v err=%v", replay, err)
		}
	}
	p.Request.Operation = "stage"
	if _, err := s.ApplyDeploymentStaging(ctx, app.ID, p); !errors.Is(err, state.ErrDeploymentStagingConflict) {
		t.Fatalf("late stage after cleanup=%v", err)
	}
	deps, err := store.ListDeploymentsForApp(ctx, app.ID, 10, 0)
	if err != nil || len(deps) != 0 {
		t.Fatalf("staging queued builds: %+v err=%v", deps, err)
	}
}

func TestDeploymentPolicyJournalMemoryConflicts(t *testing.T) {
	for _, mode := range []string{"cleanup-first", "policy-edit", "aba", "same-value", "new-deploy", "changed-intent", "wrong-app", "wrong-account"} {
		t.Run(mode, func(t *testing.T) { testDeploymentPolicyConflict(t, state.NewMemStore(), mode) })
	}
}

func testDeploymentPolicyConflict(t *testing.T, store state.Store, mode string) {
	t.Helper()
	app, p := stagingFixture(t, store)
	s := store.(state.DeploymentStagingStore)
	ctx := t.Context()
	if mode == "cleanup-first" {
		p.Request.Operation = "restore"
		r, err := s.ApplyDeploymentStaging(ctx, app.ID, p)
		if err != nil || r.Changed || r.Receipt.Status != "cleaned" || r.Receipt.Revision != p.Request.ExpectedRevision+1 {
			t.Fatalf("seal=%+v err=%v", r, err)
		}
		p.Request.Operation = "stage"
		if _, err := s.ApplyDeploymentStaging(ctx, app.ID, p); !errors.Is(err, state.ErrDeploymentStagingConflict) {
			t.Fatalf("delayed stage=%v", err)
		}
		return
	}
	if _, err := s.ApplyDeploymentStaging(ctx, app.ID, p); err != nil {
		t.Fatal(err)
	}
	current, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := current.Manifest
	switch mode {
	case "policy-edit", "aba":
		m.Profiling = &api.ProfilingConfig{Enabled: true, WindowSeconds: 20}
		if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &m}); err != nil {
			t.Fatal(err)
		}
		if mode == "aba" {
			m.Profiling = &api.ProfilingConfig{Enabled: true, WindowSeconds: 10}
			if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &m}); err != nil {
				t.Fatal(err)
			}
		}
	case "same-value":
		if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &m, TouchDeploymentPolicies: true}); err != nil {
			t.Fatal(err)
		}
	case "new-deploy":
		if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64)}); err != nil {
			t.Fatal(err)
		}
	case "changed-intent":
		p.Request.Before["profiling"] = json.RawMessage(`{"enabled":true}`)
	case "wrong-app":
		p.Request.ExpectedAppID = "another-app"
	case "wrong-account":
		p.AccountID = "another-account"
	}
	p.Request.Operation = "restore"
	if _, err := s.ApplyDeploymentStaging(ctx, app.ID, p); !errors.Is(err, state.ErrDeploymentStagingConflict) {
		t.Fatalf("conflict %s=%v", mode, err)
	}
	after, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Manifest.Profiling == nil || !after.Manifest.Profiling.Enabled || after.Manifest.Tracing == nil || !after.Manifest.Tracing.Enabled {
		t.Fatalf("conflicting cleanup overwrote policies: %+v", after.Manifest)
	}
}

func TestDeploymentPolicyJournalMemoryConcurrency(t *testing.T) {
	testPolicyStageCleanupRace(t, state.NewMemStore())
}

func TestDeploymentPolicyJournalMemorySameDesired(t *testing.T) {
	testPolicySameDesired(t, state.NewMemStore())
}

func testPolicySameDesired(t *testing.T, store state.Store) {
	t.Helper()
	app, p := stagingFixture(t, store)
	s := store.(state.DeploymentStagingStore)
	first, err := s.ApplyDeploymentStaging(t.Context(), app.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	newIntent := p
	newIntent.Request.SessionID = strings.Repeat("b", 32)
	newIntent.Request.ExpectedRevision = first.Receipt.Revision
	newIntent.Request.Before = p.Request.Desired
	second, err := s.ApplyDeploymentStaging(t.Context(), app.ID, newIntent)
	if err != nil || second.Changed || second.Receipt.Revision != first.Receipt.Revision+1 {
		t.Fatalf("same desired did not claim ownership: %+v err=%v", second, err)
	}
	p.Request.Operation = "restore"
	if _, err := s.ApplyDeploymentStaging(t.Context(), app.ID, p); !errors.Is(err, state.ErrDeploymentStagingConflict) {
		t.Fatalf("older cleanup undid new intent: %v", err)
	}
	newIntent.Request.Operation = "restore"
	cleaned, err := s.ApplyDeploymentStaging(t.Context(), app.ID, newIntent)
	if err != nil || cleaned.Changed || cleaned.Receipt.Status != "cleaned" {
		t.Fatalf("same-value cleanup=%+v err=%v", cleaned, err)
	}
	newIntent.Request.Operation = "stage"
	if _, err := s.ApplyDeploymentStaging(t.Context(), app.ID, newIntent); !errors.Is(err, state.ErrDeploymentStagingConflict) {
		t.Fatalf("cleanup did not fence duplicate stage: %v", err)
	}
}

func testPolicyStageCleanupRace(t *testing.T, store state.Store) {
	t.Helper()
	app, p := stagingFixture(t, store)
	s := store.(state.DeploymentStagingStore)
	var wg sync.WaitGroup
	errorsCh := make(chan error, 2)
	wg.Go(func() { _, err := s.ApplyDeploymentStaging(t.Context(), app.ID, p); errorsCh <- err })
	wg.Go(func() {
		restore := p
		restore.Request.Operation = "restore"
		_, err := s.ApplyDeploymentStaging(t.Context(), app.ID, restore)
		errorsCh <- err
	})
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil && !errors.Is(err, state.ErrDeploymentStagingConflict) {
			t.Fatal(err)
		}
	}
	after, err := store.AppByID(t.Context(), app.ID)
	if err != nil || after.Manifest.Profiling != nil || after.Manifest.Tracing != nil {
		t.Fatalf("race left staged policies: %+v err=%v", after, err)
	}
}
