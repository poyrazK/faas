package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type startupLayerVerifier interface {
	Verify(context.Context, string, string) error
}

type attestationWarmStore interface {
	ListAllApps(context.Context) ([]state.App, error)
	ListAppsByNodeID(context.Context, string) ([]state.App, error)
	LiveDeployments(context.Context, string) ([]state.Deployment, error)
}

// layerAttestationWarm primes verified layers without holding schedd's
// readiness gate behind remote storage. The verifier is already attached to
// the engine before it starts, so every wake remains fail-closed while the
// best-effort cache warm runs in the background.
//
// owns limits the warm to apps this schedd wakes. Verifying a layer reads and
// hashes the whole multi-GB image: on production-us the owner-less
// control-plane schedd verified all 46 live layers after every restart, held
// both of the host's CPUs for 515 s and starved meterd's start past its
// systemd timeout, which rolled the rc.246 release back (hunt #5, H5-52).
// A nil owns warms every live layer (single-box).
//
// The warm repeats every interval because ownership moves after startup. A
// rolling rollout restarts each compute schedd while its apps are homed on
// the peer and re-homes them minutes later, so a startup-only warm verified
// nothing: every first wake then hashed its whole layer inside the wake path,
// 22-25 s per wake in a 30-wake burst on production-us (hunt #6, H5-56). A
// later pass verifies only owned live layers that have not verified yet; a
// layer that failed waits api.AttestationWarmRetryBackoff before the next
// attempt. interval <= 0 runs a single pass.
type layerAttestationWarm struct {
	store       attestationWarmStore
	verifier    startupLayerVerifier
	ownerNodeID string
	owns        func(state.App) bool
	interval    time.Duration
	log         *slog.Logger
	now         func() time.Time

	verified   map[string]bool
	retryAfter map[string]time.Time
}

func (w *layerAttestationWarm) start(ctx context.Context) <-chan struct{} {
	if w.now == nil {
		w.now = time.Now
	}
	w.verified = map[string]bool{}
	w.retryAfter = map[string]time.Time{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.pass(ctx, true)
		if w.interval <= 0 {
			return
		}
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.pass(ctx, false)
			}
		}
	}()
	return done
}

func (w *layerAttestationWarm) pass(ctx context.Context, startup bool) {
	keys, err := w.ownedLiveLayers(ctx)
	if err != nil {
		if ctx.Err() == nil {
			w.log.Warn("layer attestation warm: list owned layers", "startup", startup, "err", err)
		}
		return
	}
	now := w.now()
	var pending []string
	for key := range keys {
		if !w.verified[key] && !now.Before(w.retryAfter[key]) {
			pending = append(pending, key)
		}
	}
	for key := range w.verified {
		if !keys[key] {
			delete(w.verified, key)
		}
	}
	for key := range w.retryAfter {
		if !keys[key] {
			delete(w.retryAfter, key)
		}
	}
	if !startup && len(pending) == 0 {
		return
	}
	started := time.Now()
	verified, failed, err := prepareLayerAttestations(ctx, pending, w.verifier, w.log)
	for _, key := range verified {
		w.verified[key] = true
		delete(w.retryAfter, key)
	}
	for _, key := range failed {
		w.retryAfter[key] = now.Add(api.AttestationWarmRetryBackoff)
	}
	if err != nil {
		if ctx.Err() == nil {
			w.log.Warn("layer attestation warm failed", "startup", startup, "err", err)
		}
		return
	}
	msg := "layer attestations prepared"
	if startup {
		msg = "startup: layer attestations prepared"
	}
	w.log.Info(msg, "verified", len(verified), "failed", len(failed), "owned_layers", len(keys), "duration_ms", time.Since(started).Milliseconds())
}

// ownedLiveLayers returns the rootfs keys of every live deployment of an app
// this schedd owns, listed the same way the floor reconciler lists its apps.
func (w *layerAttestationWarm) ownedLiveLayers(ctx context.Context) (map[string]bool, error) {
	var apps []state.App
	var err error
	if w.ownerNodeID != "" {
		apps, err = w.store.ListAppsByNodeID(ctx, w.ownerNodeID)
	} else {
		apps, err = w.store.ListAllApps(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}
	keys := map[string]bool{}
	for _, app := range apps {
		if w.owns != nil && !w.owns(app) {
			continue
		}
		deployments, err := w.store.LiveDeployments(ctx, app.ID)
		if err != nil {
			return nil, fmt.Errorf("live deployments for app %s: %w", app.ID, err)
		}
		for _, dep := range deployments {
			if dep.Status == state.DeployLive && dep.RootfsKey != "" {
				keys[dep.RootfsKey] = true
			}
		}
	}
	return keys, nil
}

// prepareLayerAttestations verifies each layer with bounded workers to prime
// only successful cryptographic attestations, without booting or restoring a
// VM. A broken tenant artifact remains rejected by the ordinary wake verifier;
// it does not prevent unrelated valid deployments from becoming available.
func prepareLayerAttestations(ctx context.Context, layerKeys []string, verifier startupLayerVerifier, log *slog.Logger) (verified, failed []string, err error) {
	keys := make(chan string)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < api.AttestationWarmWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range keys {
				checkCtx, cancel := context.WithTimeout(ctx, api.AttestationWarmLayerTimeout)
				err := verifier.Verify(checkCtx, key, "sigs/"+key+".sig")
				cancel()
				mu.Lock()
				if err != nil {
					failed = append(failed, key)
				} else {
					verified = append(verified, key)
				}
				mu.Unlock()
				if err != nil && ctx.Err() == nil {
					log.Warn("layer attestation unavailable", "layer", key, "err", err)
				}
			}
		}()
	}
	for _, key := range layerKeys {
		select {
		case keys <- key:
		case <-ctx.Done():
			close(keys)
			wg.Wait()
			return verified, failed, fmt.Errorf("layer attestations: %w", ctx.Err())
		}
	}
	close(keys)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return verified, failed, fmt.Errorf("layer attestations: %w", err)
	}
	return verified, failed, nil
}
