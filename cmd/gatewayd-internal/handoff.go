package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/drain"
	"github.com/onebox-faas/faas/pkg/gateway/egressgrpc"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
	"github.com/onebox-faas/faas/pkg/genslot"
	"github.com/onebox-faas/faas/pkg/usageoutbox"
)

// Two-generation hand-off (docs/adr/20261018074719-zero-downtime-daemon-restarts.md).
// On `systemctl reload` this process starts its successor on the same
// sockets and, once the successor serves, finishes its own requests. The
// durable queues each generation writes live in per-generation slots
// (pkg/genslot), so the two never write the same file.

// returnHomeWait bounds how long an exiting generation waits for the root
// slot of a queue to free up, so a release that predates slots finds its
// records where it looks. A stopping unit stops every generation at once;
// one still draining in the root slot holds it for up to the restart grace.
const returnHomeWait = 5 * time.Second

// generationStores are the durable queues this generation owns.
type generationStores struct {
	log *slog.Logger

	mu         sync.Mutex
	egressSlot *genslot.Slot
	egressName string
	egress     *egressgrpc.Server
	usageSlot  *genslot.Slot
	usage      *usageoutbox.Outbox
	handedOff  bool
}

// openEgress claims a slot for the egress replay ledger at path.
func (g *generationStores) openEgress(sink *egresssink.EgressSink, path string) (*egressgrpc.Server, error) {
	slot, err := genslot.Acquire(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	srv, err := egressgrpc.NewPersistentServer(sink, g.log, filepath.Join(slot.Dir(), filepath.Base(path)))
	if err != nil {
		_ = slot.Release()
		return nil, err
	}
	g.mu.Lock()
	g.egressSlot, g.egressName, g.egress = slot, filepath.Base(path), srv
	g.mu.Unlock()
	return srv, nil
}

// openUsage claims a slot for the consumer usage outbox under root.
func (g *generationStores) openUsage(root string) (*usageoutbox.Outbox, error) {
	slot, err := genslot.Acquire(root)
	if err != nil {
		return nil, err
	}
	q, err := usageoutbox.Open(slot.Dir(), usageoutbox.DefaultMaxBytes)
	if err != nil {
		_ = slot.Release()
		return nil, err
	}
	g.mu.Lock()
	g.usageSlot, g.usage = slot, q
	g.mu.Unlock()
	return q, nil
}

// adopt moves records an exited generation left in its slots into this
// generation's queues.
func (g *generationStores) adopt() {
	g.mu.Lock()
	egressSlot, egressName, egress := g.egressSlot, g.egressName, g.egress
	usageSlot, usage := g.usageSlot, g.usage
	g.mu.Unlock()
	if egressSlot != nil {
		err := egressSlot.AdoptOrphans(func(dir string) error {
			n, err := egress.Absorb(filepath.Join(dir, egressName))
			if n > 0 {
				g.log.Info("gatewayd-internal: adopted an exited generation's egress records", "records", n, "dir", dir)
			}
			return err
		})
		if err != nil {
			g.log.Warn("gatewayd-internal: adopt egress records; retrying", "err", err)
		}
	}
	if usageSlot != nil {
		err := usageSlot.AdoptOrphans(func(dir string) error {
			n, err := usage.Absorb(dir)
			if n > 0 {
				g.log.Info("gatewayd-internal: adopted an exited generation's usage events", "events", n, "dir", dir)
			}
			return err
		})
		if err != nil {
			g.log.Warn("gatewayd-internal: adopt usage events; retrying", "err", err)
		}
	}
}

// adoptLoop adopts at start and then every genslot.AdoptInterval until ctx
// ends, which a hand-off does: the successor adopts from then on.
func (g *generationStores) adoptLoop(ctx context.Context) {
	t := time.NewTicker(genslot.AdoptInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.adopt()
		}
	}
}

func (g *generationStores) markHandedOff() {
	g.mu.Lock()
	g.handedOff = true
	g.mu.Unlock()
}

// close keeps the egress records still in memory and moves this
// generation's queues to the root slot. A handed-off generation does not
// wait for it: its successor holds the root slot or adopts these records.
// Callers stop every writer and reader of the queues first.
func (g *generationStores) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	wait := returnHomeWait
	if g.handedOff {
		wait = 0
	}
	if g.egress != nil {
		if err := g.egress.Flush(); err != nil {
			g.log.Warn("gatewayd-internal: flush egress records at exit", "err", err)
		}
	}
	if g.egressSlot != nil {
		name := g.egressName
		err := g.egressSlot.ReturnHome(wait, func(from, home string) error {
			return egressgrpc.MergeLedger(filepath.Join(from, name), filepath.Join(home, name), g.log)
		})
		if err != nil {
			g.log.Warn("gatewayd-internal: egress records stay for the next generation", "err", err)
		}
	}
	if g.usageSlot != nil {
		err := g.usageSlot.ReturnHome(wait, func(from, home string) error {
			q, err := usageoutbox.Open(home, usageoutbox.DefaultMaxBytes)
			if err != nil {
				return err
			}
			_, err = q.Absorb(from)
			return err
		})
		if err != nil {
			g.log.Warn("gatewayd-internal: usage events stay for the next generation", "err", err)
		}
	}
}

// drainOldGeneration finishes this generation after a hand-off. Its
// listeners close at once (the successor accepts on the same sockets) and
// its requests run for up to api.HandoffDrainMaxSeconds. SSE and WebSocket
// sessions end one restart grace before that bound so their clients
// reconnect to the successor; a SIGTERM (ctx) ends them at once and allows
// one more restart grace.
func drainOldGeneration(ctx context.Context, log *slog.Logger, servers []drainedServer, dnsServers []*dns.Server, sessionDrains []func(context.Context) error, deps runDeps) error {
	maxDrain := time.Duration(api.HandoffDrainMaxSeconds) * time.Second
	// The drain outlives ctx: a SIGTERM shortens it to one grace instead of
	// cutting it.
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), maxDrain)
	defer cancel()
	var endOnce sync.Once
	endStreams := func() {
		endOnce.Do(func() {
			if deps.drain != nil {
				deps.drain.BeginShutdown()
			}
		})
	}
	streamsTimer := time.AfterFunc(maxDrain-drain.DrainGrace, endStreams)
	defer streamsTimer.Stop()
	go func() {
		select {
		case <-ctx.Done():
			log.Info("gatewayd-internal: old generation asked to stop; ending its streams")
			endStreams()
			time.AfterFunc(drain.DrainGrace, cancel)
		case <-drainCtx.Done():
		}
	}()

	start := time.Now()
	if deps.egressGRPC != nil {
		// meterd reconnects to the successor; records this generation still
		// meters reach meterd through the ledger the successor adopts.
		stopCtx, stopCancel := context.WithTimeout(drainCtx, 5*time.Second)
		_ = deps.egressGRPC.stop(stopCtx)
		stopCancel()
	}
	var sessions sync.WaitGroup
	for _, drainSessions := range sessionDrains {
		sessions.Go(func() { _ = drainSessions(drainCtx) })
	}
	shutdownListeners(drainCtx, servers, dnsServers, deps.synth)
	sessions.Wait()
	var drainErr error
	if deps.drain != nil {
		outcome, err := deps.drain.Drain(drainCtx, maxDrain)
		deps.metrics.ObserveDrainWait("gatewayd-internal", string(outcome), time.Since(start).Seconds())
		switch {
		case err != nil:
			drainErr = err
		case outcome != drain.OutcomeClean:
			drainErr = fmt.Errorf("gatewayd-internal: old generation drain %s", outcome)
		}
	}
	// The vmmd connections carry the streams just drained; close them last.
	if deps.nodeCache != nil {
		_ = deps.nodeCache.Close()
	}
	if drainErr != nil {
		log.Warn("gatewayd-internal: old generation drain exited non-clean", "err", drainErr, "elapsed", time.Since(start))
		return drainErr
	}
	log.Info("gatewayd-internal: old generation drained", "elapsed", time.Since(start))
	return nil
}
