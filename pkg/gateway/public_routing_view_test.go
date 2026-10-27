// adr: 570
package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeRoutingView stands in for pkg/publicrouting's refreshed view.
type fakeRoutingView struct {
	mu    sync.Mutex
	view  state.PublicRoutingView
	err   error
	keys  []state.PublicRoutingViewKey
	reads atomic.Int32
}

func (v *fakeRoutingView) View(_ context.Context, key state.PublicRoutingViewKey) (state.PublicRoutingView, error) {
	v.reads.Add(1)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keys = append(v.keys, key)
	return v.view, v.err
}

func (v *fakeRoutingView) set(view state.PublicRoutingView) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.view = view
}

func routingViewOf(deployments ...string) state.PublicRoutingView {
	var view state.PublicRoutingView
	for _, id := range deployments {
		view.Weights = append(view.Weights, state.ServiceDeploymentWeight{ID: id, TrafficPercent: 100 / len(deployments)})
	}
	return view
}

func countingExactPinner(calls *atomic.Int32) PublicRoutingPinner {
	return func(_ context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
		calls.Add(1)
		return publicSnapshotFixture(app, inputs), nil
	}
}

// Only a request with no pin admits from the view; every pin keeps the
// exact read, which can verify a deployment the view does not hold.
func TestViewPinnerSendsOnlyPinnedRequestsToExactRead(t *testing.T) {
	app := App{ID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString()}
	deployment, release := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name   string
		inputs PublicRoutingInputs
		exact  bool
	}{
		{"unpinned project request", PublicRoutingInputs{Valid: true, Scope: "production", ResolveRelease: true}, false},
		{"async request", PublicRoutingInputs{Valid: true, Scope: "production", Async: true}, false},
		{"scoped request", PublicRoutingInputs{Valid: true, Scope: "staging"}, false},
		{"version key", PublicRoutingInputs{Valid: true, Scope: "production", VersionKey: "user"}, false},
		{"release header", PublicRoutingInputs{Valid: true, Scope: "production", ResolveRelease: true, ReleasePresent: true, RequestedReleaseID: release}, true},
		{"revision header", PublicRoutingInputs{Valid: true, Scope: "production", RevisionPresent: true, RequestedRevisionID: deployment}, true},
		{"host pin", PublicRoutingInputs{Valid: true, Scope: "production", HostDeploymentID: deployment, HostScope: "production"}, true},
		{"hosting smoke", PublicRoutingInputs{Valid: true, Scope: "production", HostDeploymentID: deployment, Smoke: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var exact atomic.Int32
			view := &fakeRoutingView{view: routingViewOf(deployment)}
			view.view.Release = &state.PublicRoutingReleaseView{}
			if _, err := NewViewPublicRoutingPinner(view, countingExactPinner(&exact))(t.Context(), app, tc.inputs); err != nil {
				t.Fatal(err)
			}
			usedExact, usedView := exact.Load() == 1, view.reads.Load() == 1
			if usedExact != tc.exact || usedView == tc.exact {
				t.Fatalf("exact reads=%d view reads=%d; want exact=%v", exact.Load(), view.reads.Load(), tc.exact)
			}
			if !tc.exact && view.keys[0] != (state.PublicRoutingViewKey{AppID: app.ID, AccountID: app.AccountID, ProjectID: app.ProjectID, Scope: tc.inputs.Scope}) {
				t.Fatalf("view key = %+v", view.keys[0])
			}
		})
	}
}

// The view pinner builds the snapshot the exact read builds, so
// validatePublicRoutingSnapshot and selection see no difference.
func TestViewPinnerBuildsTheExactSnapshot(t *testing.T) {
	app := App{ID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString()}
	live, graph := uuid.NewString(), uuid.NewString()
	production := PublicRoutingInputs{Valid: true, Scope: "production", ResolveRelease: true}
	for _, tc := range []struct {
		name   string
		inputs PublicRoutingInputs
		view   state.PublicRoutingView
		want   func(*PublicRoutingSnapshot)
	}{
		{"project app without an active graph", production,
			state.PublicRoutingView{Weights: []state.ServiceDeploymentWeight{{ID: live, TrafficPercent: 100}}, Lane: "production", Release: &state.PublicRoutingReleaseView{}},
			func(s *PublicRoutingSnapshot) {
				s.ReleaseVerdict, s.Weights = "allowed", []DeploymentWeightsRow{{ID: live, TrafficPercent: 100}}
			}},
		{"project app on the legacy default lane", production,
			state.PublicRoutingView{Weights: []state.ServiceDeploymentWeight{{ID: live, TrafficPercent: 100}}, Lane: "default", Release: &state.PublicRoutingReleaseView{}},
			func(s *PublicRoutingSnapshot) {
				s.ReleaseVerdict, s.RosterScope, s.Weights = "allowed", "default", []DeploymentWeightsRow{{ID: live, TrafficPercent: 100}}
			}},
		{"active graph", production,
			state.PublicRoutingView{Weights: []state.ServiceDeploymentWeight{{ID: uuid.NewString(), TrafficPercent: 100}}, Lane: "production",
				Release: &state.PublicRoutingReleaseView{Active: state.PublicRoutingReleaseTarget{ID: graph, DeploymentID: live}}},
			func(s *PublicRoutingSnapshot) {
				s.ReleaseVerdict, s.ReleaseID, s.ReleaseDeploymentID = "allowed", graph, live
			}},
		{"incomplete graph", production,
			state.PublicRoutingView{Release: &state.PublicRoutingReleaseView{Active: state.PublicRoutingReleaseTarget{Err: state.ErrConflict}}},
			func(s *PublicRoutingSnapshot) { s.ReleaseVerdict = "conflict" }},
		{"async request", PublicRoutingInputs{Valid: true, Scope: "production", Async: true},
			state.PublicRoutingView{Weights: []state.ServiceDeploymentWeight{{ID: live, TrafficPercent: 100}}, Lane: "production", Release: &state.PublicRoutingReleaseView{}},
			func(*PublicRoutingSnapshot) {}},
		{"unscoped app on named production", PublicRoutingInputs{Valid: true},
			state.PublicRoutingView{Weights: []state.ServiceDeploymentWeight{{ID: live, TrafficPercent: 100}}, Lane: "production"},
			func(s *PublicRoutingSnapshot) {
				s.RosterScope, s.Weights = "production", []DeploymentWeightsRow{{ID: live, TrafficPercent: 100}}
			}},
		{"scoped roster", PublicRoutingInputs{Valid: true, Scope: "staging"},
			state.PublicRoutingView{Weights: []state.ServiceDeploymentWeight{{ID: live, TrafficPercent: 100}}},
			func(s *PublicRoutingSnapshot) { s.Weights = []DeploymentWeightsRow{{ID: live, TrafficPercent: 100}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var exact atomic.Int32
			got, err := NewViewPublicRoutingPinner(&fakeRoutingView{view: tc.view}, countingExactPinner(&exact))(t.Context(), app, tc.inputs)
			want := publicSnapshotFixture(app, tc.inputs)
			tc.want(&want)
			if err != nil || fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
				t.Fatalf("snapshot=%+v err=%v\nwant     %+v", got, err, want)
			}
			if err := validatePublicRoutingSnapshot(got, app, tc.inputs); err != nil {
				t.Fatalf("view snapshot failed validation: %v", err)
			}
		})
	}
}

// A running canary splits each request at its own key and time, as the
// per-request read did; the view holds the policy, not one request's choice.
func TestViewPinnerSplitsCanaryPerRequest(t *testing.T) {
	app := App{ID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString()}
	baseline, candidate := uuid.NewString(), uuid.NewString()
	started := time.Now().Add(-time.Minute)
	policy := state.ProjectCanaryPolicyState{ID: uuid.NewString(), BaselineReleaseID: baseline, CandidateReleaseID: candidate,
		Generation: 1, Percent: api.ProjectCanaryInitialPercent, Status: "running", StageStartedAt: started,
		Deadline: started.Add(api.ProjectReleaseVerificationTimeout)}
	targets := map[string]state.PublicRoutingReleaseTarget{baseline: {ID: baseline, DeploymentID: "old"}, candidate: {ID: candidate, DeploymentID: "new"}}
	view := &fakeRoutingView{view: state.PublicRoutingView{Release: &state.PublicRoutingReleaseView{
		Canary: &state.PublicRoutingCanaryView{Policy: policy, ActiveReleaseID: baseline, Targets: targets}}}}
	var exact atomic.Int32
	pin := NewViewPublicRoutingPinner(view, countingExactPinner(&exact))
	inputs := PublicRoutingInputs{Valid: true, Scope: "production", ResolveRelease: true}
	seen := map[string]int{}
	for range 1000 {
		snapshot, err := pin(t.Context(), app, inputs)
		if err != nil || snapshot.ReleaseVerdict != "allowed" {
			t.Fatalf("canary snapshot=%+v err=%v", snapshot, err)
		}
		seen[snapshot.ReleaseDeploymentID]++
	}
	if seen["old"] == 0 || seen["new"] == 0 || seen["old"]+seen["new"] != 1000 || seen["new"] > 200 {
		t.Fatalf("canary split = %v; want both graphs near %d%% candidate", seen, api.ProjectCanaryInitialPercent)
	}
	// Past the stage deadline the same view sends every request to the baseline.
	policy.StageStartedAt, policy.Deadline = started.Add(-time.Hour), started.Add(-time.Hour).Add(api.ProjectReleaseVerificationTimeout)
	view.view.Release.Canary.Policy = policy
	for range 50 {
		if snapshot, err := pin(t.Context(), app, inputs); err != nil || snapshot.ReleaseDeploymentID != "old" {
			t.Fatalf("expired canary snapshot=%+v err=%v; want the baseline", snapshot, err)
		}
	}
	if exact.Load() != 0 {
		t.Fatal("canary requests used the exact read")
	}
}

// One roster is frozen per request across cold wake and retry, whatever the
// view becomes meanwhile; the next request sees the refreshed view.
func TestViewPinnerRetainsDeploymentThroughColdWakeAndRetry(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	old, next := uuid.NewString(), uuid.NewString()
	view := &fakeRoutingView{view: routingViewOf(old)}
	var exact atomic.Int32
	wrapped := &publicSnapshotBackend{fakeBackend: backend}
	h.backend = wrapped
	h.WithPublicRoutingPolicy(NewViewPublicRoutingPinner(view, countingExactPinner(&exact))).
		WithRetryEnabled(true).WithRetryDefault(testPolicy())
	wrapped.onAdmit = func(_ context.Context, deployment, _ string, _ int) error {
		if deployment != old {
			return fmt.Errorf("wake crossed admitted deployment: %s", deployment)
		}
		view.set(routingViewOf(next)) // a refresh lands while the request waits
		backend.mu.Lock()
		defer backend.mu.Unlock()
		backend.targets = []Target{{InstanceID: "one", NodeID: "node", DeploymentID: old}, {InstanceID: "two", NodeID: "node", DeploymentID: old},
			{InstanceID: "new", NodeID: "node", DeploymentID: next}}
		return nil
	}
	attempts := 0
	h.WithForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			want := old
			if attempts > 2 {
				want = next
			}
			if target.DeploymentID != want {
				t.Errorf("attempt %d crossed deployment: %s want %s", attempts, target.DeploymentID, want)
			}
			if attempts == 1 {
				markStaleTarget(r.Context())
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	for i, wantAttempts := range []int{2, 3} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/checkout", nil))
		if rec.Code != http.StatusNoContent || attempts != wantAttempts {
			t.Fatalf("request %d: status=%d attempts=%d body=%s", i+1, rec.Code, attempts, rec.Body)
		}
	}
	if exact.Load() != 0 || view.reads.Load() != 2 || wrapped.mixedReads.Load() != 0 {
		t.Fatalf("exact reads=%d view reads=%d mutable reads=%d; want one view read per request",
			exact.Load(), view.reads.Load(), wrapped.mixedReads.Load())
	}
}

// A view that cannot answer refuses before wake with the stage the exact
// read's failure would record, and the refusal is logged.
func TestViewPinnerRefusalRecordsStage(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		err        error
		project    bool
	}{
		{name: "owner", want: "routing_owner_unverified", err: state.ErrNotFound},
		{name: "outage past the grace", want: "routing_read_timeout", err: context.DeadlineExceeded},
		{name: "store failure", want: "routing_read_error", err: errors.New("connection refused")},
		{name: "project view without a release graph", want: "routing_read_error", project: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			var logs bytes.Buffer
			h.log = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			if tc.project {
				backend.mu.Lock()
				backend.app.ProjectID = uuid.NewString()
				backend.mu.Unlock()
			}
			h.backend = &publicSnapshotBackend{fakeBackend: backend}
			var exact atomic.Int32
			h.WithPublicRoutingPolicy(NewViewPublicRoutingPinner(&fakeRoutingView{view: routingViewOf(uuid.NewString()), err: tc.err}, countingExactPinner(&exact)))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
			if rec.Code != http.StatusServiceUnavailable || exact.Load() != 0 {
				t.Fatalf("status=%d exact=%d body=%s", rec.Code, exact.Load(), rec.Body)
			}
			if got := decisionLogField(t, logs.Bytes(), "policy_stage"); got != tc.want {
				t.Fatalf("policy_stage=%q want %q; logs=%s", got, tc.want, logs.String())
			}
			if !bytes.Contains(logs.Bytes(), []byte("gateway: public routing refused with policy_unavailable")) {
				t.Fatalf("refusal was not logged: %s", logs.String())
			}
		})
	}
}
