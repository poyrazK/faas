// adr: 570
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTargetHydrationUsesCurrentPlacementReader(t *testing.T) {
	for _, path := range []string{"reconcile", "refresh", "validate", "at capacity"} {
		t.Run(path, func(t *testing.T) {
			target := placementFixture("app", "instance")
			reads, legacyReads := 0, 0
			b := NewPGBackend(nil, nil, nil).WithLiveTargetLoader(func(context.Context, string) ([]Target, error) {
				legacyReads++
				return []Target{target}, nil
			}).WithTargetPlacementLoader(func(ctx context.Context, apps []string) (map[string]TargetPlacementSnapshot, error) {
				reads++
				if len(apps) != 1 || apps[0] != target.AppID {
					t.Fatalf("unscoped hydration: %v", apps)
				}
				if _, bounded := ctx.Deadline(); !bounded {
					t.Fatal("current hydration has no read deadline")
				}
				return map[string]TargetPlacementSnapshot{target.AppID: placementSnapshot(target.AppID, target)}, nil
			})
			var err error
			switch path {
			case "reconcile":
				err = b.ReconcileLiveTargets(t.Context(), target.AppID)
			case "refresh":
				err = b.RefreshLiveTargets(t.Context(), target.AppID)
			case "validate":
				b.RecordTarget(target.AppID, target)
				var live bool
				live, err = b.ValidateLiveTarget(t.Context(), target.AppID, target.InstanceID)
				if !live {
					t.Errorf("current target refused: %v", err)
				}
			case "at capacity":
				var atCapacity bool
				_, _, atCapacity, err = b.recordAdmission(t.Context(), target.AppID, target.DeploymentID, "", "", "", "", 0, true, 0)
				if !atCapacity {
					t.Fatal("hydration changed the scheduler's typed capacity result")
				}
			}
			if err != nil || reads != 1 || legacyReads != 0 || b.CapacityCount(target.AppID) != 1 || !b.Pick(target.AppID).OK {
				t.Fatalf("current reader bypassed: path=%s reads=%d legacyReads=%d capacity=%d pick=%+v err=%v", path, reads, legacyReads, b.CapacityCount(target.AppID), b.Pick(target.AppID), err)
			}
		})
	}
}

func TestTargetHydrationIncompleteReadRetainsCapacityAndRefusesTraffic(t *testing.T) {
	for _, path := range []string{"refresh", "validate", "at capacity"} {
		for _, reason := range []string{"store error", "missing result", "partial result"} {
			t.Run(path+"/"+reason, func(t *testing.T) {
				target := placementFixture("app", "instance")
				snapshot := placementSnapshot(target.AppID, target)
				failure := errors.New("placement store unavailable")
				reads, legacyReads := 0, 0
				b := NewPGBackend(nil, nil, nil).WithLiveTargetLoader(func(context.Context, string) ([]Target, error) {
					legacyReads++
					return []Target{target}, nil
				}).WithTargetPlacementLoader(func(context.Context, []string) (map[string]TargetPlacementSnapshot, error) {
					reads++
					switch reason {
					case "store error":
						return nil, failure
					case "missing result":
						return nil, nil
					default:
						snapshot.Complete = false
						return map[string]TargetPlacementSnapshot{target.AppID: snapshot}, nil
					}
				})
				b.RecordTarget(target.AppID, target)
				var err error
				switch path {
				case "refresh":
					err = b.RefreshLiveTargets(t.Context(), target.AppID)
				case "validate":
					_, err = b.ValidateLiveTarget(t.Context(), target.AppID, target.InstanceID)
				case "at capacity":
					_, _, _, err = b.recordAdmission(t.Context(), target.AppID, target.DeploymentID, "", "", "", "", 0, true, 0)
				}
				if err == nil || (reason == "store error" && !errors.Is(err, failure)) || reads != 1 || legacyReads != 0 || b.CapacityCount(target.AppID) != 1 || b.Pick(target.AppID).OK {
					t.Fatalf("incomplete placement routed or removed capacity: reads=%d legacy=%d capacity=%d pick=%+v err=%v", reads, legacyReads, b.CapacityCount(target.AppID), b.Pick(target.AppID), err)
				}
			})
		}
	}
}

func TestTargetHydrationValidationRejectsChangedPlacement(t *testing.T) {
	for _, field := range []string{"wake", "node", "port", "deployment"} {
		t.Run(field, func(t *testing.T) {
			old := placementFixture("app", "instance")
			current := old
			switch field {
			case "wake":
				current.WakeID = "replacement"
			case "node":
				current.NodeID = "destination"
			case "port":
				current.Port = 9090
			case "deployment":
				current.DeploymentID = "replacement"
			}
			b := NewPGBackend(nil, nil, nil).WithLiveTargetLoader(func(context.Context, string) ([]Target, error) {
				return []Target{current}, nil
			}).WithTargetPlacementLoader(func(context.Context, []string) (map[string]TargetPlacementSnapshot, error) {
				return map[string]TargetPlacementSnapshot{old.AppID: placementSnapshot(old.AppID, current)}, nil
			})
			b.RecordTarget(old.AppID, old)
			live, err := b.ValidateLiveTarget(t.Context(), old.AppID, old.InstanceID)
			pick := b.PickForDeployment(old.AppID, current.DeploymentID)
			if field == "deployment" && b.Pick(old.AppID).OK {
				t.Fatal("placement validation rewrote the admitted deployment weights")
			}
			if err != nil || live || !pick.OK || !sameTargetPlacement(pick.Target, current) || b.CapacityCount(old.AppID) != 1 {
				t.Fatalf("old routing tuple survived validation: live=%t pick=%+v capacity=%d err=%v", live, pick, b.CapacityCount(old.AppID), err)
			}
		})
	}
}

func TestTargetHydrationRefreshRemovesConfirmedAbsent(t *testing.T) {
	target := placementFixture("app", "stopped")
	b := NewPGBackend(nil, nil, nil).WithLiveTargetLoader(func(context.Context, string) ([]Target, error) {
		return nil, nil
	}).WithTargetPlacementLoader(func(context.Context, []string) (map[string]TargetPlacementSnapshot, error) {
		return map[string]TargetPlacementSnapshot{target.AppID: placementSnapshot(target.AppID)}, nil
	})
	b.RecordTarget(target.AppID, target)
	if err := b.RefreshLiveTargets(t.Context(), target.AppID); err != nil {
		t.Fatal(err)
	}
	if b.CapacityCount(target.AppID) != 0 || b.Pick(target.AppID).OK {
		t.Fatal("complete absence did not remove the stale resident")
	}
}

type hydrationFailedValidator struct {
	*fakeBackend
	validateCalls int
}

func (b *hydrationFailedValidator) PickWarm(app string) PickResult { return b.fakeBackend.Pick(app) }
func (b *hydrationFailedValidator) ValidateLiveTarget(context.Context, string, string) (bool, error) {
	b.validateCalls++
	return false, errors.New("authoritative placement unavailable")
}

func TestTargetHydrationIdleReadFailureDoesNotForward(t *testing.T) {
	h, fake, _ := newTestHandler(t)
	fake.app.IdleTimeoutS = 1
	fake.AddTarget(Target{NodeID: fake.upstream, InstanceID: "instance", DeploymentID: "deployment", AddedAt: time.Now().Add(-time.Minute)})
	backend := &hydrationFailedValidator{fakeBackend: fake}
	h.backend = backend
	forwards := 0
	h.proxyByNode = func(Target) http.Handler {
		forwards++
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil))
	if backend.validateCalls != 1 || response.Code != http.StatusServiceUnavailable || forwards != 0 || *fake.Admits() != 0 {
		t.Fatalf("failed placement validation delivered traffic: checks=%d status=%d forwards=%d admits=%d", backend.validateCalls, response.Code, forwards, *fake.Admits())
	}
}

type hydrationChangedValidator struct {
	*fakeBackend
	current       Target
	validateCalls int
}

func (b *hydrationChangedValidator) PickWarm(app string) PickResult { return b.fakeBackend.Pick(app) }
func (b *hydrationChangedValidator) ValidateLiveTarget(context.Context, string, string) (bool, error) {
	b.validateCalls++
	b.mu.Lock()
	b.targets = []Target{b.current}
	b.mu.Unlock()
	return true, nil
}

func TestTargetHydrationIdleValidationReselectsWithinAdmittedDeployment(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		name := "ordinary"
		if pinned {
			name = "pinned deployment"
		}
		t.Run(name, func(t *testing.T) {
			h, fake, _ := newTestHandler(t)
			fake.app.IdleTimeoutS = 1
			old := Target{AppID: fake.app.ID, NodeID: fake.upstream, InstanceID: "instance", WakeID: "old-wake", DeploymentID: "old-deployment", AddedAt: time.Now().Add(-time.Minute)}
			current := old
			current.NodeID, current.WakeID, current.Port, current.DeploymentID, current.AddedAt = "destination", "new-wake", 9090, "new-deployment", time.Now()
			fake.AddTarget(old)
			if pinned {
				fake.app.PinnedDeploymentID = old.DeploymentID
			}
			backend := &hydrationChangedValidator{fakeBackend: fake, current: current}
			h.backend = backend
			var forwarded []Target
			h.proxyByNode = func(target Target) http.Handler {
				forwarded = append(forwarded, target)
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil))
			if backend.validateCalls != 1 || *fake.Admits() != 0 {
				t.Fatalf("validation unexpectedly admitted capacity: checks=%d admits=%d", backend.validateCalls, *fake.Admits())
			}
			if pinned {
				if response.Code != http.StatusServiceUnavailable || len(forwarded) != 0 {
					t.Fatalf("validation escaped admitted deployment: status=%d forwarded=%+v", response.Code, forwarded)
				}
			} else {
				// Warm wire requests carry no wake correlation. The picker still
				// retains the replacement wake for lifetime-scoped validation.
				wireTarget := current
				wireTarget.WakeID = ""
				if response.Code != http.StatusNoContent || len(forwarded) != 1 || !sameTargetPlacement(forwarded[0], wireTarget) || backend.Pick(fake.app.ID).Target.WakeID != current.WakeID {
					t.Fatalf("validation forwarded stale placement: status=%d forwarded=%+v", response.Code, forwarded)
				}
			}
		})
	}
}
