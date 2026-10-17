// adr: 570
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

type capturedRecoveryCall struct {
	app, deployment, scope, trigger string
	cancelled                       bool
	bounded                         bool
}

type capturedRecoveryScheduler struct {
	NoopScheduler
	calls chan capturedRecoveryCall
}

func (s *capturedRecoveryScheduler) AdmitInstance(ctx context.Context, app, deployment, scope, trigger string) (string, string, string, string, int32, bool, int, error) {
	_, bounded := ctx.Deadline()
	s.calls <- capturedRecoveryCall{app, deployment, scope, trigger, ctx.Err() != nil, bounded}
	return "replacement", "node", deployment, "replacement-wake", 0, false, 8080, nil
}

func TestPublicStaleRecoveryKeepsHealthyCapturedSibling(t *testing.T) {
	scheduler := &capturedRecoveryScheduler{calls: make(chan capturedRecoveryCall, 1)}
	b := NewPGBackend(nil, scheduler, nil).WithStore(serviceDiscoveryWeightStore{})
	stale := Target{AppID: "app", InstanceID: "failed", DeploymentID: "selected", NodeID: "node", WakeID: "old"}
	b.RecordTarget("app", stale)
	b.RecordTarget("app", Target{InstanceID: "healthy", DeploymentID: "selected", NodeID: "node"})
	b.EvictRoutedTarget(stale)
	ctx := context.WithValue(t.Context(), publicRoutingSnapshotKey{}, PublicRoutingSnapshot{AppID: "app", SelectedDeploymentID: "selected", Scope: "production"})
	b.RecoverStaleTarget(ctx, "app", "", 2)
	select {
	case call := <-scheduler.calls:
		t.Fatalf("healthy captured sibling triggered replacement: %+v", call)
	case <-time.After(200 * time.Millisecond):
	}
	if b.CapacityCount("app") != 1 || b.HealthyCount("app") != 0 || b.PickForDeployment("app", "selected").Target.InstanceID != "healthy" {
		t.Fatal("failed sibling eviction changed captured capacity or picker weights")
	}
	b.RecordTarget("app", stale)
	if b.CapacityCount("app") != 1 {
		t.Fatal("recovery resurrected quarantined failed lifetime")
	}
}

func TestPublicStaleRecoveryCannotBorrowOtherCohortAndRetainsDetachedScope(t *testing.T) {
	scheduler := &capturedRecoveryScheduler{calls: make(chan capturedRecoveryCall, 1)}
	b := NewPGBackend(nil, scheduler, nil).WithStore(serviceDiscoveryWeightStore{rows: []DeploymentWeightsRow{{ID: "other", TrafficPercent: 100}}})
	b.RecordTarget("app", Target{InstanceID: "other", DeploymentID: "other", NodeID: "node"})
	if err := b.RefreshDeploymentWeights(t.Context(), "app"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), publicRoutingSnapshotKey{}, PublicRoutingSnapshot{AppID: "app", SelectedDeploymentID: "selected", Scope: "stage"}))
	cancel()
	b.RecoverStaleTarget(ctx, "app", "production", 2)
	select {
	case call := <-scheduler.calls:
		if call.app != "app" || call.deployment != "selected" || call.scope != "stage" || call.trigger != "stale_target_recovery" || call.cancelled || !call.bounded {
			t.Fatalf("recovery crossed captured authority or lost bounded detachment: %+v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated healthy cohort blocked selected replacement")
	}
}

func TestPublicStaleRecoveryRefusesUnverifiedSnapshotAndResidentCap(t *testing.T) {
	for _, tc := range []struct {
		name, owner, deployment string
		resident                bool
	}{
		{"wrong app", "foreign", "selected", false},
		{"missing deployment", "app", "", false},
		{"withdrawn resident at cap", "app", "selected", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheduler := &capturedRecoveryScheduler{calls: make(chan capturedRecoveryCall, 1)}
			b := NewPGBackend(nil, scheduler, nil).WithStore(serviceDiscoveryWeightStore{})
			if tc.resident {
				b.RecordTarget("app", Target{InstanceID: "withdrawn", DeploymentID: "selected", NodeID: "node", RequiresReadiness: true})
			}
			ctx := context.WithValue(t.Context(), publicRoutingSnapshotKey{}, PublicRoutingSnapshot{AppID: tc.owner, SelectedDeploymentID: tc.deployment, Scope: "production"})
			b.RecoverStaleTarget(ctx, "app", "", 1)
			select {
			case call := <-scheduler.calls:
				t.Fatalf("unverified/capped recovery admitted: %+v", call)
			case <-time.After(200 * time.Millisecond):
			}
			if tc.resident && (b.CapacityCount("app") != 1 || b.PickForDeployment("app", "selected").OK) {
				t.Fatal("withdrawn resident disappeared from the scheduler cap or routed")
			}
		})
	}
}

type capturedRecoveryRouter struct{ app App }

func (r capturedRecoveryRouter) ResolveHost(context.Context, string) (App, bool, error) {
	return r.app, true, nil
}

func TestPublicStaleRecoveryHandlerFailsOverAndReplacementRejoins(t *testing.T) {
	h, fixture, _ := newTestHandler(t)
	scheduler := &capturedRecoveryScheduler{calls: make(chan capturedRecoveryCall, 1)}
	deployment := uuid.NewString()
	app, _ := fixture.Lookup(t.Context(), fixture.host)
	b := NewPGBackend(capturedRecoveryRouter{app}, scheduler, nil).WithStore(serviceDiscoveryWeightStore{})
	stale := Target{AppID: fixture.app.ID, InstanceID: "failed", DeploymentID: deployment, NodeID: "node", WakeID: "old"}
	b.RecordTarget(fixture.app.ID, stale)
	b.RecordTarget(fixture.app.ID, Target{InstanceID: "healthy", DeploymentID: deployment, NodeID: "node"})
	h.backend = b
	h.WithPublicRoutingPolicy(func(_ context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
		snapshot := publicSnapshotFixture(app, inputs)
		snapshot.Weights = []DeploymentWeightsRow{{ID: deployment, TrafficPercent: 100}}
		return snapshot, nil
	}).WithForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if target.InstanceID == stale.InstanceID {
				markStaleTarget(r.Context())
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(target.InstanceID))
		})
	})
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://"+fixture.host+"/", nil))
		return w
	}
	sawFailure := false
	for range 4 {
		if request().Code == http.StatusServiceUnavailable {
			sawFailure = true
			break
		}
	}
	if !sawFailure {
		t.Fatal("round-robin never selected the failed sibling")
	}
	for range 6 {
		w := request()
		if w.Code != http.StatusOK || w.Body.String() != "healthy" {
			t.Fatalf("post-failover response=%d/%s", w.Code, w.Body)
		}
	}
	select {
	case call := <-scheduler.calls:
		t.Fatalf("handler started unnecessary replacement: %+v", call)
	case <-time.After(200 * time.Millisecond):
	}
	b.RecordTarget(fixture.app.ID, stale) // Delayed notification cannot revive this failed lifetime.
	b.RecordTarget(fixture.app.ID, Target{InstanceID: "replacement", DeploymentID: deployment, NodeID: "node", WakeID: "new"})
	seenReplacement := false
	for range 6 {
		w := request()
		if w.Code != http.StatusOK || w.Body.String() != "healthy" && w.Body.String() != "replacement" {
			t.Fatalf("replacement rejoin response=%d/%s", w.Code, w.Body)
		}
		seenReplacement = seenReplacement || w.Body.String() == "replacement"
	}
	if !seenReplacement || b.CapacityCount(fixture.app.ID) != 2 || b.HealthyCount(fixture.app.ID) != 0 {
		t.Fatal("replacement failed to rejoin the captured cohort or changed weights/capacity")
	}
}
