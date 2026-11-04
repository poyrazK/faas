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

// pinnedRoutingView holds what pinned requests check: one live production
// deployment with traffic, an app that allows revision pins, and one active
// release graph targeting the deployment.
func pinnedRoutingView(deployment, release string) state.PublicRoutingView {
	view := routingViewOf(deployment)
	view.Live, view.LiveComplete = []state.PublicRoutingLiveDeployment{{ID: deployment, Scope: "production", TrafficPercent: 100}}, true
	view.RevisionPins, view.RevisionPinsKnown = true, true
	view.Release = &state.PublicRoutingReleaseView{GraphsComplete: true, Graphs: []state.PublicRoutingReleaseGraph{
		{ID: release, Active: true, Target: state.PublicRoutingReleaseTarget{ID: release, DeploymentID: deployment}}}}
	return view
}

// ADR-20261028095914: pinned requests admit from the view too. Only a hosting
// smoke, and a pin the view cannot answer, keep the exact read.
func TestViewPinnerAnswersPinsFromTheView(t *testing.T) {
	app := App{ID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString()}
	deployment, release := uuid.NewString(), uuid.NewString()
	hostPin := PublicRoutingInputs{Valid: true, Scope: "production", HostDeploymentID: deployment, HostScope: "production"}
	releasePin := PublicRoutingInputs{Valid: true, Scope: "production", ResolveRelease: true, ReleasePresent: true, RequestedReleaseID: release}
	revisionPin := PublicRoutingInputs{Valid: true, Scope: "production", RevisionPresent: true, RequestedRevisionID: deployment}
	for _, tc := range []struct {
		name   string
		inputs PublicRoutingInputs
		cut    func(*state.PublicRoutingView)
		exact  bool
	}{
		{name: "unpinned project request", inputs: PublicRoutingInputs{Valid: true, Scope: "production", ResolveRelease: true}},
		{name: "async request", inputs: PublicRoutingInputs{Valid: true, Scope: "production", Async: true}},
		{name: "scoped request", inputs: PublicRoutingInputs{Valid: true, Scope: "staging"}},
		{name: "version key", inputs: PublicRoutingInputs{Valid: true, Scope: "production", VersionKey: "user"}},
		{name: "release header", inputs: releasePin},
		{name: "revision header", inputs: revisionPin},
		{name: "host pin", inputs: hostPin},
		{name: "hosting smoke", inputs: PublicRoutingInputs{Valid: true, Scope: "production", HostDeploymentID: deployment, Smoke: true}, exact: true},
		{name: "host pin the cut list lacks", inputs: hostPin, exact: true,
			cut: func(v *state.PublicRoutingView) { v.Live, v.LiveComplete = nil, false }},
		{name: "revision pin the cut list lacks", inputs: revisionPin, exact: true,
			cut: func(v *state.PublicRoutingView) { v.Live, v.LiveComplete = nil, false }},
		{name: "revision pin with an unreadable pin setting", inputs: revisionPin, exact: true,
			cut: func(v *state.PublicRoutingView) { v.RevisionPinsKnown = false }},
		{name: "release pin the cut list lacks", inputs: releasePin, exact: true,
			cut: func(v *state.PublicRoutingView) { v.Release.Graphs, v.Release.GraphsComplete = nil, false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var exact atomic.Int32
			current := pinnedRoutingView(deployment, release)
			if tc.cut != nil {
				tc.cut(&current)
			}
			view := &fakeRoutingView{view: current}
			if _, err := NewViewPublicRoutingPinner(view, countingExactPinner(&exact))(t.Context(), app, tc.inputs); err != nil {
				t.Fatal(err)
			}
			if usedExact := exact.Load() == 1; usedExact != tc.exact {
				t.Fatalf("exact reads=%d view reads=%d; want exact=%v", exact.Load(), view.reads.Load(), tc.exact)
			}
			if !tc.inputs.Smoke && (view.reads.Load() != 1 || view.keys[0] != (state.PublicRoutingViewKey{AppID: app.ID, AccountID: app.AccountID, ProjectID: app.ProjectID, Scope: tc.inputs.Scope})) {
				t.Fatalf("view reads=%d keys=%+v", view.reads.Load(), view.keys)
			}
		})
	}
}

// A pinned request's snapshot is the one the exact read builds: its host,
// release or revision verdict, evaluated at request time.
func TestViewPinnerAnswersPinsLikeTheExactRead(t *testing.T) {
	app := App{ID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString()}
	deployment, release := uuid.NewString(), uuid.NewString()
	other := uuid.NewString()
	future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Minute)
	hostPin := func(scope, id string) PublicRoutingInputs {
		return PublicRoutingInputs{Valid: true, Scope: "production", HostDeploymentID: id, HostScope: scope}
	}
	revisionPin := func(id string) PublicRoutingInputs {
		return PublicRoutingInputs{Valid: true, Scope: "production", RevisionPresent: true, RequestedRevisionID: id}
	}
	releasePin := func(id string) PublicRoutingInputs {
		return PublicRoutingInputs{Valid: true, Scope: "production", ResolveRelease: true, ReleasePresent: true, RequestedReleaseID: id}
	}
	withLive := func(d state.PublicRoutingLiveDeployment) func(*state.PublicRoutingView) {
		return func(v *state.PublicRoutingView) { v.Live = []state.PublicRoutingLiveDeployment{d} }
	}
	withGraph := func(g state.PublicRoutingReleaseGraph) func(*state.PublicRoutingView) {
		return func(v *state.PublicRoutingView) { v.Release.Graphs = []state.PublicRoutingReleaseGraph{g} }
	}
	for _, tc := range []struct {
		name   string
		inputs PublicRoutingInputs
		change func(*state.PublicRoutingView)
		want   func(*PublicRoutingSnapshot)
	}{
		{"host pin to a live deployment", hostPin("production", deployment), nil,
			func(s *PublicRoutingSnapshot) { s.HostChecked, s.HostAllowed = true, true }},
		{"host pin in another scope", hostPin("staging", deployment), nil,
			func(s *PublicRoutingSnapshot) { s.HostChecked = true }},
		{"host pin to a deployment not live", hostPin("production", other), nil,
			func(s *PublicRoutingSnapshot) { s.HostChecked = true }},
		{"host pin in the default scope", hostPin("", deployment),
			withLive(state.PublicRoutingLiveDeployment{ID: deployment, Scope: "default"}),
			func(s *PublicRoutingSnapshot) { s.HostChecked, s.HostAllowed = true, true }},
		{"revision with traffic", revisionPin(deployment), nil,
			func(s *PublicRoutingSnapshot) { s.RevisionChecked, s.RevisionAllowed = true, true }},
		{"revision kept by an unexpired pin", revisionPin(deployment),
			withLive(state.PublicRoutingLiveDeployment{ID: deployment, Scope: "production", PinExpiresAt: future}),
			func(s *PublicRoutingSnapshot) { s.RevisionChecked, s.RevisionAllowed = true, true }},
		{"revision whose pin expired", revisionPin(deployment),
			withLive(state.PublicRoutingLiveDeployment{ID: deployment, Scope: "production", PinExpiresAt: past}),
			func(s *PublicRoutingSnapshot) { s.RevisionChecked = true }},
		{"revision in another scope", revisionPin(deployment),
			withLive(state.PublicRoutingLiveDeployment{ID: deployment, Scope: "staging", TrafficPercent: 100}),
			func(s *PublicRoutingSnapshot) { s.RevisionChecked = true }},
		{"revision on an app without revision pins", revisionPin(deployment),
			func(v *state.PublicRoutingView) { v.RevisionPins = false },
			func(s *PublicRoutingSnapshot) { s.RevisionChecked = true }},
		{"revision not live", revisionPin(other), nil,
			func(s *PublicRoutingSnapshot) { s.RevisionChecked = true }},
		{"active release pin", releasePin(release), nil,
			func(s *PublicRoutingSnapshot) {
				s.ReleaseVerdict, s.ReleaseID, s.ReleaseDeploymentID = "allowed", release, deployment
			}},
		{"retained release pin", releasePin(release),
			withGraph(state.PublicRoutingReleaseGraph{ID: release, ExpiresAt: future, Target: state.PublicRoutingReleaseTarget{ID: release, DeploymentID: deployment}}),
			func(s *PublicRoutingSnapshot) {
				s.ReleaseVerdict, s.ReleaseID, s.ReleaseDeploymentID = "allowed", release, deployment
			}},
		{"expired release pin", releasePin(release),
			withGraph(state.PublicRoutingReleaseGraph{ID: release, ExpiresAt: past, Target: state.PublicRoutingReleaseTarget{ID: release, DeploymentID: deployment}}),
			func(s *PublicRoutingSnapshot) { s.ReleaseVerdict = "gone" }},
		{"incomplete release pin", releasePin(release),
			withGraph(state.PublicRoutingReleaseGraph{ID: release, Active: true, Target: state.PublicRoutingReleaseTarget{Err: state.ErrConflict}}),
			func(s *PublicRoutingSnapshot) { s.ReleaseVerdict = "conflict" }},
		{"unknown release pin", releasePin(other), nil,
			func(s *PublicRoutingSnapshot) { s.ReleaseVerdict = "gone" }},
		{"release pin outside a project", releasePin(release),
			func(v *state.PublicRoutingView) { v.Release = nil },
			func(s *PublicRoutingSnapshot) { s.ReleaseVerdict = "gone" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := pinnedRoutingView(deployment, release)
			if tc.change != nil {
				tc.change(&current)
			}
			var exact atomic.Int32
			got, err := NewViewPublicRoutingPinner(&fakeRoutingView{view: current}, countingExactPinner(&exact))(t.Context(), app, tc.inputs)
			want := publicSnapshotFixture(app, tc.inputs)
			tc.want(&want)
			if err != nil || exact.Load() != 0 || fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
				t.Fatalf("snapshot=%+v err=%v exact=%d\nwant     %+v", got, err, exact.Load(), want)
			}
			if err := validatePublicRoutingSnapshot(got, app, tc.inputs); err != nil {
				t.Fatalf("view snapshot failed validation: %v", err)
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
