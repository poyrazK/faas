package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemRevisionPinStableCutoverAndExpiry(t *testing.T) {
	m, ctx, _, app, old := memDeploymentFixture(t)
	m.mu.Lock()
	storedApp := m.apps[app.ID]
	storedApp.Manifest.RevisionPinTTLSeconds = 60
	m.apps[app.ID] = storedApp
	m.mu.Unlock()
	if err := m.MarkDeploymentLive(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	next, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:next"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ResolveRevisionPin(ctx, app.ID, DefaultEnvScope, old.ID); err != nil || got.ID != old.ID || got.TrafficPercent != 0 {
		t.Fatalf("old pin = %+v, %v", got, err)
	}
	if got, err := m.ResolveRevisionPin(ctx, app.ID, DefaultEnvScope, next.ID); err != nil || got.TrafficPercent != 100 {
		t.Fatalf("current pin = %+v, %v", got, err)
	}
	if _, err := m.ResolveRevisionPin(ctx, uuid.NewString(), DefaultEnvScope, old.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign pin: %v", err)
	}
	m.mu.Lock()
	m.revisionPins[old.ID] = time.Now().Add(-time.Second)
	m.mu.Unlock()
	if _, err := m.ResolveRevisionPin(ctx, app.ID, DefaultEnvScope, old.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired pin: %v", err)
	}
	if count, err := m.ExpireRevisionPins(context.Background()); err != nil || count != 1 {
		t.Fatalf("expire count=%d err=%v", count, err)
	}
	if got, _ := m.DeploymentByID(ctx, old.ID); got.Status != DeploySuperseded {
		t.Fatalf("expired status = %q", got.Status)
	}
}
