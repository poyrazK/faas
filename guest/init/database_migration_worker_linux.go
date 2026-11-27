//go:build linux

package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
)

// migrationFeed is an immutable snapshot used by the local subscriber
// socket. Maintenance queries use a separate socket so admission can read
// the source and target before PostgreSQL opens a new replication stream.
type migrationFeed struct {
	cleanup        bool
	cleanupDurable string
	receipt        liveMigration
	source         dbguest.ImportSource
	lost           *atomic.Bool
}

func (g *databaseGuest) offerMigrationFeed(m *liveMigration, src dbguest.ImportSource) {
	lost := new(atomic.Bool)
	if previous := g.migrationFeed.Load(); previous != nil {
		lost = previous.lost
	}
	g.migrationFeed.Store(&migrationFeed{receipt: *m, source: src, lost: lost})
}

func (g *databaseGuest) admitMigrationFeed() bool {
	if !g.migrationConnectMu.TryLock() {
		return false
	}
	defer g.migrationConnectMu.Unlock()
	feed := g.migrationFeed.Load()
	if feed == nil || feed.lost.Load() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	m, src := &feed.receipt, feed.source
	system, err := g.migrationSourceIdentity(ctx, src)
	if err != nil {
		return false
	}
	if system != m.System {
		feed.lost.Store(true)
		return false
	}
	slot, err := g.migrationSlot(ctx, m, src)
	if err != nil {
		return false
	}
	if feed.cleanup {
		return g.migrationFeed.Load() == feed &&
			(!slot.Exists || dbmigration.IsContinuous(slot, src.Database, m.Floor, m.Confirmed, feed.cleanupDurable))
	}
	if slot.Active && slot.Application != m.resource() {
		return false
	}
	sub, err := g.migrationSubscription(ctx, m)
	if err != nil {
		return false
	}
	witness := sub.Durable
	if slot.Sender != "" && slot.Sender == m.Sender && slot.Application == m.resource() && sub.Applying {
		witness = slot.Confirmed
	}
	if !dbmigration.IsContinuous(slot, src.Database, m.Floor, m.Confirmed, witness) {
		// The subscriber has not started a new stream. If an existing apply
		// session raced this sample, retry instead of accepting its advance.
		if !slot.Active {
			feed.lost.Store(true)
		}
		return false
	}
	current := g.migrationFeed.Load()
	return current != nil && current.lost == feed.lost && !current.lost.Load()
}

func subscriberMigrationDSN(src dbguest.ImportSource, m *liveMigration) string {
	dsn := strings.Replace(migrationDSN(src), migrationSocketDir, migrationFeedSocketDir, 1)
	return dsn + " passfile=" + migrationPassfile + " application_name=" + m.resource()
}

type migrationSubscription struct {
	Exists, Enabled, Applying bool
	MarkerReady               bool `json:"marker_ready"`
	Ready, Tables             int
	Durable                   string
}

func (g *databaseGuest) migrationSubscription(ctx context.Context, m *liveMigration) (migrationSubscription, error) {
	marker := ""
	if m.Verification != nil {
		marker = m.verificationMarker().Name
	}
	raw, err := g.queryIn(ctx, m.Target, dbmigration.SubscriptionWithMarkerSQL(m.resource(), marker))
	var sub migrationSubscription
	if err == nil {
		err = json.Unmarshal([]byte(raw), &sub)
	}
	return sub, err
}

func (g *databaseGuest) runLiveMigration(m *liveMigration, src dbguest.ImportSource, generation uint64) {
	for {
		g.migrationMu.Lock()
		if !m.running || generation != g.migrationGeneration || g.currentState() != dbguest.StateReady {
			g.migrationMu.Unlock()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		reason := g.stepLiveMigration(ctx, m, src)
		if m.State == dbmigration.ResyncRequired {
			m.running = false
			g.migrationRunning.Store(false)
		} else if reason != "" {
			m.State, m.Reason = dbmigration.Paused, reason
		}
		cancel()
		if reason == "" && m.Verification != nil && m.Verification.State == dbmigration.Verifying && g.migrationPrepareCancel == nil {
			verify, cancel := context.WithTimeout(context.Background(), 22*time.Second)
			g.advanceMigrationVerification(verify, m, src)
			cancel()
		}
		g.publishMigrationProgress(m)
		running := m.running
		g.migrationMu.Unlock()
		if !running {
			return
		}
		select {
		case <-g.done:
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (g *databaseGuest) stepLiveMigration(ctx context.Context, m *liveMigration, src dbguest.ImportSource) string {
	if feed := g.migrationFeed.Load(); feed != nil && feed.lost.Load() {
		g.invalidateMigration(ctx, m, "source_slot_continuity_lost")
		return m.Reason
	}
	if storage := g.storageStatus(); storage != nil && (storage.ReadOnly || storage.Low) {
		g.cancelMigrationPreparation()
		g.migrationFeed.Store(nil)
		_ = g.setMigrationSubscription(ctx, m, false)
		return "target_storage_low"
	}
	system, err := g.migrationSourceIdentity(ctx, src)
	if err != nil {
		return "source_unreachable"
	}
	if system != m.System {
		g.invalidateMigration(ctx, m, "source_identity_changed")
		return m.Reason
	}
	tables, err := g.migrationInventory(ctx, src, m)
	if err != nil {
		return "source_inspection_unavailable"
	}
	if dbmigration.SchemaDigest(tables) != m.Schema {
		g.invalidateMigration(ctx, m, "source_schema_changed")
		return m.Reason
	}
	if m.Floor == "" && g.migrationSchema(ctx, m, tables) != nil {
		return "target_schema_unavailable"
	}
	if !g.ensureMigrationPublication(ctx, m, src, tables) {
		if m.SlotIntent && ctx.Err() == nil {
			// A transport failure is retried before treating an observed
			// publication change as a loss of continuity.
			if _, err := g.migrationPublication(ctx, m, src); err == nil {
				g.invalidateMigration(ctx, m, "source_publication_changed")
			}
		}
		return "source_publication_unavailable"
	}
	slot, err := g.migrationSlot(ctx, m, src)
	if err != nil {
		return "source_inspection_unavailable"
	}
	if m.Floor == "" {
		if reason := g.initializeMigrationSlot(ctx, m, src, slot); reason != "" {
			return reason
		}
		slot, err = g.migrationSlot(ctx, m, src)
		if err != nil {
			return "source_inspection_unavailable"
		}
	}
	sub, err := g.migrationSubscription(ctx, m)
	if err != nil {
		return "target_subscription_unavailable"
	}
	// Every new subscriber stream passes the socket's continuity check
	// before applying. An attached, verified subscription can then safely
	// acknowledge WAL without changes for this publication.
	witness := sub.Durable
	if feed := g.migrationFeed.Load(); feed != nil && !feed.lost.Load() &&
		slot.Sender != "" && slot.Application == m.resource() && sub.Applying {
		witness = slot.Confirmed
	}
	if !dbmigration.IsContinuous(slot, src.Database, m.Floor, m.Confirmed, witness) {
		g.invalidateMigration(ctx, m, "source_slot_continuity_lost")
		return m.Reason
	}
	g.offerMigrationFeed(m, src)
	if !sub.Exists {
		if m.Sender != "" || m.TablesReady != 0 {
			g.invalidateMigration(ctx, m, "target_subscription_lost")
			return m.Reason
		}
		if _, err := g.queryIn(ctx, m.Target, dbmigration.CreateSubscriptionSQL(m.resource(), subscriberMigrationDSN(src, m))); err != nil {
			return "target_subscription_unavailable"
		}
	}
	if sub.Exists && sub.Tables != m.Tables {
		g.invalidateMigration(ctx, m, "target_subscription_changed")
		return m.Reason
	}
	if !sub.Enabled {
		if slot.Active || g.setMigrationSubscription(ctx, m, true) != nil {
			return "target_subscription_unavailable"
		}
	}
	p := m.Progress
	p.TablesReady, p.RetainedWALBytes = sub.Ready, slot.Retained
	p.ApplyLagBytes = dbmigration.BytesBehind(slot.Current, slot.Confirmed)
	p.State, p.Reason = dbmigration.Copying, ""
	switch {
	case sub.Exists && (!sub.Applying || !slot.Active || slot.Application != m.resource()):
		p.State, p.Reason = dbmigration.Paused, "subscriber_connecting"
	case sub.Ready == m.Tables && p.ApplyLagBytes > 0:
		p.State = dbmigration.CatchingUp
	case sub.Ready == m.Tables:
		p.State = dbmigration.Live
	}
	next := *m
	next.Progress, next.Confirmed = p, slot.Confirmed
	if slot.Sender != "" && slot.Application == m.resource() && sub.Applying {
		next.Sender = slot.Sender
	}
	if next.Progress != m.Progress || next.Confirmed != m.Confirmed || next.Sender != m.Sender {
		if err := g.writeMigration(ctx, &next, false); err != nil {
			return "migration_receipt_unavailable"
		}
		*m = next
		g.offerMigrationFeed(m, src)
	}
	return ""
}
