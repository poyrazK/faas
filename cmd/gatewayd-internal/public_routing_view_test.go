// adr: 570
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/publicrouting"
	"github.com/onebox-faas/faas/pkg/state"
)

// countingRoutingSnapshotStore counts exact per-request reads.
type countingRoutingSnapshotStore struct {
	state.PublicRoutingSnapshotStore
	mu    sync.Mutex
	reads int
}

func (s *countingRoutingSnapshotStore) WithPublicRoutingSnapshot(ctx context.Context, read func(state.PublicRoutingPolicyReader) error) error {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
	return s.PublicRoutingSnapshotStore.WithPublicRoutingSnapshot(ctx, read)
}

func (s *countingRoutingSnapshotStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// assertViewMatchesExact pins ordinary inputs both ways against the same
// committed state: the view's snapshot must equal the exact read's.
func assertViewMatchesExact(t *testing.T, f publicRoutingPGFixture, app gateway.App, inputs gateway.PublicRoutingInputs) gateway.PublicRoutingSnapshot {
	t.Helper()
	exact, exactErr := publicRoutingPinnerFor(f.store, true)(t.Context(), app, inputs)
	view, viewErr := publicRoutingViewPinnerFor(f.store, publicrouting.New(f.store), true)(t.Context(), app, inputs)
	if (exactErr == nil) != (viewErr == nil) || errors.Is(exactErr, state.ErrNotFound) != errors.Is(viewErr, state.ErrNotFound) {
		t.Fatalf("errors differ: exact=%v view=%v", exactErr, viewErr)
	}
	if exactErr == nil && fmt.Sprintf("%+v", exact) != fmt.Sprintf("%+v", view) {
		t.Fatalf("view snapshot differs from the exact read:\nexact %+v\nview  %+v", exact, view)
	}
	return view
}

func TestPublicRoutingViewPostgresMatchesExactRead(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	app := gateway.App{ID: f.app.ID, AccountID: f.app.AccountID, ProjectID: f.project.ID}
	production := gateway.PublicRoutingInputs{Valid: true, Scope: "production", ResolveRelease: true}
	async := gateway.PublicRoutingInputs{Valid: true, Scope: "production", Async: true}
	staging := gateway.PublicRoutingInputs{Valid: true, Scope: "staging"}

	if snapshot := assertViewMatchesExact(t, f, app, production); len(snapshot.Weights) != 0 || snapshot.ReleaseVerdict != "allowed" {
		t.Fatalf("new app: %+v", snapshot)
	}
	legacy := f.deployment(t, "default", "sha256:legacy")
	if snapshot := assertViewMatchesExact(t, f, app, production); snapshot.RosterScope != "default" || snapshot.Weights[0].ID != legacy.ID {
		t.Fatalf("legacy lane: %+v", snapshot)
	}
	current := f.deployment(t, "production", "sha256:current")
	release := f.publish(t, current.ID)
	if snapshot := assertViewMatchesExact(t, f, app, production); snapshot.ReleaseID != release.ID || snapshot.ReleaseDeploymentID != current.ID {
		t.Fatalf("active graph: %+v", snapshot)
	}
	assertViewMatchesExact(t, f, app, async)
	stage := f.deployment(t, "staging", "sha256:staging")
	if snapshot := assertViewMatchesExact(t, f, app, staging); len(snapshot.Weights) != 1 || snapshot.Weights[0].ID != stage.ID {
		t.Fatalf("scoped roster: %+v", snapshot)
	}
	if _, err := f.pool.Exec(t.Context(), `DELETE FROM project_release_members WHERE release_id=$1 AND app_id=$2`, release.ID, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if snapshot := assertViewMatchesExact(t, f, app, production); snapshot.ReleaseVerdict != "conflict" {
		t.Fatalf("incomplete graph: %+v", snapshot)
	}
	unscoped := f.sourceApp(t, "public-routing-view-unscoped", f.app.AccountID)
	other := f
	other.app = unscoped
	other.deployment(t, "default", "sha256:unscoped-default")
	named := other.deployment(t, "production", "sha256:unscoped-production")
	if snapshot := assertViewMatchesExact(t, f, gateway.App{ID: unscoped.ID, AccountID: unscoped.AccountID}, gateway.PublicRoutingInputs{Valid: true}); snapshot.RosterScope != "production" || snapshot.Weights[0].ID != named.ID {
		t.Fatalf("unscoped app on named production: %+v", snapshot)
	}
	for _, foreign := range []gateway.App{{ID: app.ID, AccountID: uuid.NewString(), ProjectID: app.ProjectID}, {ID: app.ID, AccountID: app.AccountID, ProjectID: uuid.NewString()},
		{ID: app.ID, AccountID: app.AccountID}, {ID: uuid.NewString(), AccountID: app.AccountID, ProjectID: app.ProjectID}} {
		if _, err := publicRoutingViewPinnerFor(f.store, publicrouting.New(f.store), true)(t.Context(), foreign, staging); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign owner %+v: %v", foreign, err)
		}
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO deployments (id,app_id,kind,scope,image_digest,status,traffic_percent,traffic_percent_explicit)
		SELECT gen_random_uuid(),$1,'image','production','sha256:bounded-'||n,'live',1,true FROM generate_series(1,$2) n`, f.app.ID, api.TrafficPolicyMaxDeployments); err != nil {
		t.Fatal(err)
	}
	if _, err := publicRoutingViewPinnerFor(f.store, publicrouting.New(f.store), true)(t.Context(), app, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err == nil {
		t.Fatal("oversized roster was admitted from the view")
	}
	if f.pool.Stat().AcquiredConns() != 0 {
		t.Fatal("the routing view retained a transaction")
	}
}

// The view holds a running canary's policy and both graphs, and splits each
// request with the per-request read's rule.
func TestPublicRoutingViewPostgresResolvesCanaryLikeExactRead(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	baselineDeployment := f.deployment(t, "production", "sha256:baseline")
	baseline := f.publish(t, baselineDeployment.ID)
	candidateDeployment := f.deployment(t, "production", "sha256:candidate")
	candidate := f.publish(t, candidateDeployment.ID)
	// The candidate graph is active only at the final stage; the canary keeps
	// the baseline active until then.
	if _, err := f.pool.Exec(t.Context(), `UPDATE project_release_sets SET active = false, expires_at = now() + interval '1 hour' WHERE id = $1`, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE project_release_sets SET active = true, expires_at = NULL WHERE id = $1`, baseline.ID); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Minute).UTC()
	policy := state.ProjectCanaryPolicyState{ID: uuid.NewString(), BaselineReleaseID: baseline.ID, CandidateReleaseID: candidate.ID,
		Generation: 1, Percent: api.ProjectCanaryMiddlePercent, Status: "running", StageStartedAt: started,
		Deadline: started.Add(api.ProjectReleaseVerificationTimeout)}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO project_release_canaries (account_id, project_id, attempt_id, policy, status) VALUES ($1, $2, $3, $4::jsonb, 'running')`,
		f.app.AccountID, f.project.ID, "canary-"+uuid.NewString(), raw); err != nil {
		t.Fatal(err)
	}
	key := state.PublicRoutingViewKey{AppID: f.app.ID, AccountID: f.app.AccountID, ProjectID: f.project.ID, Scope: "production"}
	results, err := f.store.ReadPublicRoutingViews(t.Context(), []state.PublicRoutingViewKey{key})
	if err != nil || results[key].Err != nil || results[key].View.Release == nil || results[key].View.Release.Canary == nil {
		t.Fatalf("canary view = %+v err=%v", results[key], err)
	}
	view := results[key].View.Release
	seen := map[string]int{}
	for range 200 {
		requestKey := uuid.NewString()
		var exactID, exactDeployment string
		if err := f.store.WithPublicRoutingSnapshot(t.Context(), func(reader state.PublicRoutingPolicyReader) error {
			var err error
			exactID, exactDeployment, err = reader.(interface {
				ResolvePublicProjectCanary(context.Context, string, string, string) (string, string, error)
			}).ResolvePublicProjectCanary(t.Context(), f.app.ID, "production", requestKey)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		id, deployment, err := view.Resolve(requestKey, time.Now())
		if err != nil || id != exactID || deployment != exactDeployment {
			t.Fatalf("key %s: view=(%s,%s,%v) exact=(%s,%s)", requestKey, id, deployment, err, exactID, exactDeployment)
		}
		seen[deployment]++
	}
	if seen[baselineDeployment.ID] == 0 || seen[candidateDeployment.ID] == 0 {
		t.Fatalf("canary split = %v; want both graphs", seen)
	}
	// A graph that expired is gone in both reads.
	if _, err := f.pool.Exec(t.Context(), `UPDATE project_release_sets SET expires_at = now() - interval '1 minute' WHERE id = $1`, candidate.ID); err != nil {
		t.Fatal(err)
	}
	results, err = f.store.ReadPublicRoutingViews(t.Context(), []state.PublicRoutingViewKey{key})
	if err != nil || !errors.Is(results[key].View.Release.Canary.Targets[candidate.ID].Err, state.ErrNotFound) {
		t.Fatalf("expired candidate = %+v err=%v; want gone", results[key], err)
	}
}

// Ordinary requests read the store once per app; pinned requests still read
// it every time.
func TestPublicRoutingViewPostgresKeepsWarmRequestsOffTheStore(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	current := f.deployment(t, "production", "sha256:current")
	release := f.publish(t, current.ID)
	exact := &countingRoutingSnapshotStore{PublicRoutingSnapshotStore: f.store}
	view := publicrouting.New(f.store)
	pin := publicRoutingViewPinnerFor(exact, view, true)
	app := f.routingApp()
	ordinary := gateway.PublicRoutingInputs{Valid: true, Scope: "production", ResolveRelease: true}
	for range 20 {
		if snapshot, err := pin(t.Context(), app, ordinary); err != nil || snapshot.ReleaseDeploymentID != current.ID {
			t.Fatalf("ordinary snapshot=%+v err=%v", snapshot, err)
		}
	}
	pinned := gateway.PublicRoutingInputs{Valid: true, Scope: "production", ResolveRelease: true, ReleasePresent: true, RequestedReleaseID: release.ID}
	for range 3 {
		if snapshot, err := pin(t.Context(), app, pinned); err != nil || snapshot.ReleaseDeploymentID != current.ID {
			t.Fatalf("pinned snapshot=%+v err=%v", snapshot, err)
		}
	}
	if exact.count() != 3 || view.Tracked() != 1 {
		t.Fatalf("exact reads=%d tracked views=%d; want only the pinned requests to read per request", exact.count(), view.Tracked())
	}
	next := f.deployment(t, "production", "sha256:next")
	f.publish(t, next.ID)
	if snapshot, _ := pin(t.Context(), app, ordinary); snapshot.ReleaseDeploymentID != current.ID {
		t.Fatalf("view changed without a refresh: %+v", snapshot)
	}
	if err := view.RefreshApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := pin(t.Context(), app, ordinary); err != nil || snapshot.ReleaseDeploymentID != next.ID {
		t.Fatalf("refreshed snapshot=%+v err=%v", snapshot, err)
	}
}

type routingViewInvalidator struct {
	*fakeInvalidator
	mu        sync.Mutex
	requested []string
	refreshed []string
	err       error
}

func (r *routingViewInvalidator) RequestPublicRoutingRefresh(app string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requested = append(r.requested, app)
}

func (r *routingViewInvalidator) RefreshPublicRouting(_ context.Context, app string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshed = append(r.refreshed, app)
	return r.err
}

func TestPublicRoutingViewNotificationsWakeTheNamedApp(t *testing.T) {
	app := uuid.NewString()
	for _, tc := range []struct {
		channel, payload string
		want             []string
	}{
		{db.NotifyDeploymentChanged, `{"kind":"traffic","app_id":"` + app + `","deployment_id":"d"}`, []string{app}},
		{db.NotifyAppChanged, app, []string{app}},
		{db.NotifyAppChanged, `not an app`, []string{""}},
		{db.NotifyAppDelete, `{"app_id":"` + app + `"}`, []string{app}},
		{db.NotifyAccountDeleted, `{"account_id":"a"}`, []string{""}},
		{db.NotifyInstanceChanged, `{"app_id":"` + app + `","instance_id":"i","state":"running"}`, nil},
		{db.NotifyDeploymentChanged, `{}`, nil},
	} {
		inv := &routingViewInvalidator{fakeInvalidator: &fakeInvalidator{}}
		requestPublicRoutingRefresh(inv, db.Notification{Channel: tc.channel, Payload: tc.payload})
		if fmt.Sprint(inv.requested) != fmt.Sprint(tc.want) {
			t.Fatalf("%s %s: refresh requests %q want %q", tc.channel, tc.payload, inv.requested, tc.want)
		}
	}
}

// schedd drains a predecessor once every gateway acknowledges the route
// change, so the acknowledgement waits for the routing view's refresh.
func TestHandleDeploymentRouteInvalidationRefreshesRoutingViewBeforeAck(t *testing.T) {
	inv := &routingViewInvalidator{fakeInvalidator: &fakeInvalidator{}}
	if _, applied := handleDeploymentRouteInvalidation(context.Background(), inv,
		`{"app_id":"app-7","deployment_id":"dep-3","generation":44}`, testLogger()); !applied || fmt.Sprint(inv.refreshed) != "[app-7]" {
		t.Fatalf("applied=%v routing refreshes=%v", applied, inv.refreshed)
	}
	inv = &routingViewInvalidator{fakeInvalidator: &fakeInvalidator{}, err: errors.New("store unavailable")}
	if _, applied := handleDeploymentRouteInvalidation(context.Background(), inv,
		`{"app_id":"app-7","deployment_id":"dep-3","generation":44}`, testLogger()); applied {
		t.Fatal("route change acknowledged before the routing view refreshed")
	}
}
