package main

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type startupVerifierFunc func(context.Context, string, string) error

func (f startupVerifierFunc) Verify(ctx context.Context, layer, sig string) error {
	return f(ctx, layer, sig)
}

// fakeWarmStore serves apps and their live deployments; tests mutate it
// between warm passes.
type fakeWarmStore struct {
	mu   sync.Mutex
	apps []state.App
	deps map[string][]state.Deployment
}

func (s *fakeWarmStore) ListAllApps(context.Context) ([]state.App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]state.App(nil), s.apps...), nil
}

func (s *fakeWarmStore) ListAppsByNodeID(_ context.Context, nodeID string) ([]state.App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []state.App
	for _, app := range s.apps {
		if app.NodeID == nodeID {
			out = append(out, app)
		}
	}
	return out, nil
}

func (s *fakeWarmStore) LiveDeployments(_ context.Context, appID string) ([]state.Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]state.Deployment(nil), s.deps[appID]...), nil
}

func (s *fakeWarmStore) setNode(appID, nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.apps {
		if s.apps[i].ID == appID {
			s.apps[i].NodeID = nodeID
		}
	}
}

// recordingVerifier records verified keys and fails the keys in bad.
type recordingVerifier struct {
	mu   sync.Mutex
	seen []string
	bad  map[string]bool
}

func (v *recordingVerifier) Verify(_ context.Context, key, sig string) error {
	if sig != "sigs/"+key+".sig" {
		return errors.New("wrong signature key " + sig)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.seen = append(v.seen, key)
	if v.bad[key] {
		return errors.New("invalid signature")
	}
	return nil
}

func (v *recordingVerifier) take() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := v.seen
	v.seen = nil
	sort.Strings(out)
	return out
}

func live(appID, key string) state.Deployment {
	return state.Deployment{AppID: appID, Status: state.DeployLive, RootfsKey: key}
}

func TestPrepareAttestationsBoundsWorkAndKeepsFailuresIsolated(t *testing.T) {
	var mu sync.Mutex
	active, maxActive := 0, 0
	verify := startupVerifierFunc(func(ctx context.Context, key, sig string) error {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
		if key == "bad" {
			return errors.New("invalid signature")
		}
		return nil
	})
	verified, failed, err := prepareLayerAttestations(context.Background(), []string{"one", "two", "bad"}, verify, discardLog())
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(verified)
	if len(verified) != 2 || verified[0] != "one" || verified[1] != "two" || len(failed) != 1 || failed[0] != "bad" {
		t.Fatalf("verified=%v failed=%v", verified, failed)
	}
	if active != 0 || maxActive > api.AttestationWarmWorkers {
		t.Fatalf("active=%d max=%d", active, maxActive)
	}
}

func TestPrepareAttestationsCancellationCannotOpenReadiness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	verify := startupVerifierFunc(func(ctx context.Context, _, _ string) error { cancel(); <-ctx.Done(); return ctx.Err() })
	_, _, err := prepareLayerAttestations(ctx, []string{"one"}, verify, discardLog())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
}

func TestStartLayerAttestationWarmDoesNotBlockReadiness(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	verify := startupVerifierFunc(func(context.Context, string, string) error {
		close(started)
		<-release
		return nil
	})
	store := &fakeWarmStore{
		apps: []state.App{{ID: "app"}},
		deps: map[string][]state.Deployment{"app": {live("app", "slow-remote-layer")}},
	}
	done := (&layerAttestationWarm{store: store, verifier: verify, log: discardLog()}).start(context.Background())

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background attestation warm did not start")
	}
	select {
	case <-done:
		t.Fatal("background attestation warm completed before verifier was released")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("background attestation warm did not finish")
	}
}

// production-us hunt #5 (H5-52): the owner-less control-plane schedd hashed
// every live layer after each restart and starved the host. The warm
// verifies only layers of apps this schedd owns, deduplicated, live only.
func TestLayerAttestationWarmSkipsAppsOwnedElsewhere(t *testing.T) {
	store := &fakeWarmStore{
		apps: []state.App{{ID: "mine"}, {ID: "peer"}},
		deps: map[string][]state.Deployment{
			"mine": {live("mine", "mine-layer"), live("mine", "mine-layer"), {AppID: "mine", Status: state.DeployFailed, RootfsKey: "excluded"}},
			"peer": {live("peer", "peer-layer")},
		},
	}
	verifier := &recordingVerifier{}
	warm := &layerAttestationWarm{store: store, verifier: verifier, owns: func(app state.App) bool { return app.ID == "mine" }, log: discardLog()}
	<-warm.start(context.Background())
	if got := verifier.take(); len(got) != 1 || got[0] != "mine-layer" {
		t.Fatalf("verified %v, want only the owned app's live layer", got)
	}
}

// production-us hunt #6 (H5-56): a rolling rollout restarted each compute
// schedd while its apps were homed on the peer and re-homed them two minutes
// later. The startup-only warm verified nothing, so every first wake hashed
// its layer inside the wake path. Later passes pick up re-homed apps, skip
// layers already verified, and retry a failed layer only after the backoff.
func TestLayerAttestationWarmFollowsOwnershipAfterStartup(t *testing.T) {
	const self, peer = "node-self", "node-peer"
	store := &fakeWarmStore{
		apps: []state.App{{ID: "a", NodeID: peer}, {ID: "b", NodeID: peer}},
		deps: map[string][]state.Deployment{"a": {live("a", "layer-a")}, "b": {live("b", "layer-b")}},
	}
	verifier := &recordingVerifier{bad: map[string]bool{"layer-b": true}}
	now := time.Date(2026, 10, 8, 6, 31, 5, 0, time.UTC)
	warm := &layerAttestationWarm{
		store: store, verifier: verifier, ownerNodeID: self,
		owns: func(app state.App) bool { return app.NodeID == self },
		log:  discardLog(), now: func() time.Time { return now },
	}
	ctx := context.Background()
	<-warm.start(ctx) // interval 0: the startup pass only
	if got := verifier.take(); len(got) != 0 {
		t.Fatalf("startup pass verified %v while the peer owned every app", got)
	}

	store.setNode("a", self)
	store.setNode("b", self)
	now = now.Add(2 * time.Minute)
	warm.pass(ctx, false)
	if got := verifier.take(); len(got) != 2 || got[0] != "layer-a" || got[1] != "layer-b" {
		t.Fatalf("pass after re-homing verified %v, want both layers", got)
	}

	now = now.Add(api.AttestationWarmInterval)
	warm.pass(ctx, false)
	if got := verifier.take(); len(got) != 0 {
		t.Fatalf("pass inside the retry backoff verified %v, want nothing", got)
	}

	now = now.Add(api.AttestationWarmRetryBackoff)
	warm.pass(ctx, false)
	if got := verifier.take(); len(got) != 1 || got[0] != "layer-b" {
		t.Fatalf("pass after the backoff verified %v, want only the failed layer", got)
	}

	// A new deployment of an owned app is verified on the next pass.
	store.mu.Lock()
	store.deps["a"] = []state.Deployment{live("a", "layer-a2")}
	store.mu.Unlock()
	warm.pass(ctx, false)
	if got := verifier.take(); len(got) != 1 || got[0] != "layer-a2" {
		t.Fatalf("pass after a redeploy verified %v, want the new layer", got)
	}
}
