package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

type idleValidationBackend struct {
	*fakeBackend
	validateCalls int
}

func (b *idleValidationBackend) PickWarm(appID string) PickResult {
	return b.fakeBackend.Pick(appID)
}

func (b *idleValidationBackend) ValidateLiveTarget(_ context.Context, appID, instanceID string) (bool, error) {
	b.validateCalls++
	b.mu.Lock()
	defer b.mu.Unlock()
	kept := b.targets[:0]
	for _, target := range b.targets {
		if target.InstanceID != instanceID {
			kept = append(kept, target)
		}
	}
	b.targets = kept
	b.running = false
	return false, nil
}

func TestWarmTargetNeedsValidationAfterIdleWindow(t *testing.T) {
	now := time.Now()
	h := &Handler{}
	app := App{Plan: api.PlanFree, IdleTimeoutS: 10}
	if h.warmTargetNeedsValidation(app, Target{InstanceID: "instance-1", AddedAt: now.Add(-9 * time.Second)}, now) {
		t.Fatal("fresh target unexpectedly requires validation")
	}
	if !h.warmTargetNeedsValidation(app, Target{InstanceID: "instance-1", AddedAt: now.Add(-10 * time.Second)}, now) {
		t.Fatal("idle-aged target did not require validation")
	}
}

func TestIdleParkedTargetRestoresWithinCurrentRequest(t *testing.T) {
	h, fake, _ := newTestHandler(t)
	fake.app.Plan = api.PlanFree
	fake.app.IdleTimeoutS = 1
	fake.wakeMethodOut = WakeMethodSnapshotRestore
	fake.AddTarget(Target{
		NodeID:       fake.upstream,
		InstanceID:   "parked-instance",
		DeploymentID: "deployment-1",
		AddedAt:      time.Now().Add(-2 * time.Second),
	})
	backend := &idleValidationBackend{fakeBackend: fake}
	h.backend = backend

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body)
	}
	if got := rec.Header().Get(wire.WakeHeader); got != wire.RestoredWakeValue {
		t.Fatalf("wake tier = %q, want %q", got, wire.RestoredWakeValue)
	}
	if backend.validateCalls != 1 {
		t.Fatalf("validation calls = %d, want 1", backend.validateCalls)
	}
	if got := *fake.Admits(); got != 1 {
		t.Fatalf("admits = %d, want 1 synchronous restore", got)
	}
}

func TestPGBackendValidateLiveTargetEvictsParkedEntry(t *testing.T) {
	b := NewPGBackend(nil, nil, nil).WithLiveTargetLoader(func(context.Context, string) ([]Target, error) {
		return nil, nil
	})
	b.RecordTarget("app-1", Target{NodeID: "node-1", InstanceID: "instance-1", DeploymentID: "deployment-1"})

	live, err := b.ValidateLiveTarget(context.Background(), "app-1", "instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if live {
		t.Fatal("live = true, want false")
	}
	if got := b.HealthyCount("app-1"); got != 0 {
		t.Fatalf("HealthyCount = %d, want 0 after validation", got)
	}
}

func TestPGBackendTouchTargetAdvancesActivityStamp(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	old := time.Now().Add(-time.Minute)
	want := time.Now()
	b.RecordTarget("app-1", Target{NodeID: "node-1", InstanceID: "instance-1", DeploymentID: "deployment-1", AddedAt: old})
	b.TouchTarget("app-1", "instance-1", want)

	pick := b.Pick("app-1")
	if !pick.OK {
		t.Fatal("Pick returned no target")
	}
	if !pick.Target.AddedAt.Equal(want) {
		t.Fatalf("AddedAt = %s, want %s", pick.Target.AddedAt, want)
	}
}
