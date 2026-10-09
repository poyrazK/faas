package profiling

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Run against a private, tenant-enabled Pyroscope (see docs/profiling.md).
// A real backend qualifies the profile type ID and Connect protobuf contract.
func TestCPUProfilePyroscopeIntegration(t *testing.T) {
	endpoint := os.Getenv("GREGALE_PROFILE_TEST_BACKEND")
	if endpoint == "" {
		t.Skip("requires private Pyroscope")
	}
	backend, err := NewPyroscope(endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	now := time.Now().Add(-5 * time.Second)
	principal := principalFixture(now)
	if err := backend.Push(ctx, principal, cpuFixture(now, 1e8)); err != nil {
		t.Fatal(err)
	}
	candidate := principal
	candidate.DeploymentID = uuid.NewString()
	if err := backend.Push(ctx, candidate, cpuFixture(now, 3e8)); err != nil {
		t.Fatal(err)
	}
	foreign := principal
	foreign.AccountID = uuid.NewString()
	if err := backend.Push(ctx, foreign, cpuFixture(now, 9e8)); err != nil {
		t.Fatal(err)
	}
	q := api.ProfileQuery{DeploymentID: principal.DeploymentID, Runtime: principal.Runtime, Start: now.Add(-time.Second), End: time.Now()}
	a, err := backend.Query(ctx, principal.AccountID, principal.AppID, principal.Scope, q)
	if err != nil {
		t.Fatal(err)
	}
	av, err := View(a, q)
	if err != nil || math.Abs(av.CPUSeconds-.1) > 1e-8 {
		t.Fatalf("backend baseline = %+v, err %v", av, err)
	}
	q.DeploymentID = candidate.DeploymentID
	b, err := backend.Query(ctx, principal.AccountID, principal.AppID, principal.Scope, q)
	if err != nil {
		t.Fatal(err)
	}
	bv, err := View(b, q)
	if err != nil || math.Abs(bv.CPUSeconds-.3) > 1e-8 {
		t.Fatalf("backend candidate = %+v, err %v", bv, err)
	}
	if !Compare(av, bv).Comparable {
		t.Fatal("real profiles not comparable")
	}

	// Identical captures from fork workers must add together; retransmitting
	// either worker's capture must not add CPU again.
	workers := principal
	workers.DeploymentID = uuid.NewString()
	service := NewService(backend, nil)
	upload := Envelope{Principal: workers, Upload: Upload{Profile: encoded(t, cpuFixture(now, 1e8))}}
	for _, pid := range []string{"42", "43", "43"} {
		upload.Upload.ProcessID = pid
		if err := service.Push(ctx, upload); err != nil {
			t.Fatal(err)
		}
	}
	q.DeploymentID = workers.DeploymentID
	merged, err := backend.Query(ctx, workers.AccountID, workers.AppID, workers.Scope, q)
	if err != nil {
		t.Fatal(err)
	}
	view, err := View(merged, q)
	if err != nil || math.Abs(view.CPUSeconds-.2) > 1e-8 {
		t.Fatalf("fork worker CPU = %+v, err %v", view, err)
	}
	coverage, err := backend.QueryCoverage(ctx, workers.AccountID, workers.AppID, workers.Scope, q)
	if err != nil || !coverage.Available || coverage.ReceivedProfiles != 2 || coverage.ContributingCollectors != 2 || coverage.CoveredSeconds != 1 || coverage.LastReceivedAt == nil {
		t.Fatalf("fork worker coverage = %+v, err %v", coverage, err)
	}
	if err := backend.RecordFailure(ctx, workers, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	coverage, err = backend.QueryCoverage(ctx, workers.AccountID, workers.AppID, workers.Scope, q)
	if err != nil || coverage.RecordedFailedUploads != 1 || coverage.FailuresComplete {
		t.Fatal(coverage, err)
	}
	isolated, err := backend.QueryCoverage(ctx, foreign.AccountID, workers.AppID, workers.Scope, q)
	if err != nil || isolated.ReceivedProfiles != 0 || isolated.RecordedFailedUploads != 0 {
		t.Fatal("coverage crossed tenants", isolated, err)
	}
}
