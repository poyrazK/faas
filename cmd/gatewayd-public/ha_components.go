// Top-level wiring for Tier A8 / ADR-083 components
// (code-review fix #2 + #6).
//
// Two goroutines launched from main.go's run():
//
//  1. DNSHandoff subscriber: subscribes to the
//     compute_node_changed pg_notify channel; on every
//     notification, re-elects the leader via leader.ElectLeader
//     and either UpsertRecord (we won) or runs DNSHandoff.Run
//     (we lost). Defined in cmd/gatewayd-public/dns_handoff.go.
//
//  2. StandbyWarmup loop: probes the local public listener
//     on every tick; errors bump a Prometheus counter.
//     Defined in cmd/gatewayd-public/standby_warmup.go.
//
// Both return nil on ctx cancel. The wire.Daemon harness waits
// for ctx cancel; both goroutines exit on cancel before main.go
// returns so the pg_notify LISTEN socket isn't leaked.
//
// Gating:
//   - FAAS_DNS_PROVIDER unset → DNSHandoff is not wired
//     (single-box dev path).
//   - FAAS_NODE_NAME unset → DNSHandoff subscriber still runs
//     but never elects THIS node as leader (warmup still runs;
//     useful for testing the warmup loop without a second node).
//   - FAAS_STANDBY_WARMUP_ENABLED=false → WarmupLoop is skipped
//     (default true).

package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// startHAComponents spins up the DNSHandoff subscriber and the
// StandbyWarmup loop. Returns nil on ctx cancel; non-fatal
// warnings log on DNS provider init failure (the runbook covers
// the manual failover path; the orchestrator's dns_stale path
// pages on real failure).
//
// The two goroutines are tracked in a WaitGroup so leakcheck
// can confirm clean exit on SIGTERM.
func startHAComponents(
	ctx context.Context,
	log *slog.Logger,
	pool *pgxpool.Pool,
	pgStore *state.PgStore,
	inflight *gateway.ConnStateTracker,
	nodeName, nodeIP string,
	metrics ...*wire.OpsMetrics,
) error {
	var wg sync.WaitGroup
	// Reuse the registry mounted by gatewayd-public's control mux. A second
	// registry here made HA state invisible because the mux only served the
	// main process registry. The variadic form keeps old test seams source
	// compatible; production always passes the served registry.
	var ops *wire.OpsMetrics
	if len(metrics) > 0 {
		ops = metrics[0]
	}
	if ops == nil {
		ops = wire.NewOpsMetrics("gatewayd_public")
		wire.BootStamps(ctx, "gatewayd-public", ops)
		wire.RegisterDefaultOps(ops)
	}

	// DNSHandoff. Gated on FAAS_DNS_PROVIDER (no provider → no
	// DNS to flip → subscriber is a no-op).
	if prov := envOr("FAAS_DNS_PROVIDER", ""); prov == "" {
		log.Info("gatewayd-public: FAAS_DNS_PROVIDER unset; DNSHandoff subscriber disabled")
	} else {
		sealedToken := []byte(envOr("FAAS_DNS_PROVIDER_SEALED", ""))
		dns, dErr := gateway.NewDNSProvider(gateway.DNSProviderConfig{
			Zone:        envOr("FAAS_DNS_ZONE", ""),
			SealedToken: sealedToken,
			APIURL:      envOr("FAAS_DNS_API_URL", ""),
		}, prov)
		if dErr != nil {
			log.Warn("gatewayd-public: DNS provider init failed; manual failover only",
				"provider", prov, "err", dErr.Error())
		} else {
			handoff := &gateway.DNSHandoff{
				NodeName:    nodeName,
				DNSProvider: dns,
				InFlight:    inflight,
				Metrics:     ops,
				LeaderStore: newLeaderStoreAdapter(pgStore),
				Now:         time.Now,
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				wiring := &dnsHandoffWiring{
					LeaderStore: newLeaderStoreAdapter(pgStore),
					DNSProvider: dns,
					Handoff:     handoff,
					NodeName:    nodeName,
					NodeIP:      nodeIP,
				}
				if err := runDNSHandoff(ctx, log, pool, wiring, ops); err != nil {
					log.Warn("gatewayd-public: dns handoff loop exited with error",
						"err", err.Error())
				}
			}()
		}
	}

	// StandbyWarmup. Gated on FAAS_STANDBY_WARMUP_ENABLED inside
	// runStandbyWarmup (default true).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := runStandbyWarmup(ctx, log, ops); err != nil {
			log.Warn("gatewayd-public: standby warmup loop exited with error",
				"err", err.Error())
		}
	}()

	// Watcher: when the wire.Daemon harness cancels ctx, wait
	// for both goroutines to exit so the pg_notify LISTEN socket
	// isn't leaked. Returned WaitGroup waits up to leakcheck's
	// grace; a stuck goroutine surfaces as a separate warning.
	go func() {
		<-ctx.Done()
		wg.Wait()
	}()
	return nil
}
