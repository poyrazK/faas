package deploycontroller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	overlayA = "c7fe89a56eee1674da23d7bee29c19a5d31caf56"
	overlayB = "21b559ca386f9180d2f08c000d1f17ae8831acac"
)

// Production-us 2026-10-20: cd-controlplane run 39473046675 activated unit
// overlay c7fe89a5 over ec2334def4, its SLO gate failed, and `deployctl
// rollback --only` restored ec2334def4. Run 39480533579 then activated
// overlay 21b559ca over ec2334def4, its gate failed too, and the rollback
// restored c7fe89a5 — the newest other directory, which the first rollback
// had already removed. A rollback must restore the release its deploy
// replaced, both as the lane runs it (the plan names the base) and as an
// operator runs it (no flags: the activation history).
func TestUnitRollbackAfterAnEarlierRollbackRestoresTheBase(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  func(overlay string) RollbackRequest
	}{
		{"activation_history", func(string) RollbackRequest { return RollbackRequest{} }},
		{"plan_base", func(overlay string) RollbackRequest {
			return RollbackRequest{Target: "base", ExpectedCurrent: overlay}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, current, staged := overlayHost(t)
			base := filepath.Join(root, "base")
			controller := newController(t, root, current, &fakeRuntime{})
			for i, overlay := range []string{overlayA, overlayB} {
				req := overlayRequest(staged)
				req.ReleaseID = overlay
				if _, err := controller.AssembleOverlay(req); err != nil {
					t.Fatalf("AssembleOverlay %s: %v", overlay, err)
				}
				// Installed in order: each overlay's directory is newer than
				// the base and than the overlays before it.
				touch(t, filepath.Join(root, overlay), time.Duration(i+1)*time.Hour)
				if err := controller.DeployOver(context.Background(), overlay, "base"); err != nil {
					t.Fatalf("DeployOver %s: %v", overlay, err)
				}
				target, err := controller.Rollback(context.Background(), tc.req(overlay))
				if err != nil {
					t.Fatalf("Rollback of %s: %v", overlay, err)
				}
				if target != base {
					t.Fatalf("Rollback of %s restored %q, want the base %q", overlay, target, base)
				}
				if got, _ := readCurrentTarget(current); got != base {
					t.Fatalf("current = %q after rolling back %s, want the base", got, overlay)
				}
			}
			// Both rolled back: nothing records a release before the base.
			if _, err := controller.Rollback(context.Background(), RollbackRequest{}); err == nil {
				t.Fatal("a third rollback restored a rolled-back overlay")
			}
		})
	}
}

// TestRollbackRefusesWhenAnotherReleaseReplacedTheExpectedOne: a gate that
// failed on one release must not roll back a release deployed since.
func TestRollbackRefusesWhenAnotherReleaseReplacedTheExpectedOne(t *testing.T) {
	root := t.TempDir()
	base := makeRelease(t, root, "v1")
	makeRelease(t, root, "v2")
	makeRelease(t, root, "v3")
	current := filepath.Join(root, "current")
	if err := os.Symlink(base, current); err != nil {
		t.Fatal(err)
	}
	controller := newController(t, root, current, &fakeRuntime{})
	for _, id := range []string{"v2", "v3"} {
		if err := controller.Deploy(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &fakeRuntime{}
	controller = newController(t, root, current, runtime)
	_, err := controller.Rollback(context.Background(), RollbackRequest{Target: "v1", ExpectedCurrent: "v2"})
	if err == nil || !strings.Contains(err.Error(), `expected release "v2" to be active, but the active release is "v3"`) {
		t.Fatalf("Rollback error = %v, want the compare-and-swap refusal", err)
	}
	if len(runtime.calls) != 0 {
		t.Fatalf("runtime was touched on a refused rollback: %v", runtime.calls)
	}
}

// TestRollbackRerunIsConverged: a CD rerun after the rollback finished, or
// after it published the pointer and was cancelled, finds the target active.
// It restarts only daemons still on another release, and never rolls back
// again.
func TestRollbackRerunIsConverged(t *testing.T) {
	for _, tc := range []struct {
		name      string
		running   bool
		wantCalls []string
	}{
		{"daemons_on_target", true, []string{"running"}},
		{"daemons_on_rolled_back_release", false, []string{"running", "restart", "healthy"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			base := makeRelease(t, root, "v1")
			makeRelease(t, root, "v2")
			current := filepath.Join(root, "current")
			if err := os.Symlink(base, current); err != nil {
				t.Fatal(err)
			}
			controller := newController(t, root, current, &fakeRuntime{})
			if err := controller.Deploy(context.Background(), "v2"); err != nil {
				t.Fatal(err)
			}
			req := RollbackRequest{Target: "v1", ExpectedCurrent: "v2"}
			if _, err := controller.Rollback(context.Background(), req); err != nil {
				t.Fatal(err)
			}

			runtime := &runnerRuntime{running: tc.running}
			controller = newController(t, root, current, runtime)
			target, err := controller.Rollback(context.Background(), req)
			if err != nil || target != base {
				t.Fatalf("rerun Rollback = %q, %v; want %q", target, err, base)
			}
			if !slices.Equal(runtime.calls, tc.wantCalls) {
				t.Fatalf("runtime calls = %v; want %v", runtime.calls, tc.wantCalls)
			}
			if runtime.root != base {
				t.Fatalf("RunningFrom root = %q; want %q", runtime.root, base)
			}
		})
	}
}

// TestDeployUnwindKeepsTheHistoryOnThePreviousRelease: a deploy that rolled
// itself back must not stay recorded, or the next rollback would treat the
// release that was serving as the one to undo.
func TestDeployUnwindKeepsTheHistoryOnThePreviousRelease(t *testing.T) {
	root := t.TempDir()
	v1 := makeRelease(t, root, "v1")
	v2 := makeRelease(t, root, "v2")
	makeRelease(t, root, "v3")
	current := filepath.Join(root, "current")
	if err := os.Symlink(v1, current); err != nil {
		t.Fatal(err)
	}
	controller := newController(t, root, current, &fakeRuntime{})
	if err := controller.Deploy(context.Background(), "v2"); err != nil {
		t.Fatal(err)
	}
	failing := newController(t, root, current, &fakeRuntime{healthyErr: errors.New("apid not ready")})
	if err := failing.Deploy(context.Background(), "v3"); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("Deploy v3 = %v, want it to roll itself back", err)
	}
	if got, _ := readCurrentTarget(current); got != v2 {
		t.Fatalf("current = %q after the unwind, want %q", got, v2)
	}
	target, err := controller.Rollback(context.Background(), RollbackRequest{})
	if err != nil || target != v1 {
		t.Fatalf("Rollback = %q, %v; want the release v2 replaced, %q", target, err, v1)
	}
}

// TestRollbackRefusesAHistoryAheadOfThePointer: an activation that crashed
// after recording itself but before publishing the pointer leaves the
// history naming a release that is not active. That history says nothing
// about what preceded the active release; the next deploy repairs it.
func TestRollbackRefusesAHistoryAheadOfThePointer(t *testing.T) {
	root := t.TempDir()
	v1 := makeRelease(t, root, "v1")
	makeRelease(t, root, "v2")
	current := filepath.Join(root, "current")
	if err := os.Symlink(v1, current); err != nil {
		t.Fatal(err)
	}
	if err := writeHistory(root, history{Releases: []string{"v1", "v2"}}); err != nil {
		t.Fatal(err)
	}
	controller := newController(t, root, current, &fakeRuntime{})
	if _, err := controller.Rollback(context.Background(), RollbackRequest{}); err == nil {
		t.Fatal("Rollback trusted a history whose last release is not active")
	}
	if err := controller.Deploy(context.Background(), "v2"); err != nil {
		t.Fatal(err)
	}
	h, err := readHistory(root)
	if err != nil || !slices.Equal(h.Releases, []string{"v1", "v2"}) {
		t.Fatalf("history after the repairing deploy = %v, %v; want [v1 v2]", h.Releases, err)
	}
}

// TestDeployRetainsTheRollbackTargetPastTheRetentionWindow: a full release
// deployed over a base that several newer, rolled-back overlays outnumber in
// the retention window must not prune that base, or its rollback has nothing
// to restore.
func TestDeployRetainsTheRollbackTargetPastTheRetentionWindow(t *testing.T) {
	root := t.TempDir()
	base := makeRelease(t, root, strings.Repeat("a", 40))
	current := filepath.Join(root, "current")
	if err := os.Symlink(base, current); err != nil {
		t.Fatal(err)
	}
	controller := newController(t, root, current, &fakeRuntime{})
	// Retention keeps three inactive releases by directory time; four
	// rolled-back releases are all newer than the base.
	for i, digit := range []string{"1", "2", "3", "4"} {
		id := strings.Repeat(digit, 40)
		touch(t, makeRelease(t, root, id), time.Duration(i+1)*time.Minute)
		if err := controller.Deploy(context.Background(), id); err != nil {
			t.Fatalf("Deploy %s: %v", id, err)
		}
		if _, err := controller.Rollback(context.Background(), RollbackRequest{}); err != nil {
			t.Fatalf("Rollback of %s: %v", id, err)
		}
	}
	full := strings.Repeat("f", 40)
	touch(t, makeRelease(t, root, full), time.Hour)
	if err := controller.Deploy(context.Background(), full); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(base); err != nil {
		t.Fatalf("retention pruned the release the active one replaced: %v", err)
	}
	target, err := controller.Rollback(context.Background(), RollbackRequest{})
	if err != nil || target != base {
		t.Fatalf("Rollback = %q, %v; want the base %q", target, err, base)
	}
}

func TestHistoryUnwindsAndBounds(t *testing.T) {
	var h history
	h = h.activated("v1", "v2")
	h = h.activated("v2", "v3")
	if id, ok := h.predecessor("v3"); !ok || id != "v2" {
		t.Fatalf("predecessor(v3) = %q, %t", id, ok)
	}
	if _, ok := h.predecessor("v2"); ok {
		t.Fatal("a history ending at v3 named a predecessor for v2")
	}
	h = h.unwindTo("v1")
	if !slices.Equal(h.Releases, []string{"v1"}) {
		t.Fatalf("unwindTo(v1) = %v", h.Releases)
	}
	if got := h.unwindTo("elsewhere").Releases; !slices.Equal(got, []string{"elsewhere"}) {
		t.Fatalf("unwindTo an unrecorded release = %v, want a history that starts from it", got)
	}
	for i := 0; i < 2*historyLimit; i++ {
		h = h.activated(h.Releases[len(h.Releases)-1], strings.Repeat("x", i+1))
	}
	if len(h.Releases) != historyLimit {
		t.Fatalf("history grew to %d entries, want %d", len(h.Releases), historyLimit)
	}
}

func TestReadHistoryRefusesAPathOutsideTheReleasesRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(historyPath(root), []byte(`{"releases":["v1","../etc"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readHistory(root); err == nil {
		t.Fatal("readHistory accepted a release id that leaves the releases root")
	}
}
