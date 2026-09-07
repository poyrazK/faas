package main

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type startupVerifierFunc func(context.Context, string, string) error

func (f startupVerifierFunc) Verify(ctx context.Context, layer, sig string) error {
	return f(ctx, layer, sig)
}

type startupDeploymentListerFunc func(context.Context) ([]state.Deployment, error)

func (f startupDeploymentListerFunc) ListAllDeployments(ctx context.Context) ([]state.Deployment, error) {
	return f(ctx)
}

func TestPrepareAttestationsBoundsWorkAndKeepsFailuresIsolated(t *testing.T) {
	deps := []state.Deployment{{Status: state.DeployLive, RootfsKey: "one"}, {Status: state.DeployLive, RootfsKey: "two"}, {Status: state.DeployLive, RootfsKey: "bad"}, {Status: state.DeployLive, RootfsKey: "one"}, {Status: state.DeployFailed, RootfsKey: "excluded"}}
	var mu sync.Mutex
	seen := map[string]int{}
	active, maxActive := 0, 0
	verify := startupVerifierFunc(func(ctx context.Context, key, sig string) error {
		if sig != "sigs/"+key+".sig" {
			t.Errorf("wrong signature key %q", sig)
		}
		mu.Lock()
		seen[key]++
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
		if key == "bad" {
			return errors.New("invalid signature")
		}
		return nil
	})
	if err := prepareLayerAttestations(context.Background(), deps, verify, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 || seen["one"] != 1 || seen["two"] != 1 || seen["bad"] != 1 {
		t.Fatalf("verified keys: %v", seen)
	}
	if active != 0 || maxActive > api.StartupAttestationWorkers {
		t.Fatalf("active=%d max=%d", active, maxActive)
	}
}

func TestPrepareAttestationsCancellationCannotOpenReadiness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	verify := startupVerifierFunc(func(ctx context.Context, _, _ string) error { cancel(); <-ctx.Done(); return ctx.Err() })
	err := prepareLayerAttestations(ctx, []state.Deployment{{Status: state.DeployLive, RootfsKey: "one"}}, verify, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
}

func TestStartLayerAttestationWarmDoesNotBlockReadiness(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	verify := startupVerifierFunc(func(context.Context, string, string) error {
		close(started)
		<-release
		return nil
	})
	done := startLayerAttestationWarm(
		context.Background(),
		startupDeploymentListerFunc(func(context.Context) ([]state.Deployment, error) {
			return []state.Deployment{{Status: state.DeployLive, RootfsKey: "slow-remote-layer"}}, nil
		}),
		verify,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background attestation warm did not start")
	}
	select {
	case <-done:
		t.Fatal("background attestation warm completed before verifier was released")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("background attestation warm did not finish")
	}
}
