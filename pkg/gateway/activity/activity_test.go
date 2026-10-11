// adr: 696
package activity

import (
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func newTracker(t *testing.T) *Tracker {
	t.Helper()
	tracker, err := New(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return tracker
}

func TestActivityObservationDistinguishesDeploymentsAndInvalidatesZero(t *testing.T) {
	tracker := newTracker(t)
	app, old, candidate := uuid.NewString(), uuid.NewString(), uuid.NewString()
	before := tracker.Observe(app, old)
	finishOld, finishCandidate := tracker.Begin(app, old), tracker.Begin(app, candidate)
	if got := tracker.Observe(app, old); !got.CoverageKnown || got.ActiveForwards != 1 || got.TotalActiveForwards != 2 || got.ActivityVersion <= before.ActivityVersion {
		t.Fatalf("busy predecessor = %+v", got)
	}
	finishOld()
	after := tracker.Observe(app, old)
	if !after.CoverageKnown || after.ActiveForwards != 0 || after.TotalActiveForwards != 1 || after.ActivityVersion <= before.ActivityVersion || after.SessionID != before.SessionID {
		t.Fatalf("new zero = %+v, initial = %+v", after, before)
	}
	finishOld()
	if got := tracker.Observe(app, old); got != after {
		t.Fatalf("duplicate completion changed observation: %+v", got)
	}
	finishCandidate()
	if tracker.total != 0 || len(tracker.active) != 0 {
		t.Fatal("idle keys or count retained")
	}
	finishReturnedTraffic := tracker.Begin(app, old)
	finishReturnedTraffic()
	if got := tracker.Observe(app, old); got.ActivityVersion <= after.ActivityVersion || got.ActiveForwards != 0 {
		t.Fatalf("returned traffic reused a zero observation: %+v", got)
	}
}

func TestActivityMissingIdentityPermanentlyMakesCoverageUnknown(t *testing.T) {
	app, deployment := uuid.NewString(), uuid.NewString()
	for _, invalid := range []string{"", "opaque-id", uuid.Nil.String(), strings.ToUpper("f85a0efb-406f-40fb-b385-24dfd47d6fff"), " " + deployment} {
		for _, missingApp := range []bool{false, true} {
			tracker := newTracker(t)
			a, d := app, invalid
			if missingApp {
				a, d = invalid, deployment
			}
			finish := tracker.Begin(a, d)
			if got := tracker.Observe(app, deployment); got.CoverageKnown {
				t.Fatalf("unidentified start claimed coverage: %+v", got)
			}
			finish()
			tracker.Begin(app, deployment)()
			if got := tracker.Observe(app, deployment); got.CoverageKnown || got.CoverageUnknownReason != MissingIdentity || got.ActiveForwards != 0 || got.ActivityVersion != 5 {
				t.Fatalf("completion repaired missing identity: %+v", got)
			}
		}
	}
}

func TestActivityKeyCapacityIsBoundedAndCannotRecoverCoverage(t *testing.T) {
	tracker := newTracker(t)
	app := uuid.NewString()
	var completions []func()
	for range api.RuntimeUpgradeActivityKeyLimit {
		completions = append(completions, tracker.Begin(app, uuid.NewString()))
	}
	finish := tracker.Begin(app, uuid.NewString())
	if len(tracker.active) != api.RuntimeUpgradeActivityKeyLimit || tracker.known || tracker.unknownReason != KeyCapacityExceeded {
		t.Fatal("key overflow grew state or claimed coverage")
	}
	finish()
	for _, complete := range completions {
		complete()
	}
	tracker.Begin(app, uuid.NewString())()
	if tracker.total != 0 || len(tracker.active) != 0 || tracker.known {
		t.Fatal("capacity recovery repaired lost coverage")
	}
}

func TestActivityForwardCapacityDoesNotOverflowCounts(t *testing.T) {
	tracker := newTracker(t)
	app, deployment := uuid.NewString(), uuid.NewString()
	var completions []func()
	for range api.RuntimeUpgradeActivityForwardLimit {
		completions = append(completions, tracker.Begin(app, deployment))
	}
	finish := tracker.Begin(app, deployment)
	if got := tracker.Observe(app, deployment); got.CoverageKnown || got.CoverageUnknownReason != ForwardCapacityExceeded || got.ActiveForwards != api.RuntimeUpgradeActivityForwardLimit {
		t.Fatalf("forward overflow = %+v", got)
	}
	finish()
	for _, complete := range completions {
		complete()
	}
	if got := tracker.Observe(app, deployment); got.CoverageKnown || got.ActiveForwards != 0 || got.TotalActiveForwards != 0 {
		t.Fatalf("overflow completions = %+v", got)
	}
}

func TestActivityIdleChurnDoesNotConsumeKeyCapacity(t *testing.T) {
	tracker := newTracker(t)
	app, deployment := uuid.NewString(), uuid.NewString()
	for range api.RuntimeUpgradeActivityKeyLimit * 2 {
		tracker.Begin(app, uuid.NewString())()
	}
	if got := tracker.Observe(app, deployment); !got.CoverageKnown || got.TotalActiveForwards != 0 || len(tracker.active) != 0 {
		t.Fatalf("idle churn = %+v, keys = %d", got, len(tracker.active))
	}
}

func TestActivityConcurrentStartsAndDuplicateCompletions(t *testing.T) {
	tracker := newTracker(t)
	app, deployment := uuid.NewString(), uuid.NewString()
	var wg sync.WaitGroup
	for range 256 {
		wg.Go(func() {
			complete := tracker.Begin(app, deployment)
			_ = tracker.Observe(app, deployment)
			var duplicate sync.WaitGroup
			duplicate.Go(complete)
			duplicate.Go(complete)
			duplicate.Wait()
		})
	}
	wg.Wait()
	if got := tracker.Observe(app, deployment); !got.CoverageKnown || got.ActiveForwards != 0 || got.TotalActiveForwards != 0 || got.ActivityVersion != 513 {
		t.Fatalf("concurrent activity = %+v", got)
	}
}

func TestActivitySessionRestartAndInvalidReads(t *testing.T) {
	app, deployment := uuid.NewString(), uuid.NewString()
	tracker, restarted := newTracker(t), newTracker(t)
	if tracker.Observe(app, deployment).SessionID == restarted.Observe(app, deployment).SessionID {
		t.Fatal("restart reused process identity")
	}
	if got := tracker.Observe("", deployment); got.CoverageKnown || got.CoverageUnknownReason != MissingIdentity || !tracker.Observe(app, deployment).CoverageKnown {
		t.Fatal("invalid read claimed coverage or poisoned a valid read")
	}
	var absent *Tracker
	var uninitialized Tracker
	uninitialized.Begin(app, deployment)()
	if got, zero := absent.Observe(app, deployment), uninitialized.Observe(app, deployment); got.CoverageKnown || zero.CoverageKnown || got.CoverageUnknownReason != TrackingDisabled || zero.CoverageUnknownReason != TrackingUninitialized {
		t.Fatal("disabled or uninitialized tracker claimed coverage")
	}
	for _, invalid := range []string{"", uuid.Nil.String(), "session"} {
		if _, err := New(invalid); err == nil {
			t.Fatalf("invalid session accepted: %q", invalid)
		}
	}
}

func TestActivityVersionExhaustionNeverReusesEarlierEvidence(t *testing.T) {
	tracker := newTracker(t)
	app, deployment := uuid.NewString(), uuid.NewString()
	tracker.version = math.MaxUint64
	tracker.Begin(app, deployment)()
	if got := tracker.Observe(app, deployment); got.CoverageKnown || got.CoverageUnknownReason != VersionExhausted || got.ActivityVersion != math.MaxUint64 {
		t.Fatalf("version exhaustion wrapped: %+v", got)
	}
}
