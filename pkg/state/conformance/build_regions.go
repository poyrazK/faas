package conformance

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedRegionalBuilder(t *testing.T, fx *Fixture) (state.App, state.ComputeNode, state.ComputeNode, state.ComputeNode) {
	t.Helper()
	region := "eu-build"
	if _, err := fx.Store.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: region, DisplayName: "Build region", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	nodes := make([]state.ComputeNode, 3)
	for i := range nodes {
		node := fx.Node
		node.ID, node.Name = "", "builder-"+uuid.NewString()
		node.Region = &region
		if i == 2 {
			other := "us-build"
			node.Region = &other
		}
		var err error
		nodes[i], err = fx.Store.CreateComputeNode(fx.Ctx, node)
		if err != nil {
			t.Fatal(err)
		}
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "build-region-"+uuid.NewString(), "", region
	app, err := fx.Store.CreateApp(fx.Ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	return app, nodes[0], nodes[1], nodes[2]
}

func regionalQueuedBuild(t *testing.T, fx *Fixture, app state.App) state.Build {
	t.Helper()
	dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	b, err := fx.Store.CreateBuild(fx.Ctx, dep.ID, dep.Kind, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func testRegionalBuildClaims(t *testing.T, fx *Fixture) {
	app, node, peer, other := seedRegionalBuilder(t, fx)
	claims := fx.Store.(state.BuildAffinityClaimStore)
	b := regionalQueuedBuild(t, fx, app)
	legacyClaims := []func() (state.Build, error){
		func() (state.Build, error) { return fx.Store.ClaimQueuedBuild(fx.Ctx, b.ID) },
		func() (state.Build, error) { return fx.Store.ClaimNextQueuedBuild(fx.Ctx) },
		func() (state.Build, error) { return fx.Store.ClaimNextQueuedBuildWithFairness(fx.Ctx, time.Minute) },
	}
	for _, claim := range legacyClaims {
		if _, err := claim(); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("unidentified regional claim: %v", err)
		}
	}
	for _, name := range []string{"", "unknown-builder", other.Name} {
		for _, grace := range []time.Duration{0, time.Millisecond, time.Hour} {
			if _, err := claims.ClaimQueuedBuildWithNodeAffinity(fx.Ctx, b.ID, name, grace); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("notification claim name=%q grace=%v: %v", name, grace, err)
			}
			if _, err := claims.ClaimNextQueuedBuildWithNodeAffinity(fx.Ctx, name, grace, time.Minute); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("poll claim name=%q grace=%v: %v", name, grace, err)
			}
		}
	}
	if err := fx.Store.NodeSetLifecycle(fx.Ctx, node.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
		t.Fatal(err)
	}
	if _, err := claims.ClaimQueuedBuildWithNodeAffinity(fx.Ctx, b.ID, node.Name, 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("inactive node claim: %v", err)
	}
	if _, err := fx.Store.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: app.ComputeRegion, DisplayName: "Paused", AcceptingNewApps: false}); err != nil {
		t.Fatal(err)
	}
	claimed, err := claims.ClaimNextQueuedBuildWithNodeAffinity(fx.Ctx, peer.Name, 0, time.Minute)
	if err != nil || claimed.ID != b.ID || claimed.BuilderNodeID != peer.ID {
		t.Fatalf("existing app in paused region: %+v %v", claimed, err)
	}
	read, err := fx.Store.BuildByID(fx.Ctx, b.ID)
	if err != nil || read.BuilderNodeID != peer.ID {
		t.Fatalf("claim owner not persisted: %+v %v", read, err)
	}
	if err := fx.Store.UpdateBuildStatus(fx.Ctx, b.ID, state.BuildSucceeded, "", false, true); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.CreateBuildProvenance(fx.Ctx, state.BuildProvenance{BuildID: b.ID, BuilderNodeID: peer.Name, StartedAt: claimed.StartedAt, FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.NodeSetLifecycle(fx.Ctx, node.ID, state.NodeLifecycleDraining, state.NodeLifecycleActive); err != nil {
		t.Fatal(err)
	}
	for _, grace := range []time.Duration{0, time.Millisecond} {
		next := regionalQueuedBuild(t, fx, app)
		if _, err := claims.ClaimQueuedBuildWithNodeAffinity(fx.Ctx, next.ID, node.Name, time.Hour); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("fresh cache preference was bypassed: %v", err)
		}
		if grace > 0 {
			time.Sleep(5 * time.Millisecond)
		}
		if _, err := claims.ClaimNextQueuedBuildWithNodeAffinity(fx.Ctx, other.Name, grace, 0); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cache fallback escaped region: %v", err)
		}
		got, err := claims.ClaimQueuedBuildWithNodeAffinity(fx.Ctx, next.ID, node.Name, grace)
		if err != nil || got.BuilderNodeID != node.ID {
			t.Fatalf("cache fallback inside region grace=%v: %+v %v", grace, got, err)
		}
	}
	// Older unidentified interfaces remain usable for legacy apps.
	legacy := regionalQueuedBuild(t, fx, fx.App)
	got, err := fx.Store.ClaimNextQueuedBuildWithFairness(fx.Ctx, time.Minute)
	if err != nil || got.ID != legacy.ID || got.BuilderNodeID != "" {
		t.Fatalf("legacy FIFO compatibility: %+v %v", got, err)
	}
}

func testRegionalBuildCleanup(t *testing.T, fx *Fixture) {
	app, node, peer, other := seedRegionalBuilder(t, fx)
	b := regionalQueuedBuild(t, fx, app)
	claims := fx.Store.(state.BuildAffinityClaimStore)
	if _, err := claims.ClaimQueuedBuildWithNodeAffinity(fx.Ctx, b.ID, node.Name, 0); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.MarkBuildCancelled(fx.Ctx, b.ID, "test", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	cleanup := fx.Store.(state.BuildVMCleanupStore)
	scoped := fx.Store.(state.BuildNodeVMCleanupStore)
	for _, name := range []string{"", "unknown-builder", peer.Name, other.Name} {
		got, err := scoped.ClaimBuildVMCleanupOnNode(fx.Ctx, name, 10)
		if err != nil || len(got) != 0 {
			t.Fatalf("wrong-node cleanup %q: %+v %v", name, got, err)
		}
	}
	if got, err := cleanup.ClaimBuildVMCleanup(fx.Ctx, 10); err != nil || len(got) != 0 {
		t.Fatalf("legacy cleanup stole owned obligation: %+v %v", got, err)
	}
	if err := fx.Store.NodeSetLifecycle(fx.Ctx, node.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
		t.Fatal(err)
	}
	got, err := scoped.ClaimBuildVMCleanupOnNode(fx.Ctx, node.Name, 10)
	if err != nil || len(got) != 1 || got[0].BuildID != b.ID {
		t.Fatalf("inactive owner cleanup: %+v %v", got, err)
	}
	first := got[0]
	if err := cleanup.CompleteBuildVMCleanup(fx.Ctx, b.ID, first.ClaimToken, errors.New("vmmd unavailable")); err != nil {
		t.Fatal(err)
	}
	got, err = scoped.ClaimBuildVMCleanupOnNode(fx.Ctx, node.Name, 10)
	if err != nil || len(got) != 1 || got[0].ClaimToken == first.ClaimToken {
		t.Fatalf("owner retry: %+v %v", got, err)
	}
	if err := cleanup.CompleteBuildVMCleanup(fx.Ctx, b.ID, first.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	if err := cleanup.CompleteBuildVMCleanup(fx.Ctx, b.ID, got[0].ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := scoped.ClaimBuildVMCleanupOnNode(fx.Ctx, node.Name, 10); err != nil || len(got) != 0 {
		t.Fatalf("completed cleanup remained claimable: %+v %v", got, err)
	}
}

func testRegionalBuildConcurrentClaims(t *testing.T, fx *Fixture) {
	app, node, peer, other := seedRegionalBuilder(t, fx)
	claims := fx.Store.(state.BuildAffinityClaimStore)
	const count = 16
	builds := make([]state.Build, count)
	for i := range builds {
		builds[i] = regionalQueuedBuild(t, fx, app)
	}
	start := make(chan struct{})
	winners := make(chan state.Build, count*4)
	errorsCh := make(chan error, count*4)
	var wg sync.WaitGroup
	for i := 0; i < count*4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			var b state.Build
			var err error
			switch {
			case i < count:
				b, err = claims.ClaimQueuedBuildWithNodeAffinity(fx.Ctx, builds[i].ID, node.Name, 0)
			case i < count*2:
				b, err = claims.ClaimNextQueuedBuildWithNodeAffinity(fx.Ctx, peer.Name, 0, time.Minute)
			case i < count*3:
				b, err = claims.ClaimQueuedBuildWithNodeAffinity(fx.Ctx, builds[i-count*2].ID, other.Name, 0)
			default:
				b, err = fx.Store.ClaimNextQueuedBuildWithFairness(fx.Ctx, time.Minute)
			}
			if err != nil {
				if !errors.Is(err, state.ErrNotFound) {
					errorsCh <- err
				}
				return
			}
			winners <- b
		}(i)
	}
	close(start)
	wg.Wait()
	close(winners)
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
	seen := map[string]bool{}
	for b := range winners {
		if seen[b.ID] || b.BuilderNodeID != node.ID && b.BuilderNodeID != peer.ID {
			t.Errorf("duplicate or misplaced winner: %+v", b)
		}
		seen[b.ID] = true
	}
	if len(seen) != count {
		t.Fatalf("claimed %d distinct builds, want %d", len(seen), count)
	}
}
