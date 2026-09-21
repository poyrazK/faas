package deploycontroller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The Rollback verb exists for the window Deploy cannot see: the CD
// pipeline's post-activation gates — liveness, the public customer path,
// metering convergence — run after Deploy has already returned success. Until
// this verb, a gate failure left the bad release serving and the only
// documented remedy was to rerun CD.

func TestRollbackActivatesTheNewestRetainedRelease(t *testing.T) {
	root := t.TempDir()
	older := makeRelease(t, root, "v1")
	// Retention keeps several; the newest verified one that is not active wins.
	middle := makeRelease(t, root, "v2")
	active := makeRelease(t, root, "v3")
	touch(t, older, -2*time.Hour)
	touch(t, middle, -time.Hour)

	current := filepath.Join(root, "current")
	if err := os.Symlink(active, current); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	controller := newController(t, root, current, runtime)

	target, err := controller.Rollback(context.Background())
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if target != middle {
		t.Fatalf("rolled back to %q, want the newest non-active release %q", target, middle)
	}

	// The full activation sequence must run — a rollback that only moves the
	// pointer leaves the old binaries unstarted.
	want := []string{"activate:" + middle, "restart", "healthy"}
	if strings.Join(runtime.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", runtime.calls, want)
	}

	// The published pointer must actually move, or the next deploy computes
	// its rollback target from a stale current.
	resolved, err := readCurrentTarget(current)
	if err != nil {
		t.Fatalf("readCurrentTarget: %v", err)
	}
	if resolved != middle {
		t.Fatalf("current points at %q, want %q", resolved, middle)
	}
}

func TestRollbackRefusesWhenNothingIsActive(t *testing.T) {
	root := t.TempDir()
	makeRelease(t, root, "v1")
	controller := newController(t, root, filepath.Join(root, "current"), &fakeRuntime{})

	if _, err := controller.Rollback(context.Background()); err == nil {
		t.Fatal("Rollback succeeded with no active release")
	} else if !strings.Contains(err.Error(), "no release is active") {
		t.Fatalf("error = %v, want it to name the missing active release", err)
	}
}

func TestRollbackRefusesWhenNoOtherReleaseIsRetained(t *testing.T) {
	root := t.TempDir()
	only := makeRelease(t, root, "v1")
	current := filepath.Join(root, "current")
	if err := os.Symlink(only, current); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	controller := newController(t, root, current, runtime)

	if _, err := controller.Rollback(context.Background()); err == nil {
		t.Fatal("Rollback succeeded with only the active release retained")
	} else if !strings.Contains(err.Error(), "no verified retained release") {
		t.Fatalf("error = %v, want it to name the missing rollback target", err)
	}
	if len(runtime.calls) != 0 {
		t.Fatalf("runtime was touched on a refused rollback: %v", runtime.calls)
	}
}

// TestRollbackRefusesADriftedTarget is the one that matters for trust: the
// newest retained directory is not activated merely because it is newest. A
// tampered or half-written release must be refused, not shipped as a recovery.
func TestRollbackRefusesADriftedTarget(t *testing.T) {
	root := t.TempDir()
	drifted := makeRelease(t, root, "v1")
	active := makeRelease(t, root, "v2")
	if err := os.WriteFile(filepath.Join(drifted, "bin", "apid"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}

	current := filepath.Join(root, "current")
	if err := os.Symlink(active, current); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	controller := newController(t, root, current, runtime)

	if _, err := controller.Rollback(context.Background()); err == nil {
		t.Fatal("Rollback activated a release whose contents no longer match its manifest")
	}
	for _, call := range runtime.calls {
		if strings.HasPrefix(call, "activate:") {
			t.Fatalf("drifted release was activated: %v", runtime.calls)
		}
	}
}

// TestRollbackReportsAnUnhealthyTarget pins that a rollback which does not
// come up healthy is an error, not a silent success. An operator reading the
// CD log must not be told the fleet recovered when it did not.
func TestRollbackReportsAnUnhealthyTarget(t *testing.T) {
	root := t.TempDir()
	previous := makeRelease(t, root, "v1")
	active := makeRelease(t, root, "v2")
	touch(t, previous, -time.Hour)

	current := filepath.Join(root, "current")
	if err := os.Symlink(active, current); err != nil {
		t.Fatal(err)
	}
	// healthyErr applies to the first Healthy call, which here is the rollback's.
	runtime := &fakeRuntime{healthyErr: errors.New("gateway not ready")}
	controller := newController(t, root, current, runtime)

	if _, err := controller.Rollback(context.Background()); err == nil {
		t.Fatal("Rollback reported success while the target was unhealthy")
	} else if !strings.Contains(err.Error(), "gateway not ready") {
		t.Fatalf("error = %v, want the underlying health failure preserved", err)
	}
}

// TestRollbackIsSerializedWithDeploy pins that the two take the same lock. A
// rollback interleaving with a deploy would race the current pointer.
func TestRollbackIsSerializedWithDeploy(t *testing.T) {
	root := t.TempDir()
	previous := makeRelease(t, root, "v1")
	active := makeRelease(t, root, "v2")
	touch(t, previous, -time.Hour)

	current := filepath.Join(root, "current")
	if err := os.Symlink(active, current); err != nil {
		t.Fatal(err)
	}
	controller := newController(t, root, current, &fakeRuntime{})

	held, err := acquireLock(filepath.Join(root, "deploy.lock"))
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	defer func() { _ = held.Close() }()

	if _, err := controller.Rollback(context.Background()); err == nil {
		t.Fatal("Rollback proceeded while another deployment held the lock")
	} else if !strings.Contains(err.Error(), "another deployment is active") {
		t.Fatalf("error = %v, want the deploy-lock contention message", err)
	}
}

// touch back-dates a release directory so newestVerifiedRollback's ordering is
// deterministic rather than dependent on filesystem timestamp resolution.
func touch(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(age)
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatal(err)
	}
}
