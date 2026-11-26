// adr: 20261031193635
package apidserver_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// A copy or binding still being made is not a failed attempt; every other
// error is, and keeps its managed PostgreSQL meaning.
func TestEnvironmentBranchDataInProgress(t *testing.T) {
	copying := fmt.Errorf("isolated PostgreSQL copy is not ready: %w%w", apidserver.ErrDatabaseCopyInProgress, managedpostgres.ErrConflict)
	binding := fmt.Errorf("database copy binding for workload %q is not ready: %w%w", "web", apidserver.ErrEnvironmentBranchDataInProgress, managedpostgres.ErrUnavailable)
	for _, err := range []error{copying, binding, fmt.Errorf("copy: %w", copying)} {
		if !apidserver.EnvironmentBranchDataInProgress(err) {
			t.Errorf("%v: not in progress", err)
		}
	}
	if !errors.Is(copying, managedpostgres.ErrConflict) || !errors.Is(binding, managedpostgres.ErrUnavailable) {
		t.Fatal("in-progress errors lost their managed PostgreSQL class")
	}
	for _, err := range []error{managedpostgres.ErrConflict, managedpostgres.ErrUnavailable, managedpostgres.ErrQuotaExceeded, apidserver.ErrEnvironmentBranchBucketLimit} {
		if apidserver.EnvironmentBranchDataInProgress(err) {
			t.Errorf("%v: counted as in progress", err)
		}
	}
}

// A copy in progress is checked every second at first, then less often.
func TestEnvironmentBranchDataInProgressWait(t *testing.T) {
	for _, c := range []struct{ age, want time.Duration }{
		{0, time.Second}, {29 * time.Second, time.Second}, {time.Minute, 2 * time.Second}, {5 * time.Minute, 5 * time.Second},
	} {
		if got := apidserver.EnvironmentBranchDataInProgressWait(c.age); got != c.want {
			t.Errorf("age %s: wait %s, want %s", c.age, got, c.want)
		}
	}
}

// Creating a branch with pending data wakes the reconciler without blocking,
// however many branches are created before it runs.
func TestEnvironmentBranchDataWake(t *testing.T) {
	e := apidserver.SetupSecrets(t, api.PlanPro)
	srv := *e.S()
	srv.WakeEnvironmentBranchData()
	srv.WakeEnvironmentBranchData()
	select {
	case <-srv.EnvironmentBranchDataWakeChannel():
	default:
		t.Fatal("no wake-up queued")
	}
	select {
	case <-srv.EnvironmentBranchDataWakeChannel():
		t.Fatal("wake-ups were not coalesced")
	default:
	}
}

// A restore that hands back this very copy, still provisioning, with its
// reconcile's conflict is a copy in progress. Any other row, state or error
// stays a failed attempt.
func TestEnvironmentBranchDataCopyRestoreConflict(t *testing.T) {
	point := time.Date(2026, 11, 25, 19, 39, 41, 0, time.UTC)
	source := managedpostgres.Database{ID: "src", ProviderResourceID: "br-src", DataResourceID: "br-src", BackendID: "neon", BackendFingerprint: "fp", State: managedpostgres.StateReady}
	source.Spec.RestoreWindowSeconds = 604800
	copy := managedpostgres.Database{ID: "copy", RestoreSourceDatabaseID: "src", RestoreSourceResourceID: "br-src", RestorePointInTime: point,
		Spec: source.Spec, BackendID: "neon", BackendFingerprint: "fp", State: managedpostgres.StateProvisioning}
	conflict := fmt.Errorf("reconcile: %w", managedpostgres.ErrConflict)
	if err := apidserver.DatabaseCopyRestoreError(copy, source, point, conflict); !apidserver.EnvironmentBranchDataInProgress(err) || !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("provisioning copy with a conflict = %v; want in progress", err)
	}
	failed := copy
	failed.State = managedpostgres.StateFailed
	otherPoint := copy
	otherPoint.RestorePointInTime = point.Add(time.Second)
	for name, c := range map[string]struct {
		cloned managedpostgres.Database
		err    error
	}{
		"no row":         {managedpostgres.Database{}, conflict},
		"failed copy":    {failed, conflict},
		"other point":    {otherPoint, conflict},
		"the source":     {source, conflict},
		"invalid":        {copy, managedpostgres.ErrInvalid},
		"unavailable":    {copy, managedpostgres.ErrUnavailable},
		"quota exceeded": {copy, managedpostgres.ErrQuotaExceeded},
	} {
		if err := apidserver.DatabaseCopyRestoreError(c.cloned, source, point, c.err); apidserver.EnvironmentBranchDataInProgress(err) || !errors.Is(err, c.err) {
			t.Errorf("%s: %v counted as in progress or lost its class", name, err)
		}
	}
}
