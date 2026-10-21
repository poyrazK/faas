package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type workerPinFixture struct {
	store                                                               state.Store
	account, project, app, binding, trigger, baselineDep, baselineGraph string
}

func newWorkerPinFixture(t *testing.T, store state.Store) workerPinFixture {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "worker-release-pin@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "worker-pin"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "processor", WorkloadName: "processor", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker, RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	f := workerPinFixture{store: store, account: account.ID, project: project.ID, app: app.ID}
	f.baselineDep, f.baselineGraph = f.publish(t, "baseline")
	binding, err := store.(state.QueueBindingConsumerStore).CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, DeploymentScope: "production", Name: "orders", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	f.binding, f.trigger = binding.Binding.ID, binding.Changes[0].TriggerID
	return f
}

func (f workerPinFixture) publish(t *testing.T, name string) (string, string) {
	t.Helper()
	ctx := t.Context()
	dep, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: f.app, Scope: "production", Kind: state.DeploymentKindTarball, ImageDigest: "sha256:" + name})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetDeploymentRootfs(ctx, dep.ID, "/"+name+".ext4", "layers/"+dep.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	graph, err := f.store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, f.account, f.project, "production", 1800, []state.ProjectReleaseMember{{AppID: f.app, DeploymentID: dep.ID}})
	if err != nil {
		t.Fatal(err)
	}
	return dep.ID, graph.ID
}

func (f workerPinFixture) enqueue(t *testing.T, parent string) state.Invocation {
	t.Helper()
	source := state.InvocationQueue
	if parent != "" {
		source = state.InvocationReplay
	}
	inv, err := f.store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: f.account, AppID: f.app, Source: source, QueueBindingID: f.binding, QueueName: "orders", DeploymentScope: "production", Payload: json.RawMessage(`{}`), Headers: json.RawMessage(`{}`), DueAt: time.Now(), ReplayedFromInvocationID: parent})
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func (f workerPinFixture) claim(t *testing.T, id string) state.Invocation {
	t.Helper()
	var inv state.Invocation
	var err error
	if claimer, ok := f.store.(interface {
		ClaimQueueTriggerInvocation(context.Context, string, string, string, string, int) (state.Invocation, error)
	}); ok {
		inv, err = claimer.ClaimQueueTriggerInvocation(t.Context(), id, f.trigger, f.app, "orders", 60)
	} else {
		inv, err = f.store.ClaimInvocation(t.Context(), id, "", 60)
	}
	if err != nil {
		t.Fatal(err)
	}
	inv.Source = state.InvocationSource("esm")
	return inv
}

func TestProjectWorkerReleasePinsMemory(t *testing.T) {
	testProjectWorkerReleasePins(t, state.NewMemStore(), nil, nil)
}

func testProjectWorkerReleasePins(t *testing.T, store state.Store, restart func() state.Store, expire func(string)) {
	f := newWorkerPinFixture(t, store)
	ctx := t.Context()
	policies, err := state.ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		t.Fatal(err)
	}
	if coverageStore, ok := store.(state.ProjectEnvironmentCloneSchemaCoverageStore); ok {
		coverage, err := coverageStore.ProjectEnvironmentCloneSchemaCoverage(ctx, f.account, f.project)
		if err != nil || !coverage.Known {
			t.Fatalf("migration/clone registry mismatch: %+v %v", coverage.Blockers, err)
		}
		policies = coverage.Tables
	}
	foundPolicy := false
	for _, policy := range policies {
		if policy.TableName == "project_worker_release_pins" {
			foundPolicy = policy.Kind == state.CloneSchemaOperational && len(policy.Columns) == 6
		}
	}
	if !foundPolicy {
		t.Fatal("release pins must be operational clone state")
	}
	root := f.enqueue(t, "")
	unclaimed := root
	unclaimed.Source = state.InvocationSource("esm")
	unclaimed.Attempts = 1
	if _, _, err := state.ResolveInvocationVersion(ctx, store, unclaimed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unclaimed job established routing authority: %v", err)
	}
	inv := f.claim(t, root.ID)
	wrongBinding := inv
	wrongBinding.QueueBindingID = f.baselineGraph
	if _, _, err := state.ResolveInvocationVersion(ctx, store, wrongBinding); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("foreign binding captured a pin: %v", err)
	}
	forged := inv
	forged.Headers, _ = json.Marshal(map[string]string{api.ReleaseHeader: f.baselineGraph})
	if _, _, err := state.ResolveInvocationVersion(ctx, store, forged); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("dispatch envelope changed enqueue pin intent: %v", err)
	}
	stale := inv
	stale.Attempts++
	if _, _, err := state.ResolveInvocationVersion(ctx, store, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale claim: %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			_, v, err := state.ResolveInvocationVersion(ctx, store, inv)
			if err == nil && (v.ReleaseID != f.baselineGraph || v.DeploymentID != f.baselineDep) {
				err = errors.New("wrong baseline graph")
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	_, newGraph := f.publish(t, "candidate")
	if restart != nil {
		store = restart()
		f.store = store
	}
	// Replay lineage in the delivery envelope must never override the ledger.
	inv.ReplayRootInvocationID = newGraph
	resolved, v, err := state.ResolveInvocationVersion(ctx, store, inv)
	if err != nil || v.ReleaseID != f.baselineGraph || v.DeploymentID != f.baselineDep {
		t.Fatalf("cutover changed admitted work: %+v %v", v, err)
	}
	var headers map[string]string
	if err := json.Unmarshal(resolved.Headers, &headers); err != nil || headers[api.ReleaseHeader] != f.baselineGraph {
		t.Fatalf("canonical graph missing: %s %v", resolved.Headers, err)
	}
	conflict := inv
	conflict.Headers, _ = json.Marshal(map[string]string{api.ReleaseHeader: newGraph})
	if _, _, err := state.ResolveInvocationVersion(ctx, store, conflict); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("conflicting explicit graph: %v", err)
	}
	conflict = inv
	conflict.ReplayGeneration++
	if _, _, err := state.ResolveInvocationVersion(ctx, store, conflict); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale replay generation: %v", err)
	}
	if err := store.FailInvocation(ctx, inv.ID, "retry", time.Nanosecond, 3, state.WithClaimAttempt(inv.Attempts)); err != nil {
		t.Fatal(err)
	}
	retry := f.claim(t, inv.ID)
	_, v, err = state.ResolveInvocationVersion(ctx, store, retry)
	if err != nil || v.ReleaseID != f.baselineGraph {
		t.Fatalf("retry moved graphs: %+v %v", v, err)
	}
	if err := store.FailInvocation(ctx, retry.ID, "dead", time.Second, 1, state.WithClaimAttempt(retry.Attempts)); err != nil {
		t.Fatal(err)
	}
	replayer := store.(interface {
		RetryQueueDeadLetter(context.Context, string, string) (state.Invocation, error)
	})
	replayed, err := replayer.RetryQueueDeadLetter(ctx, f.account, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ReplayGeneration <= retry.ReplayGeneration {
		t.Fatal("replay generation did not advance")
	}
	replayed = f.claim(t, replayed.ID)
	_, v, err = state.ResolveInvocationVersion(ctx, store, replayed)
	if err != nil || v.ReleaseID != f.baselineGraph {
		t.Fatalf("operator replay moved graphs: %+v %v", v, err)
	}
	if _, _, err := state.ResolveInvocationVersion(ctx, store, retry); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("prior generation claim adopted replay: %v", err)
	}
	// A new unrelated job follows the candidate graph, rather than inheriting
	// a pin from the worker application or queue binding.
	fresh := f.claim(t, f.enqueue(t, "").ID)
	_, v, err = state.ResolveInvocationVersion(ctx, store, fresh)
	if err != nil || v.ReleaseID != newGraph {
		t.Fatalf("new work did not follow cutover: %+v %v", v, err)
	}
	if expire != nil {
		expire(f.baselineGraph)
		for range 2 {
			_, v, err = state.ResolveInvocationVersion(ctx, store, replayed)
			if !errors.Is(err, state.ErrNotFound) || v.ReleaseID != "" {
				t.Fatalf("expired pin fell through to candidate: %+v %v", v, err)
			}
		}
	}
}
