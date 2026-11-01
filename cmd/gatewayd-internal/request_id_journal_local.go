// adr: 20261101085552
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/requestjournal"
	"github.com/onebox-faas/faas/pkg/state"
)

// requestIDJournalSweepInterval is how often the node-local journal closes
// due segments and removes expired or over-budget ones.
const requestIDJournalSweepInterval = time.Minute

// startLocalRequestIDJournal opens the node-local journal in the unit's state
// directory, serves apid's lookups for it over client's connection, and sweeps
// it until ctx ends. stop closes the store after the sweeper and the lookup
// stream have exited.
func startLocalRequestIDJournal(ctx context.Context, client *apidgrpc.RequestTelemetryClientImpl, node string, metrics *gateway.Metrics, log *slog.Logger) (*requestjournal.Store, func(), error) {
	dir := os.Getenv("STATE_DIRECTORY") // set by systemd from StateDirectory=
	if dir == "" {
		dir = "/var/lib/faas-gatewayd-internal"
	}
	store, err := requestjournal.Open(filepath.Join(dir, "request-journal"), requestjournal.Config{
		Span:    time.Duration(api.GatewayRequestJournalSegmentMinutes) * time.Minute,
		Grace:   time.Duration(api.GatewayRequestJournalSegmentGraceSeconds) * time.Second,
		Budget:  api.GatewayRequestJournalDiskBudgetBytes,
		OnEvict: metrics.AddRequestIDJournalEvicted,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("gatewayd: open node-local request ID journal: %w", err)
	}
	if node == "" {
		node = state.DefaultLocalNodeName
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		ticker := time.NewTicker(requestIDJournalSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				if err := store.Sweep(); err != nil {
					log.Warn("request ID journal: sweep failed", "err", err)
				}
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		if client == nil {
			log.Warn("request ID journal: apid client unavailable; this node's request IDs cannot be looked up")
			return
		}
		gateway.ServeRequestIDLookups(runCtx, client.RequestIDLookups(), node, store, log)
	}()
	stop := func() {
		cancel()
		<-done
		<-done
		if err := store.Close(); err != nil {
			log.Warn("request ID journal: close", "err", err)
		}
	}
	return store, stop, nil
}
