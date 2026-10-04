// adr: 531
package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type publicSnapshotBackend struct {
	*fakeBackend
	onAdmit    func(context.Context, string, string, int) error
	mixedReads atomic.Int32
}

func (b *publicSnapshotBackend) Admit(ctx context.Context, app, deployment, scope, trigger string, maximum int) (string, WakeMethod, bool, error) {
	if b.onAdmit != nil {
		if err := b.onAdmit(ctx, deployment, scope, maximum); err != nil {
			return "", WakeMethodUnspecified, false, err
		}
	}
	return "wake", WakeMethodColdBoot, false, nil
}

func (b *publicSnapshotBackend) Pick(string) PickResult {
	b.mixedReads.Add(1)
	return b.fakeBackend.Pick(b.app.ID)
}
func (b *publicSnapshotBackend) AffinityDeployment(string, string) (string, bool) {
	b.mixedReads.Add(1)
	return "", false
}
func (b *publicSnapshotBackend) ResolveProjectRelease(context.Context, string, string, string) (string, string, error) {
	b.mixedReads.Add(1)
	return "", "", errors.New("mixed release read")
}
func (b *publicSnapshotBackend) ResolveRevisionPin(context.Context, string, string, string) (bool, error) {
	b.mixedReads.Add(1)
	return false, errors.New("mixed revision read")
}
func (b *publicSnapshotBackend) EvictInstance(app, instance string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	filtered := b.targets[:0]
	for _, target := range b.targets {
		if target.InstanceID != instance {
			filtered = append(filtered, target)
		}
	}
	b.targets = filtered
}

func publicSnapshotFixture(app App, inputs PublicRoutingInputs) PublicRoutingSnapshot {
	return PublicRoutingSnapshot{AppID: app.ID, AccountID: app.AccountID, Scope: inputs.Scope, Async: inputs.Async,
		ReleaseRequested: inputs.RequestedReleaseID, ReleaseResolved: inputs.ResolveRelease,
		RevisionID: inputs.RequestedRevisionID, HostDeploymentID: inputs.HostDeploymentID, HostScope: inputs.HostScope}
}

func TestPublicRoutingSnapshotRetainsDeploymentThroughColdWakeAndRetry(t *testing.T) {
	for _, mode := range []string{"weighted", "version", "release", "revision", "host"} {
		t.Run(mode, func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			old, next, release := uuid.NewString(), uuid.NewString(), uuid.NewString()
			backend.app.RevisionPinTTLSeconds = 3600
			if mode == "release" {
				backend.app.ProjectID = uuid.NewString()
			}
			if mode == "host" {
				backend.app.PinnedDeploymentID, backend.app.PinnedDeploymentScope = old, "production"
			}
			sourceRows := []DeploymentWeightsRow{{ID: old, TrafficPercent: 100}}
			var phase atomic.Bool
			wrapped := &publicSnapshotBackend{fakeBackend: backend}
			h.backend = wrapped
			h.WithPublicRoutingPolicy(func(ctx context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
				snapshot := publicSnapshotFixture(app, inputs)
				snapshot.Weights = sourceRows
				if mode == "release" {
					snapshot.ReleaseVerdict, snapshot.ReleaseID, snapshot.ReleaseDeploymentID = "allowed", release, sourceRows[0].ID
				}
				if mode == "revision" {
					snapshot.RevisionChecked, snapshot.RevisionAllowed = true, !phase.Load()
				}
				if mode == "host" {
					snapshot.HostChecked, snapshot.HostAllowed = true, true
				}
				return snapshot, nil
			}).WithRetryEnabled(true).WithRetryDefault(testPolicy())
			wrapped.onAdmit = func(ctx context.Context, deployment, scope string, maximum int) error {
				if deployment != old {
					return fmt.Errorf("wake crossed admitted deployment: %s", deployment)
				}
				if mode == "weighted" || mode == "version" {
					if maximum != effectiveAppConcurrencyLimit(backend.app, api.MustLimitsFor(backend.app.Plan).MaxConcurrency) {
						return fmt.Errorf("ordinary wake raised concurrency: %d", maximum)
					}
				}
				// Change source routing while the request is blocked in cold wake.
				phase.Store(true)
				sourceRows[0].ID = next
				backend.mu.Lock()
				defer backend.mu.Unlock()
				if mode == "host" {
					backend.app.PinnedDeploymentID = next
				}
				backend.targets = []Target{{InstanceID: "one", NodeID: "node", DeploymentID: old}, {InstanceID: "two", NodeID: "node", DeploymentID: old}, {InstanceID: "new", NodeID: "node", DeploymentID: next}}
				return nil
			}
			attempts := 0
			var proof string
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
						proof = TrafficPolicyRevision(r.Context())
						markStaleTarget(r.Context())
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					if attempts == 2 && TrafficPolicyRevision(r.Context()) != proof {
						t.Error("retry changed policy proof")
					}
					w.WriteHeader(http.StatusNoContent)
				})
			})
			request := func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/checkout", nil)
				if mode == "version" {
					r.Header.Set(api.VersionKeyHeader, "user-one")
				}
				if mode == "release" {
					r.Header.Set(api.ReleaseHeader, release)
				}
				if mode == "revision" {
					r.Header.Set(api.RevisionHeader, old)
				}
				return r
			}
			first := httptest.NewRecorder()
			h.ServeHTTP(first, request())
			if first.Code != http.StatusNoContent || attempts != 2 || proof == "" || first.Header().Get(TrafficPolicyRevisionHeader) != proof {
				t.Fatalf("admitted response=%d attempts=%d proof=%q body=%s", first.Code, attempts, proof, first.Body)
			}
			fresh := httptest.NewRecorder()
			h.ServeHTTP(fresh, request())
			wantStatus := http.StatusNoContent
			if mode == "revision" {
				wantStatus = http.StatusGone
			}
			if fresh.Code != wantStatus || fresh.Header().Get(TrafficPolicyRevisionHeader) == proof {
				t.Fatalf("fresh response did not observe routing: %d/%s", fresh.Code, fresh.Body)
			}
			if wrapped.mixedReads.Load() != 0 {
				t.Fatal("request consulted mutable routing after sealing")
			}
		})
	}
}

func TestPublicRoutingSnapshotRefusesBeforeWake(t *testing.T) {
	for _, kind := range []string{"outage", "wrong-owner", "missing-verdict", "duplicate-weights", "invalid-weight", "empty-roster", "slow", "revision-gone", "release-gone", "release-conflict"} {
		t.Run(kind, func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			deployment, release := uuid.NewString(), uuid.NewString()
			backend.app.RevisionPinTTLSeconds = 3600
			if strings.HasPrefix(kind, "release") {
				backend.app.ProjectID = uuid.NewString()
			}
			forwarded := false
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded = true })
			})
			h.WithPublicRoutingPolicy(func(ctx context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
				snapshot := publicSnapshotFixture(app, inputs)
				snapshot.Weights = []DeploymentWeightsRow{{ID: deployment, TrafficPercent: 100}}
				switch kind {
				case "outage":
					return snapshot, errors.New("store unavailable")
				case "wrong-owner":
					snapshot.AccountID = "foreign"
				case "duplicate-weights":
					snapshot.Weights = append(snapshot.Weights, snapshot.Weights[0])
				case "invalid-weight":
					snapshot.Weights[0].TrafficPercent = api.TrafficPolicyMaxWeight + 1
				case "empty-roster":
					snapshot.Weights = nil
				case "slow":
					<-ctx.Done()
					return snapshot, nil // Backend ignores its error; the caller still checks expiry.
				case "revision-gone":
					snapshot.RevisionChecked = true
				case "release-gone":
					snapshot.ReleaseVerdict = "gone"
				case "release-conflict":
					snapshot.ReleaseVerdict = "conflict"
				}
				return snapshot, nil
			})
			r := httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil)
			if kind == "missing-verdict" || kind == "revision-gone" {
				r.Header.Set(api.RevisionHeader, deployment)
			}
			if strings.HasPrefix(kind, "release") {
				r.Header.Set(api.ReleaseHeader, release)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			want := http.StatusServiceUnavailable
			if kind == "revision-gone" || kind == "release-gone" {
				want = http.StatusGone
			}
			if rec.Code != want || forwarded || backend.admits != 0 {
				t.Fatalf("refusal=%d body=%s forwarded=%v admits=%d", rec.Code, rec.Body, forwarded, backend.admits)
			}
			if kind == "empty-roster" && (!strings.Contains(rec.Body.String(), api.CodeCapacity) || strings.Contains(rec.Body.String(), api.CodeTrafficPolicyUnavailable)) {
				t.Fatalf("verified empty roster must report capacity: %s", rec.Body)
			}
		})
	}
}

func TestPublicRoutingWakeCoalescesCohortsAndKeepsTotalQueueAllowance(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.app.MaxQueueWaitMS = 90
	wrapped := &publicSnapshotBackend{fakeBackend: backend}
	h.backend = wrapped
	first, second := uuid.NewString(), uuid.NewString()
	started := make(chan string, 2)
	completed := make(chan string, 2)
	var calls atomic.Int32
	wrapped.onAdmit = func(ctx context.Context, deployment, _ string, _ int) error {
		calls.Add(1)
		started <- deployment
		select {
		case <-time.After(60 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
		backend.mu.Lock()
		backend.targets = append(backend.targets, Target{InstanceID: deployment, NodeID: "node", DeploymentID: deployment})
		backend.mu.Unlock()
		completed <- deployment
		return nil
	}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	start := func(deployment string) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, _, err := h.wakeDeployment(t.Context(), backend.app, deployment, "production", "gateway", 4)
			errorsSeen <- err
		}()
	}
	start(first)
	select {
	case id := <-started:
		if id != first {
			t.Fatal("first cohort changed")
		}
	case <-time.After(time.Second):
		t.Fatal("first wake did not start")
	}
	before := time.Now()
	start(second)
	// The second cohort shares the app queue, then wakes only itself. Its
	// 90 ms allowance includes the first cohort's 60 ms, not 90 ms per wake.
	wait.Wait()
	firstErr, secondErr := <-errorsSeen, <-errorsSeen
	var timeout *WakeQueueWaitTimeoutError
	if firstErr != nil || !errors.As(secondErr, &timeout) {
		t.Fatalf("cohort queue reset: %v/%v elapsed=%s", firstErr, secondErr, time.Since(before))
	}
	for count := 0; count < 2; count++ {
		select {
		case <-completed:
		case <-time.After(time.Second):
			t.Fatal("bounded detached wake did not complete")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("cohorts did not coalesce independently: %d", calls.Load())
	}
}

func TestPublicRoutingSnapshotLookupConsumesTotalDeadline(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	setTotalBudget(h, backend.app, 50)
	h.WithPublicRoutingPolicy(func(ctx context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
		<-ctx.Done()
		return publicSnapshotFixture(app, inputs), nil
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
	assertTotalTimeout(t, rec)
	if backend.admits != 0 {
		t.Fatal("expired snapshot reached wake")
	}
}

func TestPublicRoutingColdSecondCohortKeepsBoundedRolloutOverlap(t *testing.T) {
	h, fixture, _ := newTestHandler(t)
	fixture.app.MaxConcurrency = 1
	first, candidate := uuid.NewString(), uuid.NewString()
	fixture.AddTarget(Target{InstanceID: "stable", DeploymentID: first, NodeID: "node"})
	weights := []DeploymentWeightsRow{{ID: first, TrafficPercent: 50}, {ID: candidate, TrafficPercent: 50}}
	backend := &publicSnapshotBackend{fakeBackend: fixture}
	h.backend = backend
	h.WithPublicRoutingPolicy(func(ctx context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
		snapshot := publicSnapshotFixture(app, inputs)
		snapshot.Weights = weights
		return snapshot, nil
	})
	backend.onAdmit = func(ctx context.Context, deployment, scope string, maximum int) error {
		if deployment != candidate || maximum != 1+api.RolloutConcurrencyGrant {
			return fmt.Errorf("cold rollout admission changed: %s/%d", deployment, maximum)
		}
		fixture.AddTarget(Target{InstanceID: "candidate", DeploymentID: candidate, NodeID: "node"})
		return nil
	}
	h.WithForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if target.DeploymentID != candidate {
				t.Errorf("cold rollout used warm sibling: %s", target.DeploymentID)
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	for index := 0; index < 1000; index++ {
		key := fmt.Sprintf("customer-%d", index)
		if deployment, _ := AffinityDeploymentFromWeights(fixture.app.ID, key, weights); deployment == candidate {
			r := httptest.NewRequest(http.MethodGet, "http://"+fixture.host+"/", nil)
			r.Header.Set(api.VersionKeyHeader, key)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("cold rollout=%d/%s", rec.Code, rec.Body)
			}
			return
		}
	}
	t.Fatal("could not find candidate version key")
}

func TestPublicRoutingSnapshotPreservesPinValidationBeforeRead(t *testing.T) {
	for _, tc := range []struct {
		name     string
		revision []string
		release  []string
		status   int
	}{
		{"conflicting", []string{uuid.NewString()}, []string{uuid.NewString()}, 400},
		{"release-malformed", nil, []string{"invalid"}, 400},
		{"release-duplicate", nil, []string{uuid.NewString(), uuid.NewString()}, 400},
		{"revision-length", []string{"invalid"}, nil, 400},
		{"revision-malformed", []string{strings.Repeat("x", 36)}, nil, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			backend.app.ProjectID, backend.app.RevisionPinTTLSeconds = uuid.NewString(), 3600
			h.WithPublicRoutingPolicy(func(context.Context, App, PublicRoutingInputs) (PublicRoutingSnapshot, error) {
				t.Error("invalid pin queried routing")
				return PublicRoutingSnapshot{}, errors.New("unexpected")
			})
			r := httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil)
			if tc.revision != nil {
				r.Header[api.RevisionHeader] = tc.revision
			}
			if tc.release != nil {
				r.Header[api.ReleaseHeader] = tc.release
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != tc.status || backend.admits != 0 {
				t.Fatalf("pin validation=%d/%s admits=%d", rec.Code, rec.Body, backend.admits)
			}
		})
	}
}

func TestPublicRoutingSnapshotCachePartitionsCohortsWithStableProof(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	first, second := uuid.NewString(), uuid.NewString()
	weights := []DeploymentWeightsRow{{ID: first, TrafficPercent: 50}, {ID: second, TrafficPercent: 50}}
	h.WithPublicRoutingPolicy(func(ctx context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
		snapshot := publicSnapshotFixture(app, inputs)
		snapshot.Weights = weights
		return snapshot, nil
	})
	cache := NewResponseCache()
	h.WithResponseCache(cache)
	rule := EdgeRuleCacheResolved{ID: "public-cache", PathGlob: "/catalog", MaxAgeSeconds: 60}
	seedCacheRule(t, h, backend.host, rule)
	for _, deployment := range []string{first, second} {
		key := CacheKey{AppID: backend.app.ID, DeploymentID: deployment, RuleID: rule.ID, Method: http.MethodGet,
			NormalizedPath: "/catalog", VaryHash: hostVaryHash(backend.host)}
		cache.Put(key, http.StatusOK, nil, []byte(deployment), time.Now().Add(time.Minute), time.Now().Add(time.Minute), rule.toStateEdgeRuleCacheAction())
	}
	seen := map[string]bool{}
	proof := ""
	for index := 0; index < 64; index++ {
		r := httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/catalog", nil)
		r.Header.Set(api.VersionKeyHeader, fmt.Sprintf("customer-%d", index))
		want, _ := AffinityDeploymentFromWeights(backend.app.ID, r.Header.Get(api.VersionKeyHeader), weights)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Fatalf("cohort cache=%d/%s want %s", rec.Code, rec.Body, want)
		}
		seen[want] = true
		if proof == "" {
			proof = rec.Header().Get(TrafficPolicyRevisionHeader)
		} else if rec.Header().Get(TrafficPolicyRevisionHeader) != proof {
			t.Fatal("selection changed policy proof")
		}
	}
	if len(seen) != 2 || proof == "" || backend.admits != 0 {
		t.Fatalf("cache partitions=%v proof=%s admits=%d", seen, proof, backend.admits)
	}
}

func TestPublicRoutingSnapshotBrowserWakePageKeepsDetachedCohort(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	deployment := uuid.NewString()
	wrapped := &publicSnapshotBackend{fakeBackend: backend}
	h.backend = wrapped
	release, ready := make(chan struct{}), make(chan struct{})
	defer close(release)
	wrapped.onAdmit = func(ctx context.Context, selected, scope string, maximum int) error {
		if selected != deployment {
			return errors.New("wake changed cohort")
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		backend.AddTarget(Target{InstanceID: "browser", NodeID: backend.upstream, DeploymentID: deployment})
		close(ready)
		return nil
	}
	h.WithPublicRoutingPolicy(func(ctx context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
		snapshot := publicSnapshotFixture(app, inputs)
		snapshot.Weights = []DeploymentWeightsRow{{ID: deployment, TrafficPercent: 100}}
		return snapshot, nil
	})
	r := httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil)
	r.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK || rec.Header().Get(wakePageHeader) != "1" || !h.gate.WakeInProgress(backend.app.ID) {
		t.Fatalf("browser wake page=%d/%s active=%v", rec.Code, rec.Body, h.gate.WakeInProgress(backend.app.ID))
	}
	// Release without closing twice; the cleanup closes release on all failures.
	release <- struct{}{}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("detached exact wake did not complete")
	}
	retry := httptest.NewRecorder()
	h.ServeHTTP(retry, r.Clone(t.Context()))
	if retry.Code != http.StatusOK || retry.Body.String() != "hello from app" {
		t.Fatalf("wake retry=%d/%s", retry.Code, retry.Body)
	}
}

func TestPublicRoutingWakeQueueSharesCapAcrossCohorts(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.app.WakeMaxQueueDepth = 2
	wrapped := &publicSnapshotBackend{fakeBackend: backend}
	h.backend = wrapped
	deployment := uuid.NewString()
	release, started := make(chan struct{}), make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	wrapped.onAdmit = func(ctx context.Context, selected, _ string, _ int) error {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		backend.AddTarget(Target{InstanceID: selected, NodeID: "node", DeploymentID: selected})
		return nil
	}
	results := make(chan error, 2)
	start := func() {
		go func() {
			_, _, _, err := h.wakeDeployment(t.Context(), backend.app, deployment, "production", "gateway", 4)
			results <- err
		}()
	}
	start()
	<-started
	start()
	deadline := time.Now().Add(time.Second)
	for h.gate.InflightWaiters(backend.app.ID) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	_, _, _, err := h.wakeDeployment(t.Context(), backend.app, uuid.NewString(), "production", "gateway", 4)
	if !errors.Is(err, ErrQueueFull) || calls.Load() != 1 {
		t.Fatalf("cross-cohort queue cap bypassed: %v calls=%d", err, calls.Load())
	}
	release <- struct{}{}
	for index := 0; index < 2; index++ {
		if err := <-results; err != nil {
			t.Fatalf("coalesced waiter: %v", err)
		}
	}
}

func TestWakeGateOldCohortReleaseCannotRetireNewGeneration(t *testing.T) {
	gate := NewWakeGate(2, time.Second)
	old := &wakeCall{target: "old", waiters: 1, completed: true}
	current := &wakeCall{target: "new", waiters: 1}
	gate.inflight["app"] = current
	gate.release("app", old)
	if gate.inflight["app"] != current || gate.InflightWaiters("app") != 1 {
		t.Fatal("old generation removed the new cohort")
	}
}

func TestPublicRoutingSessionPreferenceStaysInVerifiedRoster(t *testing.T) {
	h, fixture, _ := newTestHandler(t)
	app := fixture.app
	app.SessionAffinity = true
	backend := NewPGBackend(nil, NewFakeScheduler("node"), nil)
	h.backend = backend
	for _, target := range []Target{{InstanceID: "preferred", DeploymentID: "first", NodeID: "node"},
		{InstanceID: "sibling", DeploymentID: "first", NodeID: "node"}, {InstanceID: "foreign", DeploymentID: "second", NodeID: "node"}} {
		backend.RecordTarget(app.ID, target)
	}
	for _, tc := range []struct {
		name, cookieApp, instance, version, want, reason string
		weights                                          []DeploymentWeightsRow
	}{
		{"eligible", app.ID, "preferred", "", "first", "session", []DeploymentWeightsRow{{ID: "first", TrafficPercent: 50}, {ID: "second", TrafficPercent: 50}}},
		{"retired", app.ID, "preferred", "", "second", "weighted", []DeploymentWeightsRow{{ID: "second", TrafficPercent: 100}}},
		{"stale", app.ID, "gone", "", "second", "weighted", []DeploymentWeightsRow{{ID: "second", TrafficPercent: 100}}},
		{"foreign-app", "foreign-app", "preferred", "", "second", "weighted", []DeploymentWeightsRow{{ID: "second", TrafficPercent: 100}}},
		{"version-first", app.ID, "preferred", "customer", "second", "version", []DeploymentWeightsRow{{ID: "second", TrafficPercent: 100}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cookie, err := h.sessionAffinityCookieValue(tc.cookieApp, tc.instance)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
			r.AddCookie(&http.Cookie{Name: sessionAffinityCookieName, Value: cookie})
			snapshot := PublicRoutingSnapshot{Weights: tc.weights}
			h.selectPublicRoutingDeployment(r, app, PublicRoutingInputs{VersionKey: tc.version}, &snapshot)
			if snapshot.SelectedDeploymentID != tc.want || snapshot.SelectionReason != tc.reason {
				t.Fatalf("session selection=%+v", snapshot)
			}
		})
	}
	if got := backend.PickForDeploymentInstance(app.ID, "first", "preferred"); !got.OK || got.Target.InstanceID != "preferred" {
		t.Fatalf("eligible instance preference=%+v", got)
	}
	if got := backend.PickForDeploymentInstance(app.ID, "first", "foreign"); !got.OK || got.Target.DeploymentID != "first" {
		t.Fatalf("foreign instance escaped cohort=%+v", got)
	}
	if got := backend.PickForDeploymentInstance(app.ID, "cold", "preferred"); got.OK {
		t.Fatalf("cold deployment fell back=%+v", got)
	}
	backend.RecordTarget(app.ID, Target{InstanceID: "preferred", DeploymentID: "first", NodeID: "node", RequiresReadiness: true})
	backend.SetInstanceReadiness(app.ID, "preferred", "unready", time.Now(), 1)
	if got := backend.PickForDeploymentInstance(app.ID, "first", "preferred"); !got.OK || got.Target.InstanceID != "sibling" {
		t.Fatalf("preferred instance bypassed readiness=%+v", got)
	}
}

func TestPublicRoutingCacheRefreshRetainsAdmittedCohort(t *testing.T) {
	h, fixture, _ := newTestHandler(t)
	first, second := uuid.NewString(), uuid.NewString()
	backend := &publicSnapshotBackend{fakeBackend: fixture}
	h.backend = backend
	weights := []DeploymentWeightsRow{{ID: first, TrafficPercent: 100}}
	h.WithPublicRoutingPolicy(func(ctx context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
		snapshot := publicSnapshotFixture(app, inputs)
		snapshot.Weights = weights
		return snapshot, nil
	})
	backend.onAdmit = func(ctx context.Context, deployment, scope string, maximum int) error {
		if deployment != first {
			return errors.New("refresh changed admitted deployment")
		}
		weights[0].ID = second
		fixture.AddTarget(Target{InstanceID: "first", DeploymentID: first, NodeID: "node"})
		fixture.AddTarget(Target{InstanceID: "second", DeploymentID: second, NodeID: "node"})
		return nil
	}
	cache := NewResponseCache()
	h.WithResponseCache(cache)
	rule := EdgeRuleCacheResolved{ID: "refresh-cohort", PathGlob: "/catalog", MaxAgeSeconds: 60, StaleIfErrorSeconds: 60}
	seedCacheRule(t, h, fixture.host, rule)
	key := CacheKey{AppID: fixture.app.ID, DeploymentID: first, RuleID: rule.ID, Method: http.MethodGet,
		NormalizedPath: "/catalog", VaryHash: hostVaryHash(fixture.host)}
	cache.Put(key, http.StatusOK, nil, []byte("stale"), time.Now().Add(-time.Second), time.Now().Add(time.Minute), rule.toStateEdgeRuleCacheAction())
	h.WithForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if target.DeploymentID != first {
				t.Errorf("cache refresh crossed deployment: %s", target.DeploymentID)
			}
			_, _ = w.Write([]byte(first))
		})
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+fixture.host+"/catalog", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "stale" {
		t.Fatalf("stale response=%d/%s", rec.Code, rec.Body)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		outcome, entry := cache.Get(key)
		if outcome == "fresh" && string(entry.body) == first {
			if backend.mixedReads.Load() != 0 {
				t.Fatal("refresh reread mutable routing")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("cohort cache refresh did not publish")
}

type publicSnapshotBurstBackend struct {
	*publicSnapshotBackend
	started, release chan struct{}
	calls            atomic.Int32
	selected         string
	maximum, count   int
}

func (b *publicSnapshotBurstBackend) AdmitDeploymentBurst(ctx context.Context, app, deployment, scope, trigger string, maximum, count int) (int, error) {
	b.calls.Add(1)
	b.selected, b.maximum, b.count = deployment, maximum, count
	close(b.started)
	select {
	case <-b.release:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	for index := 0; index < count; index++ {
		b.AddTarget(Target{InstanceID: fmt.Sprintf("burst-%d", index), DeploymentID: deployment, NodeID: "node"})
	}
	return count, nil
}

func TestPublicRoutingBurstKeepsSingleAppWorkerAndSelectedDeployment(t *testing.T) {
	h, fixture, _ := newTestHandler(t)
	backend := &publicSnapshotBurstBackend{publicSnapshotBackend: &publicSnapshotBackend{fakeBackend: fixture}, started: make(chan struct{}), release: make(chan struct{})}
	h.backend = backend
	fixture.AddTarget(Target{InstanceID: "existing", DeploymentID: "first", NodeID: "node"})
	state := h.burstPressure.state(fixture.app.ID)
	state.inflight.Store(3)
	defer state.inflight.Store(0)
	routing := PublicRoutingSnapshot{SelectedDeploymentID: "first", Scope: "production", SelectionReason: "weighted"}
	if waited, err := h.maybePublicRoutingBurst(t.Context(), fixture.app, 3, 1, routing); waited || err != nil {
		t.Fatalf("ready request blocked for expansion: %v %v", waited, err)
	}
	<-backend.started
	state.mu.Lock()
	generation := state.worker
	state.mu.Unlock()
	routing.SelectedDeploymentID = "second"
	if _, err := h.maybePublicRoutingBurst(t.Context(), fixture.app, 3, 1, routing); err != nil {
		t.Fatal(err)
	}
	if backend.calls.Load() != 1 || backend.selected != "first" || backend.maximum != 3 || backend.count != 2 {
		t.Fatalf("burst changed cohort or cap: %s/%d/%d calls=%d", backend.selected, backend.maximum, backend.count, backend.calls.Load())
	}
	close(backend.release)
	select {
	case <-generation.done:
	case <-time.After(time.Second):
		t.Fatal("capacity worker retained ownership")
	}
	if generation.err != nil || fixture.HealthyCount(fixture.app.ID) != 3 {
		t.Fatalf("burst did not fill admitted cohort: %v", generation.err)
	}
}
