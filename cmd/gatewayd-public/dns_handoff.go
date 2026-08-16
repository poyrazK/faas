// DNSHandoff wiring for cmd/gatewayd-public (Tier A8 / ADR-083 /
// code-review fix #2).
//
// Subscribes to the `compute_nodes_changed` pg_notify channel.
// On every notification:
//
//  1. Re-elect the leader via leader.ElectLeader (lex-min over
//     active compute_nodes).
//  2. If THIS node is the new leader, call DNSProvider.UpsertRecord
//     so the public A-record points at the new leader's egress IP.
//  3. If THIS node was the previous leader and lost the election,
//     call DNSHandoff.Run — wait for in-flight to reach zero,
//     then delete our A-record.
//  4. Otherwise (we're a non-leader standby), no DNS change —
//     warmup keeps the cache fresh.
//
// The pg_notify listener uses db.SubscribeWithReconnect so a
// Postgres restart doesn't kill the daemon (precedent:
// pkg/sched/loop.go:318). The drain protocol's stop semantics
// live in DNSHandoff.Run (pkg/gateway/dns_handoff.go:169); the
// only logic in this file is the routing of pg_notify events
// to either UpsertRecord (becoming leader) or Run (losing
// leadership).

package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/leader"
	"github.com/onebox-faas/faas/pkg/wire"
)

// runDNSHandoff subscribes to compute_nodes_changed and routes
// every notification to the right DNSHandoff.Run / UpsertRecord
// call. Blocks until ctx is cancelled.
func runDNSHandoff(
	ctx context.Context,
	log *slog.Logger,
	pool *pgxpool.Pool,
	wiring *dnsHandoffWiring,
	ops *wire.OpsMetrics,
) error {
	notif, err := db.SubscribeWithReconnect(ctx, pool, []string{
		db.NotifyComputeNodesChanged,
	}, log)
	if err != nil {
		return fmt.Errorf("gatewayd-public: subscribe compute_nodes_changed: %w", err)
	}
	// lastLeaderName is the cached current leader. Empty on
	// first boot. We compare against the freshly-elected
	// leader and only fire DNS actions on a CHANGE — a flap
	// during a partial network partition can fire 5+ events
	// for the same logical transition.
	lastLeaderName := ""

	for {
		select {
		case <-ctx.Done():
			return nil
		case n, ok := <-notif:
			if !ok {
				// db.SubscribeWithReconnect never closes
				// the channel on a transient conn drop —
				// this branch only fires on ctx cancel.
				return nil
			}
			log.Info("gatewayd-public: compute_nodes_changed received",
				"channel", n.Channel,
				"last_leader", lastLeaderName,
			)
			// Re-elect. leader.ElectLeader is pure (no IO),
			// so we don't actually need the payload of the
			// pg_notify — we re-read the live compute_nodes
			// via the leaderStore.
			newLeader, err := leader.ElectLeader(ctx, wiring.LeaderStore)
			if err != nil {
				log.Warn("gatewayd-public: elect leader failed", "err", err.Error())
				continue
			}
			if newLeader.Name == lastLeaderName {
				continue
			}
			prev := lastLeaderName
			lastLeaderName = newLeader.Name

			switch {
			case newLeader.Name == wiring.NodeName:
				// We won the election — make sure DNS
				// points at us. UpsertRecord is
				// idempotent (no-op if record already
				// matches).
				if err := wiring.DNSProvider.UpsertRecord(ctx, newLeader.Name, wiring.NodeIP); err != nil {
					log.Warn("gatewayd-public: upsert on election win failed",
						"err", err.Error())
					ops.ActivePassiveFailovers("dns_stale").Inc()
				}
			case prev == wiring.NodeName:
				// We lost the election (we were the
				// previous leader). Drain — wait for
				// in-flight to reach zero, then delete
				// our A-record.
				out := wiring.Handoff.Run(ctx)
				log.Info("gatewayd-public: drain complete",
					"outcome", string(out),
					"node", wiring.NodeName,
				)
			default:
				// Neither winner nor loser — standby
				// stays on its current slugs.
				log.Info("gatewayd-public: standby, no DNS action",
					"new_leader", newLeader.Name,
					"prev_leader", prev,
				)
			}
		}
	}
}

// dnsHandoffWiring is the read-only bundle runDNSHandoff needs.
// Constructed in main.go after the pgStore, DNSProvider, and
// DNSHandoff are built. Held by pointer (passed by pointer in
// runDNSHandoff) so a future SIGHUP-driven reload can swap
// fields without restarting the goroutine.
type dnsHandoffWiring struct {
	LeaderStore leader.LeaderStore
	DNSProvider gateway.DNSProvider
	Handoff     *gateway.DNSHandoff
	NodeName    string
	NodeIP      string
}
