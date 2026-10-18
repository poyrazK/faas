package main

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// A compute node's gatewayd-internal may hold its data listener in a
// systemd socket (ADR-2026101801). While it restarts, dials to it still connect
// and requests wait in the socket's backlog. The pool probes every node and
// skips one whose gatewayd-internal has not answered /healthz within
// restartWait: new requests wait for a restarting node at most that long
// before failing over. Requests already queued there wait for the restart.

// computeGatewayProbeInterval is how often each node is probed.
const computeGatewayProbeInterval = time.Second

// computeGatewayRestartWaitEnv overrides api.ComputeGatewayRestartWaitSeconds.
const computeGatewayRestartWaitEnv = "FAAS_COMPUTE_GATEWAY_RESTART_WAIT"

// computeGatewayRestartWait reads the configured cap; an unset, invalid or
// non-positive value keeps the default.
func computeGatewayRestartWait(getenv func(string) string) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(getenv(computeGatewayRestartWaitEnv))); err == nil && d > 0 {
		return d
	}
	return time.Duration(api.ComputeGatewayRestartWaitSeconds) * time.Second
}

// probeComputeGateway asks one node's gatewayd-internal for /healthz.
func probeComputeGateway(ctx context.Context, address string) error {
	return checkInternalGateway(ctx, gateway.NewTCPDialer(address), &url.URL{Scheme: "http", Host: address})
}

// computeGatewayReadiness records which nodes answered their last probe.
type computeGatewayReadiness struct {
	wait  time.Duration
	probe func(ctx context.Context, address string) error

	mu       sync.Mutex
	unready  map[string]bool
	inFlight map[string]bool
}

func newComputeGatewayReadiness(wait time.Duration) *computeGatewayReadiness {
	if wait <= 0 {
		wait = time.Duration(api.ComputeGatewayRestartWaitSeconds) * time.Second
	}
	return &computeGatewayReadiness{
		wait:     wait,
		probe:    probeComputeGateway,
		unready:  map[string]bool{},
		inFlight: map[string]bool{},
	}
}

// ready reports whether address answered its last probe. A node never
// probed is ready, so a new node takes traffic at once.
func (r *computeGatewayReadiness) ready(address string) bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.unready[address]
}

// order puts ready endpoints first, keeping their round-robin order. Unready
// ones stay last: a dial still reaches them when no ready node accepts.
func (r *computeGatewayReadiness) order(endpoints []computeGatewayEndpoint, start int) []computeGatewayEndpoint {
	ordered := make([]computeGatewayEndpoint, 0, len(endpoints))
	var waiting []computeGatewayEndpoint
	for offset := range endpoints {
		endpoint := endpoints[(start+offset)%len(endpoints)]
		if r.ready(endpoint.address) {
			ordered = append(ordered, endpoint)
		} else {
			waiting = append(waiting, endpoint)
		}
	}
	return append(ordered, waiting...)
}

// probeAll probes every endpoint concurrently, each within the cap, and
// returns when all have answered or timed out. An endpoint still being
// probed from an earlier round is skipped. Addresses no longer in the
// inventory are forgotten.
func (r *computeGatewayReadiness) probeAll(ctx context.Context, endpoints []computeGatewayEndpoint, log *slog.Logger) {
	current := make(map[string]bool, len(endpoints))
	var wg sync.WaitGroup
	for _, endpoint := range endpoints {
		current[endpoint.address] = true
		r.mu.Lock()
		busy := r.inFlight[endpoint.address]
		r.inFlight[endpoint.address] = true
		r.mu.Unlock()
		if busy {
			continue
		}
		wg.Go(func() {
			probeCtx, cancel := context.WithTimeout(ctx, r.wait)
			err := r.probe(probeCtx, endpoint.address)
			cancel()
			r.mu.Lock()
			was := !r.unready[endpoint.address]
			r.unready[endpoint.address] = err != nil
			delete(r.inFlight, endpoint.address)
			r.mu.Unlock()
			switch {
			case err != nil && was && ctx.Err() == nil:
				log.Warn("compute gateway not answering; routing to other nodes", "node", endpoint.name, "target", endpoint.address, "wait", r.wait, "err", err)
			case err == nil && !was:
				log.Info("compute gateway answering again", "node", endpoint.name, "target", endpoint.address)
			}
		})
	}
	wg.Wait()
	r.mu.Lock()
	for address := range r.unready {
		if !current[address] {
			delete(r.unready, address)
		}
	}
	r.mu.Unlock()
}

// WatchReadiness probes the pool's nodes until ctx ends.
func (p *computeGatewayPool) WatchReadiness(ctx context.Context) {
	tick := time.NewTicker(computeGatewayProbeInterval)
	defer tick.Stop()
	for {
		p.mu.Lock()
		endpoints := append([]computeGatewayEndpoint(nil), p.endpoints...)
		p.mu.Unlock()
		go p.readiness.probeAll(ctx, endpoints, p.log)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// withRestartWait sets the cap from the environment.
func (p *computeGatewayPool) withRestartWait() *computeGatewayPool {
	p.readiness = newComputeGatewayReadiness(computeGatewayRestartWait(os.Getenv))
	return p
}
