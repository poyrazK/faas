package conformance

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// testPostReleaseWatchCandidates pins ADR-1445's candidate rule on both
// stores: a completed live release that kept automatic rollback, finished
// inside the window and is the newest in its scope is watched against its
// superseded predecessor; a rolled-back, opted-out or replaced release is not.
func testPostReleaseWatchCandidates(t *testing.T, fx *Fixture) {
	t.Helper()
	store, ok := fx.Store.(state.PostReleaseWatchStore)
	if !ok {
		t.Fatalf("%T does not implement state.PostReleaseWatchStore", fx.Store)
	}
	deploy := func(digest string, optIn bool) state.Deployment {
		t.Helper()
		d, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: fx.App.ID, Kind: state.DeploymentKindImage,
			ImageDigest: digest, Status: state.DeployPending, RollbackOn5xx: optIn})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	live := func(id string) {
		t.Helper()
		if err := fx.Store.MarkDeploymentLive(fx.Ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	complete := func(id string, completedAgo time.Duration) {
		t.Helper()
		completed := time.Now().UTC().Add(-completedAgo)
		started := completed.Add(-4 * time.Minute)
		if _, err := fx.Store.SafedeployStampRollout(fx.Ctx, id, "complete", &started, &completed, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	watched := func() map[string]state.PostReleaseWatchCandidate {
		t.Helper()
		rows, err := store.ListPostReleaseWatchCandidates(fx.Ctx, 30*time.Minute, 100)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]state.PostReleaseWatchCandidate{}
		for _, row := range rows {
			if row.AppID == fx.App.ID {
				out[row.DeploymentID] = row
			}
		}
		return out
	}

	previous := deploy("sha256:watch-previous", true)
	live(previous.ID)
	time.Sleep(10 * time.Millisecond)
	release := deploy("sha256:watch-release", true)
	live(release.ID)
	if err := fx.Store.MarkDeploymentSuperseded(fx.Ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	complete(release.ID, time.Minute)
	got := watched()
	if c, ok := got[release.ID]; !ok || c.PredecessorID != previous.ID || c.RolloutStartedAt.IsZero() || c.Scope != "default" {
		t.Fatalf("watched = %+v, want release %s against predecessor %s", got, release.ID, previous.ID)
	}
	if _, ok := got[previous.ID]; ok {
		t.Fatal("a superseded predecessor was watched")
	}

	complete(release.ID, 31*time.Minute)
	if _, ok := watched()[release.ID]; ok {
		t.Fatal("a release past its window was watched")
	}
	complete(release.ID, time.Minute)

	decision := json.RawMessage(`{"kind":"abort","rule":"abort_post_release_p95_latency"}`)
	if err := store.RecordPostReleaseRolloutDecision(fx.Ctx, release.ID, decision); err != nil {
		t.Fatal(err)
	}
	stored, err := fx.Store.DeploymentByID(fx.Ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]string
	if err := json.Unmarshal(stored.RolloutDecision, &back); err != nil || back["rule"] != "abort_post_release_p95_latency" {
		t.Fatalf("stored decision = %s err=%v", stored.RolloutDecision, err)
	}
	if _, err := fx.Store.MarkAutoRollback(fx.Ctx, release.ID, string(state.AutoRollbackReasonPostReleaseRegression), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, ok := watched()[release.ID]; ok {
		t.Fatal("a rolled-back release was watched")
	}

	// A newer deployment in the scope ends the watch on the older release.
	next := deploy("sha256:watch-next", true)
	live(next.ID)
	if err := fx.Store.MarkDeploymentSuperseded(fx.Ctx, release.ID); err != nil {
		t.Fatal(err)
	}
	complete(next.ID, time.Minute)
	if c, ok := watched()[next.ID]; !ok || c.PredecessorID != release.ID {
		t.Fatalf("watched = %+v, want %s against %s", watched(), next.ID, release.ID)
	}
	time.Sleep(10 * time.Millisecond)
	pending := deploy("sha256:watch-pending", true)
	if _, ok := watched()[next.ID]; ok {
		t.Fatalf("release %s still watched after %s was created", next.ID, pending.ID)
	}
	optOut := deploy("sha256:watch-optout", false)
	if err := fx.Store.UpdateDeploymentStatus(fx.Ctx, pending.ID, state.DeployFailed, "test"); err != nil {
		t.Fatal(err)
	}
	live(optOut.ID)
	if err := fx.Store.MarkDeploymentSuperseded(fx.Ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	complete(optOut.ID, time.Minute)
	if _, ok := watched()[optOut.ID]; ok {
		t.Fatal("a release that opted out of automatic rollback was watched")
	}
	if err := store.RecordPostReleaseRolloutDecision(fx.Ctx, release.ID, json.RawMessage(`{`)); err == nil {
		t.Fatal("an invalid decision was stored")
	}
}
