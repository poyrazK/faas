package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRevisionPinStableCutoverAndExpiry(t *testing.T) {
	m, ctx, _, app, old := state.MemDeploymentFixture(t)
	(*m.Mu()).Lock()
	storedApp := (*m.Apps())[app.ID]
	storedApp.Manifest.RevisionPinTTLSeconds = 60
	(*m.Apps())[app.ID] = storedApp
	(*m.Mu()).Unlock()
	if err := m.MarkDeploymentLive(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	next, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:next"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ResolveRevisionPin(ctx, app.ID, state.DefaultEnvScope, old.ID); err != nil || got.ID != old.ID || got.TrafficPercent != 0 {
		t.Fatalf("old pin = %+v, %v", got, err)
	}
	if got, err := m.ResolveRevisionPin(ctx, app.ID, state.DefaultEnvScope, next.ID); err != nil || got.TrafficPercent != 100 {
		t.Fatalf("current pin = %+v, %v", got, err)
	}
	if _, err := m.ResolveRevisionPin(ctx, uuid.NewString(), state.DefaultEnvScope, old.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign pin: %v", err)
	}
	(*m.Mu()).Lock()
	(*m.RevisionPins())[old.ID] = time.Now().Add(-time.Second)
	(*m.Mu()).Unlock()
	if _, err := m.ResolveRevisionPin(ctx, app.ID, state.DefaultEnvScope, old.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired pin: %v", err)
	}
	if count, err := m.ExpireRevisionPins(context.Background()); err != nil || count != 1 {
		t.Fatalf("expire count=%d err=%v", count, err)
	}
	if got, _ := m.DeploymentByID(ctx, old.ID); got.Status != state.DeploySuperseded {
		t.Fatalf("expired status = %q", got.Status)
	}
}
