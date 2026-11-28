package apidserver_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The in-progress budget of a branch copy runs from when the copy or binding
// was made, not from the branch's creation: prod 2026-11-28 a PR branch's
// empty database could not start for two hours (stale usage), and each check
// of the copy it finally started spent an attempt and waited 10 minutes.
func TestEnvironmentBranchDataInProgressBudgetStartsWithTheCopy(t *testing.T) {
	created := time.Date(2026, 11, 28, 8, 37, 44, 0, time.UTC)
	branch := state.ProjectEnvironmentBranch{CreatedAt: created}
	inProgress := fmt.Errorf("set up the empty database: %w", apidserver.ErrEnvironmentBranchDataInProgress)

	if got := apidserver.EnvironmentBranchDataInProgressFrom(branch, inProgress); !got.Equal(created) {
		t.Fatalf("budget without a copy time starts at %v; want the branch's creation %v", got, created)
	}
	copyAt := created.Add(110 * time.Minute)
	started := apidserver.CopyStarted(copyAt, inProgress)
	if got := apidserver.EnvironmentBranchDataInProgressFrom(branch, started); !got.Equal(copyAt) {
		t.Fatalf("budget starts at %v; want the copy's creation %v", got, copyAt)
	}
	if !apidserver.EnvironmentBranchDataInProgress(started) || !errors.Is(started, apidserver.ErrEnvironmentBranchDataInProgress) {
		t.Fatalf("the copy time hid the in-progress error: %v", started)
	}
	if started.Error() != inProgress.Error() {
		t.Fatalf("message = %q; want %q", started.Error(), inProgress.Error())
	}

	// A new binding of an older copy starts its own budget, whichever way the
	// times are wrapped.
	bindingAt := copyAt.Add(time.Minute)
	for name, err := range map[string]error{
		"binding inside":  apidserver.CopyStarted(copyAt, apidserver.CopyStarted(bindingAt, inProgress)),
		"binding outside": apidserver.CopyStarted(bindingAt, apidserver.CopyStarted(copyAt, inProgress)),
	} {
		if got := apidserver.EnvironmentBranchDataInProgressFrom(branch, err); !got.Equal(bindingAt) {
			t.Fatalf("%s: budget starts at %v; want the binding's creation %v", name, got, bindingAt)
		}
	}

	// A copy made before the branch (adopted across a reset) counts from the
	// branch, and nothing is recorded without a time or an error.
	if got := apidserver.EnvironmentBranchDataInProgressFrom(branch, apidserver.CopyStarted(created.Add(-time.Hour), inProgress)); !got.Equal(created) {
		t.Fatalf("an older copy's budget starts at %v; want the branch's creation %v", got, created)
	}
	if err := apidserver.CopyStarted(time.Time{}, inProgress); err != inProgress {
		t.Fatalf("CopyStarted without a time = %v; want the error unchanged", err)
	}
	if err := apidserver.CopyStarted(copyAt, nil); err != nil {
		t.Fatalf("CopyStarted(nil) = %v", err)
	}
}
