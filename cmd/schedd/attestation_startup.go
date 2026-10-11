package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type startupLayerVerifier interface {
	Verify(context.Context, string, string) error
}

type startupDeploymentLister interface {
	ListAllDeployments(context.Context) ([]state.Deployment, error)
}

// startLayerAttestationWarm primes verified layers without holding schedd's
// readiness gate behind remote storage. The verifier is already attached to
// the engine before this starts, so every wake remains fail-closed while the
// best-effort cache warm runs in the background.
//
// owns limits the warm to apps this schedd wakes. Verifying a layer reads and
// hashes the whole multi-GB image: on production-us the owner-less
// control-plane schedd verified all 46 live layers after every restart, held
// both of the host's CPUs for 515 s and starved meterd's start past its
// systemd timeout, which rolled the rc.246 release back (hunt #5, H5-52).
// A nil owns warms every live layer (single-box).
func startLayerAttestationWarm(ctx context.Context, lister startupDeploymentLister, verifier startupLayerVerifier, owns func(appID string) bool, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		deployments, err := lister.ListAllDeployments(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("startup: list layer attestations for warm", "err", err)
			}
			return
		}
		if owns != nil {
			kept := deployments[:0]
			for _, dep := range deployments {
				if owns(dep.AppID) {
					kept = append(kept, dep)
				}
			}
			deployments = kept
		}
		if err := prepareLayerAttestations(ctx, deployments, verifier, log); err != nil && ctx.Err() == nil {
			log.Warn("startup: layer attestation warm failed", "err", err)
		}
	}()
	return done
}

// Verify live deployment layers to prime only successful cryptographic
// attestations, without booting or restoring a VM.
// A broken tenant artifact remains rejected by the ordinary wake verifier;
// it does not prevent unrelated valid deployments from becoming available.
func prepareLayerAttestations(ctx context.Context, deployments []state.Deployment, verifier startupLayerVerifier, log *slog.Logger) error {
	keys := make(chan string)
	var wg sync.WaitGroup
	var verified, failed atomic.Int64
	started := time.Now()
	for i := 0; i < api.StartupAttestationWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range keys {
				checkCtx, cancel := context.WithTimeout(ctx, api.StartupAttestationLayerTimeout)
				err := verifier.Verify(checkCtx, key, "sigs/"+key+".sig")
				cancel()
				if err != nil {
					failed.Add(1)
					log.Warn("startup: layer attestation unavailable", "layer", key, "err", err)
				} else {
					verified.Add(1)
				}
			}
		}()
	}
	seen := map[string]bool{}
	for _, dep := range deployments {
		if dep.Status != state.DeployLive || dep.RootfsKey == "" || seen[dep.RootfsKey] {
			continue
		}
		seen[dep.RootfsKey] = true
		select {
		case keys <- dep.RootfsKey:
		case <-ctx.Done():
			close(keys)
			wg.Wait()
			return fmt.Errorf("startup attestations: %w", ctx.Err())
		}
	}
	close(keys)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("startup attestations: %w", err)
	}
	log.Info("startup: layer attestations prepared", "verified", verified.Load(), "failed", failed.Load(), "duration_ms", time.Since(started).Milliseconds())
	return nil
}
