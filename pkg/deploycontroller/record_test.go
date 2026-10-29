package deploycontroller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordingRuntime is a fakeRuntime that keeps the release history.
type recordingRuntime struct {
	*fakeRuntime
	started  []Activation
	finished []finishedActivation
}

type finishedActivation struct {
	id             string
	activation     Activation
	status, detail string
}

func (r *recordingRuntime) StartActivation(_ context.Context, a Activation) string {
	r.started = append(r.started, a)
	return fmt.Sprintf("id-%d", len(r.started))
}

func (r *recordingRuntime) FinishActivation(_ context.Context, id string, a Activation, status, detail string) {
	r.finished = append(r.finished, finishedActivation{id: id, activation: a, status: status, detail: detail})
}

// last returns the only activation recorded since the previous call.
func (r *recordingRuntime) last(t *testing.T) finishedActivation {
	t.Helper()
	if len(r.started) != 1 || len(r.finished) != 1 {
		t.Fatalf("recorded %d starts and %d finishes, want one of each", len(r.started), len(r.finished))
	}
	got := r.finished[0]
	if got.id != "id-1" || got.activation != r.started[0] {
		t.Fatalf("finished %+v, want the started activation %+v as id-1", got, r.started[0])
	}
	if got.activation.StartedAt.IsZero() {
		t.Fatal("activation has no start time")
	}
	r.started, r.finished = nil, nil
	return got
}

func TestDeployRecordsItsOutcome(t *testing.T) {
	cause := errors.New("exit status 1")
	for _, tc := range []struct {
		name     string
		previous bool
		runtime  fakeRuntime
		status   string
	}{
		{name: "healthy", previous: true, status: ActivationSucceeded},
		{name: "unwound after failed health", previous: true, runtime: fakeRuntime{healthyErr: cause}, status: ActivationRolledBack},
		{name: "restart failed and so did the unwind", previous: true, runtime: fakeRuntime{restartErr: cause}, status: ActivationFailed},
		{name: "migration failed before any change", previous: true, runtime: fakeRuntime{migrateErr: cause}, status: ActivationFailed},
		{name: "first activation has nothing to unwind to", runtime: fakeRuntime{restartErr: cause}, status: ActivationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			newRelease := makeRelease(t, root, "new")
			current := filepath.Join(root, "current")
			if tc.previous {
				if err := os.Symlink(makeRelease(t, root, "old"), current); err != nil {
					t.Fatal(err)
				}
			}
			fake := tc.runtime
			runtime := &recordingRuntime{fakeRuntime: &fake}
			err := newController(t, root, current, runtime).Deploy(context.Background(), "new")
			if (err == nil) != (tc.status == ActivationSucceeded) {
				t.Fatalf("Deploy = %v, want an error only when it did not succeed", err)
			}

			got := runtime.last(t)
			want := Activation{Kind: ActivationDeploy, ReleaseID: "new", ReleaseRoot: newRelease, CommitSHA: "commit-new", StartedAt: got.activation.StartedAt}
			if tc.previous {
				want.Previous = "old"
			}
			if got.activation != want {
				t.Fatalf("activation = %+v, want %+v", got.activation, want)
			}
			if got.status != tc.status {
				t.Fatalf("status = %q, want %q (detail %q)", got.status, tc.status, got.detail)
			}
			if (got.detail == "") != (err == nil) || (err != nil && got.detail != err.Error()) {
				t.Fatalf("detail = %q, want the deploy error %v", got.detail, err)
			}
		})
	}
}

func TestRollbackRecordsTheReleaseItRestores(t *testing.T) {
	root := t.TempDir()
	oldRelease := makeRelease(t, root, "old")
	makeRelease(t, root, "new")
	current := filepath.Join(root, "current")
	runtime := &recordingRuntime{fakeRuntime: &fakeRuntime{}}
	controller := newController(t, root, current, runtime)
	for _, id := range []string{"old", "new"} {
		if err := controller.Deploy(context.Background(), id); err != nil {
			t.Fatalf("Deploy %s: %v", id, err)
		}
	}
	runtime.started, runtime.finished = nil, nil

	if _, err := controller.Rollback(context.Background(), RollbackRequest{}); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	got := runtime.last(t)
	want := Activation{Kind: ActivationRollback, ReleaseID: "old", ReleaseRoot: oldRelease, CommitSHA: "commit-old", Previous: "new", StartedAt: got.activation.StartedAt}
	if got.activation != want || got.status != ActivationSucceeded {
		t.Fatalf("recorded %+v as %q, want %+v as succeeded", got.activation, got.status, want)
	}

	// A rerun finds the target active: it converges and records nothing.
	if _, err := controller.Rollback(context.Background(), RollbackRequest{Target: "old"}); err != nil {
		t.Fatalf("Rollback rerun: %v", err)
	}
	if len(runtime.started) != 0 {
		t.Fatalf("converged rollback recorded %+v", runtime.started)
	}
}

func TestFailedRollbackIsRecorded(t *testing.T) {
	root := t.TempDir()
	makeRelease(t, root, "old")
	newRelease := makeRelease(t, root, "new")
	current := filepath.Join(root, "current")
	if err := os.Symlink(newRelease, current); err != nil {
		t.Fatal(err)
	}
	runtime := &recordingRuntime{fakeRuntime: &fakeRuntime{activateErr: errors.New("unit install failed")}}
	_, err := newController(t, root, current, runtime).Rollback(context.Background(), RollbackRequest{Target: "old"})
	if err == nil {
		t.Fatal("Rollback succeeded, want the activation error")
	}
	got := runtime.last(t)
	if got.activation.Kind != ActivationRollback || got.activation.Previous != "new" || got.status != ActivationFailed || !strings.Contains(got.detail, "unit install failed") {
		t.Fatalf("recorded %+v as %q (%q), want a failed rollback from new", got.activation, got.status, got.detail)
	}
}

func TestConvergedDeployRecordsNothing(t *testing.T) {
	root := t.TempDir()
	release := makeRelease(t, root, "current")
	current := filepath.Join(root, "active")
	if err := os.Symlink(release, current); err != nil {
		t.Fatal(err)
	}
	runtime := &recordingRuntime{fakeRuntime: &fakeRuntime{}}
	if err := newController(t, root, current, runtime).Deploy(context.Background(), "current"); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if len(runtime.started) != 0 || len(runtime.finished) != 0 {
		t.Fatalf("recorded %+v / %+v for a release that was already active", runtime.started, runtime.finished)
	}
}
